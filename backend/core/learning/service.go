package learning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// BuildCacheKey keeps globally reusable facts reusable while preventing
// translations and context-sensitive results from colliding.
func BuildCacheKey(capability, text, sourceLanguage, targetLanguage, context, document string, start, end int) string {
	legacy := buildLegacyCacheKey(capability, text, sourceLanguage, targetLanguage, context, document, start, end)
	version := 1
	if def, ok := CapabilityByKey(capability); ok {
		version = def.Version
	}
	return fmt.Sprintf("learning-v1\x1f%s\x1f%d\x1f1\x1f%s", capability, version, legacy)
}

func buildLegacyCacheKey(capability, text, sourceLanguage, targetLanguage, context, document string, start, end int) string {
	base := normalize(text)
	if capability == "general_translation" {
		return strings.Join([]string{base, normalize(sourceLanguage), normalize(targetLanguage)}, "\x1f")
	}
	if def, ok := CapabilityByKey(capability); ok && def.CachePolicy.ContextSensitive && strings.TrimSpace(context) != "" {
		signature := fmt.Sprintf("%s|%d|%d|%s", document, start, end, normalize(context))
		sum := sha256.Sum256([]byte(signature))
		return fmt.Sprintf("%s\x1f%x", base, sum[:8])
	}
	return base
}

type Service struct{ db *gorm.DB }

func LocalAvailable() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_VOCABULARY_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func requireLocal() error {
	if !LocalAvailable() {
		return errors.New("learning feature requires the Desktop local backend")
	}
	return nil
}

func New(db *gorm.DB) *Service { return &Service{db: db} }
func (s *Service) requireDatasetOwner(ctx context.Context, owner, dataset string) error {
	var row orm.Dataset
	if err := s.db.WithContext(ctx).Where("id = ? AND create_user_id = ?", dataset, owner).First(&row).Error; err != nil {
		return errors.New("knowledge base not found or not writable")
	}
	return nil
}
func normalize(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(strings.TrimSpace(v))), " "))
}

type CapabilityRef struct {
	Key          string         `json:"key"`
	Version      int            `json:"version"`
	Enabled      bool           `json:"enabled"`
	DisplayOrder int            `json:"display_order"`
	Settings     map[string]any `json:"settings,omitempty"`
}

func BuiltinProfiles() []ProfileDefinition { return append([]ProfileDefinition(nil), profiles...) }
func Capabilities() []Capability           { return append([]Capability(nil), capabilities...) }
func QuestionTypes() []QuestionType        { return append([]QuestionType(nil), questionTypes...) }

func (s *Service) ListProfiles(ctx context.Context, owner string) ([]CapabilityProfile, error) {
	rows := make([]CapabilityProfile, 0)
	err := s.db.WithContext(ctx).Where("owner_id = ?", owner).Order("created_at").Find(&rows).Error
	return rows, err
}
func (s *Service) CreateProfile(ctx context.Context, owner, name, description string, refs []CapabilityRef) (CapabilityProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CapabilityProfile{}, errors.New("profile name is required")
	}
	if err := validateRefs(refs); err != nil {
		return CapabilityProfile{}, err
	}
	now := time.Now().UTC()
	row := CapabilityProfile{ID: uuid.NewString(), OwnerID: owner, CustomName: name, Description: strings.TrimSpace(description), CapabilityRefsJSON: marshal(refs), CreatedAt: now, UpdatedAt: now}
	return row, s.db.WithContext(ctx).Create(&row).Error
}
func (s *Service) ProfileRefs(ctx context.Context, owner, key string) ([]CapabilityRef, error) {
	for _, profile := range profiles {
		if profile.Key == key {
			refs := make([]CapabilityRef, 0, len(profile.Capabilities))
			for i, capability := range profile.Capabilities {
				refs = append(refs, CapabilityRef{Key: capability, Version: 1, Enabled: true, DisplayOrder: i + 1})
			}
			return refs, nil
		}
	}
	var row CapabilityProfile
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", key, owner).First(&row).Error; err != nil {
		return nil, errors.New("capability profile not found")
	}
	var refs []CapabilityRef
	if json.Unmarshal([]byte(row.CapabilityRefsJSON), &refs) != nil {
		return nil, errors.New("capability profile is invalid")
	}
	return refs, validateRefs(refs)
}
func validateRefs(refs []CapabilityRef) error {
	if len(refs) == 0 {
		return errors.New("at least one capability is required")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		def, ok := CapabilityByKey(ref.Key)
		if !ok {
			return errors.New("unsupported learning capability")
		}
		if ref.Version != 0 && ref.Version != def.Version {
			return errors.New("unsupported capability version")
		}
		if seen[ref.Key] {
			return errors.New("duplicate learning capability")
		}
		seen[ref.Key] = true
	}
	return nil
}
func (s *Service) PutKnowledgeBaseCapabilities(ctx context.Context, owner, dataset string, refs []CapabilityRef) error {
	if err := s.requireDatasetOwner(ctx, owner, dataset); err != nil {
		return err
	}
	if err := validateRefs(refs); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		desired := map[string]bool{}
		for i, ref := range refs {
			def, _ := CapabilityByKey(ref.Key)
			if err := validateCapabilitySettings(def, ref.Settings); err != nil {
				return err
			}
			desired[def.Key] = true
			order := ref.DisplayOrder
			if order == 0 {
				order = i + 1
			}
			row := KnowledgeBaseCapability{ID: uuid.NewString(), OwnerID: owner, DatasetID: dataset, CapabilityKey: def.Key, CapabilityVersion: def.Version, Enabled: ref.Enabled, DisplayOrder: order, SettingsJSON: marshal(ref.Settings), CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dataset_id"}, {Name: "capability_key"}}, DoUpdates: clause.AssignmentColumns([]string{"owner_id", "capability_version", "enabled", "display_order", "settings_json", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		var existing []KnowledgeBaseCapability
		if err := tx.Where("owner_id = ? AND dataset_id = ?", owner, dataset).Find(&existing).Error; err != nil {
			return err
		}
		for _, row := range existing {
			if !desired[row.CapabilityKey] {
				if err := tx.Model(&row).Updates(map[string]any{"enabled": false, "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func validateCapabilitySettings(def Capability, settings map[string]any) error {
	for key := range settings {
		switch key {
		case "cache_scope", "allow_llm_fallback", "target_language", "output_language", "max_selection_length", "max_candidates_per_block", "max_document_candidates":
		default:
			return fmt.Errorf("unsupported capability setting: %s", key)
		}
	}
	if scope := strings.TrimSpace(fmt.Sprint(settings["cache_scope"])); scope != "" && scope != "<nil>" && !contains(def.CachePolicy.AllowedScopes, scope) {
		return errors.New("cache scope is not allowed for capability")
	}
	if raw, ok := settings["max_selection_length"]; ok {
		value, valid := numericSetting(raw)
		if !valid || value < 1 || value > 10000 {
			return errors.New("invalid maximum selection length")
		}
	}
	for key, maximum := range map[string]float64{"max_candidates_per_block": 100, "max_document_candidates": 1000} {
		if raw, ok := settings[key]; ok {
			value, valid := numericSetting(raw)
			if !valid || value < 1 || value > maximum || math.Trunc(value) != value {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	if raw, ok := settings["allow_llm_fallback"]; ok {
		if _, valid := raw.(bool); !valid {
			return errors.New("knowledge base capability settings are invalid")
		}
	}
	if raw, ok := settings["target_language"]; ok {
		if _, valid := raw.(string); !valid {
			return errors.New("knowledge base capability settings are invalid")
		}
	}
	if raw, ok := settings["output_language"]; ok {
		language, valid := raw.(string)
		if !valid || !contains([]string{"auto", "zh-Hans", "en", "zh-Hans+en"}, strings.TrimSpace(language)) {
			return errors.New("invalid capability output language")
		}
	}
	return nil
}

func numericSetting(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	default:
		return 0, false
	}
}

func (s *Service) configuredCapability(ctx context.Context, owner, dataset, key string) (Capability, map[string]any, error) {
	def, ok := CapabilityByKey(key)
	if !ok {
		return Capability{}, nil, errors.New("unsupported learning capability")
	}
	settings := map[string]any{}
	if dataset == "" {
		return def, settings, nil
	}
	var row KnowledgeBaseCapability
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND dataset_id = ? AND capability_key = ? AND enabled = ?", owner, dataset, key, true).First(&row).Error; err != nil {
		return Capability{}, nil, errors.New("learning capability is not enabled for knowledge base")
	}
	if row.SettingsJSON != "" && json.Unmarshal([]byte(row.SettingsJSON), &settings) != nil {
		return Capability{}, nil, errors.New("knowledge base capability settings are invalid")
	}
	if err := validateCapabilitySettings(def, settings); err != nil {
		return Capability{}, nil, err
	}
	return def, settings, nil
}
func (s *Service) ListKnowledgeBaseCapabilities(ctx context.Context, owner, dataset string) ([]KnowledgeBaseCapability, error) {
	if err := s.requireDatasetOwner(ctx, owner, dataset); err != nil {
		return nil, err
	}
	var rows []KnowledgeBaseCapability
	err := s.db.WithContext(ctx).Where("owner_id = ? AND dataset_id = ?", owner, dataset).Order("display_order").Find(&rows).Error
	return rows, err
}

type PresetInput struct {
	ScopeType, ScopeID, DocumentRevision, CapabilityKey, Key string
	Value                                                    map[string]any
	SchemaVersion, Priority                                  int
	Origin                                                   string
}

func (s *Service) PutPreset(ctx context.Context, owner string, in PresetInput) (Preset, error) {
	def, ok := CapabilityByKey(in.CapabilityKey)
	if !ok {
		return Preset{}, errors.New("unsupported learning capability")
	}
	if in.ScopeType != "user_global" && in.ScopeType != "knowledge_base" && in.ScopeType != "document" {
		return Preset{}, errors.New("invalid preset scope")
	}
	if !contains(def.CachePolicy.AllowedScopes, in.ScopeType) {
		return Preset{}, errors.New("cache scope is not allowed for capability")
	}
	if in.ScopeType != "user_global" && strings.TrimSpace(in.ScopeID) == "" {
		return Preset{}, errors.New("scope_id is required")
	}
	if in.ScopeType == "knowledge_base" {
		if err := s.requireDatasetOwner(ctx, owner, in.ScopeID); err != nil {
			return Preset{}, err
		}
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	now := time.Now().UTC()
	row := Preset{ID: uuid.NewString(), OwnerID: owner, ScopeType: in.ScopeType, ScopeID: strings.TrimSpace(in.ScopeID), DocumentRevision: in.DocumentRevision, CapabilityKey: def.Key, CapabilityVersion: def.Version, NormalizedKey: normalize(in.Key), ValueJSON: marshal(in.Value), Origin: in.Origin, Status: "published", SchemaVersion: in.SchemaVersion, Priority: in.Priority, UserEdited: in.Origin == "user", CreatedAt: now, UpdatedAt: now}
	query := s.db.WithContext(ctx).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND capability_key = ? AND normalized_key = ? AND schema_version = ?", owner, row.ScopeType, row.ScopeID, row.CapabilityKey, row.NormalizedKey, row.SchemaVersion)
	var existing Preset
	if err := query.First(&existing).Error; err == nil {
		row.ID, row.CreatedAt = existing.ID, existing.CreatedAt
		err = query.Updates(map[string]any{"value_json": row.ValueJSON, "origin": row.Origin, "status": row.Status, "priority": row.Priority, "user_edited": row.UserEdited, "document_revision": row.DocumentRevision, "updated_at": row.UpdatedAt}).Error
		return row, err
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Preset{}, err
	}
	return row, s.db.WithContext(ctx).Create(&row).Error
}
func (s *Service) ResolvePreset(ctx context.Context, owner, capability, key, dataset, document, revision string) (*Preset, error) {
	scopes := []struct{ kind, id string }{{"document", document}, {"knowledge_base", dataset}, {"user_global", ""}}
	for _, scope := range scopes {
		if scope.kind != "user_global" && scope.id == "" {
			continue
		}
		var row Preset
		q := s.db.WithContext(ctx).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND capability_key = ? AND normalized_key = ? AND status = ?", owner, scope.kind, scope.id, capability, normalize(key), "published").Order("user_edited DESC, priority DESC, updated_at DESC")
		if scope.kind == "document" && revision != "" {
			q = q.Where("document_revision = ? OR document_revision = ''", revision)
		}
		if err := q.First(&row).Error; err == nil {
			return &row, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return nil, nil
}

// resolveContextualDocumentPresetByText is the final fallback for a confirmed
// preanalysis answer. Preanalysis keys include the source context and offsets,
// which can differ from a later browser selection even when the selected term
// is identical. Exact contextual keys are always resolved first; this fallback
// only reuses a published answer for the same term, capability, document, and
// revision.
func (s *Service) resolveContextualDocumentPresetByText(ctx context.Context, owner, capability, text, document, revision string) (*Preset, error) {
	if document == "" {
		return nil, nil
	}
	def, ok := CapabilityByKey(capability)
	if !ok || !def.CachePolicy.ContextSensitive {
		return nil, nil
	}
	q := s.db.WithContext(ctx).Where("owner_id = ? AND scope_type = ? AND scope_id = ? AND capability_key = ? AND status = ?", owner, "document", document, capability, "published").Order("user_edited DESC, priority DESC, updated_at DESC")
	if revision != "" {
		q = q.Where("document_revision = ? OR document_revision = ''", revision)
	}
	var rows []Preset
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	wanted := normalize(text)
	for i := range rows {
		parts := strings.Split(rows[i].NormalizedKey, "\x1f")
		if len(parts) >= 6 && parts[0] == "learning-v1" && parts[1] == capability && parts[4] == wanted {
			return &rows[i], nil
		}
	}
	return nil, nil
}

func (s *Service) ListPresets(ctx context.Context, owner, scopeType, scopeID, capability string) ([]Preset, error) {
	q := s.db.WithContext(ctx).Where("owner_id = ? AND status = ?", owner, "published")
	if scopeType != "" {
		q = q.Where("scope_type = ?", scopeType)
	}
	if scopeID != "" {
		q = q.Where("scope_id = ?", scopeID)
	}
	if capability != "" {
		q = q.Where("capability_key = ?", capability)
	}
	var rows []Preset
	err := q.Order("updated_at DESC").Find(&rows).Error
	return rows, err
}

func (s *Service) UpdatePreset(ctx context.Context, owner, id string, value map[string]any, priority int) (Preset, error) {
	var row Preset
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&row).Error; err != nil {
		return row, err
	}
	def, ok := CapabilityByKey(row.CapabilityKey)
	if !ok {
		return row, errors.New("unsupported learning capability")
	}
	if len(requiredMissing(def, value)) > 0 {
		return row, errors.New("resolved content misses required fields")
	}
	row.ValueJSON, row.Priority, row.UserEdited, row.Origin, row.Status, row.UpdatedAt = marshal(value), priority, true, "user", "published", time.Now().UTC()
	err := s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"value_json": row.ValueJSON, "priority": priority, "user_edited": true, "origin": "user", "status": "published", "updated_at": row.UpdatedAt}).Error
	return row, err
}

func (s *Service) DeletePreset(ctx context.Context, owner, id string) error {
	result := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ? AND owner_id = ?", id, owner).Updates(map[string]any{"status": "stale", "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func analyzeText(text string) (string, []string) {
	runes := []rune(strings.TrimSpace(text))
	hasHan, hasLatin := false, false
	for _, r := range runes {
		hasHan = hasHan || unicode.Is(unicode.Han, r)
		hasLatin = hasLatin || unicode.IsLetter(r) && r < unicode.MaxLatin1
	}
	if hasHan {
		kind := "word"
		if len(runes) == 1 {
			kind = "character"
		} else if len(runes) > 20 {
			kind = "passage"
		} else if strings.ContainsAny(text, "。！？；，") {
			kind = "sentence"
		}
		return "zh-Hans", []string{kind}
	}
	if hasLatin {
		return "en", []string{"word", "phrase"}
	}
	return "und", []string{"phrase"}
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want || v == "*" {
			return true
		}
	}
	return false
}
func (s *Service) CreateBook(ctx context.Context, owner string, row Book, questions []string) (Book, error) {
	if err := requireLocal(); err != nil {
		return row, err
	}
	def, ok := CapabilityByKey(row.CapabilityKey)
	if !ok {
		return row, errors.New("unsupported learning capability")
	}
	if len(questions) == 0 {
		questions = def.DefaultQuestionTypes
	}
	questions, err := normalizeBookQuestions(def, questions)
	if err != nil {
		return row, err
	}
	if strings.TrimSpace(row.Name) == "" {
		return row, errors.New("book name is required")
	}
	now := time.Now().UTC()
	row.ID = uuid.NewString()
	row.OwnerID = owner
	row.CapabilityVersion = def.Version
	row.SchemaVersion = 1
	row.QuestionTypesJSON = marshal(questions)
	row.CreatedAt = now
	row.UpdatedAt = now
	return row, s.db.WithContext(ctx).Create(&row).Error
}

func normalizeBookQuestions(def Capability, questions []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, key := range def.AllowedQuestionTypes {
		allowed[key] = true
	}
	if len(questions) == 0 {
		return nil, errors.New("learning collection has no question types")
	}
	out, seen := make([]string, 0, len(questions)), map[string]bool{}
	for _, key := range questions {
		if !allowed[key] {
			return nil, errors.New("question type is not supported by capability")
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out, nil
}

func (s *Service) UpdateBook(ctx context.Context, owner, id, name, description, capability string, questions []string) (Book, error) {
	if err := requireLocal(); err != nil {
		return Book{}, err
	}
	var row Book
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND archived_at IS NULL", id, owner).First(&row).Error; err != nil {
		return row, err
	}
	if capability != "" && capability != row.CapabilityKey {
		return row, errors.New("learning capability cannot be changed")
	}
	def, _ := CapabilityByKey(row.CapabilityKey)
	questions, err := normalizeBookQuestions(def, questions)
	if err != nil {
		return row, err
	}
	if strings.TrimSpace(name) == "" {
		return row, errors.New("book name is required")
	}
	row.Name, row.Description, row.QuestionTypesJSON, row.UpdatedAt = strings.TrimSpace(name), strings.TrimSpace(description), marshal(questions), time.Now().UTC()
	err = s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"name": row.Name, "description": row.Description, "question_types_json": row.QuestionTypesJSON, "updated_at": row.UpdatedAt}).Error
	return row, err
}

func (s *Service) ArchiveBook(ctx context.Context, owner, id string) error {
	if err := requireLocal(); err != nil {
		return err
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&Book{}).Where("id = ? AND owner_id = ? AND archived_at IS NULL", id, owner).Update("archived_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func sortStrings(v []string) { sort.Strings(v) }

var _ = json.Valid
