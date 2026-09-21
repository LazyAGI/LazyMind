package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/translation"
)

const pdfTranslationJobType = "document_pdf_translation"

const (
	defaultTranslationConcurrency = 4
	translationChunkAttempts      = 3
)

var translationRetryBaseDelay = 500 * time.Millisecond

type pdfTranslationPayload struct {
	DatasetID      string `json:"dataset_id"`
	DocumentID     string `json:"document_id"`
	RenderJobID    string `json:"render_job_id"`
	SourcePath     string `json:"source_path"`
	LayoutPath     string `json:"layout_path"`
	TargetLanguage string `json:"target_language"`
	ProviderType   string `json:"provider_type"`
	OutputFilename string `json:"output_filename"`
}

type translationLayoutManifest struct {
	Version int `json:"version"`
	Blocks  []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"blocks"`
}

func RegisterPDFTranslationJobs() {
	asyncjob.Register(pdfTranslationJobType, handlePDFTranslationJob)
}

func saveMultipartInput(file multipart.File, path string, limit int64) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(file, limit))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func createBackendTranslationPDFJob(w http.ResponseWriter, r *http.Request) {
	row, ext, state, ok := loadPDFDocument(r, true)
	if !ok {
		common.ReplyErr(w, "document not found or forbidden", http.StatusNotFound)
		return
	}
	if err := r.ParseMultipartForm(160 << 20); err != nil {
		common.ReplyErr(w, "invalid multipart body", http.StatusBadRequest)
		return
	}
	req := createPDFJobRequest{
		TargetLanguage: r.FormValue("target_language"), ProviderType: r.FormValue("provider_type"),
		Provider: r.FormValue("provider"), Model: r.FormValue("model"), OptionsHash: r.FormValue("options_hash"),
		Force: strings.EqualFold(r.FormValue("force"), "true"),
	}
	if req.TargetLanguage == "" {
		req.TargetLanguage = "zh"
	}
	if req.ProviderType != "api" && req.ProviderType != "llm" {
		common.ReplyErr(w, "unsupported document translation provider", http.StatusBadRequest)
		return
	}
	// Markdown translation changed from paragraph replacement to a
	// syntax-preserving executor. Include its version in the cache key so
	// malformed artifacts produced by the legacy executor are not reused.
	if files := r.MultipartForm.File["source"]; len(files) > 0 {
		extension := strings.ToLower(filepath.Ext(files[0].Filename))
		if extension == ".md" || extension == ".markdown" {
			req.OptionsHash += "\x00markdown-structure-v2"
		}
	}
	key := pdfCacheKey(state, pdfArtifactTranslation, req)
	if !req.Force {
		if artifact := latestArtifactWithCacheKey(ext, pdfArtifactTranslation, key); artifact != nil {
			common.ReplyOK(w, map[string]any{"cache_status": "hit", "artifact": artifact})
			return
		}
		for _, job := range ext.PDFRenderJobs {
			if job.BackendManaged && job.Kind == pdfArtifactTranslation && job.CacheKey == key && job.Status != "FAILED" && job.Status != "CANCELLED" {
				common.ReplyOK(w, map[string]any{"cache_status": "running", "job": job})
				return
			}
		}
	}
	source, sourceHeader, err := r.FormFile("source")
	if err != nil {
		common.ReplyErr(w, "translation source is required", http.StatusBadRequest)
		return
	}
	defer source.Close()
	artifactDir := filepath.Join(filepath.Dir(previewPathForContent(ext)), ".lazymind-artifacts", row.ID)
	if err := os.MkdirAll(artifactDir, 0o750); err != nil {
		common.ReplyErr(w, "create artifact directory failed", http.StatusInternalServerError)
		return
	}
	renderJobID := uuid.NewString()
	extension := strings.ToLower(filepath.Ext(sourceHeader.Filename))
	allowed := map[string]bool{".pdf": true, ".docx": true, ".pptx": true, ".xlsx": true, ".md": true, ".markdown": true, ".txt": true, ".html": true, ".htm": true}
	if !allowed[extension] {
		common.ReplyErr(w, "unsupported backend translation format", http.StatusBadRequest)
		return
	}
	sourcePath := filepath.Join(artifactDir, renderJobID+".source"+extension)
	layoutPath := filepath.Join(artifactDir, renderJobID+".input-layout.json")
	if err := saveMultipartInput(source, sourcePath, 128<<20); err != nil {
		common.ReplyErr(w, "save translation source failed", http.StatusInternalServerError)
		return
	}
	if extension == ".pdf" {
		// PDF layout is deliberately extracted by the backend. Reader nodes do
		// not guarantee the typography and protected-region metadata required
		// for faithful in-place translation, and browser-generated manifests
		// would create a second, inconsistent parsing path.
	}
	now := time.Now().UTC().Format(time.RFC3339)
	jobRecord := pdfRenderJobRecord{ID: renderJobID, Kind: pdfArtifactTranslation, CacheKey: key, Status: pdfJobRunning,
		Stage: "QUEUED", Progress: 0, TargetLanguage: req.TargetLanguage, ProviderType: req.ProviderType,
		Provider: req.Provider, Model: req.Model, CreatedAt: now, UpdatedAt: now, BackendManaged: true}
	payload := pdfTranslationPayload{DatasetID: row.DatasetID, DocumentID: row.ID, RenderJobID: renderJobID,
		SourcePath: sourcePath, LayoutPath: layoutPath, TargetLanguage: req.TargetLanguage, ProviderType: req.ProviderType,
		OutputFilename: strings.TrimSuffix(sourceHeader.Filename, extension) + "-" + req.TargetLanguage + extension}
	queued, err := asyncjob.Enqueue(r.Context(), store.DB(), asyncjob.EnqueueRequest{JobType: pdfTranslationJobType,
		ResourceType: "pdf_render_job", ResourceID: renderJobID, IdempotencyKey: key, Payload: payload,
		MaxAttempts: 2, CreateUserID: store.UserID(r), SkipSucceeded: req.Force})
	if err != nil {
		_ = os.Remove(sourcePath)
		_ = os.Remove(layoutPath)
		common.ReplyErr(w, "enqueue translation job failed", http.StatusInternalServerError)
		return
	}
	_ = queued
	ext.PDFRenderJobs = append(ext.PDFRenderJobs, jobRecord)
	if err := saveDocumentExt(r, row, ext); err != nil {
		_, _ = asyncjob.CancelResourceJobs(r.Context(), store.DB(), pdfTranslationJobType, "pdf_render_job", renderJobID, "render job persistence failed")
		common.ReplyErr(w, "create pdf render job failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"cache_status": "miss", "job": jobRecord})
}

func updateTranslationRenderJob(ctx context.Context, payload pdfTranslationPayload, mutate func(*pdfRenderJobRecord, *documentExt) error) error {
	return store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row orm.Document
		if err := tx.Where("id = ? AND dataset_id = ?", payload.DocumentID, payload.DatasetID).Take(&row).Error; err != nil {
			return err
		}
		var ext documentExt
		_ = json.Unmarshal(row.Ext, &ext)
		for i := range ext.PDFRenderJobs {
			if ext.PDFRenderJobs[i].ID == payload.RenderJobID {
				if err := mutate(&ext.PDFRenderJobs[i], &ext); err != nil {
					return err
				}
				raw, err := json.Marshal(ext)
				if err != nil {
					return err
				}
				return tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ?", row.ID, row.DatasetID).Update("ext", raw).Error
			}
		}
		return errors.New("pdf render job not found")
	})
}

func splitRunes(value string, size int) []string {
	runes := []rune(value)
	var result []string
	for start := 0; start < len(runes); start += size {
		end := min(start+size, len(runes))
		result = append(result, string(runes[start:end]))
	}
	return result
}

type translationWorkUnit struct {
	ID   string
	Text string
}

type translationChunkTask struct {
	unitIndex  int
	chunkIndex int
	text       string
	weight     int64
}

type translationRateGate struct {
	mu   sync.Mutex
	next time.Time
}

func newTranslationRateGate(providerType string) *translationRateGate {
	if providerType != "api" {
		return nil
	}
	return &translationRateGate{}
}

func (gate *translationRateGate) Wait(ctx context.Context) error {
	if gate == nil {
		return nil
	}
	gate.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if gate.next.After(now) {
		wait = gate.next.Sub(now)
	}
	gate.next = now.Add(wait + 275*time.Millisecond)
	gate.mu.Unlock()
	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (gate *translationRateGate) Close() {}

func documentTranslationConcurrency() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("DOCUMENT_TRANSLATION_CONCURRENCY")))
	if err != nil || value <= 0 {
		return defaultTranslationConcurrency
	}
	return min(value, 16)
}

func translateChunkWithRetry(ctx context.Context, attempts int, translate func(context.Context) (string, error)) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		value, err := translate(ctx)
		if err == nil {
			return value, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil || attempt == attempts {
			break
		}
		delay := time.Duration(1<<(attempt-1)) * translationRetryBaseDelay
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", fmt.Errorf("translation failed after %d attempts: %w", attempts, lastErr)
}

func translateUnitsParallel(
	ctx context.Context,
	units []translationWorkUnit,
	chunkSize int,
	concurrency int,
	translate func(context.Context, string) (string, error),
	onProgress func(completed, total int64) error,
) (map[string]string, error) {
	if len(units) == 0 {
		return nil, errors.New("no translatable text units")
	}
	chunksByUnit := make([][]string, len(units))
	tasks := make([]translationChunkTask, 0, len(units))
	var total int64
	for unitIndex, unit := range units {
		chunks := splitRunes(unit.Text, chunkSize)
		chunksByUnit[unitIndex] = make([]string, len(chunks))
		for chunkIndex, chunk := range chunks {
			weight := int64(len([]rune(chunk)))
			total += weight
			tasks = append(tasks, translationChunkTask{unitIndex: unitIndex, chunkIndex: chunkIndex, text: chunk, weight: weight})
		}
	}
	if total == 0 {
		return nil, errors.New("no translatable text units")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan translationChunkTask)
	errCh := make(chan error, 1)
	var completed atomic.Int64
	var outputMu sync.Mutex
	var progressMu sync.Mutex
	worker := func() {
		for task := range work {
			value, err := translateChunkWithRetry(ctx, translationChunkAttempts, func(callCtx context.Context) (string, error) {
				return translate(callCtx, task.text)
			})
			if err != nil {
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}
			outputMu.Lock()
			chunksByUnit[task.unitIndex][task.chunkIndex] = value
			outputMu.Unlock()
			done := completed.Add(task.weight)
			progressMu.Lock()
			err = onProgress(done, total)
			progressMu.Unlock()
			if err != nil {
				select {
				case errCh <- err:
					cancel()
				default:
				}
				return
			}
		}
	}
	workerCount := min(max(1, concurrency), len(tasks))
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() { defer wg.Done(); worker() }()
	}
	go func() {
		defer close(work)
		for _, task := range tasks {
			select {
			case <-ctx.Done():
				return
			case work <- task:
			}
		}
	}()
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	result := make(map[string]string, len(units))
	for index, unit := range units {
		result[unit.ID] = strings.Join(chunksByUnit[index], "\n")
	}
	return result, nil
}

func translateDocumentChunk(ctx context.Context, userID, providerType, text, target string) (string, error) {
	if providerType == "api" {
		response, err := translation.TranslateText(ctx, userID, text, target)
		return response.TranslatedText, err
	}
	language := "简体中文"
	if target == "en" {
		language = "English"
	}
	conversationID := "document-translate-" + uuid.NewString()
	body := map[string]any{
		"action": "CHAT_CONVERSATIONS_ACTION_CREATE", "conversation_id": conversationID,
		"conversation": map[string]any{"display_name": "文档翻译", "search_config": map[string]any{"dataset_list": []string{}}},
		"models":       []string{"LazyMind"}, "surface": "knowledge_pdf_translation", "stream": false,
		"input":          []map[string]string{{"input_type": "text", "text": "把下面文本准确、自然地翻译成" + language + "。只输出译文，保留公式、编号、引用标记和专有名词，不要解释。\n\n" + text}},
		"thinking_depth": "low", "mode": "auto", "basic_chat_only": true,
		"initial_conversation_settings": map[string]bool{"enable_workflow": false, "enable_subagent": false, "ephemeral": true},
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LAZYMIND_PUBLIC_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8000/api/core"
	}
	var result struct {
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := common.ApiPost(ctx, baseURL+"/conversations:chat", body, map[string]string{"X-User-Id": userID}, &result, 10*time.Minute); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Data.Message) == "" {
		return "", errors.New("LLM translation returned empty text")
	}
	return strings.TrimSpace(result.Data.Message), nil
}

func handlePDFTranslationJob(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload pdfTranslationPayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil || payload.RenderJobID == "" {
		return asyncjob.Result{Permanent: true, ErrorCode: "invalid_payload"}, errors.New("invalid pdf translation payload")
	}
	fail := func(err error) (asyncjob.Result, error) {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return asyncjob.Result{Permanent: true, ErrorCode: asyncjob.ErrorCodeCanceled}, err
		}
		willRetry := job.AttemptCount < job.MaxAttempts
		_ = updateTranslationRenderJob(context.Background(), payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
			if willRetry {
				record.Status, record.Stage = pdfJobWaiting, "RETRYING"
			} else {
				record.Status, record.Stage = "FAILED", "FAILED"
			}
			record.ErrorMessage = err.Error()
			record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return nil
		})
		return asyncjob.Result{Permanent: false, ErrorCode: "document_translation_failed"}, err
	}
	if strings.ToLower(filepath.Ext(payload.SourcePath)) != ".pdf" {
		result, err := executeBackendDocumentTranslation(ctx, job, reporter, payload)
		if err != nil {
			return fail(err)
		}
		return result, nil
	}
	layoutServiceURL := strings.Replace(strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL")),
		"/v1/office/to-pdf", "/v1/pdf/extract-translation-layout", 1)
	if layoutServiceURL == "" {
		return fail(errors.New("PDF translation layout extractor is not configured"))
	}
	var layoutResult struct {
		OutputPath string `json:"output_path"`
		BlockCount int    `json:"block_count"`
	}
	if err := common.ApiPost(ctx, layoutServiceURL, map[string]string{
		"source_path": payload.SourcePath,
		"output_path": payload.LayoutPath,
	}, nil, &layoutResult, 5*time.Minute); err != nil {
		return fail(fmt.Errorf("extract PDF translation blocks: %w", err))
	}
	if layoutResult.BlockCount == 0 {
		return fail(errors.New("PDF translation layout extractor returned no blocks"))
	}
	rawLayout, err := os.ReadFile(payload.LayoutPath)
	if err != nil {
		return fail(err)
	}
	var manifest translationLayoutManifest
	if err := json.Unmarshal(rawLayout, &manifest); err != nil {
		return fail(fmt.Errorf("invalid layout manifest: %w", err))
	}
	translatable := make([]int, 0, len(manifest.Blocks))
	for i, block := range manifest.Blocks {
		if strings.TrimSpace(block.Text) != "" && !strings.Contains(strings.ToLower(block.Type), "image") && !strings.Contains(strings.ToLower(block.Type), "formula") {
			translatable = append(translatable, i)
		}
	}
	if len(translatable) == 0 {
		return fail(errors.New("no translatable text blocks"))
	}
	_ = updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
		record.Status, record.Stage, record.Progress, record.ErrorMessage, record.UpdatedAt = pdfJobRunning, "TRANSLATING", 3, "", time.Now().UTC().Format(time.RFC3339)
		return nil
	})
	units := make([]translationWorkUnit, 0, len(translatable))
	for _, index := range translatable {
		units = append(units, translationWorkUnit{ID: manifest.Blocks[index].ID, Text: manifest.Blocks[index].Text})
	}
	chunkSize := 1800
	if payload.ProviderType == "llm" {
		chunkSize = 5000
	}
	rateGate := newTranslationRateGate(payload.ProviderType)
	defer rateGate.Close()
	translations, err := translateUnitsParallel(ctx, units, chunkSize, documentTranslationConcurrency(),
		func(callCtx context.Context, text string) (string, error) {
			if err := rateGate.Wait(callCtx); err != nil {
				return "", err
			}
			return translateDocumentChunk(callCtx, job.CreateUserID, payload.ProviderType, text, payload.TargetLanguage)
		}, func(completed, total int64) error {
			if err := reporter.Heartbeat(ctx); err != nil {
				return err
			}
			_ = reporter.SetProgress(ctx, completed, total)
			progress := 5 + int(completed*62/total)
			return updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
				record.Stage, record.Progress, record.UpdatedAt = "TRANSLATING", progress, time.Now().UTC().Format(time.RFC3339)
				return nil
			})
		})
	if err != nil {
		return fail(err)
	}
	translationPath := strings.TrimSuffix(payload.LayoutPath, ".json") + ".translations.json"
	rawTranslations, _ := json.Marshal(translations)
	if err := os.WriteFile(translationPath, rawTranslations, 0o640); err != nil {
		return fail(err)
	}
	outputPath := filepath.Join(filepath.Dir(payload.SourcePath), payload.RenderJobID+".translated.pdf")
	_ = updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
		record.Stage, record.Progress, record.UpdatedAt = "RENDERING", 72, time.Now().UTC().Format(time.RFC3339)
		return nil
	})
	serviceURL := strings.Replace(strings.TrimSpace(os.Getenv("LAZYMIND_OFFICE_CONVERT_URL")), "/v1/office/to-pdf", "/v1/pdf/render-translation", 1)
	if serviceURL == "" {
		return fail(errors.New("PDF translation renderer is not configured"))
	}
	var renderResult struct {
		OutputPath          string `json:"output_path"`
		WarningCount        int    `json:"warning_count"`
		RenderedBlockCount  int    `json:"rendered_block_count"`
		RequestedBlockCount int    `json:"requested_block_count"`
	}
	if err := common.ApiPost(ctx, serviceURL, map[string]string{"source_path": payload.SourcePath, "layout_path": payload.LayoutPath,
		"translations_path": translationPath, "output_path": outputPath, "target_language": payload.TargetLanguage}, nil, &renderResult, 15*time.Minute); err != nil {
		return fail(err)
	}
	if renderResult.RequestedBlockCount == 0 || renderResult.RenderedBlockCount != renderResult.RequestedBlockCount {
		return fail(fmt.Errorf("PDF translation renderer produced %d of %d requested text blocks",
			renderResult.RenderedBlockCount, renderResult.RequestedBlockCount))
	}
	artifactID := uuid.NewString()
	err = updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, ext *documentExt) error {
		artifact := pdfArtifactRecord{ID: artifactID, Kind: pdfArtifactTranslation, CacheKey: record.CacheKey, StoredPath: outputPath,
			Filename: payload.OutputFilename, ContentType: "application/pdf", TargetLanguage: record.TargetLanguage,
			ProviderType: record.ProviderType, Provider: record.Provider, Model: record.Model, WarningCount: renderResult.WarningCount,
			CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		var replaced []pdfArtifactRecord
		ext.PDFArtifacts, replaced = replaceArtifactWithSameCacheKey(ext.PDFArtifacts, artifact)
		removeArtifactFiles(replaced)
		record.Status, record.Stage, record.Progress, record.ArtifactID = "READY", "READY", 100, artifactID
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
	if err != nil {
		return fail(err)
	}
	result, _ := json.Marshal(map[string]string{"artifact_id": artifactID})
	return asyncjob.Result{ResultJSON: result}, nil
}

func executeBackendDocumentTranslation(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter, payload pdfTranslationPayload) (asyncjob.Result, error) {
	executor, err := newBackendDocumentTranslationExecutor(payload.SourcePath)
	if err != nil {
		return asyncjob.Result{}, err
	}
	units := executor.Units()
	if len(units) == 0 {
		return asyncjob.Result{}, errors.New("no translatable text units")
	}
	_ = updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
		record.Status, record.Stage, record.Progress, record.ErrorMessage, record.UpdatedAt = pdfJobRunning, "TRANSLATING", 3, "", time.Now().UTC().Format(time.RFC3339)
		return nil
	})
	workUnits := make([]translationWorkUnit, 0, len(units))
	for _, unit := range units {
		workUnits = append(workUnits, translationWorkUnit{ID: unit.ID, Text: unit.Text})
	}
	chunkSize := 1800
	if payload.ProviderType == "llm" {
		chunkSize = 5000
	}
	rateGate := newTranslationRateGate(payload.ProviderType)
	defer rateGate.Close()
	translations, err := translateUnitsParallel(ctx, workUnits, chunkSize, documentTranslationConcurrency(),
		func(callCtx context.Context, text string) (string, error) {
			if err := rateGate.Wait(callCtx); err != nil {
				return "", err
			}
			return translateDocumentChunk(callCtx, job.CreateUserID, payload.ProviderType, text, payload.TargetLanguage)
		}, func(completed, total int64) error {
			if err := reporter.Heartbeat(ctx); err != nil {
				return err
			}
			_ = reporter.SetProgress(ctx, completed, total)
			progress := 5 + int(completed*70/total)
			return updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
				record.Stage, record.Progress, record.UpdatedAt = "TRANSLATING", progress, time.Now().UTC().Format(time.RFC3339)
				return nil
			})
		})
	if err != nil {
		return asyncjob.Result{}, err
	}
	extension := strings.ToLower(filepath.Ext(payload.SourcePath))
	outputPath := filepath.Join(filepath.Dir(payload.SourcePath), payload.RenderJobID+".translated"+extension)
	_ = updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, _ *documentExt) error {
		record.Stage, record.Progress, record.UpdatedAt = "RENDERING", 80, time.Now().UTC().Format(time.RFC3339)
		return nil
	})
	warnings, err := executor.Build(ctx, translations, outputPath)
	if err != nil {
		return asyncjob.Result{}, err
	}
	artifactID := uuid.NewString()
	err = updateTranslationRenderJob(ctx, payload, func(record *pdfRenderJobRecord, ext *documentExt) error {
		contentType := mime.TypeByExtension(extension)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		artifact := pdfArtifactRecord{ID: artifactID, Kind: pdfArtifactTranslation, CacheKey: record.CacheKey, StoredPath: outputPath,
			Filename: payload.OutputFilename, ContentType: contentType, TargetLanguage: record.TargetLanguage,
			ProviderType: record.ProviderType, Provider: record.Provider, Model: record.Model, WarningCount: warnings,
			CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		var replaced []pdfArtifactRecord
		ext.PDFArtifacts, replaced = replaceArtifactWithSameCacheKey(ext.PDFArtifacts, artifact)
		removeArtifactFiles(replaced)
		record.Status, record.Stage, record.Progress, record.ArtifactID = "READY", "READY", 100, artifactID
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
	if err != nil {
		return asyncjob.Result{}, err
	}
	result, _ := json.Marshal(map[string]string{"artifact_id": artifactID})
	return asyncjob.Result{ResultJSON: result}, nil
}
