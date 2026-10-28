package academic

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
)

var errFulltextUnavailable = errors.New("full text is unavailable: no registered PDF candidate")

type PaperRef struct {
	CitationCount    *int64   `json:"citation_count,omitempty"`
	SourceDocumentID string   `json:"document_id,omitempty"`
	SourceVersion    string   `json:"source_version,omitempty"`
	SourceProvider   string   `json:"source_provider,omitempty"`
	PaperID          string   `json:"paper_id"`
	Title            string   `json:"title,omitempty"`
	Authors          []string `json:"authors,omitempty"`
	Abstract         *string  `json:"abstract"`
	Year             int      `json:"year,omitempty"`
	Venue            string   `json:"venue,omitempty"`
	DOI              string   `json:"doi,omitempty"`
	ArxivID          string   `json:"arxiv_id,omitempty"`
	PaperURL         string   `json:"paper_url,omitempty"`
	PDFURL           string   `json:"pdf_url,omitempty"`
}
type ContentPage struct {
	Page int    `json:"page"`
	Text string `json:"text"`
}

func paperRef(work orm.AcademicWork) PaperRef {
	p := PaperRef{PaperID: work.ID, Title: work.CanonicalTitle, Year: work.PublicationYear, Venue: work.Venue, DOI: work.DOINormalized, ArxivID: work.ArxivIDBase}
	_ = json.Unmarshal(work.AuthorsJSON, &p.Authors)
	if work.Abstract != "" {
		a := work.Abstract
		p.Abstract = &a
	}
	if work.ArxivIDBase != "" {
		p.SourceProvider = "arxiv"
		p.PaperURL = "https://arxiv.org/abs/" + work.ArxivIDBase
		p.PDFURL = "https://arxiv.org/pdf/" + work.ArxivIDBase
	}
	return p
}
func internalAcademic(ctx context.Context, action string, input, output any) error {
	token := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if token == "" {
		return errors.New("internal service token is unavailable")
	}
	endpoint := strings.TrimRight(os.Getenv("LAZYMIND_CHAT_SERVICE_URL"), "/")
	if endpoint == "" {
		endpoint = "http://chat:8046"
	}
	timeout := 10 * time.Minute
	if action == "preparation:run" {
		timeout = 0
	}
	return common.ApiPost(ctx, endpoint+"/internal/academic/"+action, input, map[string]string{"X-LazyMind-Internal-Token": token}, output, timeout)
}
func init() { asyncjob.Register("academic.paper_content", runPaperContent) }

func ensurePaperContent(ctx context.Context, db *gorm.DB, userID, paperID string, source paperSource) (orm.AcademicPaperContent, error) {
	if source.Paper.PaperID != paperID || source.DatasetID == "" {
		return orm.AcademicPaperContent{}, errors.New("paper source context required")
	}
	if row, err := findSourceContent(db.WithContext(ctx), userID, source); err == nil {

		return row, err
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return row, err
	}
	kind := "external"
	if source.DocumentID != "" {
		kind = "kb"
	}
	row := orm.AcademicPaperContent{ID: uuid.NewString(), UserID: userID, PaperID: paperID, SourceKind: kind, SourceProvider: source.Paper.SourceProvider, SourceKey: source.key(), SourceVersion: source.Paper.SourceVersion, SourceDatasetID: source.DatasetID, SourceDocumentID: source.DocumentID, SourceHash: source.Hash, ParserVersion: orm.AcademicPaperParserVersion, Status: "processing", PagesJSON: json.RawMessage(`[]`), UpdatedAt: time.Now().UTC()}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return tx.Where("user_id=? AND paper_id=? AND parser_version=? AND source_key=?", userID, paperID, orm.AcademicPaperParserVersion, source.key()).Take(&row).Error
		}
		_, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: "academic.paper_content", ResourceType: "academic_content", ResourceID: row.ID, CreateUserID: userID, IdempotencyKey: row.ID, Payload: map[string]any{"source": source}, MaxAttempts: 3})
		return err
	})
	return row, err
}

func runPaperContent(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	db := store.DB().WithContext(ctx)
	var row orm.AcademicPaperContent
	if err := db.Where("id=? AND user_id=?", job.ResourceID, job.CreateUserID).Take(&row).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	if row.Status == "ready" {
		return asyncjob.Result{}, nil
	}
	var payload struct {
		Source paperSource `json:"source"`
	}
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil || payload.Source.DatasetID == "" {
		return asyncjob.Result{Permanent: true}, errors.New("legacy fulltext job requires a new expansion-context read")
	}
	pages, kind, hash, version, err := readKnownPaper(ctx, job.CreateUserID, payload.Source, row)

	if err != nil {
		status := "processing"
		if job.AttemptCount >= job.MaxAttempts {
			status = "failed"
		}
		if errors.Is(err, errFulltextUnavailable) {
			status = "unavailable"
		}
		var latest orm.AcademicPaperContent
		if e := db.Where("id=?", row.ID).Take(&latest).Error; e != nil {
			return asyncjob.Result{}, e
		}
		var acquisition contentAcquisition
		_ = json.Unmarshal(latest.QualityJSON, &acquisition)
		quality := checkPaperQuality(payload.Source.Paper, nil, kind, hash)
		quality.Acquisition, quality.Reason = acquisition, err.Error()
		quality.Downloaded = quality.Downloaded || acquisition.Downloaded
		if updateErr := db.Model(&row).Updates(map[string]any{"status": status, "quality_json": marshal(quality), "source_hash": firstNonEmpty(hash, acquisition.PDFHash), "error": err.Error(), "updated_at": time.Now().UTC()}).Error; updateErr != nil {
			return asyncjob.Result{}, updateErr
		}
		return asyncjob.Result{Permanent: errors.Is(err, errFulltextUnavailable)}, err
	}
	data, err := json.Marshal(pages)
	if err != nil {
		return asyncjob.Result{}, err
	}
	sum := sha256.Sum256(data)
	updates := map[string]any{"status": "ready", "pages_json": data, "content_hash": hex.EncodeToString(sum[:]), "source_kind": kind, "source_provider": payload.Source.Paper.SourceProvider, "source_hash": hash, "source_version": version, "error": "", "updated_at": time.Now().UTC()}
	quality := checkPaperQuality(payload.Source.Paper, pages, kind, hash)
	var acquisition contentAcquisition
	var latest orm.AcademicPaperContent
	if e := db.Where("id=?", row.ID).Take(&latest).Error; e != nil {
		return asyncjob.Result{}, e
	}
	_ = json.Unmarshal(latest.QualityJSON, &acquisition)
	quality.Acquisition = acquisition
	if acquisition.SourceProvider != "" {
		updates["source_provider"] = acquisition.SourceProvider
	}
	updates["quality_json"] = marshal(quality)
	if !quality.Eligible {
		updates["status"] = "needs_review"
		updates["error"] = quality.Reason
	}
	if kind == "external" {
		if payload.Source.Paper.ArxivID != "" {
			updates["source_provider"] = "arxiv"
		}
		updates["source_dataset_id"], updates["source_document_id"] = "", ""
	}
	return asyncjob.Result{}, db.Model(&row).Updates(updates).Error
}

func readKnownPaper(ctx context.Context, userID string, source paperSource, content orm.AcademicPaperContent) ([]ContentPage, string, string, string, error) {
	if document, err := sourceDocument(ctx, store.DB(), source); err == nil {
		if service, e := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()}); e == nil && documentParseReady(ctx, document) {
			pages := []ContentPage{}
			token := ""
			for {
				result, e := service.ListDocumentChunks(ctx, doc.DocumentChunksRequest{UserID: userID, DatasetID: source.DatasetID, DocumentID: source.DocumentID, SegmentGroup: doc.RootNodeGroup, PageSize: 200, PageToken: token, Caller: doc.DatasetCatalogCaller{UserID: userID}})
				if e != nil {
					break
				}
				for _, chunk := range result.Chunks {
					if strings.TrimSpace(chunk.Text) == "" {
						continue
					}
					page := 0
					if chunk.LayoutBlocks != nil && len(*chunk.LayoutBlocks) > 0 {
						page = (*chunk.LayoutBlocks)[0].Page
					}
					pages = append(pages, ContentPage{Page: page, Text: chunk.Text})
				}
				if result.NextPageToken == "" {
					if len(pages) > 0 {
						return pages, "kb", source.Hash, source.Paper.SourceVersion, nil
					}
					break
				}
				if result.NextPageToken == token {
					break
				}
				token = result.NextPageToken
			}
		}
		var ext struct {
			StoredPath string `json:"stored_path"`
		}
		_ = json.Unmarshal(document.Ext, &ext)
		if ext.StoredPath != "" {
			path, e := filepath.EvalSymlinks(ext.StoredPath)
			root, _ := filepath.Abs(doc.UploadRoot())
			if e == nil && strings.HasPrefix(path, root+string(os.PathSeparator)) {
				if pages, hash, e := parsePaperPDF(ctx, path, source.Hash); e == nil {
					return pages, "kb", hash, source.Paper.SourceVersion, nil
				}
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", "", "", err
	}
	paper := source.Paper
	if paper.ArxivID != "" && paper.SourceVersion == "" {
		// An unversioned local seed stays unversioned when reading its PDF.
		// If external fallback is necessary, pin that revision before download
		// so a failed download or interrupted job cannot advance on retry.
		paper.SourceVersion = content.SourceVersion
		if paper.SourceVersion == "" {
			metadata, err := arxivsource.NewClient().Lookup(ctx, paper.ArxivID)
			if err != nil {
				return nil, "", "", "", err
			}
			paper.SourceVersion = metadata.Version
			if err = store.DB().WithContext(ctx).Model(&content).Update("source_version", metadata.Version).Error; err != nil {
				return nil, "", "", "", err
			}
		}
		paper.SourceProvider = "arxiv"
		pinPaperURLs(&paper)
	}
	var path string
	var err error
	if _, ok := arxivMetadataFromRef(paper); ok {
		id, _ := arxivsource.Parse(paper.ArxivID + paper.SourceVersion)
		guard, e := lockArxivImport(ctx, store.DB(), id.BaseID)
		if e != nil {
			return nil, "", "", "", e
		}
		defer guard.release()
		ctx = guard.ctx
		path, _, err = acquireCachedPDFLocked(ctx, userID, paper, func(ctx context.Context, candidate FulltextCandidate) (string, int64, error) {
			return acquirePDF(ctx, userID, candidate)
		})
	} else {
		source.Paper = paper
		return readExternalCopies(ctx, userID, source, content)
	}
	if err != nil {
		return nil, "", "", "", err
	}
	pages, hash, err := parsePaperPDF(ctx, path, "")
	return pages, "external", hash, paper.SourceVersion, err
}

func parsePaperPDF(ctx context.Context, path, expectedHash string) ([]ContentPage, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	root := filepath.Join(doc.UploadRoot(), "academic")
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, "", err
	}
	ref := uuid.NewString() + ".pdf"
	target := filepath.Join(root, ref)
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(target)
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), file)
	closeErr := out.Close()
	if copyErr != nil {
		return nil, "", copyErr
	}
	if closeErr != nil {
		return nil, "", closeErr
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if expectedHash != "" && expectedHash != hash {
		return nil, "", errors.New("PDF version changed")
	}
	var parsed struct {
		Pages         []ContentPage `json:"pages"`
		ParserVersion string        `json:"parser_version"`
	}
	if err = internalAcademic(ctx, "pdf:parse", map[string]string{"file_ref": ref, "content_hash": hash}, &parsed); err != nil {
		return nil, hash, err
	}
	if parsed.ParserVersion != orm.AcademicPaperParserVersion {
		return nil, hash, errors.New("PDF 解析服务版本不一致，请同步更新解析服务")
	}
	if len(parsed.Pages) == 0 {
		return nil, hash, errors.New("PDF yielded no full text")
	}
	return parsed.Pages, hash, nil
}

type contentCursor struct {
	ID          string `json:"id"`
	Hash        string `json:"hash"`
	Offset      int    `json:"offset"`
	ExpansionID string `json:"expansion_id,omitempty"`
	HandoffID   string `json:"handoff_id,omitempty"`
	Positions   []int  `json:"positions,omitempty"`
}

func ReadPaperContent(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		PaperID             string   `json:"paper_id"`
		Cursor              string   `json:"cursor"`
		ExpansionID         string   `json:"expansion_id"`
		HandoffID           string   `json:"handoff_id"`
		Keywords            []string `json:"keywords"`
		PageStart           int      `json:"page_start"`
		PageEnd             int      `json:"page_end"`
		PreparationRevision *int64   `json:"preparation_revision"`
		Positions           []int    `json:"positions"`
	}
	if !parseJSON(r, &req) || (req.PaperID == "") == (req.Cursor == "") || (req.ExpansionID != "" && req.HandoffID != "") {
		replyError(w, 400, "INVALID_REQUEST", "supply paper_id with expansion_id or handoff_id, or a context-bound cursor")
		return
	}
	var cursor contentCursor
	var row orm.AcademicPaperContent
	if req.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Offset < 0 || (cursor.ExpansionID == "") == (cursor.HandoffID == "") || (req.ExpansionID != "" && req.ExpansionID != cursor.ExpansionID) || (req.HandoffID != "" && req.HandoffID != cursor.HandoffID) {
			replyError(w, 400, "INVALID_CURSOR", "invalid cursor")
			return
		}
		req.ExpansionID = cursor.ExpansionID
		req.HandoffID = cursor.HandoffID
		if store.DB().Where("id=? AND user_id=?", cursor.ID, user).Take(&row).Error != nil {
			replyError(w, 404, "NOT_FOUND", "content not found")
			return
		}
		req.PaperID = row.PaperID
	}
	var source paperSource
	var selected orm.AcademicPaperContent
	var err error
	if req.HandoffID != "" {
		handoff, ok := ownedHandoff(w, r, user, req.HandoffID)
		if !ok {
			return
		}
		var contents []orm.AcademicPaperContent
		source, contents, err = handoffSource(store.DB(), handoff, req.PaperID)
		if err != nil {
			replyError(w, 422, "INVALID_SELECTION", err.Error())
			return
		}
		for _, content := range contents {
			if content.PaperID != req.PaperID || content.Status != "ready" || content.ContentHash == "" {
				continue
			}
			if store.DB().Where("id=? AND user_id=? AND paper_id=? AND status='ready' AND content_hash=?", content.ID, user, req.PaperID, content.ContentHash).Take(&selected).Error != nil {
				replyError(w, 409, "VERSION_CHANGED", "handoff content version unavailable")
				return
			}
			break
		}
	} else {
		record, ok := ownedExpansion(w, r, user, req.ExpansionID)
		if !ok {
			return
		}
		if req.PreparationRevision != nil && (record.PreparationRevision != *req.PreparationRevision || record.Status != "preparing") {
			replyError(w, 409, "VERSION_CHANGED", "preparation attempt is no longer current")
			return
		}
		source, err = expansionSource(store.DB(), record, req.PaperID)
		if err != nil {
			replyError(w, 422, "INVALID_SELECTION", err.Error())
			return
		}
	}
	if selected.ID == "" {
		selected, err = ensurePaperContent(r.Context(), store.DB(), user, req.PaperID, source)
	}
	if err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	if req.Cursor != "" && (selected.ID != row.ID || row.ContentHash != cursor.Hash || row.Status != "ready") {
		replyError(w, 409, "VERSION_CHANGED", "content version unavailable")
		return
	}
	row = selected
	var pages []ContentPage
	_ = json.Unmarshal(row.PagesJSON, &pages)
	positions := cursor.Positions
	if req.Cursor == "" {
		if req.Positions != nil {
			if len(req.Positions) > 5 || req.PageStart != 0 || req.PageEnd != 0 || len(req.Keywords) > 0 {
				replyError(w, 400, "INVALID_REQUEST", "supply 1..5 positions or a page/keyword selector")
				return
			}
			seen := map[int]bool{}
			for _, index := range req.Positions {
				if seen[index] {
					replyError(w, 400, "INVALID_REQUEST", "duplicate position")
					return
				}
				seen[index] = true
			}
			positions = req.Positions
		} else {
			positions, err = selectedContentPositions(pages, req.Keywords, req.PageStart, req.PageEnd)
		}
		if err != nil {
			replyError(w, 400, "INVALID_REQUEST", err.Error())
			return
		}
	}
	for _, index := range positions {
		if index < 0 || index >= len(pages) {
			replyError(w, 400, "INVALID_CURSOR", "invalid content position")
			return
		}
	}
	offset := cursor.Offset
	if offset > len(positions) {
		replyError(w, 400, "INVALID_CURSOR", "invalid offset")
		return
	}
	end := offset + 5
	if end > len(positions) {
		end = len(positions)
	}
	output := []ContentPage{}
	delivered := []int{}
	if usablePaperContent(row) {
		for _, index := range positions[offset:end] {
			output = append(output, pages[index])
			delivered = append(delivered, index)
		}
	}
	next := ""
	if end < len(positions) && usablePaperContent(row) {
		next = base64.RawURLEncoding.EncodeToString(marshal(contentCursor{ID: row.ID, Hash: row.ContentHash, Offset: end, Positions: positions, ExpansionID: req.ExpansionID, HandoffID: req.HandoffID}))
	}
	level := "none"
	if usablePaperContent(row) {
		level = "fulltext"
	}
	if err = recordPaperRead(r.Context(), user, req.ExpansionID, row, delivered, pages); err != nil {
		replyError(w, 500, "READ_FAILED", "could not save reading coverage")
		return
	}
	warnings := []string{}
	for _, page := range output {
		if page.Page == 0 {
			warnings = append(warnings, "原解析结果未提供可靠页码，page=0 表示位置未知")
			break
		}
	}
	if row.Error != "" {
		warnings = append(warnings, row.Error)
	}
	common.ReplyJSON(w, map[string]any{"status": row.Status, "paper": source.Paper, "content_level": level, "content_hash": row.ContentHash, "pages": output, "positions": delivered, "total_blocks": len(pages), "quality": json.RawMessage(row.QualityJSON), "next_cursor": next, "warnings": warnings, "parser_version": row.ParserVersion, "source_kind": row.SourceKind, "source_version": row.SourceVersion})
}

func RetryPaperContent(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		PaperID     string `json:"paper_id"`
		ExpansionID string `json:"expansion_id"`
	}
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "expansion_id and paper_id required")
		return
	}
	record, ok := ownedExpansion(w, r, user, req.ExpansionID)
	if !ok {
		return
	}
	source, err := expansionSource(store.DB(), record, req.PaperID)
	if err != nil {
		replyError(w, 422, "INVALID_SELECTION", err.Error())
		return
	}
	row, err := ensurePaperContent(r.Context(), store.DB(), user, req.PaperID, source)
	if err != nil {
		replyError(w, 500, "READ_FAILED", err.Error())
		return
	}
	if row.Status == "failed" || row.Status == "unavailable" || row.Status == "needs_review" {
		err = store.DB().Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&row).Updates(map[string]any{"status": "processing", "error": "", "quality_json": json.RawMessage(`{}`)}).Error; err != nil {
				return err
			}
			_, err := asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: "academic.paper_content", ResourceType: "academic_content", ResourceID: row.ID, CreateUserID: user, IdempotencyKey: row.ID, Payload: map[string]any{"source": source}, MaxAttempts: 3})
			return err
		})
		if err != nil {
			replyError(w, 500, "RETRY_FAILED", fmt.Sprint(err))
			return
		}
		row.Status = "processing"
	}
	notifyPaperRecords(r.Context(), store.DB(), user, req.PaperID)
	common.ReplyJSON(w, map[string]string{"status": row.Status})
}

func documentPaperRef(paper PaperRef, metadata json.RawMessage) PaperRef {
	var value WorkInput
	if json.Unmarshal(metadata, &value) != nil {
		if value.CitationCount != nil {
			paper.CitationCount = value.CitationCount
		}
		return paper
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(metadata, &fields)
	if _, present := fields["title"]; present {
		paper.Title = value.Title
	}
	if _, present := fields["authors"]; present {
		paper.Authors = value.Authors
	}
	if _, present := fields["year"]; present {
		paper.Year = value.Year
	}
	if _, present := fields["venue"]; present {
		paper.Venue = value.Venue
	}
	if _, present := fields["abstract"]; present {
		paper.Abstract = &value.Abstract
	}
	return paper
}

// Document metadata stays scoped to this knowledge base; manual corrections win.
func datasetPaperRef(db *gorm.DB, dataset string, work orm.AcademicWork) (PaperRef, error) {
	paper := paperRef(work)
	var entry orm.PaperLibraryEntry
	if e := db.Where("dataset_id=? AND academic_work_id=? AND removed_at IS NULL", dataset, work.ID).Take(&entry).Error; e == nil {
		m, e := paperlibrary.EffectiveMetadata(entry, work)
		if e != nil {
			return paper, e
		}
		paper.Title, paper.Authors, paper.Year, paper.Venue, paper.Abstract, paper.CitationCount = m.Title, m.Authors, m.Year, m.Venue, &m.Abstract, m.CitationCount
		return paper, nil
	} else if !errors.Is(e, gorm.ErrRecordNotFound) {
		return paper, e
	}

	var state orm.AcademicDocumentState
	documents := db.Model(&orm.Document{}).Select("id").Where("dataset_id=? AND deleted_at IS NULL", dataset)
	err := db.Where("dataset_id=? AND paper_id=? AND document_id IN (?)", dataset, work.ID, documents).Order("manual DESC, updated_at DESC").Take(&state).Error
	if err == nil {
		return documentPaperRef(paper, state.MetadataJSON), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return paper, err
	}
	return paper, nil
}

func documentParseReady(ctx context.Context, document orm.Document) bool {
	status, _, err := paperProcessingStatus(ctx, store.DB(), document)
	return err == nil && status == "ready"
}
