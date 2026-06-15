package resourceupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
)

const (
	reviewStatusPending  = "pending"
	reviewStatusAccepted = "accepted"
	reviewStatusRejected = "rejected"
	reviewStatusExpired  = "expired"

	skillReviewTypePatch = "patch"
	skillReviewTypeNew   = "new"

	memoryReviewStateSuccess = "success"
)

type SkillReviewResult struct {
	ID           string    `gorm:"column:id" json:"id"`
	SkillName    string    `gorm:"column:skill_name" json:"skill_name"`
	Type         string    `gorm:"column:type" json:"type"`
	ReviewStatus string    `gorm:"column:review_status" json:"review_status"`
	UserID       string    `gorm:"column:userid" json:"userid"`
	RequestID    string    `gorm:"column:requestid" json:"requestid"`
	SkillContent string    `gorm:"column:skill_content" json:"skill_content"`
	Summary      string    `gorm:"column:summary" json:"summary"`
	Time         time.Time `gorm:"column:time" json:"time"`
}

func (SkillReviewResult) TableName() string { return "skill_review_results" }

type MemoryReviewResult struct {
	ID            string          `gorm:"column:id" json:"id"`
	UserID        string          `gorm:"column:user_id" json:"user_id"`
	Target        string          `gorm:"column:target" json:"target"`
	SessionID     string          `gorm:"column:session_id" json:"session_id"`
	SourceContent string          `gorm:"column:source_content" json:"source_content"`
	Content       string          `gorm:"column:content" json:"content"`
	Operations    json.RawMessage `gorm:"column:operations" json:"operations,omitempty"`
	State         string          `gorm:"column:state" json:"state"`
	ReviewStatus  string          `gorm:"column:review_status" json:"review_status"`
	Time          time.Time       `gorm:"column:time" json:"time"`
}

func (MemoryReviewResult) TableName() string { return "memory_review" }

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
}

var (
	errReviewNotFound = errors.New("review result not found")
	errReviewConflict = errors.New("review result conflict")
	errReviewInvalid  = errors.New("review result invalid")
)

func mapReviewError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, errReviewNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, fallback+" not found", http.StatusNotFound)
	case errors.Is(err, errReviewConflict), errors.Is(err, gorm.ErrDuplicatedKey):
		message := strings.TrimSpace(err.Error())
		if message == "" || message == errReviewConflict.Error() {
			message = fallback + " conflict"
		}
		common.ReplyErr(w, message, http.StatusConflict)
	case errors.Is(err, errReviewInvalid):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	default:
		common.ReplyErr(w, fallback+" failed", http.StatusInternalServerError)
	}
}

func parsePositiveQueryInt(value string, def, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		n = def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

func normalizeReviewTarget(target string) string {
	switch strings.TrimSpace(target) {
	case orm.ResourceUpdateResourceTypeMemory:
		return orm.ResourceUpdateResourceTypeMemory
	case orm.ResourceUpdateResourceTypeUserPreference:
		return orm.ResourceUpdateResourceTypeUserPreference
	default:
		return strings.TrimSpace(target)
	}
}

func isAutoApplyActiveStatus(status string) bool {
	return status == orm.ResourceUpdateTaskStatusPending || status == orm.ResourceUpdateTaskStatusRunning
}

func taskReviewResultID(task orm.ResourceUpdateTask) string {
	if id := strings.TrimSpace(task.ReviewResultID); id != "" {
		return id
	}
	return strings.TrimSpace(task.TriggerID)
}

func parseSkillFrontmatter(content string) (skillFrontmatter, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return skillFrontmatter{}, fmt.Errorf("%w: skill content must start with YAML frontmatter", errReviewInvalid)
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return skillFrontmatter{}, fmt.Errorf("%w: skill content must contain closing frontmatter separator", errReviewInvalid)
	}
	yamlPart := rest[:idx]
	body := strings.TrimSpace(rest[idx+5:])
	if body == "" {
		return skillFrontmatter{}, fmt.Errorf("%w: skill content must include markdown body", errReviewInvalid)
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal([]byte(yamlPart), &meta); err != nil {
		return skillFrontmatter{}, fmt.Errorf("%w: invalid skill frontmatter: %v", errReviewInvalid, err)
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Description = strings.TrimSpace(meta.Description)
	meta.Category = strings.TrimSpace(meta.Category)
	if meta.Name == "" {
		return skillFrontmatter{}, fmt.Errorf("%w: frontmatter name required", errReviewInvalid)
	}
	if meta.Description == "" {
		return skillFrontmatter{}, fmt.Errorf("%w: frontmatter description required", errReviewInvalid)
	}
	return meta, nil
}

func validateSkillReviewContent(skillName, content string) (skillFrontmatter, error) {
	skillName = strings.TrimSpace(skillName)
	content = strings.TrimSpace(content)
	if skillName == "" || content == "" {
		return skillFrontmatter{}, fmt.Errorf("%w: skill_name and skill_content required", errReviewInvalid)
	}
	meta, err := parseSkillFrontmatter(content)
	if err != nil {
		return skillFrontmatter{}, err
	}
	if meta.Name != skillName {
		return skillFrontmatter{}, fmt.Errorf("%w: skill_name and frontmatter name must match", errReviewInvalid)
	}
	return meta, nil
}

func validatePathSegment(segment string) error {
	segment = strings.TrimSpace(segment)
	switch {
	case segment == "":
		return fmt.Errorf("%w: path segment required", errReviewInvalid)
	case segment == "." || segment == "..":
		return fmt.Errorf("%w: invalid path segment", errReviewInvalid)
	case strings.Contains(segment, "/") || strings.Contains(segment, "\\"):
		return fmt.Errorf("%w: path segment cannot contain slash", errReviewInvalid)
	default:
		return nil
	}
}

func skillContentSize(content string) int64 {
	return int64(len([]byte(content)))
}

func mimeTypeForExt(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return "text/plain; charset=utf-8"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if mt := mime.TypeByExtension(strings.ToLower(ext)); mt != "" {
		if strings.HasPrefix(mt, "text/") && !strings.Contains(strings.ToLower(mt), "charset=") {
			return mt + "; charset=utf-8"
		}
		return mt
	}
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".py", ".sh", ".js", ".ts", ".json", ".yaml", ".yml", ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func newSkillResourceID() string {
	return common.GenerateID()
}

func clearLegacyDraftSuggestionRefs(ext json.RawMessage) json.RawMessage {
	if len(ext) == 0 {
		return ext
	}
	var payload map[string]any
	if err := json.Unmarshal(ext, &payload); err != nil {
		return ext
	}
	delete(payload, "draft_suggestion_ids")
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ext
	}
	return body
}

func skillResultSelect(db *gorm.DB) *gorm.DB {
	return db.Table("skill_review_results").
		Select("id, skill_name, type, review_status, userid, requestid, skill_content, summary, time")
}

func memoryResultSelect(db *gorm.DB) *gorm.DB {
	return db.Table("memory_review").
		Select("id, user_id, target, session_id, source_content, content, operations, state, review_status, time")
}

func mapSkillPatchResultToResource(db *gorm.DB, result SkillReviewResult) (orm.SkillResource, error) {
	var row orm.SkillResource
	err := db.
		Where("owner_user_id = ? AND skill_name = ? AND node_type = ? AND created_at <= ?",
			strings.TrimSpace(result.UserID), strings.TrimSpace(result.SkillName), evolution.SkillNodeTypeParent, result.Time).
		Order("created_at DESC").
		Take(&row).Error
	return row, err
}

func mapMemoryReviewResultToMemory(db *gorm.DB, result MemoryReviewResult) (orm.SystemMemory, error) {
	var row orm.SystemMemory
	err := db.Where("user_id = ?", strings.TrimSpace(result.UserID)).Take(&row).Error
	return row, err
}

func mapMemoryReviewResultToPreference(db *gorm.DB, result MemoryReviewResult) (orm.SystemUserPreference, error) {
	var row orm.SystemUserPreference
	err := db.Where("user_id = ?", strings.TrimSpace(result.UserID)).Take(&row).Error
	return row, err
}

func activeAutoApplyStatuses() []string {
	return []string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning}
}
