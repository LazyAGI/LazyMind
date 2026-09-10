package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type workflowScriptAuditEntry struct {
	Classification string `json:"classification"`
	SHA256         string `json:"sha256"`
	Origin         string `json:"origin,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

func scriptAuditReportJSON(existingJSON string, scripts map[string]string) (string, error) {
	report := map[string]workflowScriptAuditEntry{}
	_ = json.Unmarshal([]byte(existingJSON), &report)
	for path, source := range scripts {
		report[path] = auditGeneratedWorkflowScript(path, source)
	}
	if len(report) == 0 {
		return "{}", nil
	}
	body, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func updateWorkflowGenerationScriptAudit(db *gorm.DB, draftID, analysisID, existingJSON string, scripts map[string]string) error {
	if db == nil || strings.TrimSpace(draftID) == "" || strings.TrimSpace(analysisID) == "" || len(scripts) == 0 {
		return nil
	}
	if strings.TrimSpace(existingJSON) == "" {
		var analysis orm.WorkflowGenerationAnalysis
		if err := db.Select("script_report_json").Where("id = ? AND draft_id = ?", analysisID, draftID).First(&analysis).Error; err != nil {
			return err
		}
		existingJSON = analysis.ScriptReportJSON
	}
	reportJSON, err := scriptAuditReportJSON(existingJSON, scripts)
	if err != nil {
		return err
	}
	return db.Model(&orm.WorkflowGenerationAnalysis{}).
		Where("id = ? AND draft_id = ?", analysisID, draftID).
		Updates(map[string]any{"script_report_json": reportJSON, "updated_at": time.Now().UTC()}).Error
}

func auditGeneratedWorkflowScript(path, source string) workflowScriptAuditEntry {
	hash := sha256.Sum256([]byte(source))
	entry := workflowScriptAuditEntry{
		Classification: "importable_tool",
		SHA256:         hex.EncodeToString(hash[:]),
		Origin:         "workflow_generation",
	}
	normalized := normalizedWorkflowScriptPath(path)
	switch {
	case normalized == "":
		entry.Classification = "unsupported"
		entry.Reason = "invalid script path"
	case !strings.HasSuffix(strings.ToLower(normalized), ".py"):
		entry.Classification = "unsupported"
		entry.Reason = "only Python workflow scripts can be auto-approved"
	}
	return entry
}

func normalizedWorkflowScriptPath(path string) string {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))), "./")
	if path == "" || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return ""
	}
	if !strings.HasPrefix(path, "scripts/") {
		path = "scripts/" + path
	}
	if strings.TrimPrefix(path, "scripts/") == "" {
		return ""
	}
	return path
}
