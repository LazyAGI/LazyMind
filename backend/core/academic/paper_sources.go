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
	"gorm.io/gorm/clause"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

var errUnregisteredExpansionPaper = errors.New("expansion paper is not a saved seed or verified external candidate")

// A source is frozen by the server, never supplied by the model. DatasetID is
// the expansion's boundary even when the actual bytes come from the Internet.
type paperSource struct {
	Paper      PaperRef `json:"paper"`
	DatasetID  string   `json:"dataset_id"`
	DocumentID string   `json:"document_id,omitempty"`
	Hash       string   `json:"hash,omitempty"`
	Revision   int64    `json:"revision,omitempty"`
	IsSeed     bool     `json:"is_seed"`
}

type paperDiscovery struct {
	SessionID string     `json:"session_id"`
	Query     string     `json:"query"`
	CreatedAt time.Time  `json:"created_at"`
	Papers    []PaperRef `json:"papers"`
}

func (s paperSource) key() string {
	identity := []string{s.Paper.SourceProvider, s.Paper.SourceVersion, s.Paper.PDFURL}
	if s.DocumentID != "" {
		identity = []string{s.DatasetID, s.DocumentID, s.Hash, s.Paper.SourceVersion, string(marshal(s.Revision))}
	}
	hash := sha256.Sum256(marshal(identity))
	return hex.EncodeToString(hash[:])
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

		source := paperSource{Paper: item.Paper, DatasetID: dataset, DocumentID: state.DocumentID, Hash: "", Revision: state.Revision, IsSeed: true}
		var link orm.AcademicWorkDocument
		if err := db.Where("dataset_id=? AND document_id=? AND academic_work_id=?", dataset, state.DocumentID, item.Paper.PaperID).Take(&link).Error; err == nil {
			source.Paper.SourceVersion, source.Paper.SourceProvider = link.SourceVersion, link.SourceProvider
			if link.ContentSHA256 != "" {
				source.Hash = link.ContentSHA256
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		var document orm.Document
		if err := db.Where("id=? AND dataset_id=?", state.DocumentID, dataset).Take(&document).Error; err != nil {
			return nil, err
		}
		if hash := documentFileHash(document); hash != "" {
			source.Hash = hash
		} else if source.Paper.SourceProvider != "arxiv" {
			source.Hash = ""
		}

		pinPaperURLs(&source.Paper)
		result = append(result, source)
	}
	return result, nil
}

func pinPaperURLs(p *PaperRef) {
	if p.ArxivID != "" && p.SourceVersion != "" {
		p.SourceProvider = "arxiv"
		p.PaperURL, p.PDFURL = "https://arxiv.org/abs/"+p.ArxivID+p.SourceVersion, "https://arxiv.org/pdf/"+p.ArxivID+p.SourceVersion
	}
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
			source := paperSource{Paper: paper, DatasetID: record.DatasetID}
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
	var req struct {
		ExpansionID string `json:"expansion_id"`
		Query       string `json:"query"`
		MaxResults  int    `json:"max_results"`
	}
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "invalid search")
		return
	}
	record, ok := ownedExpansion(w, r, user, req.ExpansionID)
	if !ok {
		return
	}
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}
	papers, err := arxivsource.NewClient().Search(r.Context(), strings.TrimSpace(req.Query), req.MaxResults)
	if err != nil {
		replyArxivImportError(w, err)
		return
	}
	refs := make([]PaperRef, 0, len(papers))
	err = store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND deleted_at IS NULL", record.ID, user).Take(&record).Error; err != nil {
			return err
		}
		if record.DiscoveryMode != "external" {
			var input ReviewWorkflowInput
			if err := json.Unmarshal(record.ResultJSON, &input); err != nil {
				return err
			}
			seeds := []WorkflowPaper{}
			for _, paper := range input.Papers {
				if paper.IsSeed {
					seeds = append(seeds, paper)
				}
			}
			snapshot, err := seedSources(tx, record.DatasetID, seeds)
			if err != nil {
				return err
			}
			record.SeedSnapshotJSON = marshal(snapshot)
			if err := tx.Model(&record).Updates(map[string]any{"seed_snapshot_json": record.SeedSnapshotJSON, "discovery_mode": "external"}).Error; err != nil {
				return err
			}
		}
		var searches []paperDiscovery
		if err := json.Unmarshal(record.DiscoveryJSON, &searches); err != nil {
			return err
		}
		for _, metadata := range papers {
			work, _, err := findOrCreateWork(r.Context(), tx, arxivWorkInput(metadata))
			if err != nil {
				return err
			}
			ref := paperRef(work)
			ref.Title, ref.Authors, ref.Abstract, ref.Year, ref.Venue = metadata.Title, metadata.Authors, &metadata.Abstract, metadata.Year, metadata.Venue
			ref.SourceVersion, ref.SourceProvider = metadata.Version, "arxiv"
			pinPaperURLs(&ref)
			// One task pins one revision per paper across continued rounds.
			for _, old := range searches {
				for _, prior := range old.Papers {
					if prior.PaperID == ref.PaperID {
						ref = prior
					}
				}
			}
			refs = append(refs, ref)
		}
		var session orm.WorkflowSession
		_ = tx.Where("conversation_id=?", record.ConversationID).Order("created_at DESC").Take(&session).Error
		searches = append(searches, paperDiscovery{SessionID: session.ID, Query: req.Query, CreatedAt: time.Now().UTC(), Papers: refs})
		return tx.Model(&record).Update("discovery_json", marshal(searches)).Error
	})
	if err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	common.ReplyJSON(w, map[string]any{"papers": refs, "source": "arxiv", "mock": false})
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
	if source.Paper.SourceVersion == "" {
		return source, nil
	}
	var link orm.AcademicWorkDocument
	err := db.Where("academic_work_id=? AND dataset_id=? AND source_version=? AND source_provider=? AND document_id IN (?)", source.Paper.PaperID, source.DatasetID, source.Paper.SourceVersion, source.Paper.SourceProvider,
		db.Model(&orm.Document{}).Select("id").Where("dataset_id=? AND deleted_at IS NULL", source.DatasetID)).Order("is_preferred_version DESC, updated_at DESC").Take(&link).Error
	if err == nil {
		source.DocumentID, source.Hash = link.DocumentID, link.ContentSHA256
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return source, err
	}
	return source, nil
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
