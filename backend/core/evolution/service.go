package evolution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	appLog "lazymind/core/log"
)

type SkillState struct {
	Resource     *orm.SkillResource
	RelativePath string
	Content      string
	ContentHash  string
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	out := make([]byte, 36)
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out)
}

func NewID() string {
	return newUUID()
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func ParentSkillRelativePath(category, skillName string) string {
	category = strings.TrimSpace(category)
	skillName = strings.TrimSpace(skillName)
	return filepath.ToSlash(filepath.Join(category, skillName, "SKILL.md"))
}

func ChildSkillRelativePath(category, parentSkillName, skillName, fileExt string) string {
	category = strings.TrimSpace(category)
	parentSkillName = strings.TrimSpace(parentSkillName)
	skillName = strings.TrimSpace(skillName)
	fileExt = strings.TrimSpace(strings.TrimPrefix(fileExt, "."))
	if fileExt == "" {
		fileExt = "md"
	}
	return filepath.ToSlash(filepath.Join(category, parentSkillName, fmt.Sprintf("%s.%s", skillName, strings.ToLower(fileExt))))
}

func SkillSuggestionResourceKey(row orm.SkillResource) string {
	return strings.TrimSpace(row.ID)
}

func SystemResourceKey(resourceType string) string {
	switch resourceType {
	case ResourceTypeMemory:
		return "memory"
	case ResourceTypeUserPreference:
		return "user_preference"
	default:
		return strings.TrimSpace(resourceType)
	}
}

func EnsureSystemMemory(ctx context.Context, db *gorm.DB, userID, userName string) (*orm.SystemMemory, error) {
	tx := db.WithContext(ctx)
	var row orm.SystemMemory
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	err := tx.Where("user_id = ?", userID).Order("created_at ASC").Take(&row).Error
	if err == nil {
		if strings.TrimSpace(row.ContentHash) == "" {
			row.ContentHash = HashContent(row.Content)
			row.UpdatedAt = time.Now()
			if saveErr := tx.Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(map[string]any{
				"content_hash": row.ContentHash,
				"updated_at":   row.UpdatedAt,
			}).Error; saveErr != nil {
				return nil, saveErr
			}
			appLog.Logger.Info().
				Str("user_id", userID).
				Str("memory_id", row.ID).
				Msg("backfilled missing system memory content hash")
		}
		return &row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	seed, err := loadLegacySystemMemoryTemplate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	row = orm.SystemMemory{
		ID:            newUUID(),
		UserID:        userID,
		Content:       firstNonEmpty(seed.Content, ""),
		ContentHash:   firstNonEmpty(strings.TrimSpace(seed.ContentHash), HashContent(seed.Content)),
		Version:       maxInt64(1, seed.Version),
		AutoEvo:       true,
		UpdatedBy:     firstNonEmpty(userID, seed.UpdatedBy, "system"),
		UpdatedByName: firstNonEmpty(userName, seed.UpdatedByName, "system"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := tx.Create(&row).Error; err != nil {
		return nil, err
	}
	appLog.Logger.Info().
		Str("user_id", userID).
		Str("memory_id", row.ID).
		Bool("seeded_from_legacy_template", strings.TrimSpace(seed.ID) != "").
		Msg("created system memory row")
	return &row, nil
}

func EnsureSystemUserPreference(ctx context.Context, db *gorm.DB, userID, userName string) (*orm.SystemUserPreference, error) {
	tx := db.WithContext(ctx)
	var row orm.SystemUserPreference
	userID = strings.TrimSpace(userID)
	userName = strings.TrimSpace(userName)
	err := tx.Where("user_id = ?", userID).Order("created_at ASC").Take(&row).Error
	if err == nil {
		if strings.TrimSpace(row.ContentHash) == "" {
			row.ContentHash = HashContent(row.Content)
			row.UpdatedAt = time.Now()
			if saveErr := tx.Model(&orm.SystemUserPreference{}).Where("id = ?", row.ID).Updates(map[string]any{
				"content_hash": row.ContentHash,
				"updated_at":   row.UpdatedAt,
			}).Error; saveErr != nil {
				return nil, saveErr
			}
			appLog.Logger.Info().
				Str("user_id", userID).
				Str("preference_id", row.ID).
				Msg("backfilled missing system user preference content hash")
		}
		return &row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	seed, err := loadLegacySystemUserPreferenceTemplate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	row = orm.SystemUserPreference{
		ID:            newUUID(),
		UserID:        userID,
		Content:       firstNonEmpty(seed.Content, ""),
		ContentHash:   firstNonEmpty(strings.TrimSpace(seed.ContentHash), HashContent(seed.Content)),
		Version:       maxInt64(1, seed.Version),
		AutoEvo:       true,
		UpdatedBy:     firstNonEmpty(userID, seed.UpdatedBy, "system"),
		UpdatedByName: firstNonEmpty(userName, seed.UpdatedByName, "system"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := tx.Create(&row).Error; err != nil {
		return nil, err
	}
	appLog.Logger.Info().
		Str("user_id", userID).
		Str("preference_id", row.ID).
		Bool("seeded_from_legacy_template", strings.TrimSpace(seed.ID) != "").
		Msg("created system user preference row")
	return &row, nil
}

func BuildChatResourceContext(ctx context.Context, db *gorm.DB, userID, userName string, sessionID string) (*ChatResourceContext, error) {
	mem, err := EnsureSystemMemory(ctx, db, userID, userName)
	if err != nil {
		return nil, err
	}
	pref, err := EnsureSystemUserPreference(ctx, db, userID, userName)
	if err != nil {
		return nil, err
	}
	usePersonalization, err := LoadUserPersonalizationEnabled(ctx, db, userID)
	if err != nil {
		return nil, err
	}

	var skills []orm.SkillResource
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND node_type = ? AND is_enabled = ?", userID, SkillNodeTypeParent, true).
		Order("category ASC, skill_name ASC").
		Find(&skills).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	availableSkills := make([]string, 0, len(skills))
	snapshots := make([]orm.ResourceSessionSnapshot, 0, len(skills)+2)

	snapshots = append(snapshots,
		orm.ResourceSessionSnapshot{
			ID:           newUUID(),
			SessionID:    sessionID,
			UserID:       userID,
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			SnapshotHash: firstNonEmpty(mem.ContentHash, HashContent(mem.Content)),
			CreatedAt:    now,
		},
		orm.ResourceSessionSnapshot{
			ID:           newUUID(),
			SessionID:    sessionID,
			UserID:       userID,
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			SnapshotHash: firstNonEmpty(pref.ContentHash, HashContent(pref.Content)),
			CreatedAt:    now,
		},
	)

	for _, skill := range skills {
		state, err := skillStateFromResource(&skill)
		if err != nil {
			return nil, err
		}
		parentName := firstNonEmpty(strings.TrimSpace(skill.ParentSkillName), strings.TrimSpace(skill.SkillName))
		availableSkills = append(availableSkills, fmt.Sprintf("%s/%s", strings.TrimSpace(skill.Category), parentName))
		snapshots = append(snapshots, orm.ResourceSessionSnapshot{
			ID:              newUUID(),
			SessionID:       sessionID,
			UserID:          userID,
			ResourceType:    ResourceTypeSkill,
			ResourceKey:     SkillSuggestionResourceKey(skill),
			Category:        strings.TrimSpace(skill.Category),
			ParentSkillName: parentName,
			SkillName:       strings.TrimSpace(skill.SkillName),
			FileExt:         firstNonEmpty(strings.TrimSpace(skill.FileExt), "md"),
			RelativePath:    state.RelativePath,
			SnapshotHash:    state.ContentHash,
			CreatedAt:       now,
		})
	}

	if len(availableSkills) > 1 {
		sort.Strings(availableSkills)
	}
	if err := db.WithContext(ctx).Create(&snapshots).Error; err != nil {
		return nil, err
	}

	context := &ChatResourceContext{
		AvailableTools:     []string{"all"},
		AvailableSkills:    availableSkills,
		Memory:             mem.Content,
		UserPreference:     pref.Content,
		UsePersonalization: usePersonalization,
	}
	appLog.Logger.Info().
		Str("session_id", sessionID).
		Str("user_id", userID).
		Strs("available_tools", context.AvailableTools).
		Int("available_skill_count", len(context.AvailableSkills)).
		Bool("use_personalization", context.UsePersonalization).
		Msg("built chat resource context for algorithm request")
	return context, nil
}

func ResolveSessionUser(ctx context.Context, db *gorm.DB, sessionID string) (string, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", "", gorm.ErrRecordNotFound
	}

	var snapshot orm.ResourceSessionSnapshot
	err := db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Take(&snapshot).Error
	if err == nil && strings.TrimSpace(snapshot.UserID) != "" {
		var conv orm.Conversation
		if convErr := db.WithContext(ctx).Where("id = ?", conversationIDFromSessionID(sessionID)).Take(&conv).Error; convErr == nil {
			return snapshot.UserID, strings.TrimSpace(conv.CreateUserName), nil
		}
		return snapshot.UserID, "", nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", err
	}

	var conv orm.Conversation
	if err := db.WithContext(ctx).Where("id = ?", conversationIDFromSessionID(sessionID)).Take(&conv).Error; err != nil {
		return "", "", err
	}
	return strings.TrimSpace(conv.CreateUserID), strings.TrimSpace(conv.CreateUserName), nil
}

func ResolveRequestUser(ctx context.Context, db *gorm.DB, sessionID, fallbackUserID, fallbackUserName string) (string, string, error) {
	return ResolveSessionUser(ctx, db, sessionID)
}

func FindSnapshot(ctx context.Context, db *gorm.DB, sessionID, resourceType, resourceKey string) (*orm.ResourceSessionSnapshot, error) {
	var row orm.ResourceSessionSnapshot
	if err := db.WithContext(ctx).
		Where("session_id = ? AND resource_type = ? AND resource_key = ?", strings.TrimSpace(sessionID), strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)).
		Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func FindSkillSnapshotByIdentity(ctx context.Context, db *gorm.DB, sessionID, userID, category, skillName string) (*orm.ResourceSessionSnapshot, error) {
	var row orm.ResourceSessionSnapshot
	if err := db.WithContext(ctx).
		Where(
			"session_id = ? AND user_id = ? AND resource_type = ? AND category = ? AND (skill_name = ? OR parent_skill_name = ?)",
			strings.TrimSpace(sessionID),
			strings.TrimSpace(userID),
			ResourceTypeSkill,
			strings.TrimSpace(category),
			strings.TrimSpace(skillName),
			strings.TrimSpace(skillName),
		).
		Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func loadLegacySystemMemoryTemplate(ctx context.Context, tx *gorm.DB, userID string) (orm.SystemMemory, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return orm.SystemMemory{}, nil
	}

	var row orm.SystemMemory
	err := tx.WithContext(ctx).Where("user_id = ?", "").Order("created_at ASC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.SystemMemory{}, nil
	}
	if err != nil {
		return orm.SystemMemory{}, err
	}
	return row, nil
}

func loadLegacySystemUserPreferenceTemplate(ctx context.Context, tx *gorm.DB, userID string) (orm.SystemUserPreference, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return orm.SystemUserPreference{}, nil
	}

	var row orm.SystemUserPreference
	err := tx.WithContext(ctx).Where("user_id = ?", "").Order("created_at ASC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.SystemUserPreference{}, nil
	}
	if err != nil {
		return orm.SystemUserPreference{}, err
	}
	return row, nil
}

func LoadSkillStateByResourceKey(ctx context.Context, db *gorm.DB, userID, resourceKey string) (*SkillState, error) {
	var skill orm.SkillResource
	err := db.WithContext(ctx).
		Where("owner_user_id = ? AND id = ?",
			strings.TrimSpace(userID),
			strings.TrimSpace(resourceKey),
		).
		Take(&skill).Error
	if err != nil {
		return nil, err
	}
	return skillStateFromResource(&skill)
}

func LoadParentSkillState(ctx context.Context, db *gorm.DB, userID, category, skillName string) (*SkillState, error) {
	var skill orm.SkillResource
	err := db.WithContext(ctx).
		Where("owner_user_id = ? AND category = ? AND node_type = ? AND (skill_name = ? OR parent_skill_name = ?)",
			strings.TrimSpace(userID),
			strings.TrimSpace(category),
			SkillNodeTypeParent,
			strings.TrimSpace(skillName),
			strings.TrimSpace(skillName),
		).
		Take(&skill).Error
	if err != nil {
		return nil, err
	}
	return skillStateFromResource(&skill)
}

func BuildSuggestionRecord(
	userID string,
	resourceType string,
	resourceKey string,
	action string,
	sessionID string,
	status string,
) orm.ResourceSuggestion {
	now := time.Now()
	return orm.ResourceSuggestion{
		ID:           newUUID(),
		UserID:       strings.TrimSpace(userID),
		ResourceType: strings.TrimSpace(resourceType),
		ResourceKey:  strings.TrimSpace(resourceKey),
		Action:       strings.TrimSpace(action),
		SessionID:    strings.TrimSpace(sessionID),
		Status:       strings.TrimSpace(status),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func skillStateFromResource(skill *orm.SkillResource) (*SkillState, error) {
	if skill == nil {
		return nil, gorm.ErrRecordNotFound
	}
	relativePath := strings.TrimSpace(skill.RelativePath)
	if relativePath == "" {
		relativePath = ParentSkillRelativePath(skill.Category, firstNonEmpty(skill.ParentSkillName, skill.SkillName))
	}
	relativePath = filepath.ToSlash(relativePath)
	content := skill.Content
	contentHash := strings.TrimSpace(skill.ContentHash)
	if contentHash == "" {
		contentHash = HashContent(content)
	}
	return &SkillState{
		Resource:     skill,
		RelativePath: relativePath,
		Content:      content,
		ContentHash:  contentHash,
	}, nil
}

func conversationIDFromSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if idx := strings.LastIndex(sessionID, "_"); idx > 0 && isTimestampSuffix(sessionID[idx+1:]) {
		return sessionID[:idx]
	}
	return sessionID
}

func isTimestampSuffix(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
