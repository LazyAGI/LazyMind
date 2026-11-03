package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/modelconfig"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"lazymind/core/store"
)

type formulaExplanationHandler struct {
	loadCacheIdentity func(context.Context, string) (string, error)
	loadModel         func(context.Context, string) (map[string]any, string, string, error)
	generate          func(context.Context, string, string, map[string]any) (string, error)
}

var formulaExplanationFlights = formulaExplanationFlightGroup{active: make(map[string]*formulaExplanationFlight)}

type formulaExplanationFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  formulaExplanation
	err     error
}

type formulaExplanationFlightGroup struct {
	mu     sync.Mutex
	active map[string]*formulaExplanationFlight
}

type formulaExplanationFailure struct {
	status  int
	message string
}

func (e formulaExplanationFailure) Error() string { return e.message }

// A caller can close its popup without canceling generation needed by another
// caller. The shared model request stops when the last waiter leaves.
func (group *formulaExplanationFlightGroup) do(ctx context.Context, key string, run func(context.Context) (formulaExplanation, error)) (formulaExplanation, bool, error) {
	group.mu.Lock()
	flight, shared := group.active[key]
	if shared {
		flight.waiters++
	} else {
		requestContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		flight = &formulaExplanationFlight{done: make(chan struct{}), cancel: cancel, waiters: 1}
		group.active[key] = flight
		go func() {
			flight.result, flight.err = run(requestContext)
			group.mu.Lock()
			if group.active[key] == flight {
				delete(group.active, key)
			}
			close(flight.done)
			group.mu.Unlock()
			cancel()
		}()
	}
	group.mu.Unlock()
	select {
	case <-flight.done:
		return flight.result, shared, flight.err
	case <-ctx.Done():
		group.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 && group.active[key] == flight {
			delete(group.active, key)
			flight.cancel()
		}
		group.mu.Unlock()
		return formulaExplanation{}, shared, ctx.Err()
	}
}

// ExplainPDFFormula uses the selected chat model only. It never initializes
// embedding, retrieval, translation, or the document ingestion pipeline.
func ExplainPDFFormula(w http.ResponseWriter, r *http.Request) {
	defaultFormulaHandler().ServeHTTP(w, r)
}
func loadFormulaExplanationModel(ctx context.Context, user string) (map[string]any, string, string, error) {
	// LoadChatRuntimeConfig/LoadLLMConfig also resolve non-chat roles. This path
	// deliberately resolves only the selected/default chat candidate.
	models, err := modelconfig.LoadAvailableChatModels(ctx, store.DB(), user)
	if err != nil {
		return nil, "", "", err
	}
	selected, err := modelconfig.ResolveDefaultChatModel(ctx, store.DB(), user, models)
	if err != nil || selected == nil {
		return nil, "", "", errors.New("selected model unavailable")
	}
	runtime := &modelconfig.ChatRuntimeConfig{Models: models}
	candidate := runtime.PrepareCandidate(ctx, *selected)
	if candidate == nil {
		return nil, "", "", errors.New("selected model credential unavailable")
	}
	// No key, endpoint, or user path is ever returned in the public result.
	identity, _ := json.Marshal([]any{candidate.CandidateID, candidate.CredentialRevision, selected.ModelName, selected.BaseURL, selected.ModelSettings})
	return candidate.LLMConfig, pdfir.Digest(string(identity)), selected.ModelName, nil
}

// Cache migration needs only selection metadata. It must not prepare credentials,
// resolve a cloud runtime session, or construct a runnable model configuration.
func loadFormulaCacheIdentity(ctx context.Context, user string) (string, error) {
	models, err := modelconfig.LoadAvailableChatModels(ctx, store.DB(), user)
	if err != nil {
		return "", err
	}
	selected, err := modelconfig.ResolveDefaultChatModel(ctx, store.DB(), user, models)
	if err != nil || selected == nil {
		return "", errors.New("selected model unavailable")
	}
	identity, _ := json.Marshal([]any{modelconfig.ChatCandidateID(selected.Source, selected.ID), selected.CredentialRevision, selected.ModelName, selected.BaseURL, selected.ModelSettings})
	return pdfir.Digest(string(identity)), nil
}
func formulaExplanationError(w http.ResponseWriter, status int, message string) {
	kind := "Internal server error"
	switch status {
	case 400, 422:
		kind = "Invalid request"
	case 404:
		kind = "Resource not found"
	case 409:
		kind = "Conflict"
	case 502, 503, 504:
		kind = "Upstream service error"
	}
	common.ReplyErrWithData(w, kind, map[string]any{"error_message": message}, status)
}

func (h formulaExplanationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	_, _, _, ok := loadPDFDocument(r, false)
	if !ok {
		formulaExplanationError(w, 404, "Document not found or access denied.")
		return
	}
	request := formulaReadingRequest{}
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if err := decoder.Decode(&request); err != nil && err != io.EOF {
			formulaExplanationError(w, 400, "Invalid explanation request.")
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
	result, cache, err := h.explain(r, request)
	if err != nil {
		replyReadingServiceError(w, err)
		return
	}
	w.Header().Set("Server-Timing", fmt.Sprintf("formula;dur=%d", time.Since(started).Milliseconds()))
	w.Header().Set("X-LazyMind-Formula-Cache", cache)
	common.ReplyOK(w, result)
}

type formulaReadingRequest struct {
	Force       bool    `json:"force"`
	Language    string  `json:"language"`
	FocusSymbol *string `json:"focus_symbol"`
}

func (h formulaExplanationHandler) explain(r *http.Request, request formulaReadingRequest) (formulaExplanation, string, error) {
	row, ext, _, ok := loadPDFDocument(r, false)
	if !ok {
		return formulaExplanation{}, "", formulaExplanationFailure{404, "Document not found or access denied."}
	}
	hash, err := pdfIRHash(ext)
	if err != nil {
		return formulaExplanation{}, "", formulaExplanationFailure{409, "The original PDF is unavailable. Reopen the document before requesting an explanation."}
	}
	state, err := pdfIRStatus(r.Context(), row, hash)
	if err != nil {
		return formulaExplanation{}, "", formulaExplanationFailure{500, "Cannot read the PDF structure. Retry later."}
	}
	if state.Status != "ready" || state.Document == nil {
		return formulaExplanation{}, "", formulaExplanationFailure{409, "Parse the original PDF before requesting a formula explanation."}
	}
	if state.Document.Version != 1 || state.Document.CoordinateSpace != "normalized_top_left" {
		return formulaExplanation{}, "", formulaExplanationFailure{409, "Reparse this PDF to refresh its reading structure."}
	}
	formula, evidence, err := formulaExplanationContext(*state.Document, mux.Vars(r)["block"])
	if err != nil {
		return formulaExplanation{}, "", formulaExplanationFailure{404, "Formula not found in the current PDF structure. Reopen the formula."}
	}
	if len([]rune(formula.Latex)) > 8000 {
		return formulaExplanation{}, "", formulaExplanationFailure{422, "This formula exceeds the explanation size limit."}
	}
	if request.FocusSymbol != nil && (strings.TrimSpace(*request.FocusSymbol) == "" || utf8.RuneCountInString(*request.FocusSymbol) > 500 || !strings.Contains(formula.Latex, *request.FocusSymbol)) {
		return formulaExplanation{}, "", formulaExplanationFailure{400, "The focus symbol must be a nonblank exact part of the target formula, at most 500 characters."}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	key := formulaExplanationCacheKey(*state.Document, formula.ID, formula.Latex, "saved-explanation:"+request.Language, evidence)
	if request.FocusSymbol != nil {
		focusedKey, _ := json.Marshal([]string{key, *request.FocusSymbol})
		key = pdfir.Digest(string(focusedKey))
	}
	cacheDir := filepath.Join(filepath.Dir(pdfIRSource(ext)), ".lazymind-artifacts", "formula-explanations", pdfIRResource(row.DatasetID, row.ID), pdfir.Digest(store.UserID(r)))
	cachePath := filepath.Join(cacheDir, key+".json")
	cachePaths := []string{cachePath}
	readCache := func() (formulaExplanation, bool) {
		for _, candidatePath := range cachePaths {
			if f, openErr := os.Open(candidatePath); openErr == nil {
				b, readErr := io.ReadAll(io.LimitReader(f, formulaExplanationMaxBytes+1))
				f.Close()
				if readErr == nil {
					if cached, parseErr := validateCachedFormulaExplanation(string(b), evidence); parseErr == nil && bindFormulaExplanationFocus(&cached, request.FocusSymbol) {
						// Preserve the model that actually generated this saved explanation.
						cached.Warnings = formulaExplanationWarnings(request.Language)
						if candidatePath != cachePath {
							_ = saveFormulaExplanation(cacheDir, cachePath, cached)
						}
						return cached, true
					}
				}
			}
		}
		return formulaExplanation{}, false
	}
	if !request.Force {
		if cached, ok := readCache(); ok {
			return cached, "hit", nil
		}
	}
	legacyCachePath := func(modelID string) string {
		legacyKey := formulaExplanationCacheKey(*state.Document, formula.ID, formula.Latex, modelID+":"+request.Language, evidence)
		if request.FocusSymbol != nil {
			focused, _ := json.Marshal([]string{legacyKey, *request.FocusSymbol})
			legacyKey = pdfir.Digest(string(focused))
		}
		return filepath.Join(cacheDir, legacyKey+".json")
	}
	if !request.Force && h.loadCacheIdentity != nil {
		if identity, err := h.loadCacheIdentity(ctx, store.UserID(r)); err == nil {
			cachePaths = append(cachePaths, legacyCachePath(identity))
			if cached, ok := readCache(); ok {
				return cached, "hit", nil
			}
		}
	}
	if err := readingpolicy.Check(ctx, store.DB(), store.UserID(r), readingpolicy.Formula); err != nil {
		return formulaExplanation{}, "", formulaExplanationFailure{403, err.Error()}
	}
	config, modelID, modelName, err := h.loadModel(ctx, store.UserID(r))
	if err != nil {
		return formulaExplanation{}, "", formulaExplanationFailure{422, "Select an available chat model in model settings before explaining formulas."}
	}
	cachePaths = append(cachePaths, legacyCachePath(modelID))
	if !request.Force {
		if cached, ok := readCache(); ok {
			return cached, "hit", nil
		}
	}
	flightKey := cachePath
	if request.Force {
		flightKey += ":force:" + modelID
	}
	result, shared, flightErr := formulaExplanationFlights.do(ctx, flightKey, func(runCtx context.Context) (formulaExplanation, error) {
		if err := runCtx.Err(); err != nil {
			return formulaExplanation{}, err
		}
		// A second request may have passed the first disk check while another
		// request was finishing. Recheck inside the shared flight.
		if !request.Force {
			if cached, ok := readCache(); ok {
				return cached, nil
			}
		}
		sources := make([]map[string]string, 0, len(evidence))
		for _, item := range evidence {
			sources = append(sources, map[string]string{"id": item.ID, "text": item.Text})
		}
		inputFields := map[string]any{"target_formula": map[string]string{"id": formula.ID, "latex": formula.Latex}, "source_blocks": sources, "language": request.Language}
		task := algo.FormulaExplanationTask
		if request.FocusSymbol != nil {
			inputFields["target_symbol"] = *request.FocusSymbol
		}
		input, _ := json.Marshal(inputFields)
		timer := time.NewTimer(formulaBackupDelay)
		defer timer.Stop()
		result, err := runFormulaGeneration(runCtx, func(attemptCtx context.Context) (formulaExplanation, error) {
			if err := readingpolicy.Check(attemptCtx, store.DB(), store.UserID(r), readingpolicy.Formula); err != nil {
				return formulaExplanation{}, formulaExplanationFailure{403, err.Error()}
			}
			_, currentExt, _, authorized := loadPDFDocument(r.WithContext(attemptCtx), false)
			if !authorized {
				return formulaExplanation{}, formulaExplanationFailure{404, "Document not found or access denied."}
			}
			currentHash, sourceErr := pdfIRHash(currentExt)
			if sourceErr != nil || currentHash != hash {
				return formulaExplanation{}, formulaExplanationFailure{409, "The PDF changed before generation."}
			}
			raw, err := h.generate(attemptCtx, string(input), task, config)
			if err != nil {
				return formulaExplanation{}, formulaExplanationFailure{502, "The selected model could not explain this formula. Check the model connection and retry."}
			}
			result, err := validateFormulaExplanation(raw, evidence)
			if err != nil || !bindFormulaExplanationFocus(&result, request.FocusSymbol) {
				// Validation errors contain schema/limit diagnostics, never the model response.
				log.Printf("formula_explanation validation_failed: %v", err)
				return formulaExplanation{}, formulaExplanationFailure{502, "The model returned an invalid explanation. Retry with the selected model or choose another model."}
			}
			return result, nil
		}, timer.C)
		if err != nil {
			return formulaExplanation{}, err
		}
		// A generation may take minutes. Recheck ACL and the source before
		// saving a reusable result.
		_, currentExt, _, ok := loadPDFDocument(r.WithContext(runCtx), false)
		if !ok {
			return formulaExplanation{}, formulaExplanationFailure{404, "Document not found or access denied."}
		}
		currentHash, err := pdfIRHash(currentExt)
		if err != nil || currentHash != hash {
			return formulaExplanation{}, formulaExplanationFailure{409, "The PDF changed while generating the explanation. Reparse it and retry."}
		}
		if runCtx.Err() != nil {
			return formulaExplanation{}, formulaExplanationFailure{504, "Formula explanation timed out. Retry later."}
		}
		result.Model = modelName
		result.Warnings = formulaExplanationWarnings(request.Language)
		if err := saveFormulaExplanation(cacheDir, cachePath, result); err != nil {
			result.Warnings = append(result.Warnings, "The explanation could not be cached; a later request may regenerate it.")
		}
		return result, nil
	})
	if flightErr != nil {
		return formulaExplanation{}, "", flightErr
	}
	if shared {
		return result, "shared", nil
	}
	return result, "miss", nil
}

func saveFormulaExplanation(dir, path string, result formulaExplanation) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "explanation-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	b, err := json.Marshal(result)
	if err == nil {
		_, err = file.Write(b)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

// The requested source occurrence supplies the identity. A one-symbol response
// avoids ambiguity when the model normalizes equivalent LaTeX spelling. Its
// meaning is always marked inferred, even if a supplied source is quoted.
func bindFormulaExplanationFocus(result *formulaExplanation, focus *string) bool {
	if focus == nil {
		return true
	}
	if len(result.Symbols) != 1 {
		return false
	}
	result.Symbols[0].Symbol = *focus
	result.Symbols[0].Inferred = true
	return true
}

// Cache metadata was written by this server, not supplied by the model.
func validateCachedFormulaExplanation(raw string, evidence []formulaEvidence) (formulaExplanation, error) {
	result, err := validateFormulaExplanation(raw, evidence)
	if err != nil {
		return result, err
	}
	var metadata struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return formulaExplanation{}, err
	}
	result.Model = metadata.Model
	return result, nil
}

// Keep bounded formula definitions out of supported providers' default reasoning mode.
// This is request-local and never changes the user's saved chat settings.
func formulaExplanationGenerationConfig(config map[string]any) map[string]any {
	raw, err := json.Marshal(config)
	if err != nil {
		return config
	}
	var cloned map[string]any
	if json.Unmarshal(raw, &cloned) != nil {
		return config
	}
	role, ok := cloned["llm"].(map[string]any)
	if !ok {
		return config
	}
	endpoint, _ := role["base_url"].(string)
	parsed, err := url.Parse(endpoint)
	model, _ := role["model"].(string)
	if err != nil {
		return config
	}
	supported := parsed.Hostname() == "zenmux.ai" && strings.HasPrefix(model, "deepseek/deepseek-v4")
	if parsed.Hostname() == "api.deepseek.com" || parsed.Hostname() == "aihubmix.com" {
		supported = strings.HasPrefix(model, "deepseek-v4") || model == "deepseek-flash"
	}
	if !supported {
		return config
	}
	features, ok := role["features"].(map[string]any)
	if !ok {
		features = map[string]any{}
		role["features"] = features
	}
	features["reasoning"] = false
	return cloned
}
