package doc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"io"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type paperReadingHandler struct {
	model formulaExplanationHandler
}

func ExplainPDFVisual(w http.ResponseWriter, r *http.Request) { defaultVisualHandler().ServeHTTP(w, r) }
func (h paperReadingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := loadPDFDocument(r, false)
	if !ok {
		formulaExplanationError(w, 404, "Document not found or access denied.")
		return
	}
	request := paperReadingRequest{}
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if err := decoder.Decode(&request); err != nil && err != io.EOF {
			formulaExplanationError(w, 400, "Invalid reading request.")
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			formulaExplanationError(w, 400, "Only one JSON request is allowed.")
			return
		}
	}
	if request.Language == "" {
		request.Language = "zh"
	}
	if request.Language != "zh" && request.Language != "en" {
		formulaExplanationError(w, 400, "Explanation language must be zh or en.")
		return
	}
	result, cache, err := h.read(r, request)
	if err != nil {
		replyReadingServiceError(w, err)
		return
	}
	w.Header().Set("X-Paper-Cache", cache)
	common.ReplyOK(w, result)
}

type paperReadingRequest struct {
	Language     string          `json:"language"`
	Force        bool            `json:"force"`
	SourceSHA256 json.RawMessage `json:"source_sha256"`
}

func (h paperReadingHandler) read(r *http.Request, request paperReadingRequest) (response any, cache string, resultErr error) {
	row, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		return nil, "", formulaExplanationFailure{404, "Document not found or access denied."}
	}
	expectedHash := ""
	if len(request.SourceSHA256) > 0 {
		if json.Unmarshal(request.SourceSHA256, &expectedHash) != nil || len(expectedHash) != 64 {
			return nil, "", formulaExplanationFailure{400, "source_sha256 must be a 64-character hexadecimal hash."}
		}
		if _, err := hex.DecodeString(expectedHash); err != nil {
			return nil, "", formulaExplanationFailure{400, "source_sha256 must be a 64-character hexadecimal hash."}
		}
		expectedHash = strings.ToLower(expectedHash)
	}
	hash, err := pdfIRHash(ext)
	if err != nil {
		return nil, "", formulaExplanationFailure{409, "The original PDF is unavailable. Reopen the document."}
	}
	if expectedHash != "" && expectedHash != hash {
		return nil, "", formulaExplanationFailure{409, "The displayed PDF source is no longer current. Reopen the document before continuing."}
	}
	state, err := pdfIRStatus(r.Context(), row, hash)
	if err != nil {
		return nil, "", formulaExplanationFailure{500, "Cannot read the PDF structure. Retry later."}
	}
	if state.Status != "ready" || state.Document == nil || state.Document.Version != 1 || state.Document.CoordinateSpace != "normalized_top_left" {
		return nil, "", formulaExplanationFailure{409, "Parse the original PDF before using the paper reader."}
	}
	d := *state.Document
	var evidence []formulaEvidence
	var target pdfir.Block
	blockID := mux.Vars(r)["block"]
	input := map[string]any{}
	instruction := ""
	target, evidence, err = paperVisualContext(d, blockID)
	if err != nil {
		return nil, "", formulaExplanationFailure{404, "Image or table not found in the current PDF structure."}
	}
	input["target_block"] = map[string]any{"id": target.ID, "type": target.Type, "page": target.Page}
	input["source_blocks"] = evidence
	instruction = algo.VisualExplanationTask
	input["language"] = request.Language
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var config map[string]any
	var modelName string
	key := paperVisualCacheKey(d, blockID, request.Language, evidence)
	dir := filepath.Join(filepath.Dir(pdfIRSource(ext)), ".lazymind-artifacts", "paper-reading", pdfIRResource(row.DatasetID, row.ID), pdfir.Digest(store.UserID(r)))
	path := filepath.Join(dir, key+".json")
	validate := func(raw string) (any, error) {
		result, e := validatePaperVisual(raw, evidence, request.Language)
		result.Model = modelName
		result.SourceSHA256 = hash
		return result, e
	}
	readCache := func(cachePath string) (any, bool) {
		f, e := os.Open(cachePath)
		if e != nil {
			return nil, false
		}
		defer f.Close()
		raw, e := io.ReadAll(io.LimitReader(f, paperReadingMaxBytes+1))
		if e != nil {
			return nil, false
		}
		result, e := validate(string(raw))
		if e != nil {
			return nil, false
		}
		if visual, ok := result.(paperVisualExplanation); ok {
			// Only server-owned cache metadata may supply the original generation model.
			var saved struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(raw, &saved) == nil {
				visual.Model = saved.Model
			}
			result = visual
		}
		return result, true
	}
	if !request.Force {
		if result, ok := readCache(path); ok {
			return result, "hit", nil
		}
	}
	capability := readingpolicy.Figure
	if target.Type == "table" {
		capability = readingpolicy.Table
	}
	if err := readingpolicy.Check(ctx, store.DB(), store.UserID(r), capability); err != nil {
		return nil, "", formulaExplanationFailure{403, err.Error()}
	}
	config, _, modelName, err = h.model.loadModel(ctx, store.UserID(r))
	if err != nil {
		return nil, "", formulaExplanationFailure{422, "Select an available study model in AI reading preferences before using the paper reader."}
	}

	finish := startReadingWork(path)
	defer func() { finish(resultErr) }()
	content, _ := json.Marshal(input)
	raw := ""
	if len(evidence) == 0 {
		missing, _ := json.Marshal(map[string]any{"summary": paperUnavailable(request.Language), "observations": []any{}})
		raw = string(missing)
	} else {
		raw, err = h.model.generate(ctx, string(content), instruction, config)
	}
	if err != nil {
		return nil, "", err
	}
	result, err := validate(raw)
	if err != nil {
		return nil, "", formulaExplanationFailure{502, "The model returned an invalid reading response. Retry or choose another model."}
	}
	_, currentExt, _, ok := loadPDFDocument(r, false)
	if !ok {
		return nil, "", formulaExplanationFailure{404, "Document not found or access denied."}
	}
	currentHash, err := pdfIRHash(currentExt)
	if err != nil || currentHash != hash {
		return nil, "", formulaExplanationFailure{409, "The PDF changed while generating the response. Reparse it and retry."}
	}
	if ctx.Err() != nil {
		return nil, "", formulaExplanationFailure{504, "Paper reading request timed out. Retry later."}
	}
	if err := savePaperReading(dir, path, result); err != nil {
		switch v := result.(type) {
		case paperVisualExplanation:
			v.Warnings = append(v.Warnings, "The response could not be cached; a later request may regenerate it.")
			result = v
		}
	}
	return result, "miss", nil
}

func savePaperReading(dir, path string, result any) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "reading-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(result)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func isReadingModelTimeout(err error) bool {
	var taskError *algo.StructuredTaskError
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &taskError) && taskError.Code == "request_timeout")
}

func replyReadingServiceError(w http.ResponseWriter, err error) {
	if isReadingModelTimeout(err) {
		formulaExplanationError(w, 504, "The reading model did not finish within 2 minutes. Retry or choose another study model.")
		return
	}
	var failure formulaExplanationFailure
	if errors.As(err, &failure) {
		formulaExplanationError(w, failure.status, failure.message)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		formulaExplanationError(w, 504, "Reading request timed out.")
		return
	}
	formulaExplanationError(w, 502, "The selected model could not complete this reading request.")
}
