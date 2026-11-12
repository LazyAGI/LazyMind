package historyinjection

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type injectionMarker struct {
	BundleID    string `json:"bundle_id"`
	InjectedAt  string `json:"injected_at"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func readInjectionMarker(ext map[string]any) injectionMarker {
	metadata, _ := ext[historyInjectionMetadataKey].(map[string]any)
	bundleID, _ := metadata["bundle_id"].(string)
	injectedAt, _ := metadata["injected_at"].(string)
	if _, err := time.Parse(time.RFC3339Nano, injectedAt); err != nil || strings.TrimSpace(bundleID) == "" {
		return injectionMarker{}
	}
	fingerprint, _ := metadata["fingerprint"].(string)
	return injectionMarker{BundleID: bundleID, InjectedAt: injectedAt, Fingerprint: fingerprint}
}

type installedConversation struct {
	ID        string `gorm:"column:id"`
	OwnerID   string `gorm:"column:create_user_id"`
	OwnerName string `gorm:"column:create_user_name"`
	Ext       any    `gorm:"-"`
}

func findInstalledConversation(db *gorm.DB, id string) (installedConversation, error) {
	var result installedConversation
	row := db.Table("conversations").Select("id, create_user_id, create_user_name, ext").
		Where("id = ?", id).Clauses(clause.Locking{Strength: "UPDATE"}).Row()
	err := row.Scan(&result.ID, &result.OwnerID, &result.OwnerName, &result.Ext)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return result, err
}

func (c installedConversation) marker() injectionMarker {
	ext, _ := decodeConversationExt(c.Ext)
	return readInjectionMarker(ext)
}

type predecessorSample struct {
	conversationID string
	bundleID       string
}

// The November release changed conversation IDs. Match both old identifiers,
// so a custom bundle or an ordinary conversation cannot be retired by title.
var releasePredecessors = map[string]predecessorSample{
	"academic-personal-knowledge-mock-v2": {"e97d6394-5b7c-4bab-8757-3f04c9aefdde", "academic-research-personal-knowledge-ai-v1"},
	"bid-technical-proposal-mock-v2":      {"a3b1d442-7a49-4225-a806-13fc162f7745", "bid-tech-proposal-smoke-test-v1"},
	"image-workplace-stickers-mock-v2":    {"bcea6551-d6a2-4dec-83bb-1ccf9fa11f51", "image-workplace-stickers-8-v1"},
	"ppt-group-meeting-v1":                {"e3096625-c245-45a0-bc32-ce203f3c94f3", "ppt-league-of-legends-v1"},
	"product-meeting-assistant-mock-v2":   {"3ecbcfdc-2e2a-4c38-ae75-060ba6bd02e4", "product-solution-delivery-enterprise-meeting-assistant-v1"},
}

func findPredecessor(db *gorm.DB, manifest Manifest) (installedConversation, error) {
	previous, ok := releasePredecessors[manifest.BundleID]
	if !ok || previous.conversationID == manifest.ConversationID {
		return installedConversation{}, nil
	}
	row, err := findInstalledConversation(db, previous.conversationID)
	if err != nil || row.marker().BundleID != previous.bundleID {
		return installedConversation{}, err
	}
	return row, nil
}

func retirePredecessor(db *gorm.DB, manifest Manifest, ownerID string) error {
	previous, err := findPredecessor(db, manifest)
	if err != nil || previous.OwnerID != ownerID {
		return err
	}
	now := time.Now().UTC()
	result := db.Table("conversations").Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", previous.ID, ownerID).
		Update("deleted_at", now)
	if result.Error != nil {
		return result.Error
	}
	return db.Table("task_center_tasks").Where("conversation_id = ? AND user_id = ? AND archived_at IS NULL", previous.ID, ownerID).
		Updates(map[string]any{"archived_at": now, "archived_reason": "history_sample_replaced"}).Error
}

func clearInstalledSampleRows(db *gorm.DB, conversationID, ownerID string) error {
	var sessions, tasks []string
	if err := db.Table("plugin_sessions").Where("conversation_id = ? AND create_user_id = ?", conversationID, ownerID).
		Pluck("id", &sessions).Error; err != nil {
		return err
	}
	if err := db.Table("sub_agent_tasks").Where("conversation_id = ? AND create_user_id = ?", conversationID, ownerID).
		Pluck("id", &tasks).Error; err != nil {
		return err
	}
	for _, table := range []string{"workflow_review_checkpoints", "workflow_host_actions"} {
		if err := db.Exec("DELETE FROM "+table+" WHERE session_id IN ?", sessions).Error; err != nil {
			return err
		}
	}
	if err := db.Exec("DELETE FROM workflow_input_bindings WHERE workflow_session_id IN ?", sessions).Error; err != nil {
		return err
	}
	tables := exportTables(conversationID, sessions, tasks)
	for i := len(tables) - 1; i > 0; i-- {
		table := tables[i]
		if table.Name == "task_center_tasks" {
			table.Where += " AND user_id = ?"
			table.Args = append(table.Args, ownerID)
		}
		if err := db.Exec("DELETE FROM "+table.Name+" WHERE "+table.Where, table.Args...).Error; err != nil {
			return fmt.Errorf("clear sample rows from %s: %w", table.Name, err)
		}
	}
	return nil
}

func replaceConversationInsert(insert portableInsert, conversationID string) (string, error) {
	idIndex := columnIndex(insert.columns, "id")
	if idIndex < 0 {
		return "", fmt.Errorf("replacement conversation has no id")
	}
	id, ok := decodePortableSQLText(insert.values[idIndex])
	if !ok || id != conversationID {
		return "", fmt.Errorf("replacement conversation does not match %s", conversationID)
	}
	// Keep the parent row so foreign keys such as group membership are preserved.
	var assignments []string
	for _, column := range insert.columns {
		column = strings.Trim(strings.TrimSpace(column), `"`)
		switch column {
		case "display_name", "ext", "chat_times", "enable_plugin", "plugin_mode", "enable_subagent", "is_task_conv": // workflow-naming: persistence
			assignments = append(assignments, column+" = excluded."+column)
		}
	}
	if len(assignments) == 0 {
		return "", fmt.Errorf("replacement conversation has no sample fields")
	}
	return fmt.Sprintf("INSERT INTO conversations (%s) VALUES (%s) ON CONFLICT (id) DO UPDATE SET %s",
		strings.Join(insert.columns, ", "), strings.Join(insert.values, ", "), strings.Join(assignments, ", ")), nil
}
