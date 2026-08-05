// Package knowledge_market seeds and serves the official knowledge base
// catalog shown in the knowledge plaza. The catalog data is maintained in
// config/knowledge_market_catalog.yaml and written into knowledge_market_items
// at startup; every system release refreshes the data from the YAML file.
package knowledge_market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
)

// catalogItem mirrors one entry of config/knowledge_market_catalog.yaml.
type catalogItem struct {
	ID              string        `yaml:"id"`
	Category        string        `yaml:"category"`
	Name            string        `yaml:"name"`
	Description     string        `yaml:"description"`
	Icon            string        `yaml:"icon"`
	Domain          string        `yaml:"domain"`
	Tags            []string      `yaml:"tags"`
	Version         string        `yaml:"version"`
	VersionDate     string        `yaml:"version_date"`
	VersionNote     string        `yaml:"version_note"`
	PackageURL      string        `yaml:"package_url"`
	PackageSHA256   string        `yaml:"package_sha256"`
	PackageSize     int64         `yaml:"package_size"`
	DocCount        int64         `yaml:"doc_count"`
	DataSource      string        `yaml:"data_source"`
	Files           []catalogFile `yaml:"files"`
	SampleQuestions []string      `yaml:"sample_questions"`
}

type catalogFile struct {
	Name string `yaml:"name" json:"name"`
	Size int64  `yaml:"size" json:"size"`
	Path string `yaml:"path" json:"path"`
}

type catalogFileYAML struct {
	Items []catalogItem `yaml:"knowledge_market_items"`
}

// SeedCatalog upserts knowledge_market_items from the catalog YAML file.
// Items still present in the catalog are written with status published and
// sort_order matching their position in the file; items previously seeded but
// no longer present are moved to status offline so existing installs stay valid.
func SeedCatalog(ctx context.Context, db *gorm.DB, yamlPath string) error {
	yamlPath = strings.TrimSpace(yamlPath)
	if yamlPath == "" {
		return errors.New("knowledge market catalog yaml path is required")
	}
	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		return err
	}
	items, err := loadCatalog(yamlBytes)
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if _, dup := seen[item.ID]; dup {
			return fmt.Errorf("knowledge market catalog contains duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		ids = append(ids, item.ID)
	}

	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, item := range items {
			if err := upsertItem(tx, now, i, item); err != nil {
				return err
			}
		}
		return offlineRemovedItems(tx, now, ids)
	})
}

// MustSeedCatalog runs SeedCatalog and terminates the process on failure.
func MustSeedCatalog(ctx context.Context, db *gorm.DB, yamlPath string) {
	if err := SeedCatalog(ctx, db, yamlPath); err != nil {
		log.Logger.Fatal().Err(err).Str("path", yamlPath).Msg("seed knowledge market catalog failed")
	}
	log.Logger.Info().Str("path", yamlPath).Msg("knowledge market catalog seeded from YAML")
}

func loadCatalog(yamlBytes []byte) ([]catalogItem, error) {
	var file catalogFileYAML
	if err := yaml.Unmarshal(yamlBytes, &file); err != nil {
		return nil, err
	}
	return file.Items, nil
}

func upsertItem(tx *gorm.DB, now time.Time, sortOrder int, item catalogItem) error {
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	item.Category = strings.TrimSpace(item.Category)
	item.Version = strings.TrimSpace(item.Version)
	if item.ID == "" || item.Name == "" || item.Category == "" || item.Version == "" {
		return fmt.Errorf("knowledge market catalog item requires non-empty id, name, category and version")
	}
	if item.Category != "industry" && item.Category != "evaluation" {
		return fmt.Errorf("knowledge market catalog item %q has invalid category %q", item.ID, item.Category)
	}

	tagsJSON, err := json.Marshal(item.Tags)
	if err != nil {
		return err
	}
	if string(tagsJSON) == "null" {
		tagsJSON = []byte("[]")
	}
	filesJSON, err := json.Marshal(item.Files)
	if err != nil {
		return err
	}
	if string(filesJSON) == "null" {
		filesJSON = []byte("[]")
	}
	questionsJSON, err := json.Marshal(item.SampleQuestions)
	if err != nil {
		return err
	}
	if string(questionsJSON) == "null" {
		questionsJSON = []byte("[]")
	}

	desired := orm.KnowledgeMarketItem{
		ID:              item.ID,
		Category:        item.Category,
		Name:            item.Name,
		Description:     item.Description,
		Icon:            item.Icon,
		Domain:          item.Domain,
		Tags:            json.RawMessage(tagsJSON),
		Version:         item.Version,
		VersionDate:     item.VersionDate,
		VersionNote:     item.VersionNote,
		PackageURL:      item.PackageURL,
		PackageSHA256:   item.PackageSHA256,
		PackageSize:     item.PackageSize,
		DocCount:        item.DocCount,
		DataSource:      item.DataSource,
		Files:           json.RawMessage(filesJSON),
		SampleQuestions: json.RawMessage(questionsJSON),
		Status:          "published",
		SortOrder:       sortOrder,
	}

	var row orm.KnowledgeMarketItem
	err = tx.Where("id = ?", item.ID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		desired.CreatedAt = now
		desired.UpdatedAt = now
		return tx.Create(&desired).Error
	}
	if err != nil {
		return err
	}

	if rowContentEqual(&row, &desired) && row.Status == "published" && row.SortOrder == sortOrder {
		return nil
	}
	return tx.Model(&orm.KnowledgeMarketItem{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"category":         desired.Category,
			"name":             desired.Name,
			"description":      desired.Description,
			"icon":             desired.Icon,
			"domain":           desired.Domain,
			"tags":             desired.Tags,
			"version":          desired.Version,
			"version_date":     desired.VersionDate,
			"version_note":     desired.VersionNote,
			"package_url":      desired.PackageURL,
			"package_sha256":   desired.PackageSHA256,
			"package_size":     desired.PackageSize,
			"doc_count":        desired.DocCount,
			"data_source":      desired.DataSource,
			"files":            desired.Files,
			"sample_questions": desired.SampleQuestions,
			"status":           "published",
			"sort_order":       sortOrder,
			"updated_at":       now,
		}).Error
}

// rowContentEqual compares every content field while ignoring id, status,
// sort_order and timestamps so unchanged catalogs do not touch updated_at.
func rowContentEqual(row, desired *orm.KnowledgeMarketItem) bool {
	return row.Category == desired.Category &&
		row.Name == desired.Name &&
		row.Description == desired.Description &&
		row.Icon == desired.Icon &&
		row.Domain == desired.Domain &&
		jsonEqual(row.Tags, desired.Tags) &&
		row.Version == desired.Version &&
		row.VersionDate == desired.VersionDate &&
		row.VersionNote == desired.VersionNote &&
		row.PackageURL == desired.PackageURL &&
		row.PackageSHA256 == desired.PackageSHA256 &&
		row.PackageSize == desired.PackageSize &&
		row.DocCount == desired.DocCount &&
		row.DataSource == desired.DataSource &&
		jsonEqual(row.Files, desired.Files) &&
		jsonEqual(row.SampleQuestions, desired.SampleQuestions)
}

// jsonEqual compares JSON payloads semantically so byte-level differences
// introduced by the database (for example jsonb normalization) do not count
// as content changes.
func jsonEqual(a, b json.RawMessage) bool {
	if bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) {
		return true
	}
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// offlineRemovedItems marks published items that disappeared from the catalog
// as offline. Installs reference market_item_id, so rows are never deleted.
func offlineRemovedItems(tx *gorm.DB, now time.Time, catalogIDs []string) error {
	query := tx.Model(&orm.KnowledgeMarketItem{}).Where("status = ?", "published")
	if len(catalogIDs) > 0 {
		query = query.Where("id NOT IN ?", catalogIDs)
	}
	return query.Updates(map[string]any{
		"status":     "offline",
		"updated_at": now,
	}).Error
}
