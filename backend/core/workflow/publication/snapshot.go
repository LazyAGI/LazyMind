package publication

import (
	"encoding/json"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
)

// SnapshotEdit isolates edits of transactionally published files from mutable editor uploads. Unconfigured
// packages retain their existing storage and copy-on-write behavior.
func SnapshotEdit(db *gorm.DB, sessionID, contentType string, value json.RawMessage) (json.RawMessage, string, error) {
	var session orm.WorkflowSession
	if err := db.Where("id = ?", sessionID).First(&session).Error; err != nil {
		return nil, "", err
	}
	enabled, err := PublicationEnabled(db, session)
	if err != nil {
		return nil, "", err
	}
	if !enabled {
		return value, "", nil
	}
	return artifactfile.Snapshot(sessionID, uuid.NewString(), contentType, value)
}
