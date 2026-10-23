package modelprovider

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var (
	errUnsupportedModelType       = errors.New("unsupported model_type")
	errSupplierModelType          = errors.New("model type is not supported by the supplier protocol")
	errInvalidModelName           = errors.New("model name must contain 1 to 512 bytes")
	errModelDefaultReference      = errors.New("model update would invalidate a default selection; change the selection first")
	errModelConversationReference = errors.New("model update would invalidate a conversation selection; change the conversation model first")
	errEmbeddingIdentityInUse     = errors.New("embedding model identity is in use; change the default model and downgrade indexed knowledge bases before editing")
	errSelectionModelType         = errors.New("selected model does not support this default model role")
)

type modelMetadataValidationError struct{ error }

func (err modelMetadataValidationError) Unwrap() error { return err.error }

const modelCustomizationTombstonePrefix = "override-tombstone-"

func validateModelName(name string) error {
	if name == "" || len(name) > 512 || !utf8.ValidString(name) {
		return errInvalidModelName
	}
	return nil
}

func validateSupplierModelType(parent orm.UserModelProvider, modelType string) error {
	modelType = normalizeGroupModelType(modelType)
	switch modelType {
	case "llm", "vlm", "embed", "rerank", "stt", "tts", "text2image", "text2video", "image_editing", "multimodal_embedding", "cross_modal_embed", "embed_image":
	default:
		return errUnsupportedModelType
	}
	if strings.EqualFold(RuntimeProviderName(parent), "Claude") && modelType != "llm" && modelType != "vlm" {
		return errSupplierModelType
	}
	if !RuntimeSupportsModelType(RuntimeProviderName(parent), modelType) {
		return errSupplierModelType
	}
	return nil
}

func isChatTechnicalType(typ string) bool {
	typ = normalizeGroupModelType(typ)
	return typ == "llm" || typ == "vlm"
}
func isAnyEmbeddingTechnicalType(typ string) bool {
	return isTextEmbeddingModelType(typ) || isMultimodalEmbeddingModelType(typ)
}
func equivalentTechnicalType(typ string) string {
	if isTextEmbeddingModelType(typ) {
		return "embed"
	}
	if isAnyEmbeddingTechnicalType(typ) {
		return "multimodal_embedding"
	}
	return normalizeGroupModelType(typ)
}

func modelSupportsSelection(m orm.UserModelProviderGroupModel, slot, baseURL string) bool {
	m.ModelType = normalizeGroupModelType(m.ModelType)
	if !RuntimeSupportsModelType(m.ProviderName, m.ModelType) {
		return false
	}
	switch slot {
	case "llm":
		return m.ModelType == "llm"
	case "vlm":
		return m.ModelType == "vlm" || (m.ModelType == "llm" && m.Vision)
	case EvoModelKey:
		_, ok := ResolveOpenCodeModel(m.ProviderName, m.Name, baseURL, m.ModelType, m.IsDefault)
		return ok
	case "embed_main":
		return isTextEmbeddingModelType(m.ModelType)
	case "embed_image":
		return isAnyEmbeddingTechnicalType(m.ModelType) && !isTextEmbeddingModelType(m.ModelType)
	case "reranker":
		return m.ModelType == "rerank"
	case "text2image":
		return m.ModelType == "text2image" || m.ModelType == "image_editing"
	default:
		return m.ModelType == slot
	}
}

// The model UUID is kept stable. Reject changes that would make its existing
// role bindings invalid rather than silently clearing or repointing them.
func validateModelEditReferences(tx *gorm.DB, before, after orm.UserModelProviderGroupModel, baseURL string) error {
	identityChanged := before.Name != after.Name || equivalentTechnicalType(before.ModelType) != equivalentTechnicalType(after.ModelType)
	var selections []orm.UserSelectedModel
	if tx.Migrator().HasTable(&orm.UserSelectedModel{}) {
		if err := tx.Where("user_model_provider_group_model_id = ?", before.ID).Find(&selections).Error; err != nil {
			return err
		}
	}
	if identityChanged && isAnyEmbeddingTechnicalType(before.ModelType) {
		if len(selections) > 0 {
			return errEmbeddingIdentityInUse
		}
		if isTextEmbeddingModelType(before.ModelType) && tx.Migrator().HasTable(&orm.Dataset{}) {
			var count int64
			if err := tx.Model(&orm.Dataset{}).Where("create_user_id = ? AND processing_level = ? AND deleted_at IS NULL", before.CreateUserID, "indexed").Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errEmbeddingIdentityInUse
			}
		}
	}
	for _, selection := range selections {
		if !modelSupportsSelection(after, selection.ModelKey, baseURL) {
			return errModelDefaultReference
		}
	}
	if normalizeGroupModelType(after.ModelType) != "llm" && tx.Migrator().HasTable(&orm.Conversation{}) {
		var count int64
		if err := tx.Model(&orm.Conversation{}).Where("chat_model_id = ? AND (chat_model_source IS NULL OR chat_model_source = '' OR chat_model_source IN ('own', 'shared')) AND deleted_at IS NULL", before.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errModelConversationReference
		}
	}
	return nil
}

// Catalog synchronizers treat any existing name, including a soft-deleted
// tombstone, as a user decision. Preserve that decision when a catalog model's
// API identifier is changed without replacing the referenced model UUID.
func retainRenamedCatalogTombstone(tx *gorm.DB, parent orm.UserModelProvider, before, after orm.UserModelProviderGroupModel, now time.Time) error {
	if before.Name == after.Name {
		return nil
	}
	catalogName := before.IsDefault
	if !catalogName && parent.DefaultModelProviderID != "" && tx.Migrator().HasTable(&orm.DefaultModel{}) {
		var count int64
		if err := tx.Model(&orm.DefaultModel{}).Where("default_model_provider_id = ? AND name = ?", parent.DefaultModelProviderID, before.Name).Count(&count).Error; err != nil {
			return err
		}
		catalogName = count > 0
	}
	if !catalogName {
		return nil
	}
	tombstone := before
	tombstone.ID = modelCustomizationTombstonePrefix + common.GenerateID()
	tombstone.IsDefault = true
	tombstone.CreatedAt = now
	tombstone.UpdatedAt = now
	tombstone.DeletedAt = &now
	return tx.Create(&tombstone).Error
}

func ensureEditedModelNameAvailable(tx *gorm.DB, model orm.UserModelProviderGroupModel, name string) error {
	var conflict orm.UserModelProviderGroupModel
	err := tx.Where("user_model_provider_group_id = ? AND name = ? AND id <> ?", model.UserModelProviderGroupID, name, model.ID).Take(&conflict).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Only editor-owned internal tombstones may be removed to support undoing a
	// prior rename. A deleted user model remains a real conflicting identity.
	if conflict.DeletedAt != nil && conflict.IsDefault && strings.HasPrefix(conflict.ID, modelCustomizationTombstonePrefix) {
		return tx.Where("id = ? AND deleted_at IS NOT NULL AND is_default = ?", conflict.ID, true).Delete(&orm.UserModelProviderGroupModel{}).Error
	}
	return errModelAlreadyExists
}

// LockModelSupplierForConfiguration serializes a durable model reference with
// supplier metadata edits. Callers must re-read their authorized model view
// after this lock and hold the transaction until that reference is persisted.
func LockModelSupplierForConfiguration(tx *gorm.DB, modelID string) error {
	var model orm.UserModelProviderGroupModel
	if err := tx.Where("id = ? AND deleted_at IS NULL", modelID).Take(&model).Error; err != nil {
		return err
	}
	parent := orm.UserModelProvider{ID: model.UserModelProviderID, BaseModel: orm.BaseModel{CreateUserID: model.CreateUserID}}
	return lockProvider(tx, &parent)
}
