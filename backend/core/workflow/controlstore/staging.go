package controlstore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"os"
)

// StageValue keeps filesystem I/O outside the material transaction. The caller
// discards this private copy only when the transaction does not publish it.
func StageValue(ctx context.Context, db *gorm.DB, sessionID, contentType string, raw json.RawMessage) (json.RawMessage, func(), error) {
	noop := func() {}
	if !db.Migrator().HasColumn(&orm.WorkflowSession{}, "control_protocol") {
		return raw, noop, nil
	}
	var session orm.WorkflowSession
	err := db.WithContext(ctx).Where("id = ?", sessionID).First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return raw, noop, nil
	}
	if err != nil {
		return nil, noop, err
	}
	if !Controlled(session) {
		return raw, noop, nil
	}
	value, directory, err := artifactfile.Snapshot(sessionID, uuid.NewString(), contentType, raw)
	cleanup := func() {
		if directory != "" {
			_ = os.RemoveAll(directory)
		}
	}
	return value, cleanup, err
}
