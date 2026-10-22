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
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

var errFulltextUnavailable = errors.New("full text is unavailable: no registered PDF candidate")

type PaperRef struct {
	PaperID  string   `json:"paper_id"`
	Title    string   `json:"title,omitempty"`
	Authors  []string `json:"authors,omitempty"`
	Abstract *string  `json:"abstract"`
	Year     int      `json:"year,omitempty"`
	Venue    string   `json:"venue,omitempty"`
	DOI      string   `json:"doi,omitempty"`
	ArxivID  string   `json:"arxiv_id,omitempty"`
	PaperURL string   `json:"paper_url,omitempty"`
	PDFURL   string   `json:"pdf_url,omitempty"`
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
	return common.ApiPost(ctx, endpoint+"/internal/academic/"+action, input, map[string]string{"X-LazyMind-Internal-Token": token}, output, 10*time.Minute)
}
func init() { asyncjob.Register("academic.paper_content", runPaperContent) }

func ensurePaperContent(ctx context.Context, db *gorm.DB, userID, paperID string) (orm.AcademicPaperContent, error) {
	var work orm.AcademicWork
	if err := db.WithContext(ctx).Where("id=?", paperID).Take(&work).Error; err != nil {
		return orm.AcademicPaperContent{}, err
	}
	now := time.Now().UTC()
	row := orm.AcademicPaperContent{ID: uuid.NewString(), UserID: userID, PaperID: paperID, ParserVersion: orm.AcademicPaperParserVersion, Status: "processing", PagesJSON: json.RawMessage(`[]`), UpdatedAt: now}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			row = orm.AcademicPaperContent{}
			return tx.Where("user_id=? AND paper_id=? AND parser_version=?", userID, paperID, orm.AcademicPaperParserVersion).Take(&row).Error
		}
		_, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: "academic.paper_content", ResourceType: "academic_content", ResourceID: row.ID, CreateUserID: userID, IdempotencyKey: row.ID, Payload: map[string]string{"id": row.ID}, MaxAttempts: 3})
		return err
	})
	return row, err
}
func runPaperContent(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var row orm.AcademicPaperContent
	db := store.DB().WithContext(ctx)
	if err := db.Where("id=? AND user_id=?", job.ResourceID, job.CreateUserID).Take(&row).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	if row.Status == "ready" {
		return asyncjob.Result{}, nil
	}
	var work orm.AcademicWork
	if err := db.Where("id=?", row.PaperID).Take(&work).Error; err != nil {
		return asyncjob.Result{Permanent: true}, err
	}
	pages, err := readKnownPaper(ctx, job.CreateUserID, work)
	if err != nil {
		status := "processing"
		if job.AttemptCount >= job.MaxAttempts {
			status = "failed"
		}
		if errors.Is(err, errFulltextUnavailable) {
			status = "unavailable"
		}
		if updateErr := db.Model(&row).Updates(map[string]any{"status": status, "error": err.Error(), "updated_at": time.Now().UTC()}).Error; updateErr != nil {
			return asyncjob.Result{}, updateErr
		}
		return asyncjob.Result{Permanent: errors.Is(err, errFulltextUnavailable)}, err
	}
	data, _ := json.Marshal(pages)
	sum := sha256.Sum256(data)
	return asyncjob.Result{}, db.Model(&row).Updates(map[string]any{"status": "ready", "pages_json": data, "content_hash": hex.EncodeToString(sum[:]), "error": "", "updated_at": time.Now().UTC()}).Error
}
func readKnownPaper(ctx context.Context, userID string, work orm.AcademicWork) ([]ContentPage, error) {
	// A complete root-node traversal can reuse a parsed authorized KB copy.
	var links []orm.AcademicWorkDocument
	if err := store.DB().WithContext(ctx).Where("academic_work_id=?", work.ID).Find(&links).Error; err != nil {
		return nil, err
	}
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if err == nil {
		for _, link := range links {
			var pages []ContentPage
			token := ""
			complete := false
			for {
				result, e := service.ListDocumentChunks(ctx, doc.DocumentChunksRequest{UserID: userID, DatasetID: link.DatasetID, DocumentID: link.DocumentID, SegmentGroup: doc.RootNodeGroup, PageSize: 200, PageToken: token, Caller: doc.DatasetCatalogCaller{UserID: userID}})
				if e != nil {
					break
				}
				for _, chunk := range result.Chunks {
					if strings.TrimSpace(chunk.Text) != "" {
						page := 0
						if chunk.LayoutBlocks != nil && len(*chunk.LayoutBlocks) > 0 {
							page = (*chunk.LayoutBlocks)[0].Page
						}
						pages = append(pages, ContentPage{Page: page, Text: chunk.Text})
					}
				}
				if result.NextPageToken == "" {
					complete = true
					break
				}
				if result.NextPageToken == token {
					return nil, errors.New("root pagination did not advance")
				}
				token = result.NextPageToken
			}
			if complete && len(pages) > 0 {
				if e := store.DB().WithContext(ctx).Model(&orm.AcademicPaperContent{}).Where("user_id=? AND paper_id=? AND status=?", userID, work.ID, "processing").Updates(map[string]any{"source_dataset_id": link.DatasetID, "source_document_id": link.DocumentID}).Error; e != nil {
					return nil, e
				}
				return pages, nil
			}
		}
	}
	candidates := candidatesForWork(work)
	if len(candidates) == 0 {
		return nil, errFulltextUnavailable
	}
	source, _, err := acquirePDF(ctx, candidates[0])
	if err != nil {
		return nil, err
	}
	defer os.Remove(source)
	file, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	root := filepath.Join(doc.UploadRoot(), "academic")
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	ref := uuid.NewString() + ".pdf"
	target := filepath.Join(root, ref)
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer os.Remove(target)
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), file)
	closeErr := out.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = store.DB().WithContext(ctx).Model(&orm.AcademicPaperContent{}).Where("user_id=? AND paper_id=? AND parser_version=? AND status=?", userID, work.ID, orm.AcademicPaperParserVersion, "processing").Update("source_hash", hex.EncodeToString(h.Sum(nil))).Error; err != nil {
		return nil, err
	}
	var parsed struct {
		Pages []ContentPage `json:"pages"`
	}
	err = internalAcademic(ctx, "pdf:parse", map[string]string{"file_ref": ref, "content_hash": hex.EncodeToString(h.Sum(nil))}, &parsed)
	if err != nil {
		return nil, err
	}
	if len(parsed.Pages) == 0 {
		return nil, errors.New("PDF yielded no full text")
	}
	return parsed.Pages, nil
}

type contentCursor struct {
	ID     string `json:"id"`
	Hash   string `json:"hash"`
	Offset int    `json:"offset"`
}

func ReadPaperContent(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		PaperID string `json:"paper_id"`
		Cursor  string `json:"cursor"`
	}
	if !parseJSON(r, &req) || (req.PaperID == "") == (req.Cursor == "") {
		replyError(w, 400, "INVALID_REQUEST", "supply paper_id or cursor exclusively")
		return
	}
	var row orm.AcademicPaperContent
	offset := 0
	if req.Cursor != "" {
		var c contentCursor
		b, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Offset < 0 {
			replyError(w, 400, "INVALID_CURSOR", "invalid cursor")
			return
		}
		if store.DB().Where("id=? AND user_id=?", c.ID, user).Take(&row).Error != nil {
			replyError(w, 404, "NOT_FOUND", "content not found")
			return
		}
		if row.ContentHash != c.Hash || row.Status != "ready" {
			replyError(w, 409, "VERSION_CHANGED", "content version unavailable")
			return
		}
		offset = c.Offset
	} else {
		var err error
		row, err = ensurePaperContent(r.Context(), store.DB(), user, req.PaperID)
		if err != nil {
			replyError(w, 404, "NOT_FOUND", "paper not found")
			return
		}
	}
	if !paperContentAuthorized(r.Context(), user, row) {
		replyError(w, 403, "CONTENT_FORBIDDEN", "source document is no longer accessible")
		return
	}
	var pages []ContentPage
	_ = json.Unmarshal(row.PagesJSON, &pages)
	if offset > len(pages) {
		replyError(w, 400, "INVALID_CURSOR", "invalid offset")
		return
	}
	end := offset + 5
	if end > len(pages) {
		end = len(pages)
	}
	next := ""
	if end < len(pages) {
		b, _ := json.Marshal(contentCursor{row.ID, row.ContentHash, end})
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	var work orm.AcademicWork
	if err := store.DB().Where("id=?", row.PaperID).Take(&work).Error; err != nil {
		replyError(w, 404, "NOT_FOUND", "paper unavailable")
		return
	}
	level := "none"
	if row.Status == "ready" {
		level = "fulltext"
	}
	warnings := []string{}
	for _, page := range pages[offset:end] {
		if page.Page == 0 {
			warnings = append(warnings, "原解析结果未提供可靠页码，page=0 表示位置未知")
			break
		}
	}
	if row.Error != "" {
		warnings = append(warnings, row.Error)
	}
	common.ReplyJSON(w, map[string]any{"status": row.Status, "paper": paperRef(work), "content_level": level, "content_hash": row.ContentHash, "pages": pages[offset:end], "next_cursor": next, "warnings": warnings, "parser_version": row.ParserVersion})
}
func RetryPaperContent(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req struct {
		PaperID string `json:"paper_id"`
	}
	if !parseJSON(r, &req) {
		replyError(w, 400, "INVALID_REQUEST", "paper_id required")
		return
	}
	var row orm.AcademicPaperContent
	if store.DB().Where("user_id=? AND paper_id=? AND parser_version=?", user, req.PaperID, orm.AcademicPaperParserVersion).Take(&row).Error != nil {
		replyError(w, 404, "NOT_FOUND", "content not found")
		return
	}
	if row.Status == "failed" || row.Status == "unavailable" {
		err := store.DB().Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&row).Updates(map[string]any{"status": "processing", "error": ""}).Error; err != nil {
				return err
			}
			_, err := asyncjob.EnqueueInTransaction(r.Context(), tx, asyncjob.EnqueueRequest{JobType: "academic.paper_content", ResourceType: "academic_content", ResourceID: row.ID, CreateUserID: user, IdempotencyKey: row.ID, Payload: map[string]string{"id": row.ID}, MaxAttempts: 3})
			return err
		})
		if err != nil {
			replyError(w, 500, "RETRY_FAILED", fmt.Sprint(err))
			return
		}
	}
	notifyPaperRecords(r.Context(), store.DB(), user, req.PaperID)
	common.ReplyJSON(w, map[string]string{"status": row.Status})
}

func documentPaperRef(paper PaperRef, metadata json.RawMessage) PaperRef {
	var value WorkInput
	if json.Unmarshal(metadata, &value) != nil {
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
func paperContentAuthorized(ctx context.Context, user string, content orm.AcademicPaperContent) bool {
	if content.SourceDocumentID == "" {
		return true
	}
	service, err := doc.NewDocumentService(doc.DocumentServiceDeps{DB: store.DB(), LazyDB: store.LazyLLMDB()})
	if err != nil {
		return false
	}
	_, err = service.GetDocumentMetadata(ctx, doc.DocumentGetRequest{UserID: user, DatasetID: content.SourceDatasetID, DocumentID: content.SourceDocumentID, Caller: doc.DatasetCatalogCaller{UserID: user}})
	return err == nil
}

// Document metadata stays scoped to this knowledge base; manual corrections win.
func datasetPaperRef(db *gorm.DB, dataset string, work orm.AcademicWork) (PaperRef, error) {
	paper := paperRef(work)
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
