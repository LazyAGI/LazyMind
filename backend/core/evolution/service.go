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
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	appLog "lazymind/core/log"
)

type SkillState struct {
	V2Resource   *orm.SkillV2Skill
	RelativePath string
	Content      string
	ContentHash  string
}

type PersonalResourceContent struct {
	ResourceID             string
	Content                string
	ContentHash            string
	Version                int64
	AutoEvo                bool
	AutoEvoApplyStatus     string
	AutoEvoGeneration      int64
	AutoEvoError           string
	LatestVersionChange    *VersionChangeSummary
	HasPendingReviewResult bool
	ReviewStatus           string
	UpdatedBy              string
	UpdatedByName          string
	UpdatedAt              time.Time
}

const (
	personalMemoryPath         = "memory/memory.md"
	personalUserPreferencePath = "memory/user.md"
)

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

func EnsurePersonalResourceContent(ctx context.Context, db *gorm.DB, userID, resourceType string) (*PersonalResourceContent, error) {
	userID = strings.TrimSpace(userID)
	resourceType = strings.TrimSpace(resourceType)
	initialContent := ""
	path := personalMemoryPath
	if resourceType == ResourceTypeUserPreference {
		initialContent = FormatSystemUserPreferenceForChat(orm.SystemUserPreference{})
		path = personalUserPreferencePath
	}

	tx := db.WithContext(ctx)
	if row, err := loadPersonalResourceContent(ctx, tx, userID, resourceType); err == nil {
		return row, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var out *PersonalResourceContent
	err := tx.Transaction(func(tx *gorm.DB) error {
		if row, err := loadPersonalResourceContent(ctx, tx, userID, resourceType); err == nil {
			out = row
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now()
		resourceID := newUUID()
		revisionID := newUUID()
		hash := HashContent(initialContent)
		blob := orm.PersonalResourceBlob{
			Hash:           hash,
			Size:           int64(len([]byte(initialContent))),
			Mime:           "text/markdown; charset=utf-8",
			FileType:       "markdown",
			Binary:         false,
			StorageBackend: "postgres",
			Content:        []byte(initialContent),
			CreatedAt:      now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
			return err
		}
		head := revisionID
		resource := orm.PersonalResource{
			ID:                 resourceID,
			UserID:             userID,
			ResourceType:       resourceType,
			HeadRevisionID:     &head,
			Version:            1,
			AutoEvo:            true,
			AutoEvoApplyStatus: AutoEvoApplyStatusIdle,
			UpdatedBy:          userID,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := tx.Create(&resource).Error; err != nil {
			return err
		}
		revision := orm.PersonalResourceRevision{
			ID:           revisionID,
			ResourceID:   resourceID,
			RevisionNo:   1,
			Path:         path,
			BlobHash:     hash,
			ContentHash:  hash,
			Size:         blob.Size,
			Mime:         blob.Mime,
			FileType:     blob.FileType,
			Binary:       false,
			Message:      "initial import",
			ChangeSource: "initial_import",
			CreatedAt:    now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		draft := orm.PersonalResourceDraft{
			ResourceID:     resourceID,
			BaseRevisionID: &head,
			Path:           path,
			BlobHash:       hash,
			ContentHash:    hash,
			Size:           blob.Size,
			Mime:           blob.Mime,
			FileType:       blob.FileType,
			Binary:         false,
			DraftStatus:    "",
			Version:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		out = &PersonalResourceContent{
			ResourceID:         resourceID,
			Content:            initialContent,
			ContentHash:        hash,
			Version:            1,
			AutoEvo:            true,
			AutoEvoApplyStatus: AutoEvoApplyStatusIdle,
			ReviewStatus:       ReviewStatusNone,
			UpdatedBy:          userID,
			UpdatedAt:          now,
		}
		return nil
	})
	return out, err
}

func loadPersonalResourceContent(ctx context.Context, db *gorm.DB, userID, resourceType string) (*PersonalResourceContent, error) {
	var resource orm.PersonalResource
	if err := db.WithContext(ctx).
		Where("user_id = ? AND resource_type = ?", strings.TrimSpace(userID), strings.TrimSpace(resourceType)).
		Take(&resource).Error; err != nil {
		return nil, err
	}
	if resource.HeadRevisionID == nil || strings.TrimSpace(*resource.HeadRevisionID) == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var revision orm.PersonalResourceRevision
	if err := db.WithContext(ctx).
		Where("id = ? AND resource_id = ?", *resource.HeadRevisionID, resource.ID).
		Take(&revision).Error; err != nil {
		return nil, err
	}
	var blob orm.PersonalResourceBlob
	if err := db.WithContext(ctx).Where("hash = ?", revision.BlobHash).Take(&blob).Error; err != nil {
		return nil, err
	}
	if blob.Binary {
		return nil, fmt.Errorf("personal resource %s head is binary", resourceType)
	}
	reviewStatus, hasPending, err := loadPersonalResourceReviewStatus(ctx, db, resource.ID)
	if err != nil {
		return nil, err
	}
	return &PersonalResourceContent{
		ResourceID:             resource.ID,
		Content:                string(blob.Content),
		ContentHash:            firstNonEmpty(revision.ContentHash, revision.BlobHash),
		Version:                revision.RevisionNo,
		AutoEvo:                resource.AutoEvo,
		AutoEvoApplyStatus:     NormalizeAutoEvoApplyStatus(resource.AutoEvoApplyStatus),
		AutoEvoGeneration:      resource.AutoEvoGeneration,
		AutoEvoError:           resource.AutoEvoError,
		LatestVersionChange:    versionChangeFromRevision(revision),
		HasPendingReviewResult: hasPending,
		ReviewStatus:           reviewStatus,
		UpdatedBy:              resource.UpdatedBy,
		UpdatedByName:          resource.UpdatedByName,
		UpdatedAt:              resource.UpdatedAt,
	}, nil
}

func loadPersonalResourceReviewStatus(ctx context.Context, db *gorm.DB, resourceID string) (string, bool, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&orm.PersonalResourceReviewSession{}).
		Where("resource_id = ? AND status = ?", strings.TrimSpace(resourceID), "active").
		Count(&count).Error; err != nil {
		return ReviewStatusNone, false, err
	}
	if count > 0 {
		return ReviewStatusPending, true, nil
	}
	return ReviewStatusNone, false, nil
}

func versionChangeFromRevision(revision orm.PersonalResourceRevision) *VersionChangeSummary {
	changeSource := strings.TrimSpace(revision.ChangeSource)
	if changeSource == "" {
		return nil
	}
	return &VersionChangeSummary{
		ChangeSource:  changeSource,
		SourceRefType: strings.TrimSpace(revision.SourceRefType),
		SourceRefID:   strings.TrimSpace(revision.SourceRefID),
		ChangedAt:     revision.CreatedAt.Format(time.RFC3339Nano),
	}
}

func BuildChatResourceContext(ctx context.Context, db *gorm.DB, userID, userName string, sessionID string) (*ChatResourceContext, error) {
	mem, err := EnsurePersonalResourceContent(ctx, db, userID, ResourceTypeMemory)
	if err != nil {
		return nil, err
	}
	pref, err := EnsurePersonalResourceContent(ctx, db, userID, ResourceTypeUserPreference)
	if err != nil {
		return nil, err
	}
	usePersonalization, err := LoadUserPersonalizationEnabled(ctx, db, userID)
	if err != nil {
		return nil, err
	}

	var v2Skills []orm.SkillV2Skill
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND is_enabled = ? AND deleted_at IS NULL", userID, true).
		Order("category ASC, skill_name ASC").
		Find(&v2Skills).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	availableSkills := make([]string, 0, len(v2Skills))
	snapshots := make([]orm.ResourceSessionSnapshot, 0, len(v2Skills)+2)
	seenSkillNames := map[string]struct{}{}

	snapshots = append(snapshots,
		orm.ResourceSessionSnapshot{
			ID:           newUUID(),
			SessionID:    sessionID,
			UserID:       userID,
			ResourceType: ResourceTypeMemory,
			ResourceKey:  SystemResourceKey(ResourceTypeMemory),
			SnapshotHash: mem.ContentHash,
			CreatedAt:    now,
		},
		orm.ResourceSessionSnapshot{
			ID:           newUUID(),
			SessionID:    sessionID,
			UserID:       userID,
			ResourceType: ResourceTypeUserPreference,
			ResourceKey:  SystemResourceKey(ResourceTypeUserPreference),
			SnapshotHash: pref.ContentHash,
			CreatedAt:    now,
		},
	)

	for _, skill := range v2Skills {
		state, err := skillStateFromV2Resource(ctx, db, &skill)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				appLog.Logger.Warn().
					Str("user_id", userID).
					Str("skill_id", strings.TrimSpace(skill.ID)).
					Str("category", strings.TrimSpace(skill.Category)).
					Str("skill_name", strings.TrimSpace(skill.SkillName)).
					Str("head_revision_id", valueOrEmpty(skill.HeadRevisionID)).
					Err(err).
					Msg("skipping enabled skill with invalid published SKILL.md")
				continue
			}
			return nil, err
		}
		parentName := strings.TrimSpace(skill.SkillName)
		category := strings.TrimSpace(skill.Category)
		availableName := fmt.Sprintf("%s/%s", category, parentName)
		seenSkillNames[availableName] = struct{}{}
		availableSkills = append(availableSkills, availableName)
		snapshots = append(snapshots, orm.ResourceSessionSnapshot{
			ID:              newUUID(),
			SessionID:       sessionID,
			UserID:          userID,
			ResourceType:    ResourceTypeSkill,
			ResourceKey:     strings.TrimSpace(skill.ID),
			Category:        category,
			ParentSkillName: parentName,
			SkillName:       parentName,
			FileExt:         "md",
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
		DisabledTools:      []string{},
		AvailableSkills:    availableSkills,
		Memory:             mem.Content,
		UserPreference:     pref.Content,
		UsePersonalization: usePersonalization,
	}
	appLog.Logger.Info().
		Str("session_id", sessionID).
		Str("user_id", userID).
		Strs("disabled_tools", context.DisabledTools).
		Int("available_skill_count", len(context.AvailableSkills)).
		Bool("use_personalization", context.UsePersonalization).
		Msg("built chat resource context for algorithm request")
	return context, nil
}

// AddMentionedSkills makes explicitly mentioned skills available for this chat
// session without changing the user's persistent is_enabled preference.
func AddMentionedSkills(ctx context.Context, db *gorm.DB, userID, sessionID string, skillIDs []string, resourceContext *ChatResourceContext) error {
	if resourceContext == nil || len(skillIDs) == 0 {
		return nil
	}
	existing := map[string]bool{}
	for _, name := range resourceContext.AvailableSkills {
		existing[name] = true
	}
	for _, skillID := range skillIDs {
		var skill orm.SkillV2Skill
		if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", skillID, userID).Take(&skill).Error; err != nil {
			return fmt.Errorf("mentioned skill is not accessible: %s", skillID)
		}
		state, err := skillStateFromV2Resource(ctx, db, &skill)
		if err != nil {
			return fmt.Errorf("mentioned skill is unpublished: %s", skillID)
		}
		name := fmt.Sprintf("%s/%s", strings.TrimSpace(skill.Category), strings.TrimSpace(skill.SkillName))
		if existing[name] {
			continue
		}
		existing[name] = true
		resourceContext.AvailableSkills = append(resourceContext.AvailableSkills, name)
		snapshot := orm.ResourceSessionSnapshot{ID: newUUID(), SessionID: sessionID, UserID: userID, ResourceType: ResourceTypeSkill, ResourceKey: skill.ID, Category: skill.Category, ParentSkillName: skill.SkillName, SkillName: skill.SkillName, FileExt: "md", RelativePath: state.RelativePath, SnapshotHash: state.ContentHash, CreatedAt: time.Now()}
		if err := db.WithContext(ctx).Create(&snapshot).Error; err != nil {
			return err
		}
	}
	sort.Strings(resourceContext.AvailableSkills)
	return nil
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

func LoadSkillStateByResourceKey(ctx context.Context, db *gorm.DB, userID, resourceKey string) (*SkillState, error) {
	var v2Skill orm.SkillV2Skill
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND id = ?",
			strings.TrimSpace(userID),
			strings.TrimSpace(resourceKey),
		).
		Take(&v2Skill).Error; err != nil {
		return nil, err
	}
	return skillStateFromV2Resource(ctx, db, &v2Skill)
}

func LoadParentSkillState(ctx context.Context, db *gorm.DB, userID, category, skillName string) (*SkillState, error) {
	var v2Skill orm.SkillV2Skill
	if err := db.WithContext(ctx).
		Where("owner_user_id = ? AND category = ? AND skill_name = ?",
			strings.TrimSpace(userID),
			strings.TrimSpace(category),
			strings.TrimSpace(skillName),
		).
		Take(&v2Skill).Error; err != nil {
		return nil, err
	}
	return skillStateFromV2Resource(ctx, db, &v2Skill)
}

func skillStateFromV2Resource(ctx context.Context, db *gorm.DB, skill *orm.SkillV2Skill) (*SkillState, error) {
	if skill == nil || skill.HeadRevisionID == nil {
		return nil, gorm.ErrRecordNotFound
	}
	skillMDPath := strings.TrimSpace(skill.SkillMDPath)
	if skillMDPath == "" {
		skillMDPath = "SKILL.md"
	}
	var entry orm.SkillV2RevisionEntry
	if err := db.WithContext(ctx).
		Where("revision_id = ? AND path = ? AND entry_type = ?", *skill.HeadRevisionID, skillMDPath, "file").
		Take(&entry).Error; err != nil {
		return nil, err
	}
	if entry.BlobHash == nil {
		return nil, gorm.ErrRecordNotFound
	}
	var blob orm.SkillV2Blob
	if err := db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		return nil, err
	}
	content := ""
	if !blob.Binary {
		content = string(blob.Content)
	}
	relativeRoot := strings.TrimSpace(skill.RelativeRoot)
	if relativeRoot == "" {
		relativeRoot = filepath.ToSlash(filepath.Join(skill.Category, skill.SkillName))
	}
	relativePath := filepath.ToSlash(filepath.Join(relativeRoot, skillMDPath))
	contentHash := strings.TrimSpace(blob.Hash)
	if contentHash == "" {
		contentHash = HashContent(content)
	}
	return &SkillState{
		V2Resource:   skill,
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

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
