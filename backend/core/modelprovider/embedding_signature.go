package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/modelsettings"
	"lazymind/core/store"
)

const embeddingSignatureTimeout = 90 * time.Second

var embeddingSignaturePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// EmbeddingSignatureResponse contains only digests of the two runtime paths.
// It never includes model output, provider response text, endpoints, or keys.
type EmbeddingSignatureResponse struct {
	Status           string `json:"status"`
	Code             string `json:"code"`
	ModelType        string `json:"model_type"`
	Dimensions       int    `json:"dimensions,omitempty"`
	PassageSignature string `json:"passage_signature,omitempty"`
	QuerySignature   string `json:"query_signature,omitempty"`
}

// SignGroupEmbeddingModel fingerprints a saved, owned text embedding model
// through the same algorithm runtime used to encode knowledge-base passages.
func SignGroupEmbeddingModel(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var empty struct{}
		if err := decoder.Decode(&empty); err != nil && !errors.Is(err, io.EOF) {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	if vars["model_provider_id"] == CloudSystemProviderID {
		// The cloud route has no local candidate runtime to probe. A caller must
		// treat this as an unverified switch, never as a matching signature.
		common.ReplyOK(w, EmbeddingSignatureResponse{Status: "unsupported", Code: "unsupported_provider", ModelType: "embed"})
		return
	}
	snapshot, unavailable, err := loadProbeSnapshot(r.Context(), db, userID, vars["model_provider_id"], vars["group_id"], vars["model_id"])
	if err != nil {
		probeLookupError(w, err, "model not found")
		return
	}
	if snapshot.ModelType != "embed" && unavailable == nil {
		common.ReplyOK(w, EmbeddingSignatureResponse{Status: "unsupported", Code: "unsupported_type", ModelType: "embed"})
		return
	}
	if unavailable != nil {
		common.ReplyOK(w, EmbeddingSignatureResponse{Status: unavailable.Status, Code: unavailable.Code, ModelType: "embed"})
		return
	}
	probeKey := userID + "\x00" + vars["model_id"]
	if _, busy := runningModelProbes.LoadOrStore(probeKey, struct{}{}); busy {
		common.ReplyOK(w, EmbeddingSignatureResponse{Status: "failed", Code: "probe_in_progress", ModelType: "embed"})
		return
	}
	defer runningModelProbes.Delete(probeKey)

	// Snapshot is retained for the post-request race check. Normalize only the
	// copy sent to the LazyLLM runtime, matching document-indexing requests.
	runtime := snapshot
	runtime.Source = LazyLLMSource(snapshot.Source)
	runtime.URL = LazyLLMBaseURL(snapshot.Source, snapshot.URL)
	result := executeEmbeddingSignature(r.Context(), runtime, embeddingSignatureTimeout)
	if result.Status == "ready" {
		current, unavailable, err := loadProbeSnapshot(r.Context(), db, userID, vars["model_provider_id"], vars["group_id"], vars["model_id"])
		if err != nil || unavailable != nil || current != snapshot {
			result = EmbeddingSignatureResponse{Status: "failed", Code: "configuration_changed", ModelType: "embed"}
		}
	}
	common.ReplyOK(w, result)
}

func executeEmbeddingSignature(ctx context.Context, snapshot modelProbeSnapshot, timeout time.Duration) EmbeddingSignatureResponse {
	result := EmbeddingSignatureResponse{Status: "failed", Code: "connection_error", ModelType: "embed"}
	settings, err := modelsettings.Decode(snapshot.ModelSettings)
	if err != nil {
		result.Code = "invalid_request"
		return result
	}
	body, err := json.Marshal(struct {
		modelProbeSnapshot
		RequestAdvanced json.RawMessage                 `json:"request_advanced,omitempty"`
		Embedding       *modelsettings.EmbeddingOptions `json:"embedding,omitempty"`
	}{snapshot, json.RawMessage(snapshot.RequestAdvanced), settings.Embedding})
	if err != nil {
		result.Code = "invalid_request"
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/model/embedding-signature"), bytes.NewReader(body))
	if err != nil {
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		var timeoutErr interface{ Timeout() bool }
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			result.Code = "timeout"
		}
		return result
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Code = "upstream_error"
		return result
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, modelProbeResponseLimit+1))
	if err != nil || len(raw) > modelProbeResponseLimit {
		result.Code = "invalid_response"
		return result
	}
	var upstream EmbeddingSignatureResponse
	if json.Unmarshal(raw, &upstream) != nil {
		result.Code = "invalid_response"
		return result
	}
	if upstream.Status == "ready" && upstream.Code == "ok" && upstream.Dimensions > 0 && upstream.Dimensions <= 65536 &&
		embeddingSignaturePattern.MatchString(upstream.PassageSignature) && embeddingSignaturePattern.MatchString(upstream.QuerySignature) {
		result.Status, result.Code = "ready", "ok"
		result.Dimensions = upstream.Dimensions
		result.PassageSignature = upstream.PassageSignature
		result.QuerySignature = upstream.QuerySignature
		return result
	}
	if validModelProbeOutcome(upstream.Status, upstream.Code) && (upstream.Status == "failed" || upstream.Status == "unsupported") {
		result.Status, result.Code = upstream.Status, upstream.Code
		return result
	}
	result.Code = "invalid_response"
	return result
}
