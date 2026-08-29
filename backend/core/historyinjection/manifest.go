package historyinjection

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const ManifestSchemaVersion = 1

// Manifest describes one self-contained history injection bundle. Database
// rows live in SQLFile; files are copied from the bundle into one of the
// explicitly supported runtime roots.
type Manifest struct {
	SchemaVersion    int              `json:"schema_version"`
	BundleID         string           `json:"bundle_id"`
	Category         string           `json:"category"`
	Title            string           `json:"title"`
	ConversationID   string           `json:"conversation_id"`
	SessionIDs       []string         `json:"session_ids"`
	SourceOwnerID    string           `json:"source_owner_id"`
	WorkflowRef      string           `json:"workflow_ref"`
	WorkflowRevision WorkflowRevision `json:"workflow_revision"`
	SQLFile          string           `json:"sql_file"`
	Files            []PayloadFile    `json:"files"`
}

type WorkflowRevision struct {
	ID                 string          `json:"id"`
	RevisionNo         int64           `json:"revision_no"`
	TreeHash           string          `json:"tree_hash"`
	Message            string          `json:"message"`
	CreatedBy          string          `json:"created_by"`
	CreatedAt          string          `json:"created_at"`
	CompiledGraph      json.RawMessage `json:"compiled_graph"`
	GraphHash          string          `json:"graph_hash"`
	GraphSchemaVersion string          `json:"graph_schema_version"`
}

type PayloadFile struct {
	Source       string `json:"source"`
	TargetRoot   string `json:"target_root"`
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Mode         uint32 `json:"mode"`
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("history injection manifest schema_version %d is unsupported", m.SchemaVersion)
	}
	for name, value := range map[string]string{
		"bundle_id": m.BundleID, "category": m.Category, "title": m.Title, "conversation_id": m.ConversationID,
		"source_owner_id": m.SourceOwnerID, "workflow_ref": m.WorkflowRef,
		"workflow_revision.id": m.WorkflowRevision.ID, "sql_file": m.SQLFile,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("history injection manifest %s is required", name)
		}
	}
	if !safeRelativePath(m.SQLFile) {
		return fmt.Errorf("history injection sql_file is unsafe: %q", m.SQLFile)
	}
	for _, file := range m.Files {
		if !safeRelativePath(file.Source) || !safeRelativePath(file.RelativePath) {
			return fmt.Errorf("history injection payload path is unsafe: %q -> %q", file.Source, file.RelativePath)
		}
		if file.TargetRoot != "uploads" && file.TargetRoot != "subagent" {
			return fmt.Errorf("history injection payload target_root %q is unsupported", file.TargetRoot)
		}
		if len(file.SHA256) != 64 || file.Size < 0 {
			return fmt.Errorf("history injection payload metadata is invalid for %q", file.Source)
		}
	}
	return nil
}

func safeRelativePath(value string) bool {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" || strings.HasPrefix(value, "/") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}
