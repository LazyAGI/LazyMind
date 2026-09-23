package search

import (
	"context"
	"encoding/json"
	"fmt"
	skillv2 "lazymind/core/skillv2"
	"sort"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
)

type ServiceDeps struct {
	DB *gorm.DB
}

type Service struct {
	db *gorm.DB
}

func NewService(deps ServiceDeps) *Service {
	return &Service{db: deps.DB}
}

func (s *Service) RebuildSkill(ctx context.Context, skillID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	return RebuildSkillTx(ctx, s.db.WithContext(ctx), skillID, time.Now())
}

func (s *Service) DeleteSkill(ctx context.Context, skillID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&indexRow{}).Error
	if isMissingIndexTable(err) {
		return nil
	}
	return err
}

func (s *Service) Contains(ctx context.Context, skillID, keyword string) (bool, error) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return true, nil
	}
	if s == nil || s.db == nil {
		return false, nil
	}
	if err := s.ensureFresh(ctx, skillID); err != nil {
		return false, err
	}
	var count int64
	pattern := "%" + escapeLike(keyword) + "%"
	tagPattern := "%" + escapeLike(jsonStringContent(keyword)) + "%"
	err := s.db.WithContext(ctx).Model(&indexRow{}).
		Where("skill_id = ? AND (LOWER(content) LIKE ? ESCAPE '!' OR LOWER(content) LIKE ? ESCAPE '!')", skillID, pattern, tagPattern).
		Count(&count).Error
	if isMissingIndexTable(err) {
		return containsHeadText(ctx, s.db, skillID, keyword)
	}
	return count > 0, err
}

type Hit struct {
	SkillID     string `json:"skill_id"`
	SkillKey    string `json:"skill_key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Request is assembled by trusted application code. AllowedSkillKeys contains
// only conversation-authorized manual skills; ownership is always checked here.
type Request struct {
	Query            string
	Field            string
	Value            []string
	Limit            int
	Exclude          []string
	AllowedSkillKeys []string
}

func (s *Service) Search(ctx context.Context, userID, query string, limit int, exclude []string) ([]Hit, error) {
	return s.Discover(ctx, userID, Request{Query: query, Limit: limit, Exclude: exclude})
}

func (s *Service) Discover(ctx context.Context, userID string, req Request) ([]Hit, error) {
	userID = strings.TrimSpace(userID)
	if s == nil || s.db == nil || userID == "" {
		return nil, nil
	}
	req.Field = strings.ToLower(strings.TrimSpace(req.Field))
	if req.Field != "" {
		switch req.Field {
		case "name", "description", "field", "tags", "aliases", "keywords", "category":
		default:
			return nil, fmt.Errorf("unsupported discovery field %q", req.Field)
		}
		if len(req.Value) == 0 {
			return nil, fmt.Errorf("discovery value required")
		}
	} else if strings.TrimSpace(req.Query) == "" {
		return nil, nil
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}
	if req.Limit > 20 {
		req.Limit = 20
	}
	allowed, excluded := map[string]bool{}, map[string]bool{}
	for _, key := range req.AllowedSkillKeys {
		allowed[strings.TrimSpace(key)] = true
	}
	for _, key := range req.Exclude {
		excluded[strings.TrimSpace(key)] = true
	}
	var rows []skillRow
	if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND deleted_at IS NULL AND head_revision_id IS NOT NULL", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	type scored struct {
		row   skillRow
		key   string
		score int
	}
	ranked := make([]scored, 0, len(rows))
	for _, row := range rows {
		key := strings.TrimSpace(row.Category) + "/" + strings.TrimSpace(row.SkillName)
		if excluded[key] {
			continue
		}
		if !skillv2.CallModeEnabled(skillv2.NormalizeCallMode(row.CallMode, row.IsEnabled)) && !allowed[key] {
			continue
		}
		score := discoveryScore(row, key, req.Query)
		if req.Field != "" {
			if !matchesField(row, req.Field, req.Value) {
				continue
			}
			score = 1
		}
		if score == 0 {
			continue
		}
		ranked = append(ranked, scored{row, key, score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].row.SortRank != ranked[j].row.SortRank {
			return ranked[i].row.SortRank > ranked[j].row.SortRank
		}
		if !ranked[i].row.CreatedAt.Equal(ranked[j].row.CreatedAt) {
			return ranked[i].row.CreatedAt.After(ranked[j].row.CreatedAt)
		}
		return ranked[i].key < ranked[j].key
	})
	if len(ranked) > req.Limit {
		ranked = ranked[:req.Limit]
	}
	hits := make([]Hit, 0, len(ranked))
	for _, item := range ranked {
		hits = append(hits, Hit{SkillID: item.row.ID, SkillKey: item.key, Name: item.row.SkillName, Description: item.row.Description})
	}
	return hits, nil
}

func decodeStrings(raw []byte) []string {
	var values []string
	_ = json.Unmarshal(raw, &values)
	return values
}
func fieldValues(row skillRow, field string) []string {
	switch field {
	case "name":
		return []string{row.SkillName}
	case "description":
		return []string{row.Description}
	case "category":
		return []string{row.Category}
	case "field":
		return []string{row.Field}
	case "tags":
		return decodeStrings(row.Tags)
	case "aliases":
		return decodeStrings(row.Aliases)
	case "keywords":
		return decodeStrings(row.Keywords)
	}
	return nil
}

// Structured arrays use all-of exact, case-insensitive element matching.
func matchesField(row skillRow, field string, values []string) bool {
	for _, wanted := range values {
		wanted = strings.TrimSpace(wanted)
		if wanted == "" {
			return false
		}
		found := false
		for _, actual := range fieldValues(row, field) {
			if strings.EqualFold(strings.TrimSpace(actual), wanted) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(values) > 0
}
func discoveryScore(row skillRow, key, query string) int {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return 0
	}
	if strings.EqualFold(row.SkillName, needle) || strings.EqualFold(key, needle) {
		return 10000
	}
	name := strings.ToLower(row.SkillName)
	metadata := strings.ToLower(strings.Join(append(append(append([]string{row.Field}, decodeStrings(row.Tags)...), decodeStrings(row.Aliases)...), decodeStrings(row.Keywords)...), " "))
	desc := strings.ToLower(row.Description)
	score := 0
	// An alias or keyword can occur inside a longer natural-language instruction,
	// including Chinese sentences without whitespace separators.
	for _, field := range []string{"aliases", "keywords"} {
		for _, phrase := range fieldValues(row, field) {
			phrase = strings.ToLower(strings.TrimSpace(phrase))
			if len([]rune(phrase)) > 1 && strings.Contains(needle, phrase) {
				score += 60
			}
		}
	}
	if strings.Contains(name, needle) {
		score += 100
	}
	if strings.Contains(metadata, needle) {
		score += 60
	}
	if strings.Contains(desc, needle) {
		score += 40
	}
	stops := map[string]bool{"a": true, "an": true, "the": true, "to": true, "for": true, "and": true, "or": true, "with": true, "please": true, "help": true, "me": true, "i": true, "need": true, "skill": true, "use": true}
	seen := map[string]bool{}
	for _, token := range strings.FieldsFunc(needle, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if stops[token] || seen[token] {
			continue
		}
		seen[token] = true
		if strings.Contains(name, token) {
			score += 10
		}
		if strings.Contains(metadata, token) {
			score += 6
		}
		if strings.Contains(desc, token) {
			score += 4
		}
	}
	return score
}

// KeywordScope returns a set-based, freshness-aware predicate for list queries.
// The read path guarantees fresh search results but does not repair missing or
// stale search index rows; stale and missing indexes fall back to the current
// head revision content instead of rebuilding per skill.
func (s *Service) KeywordScope(keyword string) func(*gorm.DB) *gorm.DB {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return func(db *gorm.DB) *gorm.DB { return db }
	}
	hasIndexTable := s != nil && s.db != nil && s.db.Migrator().HasTable(&indexRow{})
	pattern := "%" + escapeLike(keyword) + "%"
	tagPattern := "%" + escapeLike(jsonStringContent(keyword)) + "%"
	contentExpr := blobContentTextExpr(s.db)
	tagsExpr := tagsTextExpr(s.db)
	metadataText := "COALESCE(skills.field, '') || ' ' || COALESCE(CAST(skills.aliases AS TEXT), '') || ' ' || COALESCE(CAST(skills.keywords AS TEXT), '')"

	metadataPredicate := "(LOWER(skills.skill_name) LIKE ? ESCAPE '!' OR LOWER(skills.category) LIKE ? ESCAPE '!' OR LOWER(skills.description) LIKE ? ESCAPE '!' OR LOWER(" + tagsExpr + ") LIKE ? ESCAPE '!' OR LOWER(" + metadataText + ") LIKE ? ESCAPE '!')"
	headPredicate := `EXISTS (
		SELECT 1
		FROM skill_revision_entries AS e
		JOIN skill_blobs AS b ON b.hash = e.blob_hash
		WHERE e.revision_id = skills.head_revision_id
		  AND e.entry_type = ?
		  AND b."binary" = ?
		  AND (LOWER(e.path) LIKE ? ESCAPE '!' OR LOWER(` + contentExpr + `) LIKE ? ESCAPE '!')
	)`
	headArgs := []any{"file", false, pattern, pattern}

	if !hasIndexTable {
		return func(db *gorm.DB) *gorm.DB {
			args := []any{pattern, pattern, pattern, tagPattern, tagPattern}
			args = append(args, headArgs...)
			return db.Where("("+metadataPredicate+" OR "+headPredicate+")", args...)
		}
	}

	indexPredicate := `EXISTS (
		SELECT 1
		FROM skill_search_indexes AS idx
		WHERE idx.skill_id = skills.id
		  AND idx.head_revision_id = skills.head_revision_id
		  AND (LOWER(idx.content) LIKE ? ESCAPE '!' OR LOWER(idx.content) LIKE ? ESCAPE '!')
	)`
	fallbackPredicate := `NOT EXISTS (
		SELECT 1
		FROM skill_search_indexes AS fresh_idx
		WHERE fresh_idx.skill_id = skills.id
		  AND fresh_idx.head_revision_id = skills.head_revision_id
	) AND ` + headPredicate

	return func(db *gorm.DB) *gorm.DB {
		args := []any{pattern, pattern, pattern, tagPattern, tagPattern, pattern, tagPattern}
		args = append(args, headArgs...)
		return db.Where("("+metadataPredicate+" OR "+indexPredicate+" OR ("+fallbackPredicate+"))", args...)
	}
}

func RebuildSkillTx(ctx context.Context, tx *gorm.DB, skillID string, now time.Time) error {
	if tx == nil {
		return nil
	}
	var skill skillRow
	if err := tx.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return err
	}
	if skill.DeletedAt != nil || skill.HeadRevisionID == nil {
		err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&indexRow{}).Error
		if isMissingIndexTable(err) {
			return nil
		}
		return err
	}
	content, err := searchContentForRevision(ctx, tx, skill, *skill.HeadRevisionID)
	if err != nil {
		return err
	}
	err = tx.WithContext(ctx).Save(&indexRow{
		SkillID:        skill.ID,
		OwnerUserID:    skill.OwnerUserID,
		HeadRevisionID: *skill.HeadRevisionID,
		Content:        content,
		UpdatedAt:      now,
	}).Error
	if isMissingIndexTable(err) {
		return nil
	}
	return err
}

func (s *Service) ensureFresh(ctx context.Context, skillID string) error {
	var skill skillRow
	if err := s.db.WithContext(ctx).Select("id", "head_revision_id", "deleted_at").Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return err
	}
	if skill.DeletedAt != nil || skill.HeadRevisionID == nil {
		return s.DeleteSkill(ctx, skillID)
	}
	var row indexRow
	err := s.db.WithContext(ctx).Select("skill_id", "head_revision_id").Where("skill_id = ?", skillID).Take(&row).Error
	if err == nil && row.HeadRevisionID == *skill.HeadRevisionID {
		return nil
	}
	if isMissingIndexTable(err) {
		return nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return s.RebuildSkill(ctx, skillID)
}

func containsHeadText(ctx context.Context, db *gorm.DB, skillID, keyword string) (bool, error) {
	var skill skillRow
	if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return false, err
	}
	if skill.DeletedAt != nil || skill.HeadRevisionID == nil {
		return false, nil
	}
	content, err := searchContentForRevision(ctx, db, skill, *skill.HeadRevisionID)
	if err != nil {
		return false, err
	}
	lowered := strings.ToLower(content)
	return strings.Contains(lowered, keyword) || strings.Contains(lowered, jsonStringContent(keyword)), nil
}

func isMissingIndexTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "skill_search_indexes") &&
		(strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "sqlstate 42p01"))
}

func searchContentForRevision(ctx context.Context, tx *gorm.DB, skill skillRow, revisionID string) (string, error) {
	parts := []string{skill.SkillName, skill.Category, skill.Description, string(skill.Tags), skill.Field, strings.Join(decodeStrings(skill.Aliases), " "), strings.Join(decodeStrings(skill.Keywords), " ")}
	var rows []struct {
		Path    string
		Content []byte
	}
	if err := tx.WithContext(ctx).
		Table("skill_revision_entries AS e").
		Select("e.path, b.content").
		Joins("JOIN skill_blobs AS b ON b.hash = e.blob_hash").
		Where("e.revision_id = ? AND e.entry_type = ? AND b.\"binary\" = ?", revisionID, "file", false).
		Order("e.path ASC").
		Find(&rows).Error; err != nil {
		return "", err
	}
	for _, row := range rows {
		parts = append(parts, row.Path, string(row.Content))
	}
	return strings.Join(parts, "\n"), nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `!`, `!!`)
	value = strings.ReplaceAll(value, `%`, `!%`)
	value = strings.ReplaceAll(value, `_`, `!_`)
	return value
}

func jsonStringContent(value string) string {
	raw, _ := json.Marshal(value)
	return strings.TrimSuffix(strings.TrimPrefix(string(raw), `"`), `"`)
}

func tagsTextExpr(db *gorm.DB) string {
	if db != nil && db.Dialector != nil {
		switch db.Dialector.Name() {
		case "mysql":
			return "CAST(skills.tags AS CHAR)"
		case "postgres":
			return "skills.tags::text"
		}
	}
	return "CAST(skills.tags AS TEXT)"
}

func blobContentTextExpr(db *gorm.DB) string {
	if db != nil && db.Dialector != nil {
		switch db.Dialector.Name() {
		case "mysql":
			return "CAST(b.content AS CHAR)"
		case "postgres":
			return "convert_from(b.content, 'UTF8')"
		}
	}
	return "CAST(b.content AS TEXT)"
}

type indexRow struct {
	SkillID        string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	OwnerUserID    string    `gorm:"column:owner_user_id;type:varchar(255);not null"`
	HeadRevisionID string    `gorm:"column:head_revision_id;type:varchar(36);not null"`
	Content        string    `gorm:"column:content;type:text;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (indexRow) TableName() string { return "skill_search_indexes" }

type skillRow struct {
	ID             string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID    string     `gorm:"column:owner_user_id;type:varchar(255);not null"`
	Category       string     `gorm:"column:category;type:varchar(128);not null"`
	SkillName      string     `gorm:"column:skill_name;type:varchar(255);not null"`
	Description    string     `gorm:"column:description;type:text"`
	Tags           []byte     `gorm:"column:tags;type:json"`
	Field          string     `gorm:"column:field;type:text;not null;default:''"`
	Aliases        []byte     `gorm:"column:aliases;type:json;not null;default:'[]'"`
	Keywords       []byte     `gorm:"column:keywords;type:json;not null;default:'[]'"`
	HeadRevisionID *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	IsEnabled      bool       `gorm:"column:is_enabled;not null;default:true"`
	CallMode       string     `gorm:"column:call_mode;type:varchar(16);not null;default:'on_demand'"`
	SortRank       int64      `gorm:"column:sort_rank;not null;default:0"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
}

func (skillRow) TableName() string { return "skills" }
