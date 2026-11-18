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

type embeddingIndexClassification struct {
	IndexIdentity   string
	OperationPhase  string
	ReadinessReason string
	StrictlyCleared bool
}

// classifyEmbeddingIndexState is the single interpretation of persisted
// vector ownership and maintenance state. Display selections are deliberately
// absent from its inputs: only signed index state can prove vector identity.
func classifyEmbeddingIndexState(state orm.EmbeddingIndexState, statePresent bool, affectedKnowledgeBases, pendingKnowledgeBases int64) embeddingIndexClassification {
	phase := strings.TrimSpace(state.Phase)
	identityResidue := statePresent && embeddingIndexStateHasIdentityResidue(state)
	strictlyCleared := statePresent && phase == "cleared" && !identityResidue
	validIdentity := statePresent && strings.TrimSpace(state.ActiveModelID) != "" &&
		validEmbeddingIndexSource(state.ActiveSource) && validEmbeddingFingerprint(strings.TrimSpace(state.Signature))

	classification := embeddingIndexClassification{
		IndexIdentity: "empty", OperationPhase: "idle", StrictlyCleared: strictlyCleared,
	}
	if strictlyCleared {
		classification.IndexIdentity = "empty"
	} else if validIdentity {
		classification.IndexIdentity = "verified"
	} else if identityResidue || affectedKnowledgeBases > 0 {
		classification.IndexIdentity = "legacy_unknown"
	}

	switch phase {
	case "":
		if statePresent {
			classification.OperationPhase = "cleanup_failed"
			classification.ReadinessReason = "index_cleanup_incomplete"
		}
	case "ready":
		if validIdentity && pendingKnowledgeBases > 0 {
			classification.OperationPhase = "rebuilding"
		}
	case "detached":
		classification.IndexIdentity = "legacy_unknown"
		classification.ReadinessReason = "index_identity_unverified"
	case "clearing":
		classification.OperationPhase = "clearing"
		classification.ReadinessReason = "index_cleanup_in_progress"
	case "cleanup_failed":
		classification.OperationPhase = "cleanup_failed"
		classification.ReadinessReason = "index_cleanup_incomplete"
	case "cleared":
		if strictlyCleared {
			classification.OperationPhase = "cleared"
			classification.ReadinessReason = "index_cleared"
		} else {
			classification.OperationPhase = "cleanup_failed"
			classification.ReadinessReason = "index_cleanup_incomplete"
		}
	default:
		classification.OperationPhase = "cleanup_failed"
		classification.ReadinessReason = "index_cleanup_incomplete"
	}
	if classification.ReadinessReason == "" && classification.IndexIdentity == "legacy_unknown" {
		classification.ReadinessReason = "index_identity_unverified"
	}
	return classification
}

func validEmbeddingIndexSource(source string) bool {
	switch strings.TrimSpace(source) {
	case "own", "cloud":
		return true
	default:
		return false
	}
}

func embeddingIndexStateHasIdentityResidue(state orm.EmbeddingIndexState) bool {
	for _, value := range []string{
		state.ActiveModelID,
		state.ActiveSource,
		state.ActiveModelName,
		state.ActiveProviderName,
		state.Signature,
		state.TargetModelID,
		state.TargetSource,
		state.TargetSignature,
	} {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// EmbeddingIndexStatusResponse describes the text-vector space, not merely
// whether a supplier can currently answer embedding requests.
type EmbeddingIndexStatusResponse struct {
	Generation             int64  `json:"generation"`
	Status                 string `json:"status" enum:"empty,indexed,rebuilding,clearing,cleared,unknown"`
	IndexIdentity          string `json:"index_identity" enum:"empty,verified,legacy_unknown"`
	OperationPhase         string `json:"operation_phase" enum:"idle,rebuilding,clearing,cleanup_failed,cleared"`
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
	statePresent := err == nil
	counts, err := loadEmbeddingIndexKnowledgeBaseCounts(ctx, db)
	if err != nil {
		return EmbeddingIndexStatusResponse{}, err
	}
	classification := classifyEmbeddingIndexState(state, statePresent, counts.Affected, counts.Pending)
	status := EmbeddingIndexStatusResponse{
		Generation:    state.Generation,
		IndexIdentity: classification.IndexIdentity, OperationPhase: classification.OperationPhase,
		ModelName: state.ActiveModelName, ProviderName: state.ActiveProviderName,
		IndexedKnowledgeBases: counts.Indexed, PendingKnowledgeBases: counts.Pending, AffectedKnowledgeBases: counts.Affected,
	}
	if !statePresent && db.Migrator().HasTable(&orm.UserSelectedModel{}) {
		choice, present, choiceErr := ResolveGlobalEmbeddingChoice(ctx, db)
		if choiceErr != nil {
			return EmbeddingIndexStatusResponse{}, choiceErr
		}
		if present {
			status.ModelName, status.ProviderName = choice.ModelName, choice.ProviderName
		}
	}
	switch {
	case state.Phase == "clearing" || state.Phase == "cleanup_failed":
		status.Status = "clearing"
	case state.Phase == "cleared":
		status.Status = "cleared"
	case classification.IndexIdentity == "legacy_unknown":
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

type embeddingIndexKnowledgeBaseCounts struct {
	Affected int64
	Indexed  int64
	Pending  int64
}

func loadEmbeddingIndexKnowledgeBaseCounts(ctx context.Context, db *gorm.DB) (embeddingIndexKnowledgeBaseCounts, error) {
	counts := embeddingIndexKnowledgeBaseCounts{}
	if !db.Migrator().HasTable(&orm.Dataset{}) {
		return counts, nil
	}
	nonIndexedLevels := []string{"stored", "parsed", "chunked"}
	if !db.Migrator().HasTable(&orm.Document{}) {
		// Partial legacy schemas cannot prove an empty inventory. Retain their
		// dataset-level evidence until document inventory becomes available.
		err := db.WithContext(ctx).Table("datasets d").
			Where("d.deleted_at IS NULL AND d.processing_level NOT IN ?", nonIndexedLevels).
			Distinct("d.id").Count(&counts.Affected).Error
		counts.Indexed = counts.Affected
		return counts, err
	}
	if !db.Migrator().HasTable(&orm.DocumentProcessingState{}) {
		query := db.WithContext(ctx).Table("datasets d").
			Joins("JOIN documents doc ON doc.dataset_id = d.id AND doc.deleted_at IS NULL").
			Where("doc.document_type IS NULL OR doc.document_type <> ?", "FOLDER").
			Where("d.deleted_at IS NULL AND d.processing_level NOT IN ?", nonIndexedLevels).
			Distinct("d.id")
		if err := query.Count(&counts.Affected).Error; err != nil {
			return embeddingIndexKnowledgeBaseCounts{}, err
		}
		counts.Indexed = counts.Affected
		return counts, nil
	}
	// A target indexed level is enough to require a conservative safety check,
	// but it does not mean that vectors are ready for display.
	if err := db.WithContext(ctx).Table("datasets d").
		Joins("JOIN documents doc ON doc.dataset_id = d.id AND doc.deleted_at IS NULL").
		Joins("LEFT JOIN document_processing_states s ON s.dataset_id = d.id AND s.document_id = doc.id").
		Where("doc.document_type IS NULL OR doc.document_type <> ?", "FOLDER").
		Where("d.deleted_at IS NULL AND (d.processing_level NOT IN ? OR s.index_status = ?)", nonIndexedLevels, "succeeded").
		Distinct("d.id").Count(&counts.Affected).Error; err != nil {
		return embeddingIndexKnowledgeBaseCounts{}, err
	}
	// Treat a legacy indexed KB with no processing-state rows as potentially
	// ready, while keeping known pending/failed KBs out of the ready count.
	if err := db.WithContext(ctx).Table("datasets d").
		Joins("JOIN documents doc ON doc.dataset_id = d.id AND doc.deleted_at IS NULL").
		Joins("LEFT JOIN document_processing_states s ON s.dataset_id = d.id AND s.document_id = doc.id").
		Where("doc.document_type IS NULL OR doc.document_type <> ?", "FOLDER").
		Where("d.deleted_at IS NULL AND (s.index_status = ? OR (s.dataset_id IS NULL AND d.processing_level NOT IN ?))", "succeeded", nonIndexedLevels).
		Distinct("d.id").Count(&counts.Indexed).Error; err != nil {
		return embeddingIndexKnowledgeBaseCounts{}, err
	}
	if err := db.WithContext(ctx).Table("datasets d").
		Joins("JOIN document_processing_states s ON s.dataset_id = d.id").
		Joins("JOIN documents doc ON doc.dataset_id = d.id AND doc.id = s.document_id AND doc.deleted_at IS NULL").
		Where("doc.document_type IS NULL OR doc.document_type <> ?", "FOLDER").
		Where("d.deleted_at IS NULL AND d.processing_level NOT IN ? AND s.index_status <> ? AND s.embedding_version NOT IN ?", nonIndexedLevels, "succeeded", []string{"reembed:manual", "rebuild:manual"}).
		Distinct("d.id").Count(&counts.Pending).Error; err != nil {
		return embeddingIndexKnowledgeBaseCounts{}, err
	}
	return counts, nil
}
