package source

import (
	"context"
	"database/sql"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) GetDocumentState(ctx context.Context, sourceID, bindingID, objectKey string) (DocumentState, error) {
	var state ormDocumentState
	err := r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ? AND object_key = ?", sourceID, bindingID, objectKey).
		First(&state).Error
	if err != nil {
		return DocumentState{}, mapORMNotFound(err, ErrCodeNotFound, "document state not found")
	}
	return documentStateFromORM(state), nil
}

func (r *SQLRepository) SaveDocumentState(ctx context.Context, state DocumentState) error {
	err := r.ormDB(ctx).Clauses(documentStateUpsertClause()).Create(documentStateToORM(state)).Error
	return mapSQLConstraint(err)
}

type DocumentStateMutation func(DocumentState, bool) (DocumentState, error)

func (r *SQLRepository) MutateDocumentState(ctx context.Context, sourceID, bindingID, objectKey string, mutate DocumentStateMutation) (DocumentState, error) {
	var out DocumentState
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		current, exists, err := lockDocumentStateORMTx(tx, sourceID, bindingID, objectKey)
		if err != nil {
			return err
		}
		if !exists {
			created, err := mutate(current, true)
			if err != nil {
				return err
			}
			inserted, err := insertDocumentStateIfAbsentORMTx(tx, created)
			if err != nil {
				return err
			}
			current, exists, err = lockDocumentStateORMTx(tx, sourceID, bindingID, objectKey)
			if err != nil {
				return err
			}
			if !exists {
				return NewStoreError(ErrCodeInternal, "document state insert was not visible")
			}
			if inserted {
				out = current
				return nil
			}
		}
		next, err := mutate(current, !exists)
		if err != nil {
			return err
		}
		if err := saveDocumentStateORMTx(tx, next); err != nil {
			return err
		}
		out, _, err = lockDocumentStateORMTx(tx, sourceID, bindingID, objectKey)
		return err
	})
	return out, err
}

func documentStateUpsertClause() clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "source_id"}, {Name: "binding_id"}, {Name: "object_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"binding_generation", "source_version", "baseline_version", "deleted_at_source",
			"source_state", "sync_state", "pending_action", "document_list_visible", "selectable",
			"parse_queue_state", "document_id", "active_task_id", "last_detected_at",
			"last_synced_at", "last_error", "updated_at",
		}),
	}
}

func insertDocumentStateIfAbsentORMTx(tx *gorm.DB, state DocumentState) (bool, error) {
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "source_id"}, {Name: "binding_id"}, {Name: "object_key"}},
		DoNothing: true,
	}).Create(documentStateToORM(state))
	return result.RowsAffected > 0, result.Error
}

func lockDocumentStateORMTx(tx *gorm.DB, sourceID, bindingID, objectKey string) (DocumentState, bool, error) {
	var state ormDocumentState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("source_id = ? AND binding_id = ? AND object_key = ?", sourceID, bindingID, objectKey).
		First(&state).Error
	if err == nil {
		return documentStateFromORM(state), true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return DocumentState{}, false, mapSQLConstraint(err)
	}
	return DocumentState{}, false, nil
}

func saveDocumentStateORMTx(tx *gorm.DB, state DocumentState) error {
	return tx.Clauses(documentStateUpsertClause()).Create(documentStateToORM(state)).Error
}

func (r *SQLRepository) ListDocumentStates(ctx context.Context, sourceID, bindingID string) ([]DocumentState, error) {
	var rows []ormDocumentState
	err := r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ?", sourceID, bindingID).
		Order("object_key").
		Find(&rows).Error
	if err != nil {
		return nil, mapSQLConstraint(err)
	}
	states := make([]DocumentState, 0, len(rows))
	for _, row := range rows {
		states = append(states, documentStateFromORM(row))
	}
	return states, nil
}

func (r *SQLRepository) ListPendingStates(ctx context.Context, sourceID, bindingID string, objectKeys []string) ([]DocumentState, error) {
	states, err := r.ListDocumentStates(ctx, sourceID, bindingID)
	if err != nil {
		return nil, err
	}
	if len(objectKeys) == 0 {
		return states, nil
	}
	want := make(map[string]struct{}, len(objectKeys))
	for _, key := range objectKeys {
		want[key] = struct{}{}
	}
	filtered := states[:0]
	for _, state := range states {
		if _, ok := want[state.ObjectKey]; ok {
			filtered = append(filtered, state)
		}
	}
	return filtered, nil
}

func (r *SQLRepository) UpsertDocument(ctx context.Context, document Document) (Document, error) {
	existing, err := r.GetDocument(ctx, document.SourceID, document.BindingID, document.ObjectKey)
	if err == nil {
		document.DocumentID = existing.DocumentID
		document.CoreDocumentID = existing.CoreDocumentID
		document.CurrentVersionID = existing.CurrentVersionID
		document.CreatedAt = existing.CreatedAt
	}
	err = r.ormDB(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_id"}, {Name: "binding_id"}, {Name: "object_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"core_document_id", "current_version_id", "desired_version_id", "source_version",
			"display_name", "mime_type", "file_extension", "parse_status", "updated_at",
		}),
	}).Create(documentToORM(document)).Error
	if err != nil {
		return Document{}, mapSQLConstraint(err)
	}
	return document, nil
}

func (r *SQLRepository) GetDocument(ctx context.Context, sourceID, bindingID, objectKey string) (Document, error) {
	var document ormDocument
	err := r.ormDB(ctx).
		Where("source_id = ? AND binding_id = ? AND object_key = ?", sourceID, bindingID, objectKey).
		First(&document).Error
	if err != nil {
		return Document{}, mapORMNotFound(err, ErrCodeNotFound, "document not found")
	}
	return documentFromORM(document), nil
}

func (r *SQLRepository) UpdateDocument(ctx context.Context, document Document) error {
	err := r.ormDB(ctx).Model(&ormDocument{}).
		Where("document_id = ? AND source_id = ? AND binding_id = ? AND object_key = ?", document.DocumentID, document.SourceID, document.BindingID, document.ObjectKey).
		Updates(map[string]any{
			"tenant_id":          gorm.Expr("NULLIF(?, '')", document.TenantID),
			"core_document_id":   gorm.Expr("NULLIF(?, '')", document.CoreDocumentID),
			"current_version_id": gorm.Expr("NULLIF(?, '')", document.CurrentVersionID),
			"desired_version_id": gorm.Expr("NULLIF(?, '')", document.DesiredVersionID),
			"source_version":     gorm.Expr("NULLIF(?, '')", document.SourceVersion),
			"display_name":       document.DisplayName,
			"mime_type":          gorm.Expr("NULLIF(?, '')", document.MimeType),
			"file_extension":     gorm.Expr("NULLIF(?, '')", document.FileExtension),
			"parse_status":       document.ParseStatus,
			"updated_at":         document.UpdatedAt,
		}).Error
	return mapSQLConstraint(err)
}

func (r *SQLRepository) ListDocuments(ctx context.Context, req SourceDocumentListRequest) ([]DocumentWithState, int, error) {
	page, pageSize := normalizeSQLPage(req.Page, req.PageSize)
	var total int64
	if err := documentListBaseQuery(r.ormDB(ctx), req).Count(&total).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	rows, err := documentListBaseQuery(r.ormDB(ctx), req).
		Select(objectSelectSQLForAlias("o") + ", " + documentStateSelectSQLForAlias("s") + ", " + documentSelectSQLForAlias("d")).
		Order("o.display_name, o.object_key").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Rows()
	if err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	defer rows.Close()
	items := []DocumentWithState{}
	for rows.Next() {
		item, err := scanDocumentWithStateRows(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, int(total), rows.Err()
}

func documentListBaseQuery(db *gorm.DB, req SourceDocumentListRequest) *gorm.DB {
	query := db.Table("source_document_states AS s").
		Joins("JOIN source_object_index o ON o.source_id = s.source_id AND o.binding_id = s.binding_id AND o.object_key = s.object_key").
		Joins("LEFT JOIN documents d ON d.source_id = s.source_id AND d.binding_id = s.binding_id AND d.object_key = s.object_key").
		Where("s.source_id = ?", req.SourceID).
		Where("? = '' OR s.binding_id = ?", req.BindingID, req.BindingID).
		Where("s.document_list_visible = true").
		Where("LOWER(o.search_name || ' ' || o.display_name) LIKE LOWER(?)", "%"+req.Keyword+"%")
	if len(req.StateFilter) > 0 {
		query = query.Where("s.source_state IN ?", req.StateFilter)
	}
	if len(req.ParseStatuses) > 0 {
		query = query.Where("d.parse_status IN ?", req.ParseStatuses)
	}
	return query
}

func (r *SQLRepository) GetSourceSummary(ctx context.Context, req SourceSummaryRequest) (SourceSummary, error) {
	if _, err := r.GetSource(ctx, req.SourceID); err != nil {
		return SourceSummary{}, err
	}
	if req.BindingID != "" {
		if _, err := r.GetBinding(ctx, req.SourceID, req.BindingID); err != nil {
			return SourceSummary{}, err
		}
		return r.bindingSummary(ctx, req.SourceID, req.BindingID), nil
	}
	bindings, err := r.ListBindings(ctx, req.SourceID)
	if err != nil {
		return SourceSummary{}, err
	}
	summary := SourceSummary{SourceID: req.SourceID, LastError: JSON{}}
	for _, binding := range bindings {
		if binding.Status == "DELETING" {
			continue
		}
		item := r.bindingSummary(ctx, req.SourceID, binding.BindingID)
		summary.Add(item)
		summary.Bindings = append(summary.Bindings, item)
	}
	return summary, nil
}

func (r *SQLRepository) bindingSummary(ctx context.Context, sourceID, bindingID string) SourceSummary {
	summary := SourceSummary{SourceID: sourceID, BindingID: bindingID, LastError: JSON{}}
	db := r.ormDB(ctx).Model(&ormSourceObject{}).Where("source_id = ? AND binding_id = ?", sourceID, bindingID)
	_ = db.Count(&summary.TotalObjects).Error
	_ = db.Where("is_document").Count(&summary.DocumentObjects).Error
	_ = db.Where("is_container").Count(&summary.ContainerObjects).Error
	var storage struct {
		StorageBytes int64
	}
	_ = documentListBaseQuery(r.ormDB(ctx), SourceDocumentListRequest{SourceID: sourceID, BindingID: bindingID}).
		Select("COALESCE(SUM(o.size_bytes), 0) AS storage_bytes").
		Scan(&storage).Error
	summary.StorageBytes = storage.StorageBytes
	_ = r.ormDB(ctx).Model(&ormDocument{}).
		Joins("JOIN source_document_states s ON s.source_id = documents.source_id AND s.binding_id = documents.binding_id AND s.object_key = documents.object_key").
		Where("documents.source_id = ? AND documents.binding_id = ?", sourceID, bindingID).
		Where("documents.parse_status = ?", ParseTaskStatusSucceeded).
		Where("s.document_list_visible = true").
		Where("s.source_state <> ?", "DELETED").
		Count(&summary.ParsedDocumentCount).Error
	r.fillSummaryStateCounts(ctx, &summary)
	r.fillSummaryTaskCounts(ctx, &summary)
	checkpoint, err := r.GetSyncCheckpoint(ctx, bindingID)
	if err == nil {
		summary.LastSuccessAt = checkpoint.LastSuccessAt
		summary.LastError = checkpoint.LastError
	}
	return summary
}

func (r *SQLRepository) fillSummaryStateCounts(ctx context.Context, summary *SourceSummary) {
	type stateCount struct {
		SourceState string
		Count       int64
	}
	var rows []stateCount
	err := r.ormDB(ctx).Model(&ormDocumentState{}).
		Select("source_state, COUNT(*) AS count").
		Where("source_id = ? AND binding_id = ?", summary.SourceID, summary.BindingID).
		Group("source_state").
		Find(&rows).Error
	if err != nil {
		return
	}
	for _, row := range rows {
		AddSourceStateCount(summary, row.SourceState, row.Count)
	}
}

func (r *SQLRepository) fillSummaryTaskCounts(ctx context.Context, summary *SourceSummary) {
	type taskCount struct {
		Status string
		Count  int64
	}
	var rows []taskCount
	err := r.ormDB(ctx).Model(&ormParseTask{}).
		Select("status, COUNT(*) AS count").
		Where("source_id = ? AND binding_id = ?", summary.SourceID, summary.BindingID).
		Group("status").
		Find(&rows).Error
	if err != nil {
		return
	}
	for _, row := range rows {
		AddTaskStatusCount(summary, row.Status, row.Count)
	}
}

func documentStateSelectSQLForAlias(alias string) string {
	return alias + `.source_id, ` + alias + `.binding_id, ` + alias + `.binding_generation, ` + alias + `.object_key, ` + alias + `.source_version, ` + alias + `.baseline_version, ` + alias + `.deleted_at_source, ` + alias + `.source_state, ` + alias + `.sync_state, ` + alias + `.pending_action, ` + alias + `.document_list_visible, ` + alias + `.selectable, ` + alias + `.parse_queue_state, ` + alias + `.document_id, ` + alias + `.active_task_id, ` + alias + `.last_detected_at, ` + alias + `.last_synced_at, ` + alias + `.last_error, ` + alias + `.created_at, ` + alias + `.updated_at`
}

func documentSelectSQLForAlias(alias string) string {
	return alias + `.document_id, ` + alias + `.tenant_id, ` + alias + `.source_id, ` + alias + `.binding_id, ` + alias + `.object_key, ` + alias + `.core_document_id, ` + alias + `.current_version_id, ` + alias + `.desired_version_id, ` + alias + `.source_version, ` + alias + `.display_name, ` + alias + `.mime_type, ` + alias + `.file_extension, ` + alias + `.parse_status, ` + alias + `.created_at, ` + alias + `.updated_at`
}

func scanDocumentStateRow(row scanner) (DocumentState, error) {
	return scanDocumentState(row)
}

func scanDocumentStateRows(row scanner) (DocumentState, error) {
	return scanDocumentState(row)
}

func scanDocumentState(row scanner) (DocumentState, error) {
	var values documentStateScanValues
	err := row.Scan(values.dest()...)
	if err != nil {
		return DocumentState{}, err
	}
	state, ok := values.state()
	if !ok {
		return DocumentState{}, sql.ErrNoRows
	}
	return state, nil
}

type documentStateScanValues struct {
	sourceID, bindingID, objectKey, sourceVersion, baselineVersion sql.NullString
	sourceState, syncState, pendingAction, parseQueueState         sql.NullString
	documentID, activeTaskID                                       sql.NullString
	bindingGeneration                                              sql.NullInt64
	documentListVisible, selectable                                sql.NullBool
	deletedAtSource, lastDetectedAt, lastSyncedAt                  sql.NullTime
	createdAt, updatedAt                                           sql.NullTime
	lastError                                                      []byte
}

func (v *documentStateScanValues) dest() []any {
	return []any{&v.sourceID, &v.bindingID, &v.bindingGeneration, &v.objectKey, &v.sourceVersion,
		&v.baselineVersion, &v.deletedAtSource, &v.sourceState, &v.syncState, &v.pendingAction,
		&v.documentListVisible, &v.selectable, &v.parseQueueState, &v.documentID, &v.activeTaskID,
		&v.lastDetectedAt, &v.lastSyncedAt, &v.lastError, &v.createdAt, &v.updatedAt}
}

func (v *documentStateScanValues) state() (DocumentState, bool) {
	if !v.sourceID.Valid {
		return DocumentState{}, false
	}
	state := DocumentState{
		SourceID:            v.sourceID.String,
		BindingID:           v.bindingID.String,
		BindingGeneration:   v.bindingGeneration.Int64,
		ObjectKey:           v.objectKey.String,
		SourceVersion:       v.sourceVersion.String,
		BaselineVersion:     v.baselineVersion.String,
		DeletedAtSource:     nullTimePtr(v.deletedAtSource),
		SourceState:         v.sourceState.String,
		SyncState:           v.syncState.String,
		PendingAction:       v.pendingAction.String,
		DocumentListVisible: v.documentListVisible.Bool,
		Selectable:          v.selectable.Bool,
		ParseQueueState:     v.parseQueueState.String,
		DocumentID:          v.documentID.String,
		ActiveTaskID:        v.activeTaskID.String,
		LastDetectedAt:      nullTimePtr(v.lastDetectedAt),
		LastSyncedAt:        nullTimePtr(v.lastSyncedAt),
		LastError:           decodeJSON(v.lastError),
	}
	if v.createdAt.Valid {
		state.CreatedAt = v.createdAt.Time
	}
	if v.updatedAt.Valid {
		state.UpdatedAt = v.updatedAt.Time
	}
	return state, true
}

func scanDocumentRow(row scanner) (Document, error) {
	var values documentScanValues
	err := row.Scan(values.dest()...)
	if err != nil {
		return Document{}, err
	}
	document, ok := values.document()
	if !ok {
		return Document{}, sql.ErrNoRows
	}
	return document, nil
}

type documentScanValues struct {
	documentID, tenantID, sourceID, bindingID, objectKey             sql.NullString
	coreDocumentID, currentVersionID, desiredVersionID               sql.NullString
	sourceVersion, displayName, mimeType, fileExtension, parseStatus sql.NullString
	createdAt, updatedAt                                             sql.NullTime
}

func (v *documentScanValues) dest() []any {
	return []any{&v.documentID, &v.tenantID, &v.sourceID, &v.bindingID, &v.objectKey,
		&v.coreDocumentID, &v.currentVersionID, &v.desiredVersionID, &v.sourceVersion, &v.displayName,
		&v.mimeType, &v.fileExtension, &v.parseStatus, &v.createdAt, &v.updatedAt}
}

func (v *documentScanValues) document() (Document, bool) {
	if !v.documentID.Valid {
		return Document{}, false
	}
	document := Document{
		DocumentID:       v.documentID.String,
		TenantID:         v.tenantID.String,
		SourceID:         v.sourceID.String,
		BindingID:        v.bindingID.String,
		ObjectKey:        v.objectKey.String,
		CoreDocumentID:   v.coreDocumentID.String,
		CurrentVersionID: v.currentVersionID.String,
		DesiredVersionID: v.desiredVersionID.String,
		SourceVersion:    v.sourceVersion.String,
		DisplayName:      v.displayName.String,
		MimeType:         v.mimeType.String,
		FileExtension:    v.fileExtension.String,
		ParseStatus:      v.parseStatus.String,
	}
	if v.createdAt.Valid {
		document.CreatedAt = v.createdAt.Time
	}
	if v.updatedAt.Valid {
		document.UpdatedAt = v.updatedAt.Time
	}
	return document, true
}

func scanDocumentWithStateRows(row scanner) (DocumentWithState, error) {
	var object objectScanValues
	var state documentStateScanValues
	var document documentScanValues
	dest := append(object.dest(), state.dest()...)
	dest = append(dest, document.dest()...)
	if err := row.Scan(dest...); err != nil {
		return DocumentWithState{}, err
	}
	stateValue, ok := state.state()
	if !ok {
		return DocumentWithState{}, sql.ErrNoRows
	}
	item := DocumentWithState{Object: object.sourceObject(), State: stateValue}
	if documentValue, ok := document.document(); ok {
		item.Document = &documentValue
	}
	return item, nil
}
