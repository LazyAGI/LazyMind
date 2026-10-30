package academic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
)

var errUnregisteredExpansionPaper = errors.New("expansion paper is not a saved seed or verified external candidate")

// A source is frozen by the server, never supplied by the model. DatasetID is
// the expansion's boundary even when the actual bytes come from the Internet.
type paperSource struct {
	Paper      PaperRef            `json:"paper"`
	DatasetID  string              `json:"dataset_id"`
	DocumentID string              `json:"document_id,omitempty"`
	Hash       string              `json:"hash,omitempty"`
	Revision   int64               `json:"revision,omitempty"`
	IsSeed     bool                `json:"is_seed"`
	Candidates []FulltextCandidate `json:"candidates,omitempty"`
}

type paperDiscovery struct {
	PreparationRevision int64                          `json:"preparation_revision,omitempty"`
	CacheKey            string                         `json:"cache_key,omitempty"`
	MaxResults          int                            `json:"max_results,omitempty"`
	Position            searchPosition                 `json:"position,omitempty"`
	Next                *searchPosition                `json:"next,omitempty"`
	Sources             []searchOutcome                `json:"sources,omitempty"`
	Leads               []discoveryLead                `json:"leads,omitempty"`
	Candidates          map[string][]FulltextCandidate `json:"candidates,omitempty"`
	Mode                string                         `json:"mode,omitempty"`
	PaperID             string                         `json:"paper_id,omitempty"`
	SessionID           string                         `json:"session_id"`
	Query               string                         `json:"query"`
	CreatedAt           time.Time                      `json:"created_at"`
	Papers              []PaperRef                     `json:"papers"`
}

func (s paperSource) key() string {
	identity := []string{s.Paper.SourceProvider, s.Paper.SourceVersion, s.Paper.PDFURL}
	if s.DocumentID != "" {
		identity = []string{s.DatasetID, s.DocumentID, s.Hash, s.Paper.SourceVersion, string(marshal(s.Revision))}
	}
	hash := sha256.Sum256(marshal(identity))
	return hex.EncodeToString(hash[:])
}

// Download locations may change, but a frozen paper revision must not.
func (s paperSource) matchingCandidate(candidate FulltextCandidate) (FulltextCandidate, bool) {
	if candidate.URL == "" {
		return candidate, false
	}
	if s.Paper.SourceProvider == "arxiv" && s.Paper.SourceVersion != "" {
		id, err := arxivsource.Parse(candidate.URL)
		if err != nil || officialArxivID(candidate.URL) != s.Paper.ArxivID ||
			(id.Version != "" && id.Version != s.Paper.SourceVersion) ||
			(candidate.SourceProvider == "arxiv" && candidate.Version != "" && candidate.Version != s.Paper.SourceVersion) {
			return candidate, false
		}
		// An unversioned official URL can be pinned without treating an index's
		// version label (such as submittedVersion) as an arXiv revision.
		id.Version = s.Paper.SourceVersion
		candidate.URL, candidate.Version, candidate.SourceProvider = id.PDFURL(), id.Version, "arxiv"
		candidate.SourceType, candidate.ExpectedMIME = "official_pdf", "application/pdf"
		return candidate, true
	}
	return candidate, s.Paper.SourceVersion == "" ||
		(candidate.SourceProvider == s.Paper.SourceProvider && candidate.Version == s.Paper.SourceVersion)
}

func (s paperSource) fulltextCandidates() []FulltextCandidate {
	primary := FulltextCandidate{URL: s.Paper.PDFURL, SourceProvider: s.Paper.SourceProvider, Version: s.Paper.SourceVersion, SourceType: "open_access_pdf", ExpectedMIME: "application/pdf"}
	for _, candidate := range s.Candidates {
		if candidate.URL == primary.URL && candidate.SourceProvider == primary.SourceProvider && candidate.Version == primary.Version {
			primary = candidate
			break
		}
	}
	result := []FulltextCandidate{}
	seen := map[string]bool{}
	for _, candidate := range append([]FulltextCandidate{primary}, s.Candidates...) {
		candidate, ok := s.matchingCandidate(candidate)
		if ok && !seen[candidate.URL] {
			result = append(result, candidate)
			seen[candidate.URL] = true
		}
	}
	return result
}

func seedSources(db *gorm.DB, dataset string, papers []WorkflowPaper) ([]paperSource, error) {
	result := make([]paperSource, 0, len(papers))
	for _, item := range papers {
		var state orm.AcademicDocumentState
		query := db.Where("dataset_id=? AND paper_id=? AND document_id IN (?)", dataset, item.Paper.PaperID, db.Model(&orm.Document{}).Select("id").Where("dataset_id=? AND deleted_at IS NULL", dataset))
		if item.Paper.SourceDocumentID != "" {
			query = query.Where("document_id=?", item.Paper.SourceDocumentID)
		}
		if err := query.Order("manual DESC, updated_at DESC").Take(&state).Error; err != nil {
			return nil, err
		}

		source, err := bindLibraryDocument(db, paperSource{Paper: item.Paper, DatasetID: dataset, IsSeed: true}, state)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, nil
}

// Seeds and discovered papers must identify the same local PDF and document revision.
func bindLibraryDocument(db *gorm.DB, source paperSource, state orm.AcademicDocumentState) (paperSource, error) {
	source.DocumentID, source.Revision = state.DocumentID, state.Revision
	var link orm.AcademicWorkDocument
	if err := db.Where("dataset_id=? AND document_id=? AND academic_work_id=?", source.DatasetID, state.DocumentID, source.Paper.PaperID).Take(&link).Error; err == nil {
		source.Paper.SourceVersion, source.Paper.SourceProvider = link.SourceVersion, link.SourceProvider
		source.Hash = link.ContentSHA256
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return source, err
	}
	var document orm.Document
	if err := db.Where("id=? AND dataset_id=? AND deleted_at IS NULL", state.DocumentID, source.DatasetID).Take(&document).Error; err != nil {
		return source, err
	}
	if hash := documentFileHash(document); hash != "" {
		source.Hash = hash
	} else if source.Paper.SourceProvider != "arxiv" {
		source.Hash = ""
	}
	pinPaperURLs(&source.Paper)
	return source, nil
}

func pinPaperURLs(p *PaperRef) {
	if p.SourceProvider == "arxiv" && p.ArxivID != "" && p.SourceVersion != "" {
		if id, err := arxivsource.Parse(p.ArxivID + p.SourceVersion); err == nil && id.Version == p.SourceVersion {
			p.PaperURL, p.PDFURL = id.AbsURL(), id.PDFURL()
		}
	}
}

func completedExpansionInput(db *gorm.DB, record orm.AcademicExpansion, version int64) (ReviewWorkflowInput, error) {
	var input ReviewWorkflowInput
	if version <= 0 || version > record.Version {
		return input, errors.New("所选扩展版本尚未完成")
	}
	snapshot := record.ResultJSON
	if version != record.Version {
		var round orm.AcademicExpansionRound
		if err := db.Where("expansion_id=? AND version=? AND status='completed'", record.ID, version).Take(&round).Error; err != nil {
			return input, err
		}
		snapshot = round.ResultJSON
	}
	err := json.Unmarshal(snapshot, &input)
	return input, err
}

func expansionSource(db *gorm.DB, record orm.AcademicExpansion, paperID string) (paperSource, error) {
	var seeds []paperSource
	if err := json.Unmarshal(record.SeedSnapshotJSON, &seeds); err != nil {
		return paperSource{}, errors.New("seed snapshot is unavailable")
	}
	for _, source := range seeds {
		if source.Paper.PaperID == paperID {
			if _, err := sourceDocument(db.Statement.Context, db, source); err != nil {
				return paperSource{}, err
			}
			return source, nil
		}
	}
	var discoveries []paperDiscovery
	if err := json.Unmarshal(record.DiscoveryJSON, &discoveries); err != nil {
		return paperSource{}, err
	}
	for _, search := range discoveries {
		for _, paper := range search.Papers {
			if paper.PaperID != paperID {
				continue
			}
			copies := []FulltextCandidate{}
			for _, discovery := range discoveries {
				copies = append(copies, discovery.Candidates[paperID]...)
			}
			source := paperSource{Paper: paper, DatasetID: record.DatasetID, Candidates: copies}
			return matchingLibrarySource(db, source)
		}
	}
	return paperSource{}, errors.New("paper must be a saved seed or an externally discovered candidate of this expansion")
}

func findSourceContent(db *gorm.DB, user string, source paperSource) (orm.AcademicPaperContent, error) {
	var rows []orm.AcademicPaperContent
	q := db.Where("user_id=? AND paper_id=? AND parser_version=? AND source_kind <> 'legacy'", user, source.Paper.PaperID, orm.AcademicPaperParserVersion)
	if source.Paper.SourceVersion != "" {
		q = q.Where("source_key=? OR (source_kind='external' AND source_version=? AND source_provider=?)", source.key(), source.Paper.SourceVersion, source.Paper.SourceProvider)
	} else {
		q = q.Where("source_key=?", source.key())
	}
	if err := q.Order("CASE WHEN status='ready' THEN 0 ELSE 1 END, updated_at DESC").Find(&rows).Error; err != nil {
		return orm.AcademicPaperContent{}, err
	}
	for _, row := range rows {
		if row.SourceKind != "external" {
			if _, e := sourceDocument(db.Statement.Context, db, source); e != nil {
				continue
			}
		}
		if row.SourceKind == "external" || (row.SourceDatasetID == source.DatasetID && row.SourceDocumentID == source.DocumentID && (source.Hash == "" || row.SourceHash == source.Hash)) {
			return row, nil
		}
	}
	return orm.AcademicPaperContent{}, gorm.ErrRecordNotFound
}

func ownedExpansion(w http.ResponseWriter, r *http.Request, user, id string) (orm.AcademicExpansion, bool) {
	var record orm.AcademicExpansion
	if id == "" {
		replyError(w, 400, "INVALID_REQUEST", "expansion_id is required")
		return record, false
	}
	if store.DB().Where("id=? AND user_id=? AND deleted_at IS NULL", id, user).Take(&record).Error != nil {
		replyError(w, 404, "NOT_FOUND", "expansion not found")
		return record, false
	}
	if !academicDataset(r, user, record.DatasetID) {
		replyError(w, 403, "FORBIDDEN", "knowledge base unavailable")
		return record, false
	}
	return record, true
}

func SearchPapers(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req paperSearchRequest
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid search")
		return
	}
	record, ok := ownedExpansion(w, r, user, req.ExpansionID)
	if !ok {
		return
	}
	if req.PreparationRevision != nil && (record.PreparationRevision != *req.PreparationRevision || record.Status != "preparing") {
		replyError(w, 409, "VERSION_CHANGED", "preparation attempt is no longer current")
		return
	}

	var previous []paperDiscovery
	if err := json.Unmarshal(record.DiscoveryJSON, &previous); err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	if req.Cursor != "" {
		if err := resumeSearch(record, &req, previous); err != nil {
			replyError(w, 400, "INVALID_CURSOR", err.Error())
			return
		}
	}
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}
	if req.MaxResults < 1 || req.MaxResults > 20 || len(req.Query) > 2000 {
		replyError(w, 400, "INVALID_REQUEST", "max_results must be 1..20 and query bounded")
		return
	}

	if req.Mode == "" {
		req.Mode = "keyword"
	}
	if req.Mode != "keyword" && req.Mode != "references" && req.Mode != "citations" {
		replyError(w, 400, "INVALID_REQUEST", "unsupported search mode")
		return
	}
	if req.Mode == "keyword" && strings.TrimSpace(req.Query) == "" {
		replyError(w, 400, "INVALID_REQUEST", "query is required")
		return
	}
	if len(req.Sources) == 0 {
		if req.Mode != "keyword" {
			req.Sources = []string{"openalex"}
		} else {
			req.Sources = []string{"openalex", "arxiv"}
			config, e := modelconfig.LoadAcademicSearchToolConfig(r.Context(), store.DB(), user)
			if e != nil {
				replyError(w, 500, "CONFIG_UNAVAILABLE", e.Error())
				return
			}
			if config["sciverse"] != nil {
				req.Sources = append(req.Sources, "sciverse")
			}
		}
	}
	seenSources := map[string]bool{}
	for _, source := range req.Sources {
		if seenSources[source] || (source != "openalex" && source != "arxiv" && source != "sciverse" && source != "web") {
			replyError(w, 400, "INVALID_REQUEST", "unsupported or repeated source")
			return
		}
		seenSources[source] = true
	}
	if req.Mode != "keyword" && (req.PaperID == "" || len(req.Sources) != 1 || req.Sources[0] != "openalex") {
		replyError(w, 400, "INVALID_REQUEST", "references and citations require paper_id and only the openalex source")
		return
	}
	pages, cacheHit, err := fetchSearchPages(r.Context(), user, record, req, previous)
	if err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	result, err := saveSearchPages(r.Context(), user, record, req, pages)
	if err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	result.CacheHit = cacheHit
	common.ReplyJSON(w, result)

}

// Resolve only the attachment selected by a server-side source snapshot.
func sourceDocument(ctx context.Context, db *gorm.DB, source paperSource) (orm.Document, error) {
	var document orm.Document
	if source.DocumentID == "" {
		return document, gorm.ErrRecordNotFound
	}
	err := db.WithContext(ctx).Where("id=? AND dataset_id=? AND deleted_at IS NULL", source.DocumentID, source.DatasetID).Take(&document).Error
	if err != nil {
		return document, err
	}
	var state orm.AcademicDocumentState
	if err = db.Where("document_id=? AND dataset_id=? AND paper_id=?", source.DocumentID, source.DatasetID, source.Paper.PaperID).Take(&state).Error; err != nil {
		return document, err
	}
	if source.IsSeed && source.Revision > 0 && state.Revision != source.Revision {
		return document, errors.New("selected document identity changed; start a new task")
	}
	var link orm.AcademicWorkDocument
	if e := db.Where("document_id=? AND dataset_id=? AND academic_work_id=?", source.DocumentID, source.DatasetID, source.Paper.PaperID).Take(&link).Error; e == nil && link.SourceProvider == "arxiv" && source.Hash != "" && link.ContentSHA256 != "" && link.ContentSHA256 != source.Hash {
		return document, errors.New("selected PDF changed")
	}
	return document, nil
}

func matchingLibrarySource(db *gorm.DB, source paperSource) (paperSource, error) {
	var link orm.AcademicWorkDocument
	query := db.Where("academic_work_id=? AND dataset_id=? AND source_version=? AND source_provider=? AND document_id IN (?)", source.Paper.PaperID, source.DatasetID, source.Paper.SourceVersion, source.Paper.SourceProvider,
		db.Model(&orm.Document{}).Select("id").Where("dataset_id=? AND deleted_at IS NULL", source.DatasetID))
	if source.Paper.SourceVersion == "" {
		// An unversioned discovery can reuse only the exact registered location.
		query = query.Where("source_locator=?", source.Paper.PDFURL)
	}
	err := query.Order("is_preferred_version DESC, updated_at DESC").Take(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return source, nil
	}
	if err != nil {
		return source, err
	}
	var state orm.AcademicDocumentState
	if err = db.Where("document_id=? AND dataset_id=? AND paper_id=?", link.DocumentID, source.DatasetID, source.Paper.PaperID).Take(&state).Error; err != nil {
		return source, err
	}
	return bindLibraryDocument(db, source, state)
}

// Recognition fingerprints hash extracted text. Only bytes (or verified provider
// attachments) may serve as PDF hashes used by the parser and version cache.
func documentFileHash(document orm.Document) string {
	var ext struct {
		StoredPath string `json:"stored_path"`
	}
	if json.Unmarshal(document.Ext, &ext) != nil || ext.StoredPath == "" {
		return ""
	}
	path, err := filepath.EvalSymlinks(ext.StoredPath)
	if err != nil {
		return ""
	}
	root, err := filepath.Abs(doc.UploadRoot())
	if err != nil || !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}
