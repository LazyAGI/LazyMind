package modelconfig

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/modelprovider"
	"strings"
)

// ReadingOption is deliberately a safe projection, never a runtime configuration.
type ReadingOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type readingParserRow struct {
	GroupID          string
	ProviderName     string
	BaseURL          string
	APIKey           string
	APIKeyCiphertext string
	Owner            string
	Name             string
}

func readingParsers(ctx context.Context, db *gorm.DB, user string) ([]readingParserRow, error) {
	rows := []readingParserRow{}
	err := db.WithContext(ctx).Table("user_model_provider_groups g").Select("g.id AS group_id,g.name,g.create_user_id AS owner,p.name AS provider_name,g.base_url,g.api_key,g.api_key_ciphertext").
		Joins("JOIN user_model_providers p ON p.id=g.user_model_provider_id AND p.create_user_id=g.create_user_id AND p.deleted_at IS NULL").
		Where("g.deleted_at IS NULL AND p.category=? AND (g.is_verified=? OR (g.create_user_id=? AND EXISTS (SELECT 1 FROM user_selected_providers own WHERE own.user_id=g.create_user_id AND own.user_model_provider_group_id=g.id AND own.category=?)))", "ocr", true, user, "ocr").
		Where("g.create_user_id=? OR EXISTS (SELECT 1 FROM user_selected_providers s WHERE s.user_model_provider_group_id=g.id AND s.user_id=g.create_user_id AND s.category=? AND s.share=?)", user, "ocr", true).
		Order("p.name,g.name,g.id").Scan(&rows).Error
	return rows, err
}
func parserRef(row readingParserRow, user string) string {
	source := "shared"
	if row.Owner == user {
		source = "own"
	}
	return source + ":" + row.GroupID
}
func ReadingParserOptions(ctx context.Context, db *gorm.DB, user string) ([]ReadingOption, error) {
	rows, err := readingParsers(ctx, db, user)
	out := []ReadingOption{}
	for _, row := range rows {
		if normalizeOCRType(row.ProviderName) == "mineru" && strings.TrimSpace(row.BaseURL) != "" {
			out = append(out, ReadingOption{parserRef(row, user), row.ProviderName + " / " + row.Name})
		}
	}
	return out, err
}
func ResolveReadingParser(ctx context.Context, db *gorm.DB, user, ref string) (map[string]any, error) {
	if ref == "" {
		return LoadOCRConfig(ctx, db, user)
	}
	rows, err := readingParsers(ctx, db, user)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if parserRef(row, user) != ref || normalizeOCRType(row.ProviderName) != "mineru" || strings.TrimSpace(row.BaseURL) == "" {
			continue
		}
		key, err := modelprovider.ResolveAPIKey(row.APIKey, row.APIKeyCiphertext)
		if err != nil {
			return nil, errors.New("文档解析服务凭据不可用")
		}
		cfg := map[string]any{"ocr_type": "mineru", "ocr_url": row.BaseURL}
		if auth := normalizeOCRAuthValue(key); auth != nil {
			cfg["ocr_auth"] = map[string]any{"mineru": auth}
		}
		return cfg, nil
	}
	return nil, errors.New("所选文档解析服务不可用，请在 AI 阅读准备中重新选择")
}
func ResolveReadingStudyModel(ctx context.Context, db *gorm.DB, user, ref string) (*AvailableChatModel, error) {
	models, err := LoadAvailableChatModels(ctx, db, user)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		m, e := ResolveDefaultChatModel(ctx, db, user, models)
		if e != nil {
			return nil, e
		}
		if m != nil {
			return m, nil
		}
	}
	for i := range models {
		m := &models[i]
		if ChatCandidateID(m.Source, m.ID) == ref && ChatModelSelectable(m) {
			return m, nil
		}
	}
	return nil, errors.New("所选研读模型不可用，请在 AI 阅读准备中重新选择")
}
func ReadingStudyOptions(ctx context.Context, db *gorm.DB, user string) ([]ReadingOption, error) {
	models, err := LoadAvailableChatModels(ctx, db, user)
	out := []ReadingOption{}
	for i := range models {
		m := &models[i]
		if ChatModelSelectable(m) {
			out = append(out, ReadingOption{ChatCandidateID(m.Source, m.ID), m.ProviderName + " / " + m.GroupName + " / " + m.ModelName})
		}
	}
	return out, err
}
