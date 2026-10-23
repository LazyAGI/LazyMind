package modelprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
)

// HardcodedModelCatalogEnabled reports whether startup should keep seeding the
// YAML model lists. The lists and SeedModelCatalog stay available; the default
// startup path uses the models.dev + OpenRouter catalog instead.
func HardcodedModelCatalogEnabled() bool {
	return strings.TrimSpace(os.Getenv("LAZYMIND_HARDCODED_MODEL_CATALOG")) == "1"
}

type upstreamCatalogFile struct {
	SchemaVersion     int                               `json:"schema_version"`
	GeneratedAt       string                            `json:"generated_at"`
	Providers         map[string][]upstreamCatalogModel `json:"providers"`
	RecognitionModels []upstreamCatalogModel            `json:"recognition_models"`
}

type upstreamCatalogModel struct {
	Name      string   `json:"name"`
	ModelType string   `json:"model_type"`
	Input     []string `json:"input"`
	Output    []string `json:"output"`
	Context   int      `json:"context"`
	MaxInput  int      `json:"max_input"`
	MaxOutput int      `json:"max_output"`
}

func loadUpstreamCatalog(path string) (upstreamCatalogFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return upstreamCatalogFile{}, err
	}
	var catalog upstreamCatalogFile
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return upstreamCatalogFile{}, err
	}
	if len(catalog.Providers) == 0 {
		return upstreamCatalogFile{}, fmt.Errorf("upstream catalog %s has no providers", path)
	}
	return catalog, nil
}

func formatContextTokens(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	// Runtime K/M suffixes use powers of 1024. Keep upstream limits exact.
	return fmt.Sprintf("%d", tokens)
}

func modalitySet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = true
	}
	return set
}

// inferUpstreamCatalogModel maps models.dev / OpenRouter modalities onto the
// catalog model types the rest of LazyMind already stores.
func inferUpstreamCatalogModel(item upstreamCatalogModel) catalogModel {
	name := strings.TrimSpace(item.Name)
	input := modalitySet(item.Input)
	model := catalogModel{Name: name, Vision: input["image"] || input["video"]}
	model.Type = classifyUpstreamModel(item.ModelType, item.Input, item.Output)
	if formatted := formatContextTokens(item.MaxInput); formatted != "" {
		model.MaxInputTokensOverride = formatted
	} else if model.Type == "llm" || model.Type == "vlm" || model.Type == "embed" || model.Type == "multimodal_embedding" || model.Type == "rerank" {
		model.MaxInputTokensOverride = formatContextTokens(item.Context)
	}
	return model
}

func applyYAMLCatalogMetadata(model catalogModel, yaml catalogModel) catalogModel {
	// Verified-free selection is local policy; model metadata comes from upstream.
	model.FreeAutoSelectPriority = yaml.FreeAutoSelectPriority
	model.FreeAutoSelectBaseURLs = append([]string(nil), yaml.FreeAutoSelectBaseURLs...)
	return model
}

func yamlModelsByProvider(catalog modelCatalog) map[string]map[string]catalogModel {
	indexed := map[string]map[string]catalogModel{}
	section, ok := catalog["model_providers"]
	if !ok {
		return indexed
	}
	for _, supplier := range section.Suppliers {
		models := make(map[string]catalogModel, len(supplier.Models))
		for _, model := range supplier.Models {
			models[model.Name] = model
		}
		indexed[supplier.Name] = models
	}
	return indexed
}

// SeedUpstreamModelCatalog keeps provider and non-model service rows from YAML,
// skips every hardcoded model-provider list, and upserts models.dev + OpenRouter
// templates. Existing user models and selections survive upstream removals.
func SeedUpstreamModelCatalog(ctx context.Context, db *gorm.DB, yamlPath, upstreamPath string) error {
	upstream, err := loadUpstreamCatalog(upstreamPath)
	if err != nil {
		return err
	}
	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		return err
	}
	yamlCatalog, err := loadModelCatalog(yamlBytes)
	if err != nil {
		return err
	}
	yamlModels := yamlModelsByProvider(yamlCatalog)
	skip := make(map[string]struct{}, len(yamlModels))
	for name := range yamlModels {
		skip[name] = struct{}{}
	}

	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := SeedProviderCatalog(ctx, tx, yamlPath, skip); err != nil {
			return err
		}
		for providerName, models := range upstream.Providers {
			if len(models) == 0 {
				continue
			}
			var provider orm.DefaultModelProvider
			err := tx.Where("name = ? AND deleted_at IS NULL", providerName).Take(&provider).Error
			if err != nil {
				return fmt.Errorf("upstream provider %q: %w", providerName, err)
			}
			catalogModels := make([]catalogModel, 0, len(models))
			known := yamlModels[providerName]
			for _, item := range models {
				model := inferUpstreamCatalogModel(item)
				if model.Type == "" || (upstream.SchemaVersion >= 2 && strings.TrimSpace(item.ModelType) == "") {
					model.Type = "unknown"
				}
				if yamlModel, ok := known[model.Name]; ok {
					model = applyYAMLCatalogMetadata(model, yamlModel)
				}
				if err := upsertDefaultModel(tx, now, provider.ID, providerName, model); err != nil {
					return err
				}
				catalogModels = append(catalogModels, model)
			}
			if err := reconcileCatalogModelSelections(tx, provider.ID); err != nil {
				return err
			}
			if err := reconcileUpstreamCatalogTemplates(tx, provider.ID, catalogModels); err != nil {
				return err
			}
		}
		return nil
	})
}

// Upstream catalogs can omit models that remain available at an existing user
// endpoint. Retire only the templates used for creating new connections.
func reconcileUpstreamCatalogTemplates(tx *gorm.DB, providerID string, models []catalogModel) error {
	names := make([]string, 0, len(models))
	for _, model := range models {
		if name := strings.TrimSpace(model.Name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return tx.Unscoped().Where("default_model_provider_id = ? AND name NOT IN ?", providerID, names).Delete(&orm.DefaultModel{}).Error
}

// MustSeedUpstreamModelCatalog seeds the upstream catalog or exits the process.
func MustSeedUpstreamModelCatalog(ctx context.Context, db *gorm.DB, yamlPath, upstreamPath string) {
	if err := SeedUpstreamModelCatalog(ctx, db, yamlPath, upstreamPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", upstreamPath).Msg("seed upstream model catalog failed")
	}
	log.Logger.Info().Str("path", upstreamPath).Msg("model catalog seeded from models.dev and OpenRouter; hardcoded YAML model lists skipped")
}
