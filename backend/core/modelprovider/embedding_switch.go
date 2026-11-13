package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

var (
	errEmbeddingSwitchNeedsClear = errors.New("embedding_switch_requires_index_clear")
	errEmbeddingSignatureFailed  = errors.New("embedding_signature_failed")
)

type EmbeddingSwitchPreviewRequest struct {
	ModelID string `json:"model_id"`
	Source  string `json:"source"`
}

// EmbeddingSwitchPreviewResponse is advisory. SetSelectedModels probes again and
// checks the current index generation before accepting a selection.
type EmbeddingSwitchPreviewResponse struct {
	Outcome               string `json:"outcome" enum:"unchanged,empty,same,clear_required,probe_failed"`
	IndexedKnowledgeBases int64  `json:"indexed_knowledge_bases"`
	ActiveModelName       string `json:"active_model_name,omitempty"`
	CandidateModelName    string `json:"candidate_model_name,omitempty"`
	SignatureStatus       string `json:"signature_status,omitempty"`
	Generation            int64  `json:"generation"`
	CanSwitch             bool   `json:"can_switch"`
}

func embeddingFingerprint(result EmbeddingSignatureResponse) string {
	if result.Status != "ready" || result.Code != "ok" || result.Dimensions <= 0 ||
		!embeddingSignaturePattern.MatchString(result.PassageSignature) || !embeddingSignaturePattern.MatchString(result.QuerySignature) {
		return ""
	}
	return fmt.Sprintf("v1:%d:%s:%s", result.Dimensions, result.PassageSignature, result.QuerySignature)
}

func assessEmbeddingSwitch(state orm.EmbeddingIndexState, indexed int64, modelID, source, fingerprint string) string {
	if state.Phase == "clearing" || state.Phase == "cleanup_failed" || state.Phase == "detached" {
		return "clear_required"
	}
	if state.Phase == "cleared" {
		// A completed purge writes this terminal phase. Document rows
		// remain in Core for manual parsing, so their count cannot veto the first
		// signed model selected after the purge.
		if modelID != "" && fingerprint != "" && state.ActiveModelID == "" &&
			state.ActiveSource == "" && state.Signature == "" &&
			state.TargetModelID == "" && state.TargetSource == "" && state.TargetSignature == "" {
			return "empty"
		}
		return "clear_required"
	}
	if indexed == 0 {
		return "empty"
	}
	if modelID == state.ActiveModelID && source == state.ActiveSource && state.Signature != "" && fingerprint == state.Signature {
		return "unchanged"
	}
	if state.Signature != "" && fingerprint != "" && fingerprint == state.Signature {
		return "same"
	}
	// An unverified legacy index, a failed probe, or a different fingerprint
	// cannot be treated as compatible even when model names or IDs agree.
	return "clear_required"
}

func loadGlobalEmbeddingState(ctx context.Context, db *gorm.DB) (orm.EmbeddingIndexState, error) {
	var state orm.EmbeddingIndexState
	err := db.WithContext(ctx).Where("scope = ?", globalEmbeddingScope).Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		choice, present, resolveErr := ResolveGlobalEmbeddingChoice(ctx, db)
		if resolveErr != nil {
			return orm.EmbeddingIndexState{}, resolveErr
		}
		legacy := orm.EmbeddingIndexState{Scope: globalEmbeddingScope}
		if present && choice.Legacy {
			legacy.ActiveModelID, legacy.ActiveSource = choice.ModelID, choice.Source
			legacy.ActiveModelName, legacy.ActiveProviderName = choice.ModelName, choice.ProviderName
		}
		return legacy, nil
	}
	return state, err
}

func probeSelectedEmbedding(ctx context.Context, db *gorm.DB, userID, modelID, source string) (string, string, string, *modelProbeSnapshot, error) {
	if modelID == "" {
		return "", "", "not_required", nil, nil
	}
	if source == "cloud" {
		return modelID, "", "unsupported", nil, nil
	}
	var model orm.UserModelProviderGroupModel
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", modelID, userID).Take(&model).Error; err != nil {
		return "", "", "", nil, err
	}
	if !isTextEmbeddingModelType(model.ModelType) {
		return "", "", "", nil, errSelectionModelType
	}
	snapshot, unavailable, err := loadProbeSnapshot(ctx, db, userID, model.UserModelProviderID, model.UserModelProviderGroupID, modelID)
	if err != nil {
		return "", "", "", nil, err
	}
	if unavailable != nil {
		return model.Name, "", unavailable.Code, nil, nil
	}
	if snapshot.ModelType != "embed" {
		return model.Name, "", "unsupported_type", nil, nil
	}
	runtime := snapshot
	runtime.Source = LazyLLMSource(snapshot.Source)
	runtime.URL = LazyLLMBaseURL(snapshot.Source, snapshot.URL)
	result := executeEmbeddingSignature(ctx, runtime, embeddingSignatureTimeout)
	if result.Status == "ready" {
		current, unavailable, err := loadProbeSnapshot(ctx, db, userID, model.UserModelProviderID, model.UserModelProviderGroupID, modelID)
		if err != nil || unavailable != nil || current != snapshot {
			return model.Name, "", "configuration_changed", nil, nil
		}
	}
	return model.Name, embeddingFingerprint(result), result.Code, &snapshot, nil
}

// PreviewEmbeddingModelSwitch previews a deployment-wide text vector switch.
// No client-supplied fingerprint can authorize a switch.
func PreviewEmbeddingModelSwitch(w http.ResponseWriter, r *http.Request) {
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
	if !common.RequestUserIsAdmin(r) {
		common.ReplyErr(w, "admin role required for global embedding selection", http.StatusForbidden)
		return
	}
	var req EmbeddingSwitchPreviewRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.ModelID = strings.TrimSpace(req.ModelID)
	req.Source = strings.ToLower(strings.TrimSpace(req.Source))
	if req.Source == "" {
		req.Source = "own"
	}
	if req.Source != "own" && req.Source != "cloud" {
		common.ReplyErr(w, "invalid model source", http.StatusBadRequest)
		return
	}
	name, signature, signatureStatus, _, err := probeSelectedEmbedding(r.Context(), db, userID, req.ModelID, req.Source)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errSelectionModelType) {
			common.ReplyErr(w, "model not found", http.StatusBadRequest)
		} else {
			common.ReplyErr(w, "embedding preview failed", http.StatusInternalServerError)
		}
		return
	}
	state, err := loadGlobalEmbeddingState(r.Context(), db)
	if err != nil {
		common.ReplyErr(w, "embedding preview failed", http.StatusInternalServerError)
		return
	}
	status, err := loadEmbeddingIndexStatus(r.Context(), db)
	if err != nil {
		common.ReplyErr(w, "embedding preview failed", http.StatusInternalServerError)
		return
	}
	outcome := assessEmbeddingSwitch(state, status.AffectedKnowledgeBases, req.ModelID, req.Source, signature)
	if req.ModelID != "" && signature == "" {
		outcome = "probe_failed"
	}
	if req.ModelID == "" && status.AffectedKnowledgeBases > 0 {
		outcome = "clear_required"
	}
	common.ReplyOK(w, EmbeddingSwitchPreviewResponse{
		Outcome: outcome, IndexedKnowledgeBases: status.IndexedKnowledgeBases,
		ActiveModelName: state.ActiveModelName, CandidateModelName: name,
		SignatureStatus: signatureStatus, Generation: state.Generation,
		CanSwitch: outcome == "empty" || outcome == "same" || outcome == "unchanged",
	})
}
