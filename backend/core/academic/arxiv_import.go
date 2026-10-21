package academic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/academic/arxivsource"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

// ArxivImportResult describes a real, persisted PDF attachment and its parent.
// Imported means the file is stored; processing_status describes parsing separately.
type ArxivImportResult struct {
	Paper            arxivsource.Metadata `json:"paper"`
	WorkID           string               `json:"work_id"`
	DocumentID       string               `json:"document_id"`
	DocumentTaskID   string               `json:"document_task_id"`
	Filename         string               `json:"filename"`
	Size             int64                `json:"size"`
	SourceURL        string               `json:"source_url"`
	SourceVersion    string               `json:"source_version"`
	ContentSHA256    string               `json:"content_sha256"`
	Status           string               `json:"status"`
	ProcessingStatus string               `json:"processing_status"`
	ProcessingError  string               `json:"processing_error,omitempty"`
}

type arxivImportRequest struct {
	Input           string `json:"input"`
	TargetDatasetID string `json:"target_dataset_id"`
}
type arxivImportService struct {
	db         *gorm.DB
	lookup     func(context.Context, string) (arxivsource.Metadata, error)
	acquire    func(context.Context, FulltextCandidate) (string, int64, error)
	importFile func(context.Context, doc.LocalFileImportRequest) (doc.LocalFileImportResult, error)
}

func newArxivImportService(db *gorm.DB) *arxivImportService {
	return &arxivImportService{db: db, lookup: arxivsource.NewClient().Lookup, acquire: acquirePDF, importFile: doc.ImportLocalFile}
}

func LookupArxiv(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var req struct {
		Input string `json:"input"`
	}
	if !parseJSON(r, &req) || strings.TrimSpace(req.Input) == "" {
		replyError(w, 400, "INVALID_REQUEST", "请输入 arXiv 编号或链接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	paper, err := arxivsource.NewClient().Lookup(ctx, req.Input)
	if err != nil {
		replyArxivImportError(w, ArxivImportResult{}, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"paper": paper})
}

func ImportArxiv(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req arxivImportRequest
	if !parseJSON(r, &req) || strings.TrimSpace(req.Input) == "" || strings.TrimSpace(req.TargetDatasetID) == "" {
		replyError(w, 400, "INVALID_REQUEST", "input 和 target_dataset_id 不能为空")
		return
	}
	req.TargetDatasetID = strings.TrimSpace(req.TargetDatasetID)
	if !canWriteDataset(r.Context(), req.TargetDatasetID, userID) {
		replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标知识库的导入权限")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	result, err := newArxivImportService(store.DB()).importPaper(ctx, req.Input, req.TargetDatasetID, userID, store.UserName(r))
	if err != nil {
		replyArxivImportError(w, result, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func ListArxivLibrary(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	datasetID := strings.TrimSpace(r.URL.Query().Get("target_dataset_id"))
	if datasetID == "" {
		replyError(w, 400, "INVALID_REQUEST", "target_dataset_id 不能为空")
		return
	}
	catalog, err := doc.NewDatasetCatalogService(doc.DatasetCatalogServiceDeps{DB: store.DB()})
	if err != nil {
		replyArxivImportError(w, ArxivImportResult{}, err)
		return
	}
	if _, err = catalog.GetDataset(r.Context(), doc.DatasetGetRequest{UserID: userID, DatasetID: datasetID, Caller: caller(r)}); err != nil {
		var serviceErr *doc.DatasetServiceError
		if errors.As(err, &serviceErr) && (serviceErr.Code == doc.DatasetServiceForbidden || serviceErr.Code == doc.DatasetServiceNotFound) {
			replyError(w, 403, "TARGET_DATASET_FORBIDDEN", "没有目标知识库的读取权限")
			return
		}
		replyArxivImportError(w, ArxivImportResult{}, err)
		return
	}
	items, err := newArxivImportService(store.DB()).library(r.Context(), datasetID)
	if err != nil {
		replyArxivImportError(w, ArxivImportResult{}, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func replyArxivImportError(w http.ResponseWriter, result ArxivImportResult, err error) {
	code, status := "ARXIV_IMPORT_FAILED", http.StatusInternalServerError
	var upstream *arxivsource.LookupError
	if errors.As(err, &upstream) {
		code = "ARXIV_" + strings.ToUpper(upstream.Code)
		status = upstream.StatusCode
		switch upstream.Code {
		case "invalid_input":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		}
		if status < 400 || status > 599 {
			status = 502
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code, status = "ARXIV_TIMEOUT", http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		code, status = "ARXIV_CANCELLED", http.StatusRequestTimeout
	}
	body := map[string]any{"code": code, "message": err.Error()}
	if result.DocumentID != "" {
		body["document_id"] = result.DocumentID
		body["document_task_id"] = result.DocumentTaskID
		body["processing_status"] = result.ProcessingStatus
		body["processing_error"] = result.ProcessingError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type arxivDocumentMarker struct {
	Paper           arxivsource.Metadata `json:"paper"`
	TaskID          string               `json:"task_id"`
	ContentSHA256   string               `json:"content_sha256"`
	ProcessingError string               `json:"processing_error,omitempty"`
	FailureAt       time.Time            `json:"failure_at,omitempty"`
}
type arxivDocumentExt struct {
	StoredPath string               `json:"stored_path"`
	Marker     *arxivDocumentMarker `json:"arxiv_import,omitempty"`
}

func (s *arxivImportService) importPaper(ctx context.Context, input, datasetID, userID, userName string) (ArxivImportResult, error) {
	paper, err := s.lookup(ctx, input)
	if err != nil {
		return ArxivImportResult{}, err
	}
	// Trust provider metadata only, and pin the actual version before downloading.
	identifier, err := arxivsource.Parse(paper.BaseID + paper.Version)
	if err != nil || identifier.Version == "" || identifier.BaseID != paper.BaseID || strings.TrimSpace(paper.Title) == "" {
		return ArxivImportResult{}, errors.New("arXiv 返回的题录缺少可靠的编号、版本或标题")
	}
	paper.AbsURL, paper.PDFURL = identifier.AbsURL(), identifier.PDFURL()
	// Parent identity is shared across datasets and revisions, so serialize by
	// base identifier; attachment lookup below still matches dataset + version.
	unlock, err := lockArxivImport(ctx, s.db, identifier.BaseID)
	if err != nil {
		return ArxivImportResult{}, err
	}
	defer unlock()
	items, err := s.library(ctx, datasetID)
	if err != nil {
		return ArxivImportResult{}, err
	}
	for _, item := range items {
		if item.Paper.BaseID == paper.BaseID && item.SourceVersion == paper.Version {
			return alreadyPresentArxiv(item)
		}
	}
	// Recover a document left behind if metadata/link persistence previously failed.
	if orphan, ok, findErr := s.findUnlinkedImport(ctx, datasetID, paper); findErr != nil {
		return ArxivImportResult{}, findErr
	} else if ok {
		result, linkErr := s.associate(ctx, paper, orphan, doc.LocalFileImportResult{}, nil)
		if linkErr != nil {
			return result, linkErr
		}
		return alreadyPresentArxiv(result)
	}
	path, _, err := s.acquire(ctx, FulltextCandidate{URL: paper.PDFURL, SourceType: "pdf", SourceProvider: "arxiv", Version: paper.Version, ExpectedMIME: "application/pdf"})
	if err != nil {
		return ArxivImportResult{}, fmt.Errorf("arXiv PDF 下载失败：%w", err)
	}
	defer os.Remove(path)
	filename := safePaperFilename(paper.Title+" [arXiv "+identifier.ID()+"]") + ".pdf"
	imported, importErr := s.importFile(ctx, doc.LocalFileImportRequest{DatasetID: datasetID, SourcePath: path, DisplayName: filename, ContentType: "application/pdf", DataSourceType: "ARXIV_IMPORT", Tags: []string{"paper", "arxiv:" + identifier.ID()}, UserID: userID, UserName: userName})
	importErr = validatePaperImportResult(imported, importErr)
	if importErr == nil && (strings.EqualFold(imported.Start.Status, "FAILED") || strings.EqualFold(imported.Start.SubmitStatus, "FAILED") || strings.EqualFold(imported.Start.SubmitStatus, "REJECTED")) {
		importErr = errors.New(firstNonEmpty(imported.Start.Message, imported.Start.Detail, "文献处理任务未能启动"))
	}
	partial := ArxivImportResult{Paper: paper, DocumentID: imported.DocumentID, DocumentTaskID: imported.TaskID, ProcessingStatus: "failed"}
	if imported.DocumentID == "" {
		return partial, fmt.Errorf("arXiv 文件入库失败：%w", importErr)
	}
	// The file may exist despite processing submission failure. Keep its identity
	// recoverable, while still returning the original failure to the caller.
	var document orm.Document
	if err = s.db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", imported.DocumentID, datasetID).Take(&document).Error; err != nil {
		return partial, errors.Join(importErr, fmt.Errorf("无法确认已保存文献：%w", err))
	}
	result, linkErr := s.associate(ctx, paper, document, imported, importErr)
	if linkErr != nil {
		return result, errors.Join(importErr, linkErr)
	}
	if importErr != nil {
		return result, fmt.Errorf("PDF 已保存，但处理未成功，请在文档任务中重试：%w", importErr)
	}
	if result.ProcessingStatus == "failed" {
		return result, fmt.Errorf("PDF 已保存，但处理未成功，请在文档任务中重试：%s", result.ProcessingError)
	}
	return result, nil
}

func alreadyPresentArxiv(item ArxivImportResult) (ArxivImportResult, error) {
	item.Status = "already_present"
	if item.ProcessingStatus == "failed" {
		return item, fmt.Errorf("PDF 已在库中，但处理失败，请在文档任务中重试：%s", item.ProcessingError)
	}
	return item, nil
}

func (s *arxivImportService) associate(ctx context.Context, paper arxivsource.Metadata, document orm.Document, imported doc.LocalFileImportResult, processingErr error) (ArxivImportResult, error) {
	result := ArxivImportResult{Paper: paper, DocumentID: document.ID, DocumentTaskID: imported.TaskID, Filename: document.DisplayName, SourceURL: paper.PDFURL, SourceVersion: paper.Version, ContentSHA256: imported.ContentSHA256, Status: "imported", ProcessingStatus: "parsing"}
	ext, size, exists, err := readArxivDocument(document)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, errors.New("导入未产生可读取的 PDF 附件，请重试")
	}
	result.Size = size
	marker := arxivDocumentMarker{Paper: paper, TaskID: imported.TaskID, ContentSHA256: imported.ContentSHA256}
	if ext.Marker != nil && imported.DocumentID == "" {
		marker = *ext.Marker
		marker.Paper = paper
		result.DocumentTaskID = marker.TaskID
		result.ContentSHA256 = marker.ContentSHA256
	}
	if marker.ContentSHA256 == "" {
		file, openErr := os.Open(ext.StoredPath)
		if openErr != nil {
			return result, openErr
		}
		hash := sha256.New()
		_, hashErr := io.Copy(hash, file)
		closeErr := file.Close()
		if hashErr != nil || closeErr != nil {
			return result, errors.Join(hashErr, closeErr)
		}
		marker.ContentSHA256 = hex.EncodeToString(hash.Sum(nil))
		result.ContentSHA256 = marker.ContentSHA256
	}
	if processingErr != nil {
		marker.ProcessingError = processingErr.Error()
		marker.FailureAt = time.Now().UTC()
		result.ProcessingStatus = "failed"
		result.ProcessingError = processingErr.Error()
	}
	var values map[string]json.RawMessage
	if err = json.Unmarshal(document.Ext, &values); err != nil {
		return result, fmt.Errorf("无法读取已保存文献元数据：%w", err)
	}
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	values["arxiv_import"], _ = json.Marshal(marker)
	encoded, _ := json.Marshal(values)
	// Store the recovery marker before the work/link transaction. A failed link
	// can then be retried without downloading or creating a second document.
	if err = s.db.WithContext(ctx).Model(&orm.Document{}).Where("id = ? AND dataset_id = ?", document.ID, document.DatasetID).Update("ext", encoded).Error; err != nil {
		return result, fmt.Errorf("文件已保存，恢复标记保存失败，请重试导入：%w", err)
	}
	document.Ext = encoded
	var work orm.AcademicWork
	var link orm.AcademicWorkDocument
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		input := WorkInput{Title: paper.Title, Authors: paper.Authors, Year: paper.Year, Venue: paper.Venue, Abstract: paper.Abstract, DOI: paper.DOI, ArxivID: paper.BaseID, Provider: "arxiv", ProviderWorkID: paper.BaseID, Provenance: map[string]any{"provider": "arxiv", "pdf_url": paper.PDFURL, "source_version": paper.Version, "doi": paper.DOI}}
		var existing orm.AcademicWork
		findErr := tx.Where("arxiv_id_base = ?", paper.BaseID).Take(&existing).Error
		if findErr == nil {
			input.WorkID = existing.ID
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		} else if doi := NormalizeDOI(paper.DOI); doi != "" {
			findErr = tx.Where("doi_normalized = ?", doi).Take(&existing).Error
			if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
				return findErr
			}
			if findErr == nil && existing.ArxivIDBase != "" && existing.ArxivIDBase != paper.BaseID {
				// A publisher DOI can be shared by separate arXiv submissions.
				// Preserve it in provenance/the attachment snapshot, without
				// silently relabelling this PDF as the other arXiv identifier.
				input.DOI = ""
			}
		}
		var workErr error
		work, _, workErr = findOrCreateWork(ctx, tx, input)
		if workErr != nil {
			return workErr
		}
		now := time.Now().UTC()
		link = orm.AcademicWorkDocument{AcademicWorkID: work.ID, DatasetID: document.DatasetID, DocumentID: document.ID, VersionKind: "preprint", SourceProvider: "arxiv", SourceLocator: paper.PDFURL, SourceVersion: paper.Version, ContentSHA256: marker.ContentSHA256, MatchMethod: "arxiv_identifier", MatchConfidence: 1, IsPreferredVersion: true, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error
	})
	if err != nil {
		return result, fmt.Errorf("PDF 已保存，文献关联保存失败，请重新导入以恢复：%w", err)
	}
	return s.makeResult(ctx, work, link, document, "imported")
}

func readArxivDocument(document orm.Document) (arxivDocumentExt, int64, bool, error) {
	var ext arxivDocumentExt
	if err := json.Unmarshal(document.Ext, &ext); err != nil {
		return ext, 0, false, fmt.Errorf("文献 %s 的文件信息损坏：%w", document.ID, err)
	}
	if ext.StoredPath == "" {
		return ext, 0, false, nil
	}
	info, err := os.Stat(ext.StoredPath)
	if errors.Is(err, os.ErrNotExist) {
		return ext, 0, false, nil
	}
	if err != nil {
		return ext, 0, false, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return ext, 0, false, nil
	}
	return ext, info.Size(), true, nil
}

func (s *arxivImportService) library(ctx context.Context, datasetID string) ([]ArxivImportResult, error) {
	var links []orm.AcademicWorkDocument
	err := s.db.WithContext(ctx).Model(&orm.AcademicWorkDocument{}).Joins("JOIN documents d ON d.id = academic_work_documents.document_id AND d.dataset_id = academic_work_documents.dataset_id").Where("academic_work_documents.dataset_id = ? AND academic_work_documents.source_provider = ? AND d.deleted_at IS NULL", datasetID, "arxiv").Order("academic_work_documents.created_at DESC").Find(&links).Error
	if err != nil {
		return nil, err
	}
	items := make([]ArxivImportResult, 0, len(links))
	for _, link := range links {
		var work orm.AcademicWork
		var document orm.Document
		if err = s.db.WithContext(ctx).Where("id = ?", link.AcademicWorkID).Take(&work).Error; err != nil {
			return nil, err
		}
		if work.ArxivIDBase == "" {
			continue
		}
		if err = s.db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", link.DocumentID, datasetID).Take(&document).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		_, _, exists, fileErr := readArxivDocument(document)
		if fileErr != nil {
			return nil, fileErr
		}
		if !exists {
			continue
		}
		item, resultErr := s.makeResult(ctx, work, link, document, "already_present")
		if resultErr != nil {
			return nil, resultErr
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *arxivImportService) makeResult(ctx context.Context, work orm.AcademicWork, link orm.AcademicWorkDocument, document orm.Document, status string) (ArxivImportResult, error) {
	ext, size, _, err := readArxivDocument(document)
	if err != nil {
		return ArxivImportResult{}, err
	}
	paper := arxivsource.Metadata{Title: work.CanonicalTitle, Year: work.PublicationYear, Abstract: work.Abstract, DOI: work.DOINormalized, Venue: work.Venue, BaseID: work.ArxivIDBase, Version: link.SourceVersion}
	if err = json.Unmarshal(work.AuthorsJSON, &paper.Authors); err != nil {
		return ArxivImportResult{}, err
	}
	if ext.Marker != nil && ext.Marker.Paper.BaseID == work.ArxivIDBase && ext.Marker.Paper.Version == link.SourceVersion {
		paper = ext.Marker.Paper
	}
	identifier, parseErr := arxivsource.Parse(paper.BaseID + paper.Version)
	if parseErr != nil {
		return ArxivImportResult{}, parseErr
	}
	paper.AbsURL, paper.PDFURL = identifier.AbsURL(), identifier.PDFURL()
	result := ArxivImportResult{Paper: paper, WorkID: work.ID, DocumentID: document.ID, Filename: document.DisplayName, Size: size, SourceURL: link.SourceLocator, SourceVersion: link.SourceVersion, ContentSHA256: link.ContentSHA256, Status: status}
	result.DocumentTaskID, result.ProcessingStatus, result.ProcessingError, err = s.processingStatus(ctx, document, ext.Marker)
	return result, err
}

func (s *arxivImportService) processingStatus(ctx context.Context, document orm.Document, marker *arxivDocumentMarker) (string, string, string, error) {
	var task orm.Task
	err := s.db.WithContext(ctx).Where("doc_id = ? AND dataset_id = ? AND deleted_at IS NULL", document.ID, document.DatasetID).Order("updated_at DESC, created_at DESC").Take(&task).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", "", err
	}
	var taskExt struct {
		State string `json:"task_state"`
		Error string `json:"error_message"`
	}
	if task.ID != "" {
		if err = json.Unmarshal(task.Ext, &taskExt); err != nil {
			return "", "", "", err
		}
	}
	external, externalFound, err := arxivExternalTask(ctx, task, document)
	if err != nil {
		return task.ID, "", "", fmt.Errorf("无法读取文献解析任务状态：%w", err)
	}
	if marker != nil && marker.ProcessingError != "" && (task.ID == "" || task.ID == marker.TaskID && !task.UpdatedAt.After(marker.FailureAt)) && (!externalFound || !external.UpdatedAt.After(marker.FailureAt)) {
		return firstNonEmpty(task.ID, marker.TaskID), "failed", marker.ProcessingError, nil
	}
	// Core task ext records submission state. The parsing service owns terminal
	// states in schema-B; reading only Core would leave completed jobs running
	// forever and conceal asynchronous failures after a successful submission.
	if externalFound {
		failure := ""
		if external.ErrorMsg != nil {
			failure = strings.TrimSpace(*external.ErrorMsg)
		}
		// Parser callbacks also persist error_code=200/error_msg=success for
		// ordinary WORKING and SUCCESS acknowledgements. Only that literal
		// acknowledgement is non-error; real messages/codes remain failures.
		if strings.EqualFold(failure, "success") {
			failure = ""
		}
		if external.ErrorCode != nil {
			code := strings.TrimSpace(*external.ErrorCode)
			if code != "" && code != "200" {
				failure = firstNonEmpty(failure, "文献处理失败（"+code+"）")
			}
		}
		state := doc.NormalizeTaskStateForUI(external.Status)
		if failure != "" || state == "FAILED" || state == "CANCELED" {
			return task.ID, "failed", firstNonEmpty(failure, "文献处理失败或已取消，请在文档任务中重试"), nil
		}
		if state == "SUCCESS" {
			return task.ID, "ready", "", nil
		}
		return task.ID, "parsing", "", nil
	}
	if strings.EqualFold(taskExt.State, "FAILED") || taskExt.Error != "" {
		return task.ID, "failed", firstNonEmpty(taskExt.Error, "文献处理失败，请在文档任务中重试"), nil
	}
	var processing orm.DocumentProcessingState
	err = s.db.WithContext(ctx).Where("dataset_id = ? AND document_id = ?", document.DatasetID, document.ID).Take(&processing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return task.ID, "", "", err
	}
	for _, stage := range []struct{ status, message string }{{processing.ParseStatus, processing.ParseErrorMessage}, {processing.ChunkStatus, processing.ChunkErrorMessage}, {processing.IndexStatus, processing.IndexErrorMessage}} {
		if stage.status == "failed" || stage.message != "" {
			return task.ID, "failed", firstNonEmpty(stage.message, "文献处理失败，请在文档任务中重试"), nil
		}
	}
	switch strings.ToUpper(taskExt.State) {
	case "CREATING", "UPLOADING", "UPLOADED", "RUNNING", "SUSPENDED":
		return task.ID, "parsing", "", nil
	case "SUCCEEDED":
		return task.ID, "ready", "", nil
	case "CANCELED", "CANCELLED":
		return task.ID, "failed", "文献处理已取消，请在文档任务中重试", nil
	}
	var dataset orm.Dataset
	if err = s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", document.DatasetID).Take(&dataset).Error; err != nil {
		return task.ID, "", "", err
	}
	if dataset.ProcessingLevel == "stored" {
		return task.ID, "ready", "", nil
	}
	return task.ID, "parsing", "", nil
}

// Match the current external task first. Legacy imports without a task binding
// may use their latest document task, but reparses must not inherit an earlier
// attempt's successful state. Query failures are not an absent task.
func arxivExternalTask(ctx context.Context, task orm.Task, document orm.Document) (readonlyorm.LazyLLMDocServiceTaskRow, bool, error) {
	var row readonlyorm.LazyLLMDocServiceTaskRow
	lazyTask, lazyDocument := strings.TrimSpace(task.LazyllmTaskID), strings.TrimSpace(document.LazyllmDocID)
	if lazyTask == "" && lazyDocument == "" {
		return row, false, nil
	}
	db := store.LazyLLMDB()
	if db == nil {
		return row, false, errors.New("文献解析状态数据库未连接")
	}
	if lazyTask != "" {
		query := db.WithContext(ctx).Where("task_id = ?", lazyTask)
		if lazyDocument != "" {
			query = query.Where("doc_id = ?", lazyDocument)
		}
		err := query.Take(&row).Error
		if err == nil {
			return row, true, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, false, err
		}
	}
	if lazyDocument != "" && doc.TaskType(strings.TrimSpace(task.TaskType)) != doc.TaskTypeReparse {
		err := db.WithContext(ctx).Where("doc_id = ?", lazyDocument).Order("updated_at DESC").Take(&row).Error
		if err == nil {
			return row, true, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, false, err
		}
	}
	return row, false, nil
}

func (s *arxivImportService) findUnlinkedImport(ctx context.Context, datasetID string, paper arxivsource.Metadata) (orm.Document, bool, error) {
	var documents []orm.Document
	if err := s.db.WithContext(ctx).Where("dataset_id = ? AND deleted_at IS NULL", datasetID).Find(&documents).Error; err != nil {
		return orm.Document{}, false, err
	}
	for _, document := range documents {
		var ext arxivDocumentExt
		if json.Unmarshal(document.Ext, &ext) != nil {
			continue
		}
		match := ext.Marker != nil && ext.Marker.Paper.BaseID == paper.BaseID && ext.Marker.Paper.Version == paper.Version
		if !match {
			var tags []string
			_ = json.Unmarshal(document.Tags, &tags)
			for _, tag := range tags {
				if tag == "arxiv:"+paper.BaseID+paper.Version {
					match = true
				}
			}
			if match {
				var task orm.Task
				err := s.db.WithContext(ctx).Where("doc_id = ? AND dataset_id = ? AND deleted_at IS NULL", document.ID, datasetID).Order("created_at DESC").Take(&task).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return document, false, err
				}
				var meta struct {
					Source string `json:"data_source_type"`
				}
				_ = json.Unmarshal(task.Ext, &meta)
				match = meta.Source == "ARXIV_IMPORT"
			}
		}
		if !match {
			continue
		}
		_, _, exists, err := readArxivDocument(document)
		if err != nil {
			return document, false, err
		}
		if exists {
			return document, true, nil
		}
	}
	return orm.Document{}, false, nil
}
