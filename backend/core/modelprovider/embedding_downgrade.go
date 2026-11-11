package modelprovider

import (
	"errors"
	"strings"
	"time"

	"lazymind/core/common/orm"

	"gorm.io/gorm"
)

func isTextEmbeddingModelType(modelType string) bool {
	switch strings.ToLower(strings.TrimSpace(modelType)) {
	case "embed", "embedding", "embed_main":
		return true
	default:
		return false
	}
}

var errEmbeddingInUse = errors.New("embedding model is in use; its knowledge-base vectors must be cleared before removal")

const (
	embeddingConflictActiveIndexOwner        = "active_index_owner"
	embeddingConflictLegacySelectedOwner     = "legacy_selected_owner"
	embeddingConflictIndexIdentityUnverified = "index_identity_unverified"
)

type embeddingDestructiveOperationError struct {
	Reason string
	cause  error
}

func newEmbeddingDestructiveOperationError(cause error, reason string) error {
	switch reason {
	case embeddingConflictActiveIndexOwner, embeddingConflictLegacySelectedOwner, embeddingConflictIndexIdentityUnverified:
	default:
		reason = embeddingConflictIndexIdentityUnverified
	}
	return &embeddingDestructiveOperationError{Reason: reason, cause: cause}
}

func (err *embeddingDestructiveOperationError) Error() string {
	if err == nil || err.cause == nil {
		return errEmbeddingInUse.Error()
	}
	return err.cause.Error()
}

func (err *embeddingDestructiveOperationError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

type embeddingDestructiveClassification struct {
	BlockAllReason string
	OwnerSource    string
	OwnerModelID   string
	OwnerReason    string
}

type legacyEmbeddingIdentity struct {
	Source  string
	ModelID string
}

// classifyEmbeddingDestructiveState interprets the same signed index evidence
// as the readiness API, then considers every persisted legacy selection when
// no signed owner exists. ResolveGlobalEmbeddingChoice is intentionally not
// used here: its deterministic winner is for display/runtime convergence and
// cannot prove that all historical vectors have one owner.
func classifyEmbeddingDestructiveState(tx *gorm.DB) (embeddingDestructiveClassification, error) {
	ctx := tx.Statement.Context
	counts, err := loadEmbeddingIndexKnowledgeBaseCounts(ctx, tx)
	if err != nil {
		return embeddingDestructiveClassification{}, err
	}

	var state orm.EmbeddingIndexState
	statePresent := false
	if tx.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		err := tx.WithContext(ctx).Where("scope = ?", globalEmbeddingScope).Take(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return embeddingDestructiveClassification{}, err
		}
		statePresent = err == nil
	}
	indexState := classifyEmbeddingIndexState(state, statePresent, counts.Affected, counts.Pending)
	if indexState.StrictlyCleared {
		return embeddingDestructiveClassification{}, nil
	}

	if statePresent {
		phase := strings.TrimSpace(state.Phase)
		targetResidue := strings.TrimSpace(state.TargetModelID) != "" ||
			strings.TrimSpace(state.TargetSource) != "" || strings.TrimSpace(state.TargetSignature) != ""
		if phase == "ready" && indexState.IndexIdentity == "verified" && !targetResidue {
			ownerSource := strings.TrimSpace(state.ActiveSource)
			if ownerSource == "" {
				ownerSource = "own"
			}
			return embeddingDestructiveClassification{
				OwnerSource: ownerSource, OwnerModelID: strings.TrimSpace(state.ActiveModelID),
				OwnerReason: embeddingConflictActiveIndexOwner,
			}, nil
		}
		// A truly empty ready row is explicit evidence that no vector identity is
		// retained. Catalog defaults and stale display selections do not override it.
		if phase == "ready" && indexState.IndexIdentity == "empty" && counts.Affected == 0 && !embeddingIndexStateHasIdentityResidue(state) {
			return embeddingDestructiveClassification{}, nil
		}
		// Unknown/maintenance phases, malformed signatures, target residue, and
		// inconsistent cleared rows cannot identify a unique safe mutation target.
		if phase != "ready" || embeddingIndexStateHasIdentityResidue(state) {
			return embeddingDestructiveClassification{BlockAllReason: embeddingConflictIndexIdentityUnverified}, nil
		}
	}

	identities, incompleteEvidence, err := loadLegacyEmbeddingIdentities(tx)
	if err != nil {
		return embeddingDestructiveClassification{}, err
	}
	if incompleteEvidence || len(identities) > 1 {
		return embeddingDestructiveClassification{BlockAllReason: embeddingConflictIndexIdentityUnverified}, nil
	}
	if len(identities) == 1 {
		return embeddingDestructiveClassification{
			OwnerSource: identities[0].Source, OwnerModelID: identities[0].ModelID,
			OwnerReason: embeddingConflictLegacySelectedOwner,
		}, nil
	}
	if counts.Affected > 0 || indexState.IndexIdentity == "legacy_unknown" {
		return embeddingDestructiveClassification{BlockAllReason: embeddingConflictIndexIdentityUnverified}, nil
	}
	return embeddingDestructiveClassification{}, nil
}

func loadLegacyEmbeddingIdentities(tx *gorm.DB) ([]legacyEmbeddingIdentity, bool, error) {
	identities := make(map[legacyEmbeddingIdentity]struct{})
	incompleteEvidence := false
	ctx := tx.Statement.Context
	if tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
		var modelIDs []string
		if err := tx.WithContext(ctx).Model(&orm.UserSelectedModel{}).
			Where("model_type = ?", "embed_main").
			Pluck("user_model_provider_group_model_id", &modelIDs).Error; err != nil {
			return nil, false, err
		}
		for _, modelID := range modelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				incompleteEvidence = true
				continue
			}
			identities[legacyEmbeddingIdentity{Source: "own", ModelID: modelID}] = struct{}{}
		}
	}
	if tx.Migrator().HasTable(&orm.UserSelectedCloudModel{}) {
		var modelIDs []string
		if err := tx.WithContext(ctx).Model(&orm.UserSelectedCloudModel{}).
			Where("model_type = ?", "embed_main").
			Pluck("public_model_key", &modelIDs).Error; err != nil {
			return nil, false, err
		}
		for _, modelID := range modelIDs {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				incompleteEvidence = true
				continue
			}
			identities[legacyEmbeddingIdentity{Source: "cloud", ModelID: modelID}] = struct{}{}
		}
	}
	result := make([]legacyEmbeddingIdentity, 0, len(identities))
	for identity := range identities {
		result = append(result, identity)
	}
	return result, incompleteEvidence, nil
}

func guardEmbeddingDestructiveOperation(tx *gorm.DB, legacySentinel error, modelIDs ...string) error {
	if len(modelIDs) == 0 {
		return nil
	}
	classification, err := classifyEmbeddingDestructiveState(tx)
	if err != nil {
		return err
	}
	if classification.BlockAllReason != "" {
		return newEmbeddingDestructiveOperationError(legacySentinel, classification.BlockAllReason)
	}
	if classification.OwnerSource != "own" || classification.OwnerModelID == "" {
		return nil
	}
	for _, modelID := range modelIDs {
		if strings.TrimSpace(modelID) == classification.OwnerModelID {
			return newEmbeddingDestructiveOperationError(legacySentinel, classification.OwnerReason)
		}
	}
	return nil
}

// guardEmbeddingRemoval protects only an evidenced owner of the deployment-
// wide text index. is_default is catalog metadata and is never ownership proof.
func guardEmbeddingRemoval(tx *gorm.DB, userID string, modelIDs ...string) error {
	_ = userID // Kept for call-site compatibility; vector ownership is global.
	return guardEmbeddingDestructiveOperation(tx, errEmbeddingInUse, modelIDs...)
}

func clearEmbeddingStateAfterRemoval(tx *gorm.DB, modelIDs ...string) error {
	if len(modelIDs) == 0 || !tx.Migrator().HasTable(&orm.EmbeddingIndexState{}) {
		return nil
	}
	return tx.Model(&orm.EmbeddingIndexState{}).
		Where("scope = ? AND active_model_id IN ?", globalEmbeddingScope, modelIDs).
		Updates(map[string]any{
			"active_model_id": "", "active_source": "", "active_model_name": "", "active_provider_name": "",
			"signature": "", "phase": "ready", "generation": gorm.Expr("generation + 1"), "updated_at": time.Now().UTC(),
		}).Error
}
