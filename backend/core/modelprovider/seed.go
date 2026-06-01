package modelprovider

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

type catalogModel struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type catalogProvider struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	BaseURL     string         `yaml:"base_url"`
	Models      []catalogModel `yaml:"models"`
}

type modelCatalog struct {
	Providers []catalogProvider `yaml:"model_providers"`
}

var endpointPathMarkers = []string{"/embeddings", "/rerank", "/embed"}

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

func loadModelCatalog(yamlBytes []byte) (*modelCatalog, error) {
	var catalog modelCatalog
	if err := yaml.Unmarshal(yamlBytes, &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func upsertDefaultProvider(tx *gorm.DB, now time.Time, item catalogProvider) (string, error) {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return "", errors.New("provider name is required")
	}

	baseURL := normalizeBaseURL(item.BaseURL)
	var row orm.DefaultModelProvider
	err := tx.Where("name = ?", name).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = orm.DefaultModelProvider{
			ID:          common.GenerateID(),
			Name:        name,
			Description: item.Description,
			BaseURL:     baseURL,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		return row.ID, tx.Create(&row).Error
	}
	if err != nil {
		return "", err
	}

	return row.ID, tx.Model(&orm.DefaultModelProvider{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"description": item.Description,
			"base_url":    baseURL,
			"updated_at":  now,
			"deleted_at":  nil,
		}).Error
}

func upsertDefaultModel(tx *gorm.DB, now time.Time, providerID, providerName string, item catalogModel) error {
	name := strings.TrimSpace(item.Name)
	modelType := strings.TrimSpace(item.Type)
	if name == "" || modelType == "" {
		return errors.New("model name and type are required")
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
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		return tx.Create(&row).Error
	}
	if err != nil {
		return err
	}

	return tx.Model(&orm.DefaultModel{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"provider_name": providerName,
			"model_type":    modelType,
			"updated_at":    now,
			"deleted_at":    nil,
		}).Error
}

// SeedModelCatalog upserts default_model_providers and default_models from the YAML catalog file.
func SeedModelCatalog(ctx context.Context, db *gorm.DB, yamlPath string) error {
	yamlPath = strings.TrimSpace(yamlPath)
	if yamlPath == "" {
		return errors.New("model catalog yaml path is required")
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
		for _, provider := range catalog.Providers {
			providerID, err := upsertDefaultProvider(tx, now, provider)
			if err != nil {
				return err
			}
			for _, model := range provider.Models {
				if err := upsertDefaultModel(tx, now, providerID, provider.Name, model); err != nil {
					return err
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
