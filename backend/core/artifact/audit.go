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

func AuditLegacy(ctx context.Context, db *gorm.DB) (AuditReport, error) {
	var report AuditReport
	if db == nil {
		return report, errors.New("store not initialized")
	}
	var rows []orm.ConversationArtifact
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
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
