package modelprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const globalEmbeddingScope = "global"

// EmbeddingIndexStatusResponse describes the text-vector space, not merely
// whether a supplier can currently answer embedding requests.
type EmbeddingIndexStatusResponse struct {
	Status                 string `json:"status" enum:"empty,indexed,rebuilding,clearing,cleared,unknown"`
	ModelName              string `json:"model_name,omitempty"`
	ProviderName           string `json:"provider_name,omitempty"`
	IndexedKnowledgeBases  int64  `json:"indexed_knowledge_bases"`
	PendingKnowledgeBases  int64  `json:"pending_knowledge_bases"`
	AffectedKnowledgeBases int64  `json:"affected_knowledge_bases"`
}

func GetEmbeddingIndexState(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(store.UserID(r)) == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	status, err := loadEmbeddingIndexStatus(r.Context(), db)
	if err != nil {
		common.ReplyErr(w, "query embedding index state failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, status)
}

func loadEmbeddingIndexStatus(ctx context.Context, db *gorm.DB) (EmbeddingIndexStatusResponse, error) {
	var state orm.EmbeddingIndexState
	err := db.WithContext(ctx).Where("scope = ?", globalEmbeddingScope).Take(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return EmbeddingIndexStatusResponse{}, err
	}
	status := EmbeddingIndexStatusResponse{
		ModelName: state.ActiveModelName, ProviderName: state.ActiveProviderName,
	}
	if errors.Is(err, gorm.ErrRecordNotFound) && db.Migrator().HasTable(&orm.UserSelectedModel{}) {
		choice, present, choiceErr := ResolveGlobalEmbeddingChoice(ctx, db)
		if choiceErr != nil {
			return EmbeddingIndexStatusResponse{}, choiceErr
		}
		if present {
			status.ModelName, status.ProviderName = choice.ModelName, choice.ProviderName
		}
	}
	// A target indexed level is enough to require a conservative safety check,
	// but it does not mean that vectors are ready for display.
	nonIndexedLevels := []string{"stored", "parsed", "chunked"}
	if err := db.WithContext(ctx).Table("datasets d").
		Joins("JOIN documents doc ON doc.dataset_id = d.id AND doc.deleted_at IS NULL").
		Joins("LEFT JOIN document_processing_states s ON s.dataset_id = d.id AND s.document_id = doc.id").
		Where("d.deleted_at IS NULL").
		Distinct("d.id").Count(&status.AffectedKnowledgeBases).Error; err != nil {
		return EmbeddingIndexStatusResponse{}, err
	}
	// Treat a legacy indexed KB with no processing-state rows as potentially
	// ready, while keeping known pending/failed KBs out of the ready count.
	if err := db.WithContext(ctx).Table("datasets d").
		Joins("JOIN documents doc ON doc.dataset_id = d.id AND doc.deleted_at IS NULL").
		Joins("LEFT JOIN document_processing_states s ON s.dataset_id = d.id AND s.document_id = doc.id").
		Where("d.deleted_at IS NULL AND (s.index_status = ? OR (s.dataset_id IS NULL AND d.processing_level NOT IN ?))", "succeeded", nonIndexedLevels).
		Distinct("d.id").Count(&status.IndexedKnowledgeBases).Error; err != nil {
		return EmbeddingIndexStatusResponse{}, err
	}
	if err := db.WithContext(ctx).Table("datasets d").
		Joins("JOIN document_processing_states s ON s.dataset_id = d.id").
		Joins("JOIN documents doc ON doc.id = s.document_id AND doc.dataset_id = d.id AND doc.deleted_at IS NULL").
		Where("d.deleted_at IS NULL AND d.processing_level NOT IN ? AND s.index_status <> ? AND s.embedding_version <> ?", nonIndexedLevels, "succeeded", "reembed:manual").
		Distinct("d.id").Count(&status.PendingKnowledgeBases).Error; err != nil {
		return EmbeddingIndexStatusResponse{}, err
	}
	switch {
	case state.Phase == "clearing" || state.Phase == "cleanup_failed":
		status.Status = "clearing"
	case state.Phase == "cleared":
		status.Status = "cleared"
	case state.ActiveModelID != "" && !validEmbeddingFingerprint(state.Signature):
		status.Status = "unknown"
	case status.AffectedKnowledgeBases > 0 && !validEmbeddingFingerprint(state.Signature):
		status.Status = "unknown"
	case status.PendingKnowledgeBases > 0 && state.ActiveModelID != "":
		status.Status = "rebuilding"
	case status.IndexedKnowledgeBases > 0:
		status.Status = "indexed"
	default:
		status.Status = "empty"
	}
	return status, nil
}
