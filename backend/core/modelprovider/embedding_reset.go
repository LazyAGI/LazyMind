package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type EmbeddingClearRequest struct {
	ModelID            string `json:"model_id"`
	Source             string `json:"source"`
	Generation         int64  `json:"generation"`
	ConfirmClearChunks bool   `json:"confirm_clear_chunks"`
}

type EmbeddingClearResponse struct {
	Cleared   bool `json:"cleared"`
	Activated bool `json:"activated"`
}

var errEmbeddingIndexBusy = errors.New("embedding_index_busy")
var errEmbeddingCleanupFailed = errors.New("embedding_cleanup_failed")

// ClearEmbeddingChunks owns the confirmed cleanup and target activation. Once
// accepted, both steps finish independently of the browser connection. A retry
// resumes the persisted target without purging an already-cleared index again.
// Parsing remains an explicit, separate user action.
func ClearEmbeddingChunks(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", 503)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" || !common.RequestUserIsAdmin(r) {
		common.ReplyErr(w, "admin role required", 403)
		return
	}
	var req EmbeddingClearRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || !req.ConfirmClearChunks || (req.Source != "own" && req.Source != "") {
		common.ReplyErr(w, "explicit chunk clearing confirmation is required", 400)
		return
	}
	// A completed response-loss retry needs no upstream call. The committed
	// generation and owned active identity are authoritative, even if the
	// provider has since become temporarily unavailable.
	state, err := loadGlobalEmbeddingState(r.Context(), db)
	if err != nil {
		common.ReplyErr(w, "embedding state unavailable", 503)
		return
	}
	if req.ModelID == "" {
		// Recovery must work without any usable model credential. Only the
		// generation and explicit administrator confirmation authorize a purge.
		if state.Phase == "cleared" && state.Generation == req.Generation+1 && !embeddingIndexStateHasIdentityResidue(state) {
			common.ReplyOK(w, EmbeddingClearResponse{Cleared: true})
			return
		}
		if state.Generation != req.Generation || state.Phase == "clearing" {
			common.ReplyErr(w, errEmbeddingIndexBusy.Error(), 409)
			return
		}
		if err := clearEmbeddingChunks(r.Context(), db, state, "", "", "", false); err != nil {
			common.ReplyErr(w, err.Error(), 409)
			return
		}
		common.ReplyOK(w, EmbeddingClearResponse{Cleared: true})
		return
	}
	if req.Source == "" {
		req.Source = "own"
	}
	if state.Phase == "ready" && state.Generation == req.Generation+1 && state.ActiveModelID == req.ModelID && state.ActiveSource == req.Source && validEmbeddingFingerprint(state.Signature) {
		var owned int64
		if err := db.WithContext(r.Context()).Model(&orm.UserModelProviderGroupModel{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", req.ModelID, userID).Count(&owned).Error; err != nil || owned != 1 {
			common.ReplyErr(w, errEmbeddingIndexBusy.Error(), 409)
			return
		}
		common.ReplyOK(w, EmbeddingClearResponse{Cleared: true, Activated: true})
		return
	}
	_, signature, _, snapshot, err := probeSelectedEmbedding(r.Context(), db, userID, req.ModelID, req.Source)
	if err != nil || signature == "" || snapshot == nil {
		common.ReplyErr(w, errEmbeddingSignatureFailed.Error(), 409)
		return
	}
	target := embeddingSwitchActivation{UserID: userID, UserName: store.UserName(r), ModelID: req.ModelID, Snapshot: *snapshot}
	if err = target.run(r.Context(), db, req, signature); err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, EmbeddingClearResponse{Cleared: true, Activated: true})
}

func clearEmbeddingChunks(ctx context.Context, db *gorm.DB, previous orm.EmbeddingIndexState, modelID, source, signature string, emptyOnly bool) error {
	return resetEmbeddingChunks(ctx, db, previous, modelID, source, signature, emptyOnly, nil)
}

func resetEmbeddingChunks(ctx context.Context, db *gorm.DB, previous orm.EmbeddingIndexState, modelID, source, signature string, emptyOnly bool, activation *embeddingSwitchActivation) error {
	// A generation fences late submissions/callbacks even after a process restart.
	next := previous
	next.Scope, next.Phase, next.Generation = globalEmbeddingScope, "clearing", previous.Generation+1
	resuming := activation != nil && (previous.Phase == "clearing" || previous.Phase == "cleanup_failed") && previous.TargetModelID == modelID && previous.TargetSource == source && previous.TargetSignature == signature
	if resuming {
		// Retrying an interrupted purge is idempotent under the algorithm lock.
		next.Generation = previous.Generation
	}
	next.TargetModelID, next.TargetSource, next.TargetSignature = modelID, source, signature
	next.UpdatedAt = time.Now().UTC()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if activation != nil {
			if _, _, err := activation.validate(ctx, tx); err != nil {
				return err
			}
		}
		var count int64
		if err := tx.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", globalEmbeddingScope).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return tx.Create(&next).Error
		}
		query := tx.Model(&orm.EmbeddingIndexState{}).Where("scope = ? AND generation = ? AND phase = ?", globalEmbeddingScope, previous.Generation, previous.Phase)
		if previous.Phase == "clearing" && !resuming {
			return errEmbeddingIndexBusy
		}
		result := query.Select("*").Updates(&next)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errEmbeddingIndexBusy
		}
		return nil
	}); err != nil {
		return errEmbeddingIndexBusy
	}

	payload, _ := json.Marshal(map[string]any{"generation": next.Generation, "empty_only": emptyOnly})
	// Complete or record an incomplete cleanup even if the browser disconnects.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cleanupCtx, http.MethodPost, common.ChatServiceEndpoint()+"/internal/knowledge:clear-chunks", bytes.NewReader(payload))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-LazyMind-Internal-Token", strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")))
		var resp *http.Response
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusConflict {
				// The algorithm guarantees this response is sent before deleting anything.
				previous.Scope = globalEmbeddingScope
				restore := db.WithContext(cleanupCtx).Model(&orm.EmbeddingIndexState{}).Where("scope = ? AND generation = ? AND phase = ?", globalEmbeddingScope, next.Generation, "clearing").Select("*").Updates(&previous)
				if restore.Error == nil && restore.RowsAffected == 1 {
					return errEmbeddingIndexBusy
				}
			} else if resp.StatusCode == http.StatusOK {
				var result struct {
					Cleared    bool  `json:"cleared"`
					Generation int64 `json:"generation"`
				}
				if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Cleared && result.Generation == next.Generation {
					fields := map[string]any{
						"active_model_id": "", "active_source": "", "active_model_name": "", "active_provider_name": "", "signature": "", "phase": "cleared", "updated_at": time.Now().UTC(),
					}
					if activation == nil {
						fields["target_model_id"], fields["target_source"], fields["target_signature"] = "", "", ""
					}
					finish := db.WithContext(cleanupCtx).Model(&orm.EmbeddingIndexState{}).Where("scope = ? AND generation = ? AND phase = ?", globalEmbeddingScope, next.Generation, "clearing").Updates(fields)
					if finish.Error == nil && finish.RowsAffected == 1 {
						return nil
					}
				}
			}
		}
	}
	// Never expose a partially cleared index as usable or switch models on failure.
	db.WithContext(context.WithoutCancel(ctx)).Model(&orm.EmbeddingIndexState{}).Where("scope = ? AND generation = ? AND phase = ?", globalEmbeddingScope, next.Generation, "clearing").Updates(map[string]any{"phase": "cleanup_failed", "updated_at": time.Now().UTC()})
	return errEmbeddingCleanupFailed
}
