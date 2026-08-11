package domain

import (
	"context"

	"gorm.io/gorm"
)

const legacySessionColumns = "id, conversation_id, plugin_id, plugin_ref, plugin_revision_id, status, state_version, created_at, updated_at"

// ReadSession reads both pre-expand and expanded rows into the same Workflow
// domain model. Defaults are supplied by the reader, so a backfill is not a
// prerequisite for deploying the new binary.
func ReadSession(ctx context.Context, db *gorm.DB, id string) (*Session, error) {
	columns := legacySessionColumns + ", 'lazymind' AS origin_host, '' AS origin_ref, 'lazymind' AS controller_host"
	if DetectSchemaCapabilities(db).HostNeutralSessionRefs {
		columns = legacySessionColumns + ", COALESCE(origin_host, 'lazymind') AS origin_host, COALESCE(origin_ref, '') AS origin_ref, COALESCE(controller_host, 'lazymind') AS controller_host"
	}
	var session Session
	err := db.WithContext(ctx).Table(session.TableName()).Select(columns).Where("id = ?", id).Take(&session).Error
	return &session, err
}

// WriteSession writes only columns understood by the deployed schema. This is
// a single authoritative write, not a dual-write compatibility mechanism.
func WriteSession(ctx context.Context, db *gorm.DB, session *Session) error {
	values := map[string]any{
		"id": session.ID, "conversation_id": session.ConversationID,
		"plugin_id": session.WorkflowID, "plugin_ref": session.WorkflowRef,
		"plugin_revision_id": session.WorkflowRevision, "status": session.Status,
		"state_version": session.StateVersion, "created_at": session.CreatedAt,
		"updated_at": session.UpdatedAt,
	}
	if DetectSchemaCapabilities(db).HostNeutralSessionRefs {
		values["origin_host"] = session.OriginHost
		values["origin_ref"] = session.OriginRef
		values["controller_host"] = session.ControllerHost
	}
	return db.WithContext(ctx).Table(session.TableName()).Create(values).Error
}
