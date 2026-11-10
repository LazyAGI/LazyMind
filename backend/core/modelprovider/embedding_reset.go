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
	Cleared bool `json:"cleared"`
}

var errEmbeddingIndexBusy = errors.New("embedding_index_busy")
var errEmbeddingCleanupFailed = errors.New("embedding_cleanup_failed")

// ClearEmbeddingChunks requires a fresh preview and explicit confirmation.
// It does not enqueue parsing tasks. Selection still goes through the normal
// signed-model validation after this irreversible operation succeeds.
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
	if decoder.Decode(&req) != nil || !req.ConfirmClearChunks || req.ModelID == "" || req.Source != "own" {
		common.ReplyErr(w, "explicit chunk clearing confirmation is required", 400)
		return
	}
	_, signature, _, snapshot, err := probeSelectedEmbedding(r.Context(), db, userID, req.ModelID, req.Source)
	if err != nil || signature == "" || snapshot == nil {
		common.ReplyErr(w, errEmbeddingSignatureFailed.Error(), 409)
		return
	}
	state, err := loadGlobalEmbeddingState(r.Context(), db)
	if err != nil {
		common.ReplyErr(w, "embedding state unavailable", 503)
		return
	}
	if state.Generation != req.Generation || state.Phase == "clearing" {
		common.ReplyErr(w, errEmbeddingIndexBusy.Error(), 409)
		return
	}
	if err = clearEmbeddingChunks(r.Context(), db, state, req.ModelID, req.Source, signature, false); err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, EmbeddingClearResponse{Cleared: true})
}

func clearEmbeddingChunks(ctx context.Context, db *gorm.DB, previous orm.EmbeddingIndexState, modelID, source, signature string, emptyOnly bool) error {
	// A generation fences late submissions/callbacks even after a process restart.
	next := previous
	next.Scope, next.Phase, next.Generation = globalEmbeddingScope, "clearing", previous.Generation+1
	next.TargetModelID, next.TargetSource, next.TargetSignature = modelID, source, signature
	next.UpdatedAt = time.Now().UTC()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&orm.EmbeddingIndexState{}).Where("scope = ?", globalEmbeddingScope).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return tx.Create(&next).Error
		}
		result := tx.Model(&orm.EmbeddingIndexState{}).Where("scope = ? AND generation = ? AND phase <> ?", globalEmbeddingScope, previous.Generation, "clearing").Select("*").Updates(&next)
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
					finish := db.WithContext(cleanupCtx).Model(&orm.EmbeddingIndexState{}).Where("scope = ? AND generation = ? AND phase = ?", globalEmbeddingScope, next.Generation, "clearing").Updates(map[string]any{
						"active_model_id": "", "active_source": "", "active_model_name": "", "active_provider_name": "", "signature": "", "target_model_id": "", "target_source": "", "target_signature": "", "phase": "cleared", "updated_at": time.Now().UTC(),
					})
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
