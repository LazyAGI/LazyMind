package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

type catalogModel struct {
	Name           string  `yaml:"name"`
	Type           string  `yaml:"type"`
	MaxInputTokens *string `yaml:"max_input_tokens"`
}

type catalogSupplier struct {
	Name         string            `yaml:"name"`
	Description  map[string]string `yaml:"description"`
	BaseURL      string            `yaml:"base_url"`
	Capabilities []string          `yaml:"capabilities"` // overrides section-level default when non-empty
	Models       []catalogModel    `yaml:"models"`
}

type catalogSection struct {
	Capabilities []string          `yaml:"capabilities"`
	Suppliers    []catalogSupplier `yaml:"suppliers"`
}

// modelCatalog is a map from section key (e.g. "model_providers") to its section.
type modelCatalog map[string]catalogSection

var endpointPathMarkers = []string{"/embeddings", "/rerank", "/embed"}

var maxInputTokensPattern = regexp.MustCompile(`^[1-9][0-9]*(K|M)$`)

// normalizeBaseURL appends a trailing slash to generic API roots; endpoint-specific URLs are kept as-is.
func normalizeBaseURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return url
	}
	for _, marker := range endpointPathMarkers {
		if strings.Contains(url, marker) {
			return url
		}
	}
	if !strings.HasSuffix(url, "/") {
		return url + "/"
	}
	return url
}

func loadModelCatalog(yamlBytes []byte) (modelCatalog, error) {
	var catalog modelCatalog
	if err := yaml.Unmarshal(yamlBytes, &catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

func upsertDefaultProvider(tx *gorm.DB, now time.Time, category string, caps []string, item catalogSupplier) (string, error) {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return "", errors.New("provider name is required")
	}
	descriptionZh := strings.TrimSpace(item.Description[common.LocaleZhCN])
	descriptionEn := strings.TrimSpace(item.Description[common.LocaleEnUS])
	if descriptionZh == "" || descriptionEn == "" {
		return "", fmt.Errorf("provider %q requires non-empty %s and %s descriptions", name, common.LocaleZhCN, common.LocaleEnUS)
	}
	descriptionI18n, err := json.Marshal(map[string]string{
		common.LocaleZhCN: descriptionZh,
		common.LocaleEnUS: descriptionEn,
	})
	if err != nil {
		return "", fmt.Errorf("marshal provider %q descriptions: %w", name, err)
	}

	// Supplier-level capabilities override section-level when present.
	effectiveCaps := caps
	if len(item.Capabilities) > 0 {
		effectiveCaps = item.Capabilities
	}
	capStr := strings.Join(effectiveCaps, ",")

	baseURL := normalizeBaseURL(item.BaseURL)
	var row orm.DefaultModelProvider
	err = tx.Where("name = ?", name).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = orm.DefaultModelProvider{
			ID:              common.GenerateID(),
			Name:            name,
			Description:     descriptionZh,
			DescriptionI18n: orm.RawJSON(descriptionI18n),
			BaseURL:         baseURL,
			Category:        category,
			Capabilities:    capStr,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		return row.ID, tx.Create(&row).Error
	}
	if err != nil {
		return "", err
	}

	return row.ID, tx.Model(&orm.DefaultModelProvider{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"description":      descriptionZh,
			"description_i18n": orm.RawJSON(descriptionI18n),
			"base_url":         baseURL,
			"category":         category,
			"capabilities":     capStr,
			"updated_at":       now,
			"deleted_at":       nil,
		}).Error
}

func upsertDefaultModel(tx *gorm.DB, now time.Time, providerID, providerName string, item catalogModel) error {
	name := strings.TrimSpace(item.Name)
	modelType := strings.TrimSpace(item.Type)
	if name == "" || modelType == "" {
		return errors.New("model name and type are required")
	}
	if item.MaxInputTokens != nil {
		if modelType != "llm" && modelType != "vlm" {
			return errors.New("model max_input_tokens is only supported for llm or vlm models")
		}
		maxInputTokens := strings.ToUpper(strings.TrimSpace(*item.MaxInputTokens))
		if !maxInputTokensPattern.MatchString(maxInputTokens) {
			return errors.New("model max_input_tokens must use a positive K or M value, for example 128K or 1M")
		}
		item.MaxInputTokens = &maxInputTokens
	}

	var row orm.DefaultModel
	err := tx.Where("default_model_provider_id = ? AND name = ?", providerID, name).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = orm.DefaultModel{
			ID:                     common.GenerateID(),
			DefaultModelProviderID: providerID,
			ProviderName:           providerName,
			Name:                   name,
			ModelType:              modelType,
			MaxInputTokens:         item.MaxInputTokens,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return syncDefaultModelMaxInputTokens(tx, now, providerID, name, item.MaxInputTokens)
	}
	if err != nil {
		return err
	}

	if err := tx.Model(&orm.DefaultModel{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"provider_name":    providerName,
			"model_type":       modelType,
			"max_input_tokens": item.MaxInputTokens,
			"updated_at":       now,
			"deleted_at":       nil,
		}).Error; err != nil {
		return err
	}
	return syncDefaultModelMaxInputTokens(tx, now, providerID, name, item.MaxInputTokens)
}

// syncDefaultModelMaxInputTokens mirrors catalog metadata into default models already
// copied to user groups. Custom user-added models are intentionally left untouched.
func syncDefaultModelMaxInputTokens(tx *gorm.DB, now time.Time, providerID, modelName string, maxInputTokens *string) error {
	providerIDs := tx.Model(&orm.UserModelProvider{}).
		Select("id").
		Where("default_model_provider_id = ? AND deleted_at IS NULL", providerID)
	query := tx.Model(&orm.UserModelProviderGroupModel{}).
		Where("is_default = ? AND name = ? AND user_model_provider_id IN (?) AND deleted_at IS NULL", true, modelName, providerIDs).
		Where("max_input_tokens IS NOT NULL")
	updates := map[string]any{
		"max_input_tokens": nil,
		"updated_at":       now,
	}
	if maxInputTokens != nil {
		query = tx.Model(&orm.UserModelProviderGroupModel{}).
			Where("is_default = ? AND name = ? AND user_model_provider_id IN (?) AND deleted_at IS NULL", true, modelName, providerIDs).
			Where("max_input_tokens IS NULL OR max_input_tokens <> ?", *maxInputTokens)
		updates["max_input_tokens"] = maxInputTokens
	}
	return query.Updates(updates).Error
}

// SeedModelCatalog upserts default_model_providers and default_models from the YAML catalog file.
// Section keys ending with "_providers" derive their category by trimming that suffix.
func SeedModelCatalog(ctx context.Context, db *gorm.DB, yamlPath string) error {
	return seedCatalog(ctx, db, yamlPath, "_providers", "")
}

// SeedDatasourceCatalog upserts default_model_providers from the datasource YAML catalog file.
// All suppliers are seeded with category "datasource" regardless of section key.
func SeedDatasourceCatalog(ctx context.Context, db *gorm.DB, yamlPath string) error {
	return seedCatalog(ctx, db, yamlPath, "_sources", "datasource")
}

func seedCatalog(ctx context.Context, db *gorm.DB, yamlPath, categorySuffix, forceCategory string) error {
	yamlPath = strings.TrimSpace(yamlPath)
	if yamlPath == "" {
		return errors.New("catalog yaml path is required")
	}

	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		return err
	}

	catalog, err := loadModelCatalog(yamlBytes)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for sectionKey, section := range catalog {
			category := forceCategory
			if category == "" {
				category = strings.TrimSuffix(sectionKey, categorySuffix)
			}
			for _, supplier := range section.Suppliers {
				providerID, err := upsertDefaultProvider(tx, now, category, section.Capabilities, supplier)
				if err != nil {
					return err
				}
				for _, model := range supplier.Models {
					if err := upsertDefaultModel(tx, now, providerID, supplier.Name, model); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// MustSeedModelCatalog runs SeedModelCatalog using config/model_catalog.yaml under the working directory.
func MustSeedModelCatalog(ctx context.Context, db *gorm.DB, yamlPath string) {
	if err := SeedModelCatalog(ctx, db, yamlPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", yamlPath).Msg("seed model catalog failed")
	}
	log.Logger.Info().Str("path", yamlPath).Msg("model catalog seeded from YAML")
}

// MustSeedDatasourceCatalog runs SeedDatasourceCatalog using config/datasource_catalog.yaml under the working directory.
func MustSeedDatasourceCatalog(ctx context.Context, db *gorm.DB, yamlPath string) {
	if err := SeedDatasourceCatalog(ctx, db, yamlPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", yamlPath).Msg("seed datasource catalog failed")
	}
	log.Logger.Info().Str("path", yamlPath).Msg("datasource catalog seeded from YAML")
}
