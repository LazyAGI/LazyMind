package resourceupdate

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
)

const (
	reviewStatusPending  = "pending"
	reviewStatusAccepted = "accepted"
	reviewStatusRejected = "rejected"
	reviewStatusExpired  = "expired"

	skillReviewTypePatch = "patch"
	skillReviewTypeNew   = "new"
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

func skillResultSelect(db *gorm.DB) *gorm.DB {
	return db.Table("skill_review_results").
		Select("id, skill_name, type, review_status, userid, requestid, skill_content, COALESCE(summary, '') AS summary, time")
}
