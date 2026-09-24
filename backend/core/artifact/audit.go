package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type AuditReport struct {
	ConversationArtifactCount int `json:"conversation_artifact_count"`
	FileArtifactCount         int `json:"file_artifact_count"`
	MissingFileCount          int `json:"missing_file_count"`
	SizeMismatchCount         int `json:"size_mismatch_count"`
	DuplicateFilenameGroups   int `json:"duplicate_filename_groups"`
	DuplicateFilenameRows     int `json:"duplicate_filename_rows"`
}

// SubAgentShadowReport contains counters only, so operational audits cannot
// expose task payloads, workspace paths, or signed URLs.
type SubAgentShadowReport struct {
	LegacyVisibleRows int `json:"legacy_visible_rows"`
	EligibleRows      int `json:"eligible_rows"`
	MappedRows        int `json:"mapped_rows"`
	MissingMappings   int `json:"missing_mappings"`
	HashMismatches    int `json:"hash_mismatches"`
	UnsafeRows        int `json:"unsafe_rows"`
	WorkflowExcluded  int `json:"workflow_excluded"`
}

// AuditSubAgentShadow compares ordinary legacy rows with their V2 mapping.
// It is deliberately read-only; data repair remains an explicit operator action.
func AuditSubAgentShadow(ctx context.Context, db *gorm.DB, ownerUserID string) (SubAgentShadowReport, error) {
	var report SubAgentShadowReport
	if db == nil {
		return report, errors.New("store not initialized")
	}
	type row struct {
		ID        string `gorm:"column:id"`
		AgentType string `gorm:"column:agent_type"`
		Mapped    int    `gorm:"column:mapped"`
	}
	var rows []row
	query := db.WithContext(ctx).Table("sub_agent_artifacts AS artifact").
		Select(`artifact.id, task.agent_type,
			CASE WHEN EXISTS (SELECT 1 FROM artifact_bindings binding
				WHERE binding.scope_type = ? AND binding.scope_id = artifact.id) THEN 1 ELSE 0 END AS mapped`, ScopeSubAgentLegacyRow).
		Joins("JOIN sub_agent_tasks AS task ON task.id = artifact.task_id").
		Where("artifact.hidden = ?", false)
	if ownerUserID != "" {
		query = query.Where("task.create_user_id = ?", ownerUserID)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return report, err
	}
	for _, item := range rows {
		if item.AgentType == "workflow_step" {
			report.WorkflowExcluded++
			continue
		}
		report.LegacyVisibleRows++
		report.EligibleRows++
		if item.Mapped == 1 {
			report.MappedRows++
		} else {
			report.MissingMappings++
		}
	}
	return report, nil
}

func AuditLegacy(ctx context.Context, db *gorm.DB, ownerUserID string) (AuditReport, error) {
	var report AuditReport
	if db == nil {
		return report, errors.New("store not initialized")
	}
	query := db.WithContext(ctx)
	if ownerUserID != "" {
		query = query.Where("create_user_id = ?", ownerUserID)
	}
	var rows []orm.ConversationArtifact
	if err := query.Find(&rows).Error; err != nil {
		return report, err
	}
	report.ConversationArtifactCount = len(rows)
	type key struct{ conv, name string }
	dup := map[key]int{}
	for _, row := range rows {
		dup[key{row.ConversationID, row.Filename}]++
		if row.ContentType != "file" {
			continue
		}
		report.FileArtifactCount++
		var value map[string]any
		if json.Unmarshal(row.Value, &value) != nil {
			report.MissingFileCount++
			continue
		}
		path, _ := value["path"].(string)
		if strings.TrimSpace(path) == "" {
			report.MissingFileCount++
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			report.MissingFileCount++
			continue
		}
		size, _ := value["size"].(float64)
		if int64(size) != info.Size() {
			report.SizeMismatchCount++
		}
	}
	for _, count := range dup {
		if count > 1 {
			report.DuplicateFilenameGroups++
			report.DuplicateFilenameRows += count
		}
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), `"path"`) || strings.Contains(strings.ToLower(string(encoded)), "http") {
		return AuditReport{}, errors.New("audit report must not include paths or urls")
	}
	log.Info().
		Int("conversation_artifact_count", report.ConversationArtifactCount).
		Int("file_artifact_count", report.FileArtifactCount).
		Int("missing_file_count", report.MissingFileCount).
		Int("size_mismatch_count", report.SizeMismatchCount).
		Int("duplicate_filename_groups", report.DuplicateFilenameGroups).
		Msg("[ArtifactAudit] legacy conversation artifact snapshot")
	return report, nil
}
