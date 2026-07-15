package source

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) UpsertObjects(ctx context.Context, objects []SourceObject) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		for _, object := range objects {
			if err := validateSourceObjectIndexRow(object); err != nil {
				return err
			}
			err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "binding_id"}, {Name: "object_key"}},
				DoUpdates: sourceObjectUpsertAssignments(),
			}).Create(objectToORM(object)).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func sourceObjectUpsertAssignments() clause.Set {
	assignments := clause.AssignmentColumns([]string{
		"tree_key", "parent_key", "display_name", "search_name", "object_type", "is_document",
		"is_container", "has_children", "source_version", "mime_type", "file_extension",
		"modified_at", "deleted_at_source", "depth", "provider_meta_json", "last_seen_run_id", "updated_at",
	})
	assignments = append(assignments, clause.Assignment{
		Column: clause.Column{Name: "size_bytes"},
		Value: gorm.Expr(`CASE
WHEN excluded.size_bytes > 0 THEN excluded.size_bytes
WHEN excluded.provider_meta_json->>'kind' IN ('wiki_node', 'drive_file') AND source_object_index.size_bytes > 0 THEN source_object_index.size_bytes
ELSE excluded.size_bytes
END`),
	})
	return assignments
}

func validateSourceObjectIndexRow(object SourceObject) error {
	switch {
	case object.SourceID == "":
		return NewStoreError(ErrCodeInternal, "source_id is required")
	case object.BindingID == "":
		return NewStoreError(ErrCodeInternal, "binding_id is required")
	case object.TreeKey == "":
		return NewStoreError(ErrCodeInternal, "tree_key is required")
	case object.ObjectKey == "":
		return NewStoreError(ErrCodeInternal, "object_key is required")
	case strings.TrimSpace(object.DisplayName) == "":
		return NewStoreError(ErrCodeInternal, "display_name is required")
	case object.SearchName == "":
		return NewStoreError(ErrCodeInternal, "search_name is required")
	case object.ParentKey == object.ObjectKey:
		return NewStoreError(ErrCodeInternal, "object parent cannot be itself")
	case object.ParentKey == "" && object.Depth != 0:
		return NewStoreError(ErrCodeInternal, fmt.Sprintf("root object depth must be 0: %s", object.ObjectKey))
	case object.ParentKey != "" && object.Depth <= 0:
		return NewStoreError(ErrCodeInternal, fmt.Sprintf("child object depth must be positive: %s", object.ObjectKey))
	}
	return nil
}

func (r *SQLRepository) GetObject(ctx context.Context, sourceID, bindingID, objectKey string) (SourceObject, error) {
	var object ormSourceObject
	err := r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ? AND object_key = ?", sourceID, bindingID, objectKey).
		First(&object).Error
	if err != nil {
		return SourceObject{}, mapORMNotFound(err, ErrCodeNotFound, "object not found")
	}
	return objectFromORM(object), nil
}

func (r *SQLRepository) ListObjects(ctx context.Context, req ObjectListRequest) ([]ObjectWithState, string, bool, error) {
	limit := normalizeSQLPageSize(req.PageSize) + 1
	rows, err := objectWithStateBaseQuery(r.ormDB(ctx)).
		Where("o.source_id = ?", req.SourceID).
		Where("o.binding_id = ?", req.BindingID).
		Where("o.tree_key = ?", req.TreeKey).
		Where("COALESCE(o.parent_key, '') = ?", req.ParentKey).
		Where("? OR NOT o.is_document", req.IncludeDocuments).
		Where("? OR NOT o.is_container", req.IncludeContainers).
		Where("NOT o.is_document OR COALESCE(s.document_list_visible, TRUE)").
		Where("o.object_key > ?", req.Cursor).
		Scopes(applyObjectStateFilter(req.StateFilter)).
		Order("o.display_name, o.object_key").
		Limit(limit).
		Rows()
	if err != nil {
		return nil, "", false, mapSQLConstraint(err)
	}
	defer rows.Close()
	return scanORMObjectWithStatePage(rows, limit)
}

func (r *SQLRepository) SearchObjects(ctx context.Context, req ObjectSearchRequest) ([]ObjectWithState, string, bool, error) {
	limit := normalizeSQLPageSize(req.PageSize) + 1
	rows, err := objectWithStateBaseQuery(r.ormDB(ctx)).
		Where("o.source_id = ?", req.SourceID).
		Where("? = '' OR o.binding_id = ?", req.BindingID, req.BindingID).
		Where("? = '' OR o.tree_key = ?", req.TreeKey, req.TreeKey).
		Where("? OR NOT o.is_document", req.IncludeDocuments).
		Where("? OR NOT o.is_container", req.IncludeContainers).
		Where("NOT o.is_document OR COALESCE(s.document_list_visible, TRUE)").
		Where("LOWER(o.search_name || ' ' || o.display_name) LIKE LOWER(?)", "%"+req.Keyword+"%").
		Where("o.object_key > ?", req.Cursor).
		Scopes(applyObjectStateFilter(req.StateFilter)).
		Order("o.display_name, o.object_key").
		Limit(limit).
		Rows()
	if err != nil {
		return nil, "", false, mapSQLConstraint(err)
	}
	defer rows.Close()
	return scanORMObjectWithStatePage(rows, limit)
}

func objectSelectSQLForAlias(alias string) string {
	return alias + `.source_id, ` + alias + `.binding_id, ` + alias + `.tree_key, ` + alias + `.object_key, ` + alias + `.parent_key, ` + alias + `.display_name, ` + alias + `.search_name, ` + alias + `.object_type, ` + alias + `.is_document, ` + alias + `.is_container, ` + alias + `.has_children, ` + alias + `.source_version, ` + alias + `.size_bytes, ` + alias + `.mime_type, ` + alias + `.file_extension, ` + alias + `.modified_at, ` + alias + `.deleted_at_source, ` + alias + `.depth, ` + alias + `.provider_meta_json, ` + alias + `.last_seen_run_id, ` + alias + `.created_at, ` + alias + `.updated_at`
}

func objectWithStateBaseQuery(db *gorm.DB) *gorm.DB {
	return db.Table("source_object_index AS o").
		Select(objectTreeSelectSQLForAlias("o") + ", " + documentStateSelectSQLForAlias("s")).
		Joins("LEFT JOIN source_document_states s ON s.source_id = o.source_id AND s.binding_id = o.binding_id AND s.object_key = o.object_key")
}

func objectTreeSelectSQLForAlias(alias string) string {
	hasVisibleChildren := `EXISTS (
		SELECT 1
		FROM source_object_index child
		LEFT JOIN source_document_states child_state
		  ON child_state.source_id = child.source_id
		 AND child_state.binding_id = child.binding_id
		 AND child_state.object_key = child.object_key
		WHERE child.source_id = ` + alias + `.source_id
		  AND child.binding_id = ` + alias + `.binding_id
		  AND child.tree_key = ` + alias + `.tree_key
		  AND child.parent_key = ` + alias + `.object_key
		  AND (NOT child.is_document OR COALESCE(child_state.document_list_visible, TRUE))
	)`
	return alias + `.source_id, ` + alias + `.binding_id, ` + alias + `.tree_key, ` + alias + `.object_key, ` + alias + `.parent_key, ` + alias + `.display_name, ` + alias + `.search_name, ` + alias + `.object_type, ` + alias + `.is_document, ` + alias + `.is_container, CASE WHEN ` + alias + `.has_children OR ` + hasVisibleChildren + ` THEN TRUE ELSE FALSE END AS has_children, ` + alias + `.source_version, ` + alias + `.size_bytes, ` + alias + `.mime_type, ` + alias + `.file_extension, ` + alias + `.modified_at, ` + alias + `.deleted_at_source, ` + alias + `.depth, ` + alias + `.provider_meta_json, ` + alias + `.last_seen_run_id, ` + alias + `.created_at, ` + alias + `.updated_at`
}

func applyObjectStateFilter(values []string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if len(values) == 0 {
			return db
		}
		return db.Where("s.source_state IN ?", values)
	}
}

func scanObjectRow(row scanner) (SourceObject, error) {
	var values objectScanValues
	err := row.Scan(values.dest()...)
	if err != nil {
		return SourceObject{}, err
	}
	return values.sourceObject(), nil
}

type objectScanValues struct {
	object                                            SourceObject
	parentKey, sourceVersion, mimeType, fileExtension sql.NullString
	lastSeenRunID                                     sql.NullString
	sizeBytes                                         sql.NullInt64
	modifiedAt, deletedAtSource                       sql.NullTime
	providerMeta                                      []byte
}

func (v *objectScanValues) dest() []any {
	return []any{&v.object.SourceID, &v.object.BindingID, &v.object.TreeKey, &v.object.ObjectKey, &v.parentKey,
		&v.object.DisplayName, &v.object.SearchName, &v.object.ObjectType, &v.object.IsDocument, &v.object.IsContainer,
		&v.object.HasChildren, &v.sourceVersion, &v.sizeBytes, &v.mimeType, &v.fileExtension, &v.modifiedAt, &v.deletedAtSource,
		&v.object.Depth, &v.providerMeta, &v.lastSeenRunID, &v.object.CreatedAt, &v.object.UpdatedAt}
}

func (v *objectScanValues) sourceObject() SourceObject {
	object := v.object
	object.ParentKey = v.parentKey.String
	object.SourceVersion = v.sourceVersion.String
	object.SizeBytes = v.sizeBytes.Int64
	object.MimeType = v.mimeType.String
	object.FileExtension = v.fileExtension.String
	object.ModifiedAt = nullTimePtr(v.modifiedAt)
	object.DeletedAtSource = nullTimePtr(v.deletedAtSource)
	object.ProviderMeta = decodeJSON(v.providerMeta)
	object.LastSeenRunID = v.lastSeenRunID.String
	return object
}

func scanORMObjectWithStatePage(rows *sql.Rows, limit int) ([]ObjectWithState, string, bool, error) {
	items := []ObjectWithState{}
	for rows.Next() {
		item, err := scanObjectWithStateRows(rows)
		if err != nil {
			return nil, "", false, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, err
	}
	hasMore := len(items) == limit
	if hasMore {
		items = items[:len(items)-1]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		nextCursor = items[len(items)-1].Object.ObjectKey
	}
	return items, nextCursor, hasMore, nil
}

func scanObjectWithStateRows(row scanner) (ObjectWithState, error) {
	var object objectScanValues
	var state documentStateScanValues
	dest := append(object.dest(), state.dest()...)
	if err := row.Scan(dest...); err != nil {
		return ObjectWithState{}, err
	}
	item := ObjectWithState{Object: object.sourceObject()}
	if state, ok := state.state(); ok {
		item.State = &state
	}
	return item, nil
}


// ListDocumentsByBinding 查询某个 binding 下的所有 Document 记录。
func (r *SQLRepository) ListDocumentsByBinding(ctx context.Context, sourceID, bindingID string) ([]Document, error) {
	var rows []ormDocument
	err := r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ?", sourceID, bindingID).
		Find(&rows).Error
	if err != nil {
		return nil, mapSQLConstraint(err)
	}
	docs := make([]Document, 0, len(rows))
	for _, row := range rows {
		docs = append(docs, documentFromORM(row))
	}
	return docs, nil
}

// DeleteObjectsByBinding 按 binding 删除所有 SourceObject。
func (r *SQLRepository) DeleteObjectsByBinding(ctx context.Context, sourceID, bindingID string) error {
	return r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ?", sourceID, bindingID).
		Delete(&ormSourceObject{}).Error
}

// DeleteDocumentStatesByBinding 按 binding 删除所有 DocumentState。
func (r *SQLRepository) DeleteDocumentStatesByBinding(ctx context.Context, sourceID, bindingID string) error {
	return r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ?", sourceID, bindingID).
		Delete(&ormDocumentState{}).Error
}

// DeleteParseTasksByBinding 按 binding 删除所有 ParseTask。
func (r *SQLRepository) DeleteParseTasksByBinding(ctx context.Context, sourceID, bindingID string) error {
	return r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ?", sourceID, bindingID).
		Delete(&ormParseTask{}).Error
}
