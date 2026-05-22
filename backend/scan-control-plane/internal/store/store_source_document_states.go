package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/model"
	"github.com/lazymind/scan_control_plane/internal/sourcelayout"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sourceStateUnchanged = "UNCHANGED"
	sourceStateNew       = "NEW"
	sourceStateModified  = "MODIFIED"
	sourceStateDeleted   = "DELETED"

	syncStateIdle      = "IDLE"
	syncStatePending   = "PENDING"
	syncStateScheduled = "SCHEDULED"
	syncStateRunning   = "RUNNING"
	syncStateFailed    = "FAILED"

	pendingActionNone   = "NONE"
	pendingActionCreate = "CREATE"
	pendingActionUpdate = "UPDATE"
	pendingActionDelete = "DELETE"
)

type observedSourceObject struct {
	SourceID         string
	TenantID         string
	ObjectKey        string
	Path             string
	Name             string
	IsDir            bool
	SourceExists     bool
	OriginType       string
	OriginPlatform   string
	OriginRef        string
	SourceVersion    string
	SourceChecksum   string
	SourceSizeBytes  int64
	SourceModifiedAt *time.Time
	DetectedAt       time.Time
	DocumentID       int64
	CoreDocumentID   string
	BaselineVersion  string
}

type sourceDocumentStateView struct {
	ObjectKey            string
	Path                 string
	Name                 string
	SourceState          string
	SyncState            string
	PendingAction        string
	SourceVersion        string
	BaselineVersion      string
	SourceSizeBytes      int64
	SourceModifiedAt     *time.Time
	NextSyncAt           *time.Time
	KnowledgeBasePresent bool
	LastSyncedAt         *time.Time
	LastDetectedAt       time.Time
	LastError            string
	DocumentID           int64
	SourceExists         bool
}

func sourceObjectKey(path, originRef string) string {
	originRef = strings.TrimSpace(originRef)
	if originRef != "" {
		return originRef
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." {
		return ""
	}
	return path
}

func sourceVersionFromObservation(checksum string, size int64, modifiedAt *time.Time) string {
	checksum = strings.TrimSpace(checksum)
	if checksum != "" {
		return "c_" + checksum
	}
	if modifiedAt != nil && !modifiedAt.IsZero() {
		return "m_" + modifiedAt.UTC().Format(time.RFC3339Nano)
	}
	if size > 0 {
		return "s_" + strings.TrimSpace(fmt.Sprintf("%d", size))
	}
	return ""
}

func (s *Store) upsertSourceDocumentStates(ctx context.Context, src sourceEntity, objects []observedSourceObject) error {
	if len(objects) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, obj := range objects {
		obj.SourceID = strings.TrimSpace(firstNonEmpty(obj.SourceID, src.ID))
		obj.TenantID = strings.TrimSpace(firstNonEmpty(obj.TenantID, src.TenantID))
		obj.Path = filepath.Clean(strings.TrimSpace(obj.Path))
		if obj.Path == "." {
			obj.Path = ""
		}
		obj.ObjectKey = strings.TrimSpace(firstNonEmpty(obj.ObjectKey, sourceObjectKey(obj.Path, obj.OriginRef)))
		if obj.SourceID == "" || obj.TenantID == "" || obj.ObjectKey == "" || obj.Path == "" || obj.IsDir || isTransientSourceFilePath(obj.Path, false) {
			continue
		}
		if obj.DetectedAt.IsZero() {
			obj.DetectedAt = now
		}
		if obj.Name == "" {
			obj.Name = filepath.Base(obj.Path)
		}
		if strings.TrimSpace(obj.OriginType) == "" {
			obj.OriginType = firstNonEmpty(src.DefaultOriginType, string(model.OriginTypeLocalFS))
		}
		if strings.TrimSpace(obj.OriginPlatform) == "" {
			obj.OriginPlatform = firstNonEmpty(src.DefaultOriginPlatform, "LOCAL")
		}
		if strings.TrimSpace(obj.SourceVersion) == "" && obj.SourceExists {
			obj.SourceVersion = sourceVersionFromObservation(obj.SourceChecksum, obj.SourceSizeBytes, obj.SourceModifiedAt)
		}
		if err := s.upsertSourceDocumentState(ctx, src, obj, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertSourceDocumentState(ctx context.Context, src sourceEntity, obj observedSourceObject, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing sourceDocumentStateEntity
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_id = ? AND object_key = ?", obj.SourceID, obj.ObjectKey).
			Take(&existing).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		hasExisting := err == nil
		doc, hasDoc, err := resolveStateDocumentTx(tx, obj.SourceID, obj.Path, obj.OriginRef)
		if err != nil {
			return err
		}
		baseline := strings.TrimSpace(obj.BaselineVersion)
		if baseline == "" && hasExisting {
			baseline = strings.TrimSpace(existing.BaselineVersion)
		}
		if obj.SourceExists && strings.TrimSpace(obj.SourceVersion) == "" {
			if !hasExisting || strings.TrimSpace(existing.SourceVersion) == "" {
				return nil
			}
			obj.SourceVersion = strings.TrimSpace(existing.SourceVersion)
			if strings.TrimSpace(obj.SourceChecksum) == "" {
				obj.SourceChecksum = strings.TrimSpace(existing.SourceChecksum)
			}
			if obj.SourceSizeBytes == 0 {
				obj.SourceSizeBytes = existing.SourceSizeBytes
			}
			if obj.SourceModifiedAt == nil {
				obj.SourceModifiedAt = existing.SourceModifiedAt
			}
		}
		if baseline == "" && hasDoc {
			baseline = strings.TrimSpace(doc.CurrentVersionID)
		}
		if !hasExisting && obj.SourceExists && hasDoc &&
			strings.TrimSpace(obj.SourceVersion) != "" &&
			strings.TrimSpace(obj.BaselineVersion) == "" {
			latestTask, hasLatestTask, err := latestParseTaskForDocumentTx(tx, doc.ID)
			if err != nil {
				return err
			}
			if documentSettledForSnapshotNew(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask) {
				baseline = strings.TrimSpace(obj.SourceVersion)
			}
		}
		if baseline == "" && !hasDoc && obj.SourceExists && strings.TrimSpace(obj.SourceVersion) != "" {
			matchesCommitted, err := s.sourceObjectMatchesCommittedSnapshotTx(tx, src.ID, obj)
			if err != nil {
				return err
			}
			if matchesCommitted {
				baseline = strings.TrimSpace(obj.SourceVersion)
			}
		}
		documentID := obj.DocumentID
		coreDocumentID := strings.TrimSpace(obj.CoreDocumentID)
		knowledgeBaseSeen := false
		lastSyncedAt := obj.SourceModifiedAt
		if hasExisting {
			knowledgeBaseSeen = existing.KnowledgeBaseSeen
			lastSyncedAt = existing.LastSyncedAt
		}
		if hasDoc {
			documentID = doc.ID
			if coreDocumentID == "" {
				coreDocumentID = strings.TrimSpace(doc.CoreDocumentID)
			}
			if strings.TrimSpace(doc.CurrentVersionID) != "" || strings.TrimSpace(doc.CoreDocumentID) != "" {
				knowledgeBaseSeen = true
			}
			if lastSyncedAt == nil && strings.TrimSpace(doc.CurrentVersionID) != "" {
				t := doc.UpdatedAt.UTC()
				lastSyncedAt = &t
			}
		}
		sourceState, syncState, pendingAction, nextSyncAt := computeSourceDocumentStateTx(tx, src, obj, baseline, knowledgeBaseSeen)
		if sourceState == sourceStateUnchanged && pendingAction == pendingActionNone && !knowledgeBaseSeen && !hasDoc {
			if hasExisting {
				return tx.Delete(&sourceDocumentStateEntity{}, "id = ?", existing.ID).Error
			}
			return nil
		}
		row := sourceDocumentStateEntity{
			TenantID:          obj.TenantID,
			SourceID:          obj.SourceID,
			ObjectKey:         obj.ObjectKey,
			Path:              obj.Path,
			Name:              obj.Name,
			IsDir:             obj.IsDir,
			SourceExists:      obj.SourceExists,
			OriginType:        strings.TrimSpace(obj.OriginType),
			OriginPlatform:    strings.TrimSpace(obj.OriginPlatform),
			OriginRef:         strings.TrimSpace(obj.OriginRef),
			SourceVersion:     strings.TrimSpace(obj.SourceVersion),
			BaselineVersion:   baseline,
			SourceChecksum:    strings.TrimSpace(obj.SourceChecksum),
			SourceSizeBytes:   obj.SourceSizeBytes,
			SourceModifiedAt:  obj.SourceModifiedAt,
			SourceState:       sourceState,
			SyncState:         syncState,
			PendingAction:     pendingAction,
			NextSyncAt:        nextSyncAt,
			DocumentID:        documentID,
			CoreDocumentID:    coreDocumentID,
			LastDetectedAt:    obj.DetectedAt.UTC(),
			LastSyncedAt:      lastSyncedAt,
			DeletedAtSource:   nil,
			KnowledgeBaseSeen: knowledgeBaseSeen,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if sourceState == sourceStateDeleted {
			t := obj.DetectedAt.UTC()
			row.DeletedAtSource = &t
		}
		if hasExisting {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			if strings.TrimSpace(existing.LastError) != "" && sourceState == existing.SourceState && pendingAction == existing.PendingAction {
				row.LastError = existing.LastError
			}
			updates := map[string]any{
				"tenant_id":           row.TenantID,
				"path":                row.Path,
				"name":                row.Name,
				"is_dir":              row.IsDir,
				"source_exists":       row.SourceExists,
				"origin_type":         row.OriginType,
				"origin_platform":     row.OriginPlatform,
				"origin_ref":          row.OriginRef,
				"source_version":      row.SourceVersion,
				"baseline_version":    row.BaselineVersion,
				"source_checksum":     row.SourceChecksum,
				"source_size_bytes":   row.SourceSizeBytes,
				"source_modified_at":  row.SourceModifiedAt,
				"source_state":        row.SourceState,
				"sync_state":          row.SyncState,
				"pending_action":      row.PendingAction,
				"document_id":         row.DocumentID,
				"core_document_id":    row.CoreDocumentID,
				"last_detected_at":    row.LastDetectedAt,
				"last_error":          row.LastError,
				"knowledge_base_seen": row.KnowledgeBaseSeen,
				"updated_at":          row.UpdatedAt,
			}
			setNullableTimeColumn(updates, "next_sync_at", row.NextSyncAt)
			setNullableTimeColumn(updates, "last_synced_at", row.LastSyncedAt)
			setNullableTimeColumn(updates, "deleted_at_source", row.DeletedAtSource)
			if err := tx.Model(&sourceDocumentStateEntity{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
				return err
			}
			return cleanupDuplicatePathKeySourceDocumentStateTx(tx, row)
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return cleanupDuplicatePathKeySourceDocumentStateTx(tx, row)
	})
}

func cleanupDuplicatePathKeySourceDocumentStateTx(tx *gorm.DB, row sourceDocumentStateEntity) error {
	if strings.TrimSpace(row.OriginRef) == "" {
		return nil
	}
	path := filepath.Clean(strings.TrimSpace(row.Path))
	if path == "" || path == "." {
		return nil
	}
	objectKey := strings.TrimSpace(row.ObjectKey)
	if objectKey == "" || objectKey == path {
		return nil
	}
	query := tx.Where("source_id = ? AND path = ? AND object_key = ?", row.SourceID, path, path).
		Where("(origin_ref = '' OR origin_ref IS NULL)")
	if row.ID > 0 {
		query = query.Where("id <> ?", row.ID)
	}
	return query.Delete(&sourceDocumentStateEntity{}).Error
}

func resolveStateDocumentTx(tx *gorm.DB, sourceID, path, originRef string) (documentEntity, bool, error) {
	var doc documentEntity
	sourceID = strings.TrimSpace(sourceID)
	path = filepath.Clean(strings.TrimSpace(path))
	originRef = strings.TrimSpace(originRef)
	if originRef != "" {
		err := tx.Where("source_id = ? AND origin_ref = ?", sourceID, originRef).Order("id DESC").Take(&doc).Error
		if err == nil {
			return doc, true, nil
		}
		if err != gorm.ErrRecordNotFound {
			return documentEntity{}, false, err
		}
	}
	if path == "" || path == "." {
		return documentEntity{}, false, nil
	}
	err := tx.Where("source_id = ? AND source_object_id = ?", sourceID, path).Order("id DESC").Take(&doc).Error
	if err == nil {
		return doc, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return documentEntity{}, false, nil
	}
	return documentEntity{}, false, err
}

func latestParseTaskForDocumentTx(tx *gorm.DB, documentID int64) (parseTaskDocJoin, bool, error) {
	if documentID <= 0 {
		return parseTaskDocJoin{}, false, nil
	}
	var row parseTaskDocJoin
	err := tx.Table("parse_tasks pt").
		Select("pt.id AS task_id, pt.document_id, pt.task_action, pt.target_version_id, pt.core_document_id, pt.status, pt.core_dataset_id, pt.core_task_id, pt.scan_orchestration_status, pt.submit_at, pt.finished_at, pt.updated_at").
		Where("pt.document_id = ?", documentID).
		Order("pt.id DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return parseTaskDocJoin{}, false, err
	}
	if row.TaskID <= 0 {
		return parseTaskDocJoin{}, false, nil
	}
	return row, true, nil
}

func (s *Store) sourceObjectMatchesCommittedSnapshotTx(tx *gorm.DB, sourceID string, obj observedSourceObject) (bool, error) {
	sourceID = strings.TrimSpace(sourceID)
	path := filepath.Clean(strings.TrimSpace(obj.Path))
	if sourceID == "" || path == "" || path == "." {
		return false, nil
	}
	var relation sourceSnapshotRelationEntity
	if err := tx.Take(&relation, "source_id = ?", sourceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	items, _, err := s.snapshotItemsForDiffBaseDB(tx, sourceID, relation.LastCommittedSnapshotID)
	if err != nil {
		return false, err
	}
	item, ok := items[path]
	if !ok {
		treePath, treeErr := cloudObjectTreePathForObservedObjectTx(tx, sourceID, obj)
		if treeErr != nil {
			return false, treeErr
		}
		if treePath != "" && treePath != path {
			item, ok = items[treePath]
		}
	}
	if !ok || item.IsDir {
		return false, nil
	}
	if strings.TrimSpace(item.Checksum) != "" || strings.TrimSpace(obj.SourceChecksum) != "" {
		return strings.TrimSpace(item.Checksum) != "" &&
			strings.TrimSpace(item.Checksum) == strings.TrimSpace(obj.SourceChecksum), nil
	}
	if item.SizeBytes != obj.SourceSizeBytes {
		return false, nil
	}
	if item.ModTime == nil && obj.SourceModifiedAt == nil {
		return true, nil
	}
	if item.ModTime == nil || obj.SourceModifiedAt == nil {
		return false, nil
	}
	return item.ModTime.UTC().Equal(obj.SourceModifiedAt.UTC()), nil
}

func cloudObjectTreePathForObservedObjectTx(tx *gorm.DB, sourceID string, obj observedSourceObject) (string, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return "", nil
	}
	var src sourceEntity
	if err := tx.Select("root_path", "default_origin_type").Take(&src, "id = ?", sourceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	if !sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		return "", nil
	}
	rootPath := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	if rootPath == "" || rootPath == "." {
		return "", nil
	}
	var row cloudObjectIndexEntity
	query := tx.Where("source_id = ?", sourceID)
	originRef := strings.TrimSpace(obj.OriginRef)
	path := filepath.Clean(strings.TrimSpace(obj.Path))
	if originRef != "" {
		query = query.Where("external_object_id = ?", originRef)
	} else {
		query = query.Where("local_abs_path = ?", path)
	}
	if err := query.Order("id DESC").Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	objectPath := resolveCloudObjectLocalPath(rootPath, row)
	if objectPath == "" {
		return "", nil
	}
	return filepath.Clean(cloudObjectTreePath(objectPath, row)), nil
}

func computeSourceDocumentStateTx(tx *gorm.DB, src sourceEntity, obj observedSourceObject, baseline string, knowledgeBaseSeen bool) (string, string, string, *time.Time) {
	if !obj.SourceExists {
		if knowledgeBaseSeen || baseline != "" || obj.DocumentID > 0 || strings.TrimSpace(obj.CoreDocumentID) != "" {
			syncState, nextSyncAt := scheduledOrPendingSyncStateTx(tx, src, obj.DetectedAt)
			return sourceStateDeleted, syncState, pendingActionDelete, nextSyncAt
		}
		return sourceStateUnchanged, syncStateIdle, pendingActionNone, nil
	}
	sourceVersion := strings.TrimSpace(obj.SourceVersion)
	if baseline == "" {
		syncState, nextSyncAt := scheduledOrPendingSyncStateTx(tx, src, obj.DetectedAt)
		return sourceStateNew, syncState, pendingActionCreate, nextSyncAt
	}
	if sourceVersion != "" && sourceVersion != baseline {
		syncState, nextSyncAt := scheduledOrPendingSyncStateTx(tx, src, obj.DetectedAt)
		return sourceStateModified, syncState, pendingActionUpdate, nextSyncAt
	}
	return sourceStateUnchanged, syncStateIdle, pendingActionNone, nil
}

func scheduledOrPendingSyncStateTx(tx *gorm.DB, src sourceEntity, at time.Time) (string, *time.Time) {
	nextSyncAt := automaticMutationScheduleAt(src, at)
	if nextSyncAt == nil && sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		nextSyncAt = cloudSourceBindingScheduleAtTx(tx, src.ID, at)
	}
	if nextSyncAt != nil {
		return syncStateScheduled, nextSyncAt
	}
	return syncStatePending, nil
}

func cloudSourceBindingScheduleAtTx(tx *gorm.DB, sourceID string, at time.Time) *time.Time {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	var binding cloudSourceBindingEntity
	if err := tx.Take(&binding, "source_id = ?", sourceID).Error; err != nil {
		return nil
	}
	if !binding.Enabled || !strings.EqualFold(strings.TrimSpace(binding.Status), "ACTIVE") {
		return nil
	}
	scheduleExpr := strings.TrimSpace(binding.ScheduleExpr)
	if scheduleExpr == "" {
		return nil
	}
	return computeNextReconcileTimeWithTZ(scheduleExpr, binding.ScheduleTZ, at)
}

func (s *Store) cloudSourceMutationScheduleAt(ctx context.Context, sourceID string, at time.Time) *time.Time {
	return cloudSourceBindingScheduleAtTx(s.db.WithContext(ctx), sourceID, at)
}

func (s *Store) sourceDocumentStateByPaths(ctx context.Context, sourceID string, paths []string) (map[string]sourceDocumentStateView, error) {
	out := make(map[string]sourceDocumentStateView, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	cleaned := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		path := filepath.Clean(strings.TrimSpace(raw))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		cleaned = append(cleaned, path)
	}
	if len(cleaned) == 0 {
		return out, nil
	}
	treeToObject := map[string]string{}
	queryPaths := append([]string(nil), cleaned...)
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err == nil && sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		mapped, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, src.ID, cleaned, true)
		if err != nil {
			return nil, err
		}
		querySeen := make(map[string]struct{}, len(cleaned)+len(mapped))
		for _, path := range queryPaths {
			querySeen[path] = struct{}{}
		}
		for treePath, objectPath := range mapped {
			objectPath = filepath.Clean(strings.TrimSpace(objectPath))
			if objectPath == "" || objectPath == "." {
				continue
			}
			treeToObject[filepath.Clean(strings.TrimSpace(treePath))] = objectPath
			if _, ok := querySeen[objectPath]; ok {
				continue
			}
			querySeen[objectPath] = struct{}{}
			queryPaths = append(queryPaths, objectPath)
		}
	}
	var rows []sourceDocumentStateEntity
	if err := s.db.WithContext(ctx).
		Where("source_id = ? AND path IN ?", strings.TrimSpace(sourceID), queryPaths).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		path := filepath.Clean(strings.TrimSpace(row.Path))
		view := sourceDocumentStateViewFromEntity(row)
		out[path] = view
		for treePath, objectPath := range treeToObject {
			if objectPath == path {
				out[treePath] = view
			}
		}
	}
	return out, nil
}

func (s *Store) sourceDeletedDocumentStatePaths(ctx context.Context, sourceID string, scopeRoots []string) ([]string, error) {
	rows, err := s.sourceDocumentStatesForSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.SourceState), sourceStateDeleted) {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(row.Path))
		if path == "" || path == "." || row.IsDir {
			continue
		}
		if len(scopeRoots) > 0 && !pathInScope(path, scopeRoots) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out, nil
}

func sourceDocumentStateViewFromEntity(row sourceDocumentStateEntity) sourceDocumentStateView {
	return sourceDocumentStateView{
		ObjectKey:            strings.TrimSpace(row.ObjectKey),
		Path:                 strings.TrimSpace(row.Path),
		Name:                 strings.TrimSpace(row.Name),
		SourceState:          strings.TrimSpace(row.SourceState),
		SyncState:            strings.TrimSpace(row.SyncState),
		PendingAction:        strings.TrimSpace(row.PendingAction),
		SourceVersion:        strings.TrimSpace(row.SourceVersion),
		BaselineVersion:      strings.TrimSpace(row.BaselineVersion),
		SourceSizeBytes:      row.SourceSizeBytes,
		SourceModifiedAt:     row.SourceModifiedAt,
		NextSyncAt:           row.NextSyncAt,
		KnowledgeBasePresent: row.KnowledgeBaseSeen || strings.TrimSpace(row.CoreDocumentID) != "" || strings.TrimSpace(row.BaselineVersion) != "",
		LastSyncedAt:         row.LastSyncedAt,
		LastDetectedAt:       row.LastDetectedAt,
		LastError:            strings.TrimSpace(row.LastError),
		DocumentID:           row.DocumentID,
		SourceExists:         row.SourceExists,
	}
}

func (s *Store) sourceDocumentStatesForSource(ctx context.Context, sourceID string) ([]sourceDocumentStateEntity, error) {
	var rows []sourceDocumentStateEntity
	err := s.db.WithContext(ctx).
		Where("source_id = ?", strings.TrimSpace(sourceID)).
		Order("updated_at DESC, id DESC").
		Find(&rows).Error
	return rows, err
}

func (s *Store) observeTreeSourceDocumentStates(ctx context.Context, src sourceEntity, filePaths []string, fileStats map[string]model.TreeFileStat, scopeRoots []string) error {
	now := time.Now().UTC()
	objects := make([]observedSourceObject, 0, len(filePaths))
	current := make(map[string]struct{}, len(filePaths))
	for _, rawPath := range filePaths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." || isTransientSourceFilePath(path, false) {
			continue
		}
		current[path] = struct{}{}
		stat := fileStats[path]
		if strings.TrimSpace(stat.Path) != "" {
			path = filepath.Clean(strings.TrimSpace(stat.Path))
		}
		sourceChecksum := strings.TrimSpace(stat.Checksum)
		originRef := ""
		if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
			if refs, err := s.cloudTreePathsToObjectRefsIncludingDeleted(ctx, src.ID, []string{rawPath}, true); err == nil {
				if ref, ok := refs[filepath.Clean(strings.TrimSpace(rawPath))]; ok {
					if objectPath := strings.TrimSpace(ref.ObjectPath); objectPath != "" {
						path = filepath.Clean(objectPath)
					}
					originRef = strings.TrimSpace(ref.ExternalObjectID)
				}
			}
		}
		current[path] = struct{}{}
		objects = append(objects, observedSourceObject{
			SourceID:         src.ID,
			TenantID:         src.TenantID,
			ObjectKey:        sourceObjectKey(path, originRef),
			Path:             path,
			Name:             filepath.Base(path),
			SourceExists:     true,
			OriginType:       firstNonEmpty(src.DefaultOriginType, string(model.OriginTypeLocalFS)),
			OriginPlatform:   firstNonEmpty(src.DefaultOriginPlatform, "LOCAL"),
			OriginRef:        originRef,
			SourceChecksum:   sourceChecksum,
			SourceSizeBytes:  stat.Size,
			SourceModifiedAt: stat.ModTime,
			DetectedAt:       now,
		})
	}
	rows, err := s.sourceDocumentStatesForSource(ctx, src.ID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		path := filepath.Clean(strings.TrimSpace(row.Path))
		if path == "" || path == "." || row.IsDir {
			continue
		}
		if _, ok := current[path]; ok {
			continue
		}
		if len(scopeRoots) > 0 && !pathInScope(path, scopeRoots) {
			continue
		}
		if !row.SourceExists || strings.EqualFold(strings.TrimSpace(row.SourceState), sourceStateDeleted) {
			continue
		}
		if strings.TrimSpace(row.BaselineVersion) == "" && !row.KnowledgeBaseSeen && row.DocumentID <= 0 {
			continue
		}
		objects = append(objects, observedSourceObject{
			SourceID:        src.ID,
			TenantID:        src.TenantID,
			ObjectKey:       strings.TrimSpace(row.ObjectKey),
			Path:            path,
			Name:            strings.TrimSpace(row.Name),
			SourceExists:    false,
			OriginType:      firstNonEmpty(row.OriginType, src.DefaultOriginType, string(model.OriginTypeLocalFS)),
			OriginPlatform:  firstNonEmpty(row.OriginPlatform, src.DefaultOriginPlatform, "LOCAL"),
			OriginRef:       strings.TrimSpace(row.OriginRef),
			DetectedAt:      now,
			BaselineVersion: strings.TrimSpace(row.BaselineVersion),
			DocumentID:      row.DocumentID,
			CoreDocumentID:  strings.TrimSpace(row.CoreDocumentID),
		})
	}
	return s.upsertSourceDocumentStates(ctx, src, objects)
}

func stateUpdateType(state sourceDocumentStateView) string {
	switch strings.ToUpper(strings.TrimSpace(state.SourceState)) {
	case sourceStateNew:
		return "NEW"
	case sourceStateModified:
		return "MODIFIED"
	case sourceStateDeleted:
		return "DELETED"
	case sourceStateUnchanged:
		return "UNCHANGED"
	default:
		return ""
	}
}

func pendingSourceStateUpdateType(state sourceDocumentStateView) string {
	update := stateUpdateType(state)
	switch update {
	case "NEW", "MODIFIED", "DELETED":
	default:
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(state.SyncState)) {
	case syncStatePending, syncStateScheduled, syncStateFailed:
		return update
	default:
		return ""
	}
}

func applyStateToDocumentItem(item model.SourceDocumentItem, state sourceDocumentStateView) model.SourceDocumentItem {
	item.ObjectKey = state.ObjectKey
	item.SourceState = state.SourceState
	item.SyncState = state.SyncState
	item.PendingAction = state.PendingAction
	item.SourceVersion = state.SourceVersion
	item.BaselineVersion = state.BaselineVersion
	item.NextSyncAt = state.NextSyncAt
	item.LastError = state.LastError
	item.LastSyncedAt = firstTimePtr(item.LastSyncedAt, state.LastSyncedAt)
	if state.SourceSizeBytes > 0 {
		item.SizeBytes = state.SourceSizeBytes
	}
	if state.SourceModifiedAt != nil {
		item.SourceUpdatedAt = state.SourceModifiedAt
	}
	kb := state.KnowledgeBasePresent || (item.KnowledgeBasePresent != nil && *item.KnowledgeBasePresent)
	item.KnowledgeBasePresent = &kb
	if sourceDocumentStateSuppressesLegacyProcessing(item, state) {
		item.ParseState = legacyIdleParseStateFromSourceState(state.SourceState)
	}
	if update := stateUpdateType(state); update != "" {
		if !sourceStateShouldOverrideUpdate(update, item.UpdateType) {
			return item
		}
		item.UpdateType = update
		item.UpdateDesc = updateTypeDescription(update)
		switch update {
		case "NEW", "MODIFIED", "DELETED":
			v := true
			item.HasUpdate = &v
		case "UNCHANGED":
			v := false
			item.HasUpdate = &v
		}
	}
	return item
}

func sourceDocumentStateSuppressesLegacyProcessing(item model.SourceDocumentItem, state sourceDocumentStateView) bool {
	switch strings.ToUpper(strings.TrimSpace(state.SyncState)) {
	case syncStatePending, syncStateScheduled:
	default:
		return false
	}
	if sourceDocumentItemHasCurrentParseTask(item) {
		return false
	}
	return true
}

func sourceDocumentItemHasCurrentParseTask(item model.SourceDocumentItem) bool {
	if item.ParseTaskID <= 0 && strings.TrimSpace(item.CoreTaskID) == "" && strings.TrimSpace(item.ScanOrchestrationStatus) == "" {
		return false
	}
	targetVersion := strings.TrimSpace(item.ParseTaskTargetVersion)
	desiredVersion := strings.TrimSpace(item.DesiredVersionID)
	if targetVersion != "" && desiredVersion != "" && targetVersion != desiredVersion {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(firstNonEmpty(item.ScanOrchestrationStatus, item.ParseState))) {
	case "PENDING", "QUEUED", "RETRY_WAITING", "STAGING", "SUBMITTED", "RUNNING", "CREATING", "UPLOADING", "UPLOADED":
		return true
	default:
		return false
	}
}

func legacyIdleParseStateFromSourceState(sourceState string) string {
	if strings.EqualFold(strings.TrimSpace(sourceState), sourceStateDeleted) {
		return "DELETED"
	}
	return "SUCCEEDED"
}

func sourceStateOnlyDocumentItem(src sourceEntity, state sourceDocumentStateView) model.SourceDocumentItem {
	path := strings.TrimSpace(state.Path)
	name := strings.TrimSpace(state.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	item := model.SourceDocumentItem{
		DocumentID:         state.DocumentID,
		SourceCreateUserID: strings.TrimSpace(src.CreateUserID),
		Name:               name,
		Path:               path,
		Directory:          filepath.Base(filepath.Dir(path)),
		ParseState:         parseStateFromSourceSyncState(state.SyncState),
		FileType:           fileTypeFromPath(path),
		SizeBytes:          state.SourceSizeBytes,
		SourceUpdatedAt:    state.SourceModifiedAt,
		LastSyncedAt:       state.LastSyncedAt,
	}
	return applyStateToDocumentItem(item, state)
}

func parseStateFromSourceSyncState(syncState string) string {
	switch strings.ToUpper(strings.TrimSpace(syncState)) {
	case syncStateRunning:
		return "RUNNING"
	case syncStateFailed:
		return "FAILED"
	case syncStateScheduled, syncStatePending:
		return "SUCCEEDED"
	case syncStateIdle:
		return "SUCCEEDED"
	default:
		return "SUCCEEDED"
	}
}

func sourceDocumentStateVisibleInSourceDocuments(state sourceDocumentStateView) bool {
	return state.KnowledgeBasePresent
}

func mergeHiddenPendingSourceStateCounts(summary *model.SourceDocumentsSummary, rows []sourceDocumentStateEntity) {
	if summary == nil {
		return
	}
	seenPaths := map[string]struct{}{}
	for _, row := range rows {
		path := filepath.Clean(strings.TrimSpace(row.Path))
		if path == "" || path == "." || row.IsDir {
			continue
		}
		if _, ok := seenPaths[path]; ok {
			continue
		}
		state := sourceDocumentStateViewFromEntity(row)
		if sourceDocumentStateVisibleInSourceDocuments(state) {
			seenPaths[path] = struct{}{}
			continue
		}
		switch pendingSourceStateUpdateType(state) {
		case "NEW":
			summary.NewCount++
		case "MODIFIED":
			summary.ModifiedCount++
		case "DELETED":
			summary.DeletedCount++
		default:
			continue
		}
		seenPaths[path] = struct{}{}
	}
	summary.PendingPullCount = summary.NewCount + summary.ModifiedCount + summary.DeletedCount
}

func summarizeSourceDocumentStates(rows []sourceDocumentStateEntity, fallbackTotal int, parsedCount, storage int64) model.SourceDocumentsSummary {
	summary := model.SourceDocumentsSummary{
		ParsedDocumentCount: parsedCount,
		StorageBytes:        storage,
		TotalDocumentCount:  int64(fallbackTotal),
	}
	seenPaths := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		state := sourceDocumentStateViewFromEntity(row)
		if !sourceDocumentStateVisibleInSourceDocuments(state) {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(row.Path))
		if path == "" || path == "." || row.IsDir {
			continue
		}
		if _, ok := seenPaths[path]; !ok {
			seenPaths[path] = struct{}{}
		}
		switch strings.ToUpper(strings.TrimSpace(row.SourceState)) {
		case sourceStateNew:
			summary.NewCount++
		case sourceStateModified:
			summary.ModifiedCount++
		case sourceStateDeleted:
			summary.DeletedCount++
		}
	}
	if len(seenPaths) > 0 {
		summary.TotalDocumentCount = int64(len(seenPaths))
	}
	summary.PendingPullCount = summary.NewCount + summary.ModifiedCount + summary.DeletedCount
	return summary
}

func (s *Store) sourceDocumentStatesForSources(ctx context.Context, sourceIDs []string) (map[string][]sourceDocumentStateEntity, error) {
	out := make(map[string][]sourceDocumentStateEntity, len(sourceIDs))
	ids := uniqueTrimmedStrings(sourceIDs)
	if len(ids) == 0 {
		return out, nil
	}
	var rows []sourceDocumentStateEntity
	if err := s.db.WithContext(ctx).Where("source_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.SourceID] = append(out[row.SourceID], row)
	}
	return out, nil
}

func updateSourceDocumentStateTaskRunningTx(tx *gorm.DB, task parseTaskEntity, at time.Time, coreDocumentID string) error {
	updates := map[string]any{
		"sync_state":     syncStateRunning,
		"active_task_id": task.ID,
		"last_error":     "",
		"updated_at":     at.UTC(),
	}
	if strings.TrimSpace(coreDocumentID) != "" {
		updates["core_document_id"] = strings.TrimSpace(coreDocumentID)
	}
	res := tx.Model(&sourceDocumentStateEntity{}).
		Where("document_id = ? AND source_state IN ?", task.DocumentID, []string{sourceStateNew, sourceStateModified, sourceStateDeleted}).
		Updates(updates)
	return res.Error
}

func updateSourceDocumentStateTaskQueuedTx(tx *gorm.DB, task parseTaskEntity, at time.Time) error {
	query := tx.Model(&sourceDocumentStateEntity{}).
		Where("document_id = ? AND source_state IN ?", task.DocumentID, []string{sourceStateNew, sourceStateModified, sourceStateDeleted})
	if err := query.
		UpdateColumns(map[string]any{
			"sync_state":     syncStatePending,
			"active_task_id": task.ID,
			"last_error":     "",
			"updated_at":     at.UTC(),
		}).Error; err != nil {
		return err
	}
	return tx.Exec(
		"UPDATE source_document_states SET next_sync_at = NULL WHERE document_id = ? AND source_state IN (?, ?, ?)",
		task.DocumentID,
		sourceStateNew,
		sourceStateModified,
		sourceStateDeleted,
	).Error
}

func updateSourceDocumentStateTaskFailedTx(tx *gorm.DB, task parseTaskEntity, at time.Time, lastError string) error {
	return tx.Model(&sourceDocumentStateEntity{}).
		Where("document_id = ? AND (active_task_id = ? OR active_task_id = 0)", task.DocumentID, task.ID).
		Updates(map[string]any{
			"sync_state": syncStateFailed,
			"last_error": lastError,
			"updated_at": at.UTC(),
		}).Error
}

func updateSourceDocumentStateTaskSucceededTx(tx *gorm.DB, task parseTaskEntity, doc documentEntity, at time.Time, targetVersion string) error {
	action := normalizeTaskAction(task.TaskAction)
	if action == taskActionDelete {
		return tx.
			Where("document_id = ? OR active_task_id = ?", task.DocumentID, task.ID).
			Delete(&sourceDocumentStateEntity{}).Error
	}
	version := strings.TrimSpace(targetVersion)
	if version == "" {
		version = strings.TrimSpace(task.TargetVersionID)
	}
	var state sourceDocumentStateEntity
	if err := tx.Where("document_id = ? OR active_task_id = ?", task.DocumentID, task.ID).
		Order("id DESC").
		Take(&state).Error; err == nil {
		if sourceVersion := strings.TrimSpace(state.SourceVersion); sourceVersion != "" {
			version = sourceVersion
		}
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	query := tx.Model(&sourceDocumentStateEntity{}).
		Where("document_id = ? OR active_task_id = ?", task.DocumentID, task.ID)
	if err := query.
		UpdateColumns(map[string]any{
			"source_state":        sourceStateUnchanged,
			"sync_state":          syncStateIdle,
			"pending_action":      pendingActionNone,
			"baseline_version":    version,
			"source_version":      version,
			"active_task_id":      0,
			"core_document_id":    strings.TrimSpace(doc.CoreDocumentID),
			"knowledge_base_seen": true,
			"last_synced_at":      &at,
			"last_error":          "",
			"updated_at":          at.UTC(),
		}).Error; err != nil {
		return err
	}
	return tx.Exec("UPDATE source_document_states SET next_sync_at = NULL WHERE document_id = ? OR active_task_id = ?", task.DocumentID, task.ID).Error
}

func setNullableTimeColumn(updates map[string]any, column string, value *time.Time) {
	if value == nil {
		updates[column] = gorm.Expr("NULL")
		return
	}
	updates[column] = value
}

func applyStateToTreeNode(item model.TreeNode, state sourceDocumentStateView) model.TreeNode {
	item.ObjectKey = state.ObjectKey
	item.SourceState = state.SourceState
	item.SyncState = state.SyncState
	item.PendingAction = state.PendingAction
	item.NextSyncAt = state.NextSyncAt
	item.LastError = state.LastError
	kb := state.KnowledgeBasePresent
	item.KnowledgeBasePresent = &kb
	if update := stateUpdateType(state); update != "" {
		if !sourceStateShouldOverrideUpdate(update, item.UpdateType) {
			return item
		}
		item.UpdateType = update
		item.UpdateDesc = updateTypeDescription(update)
		if strings.TrimSpace(item.StatusSource) == "" || strings.EqualFold(strings.TrimSpace(item.StatusSource), "UNKNOWN") {
			item.StatusSource = "SOURCE_DOCUMENT_STATES"
		}
		switch update {
		case "NEW", "MODIFIED", "DELETED":
			v := true
			item.HasUpdate = &v
			selectable := strings.ToUpper(strings.TrimSpace(state.SyncState)) != syncStateRunning
			item.Selectable = &selectable
		case "UNCHANGED":
			v := false
			item.HasUpdate = &v
		}
	}
	return item
}

func sourceStateShouldOverrideUpdate(sourceUpdate, currentUpdate string) bool {
	sourceUpdate = strings.ToUpper(strings.TrimSpace(sourceUpdate))
	currentUpdate = strings.ToUpper(strings.TrimSpace(currentUpdate))
	switch sourceUpdate {
	case "NEW", "MODIFIED", "DELETED":
		if currentUpdate == "" || currentUpdate == "UNKNOWN" || currentUpdate == "UNCHANGED" || currentUpdate == sourceUpdate {
			return true
		}
		return sourceUpdate == "MODIFIED" && currentUpdate == "NEW"
	case "UNCHANGED":
		return currentUpdate == "" || currentUpdate == "UNKNOWN" || currentUpdate == "UNCHANGED" || currentUpdate == "NEW"
	default:
		return false
	}
}

func firstTimePtr(primary, fallback *time.Time) *time.Time {
	if primary != nil {
		return primary
	}
	return fallback
}
