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
	GeneratedAt string                            `json:"generated_at"`
	Providers   map[string][]upstreamCatalogModel `json:"providers"`
}

type upstreamCatalogModel struct {
	Name      string   `json:"name"`
	Input     []string `json:"input"`
	Output    []string `json:"output"`
	Context   int      `json:"context"`
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
	if tokens%1_000_000 == 0 {
		return fmt.Sprintf("%dM", tokens/1_000_000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%dK", tokens/1000)
	}
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
	lower := strings.ToLower(name)
	input := modalitySet(item.Input)
	output := modalitySet(item.Output)
	model := catalogModel{Name: name, Vision: input["image"] || input["video"]}
	switch {
	case strings.Contains(lower, "rerank"):
		model.Type = "rerank"
	case strings.Contains(lower, "embed"):
		if input["image"] || input["audio"] || input["video"] {
			model.Type = "multimodal_embedding"
		} else {
			model.Type = "embed"
		}
	case input["audio"] && output["text"] && !input["text"]:
		model.Type = "stt"
	case output["audio"] && !output["text"] && !output["image"] && !output["video"]:
		model.Type = "tts"
	case output["image"] && !output["text"] && !output["video"]:
		if input["image"] {
			model.Type = "image_editing"
		} else {
			model.Type = "text2image"
		}
	case output["video"] && !output["text"]:
		model.Type = "text2video"
	case input["image"] || input["video"]:
		model.Type = "vlm"
	default:
		model.Type = "llm"
	}
	if formatted := formatContextTokens(item.Context); formatted != "" && (model.Type == "llm" || model.Type == "vlm") {
		if lookedUp, ok := lookupMaxInputTokens(name, model.Type); ok {
			model.MaxInputTokensOverride = lookedUp
		} else {
			model.MaxInputTokensOverride = formatted
		}
	}
	return model
}

func applyYAMLCatalogMetadata(model catalogModel, yaml catalogModel) catalogModel {
	if strings.TrimSpace(yaml.Type) != "" {
		model.Type = yaml.Type
	}
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

// SeedUpstreamModelCatalog keeps provider rows from the YAML catalog, skips the
// hardcoded model lists for providers present in the upstream file, and upserts
// those models from models.dev + OpenRouter metadata.
func SeedUpstreamModelCatalog(ctx context.Context, db *gorm.DB, yamlPath, upstreamPath string) error {
	upstream, err := loadUpstreamCatalog(upstreamPath)
	if err != nil {
		return err
	}
	skip := make(map[string]struct{}, len(upstream.Providers))
	for name, models := range upstream.Providers {
		if len(models) > 0 {
			skip[name] = struct{}{}
		}
	}
	if err := SeedProviderCatalog(ctx, db, yamlPath, skip); err != nil {
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
	providerBaseURL := map[string]string{}
	if section, ok := yamlCatalog["model_providers"]; ok {
		for _, supplier := range section.Suppliers {
			providerBaseURL[supplier.Name] = supplier.BaseURL
		}
	}

	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
				if yamlModel, ok := known[model.Name]; ok {
					model = applyYAMLCatalogMetadata(model, yamlModel)
				}
				if err := upsertDefaultModel(tx, now, provider.ID, providerName, model); err != nil {
					return err
				}
				catalogModels = append(catalogModels, model)
			}
			if err := reconcileCatalogScope(tx, provider.ID, providerName, providerBaseURL[providerName], catalogModels); err != nil {
				return err
			}
		}
		return nil
	})
}

// MustSeedUpstreamModelCatalog seeds the upstream catalog or exits the process.
func MustSeedUpstreamModelCatalog(ctx context.Context, db *gorm.DB, yamlPath, upstreamPath string) {
	if err := SeedUpstreamModelCatalog(ctx, db, yamlPath, upstreamPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", upstreamPath).Msg("seed upstream model catalog failed")
	}
	log.Logger.Info().Str("path", upstreamPath).Msg("model catalog seeded from models.dev and OpenRouter; hardcoded YAML model lists skipped")
}
