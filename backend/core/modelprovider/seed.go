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
	Name                   string   `yaml:"name"`
	Type                   string   `yaml:"type"`
	MaxInputTokens         *string  `yaml:"max_input_tokens"`
	FreeAutoSelectPriority int      `yaml:"free_auto_select_priority"`
	FreeAutoSelectBaseURLs []string `yaml:"free_auto_select_base_urls"`
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

var maxInputTokensPattern = regexp.MustCompile(`^[1-9][0-9]*([KM])?$`)

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
		if modelType != "llm" && modelType != "vlm" && modelType != "embed" {
			return errors.New("model max_input_tokens is only supported for llm, vlm, or embed models")
		}
		maxInputTokens := strings.ToUpper(strings.TrimSpace(*item.MaxInputTokens))
		if !maxInputTokensPattern.MatchString(maxInputTokens) {
			return errors.New("model max_input_tokens must be a positive integer or use a K or M suffix, for example 512, 128K, or 1M")
		}
		item.MaxInputTokens = &maxInputTokens
	}
	if item.FreeAutoSelectPriority < 0 {
		return errors.New("model free_auto_select_priority must not be negative")
	}
	if item.FreeAutoSelectPriority == 0 && len(item.FreeAutoSelectBaseURLs) > 0 {
		return errors.New("model free_auto_select_base_urls requires a positive free_auto_select_priority")
	}
	freeAutoSelectBaseURLs, err := encodeFreeAutoSelectBaseURLs(item.FreeAutoSelectBaseURLs)
	if err != nil {
		return err
	}

	var row orm.DefaultModel
	err = tx.Where("default_model_provider_id = ? AND name = ?", providerID, name).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = orm.DefaultModel{
			ID:                     common.GenerateID(),
			DefaultModelProviderID: providerID,
			ProviderName:           providerName,
			Name:                   name,
			ModelType:              modelType,
			MaxInputTokens:         item.MaxInputTokens,
			FreeAutoSelectPriority: item.FreeAutoSelectPriority,
			FreeAutoSelectBaseURLs: freeAutoSelectBaseURLs,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return syncDefaultModelToUserGroups(
			tx, now, providerID, providerName, name, modelType, item.MaxInputTokens,
			item.FreeAutoSelectPriority, freeAutoSelectBaseURLs,
		)
	}
	if err != nil {
		return err
	}

	if err := tx.Model(&orm.DefaultModel{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"provider_name":              providerName,
			"model_type":                 modelType,
			"max_input_tokens":           item.MaxInputTokens,
			"free_auto_select_priority":  item.FreeAutoSelectPriority,
			"free_auto_select_base_urls": freeAutoSelectBaseURLs,
			"updated_at":                 now,
			"deleted_at":                 nil,
		}).Error; err != nil {
		return err
	}
	return syncDefaultModelToUserGroups(
		tx, now, providerID, providerName, name, modelType, item.MaxInputTokens,
		item.FreeAutoSelectPriority, freeAutoSelectBaseURLs,
	)
}

func encodeFreeAutoSelectBaseURLs(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeBaseURLForCompare(value)
		if value == "" {
			return "", errors.New("model free_auto_select_base_urls must not contain an empty URL")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal model free_auto_select_base_urls: %w", err)
	}
	return string(encoded), nil
}

// syncDefaultModelToUserGroups mirrors catalog metadata into default models already
// copied to user groups, and inserts newly catalogued defaults that are still missing.
// Custom user-added models are intentionally left untouched.
func syncDefaultModelToUserGroups(
	tx *gorm.DB,
	now time.Time,
	providerID, providerName, modelName, modelType string,
	maxInputTokens *string,
	freeAutoSelectPriority int,
	freeAutoSelectBaseURLs string,
) error {
	providerIDs := tx.Model(&orm.UserModelProvider{}).
		Select("id").
		Where("default_model_provider_id = ? AND deleted_at IS NULL", providerID)

	updates := map[string]any{
		"model_type":                 modelType,
		"max_input_tokens":           maxInputTokens,
		"free_auto_select_priority":  freeAutoSelectPriority,
		"free_auto_select_base_urls": freeAutoSelectBaseURLs,
		"updated_at":                 now,
	}
	if err := tx.Model(&orm.UserModelProviderGroupModel{}).
		Where("is_default = ? AND name = ? AND user_model_provider_id IN (?) AND deleted_at IS NULL", true, modelName, providerIDs).
		Updates(updates).Error; err != nil {
		return err
	}

	var catalog orm.DefaultModelProvider
	if err := tx.Where("id = ? AND deleted_at IS NULL", providerID).Take(&catalog).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	catalogBaseURL := normalizeBaseURLForCompare(catalog.BaseURL)

	type seededGroup struct {
		GroupID             string `gorm:"column:group_id"`
		UserModelProviderID string `gorm:"column:user_model_provider_id"`
		BaseURL             string `gorm:"column:base_url"`
		CreateUserID        string `gorm:"column:create_user_id"`
		CreateUserName      string `gorm:"column:create_user_name"`
	}
	var groups []seededGroup
	if err := tx.Table("user_model_provider_groups g").
		Select("g.id AS group_id, g.user_model_provider_id, g.base_url, g.create_user_id, g.create_user_name").
		Joins("JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.deleted_at IS NULL").
		Where("p.default_model_provider_id = ? AND g.deleted_at IS NULL", providerID).
		Where("EXISTS (SELECT 1 FROM user_model_provider_group_models m WHERE m.user_model_provider_group_id = g.id AND m.is_default = ? AND m.deleted_at IS NULL)", true).
		Scan(&groups).Error; err != nil {
		return err
	}

	for _, group := range groups {
		groupBaseURL := normalizeBaseURLForCompare(group.BaseURL)
		if strings.EqualFold(providerName, "SenseNova") {
			useTokenPlan := groupBaseURL == normalizeBaseURLForCompare(sensenovaNewPlatformBaseURL)
			if groupBaseURL != catalogBaseURL && !useTokenPlan {
				continue
			}
			if !shouldSeedSenseNovaModel(modelName, useTokenPlan) {
				continue
			}
		} else if groupBaseURL != catalogBaseURL {
			continue
		}
		var count int64
		if err := tx.Model(&orm.UserModelProviderGroupModel{}).
			Where("user_model_provider_group_id = ? AND name = ? AND deleted_at IS NULL", group.GroupID, modelName).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		row := orm.UserModelProviderGroupModel{
			ID:                       common.GenerateID(),
			UserModelProviderID:      group.UserModelProviderID,
			UserModelProviderGroupID: group.GroupID,
			ProviderName:             providerName,
			Name:                     modelName,
			ModelType:                modelType,
			MaxInputTokens:           maxInputTokens,
			FreeAutoSelectPriority:   freeAutoSelectPriority,
			FreeAutoSelectBaseURLs:   freeAutoSelectBaseURLs,
			IsDefault:                true,
			BaseModel: orm.BaseModel{
				CreateUserID:   group.CreateUserID,
				CreateUserName: group.CreateUserName,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// reconcileSenseNovaCatalogScope removes retired classic models and keeps the classic and Token Plan
// model subsets from leaking into each other's default-seeded groups. User-added models are preserved.
func reconcileSenseNovaCatalogScope(
	tx *gorm.DB,
	providerID, catalogBaseURL string,
	models []catalogModel,
) error {
	catalogNames := make(map[string]struct{}, len(models))
	names := make([]string, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			continue
		}
		catalogNames[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}

	type scopedGroupModel struct {
		ID      string `gorm:"column:id"`
		Name    string `gorm:"column:name"`
		BaseURL string `gorm:"column:base_url"`
	}
	var rows []scopedGroupModel
	if err := tx.Table("user_model_provider_group_models m").
		Select("m.id, m.name, g.base_url").
		Joins("JOIN user_model_provider_groups g ON g.id = m.user_model_provider_group_id AND g.deleted_at IS NULL").
		Joins("JOIN user_model_providers p ON p.id = m.user_model_provider_id AND p.deleted_at IS NULL").
		Where("p.default_model_provider_id = ? AND m.is_default = ? AND m.deleted_at IS NULL", providerID, true).
		Scan(&rows).Error; err != nil {
		return err
	}

	classicURL := normalizeBaseURLForCompare(catalogBaseURL)
	tokenPlanURL := normalizeBaseURLForCompare(sensenovaNewPlatformBaseURL)
	deleteIDs := make([]string, 0)
	for _, row := range rows {
		groupURL := normalizeBaseURLForCompare(row.BaseURL)
		if groupURL != classicURL && groupURL != tokenPlanURL {
			continue
		}
		_, inCatalog := catalogNames[row.Name]
		useTokenPlan := groupURL == tokenPlanURL
		if !inCatalog || !shouldSeedSenseNovaModel(row.Name, useTokenPlan) {
			deleteIDs = append(deleteIDs, row.ID)
		}
	}
	if len(deleteIDs) > 0 {
		if err := tx.Where("user_model_provider_group_model_id IN ?", deleteIDs).
			Delete(&orm.UserSelectedModel{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id IN ?", deleteIDs).
			Delete(&orm.UserModelProviderGroupModel{}).Error; err != nil {
			return err
		}
	}

	return tx.Unscoped().
		Where("default_model_provider_id = ? AND name NOT IN ?", providerID, names).
		Delete(&orm.DefaultModel{}).Error
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
				if strings.EqualFold(supplier.Name, "SenseNova") {
					if err := reconcileSenseNovaCatalogScope(tx, providerID, supplier.BaseURL, supplier.Models); err != nil {
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
