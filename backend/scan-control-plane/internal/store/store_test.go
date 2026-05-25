package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/lazymind/scan_control_plane/internal/model"
	"github.com/lazymind/scan_control_plane/internal/sourcelayout"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cp.db")
	st, err := New("sqlite", dbPath, 10*time.Second, zap.NewNop())
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func createTestSource(t *testing.T, st *Store) model.Source {
	t.Helper()
	src, err := st.CreateSource(context.Background(), model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      true,
		IdleWindowSeconds: 10,
		ReconcileSeconds:  10,
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	return src
}

func TestBatchApplyDocumentMutationsLatestWins(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)

	newer := time.Now().UTC()
	older := newer.Add(-10 * time.Second)

	events := []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: "/tmp/watch/a.txt", OccurredAt: newer},
	}
	mutations, err := st.BuildMutationsFromEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(context.Background(), mutations); err != nil {
		t.Fatalf("apply newer mutation failed: %v", err)
	}

	events = []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: "/tmp/watch/a.txt", OccurredAt: older},
	}
	mutations, err = st.BuildMutationsFromEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("build older mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(context.Background(), mutations); err != nil {
		t.Fatalf("apply older mutation failed: %v", err)
	}

	var doc documentEntity
	if err := st.db.WithContext(context.Background()).
		Where("tenant_id = ? AND source_id = ? AND source_object_id = ?", src.TenantID, src.ID, "/tmp/watch/a.txt").
		Take(&doc).Error; err != nil {
		t.Fatalf("load document failed: %v", err)
	}
	if doc.LastModifiedAt == nil {
		t.Fatalf("last_modified_at should not be nil")
	}
	if !doc.LastModifiedAt.Equal(newer) {
		t.Fatalf("expected last_modified_at=%v, got %v", newer, doc.LastModifiedAt)
	}
	var state sourceDocumentStateEntity
	if err := st.db.WithContext(context.Background()).
		Where("source_id = ? AND path = ?", src.ID, "/tmp/watch/a.txt").
		Take(&state).Error; err != nil {
		t.Fatalf("load source document state failed: %v", err)
	}
	if !state.LastDetectedAt.Equal(newer) {
		t.Fatalf("expected source state last_detected_at=%v, got %v", newer, state.LastDetectedAt)
	}
	if got, want := state.SourceVersion, "v_"+newer.Format(time.RFC3339Nano); got != want {
		t.Fatalf("expected source_version=%s, got %s", want, got)
	}
}

func TestBatchApplyDocumentMutationsCloudRenameByOriginRef(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "cloud-src",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	originRef := "obj_rename_001"
	oldPath := "/tmp/cloud/src/mirror/docs/spec.md"
	newPath := "/tmp/cloud/src/mirror/docs/spec.docx"
	firstAt := time.Now().UTC().Add(-2 * time.Minute)
	secondAt := firstAt.Add(10 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{
			SourceID:       src.ID,
			EventType:      "modified",
			Path:           oldPath,
			OccurredAt:     firstAt,
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			OriginRef:      originRef,
		},
	})
	if err != nil {
		t.Fatalf("build first cloud mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply first cloud mutations failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, oldPath)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).Where("id = ?", doc.ID).Updates(map[string]any{
		"core_document_id":   "core_doc_rename",
		"current_version_id": "v_old",
	}).Error; err != nil {
		t.Fatalf("prepare core document fields failed: %v", err)
	}

	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{
			SourceID:       src.ID,
			EventType:      "modified",
			Path:           newPath,
			OccurredAt:     secondAt,
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			OriginRef:      originRef,
		},
	})
	if err != nil {
		t.Fatalf("build rename cloud mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply rename cloud mutations failed: %v", err)
	}

	var docs []documentEntity
	if err := st.db.WithContext(ctx).Where("source_id = ?", src.ID).Find(&docs).Error; err != nil {
		t.Fatalf("query cloud documents failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document after rename, got %d", len(docs))
	}
	renamed := docs[0]
	if renamed.SourceObjectID != newPath {
		t.Fatalf("expected renamed source_object_id=%s, got %s", newPath, renamed.SourceObjectID)
	}
	if renamed.CoreDocumentID != "core_doc_rename" {
		t.Fatalf("expected core_document_id to be preserved, got %s", renamed.CoreDocumentID)
	}

	var oldCount int64
	if err := st.db.WithContext(ctx).
		Model(&documentEntity{}).
		Where("source_id = ? AND source_object_id = ?", src.ID, oldPath).
		Count(&oldCount).Error; err != nil {
		t.Fatalf("count old path failed: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("expected old path document removed after rename, still has %d rows", oldCount)
	}

	created, err := st.ScheduleDueParses(ctx, secondAt.Add(20*time.Second))
	if err != nil {
		t.Fatalf("schedule due parses after rename failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected created parse task count=1, got %d", created)
	}
	tasks := loadTasksByDocumentID(t, st, renamed.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 parse task for renamed document, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[0].TaskAction) != taskActionReparse {
		t.Fatalf("expected task_action REPARSE for renamed document, got %s", tasks[0].TaskAction)
	}
}

func TestPullAndAckCommandFlow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	_ = createTestSource(t, st)

	resp, err := st.PullPendingCommands(context.Background(), model.PullCommandsRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("pull pending commands failed: %v", err)
	}
	if len(resp.Commands) == 0 {
		t.Fatalf("expected at least one command")
	}
	cmd := resp.Commands[0]
	if cmd.ID <= 0 {
		t.Fatalf("expected command id > 0")
	}

	if err := st.AckCommand(context.Background(), model.AckCommandRequest{
		AgentID:   "agent-1",
		CommandID: cmd.ID,
		Success:   true,
	}); err != nil {
		t.Fatalf("ack command failed: %v", err)
	}

	var entity agentCommandEntity
	if err := st.db.WithContext(context.Background()).Take(&entity, "id = ?", cmd.ID).Error; err != nil {
		t.Fatalf("load command failed: %v", err)
	}
	if entity.Status != commandStatusAcked {
		t.Fatalf("expected status %s, got %s", commandStatusAcked, entity.Status)
	}
}

func TestPullPendingCommandsSkipsDecodeFailedPayload(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	if err := st.db.WithContext(ctx).Where("1 = 1").Delete(&agentCommandEntity{}).Error; err != nil {
		t.Fatalf("clear commands failed: %v", err)
	}

	now := time.Now().UTC()
	bad := agentCommandEntity{
		AgentID:     src.AgentID,
		Type:        string(model.CommandStartSource),
		Payload:     "{not-json",
		Status:      commandStatusPending,
		NextRetryAt: &now,
		CreatedAt:   now,
	}
	if err := st.db.WithContext(ctx).Create(&bad).Error; err != nil {
		t.Fatalf("create bad command failed: %v", err)
	}
	good := agentCommandEntity{
		AgentID:     src.AgentID,
		Type:        string(model.CommandStartSource),
		Payload:     `{"source_id":"src-ok","tenant_id":"tenant-1","root_path":"/tmp/watch"}`,
		Status:      commandStatusPending,
		CreatedAt:   now.Add(1 * time.Millisecond),
		NextRetryAt: &now,
	}
	if err := st.db.WithContext(ctx).Create(&good).Error; err != nil {
		t.Fatalf("create good command failed: %v", err)
	}

	pulled, err := st.PullPendingCommands(ctx, model.PullCommandsRequest{
		AgentID:  src.AgentID,
		TenantID: src.TenantID,
	})
	if err != nil {
		t.Fatalf("pull pending commands failed: %v", err)
	}
	if len(pulled.Commands) != 1 {
		t.Fatalf("expected exactly 1 decodable command, got %d", len(pulled.Commands))
	}
	if pulled.Commands[0].ID != good.ID {
		t.Fatalf("expected pulled command id=%d, got %d", good.ID, pulled.Commands[0].ID)
	}

	var badAfter agentCommandEntity
	if err := st.db.WithContext(ctx).Take(&badAfter, "id = ?", bad.ID).Error; err != nil {
		t.Fatalf("load bad command failed: %v", err)
	}
	if badAfter.Status != commandStatusPending {
		t.Fatalf("expected bad command stay pending, got %s", badAfter.Status)
	}
	if badAfter.DispatchedAt != nil {
		t.Fatalf("expected bad command dispatched_at to remain nil")
	}

	var goodAfter agentCommandEntity
	if err := st.db.WithContext(ctx).Take(&goodAfter, "id = ?", good.ID).Error; err != nil {
		t.Fatalf("load good command failed: %v", err)
	}
	if goodAfter.Status != commandStatusDispatched {
		t.Fatalf("expected good command status %s, got %s", commandStatusDispatched, goodAfter.Status)
	}
	if goodAfter.DispatchedAt == nil || goodAfter.DispatchedAt.IsZero() {
		t.Fatalf("expected good command dispatched_at to be set")
	}
}

func TestRegisterAgentRequeuesWatchSources(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	if err := st.db.WithContext(ctx).Where("1 = 1").Delete(&agentCommandEntity{}).Error; err != nil {
		t.Fatalf("clear commands failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&sourceEntity{}).
		Where("id = ?", src.ID).
		Updates(map[string]any{"status": string(model.SourceStatusDegraded)}).Error; err != nil {
		t.Fatalf("mark source degraded failed: %v", err)
	}

	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:    src.AgentID,
		TenantID:   src.TenantID,
		Hostname:   "agent-host",
		Version:    "test",
		ListenAddr: "http://file-watcher:19090",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}

	pulled, err := st.PullPendingCommands(ctx, model.PullCommandsRequest{
		AgentID:  src.AgentID,
		TenantID: src.TenantID,
	})
	if err != nil {
		t.Fatalf("pull commands failed: %v", err)
	}
	if len(pulled.Commands) != 2 {
		t.Fatalf("expected start and reconcile commands, got %d", len(pulled.Commands))
	}
	if pulled.Commands[0].Type != model.CommandStartSource {
		t.Fatalf("expected first command %s, got %s", model.CommandStartSource, pulled.Commands[0].Type)
	}
	if pulled.Commands[1].Type != model.CommandScanSource || pulled.Commands[1].Mode != "reconcile" {
		t.Fatalf("expected second command reconcile scan, got type=%s mode=%s", pulled.Commands[1].Type, pulled.Commands[1].Mode)
	}

	updated, err := st.GetSource(ctx, src.ID)
	if err != nil {
		t.Fatalf("load updated source failed: %v", err)
	}
	if updated.Status != model.SourceStatusEnabled {
		t.Fatalf("expected source status enabled after register, got %s", updated.Status)
	}
}

func TestScheduleDueParsesKeepsHistoryAfterSuccess(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/a.txt"

	firstAt := time.Now().UTC().Add(-40 * time.Second)
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: firstAt},
	})
	if err != nil {
		t.Fatalf("build first mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply first mutations failed: %v", err)
	}

	created, err := st.ScheduleDueParses(ctx, firstAt.Add(12*time.Second))
	if err != nil {
		t.Fatalf("schedule first due parse failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected first created=1, got %d", created)
	}

	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after first schedule, got %d", len(tasks))
	}
	if tasks[0].Status != "PENDING" {
		t.Fatalf("expected first task status PENDING, got %s", tasks[0].Status)
	}

	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark first task succeeded failed: %v", err)
	}

	secondAt := firstAt.Add(20 * time.Second)
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: secondAt},
	})
	if err != nil {
		t.Fatalf("build second mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply second mutations failed: %v", err)
	}

	created, err = st.ScheduleDueParses(ctx, secondAt.Add(12*time.Second))
	if err != nil {
		t.Fatalf("schedule second due parse failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected second created=1, got %d", created)
	}

	tasks = loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 2 {
		t.Fatalf("expected 2 task history rows, got %d", len(tasks))
	}
	if tasks[0].Status != "SUCCEEDED" {
		t.Fatalf("expected first task status SUCCEEDED, got %s", tasks[0].Status)
	}
	if tasks[1].Status != "PENDING" {
		t.Fatalf("expected second task status PENDING, got %s", tasks[1].Status)
	}
	if tasks[0].ID == tasks[1].ID {
		t.Fatalf("expected distinct task rows, got same id=%d", tasks[0].ID)
	}
}

func TestScheduleDueParsesMergesSinglePendingTask(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/b.txt"

	firstAt := time.Now().UTC().Add(-40 * time.Second)
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: firstAt},
	})
	if err != nil {
		t.Fatalf("build first mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply first mutations failed: %v", err)
	}

	created, err := st.ScheduleDueParses(ctx, firstAt.Add(12*time.Second))
	if err != nil {
		t.Fatalf("schedule first due parse failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected first created=1, got %d", created)
	}

	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 pending task, got %d", len(tasks))
	}
	firstTarget := tasks[0].TargetVersionID

	secondAt := firstAt.Add(20 * time.Second)
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: secondAt},
	})
	if err != nil {
		t.Fatalf("build second mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply second mutations failed: %v", err)
	}

	created, err = st.ScheduleDueParses(ctx, secondAt.Add(12*time.Second))
	if err != nil {
		t.Fatalf("schedule second due parse failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected second created=0 (merge pending), got %d", created)
	}

	doc = loadDocumentByPath(t, st, src, path)
	tasks = loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected still 1 task row, got %d", len(tasks))
	}
	if tasks[0].Status != "PENDING" {
		t.Fatalf("expected merged task status PENDING, got %s", tasks[0].Status)
	}
	if tasks[0].TargetVersionID == firstTarget {
		t.Fatalf("expected merged task target_version to be refreshed, still %s", tasks[0].TargetVersionID)
	}
	if tasks[0].TargetVersionID != doc.DesiredVersionID {
		t.Fatalf("expected merged task target_version=%s, got %s", doc.DesiredVersionID, tasks[0].TargetVersionID)
	}
}

func TestAutomaticWatchUpsertsWaitForReconcileSchedule(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "scheduled-watch-upsert",
		RootPath:          "/tmp/scheduled-watch-upsert",
		AgentID:           "agent-1",
		WatchEnabled:      true,
		IdleWindowSeconds: 10,
		ReconcileSeconds:  3600,
	})
	if err != nil {
		t.Fatalf("create scheduled watch source failed: %v", err)
	}

	baseAt := time.Now().UTC().Add(-2 * time.Minute)
	newPath := "/tmp/scheduled-watch-upsert/new-later.txt"
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "created", Path: newPath, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build create mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply create mutation failed: %v", err)
	}
	newDoc := loadDocumentByPath(t, st, src, newPath)
	if newDoc.NextParseAt == nil {
		t.Fatalf("expected new document next_parse_at to be set")
	}
	expectedNext := newDoc.NextParseAt.UTC()
	if !expectedNext.After(baseAt) {
		t.Fatalf("expected new document next_parse_at after create event time %v, got %v", baseAt, expectedNext)
	}

	created, err := st.ScheduleDueParses(ctx, baseAt.Add(30*time.Second))
	if err != nil {
		t.Fatalf("schedule before reconcile failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected no create task before reconcile schedule, got %d", created)
	}
	if tasks := loadTasksByDocumentID(t, st, newDoc.ID); len(tasks) != 0 {
		t.Fatalf("expected no tasks before reconcile schedule, got %+v", tasks)
	}

	created, err = st.ScheduleDueParses(ctx, expectedNext.Add(time.Second))
	if err != nil {
		t.Fatalf("schedule create at reconcile failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected one create task at reconcile schedule, got %d", created)
	}
	tasks := loadTasksByDocumentID(t, st, newDoc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one create task, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[0].TaskAction) != taskActionCreate {
		t.Fatalf("expected create task action, got %s", tasks[0].TaskAction)
	}

	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, newDoc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark create task succeeded failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", newDoc.ID).
		Update("core_document_id", "core-doc-scheduled-watch-upsert").Error; err != nil {
		t.Fatalf("seed core document id after create failed: %v", err)
	}
	modifiedAt := expectedNext.Add(2 * time.Minute)
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: newPath, OccurredAt: modifiedAt},
	})
	if err != nil {
		t.Fatalf("build modify mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply modify mutation failed: %v", err)
	}
	modifiedDoc := loadDocumentByPath(t, st, src, newPath)
	if modifiedDoc.NextParseAt == nil {
		t.Fatalf("expected modified document next_parse_at to be set")
	}
	expectedModifiedNext := modifiedDoc.NextParseAt.UTC()
	if !expectedModifiedNext.After(modifiedAt) {
		t.Fatalf("expected modified document next_parse_at after modify event time %v, got %v", modifiedAt, expectedModifiedNext)
	}
	created, err = st.ScheduleDueParses(ctx, modifiedAt.Add(30*time.Second))
	if err != nil {
		t.Fatalf("schedule before modify reconcile failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected no reparse task before reconcile schedule, got %d", created)
	}
	created, err = st.ScheduleDueParses(ctx, expectedModifiedNext.Add(time.Second))
	if err != nil {
		t.Fatalf("schedule modify at reconcile failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected one reparse task at reconcile schedule, got %d", created)
	}
	tasks = loadTasksByDocumentID(t, st, newDoc.ID)
	if len(tasks) != 2 {
		t.Fatalf("expected create and reparse task history, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[1].TaskAction) != taskActionReparse {
		t.Fatalf("expected reparse task action, got %s", tasks[1].TaskAction)
	}
}

func TestClaimDueTasksIncludesSourceCreator(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:  "agent-owner",
		TenantID: "tenant-1",
		Hostname: "test",
		Version:  "v1",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-owner",
		RootPath:          "/tmp/owner-watch",
		AgentID:           "agent-owner",
		CreateUserID:      "owner-1",
		DatasetID:         "ds-owner",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	now := time.Now().UTC()
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: "/tmp/owner-watch/a.txt", OccurredAt: now.Add(-20 * time.Second)},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if created, err := st.ScheduleDueParses(ctx, now); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	} else if created != 1 {
		t.Fatalf("expected one task, got %d", created)
	}

	tasks, err := st.ClaimDueTasks(ctx, "worker-1", now, 1, time.Minute)
	if err != nil {
		t.Fatalf("claim due tasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one claimed task, got %d", len(tasks))
	}
	if tasks[0].SourceCreateUserID != "owner-1" {
		t.Fatalf("expected source creator owner-1, got %q", tasks[0].SourceCreateUserID)
	}
	if tasks[0].SourceDatasetID != "ds-owner" {
		t.Fatalf("expected source dataset ds-owner, got %q", tasks[0].SourceDatasetID)
	}
}

func TestValidateTaskSubmissionRejectsStaleVersionAfterStaging(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/stale-version.txt"
	baseAt := time.Now().UTC().Add(-30 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build first mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply first mutation failed: %v", err)
	}
	if created, err := st.ScheduleDueParses(ctx, baseAt.Add(20*time.Second)); err != nil {
		t.Fatalf("schedule first task failed: %v", err)
	} else if created != 1 {
		t.Fatalf("expected one task, got %d", created)
	}
	claimed, err := st.ClaimDueTasks(ctx, "worker-1", baseAt.Add(20*time.Second), 1, time.Minute)
	if err != nil {
		t.Fatalf("claim first task failed: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected one claimed task, got %d", len(claimed))
	}
	if err := st.MarkTaskStaging(ctx, claimed[0].TaskID); err != nil {
		t.Fatalf("mark staging failed: %v", err)
	}

	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt.Add(5 * time.Second)},
	})
	if err != nil {
		t.Fatalf("build newer mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply newer mutation failed: %v", err)
	}
	validation, err := st.ValidateTaskSubmission(ctx, claimed[0].TaskID)
	if err != nil {
		t.Fatalf("validate stale task failed: %v", err)
	}
	if validation.Valid {
		t.Fatalf("expected stale staged task to be rejected")
	}
	if !strings.Contains(validation.Reason, "target_version_id") {
		t.Fatalf("expected stale version reason, got %q", validation.Reason)
	}
}

func TestValidateTaskSubmissionRejectsStaleActionAfterDelete(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/stale-action.txt"
	baseAt := time.Now().UTC().Add(-30 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build create mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply create mutation failed: %v", err)
	}
	if created, err := st.ScheduleDueParses(ctx, baseAt.Add(20*time.Second)); err != nil {
		t.Fatalf("schedule create task failed: %v", err)
	} else if created != 1 {
		t.Fatalf("expected one task, got %d", created)
	}
	claimed, err := st.ClaimDueTasks(ctx, "worker-1", baseAt.Add(20*time.Second), 1, time.Minute)
	if err != nil {
		t.Fatalf("claim create task failed: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected one claimed task, got %d", len(claimed))
	}
	if err := st.MarkTaskStaging(ctx, claimed[0].TaskID); err != nil {
		t.Fatalf("mark staging failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"parse_status":     "DELETED",
			"core_document_id": "core-doc-stale-action",
		}).Error; err != nil {
		t.Fatalf("seed deleted document state failed: %v", err)
	}

	validation, err := st.ValidateTaskSubmission(ctx, claimed[0].TaskID)
	if err != nil {
		t.Fatalf("validate stale action task failed: %v", err)
	}
	if validation.Valid {
		t.Fatalf("expected stale action task to be rejected")
	}
	if !strings.Contains(validation.Reason, "task_action") {
		t.Fatalf("expected stale action reason, got %q", validation.Reason)
	}
}

func TestCreateSourceRejectsSameRootPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	req := model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-1",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	}
	first, err := st.CreateSource(ctx, req)
	if err != nil {
		t.Fatalf("create first source failed: %v", err)
	}
	req.Name = "src-2"
	if _, err := st.CreateSource(ctx, req); !errors.Is(err, ErrSourceAlreadyExists) {
		t.Fatalf("expected ErrSourceAlreadyExists, got %v", err)
	}
	var count int64
	if err := st.db.WithContext(ctx).Model(&sourceEntity{}).Count(&count).Error; err != nil {
		t.Fatalf("count sources failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only 1 source row, got %d", count)
	}
	var current sourceEntity
	if err := st.db.WithContext(ctx).Take(&current, "id = ?", first.ID).Error; err != nil {
		t.Fatalf("load first source failed: %v", err)
	}
	if current.Name != "src-1" {
		t.Fatalf("expected original source to remain unchanged, got name %q", current.Name)
	}
	var cmdCount int64
	if err := st.db.WithContext(ctx).Model(&agentCommandEntity{}).Count(&cmdCount).Error; err != nil {
		t.Fatalf("count commands failed: %v", err)
	}
	if cmdCount != 0 {
		t.Fatalf("expected no commands when duplicate create is rejected, got %d", cmdCount)
	}
}

func TestEnsureSourceByRootPathReusesSameRootPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	req := model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-1",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	}
	first, err := st.EnsureSourceByRootPath(ctx, req)
	if err != nil {
		t.Fatalf("ensure first source failed: %v", err)
	}
	oldCreatedAt := first.CreatedAt.Add(-time.Hour)
	if err := st.db.WithContext(ctx).Model(&sourceEntity{}).Where("id = ?", first.ID).Update("created_at", oldCreatedAt).Error; err != nil {
		t.Fatalf("backdate first source created_at failed: %v", err)
	}
	req.Name = "src-2"
	second, err := st.EnsureSourceByRootPath(ctx, req)
	if err != nil {
		t.Fatalf("ensure second source failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same source id, got first=%s second=%s", first.ID, second.ID)
	}
	if !second.CreatedAt.After(oldCreatedAt) {
		t.Fatalf("expected reused source created_at to refresh, old=%v second=%v", oldCreatedAt, second.CreatedAt)
	}
	var count int64
	if err := st.db.WithContext(ctx).Model(&sourceEntity{}).Count(&count).Error; err != nil {
		t.Fatalf("count sources failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only 1 source row, got %d", count)
	}
	var cmdCount int64
	if err := st.db.WithContext(ctx).Model(&agentCommandEntity{}).Count(&cmdCount).Error; err != nil {
		t.Fatalf("count commands failed: %v", err)
	}
	if cmdCount != 0 {
		t.Fatalf("expected no commands when watch disabled, got %d", cmdCount)
	}
}

func TestUpdateSourceRejectsDuplicateRootPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	first, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-1",
		RootPath:          "/tmp/watch-a",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create first source failed: %v", err)
	}
	second, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-2",
		RootPath:          "/tmp/watch-b",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create second source failed: %v", err)
	}

	if _, err := st.UpdateSource(ctx, second.ID, model.UpdateSourceRequest{RootPath: first.RootPath}); !errors.Is(err, ErrSourceAlreadyExists) {
		t.Fatalf("expected ErrSourceAlreadyExists, got %v", err)
	}
	reloaded, err := st.GetSource(ctx, second.ID)
	if err != nil {
		t.Fatalf("reload second source failed: %v", err)
	}
	if reloaded.RootPath != second.RootPath {
		t.Fatalf("expected second root path to remain %q, got %q", second.RootPath, reloaded.RootPath)
	}
}

func TestListSourcesFiltersByCreateUserID(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	baseReq := model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-user-1",
		RootPath:          "/tmp/shared-watch",
		AgentID:           "agent-1",
		CreateUserID:      "user-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	}
	userOneSource, err := st.CreateSource(ctx, baseReq)
	if err != nil {
		t.Fatalf("create user-1 source failed: %v", err)
	}
	if userOneSource.CreateUserID != "user-1" {
		t.Fatalf("expected user-1 source create_user_id, got %q", userOneSource.CreateUserID)
	}

	baseReq.Name = "src-user-2"
	baseReq.CreateUserID = "user-2"
	userTwoSource, err := st.CreateSource(ctx, baseReq)
	if err != nil {
		t.Fatalf("create user-2 source failed: %v", err)
	}
	if userOneSource.ID == userTwoSource.ID {
		t.Fatalf("expected separate sources for different creators sharing a root path")
	}

	userOneSources, err := st.ListSources(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatalf("list user-1 sources failed: %v", err)
	}
	if len(userOneSources) != 1 || userOneSources[0].ID != userOneSource.ID {
		t.Fatalf("expected only user-1 source, got %+v", userOneSources)
	}

	userTwoSources, err := st.ListSources(ctx, "tenant-1", "user-2")
	if err != nil {
		t.Fatalf("list user-2 sources failed: %v", err)
	}
	if len(userTwoSources) != 1 || userTwoSources[0].ID != userTwoSource.ID {
		t.Fatalf("expected only user-2 source, got %+v", userTwoSources)
	}
}

func TestCreateCloudSourceAutoRootPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "cloud-src",
		RootPath:              "",
		AgentID:               "agent-1",
		DefaultOriginType:     "CLOUD_SYNC",
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}
	if !strings.HasPrefix(src.RootPath, sourcelayout.CloudSourceBaseRoot+string(filepath.Separator)) {
		t.Fatalf("expected auto cloud root path under %q, got %q", sourcelayout.CloudSourceBaseRoot, src.RootPath)
	}
	if src.SourceType != "cloud_sync" {
		t.Fatalf("expected source_type=cloud_sync, got %s", src.SourceType)
	}
	if src.WatchEnabled {
		t.Fatalf("expected cloud source watch_enabled=false")
	}
	if !strings.EqualFold(src.DefaultOriginType, "CLOUD_SYNC") {
		t.Fatalf("expected default_origin_type=CLOUD_SYNC, got %s", src.DefaultOriginType)
	}
}

func TestCreateLocalSourceManualReconcileScheduleClearsSchedule(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "manual-src",
		RootPath:          "/tmp/manual-watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
		ReconcileSeconds:  86400,
		ReconcileSchedule: "manual",
	})
	if err != nil {
		t.Fatalf("create manual source failed: %v", err)
	}
	if src.WatchEnabled {
		t.Fatalf("expected watch_enabled=false")
	}
	if src.ReconcileSchedule != "" {
		t.Fatalf("expected manual reconcile_schedule to be cleared, got %q", src.ReconcileSchedule)
	}
	if src.ReconcileSeconds != 86400 {
		t.Fatalf("expected reconcile_seconds=86400, got %d", src.ReconcileSeconds)
	}
}

func TestGenerateTasksForSourceQueuesBaselineSnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:  "partial",
		Paths: []string{"/tmp/watch/a.txt"},
	})
	if err != nil {
		t.Fatalf("generate tasks failed: %v", err)
	}
	if !resp.BaselineSnapshotQueued {
		t.Fatalf("expected baseline snapshot queued")
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected accepted_count=1, got %d", resp.AcceptedCount)
	}

	pulled, err := st.PullPendingCommands(ctx, model.PullCommandsRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("pull commands failed: %v", err)
	}
	if len(pulled.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(pulled.Commands))
	}
	if pulled.Commands[0].Type != model.CommandSnapshotSource {
		t.Fatalf("expected snapshot_source command, got %s", pulled.Commands[0].Type)
	}
}

func TestGenerateTasksForSourceCreatesManualPullJob(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-jobs",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:  "partial",
		Paths: []string{"/tmp/watch/job-a.txt", "/tmp/watch/job-b.txt"},
	})
	if err != nil {
		t.Fatalf("generate tasks failed: %v", err)
	}
	if strings.TrimSpace(resp.ManualPullJobID) == "" {
		t.Fatalf("expected non-empty manual_pull_job_id")
	}
	list, err := st.ListManualPullJobs(ctx, src.ID, model.ListManualPullJobsRequest{
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("list manual pull jobs failed: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 manual pull job, got %d", len(list.Items))
	}
	job := list.Items[0]
	if job.JobID != resp.ManualPullJobID {
		t.Fatalf("expected job_id=%s, got %s", resp.ManualPullJobID, job.JobID)
	}
	if job.Status != "SUCCEEDED" {
		t.Fatalf("expected status SUCCEEDED, got %s", job.Status)
	}
	if job.AcceptedCount != 2 {
		t.Fatalf("expected accepted_count=2, got %d", job.AcceptedCount)
	}
}

func TestDisableSourceWatchEnqueuesSnapshotThenStop(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src := createTestSource(t, st)

	// clear initial start_source command
	pulled, err := st.PullPendingCommands(ctx, model.PullCommandsRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("pull initial commands failed: %v", err)
	}
	for _, cmd := range pulled.Commands {
		if err := st.AckCommand(ctx, model.AckCommandRequest{
			AgentID:   "agent-1",
			CommandID: cmd.ID,
			Success:   true,
		}); err != nil {
			t.Fatalf("ack initial command %d failed: %v", cmd.ID, err)
		}
	}

	updated, baselineQueued, err := st.DisableSourceWatch(ctx, src.ID)
	if err != nil {
		t.Fatalf("disable source watch failed: %v", err)
	}
	if !baselineQueued {
		t.Fatalf("expected baseline snapshot queued")
	}
	if updated.WatchEnabled {
		t.Fatalf("expected watch_enabled=false")
	}

	pulled, err = st.PullPendingCommands(ctx, model.PullCommandsRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("pull commands failed: %v", err)
	}
	if len(pulled.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(pulled.Commands))
	}
	if pulled.Commands[0].Type != model.CommandSnapshotSource {
		t.Fatalf("expected first command snapshot_source, got %s", pulled.Commands[0].Type)
	}
	if pulled.Commands[1].Type != model.CommandStopSource {
		t.Fatalf("expected second command stop_source, got %s", pulled.Commands[1].Type)
	}
}

func TestExpediteTasksByPathsUpdatesExistingPendingTask(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/expedite.txt"
	eventAt := time.Now().UTC().Add(-40 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: eventAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, eventAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parse failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	taskID := tasks[0].ID
	future := time.Now().UTC().Add(1 * time.Hour)
	if err := st.db.WithContext(ctx).Model(&parseTaskEntity{}).Where("id = ?", taskID).Update("next_run_at", future).Error; err != nil {
		t.Fatalf("set future next_run_at failed: %v", err)
	}

	exp, err := st.ExpediteTasksByPaths(ctx, src.ID, model.ExpediteTasksRequest{
		Paths: []string{path},
	})
	if err != nil {
		t.Fatalf("expedite tasks failed: %v", err)
	}
	if exp.UpdatedExistingTaskCount != 1 {
		t.Fatalf("expected updated_existing_task_count=1, got %d", exp.UpdatedExistingTaskCount)
	}
	tasks = loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("expected same single task id=%d, got %+v", taskID, tasks)
	}
	if tasks[0].NextRunAt.After(time.Now().UTC().Add(2 * time.Second)) {
		t.Fatalf("expected next_run_at updated to now, got %v", tasks[0].NextRunAt)
	}
}

func loadDocumentByPath(t *testing.T, st *Store, src model.Source, path string) documentEntity {
	t.Helper()
	var doc documentEntity
	if err := st.db.WithContext(context.Background()).
		Where("tenant_id = ? AND source_id = ? AND source_object_id = ?", src.TenantID, src.ID, path).
		Take(&doc).Error; err != nil {
		t.Fatalf("load document failed: %v", err)
	}
	return doc
}

func loadTasksByDocumentID(t *testing.T, st *Store, documentID int64) []parseTaskEntity {
	t.Helper()
	var tasks []parseTaskEntity
	if err := st.db.WithContext(context.Background()).
		Where("document_id = ?", documentID).
		Order("id ASC").
		Find(&tasks).Error; err != nil {
		t.Fatalf("load tasks failed: %v", err)
	}
	return tasks
}

func findTreeNodeByPath(items []model.TreeNode, path string) (model.TreeNode, bool) {
	for _, item := range items {
		if item.Key == path {
			return item, true
		}
		if len(item.Children) > 0 {
			if found, ok := findTreeNodeByPath(item.Children, path); ok {
				return found, true
			}
		}
	}
	return model.TreeNode{}, false
}

func countTreeNodesByPath(items []model.TreeNode, path string) int {
	count := 0
	for _, item := range items {
		if item.Key == path {
			count++
		}
		if len(item.Children) > 0 {
			count += countTreeNodesByPath(item.Children, path)
		}
	}
	return count
}

type sourceStateMatrixKind string

const (
	sourceStateMatrixLocal sourceStateMatrixKind = "local"
	sourceStateMatrixDrive sourceStateMatrixKind = "drive"
	sourceStateMatrixWiki  sourceStateMatrixKind = "wiki"
)

type sourceStateMatrixChange string

const (
	sourceStateMatrixNew      sourceStateMatrixChange = "new"
	sourceStateMatrixModified sourceStateMatrixChange = "modified"
	sourceStateMatrixDeleted  sourceStateMatrixChange = "deleted"
)

type sourceStateMatrixSync string

const (
	sourceStateMatrixManual    sourceStateMatrixSync = "manual"
	sourceStateMatrixAutomatic sourceStateMatrixSync = "automatic"
)

type sourceStateMatrixCase struct {
	kind       sourceStateMatrixKind
	change     sourceStateMatrixChange
	sync       sourceStateMatrixSync
	sourceRoot string
	objectPath string
	displayKey string
	title      string
	originRef  string
}

func TestSourceDocumentStateMatrix(t *testing.T) {
	cases := make([]sourceStateMatrixCase, 0, 18)
	for _, kind := range []sourceStateMatrixKind{sourceStateMatrixLocal, sourceStateMatrixDrive, sourceStateMatrixWiki} {
		for _, change := range []sourceStateMatrixChange{sourceStateMatrixNew, sourceStateMatrixModified, sourceStateMatrixDeleted} {
			for _, syncMode := range []sourceStateMatrixSync{sourceStateMatrixManual, sourceStateMatrixAutomatic} {
				cases = append(cases, newSourceStateMatrixCase(kind, change, syncMode))
			}
		}
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.kind)+"/"+string(tc.change)+"/"+string(tc.sync), func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			ctx := context.Background()
			src := createSourceStateMatrixSource(t, st, tc)
			seedSourceStateMatrixBaseline(t, st, src, tc)

			observedAt := time.Now().UTC().Add(-5 * time.Minute)
			tree, token := observeSourceStateMatrixChange(t, st, src, tc, observedAt)
			node, ok := findTreeNodeByPath(tree, tc.displayKey)
			if !ok {
				t.Fatalf("missing observed tree node %s in %+v", tc.displayKey, tree)
			}
			expectedUpdate := sourceStateMatrixExpectedUpdate(tc.change)
			if node.UpdateType != expectedUpdate {
				t.Fatalf("expected tree update_type=%s, got %s", expectedUpdate, node.UpdateType)
			}
			if node.SourceState != expectedUpdate {
				t.Fatalf("expected source_state=%s, got %s", expectedUpdate, node.SourceState)
			}
			expectedSyncState := syncStatePending
			if tc.sync == sourceStateMatrixAutomatic {
				expectedSyncState = syncStateScheduled
			}
			if node.SyncState != expectedSyncState {
				t.Fatalf("expected sync_state=%s before sync, got %s", expectedSyncState, node.SyncState)
			}

			resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
				TenantID: src.TenantID,
				Page:     1,
				PageSize: 20,
			})
			if err != nil {
				t.Fatalf("list source documents before sync failed: %v", err)
			}
			listItem, ok := sourceStateMatrixFindDocumentItem(resp.Items, tc.objectPath)
			if tc.change == sourceStateMatrixNew {
				if ok {
					t.Fatalf("expected new document without knowledge base relation to be hidden from document list, got %+v", listItem)
				}
			} else if !ok {
				t.Fatalf("missing document list item for %s in %+v", tc.objectPath, resp.Items)
			}
			if tc.change == sourceStateMatrixNew {
				// Newly discovered files belong in the directory tree until the first sync succeeds.
				if resp.Summary.NewCount != 0 || resp.Summary.PendingPullCount != 0 {
					t.Fatalf("expected hidden new document not to affect document list summary, got %+v", resp.Summary)
				}
			} else if listItem.UpdateType != expectedUpdate || listItem.SourceState != expectedUpdate || listItem.SyncState != expectedSyncState {
				t.Fatalf("expected list state update=%s source=%s sync=%s, got item=%+v", expectedUpdate, expectedUpdate, expectedSyncState, listItem)
			}

			if tc.sync == sourceStateMatrixAutomatic {
				assertSourceStateMatrixAutomaticWaits(t, st, src, tc)
			}

			tasks := triggerSourceStateMatrixSync(t, st, src, tc, token)
			if len(tasks) != 1 {
				t.Fatalf("expected one task, got %d", len(tasks))
			}
			expectedAction := sourceStateMatrixExpectedTaskAction(tc.change)
			if normalizeTaskAction(tasks[0].TaskAction) != expectedAction {
				t.Fatalf("expected task_action=%s, got %s", expectedAction, tasks[0].TaskAction)
			}
			doc := loadDocumentByPath(t, st, src, tc.objectPath)
			if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
				t.Fatalf("mark task succeeded failed: %v", err)
			}
			assertSourceStateMatrixClearedAfterSuccess(t, st, src, tc)
		})
	}
}

func newSourceStateMatrixCase(kind sourceStateMatrixKind, change sourceStateMatrixChange, syncMode sourceStateMatrixSync) sourceStateMatrixCase {
	root := "/tmp/source-state-matrix/" + string(kind) + "/" + string(change) + "/" + string(syncMode)
	tc := sourceStateMatrixCase{
		kind:       kind,
		change:     change,
		sync:       syncMode,
		sourceRoot: root,
		objectPath: filepath.Join(root, "doc.md"),
		displayKey: filepath.Join(root, "doc.md"),
		title:      "doc.md",
	}
	switch kind {
	case sourceStateMatrixDrive:
		mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(root))
		tc.objectPath = filepath.Join(mirrorRoot, "drive-doc.md")
		tc.displayKey = tc.objectPath
		tc.title = "drive-doc.md"
		tc.originRef = "drive_" + string(change) + "_" + string(syncMode)
	case sourceStateMatrixWiki:
		mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(root))
		tc.displayKey = filepath.Join(mirrorRoot, "wiki-page")
		tc.objectPath = filepath.Join(tc.displayKey, "wiki-page.md")
		tc.title = "wiki-page"
		tc.originRef = "wiki_" + string(change) + "_" + string(syncMode)
	}
	return tc
}

func createSourceStateMatrixSource(t *testing.T, st *Store, tc sourceStateMatrixCase) model.Source {
	t.Helper()
	req := model.CreateSourceRequest{
		TenantID:          "tenant-matrix",
		Name:              "matrix-" + string(tc.kind) + "-" + string(tc.change) + "-" + string(tc.sync),
		RootPath:          tc.sourceRoot,
		AgentID:           "agent-" + string(tc.kind) + "-" + string(tc.change) + "-" + string(tc.sync),
		WatchEnabled:      tc.sync == sourceStateMatrixAutomatic,
		IdleWindowSeconds: 10,
		ReconcileSeconds:  3600,
	}
	if tc.sync == sourceStateMatrixAutomatic {
		req.ReconcileSchedule = "daily@02:00:03"
	}
	if tc.kind != sourceStateMatrixLocal {
		req.DefaultOriginType = string(model.OriginTypeCloudSync)
		req.DefaultOriginPlatform = "FEISHU"
	}
	src, err := st.CreateSource(context.Background(), req)
	if err != nil {
		t.Fatalf("create matrix source failed: %v", err)
	}
	if tc.kind != sourceStateMatrixLocal {
		scheduleExpr := "manual"
		if tc.sync == sourceStateMatrixAutomatic {
			scheduleExpr = "daily@02:00:03"
		}
		if _, err := st.UpsertCloudSourceBinding(context.Background(), src.ID, model.UpsertCloudSourceBindingRequest{
			Provider:         "feishu",
			Enabled:          boolPtr(true),
			AuthConnectionID: "conn-" + string(tc.kind) + "-" + string(tc.change) + "-" + string(tc.sync),
			TargetType:       string(tc.kind),
			TargetRef:        tc.originRef,
			ScheduleExpr:     scheduleExpr,
			ScheduleTZ:       defaultScheduleTZ,
		}); err != nil {
			t.Fatalf("create matrix cloud binding failed: %v", err)
		}
	}
	return src
}

func seedSourceStateMatrixBaseline(t *testing.T, st *Store, src model.Source, tc sourceStateMatrixCase) {
	t.Helper()
	if tc.kind != sourceStateMatrixLocal {
		seedSourceStateMatrixCloudObject(t, st, src, tc, false)
	}
	if tc.change == sourceStateMatrixNew {
		return
	}
	now := time.Now().UTC().Add(-10 * time.Minute)
	doc := documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   tc.objectPath,
		DesiredVersionID: "v_base",
		CurrentVersionID: "v_base",
		ParseStatus:      "SUCCEEDED",
		CoreDocumentID:   "core-" + string(tc.kind) + "-" + string(tc.change) + "-" + string(tc.sync),
		OriginType:       firstNonEmpty(src.DefaultOriginType, string(model.OriginTypeLocalFS)),
		OriginPlatform:   firstNonEmpty(src.DefaultOriginPlatform, "LOCAL"),
		OriginRef:        tc.originRef,
		UpdatedAt:        now,
	}
	modAt := now.UTC()
	doc.LastModifiedAt = &modAt
	if err := st.db.WithContext(context.Background()).Create(&doc).Error; err != nil {
		t.Fatalf("seed matrix document failed: %v", err)
	}
	if err := st.db.WithContext(context.Background()).Create(&sourceDocumentStateEntity{
		TenantID:          src.TenantID,
		SourceID:          src.ID,
		ObjectKey:         sourceObjectKey(tc.objectPath, tc.originRef),
		Path:              tc.objectPath,
		Name:              filepath.Base(tc.objectPath),
		SourceExists:      true,
		OriginType:        doc.OriginType,
		OriginPlatform:    doc.OriginPlatform,
		OriginRef:         tc.originRef,
		SourceVersion:     "v_base",
		BaselineVersion:   "v_base",
		SourceState:       sourceStateUnchanged,
		SyncState:         syncStateIdle,
		PendingAction:     pendingActionNone,
		DocumentID:        doc.ID,
		CoreDocumentID:    doc.CoreDocumentID,
		LastDetectedAt:    now,
		LastSyncedAt:      &modAt,
		KnowledgeBaseSeen: true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}).Error; err != nil {
		t.Fatalf("seed matrix source state failed: %v", err)
	}
}

func seedSourceStateMatrixCloudObject(t *testing.T, st *Store, src model.Source, tc sourceStateMatrixCase, deleted bool) {
	t.Helper()
	now := time.Now().UTC()
	kind := "file"
	localRel := strings.TrimPrefix(tc.objectPath, filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))+string(filepath.Separator))
	if tc.kind == sourceStateMatrixWiki {
		kind = "docx"
		localRel = filepath.Join(filepath.Base(tc.displayKey), filepath.Base(tc.objectPath))
	}
	row := cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: tc.originRef,
		ExternalName:     tc.title,
		ExternalKind:     kind,
		LocalRelPath:     localRel,
		LocalAbsPath:     tc.objectPath,
		IsDeleted:        deleted,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if tc.kind == sourceStateMatrixWiki {
		row.ProviderMetaJSON = encodeJSON(map[string]any{"has_child": true})
	}
	if err := st.db.WithContext(context.Background()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_id"}, {Name: "external_object_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"external_name":      row.ExternalName,
			"external_kind":      row.ExternalKind,
			"local_rel_path":     row.LocalRelPath,
			"local_abs_path":     row.LocalAbsPath,
			"is_deleted":         row.IsDeleted,
			"provider_meta_json": row.ProviderMetaJSON,
			"updated_at":         row.UpdatedAt,
		}),
	}).Create(&row).Error; err != nil {
		t.Fatalf("seed matrix cloud object failed: %v", err)
	}
}

func observeSourceStateMatrixChange(t *testing.T, st *Store, src model.Source, tc sourceStateMatrixCase, observedAt time.Time) ([]model.TreeNode, string) {
	t.Helper()
	eventType := "modified"
	if tc.change == sourceStateMatrixDeleted {
		eventType = "deleted"
	}
	events := []model.FileEvent{{
		SourceID:       src.ID,
		EventType:      eventType,
		Path:           tc.objectPath,
		OccurredAt:     observedAt,
		OriginType:     firstNonEmpty(src.DefaultOriginType, string(model.OriginTypeLocalFS)),
		OriginPlatform: firstNonEmpty(src.DefaultOriginPlatform, "LOCAL"),
		OriginRef:      tc.originRef,
	}}
	mutations, err := st.BuildMutationsFromEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("build matrix mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(context.Background(), mutations); err != nil {
		t.Fatalf("apply matrix mutation failed: %v", err)
	}
	if tc.change == sourceStateMatrixDeleted {
		if tc.kind != sourceStateMatrixLocal {
			seedSourceStateMatrixCloudObject(t, st, src, tc, true)
		}
		tree, token, err := st.BuildTreeUpdateState(context.Background(), src.ID, nil, nil)
		if err != nil {
			t.Fatalf("observe matrix deleted state failed: %v", err)
		}
		return tree, token
	}
	if tc.kind != sourceStateMatrixLocal {
		seedSourceStateMatrixCloudObject(t, st, src, tc, false)
	}
	size := int64(10)
	checksum := "rev-new"
	if tc.change == sourceStateMatrixModified {
		size = 20
		checksum = "rev-modified"
	}
	item := model.TreeNode{Title: tc.title, Key: tc.displayKey, IsDir: false}
	stats := map[string]model.TreeFileStat{
		tc.displayKey: {
			Path:     tc.displayKey,
			Size:     size,
			ModTime:  &observedAt,
			Checksum: checksum,
		},
	}
	tree, token, err := st.BuildTreeUpdateState(context.Background(), src.ID, []model.TreeNode{item}, stats)
	if err != nil {
		t.Fatalf("observe matrix state failed: %v", err)
	}
	return tree, token
}

func sourceStateMatrixExpectedUpdate(change sourceStateMatrixChange) string {
	switch change {
	case sourceStateMatrixNew:
		return "NEW"
	case sourceStateMatrixModified:
		return "MODIFIED"
	case sourceStateMatrixDeleted:
		return "DELETED"
	default:
		return "UNCHANGED"
	}
}

func sourceStateMatrixExpectedTaskAction(change sourceStateMatrixChange) string {
	if change == sourceStateMatrixDeleted {
		return taskActionDelete
	}
	if change == sourceStateMatrixModified {
		return taskActionReparse
	}
	return taskActionCreate
}

func sourceStateMatrixFindDocumentItem(items []model.SourceDocumentItem, path string) (model.SourceDocumentItem, bool) {
	for _, item := range items {
		if filepath.Clean(item.Path) == filepath.Clean(path) {
			return item, true
		}
	}
	return model.SourceDocumentItem{}, false
}

func assertSourceStateMatrixAutomaticWaits(t *testing.T, st *Store, src model.Source, tc sourceStateMatrixCase) {
	t.Helper()
	var state sourceDocumentStateEntity
	if err := st.db.WithContext(context.Background()).Where("source_id = ? AND path = ?", src.ID, tc.objectPath).Take(&state).Error; err != nil {
		t.Fatalf("load matrix source state failed: %v", err)
	}
	if state.NextSyncAt == nil {
		t.Fatalf("expected automatic matrix state to have next_sync_at")
	}
	created, err := st.ScheduleDueParses(context.Background(), state.NextSyncAt.Add(-time.Second))
	if err != nil {
		t.Fatalf("schedule before matrix due failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected no task before due, got %d", created)
	}
	var count int64
	if err := st.db.WithContext(context.Background()).Model(&parseTaskEntity{}).Count(&count).Error; err != nil {
		t.Fatalf("count parse tasks failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no parse tasks before due, got %d", count)
	}
}

func triggerSourceStateMatrixSync(t *testing.T, st *Store, src model.Source, tc sourceStateMatrixCase, token string) []parseTaskEntity {
	t.Helper()
	ctx := context.Background()
	if tc.sync == sourceStateMatrixManual {
		resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
			Mode:           "partial",
			Paths:          []string{tc.displayKey},
			SelectionToken: sourceStateMatrixSelectionToken(tc, token),
			TriggerPolicy:  string(model.TriggerPolicyImmediate),
			UpdatedOnly:    false,
		})
		if err != nil {
			t.Fatalf("generate matrix task failed: %v", err)
		}
		if resp.AcceptedCount != 1 {
			t.Fatalf("expected accepted_count=1, got %+v", resp)
		}
	} else {
		var state sourceDocumentStateEntity
		if err := st.db.WithContext(ctx).Where("source_id = ? AND path = ?", src.ID, tc.objectPath).Take(&state).Error; err != nil {
			t.Fatalf("load matrix source state before due failed: %v", err)
		}
		created, err := st.ScheduleDueParses(ctx, state.NextSyncAt.Add(time.Second))
		if err != nil {
			t.Fatalf("schedule matrix due failed: %v", err)
		}
		if created != 1 {
			t.Fatalf("expected created=1 at due, got %d", created)
		}
	}
	doc := loadDocumentByPath(t, st, src, tc.objectPath)
	return loadTasksByDocumentID(t, st, doc.ID)
}

func sourceStateMatrixSelectionToken(tc sourceStateMatrixCase, token string) string {
	if tc.change == sourceStateMatrixDeleted {
		return ""
	}
	return token
}

func assertSourceStateMatrixClearedAfterSuccess(t *testing.T, st *Store, src model.Source, tc sourceStateMatrixCase) {
	t.Helper()
	resp, err := st.ListSourceDocuments(context.Background(), src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list matrix documents after success failed: %v", err)
	}
	if tc.change == sourceStateMatrixDeleted {
		if _, ok := sourceStateMatrixFindDocumentItem(resp.Items, tc.objectPath); ok {
			t.Fatalf("expected deleted matrix item to be hidden after success, got %+v", resp.Items)
		}
		return
	}
	item, ok := sourceStateMatrixFindDocumentItem(resp.Items, tc.objectPath)
	if !ok {
		t.Fatalf("expected matrix item after success")
	}
	if item.UpdateType != "UNCHANGED" || item.SourceState != sourceStateUnchanged {
		t.Fatalf("expected matrix item cleared after success, got %+v", item)
	}
}

func hasTopLevelTreeNode(items []model.TreeNode, path string) bool {
	for _, item := range items {
		if item.Key == path {
			return true
		}
	}
	return false
}

func TestListParseTasksAndStats(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/list-task.txt"
	eventAt := time.Now().UTC().Add(-40 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: eventAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, eventAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}

	listResp, err := st.ListParseTasks(ctx, model.ListParseTasksRequest{
		TenantID: src.TenantID,
		SourceID: src.ID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list parse tasks failed: %v", err)
	}
	if listResp.Total != 1 {
		t.Fatalf("expected total=1, got %d", listResp.Total)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(listResp.Items))
	}
	if listResp.Items[0].SourceObjectID != path {
		t.Fatalf("expected source_object_id=%s, got %s", path, listResp.Items[0].SourceObjectID)
	}
	if listResp.Items[0].Status != "PENDING" {
		t.Fatalf("expected task status PENDING, got %s", listResp.Items[0].Status)
	}

	stats, err := st.CountParseTasksByStatusWithFilter(ctx, src.TenantID, src.ID)
	if err != nil {
		t.Fatalf("count parse tasks by status failed: %v", err)
	}
	if stats["PENDING"] != 1 {
		t.Fatalf("expected PENDING=1, got %d", stats["PENDING"])
	}
}

func TestDeleteSourceCascadesAndStopsWatcher(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/delete-source.txt"
	eventAt := time.Now().UTC().Add(-40 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: eventAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, eventAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one parse task, got %d", len(tasks))
	}
	if err := st.MarkTaskFailed(ctx, tasks[0].ID, "delete source test failure"); err != nil {
		t.Fatalf("mark task failed failed: %v", err)
	}
	if _, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{{Title: "delete-source.txt", Key: path, IsDir: false}}, nil); err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_delete_source",
		ScheduleExpr:     "daily@05:00",
		ScheduleTZ:       "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}
	if _, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"}); err != nil {
		t.Fatalf("trigger cloud sync failed: %v", err)
	}
	if err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []CloudObjectIndexRecord{
		{ExternalObjectID: "obj_delete_source", ExternalPath: "/docs/delete-source.txt", LocalAbsPath: path},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("upsert cloud object index failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.db.WithContext(ctx).Create(&reconcileSnapshotEntity{
		SourceID:    src.ID,
		SnapshotRef: "local://snapshot/delete-source.json",
		FileCount:   1,
		TakenAt:     now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create reconcile snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceBaselineSnapshotEntity{
		SourceID:    src.ID,
		SnapshotRef: "local://baseline/delete-source.json",
		FileCount:   1,
		TakenAt:     now,
		Reason:      "DELETE_SOURCE_TEST",
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create baseline snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&manualPullJobEntity{
		JobID:     "mpj_delete_source",
		TenantID:  src.TenantID,
		SourceID:  src.ID,
		Status:    "SUCCEEDED",
		Mode:      "partial",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create manual pull job failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceDocumentStateEntity{
		TenantID:       src.TenantID,
		SourceID:       src.ID,
		ObjectKey:      "delete-source-state",
		Path:           path,
		Name:           "delete-source.txt",
		SourceExists:   true,
		OriginType:     string(model.OriginTypeLocalFS),
		OriginPlatform: "LOCAL",
		SourceVersion:  "c_delete_source_state",
		SourceState:    sourceStateNew,
		SyncState:      syncStateScheduled,
		PendingAction:  pendingActionCreate,
		LastDetectedAt: now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create source document state failed: %v", err)
	}

	if err := st.DeleteSource(ctx, src.ID); err != nil {
		t.Fatalf("delete source failed: %v", err)
	}
	if _, err := st.GetSource(ctx, src.ID); err == nil {
		t.Fatalf("expected deleted source lookup to fail")
	}

	for name, target := range map[string]any{
		"documents":                  &documentEntity{},
		"source_document_states":     &sourceDocumentStateEntity{},
		"parse_tasks":                &parseTaskEntity{},
		"parse_task_dead_letters":    &parseTaskDeadLetterEntity{},
		"source_file_snapshots":      &sourceFileSnapshotEntity{},
		"source_file_snapshot_items": &sourceFileSnapshotItemEntity{},
		"source_snapshot_relations":  &sourceSnapshotRelationEntity{},
		"source_baseline_snapshots":  &sourceBaselineSnapshotEntity{},
		"reconcile_snapshots":        &reconcileSnapshotEntity{},
		"manual_pull_jobs":           &manualPullJobEntity{},
		"cloud_source_bindings":      &cloudSourceBindingEntity{},
		"cloud_sync_checkpoints":     &cloudSyncCheckpointEntity{},
		"cloud_sync_runs":            &cloudSyncRunEntity{},
		"cloud_object_index":         &cloudObjectIndexEntity{},
	} {
		var count int64
		if err := st.db.WithContext(ctx).Model(target).Count(&count).Error; err != nil {
			t.Fatalf("count %s failed: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("expected %s to be empty after delete, got %d", name, count)
		}
	}

	var commands []agentCommandEntity
	if err := st.db.WithContext(ctx).Order("id ASC").Find(&commands).Error; err != nil {
		t.Fatalf("list agent commands failed: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected only stop_source command after delete, got %+v", commands)
	}
	if commands[0].Type != string(model.CommandStopSource) {
		t.Fatalf("expected stop_source command, got %s", commands[0].Type)
	}
	if !strings.Contains(commands[0].Payload, `"source_id":"`+src.ID+`"`) {
		t.Fatalf("expected stop command payload to reference source_id %s, got %s", src.ID, commands[0].Payload)
	}
}

func TestRetryParseTask(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/retry-task.txt"
	eventAt := time.Now().UTC().Add(-40 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: eventAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, eventAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	taskID := tasks[0].ID
	if err := st.MarkTaskSubmitFailed(ctx, taskID, "mock submit failure"); err != nil {
		t.Fatalf("mark task submit failed failed: %v", err)
	}

	detail, err := st.RetryParseTask(ctx, taskID)
	if err != nil {
		t.Fatalf("retry parse task failed: %v", err)
	}
	if detail.Status != "PENDING" {
		t.Fatalf("expected status PENDING after retry, got %s", detail.Status)
	}
	if detail.ScanOrchestrationStatus != "PENDING" {
		t.Fatalf("expected scan_orchestration_status PENDING, got %s", detail.ScanOrchestrationStatus)
	}
	if detail.RetryCount != 0 {
		t.Fatalf("expected retry_count=0, got %d", detail.RetryCount)
	}
	if detail.LastError != "" {
		t.Fatalf("expected last_error cleared, got %q", detail.LastError)
	}

	doc = loadDocumentByPath(t, st, src, path)
	if doc.ParseStatus != "QUEUED" {
		t.Fatalf("expected document parse_status QUEUED after retry, got %s", doc.ParseStatus)
	}
}

func TestRetryParseTaskRejectsNonSubmitFailed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/retry-reject.txt"
	eventAt := time.Now().UTC().Add(-40 * time.Second)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: eventAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, eventAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	taskID := tasks[0].ID
	if err := st.MarkTaskFailed(ctx, taskID, "mock failure"); err != nil {
		t.Fatalf("mark task failed failed: %v", err)
	}
	if _, err := st.RetryParseTask(ctx, taskID); err == nil {
		t.Fatalf("expected retry to fail for FAILED status")
	}
}

func TestDeleteCloudSourceCleansParsedDocumentsBeforeRecreate(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	rootPath := "/tmp/cloud/delete-recreate"
	now := time.Now().UTC()

	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud-delete",
		Name:                  "cloud source",
		RootPath:              rootPath,
		AgentID:               "agent-cloud-delete",
		DatasetID:             "dataset-old",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_delete_recreate",
		TargetType:       "wiki",
		TargetRef:        "space_old",
		ScheduleExpr:     "manual",
		ScheduleTZ:       "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	doc := documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   rootPath + "/mirror/wiki/old.md",
		CoreDocumentID:   "core_old_doc",
		CurrentVersionID: "rev-old",
		DesiredVersionID: "rev-old",
		LastModifiedAt:   &now,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_old",
		TriggerPolicy:    string(model.TriggerPolicyImmediate),
		UpdatedAt:        now,
	}
	if err := st.db.WithContext(ctx).Create(&doc).Error; err != nil {
		t.Fatalf("create parsed cloud document failed: %v", err)
	}
	task := parseTaskEntity{
		TenantID:                src.TenantID,
		DocumentID:              doc.ID,
		TaskAction:              taskActionCreate,
		TargetVersionID:         "rev-old",
		OriginType:              string(model.OriginTypeCloudSync),
		OriginPlatform:          "FEISHU",
		TriggerPolicy:           string(model.TriggerPolicyImmediate),
		Status:                  "SUCCEEDED",
		CoreDatasetID:           src.DatasetID,
		CoreDocumentID:          "core_old_doc",
		CoreTaskID:              "core_task_old",
		ScanOrchestrationStatus: "SUCCEEDED",
		SubmitAt:                &now,
		NextRunAt:               now,
		StartedAt:               &now,
		FinishedAt:              &now,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := st.db.WithContext(ctx).Create(&task).Error; err != nil {
		t.Fatalf("create parse task failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&parseTaskDeadLetterEntity{
		TaskID:          task.ID,
		TenantID:        src.TenantID,
		DocumentID:      doc.ID,
		TargetVersionID: "rev-old",
		RetryCount:      8,
		OriginType:      string(model.OriginTypeCloudSync),
		OriginPlatform:  "FEISHU",
		TriggerPolicy:   string(model.TriggerPolicyImmediate),
		LastError:       "old failure",
		FailedAt:        now,
		CreatedAt:       now,
	}).Error; err != nil {
		t.Fatalf("create dead letter failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceDocumentStateEntity{
		TenantID:          src.TenantID,
		SourceID:          src.ID,
		ObjectKey:         "node_old",
		Path:              doc.SourceObjectID,
		Name:              "old.md",
		SourceExists:      true,
		OriginType:        string(model.OriginTypeCloudSync),
		OriginPlatform:    "FEISHU",
		OriginRef:         "node_old",
		SourceVersion:     "rev-old",
		BaselineVersion:   "rev-old",
		SourceState:       sourceStateUnchanged,
		SyncState:         syncStateIdle,
		PendingAction:     pendingActionNone,
		DocumentID:        doc.ID,
		CoreDocumentID:    "core_old_doc",
		ActiveTaskID:      task.ID,
		LastDetectedAt:    now,
		LastSyncedAt:      &now,
		KnowledgeBaseSeen: true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}).Error; err != nil {
		t.Fatalf("create source document state failed: %v", err)
	}
	snapshotID := "snapshot_delete_recreate"
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   snapshotID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "committed",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID:     snapshotID,
		Path:           doc.SourceObjectID,
		IsDir:          false,
		SizeBytes:      32,
		ModTime:        &now,
		Checksum:       "rev-old",
		ExternalFileID: "node_old",
	}).Error; err != nil {
		t.Fatalf("create snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: snapshotID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceBaselineSnapshotEntity{
		SourceID:    src.ID,
		SnapshotRef: "cloud://baseline/delete-recreate",
		FileCount:   1,
		TakenAt:     now,
		Reason:      "DELETE_RECREATE_TEST",
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create baseline snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&reconcileSnapshotEntity{
		SourceID:    src.ID,
		SnapshotRef: "cloud://reconcile/delete-recreate",
		FileCount:   1,
		TakenAt:     now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("create reconcile snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&manualPullJobEntity{
		JobID:     "manual_delete_recreate",
		TenantID:  src.TenantID,
		SourceID:  src.ID,
		Status:    "SUCCEEDED",
		Mode:      "partial",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create manual pull job failed: %v", err)
	}
	if _, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"}); err != nil {
		t.Fatalf("trigger cloud sync failed: %v", err)
	}
	if err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []CloudObjectIndexRecord{
		{
			ExternalObjectID: "node_old",
			ExternalPath:     "/wiki/old.md",
			ExternalName:     "old.md",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-old",
			LocalAbsPath:     doc.SourceObjectID,
			Checksum:         "rev-old",
			SizeBytes:        32,
		},
	}, now); err != nil {
		t.Fatalf("upsert cloud object index failed: %v", err)
	}

	if err := st.DeleteSource(ctx, src.ID); err != nil {
		t.Fatalf("delete cloud source failed: %v", err)
	}
	for name, target := range map[string]any{
		"documents":                  &documentEntity{},
		"source_document_states":     &sourceDocumentStateEntity{},
		"parse_tasks":                &parseTaskEntity{},
		"parse_task_dead_letters":    &parseTaskDeadLetterEntity{},
		"source_file_snapshots":      &sourceFileSnapshotEntity{},
		"source_file_snapshot_items": &sourceFileSnapshotItemEntity{},
		"source_snapshot_relations":  &sourceSnapshotRelationEntity{},
		"source_baseline_snapshots":  &sourceBaselineSnapshotEntity{},
		"reconcile_snapshots":        &reconcileSnapshotEntity{},
		"manual_pull_jobs":           &manualPullJobEntity{},
		"cloud_source_bindings":      &cloudSourceBindingEntity{},
		"cloud_sync_checkpoints":     &cloudSyncCheckpointEntity{},
		"cloud_sync_runs":            &cloudSyncRunEntity{},
		"cloud_object_index":         &cloudObjectIndexEntity{},
	} {
		var count int64
		if err := st.db.WithContext(ctx).Model(target).Count(&count).Error; err != nil {
			t.Fatalf("count %s failed: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("expected %s to be empty after cloud source delete, got %d", name, count)
		}
	}

	recreated, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              src.TenantID,
		Name:                  "cloud source recreated",
		RootPath:              rootPath,
		AgentID:               src.AgentID,
		DatasetID:             "dataset-new",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("recreate cloud source failed: %v", err)
	}
	if recreated.ID == src.ID {
		t.Fatalf("expected recreated source to have a new id")
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, recreated.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_delete_recreate",
		TargetType:       "wiki",
		TargetRef:        "space_new",
		ScheduleExpr:     "manual",
		ScheduleTZ:       "Asia/Shanghai",
	}); err != nil {
		t.Fatalf("upsert recreated binding failed: %v", err)
	}
	resp, err := st.ListSourceDocuments(ctx, recreated.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list recreated source documents failed: %v", err)
	}
	if resp.Total != 0 || len(resp.Items) != 0 || resp.Summary.TotalDocumentCount != 0 || resp.Summary.ParsedDocumentCount != 0 {
		t.Fatalf("expected recreated source to have no old documents, got total=%d parsed=%d items=%+v", resp.Total, resp.Summary.ParsedDocumentCount, resp.Items)
	}
}

func TestListSourceDocumentsWithUpdateTypeFilter(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-2 * time.Minute)

	newPath := "/tmp/watch/new-file.txt"
	deletePath := "/tmp/watch/deleted-file.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: newPath, OccurredAt: baseAt.Add(1 * time.Second)},
		{SourceID: src.ID, EventType: "modified", Path: deletePath, OccurredAt: baseAt.Add(2 * time.Second)},
	})
	if err != nil {
		t.Fatalf("build initial mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply initial mutations failed: %v", err)
	}
	deletedDoc := loadDocumentByPath(t, st, src, deletePath)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", deletedDoc.ID).
		Updates(map[string]any{
			"core_document_id":   "core-doc-deleted-file",
			"current_version_id": strings.TrimSpace(deletedDoc.DesiredVersionID),
			"parse_status":       "SUCCEEDED",
		}).Error; err != nil {
		t.Fatalf("seed deleted file as synced document failed: %v", err)
	}
	delMutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "deleted", Path: deletePath, OccurredAt: baseAt.Add(3 * time.Second)},
	})
	if err != nil {
		t.Fatalf("build delete mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, delMutations); err != nil {
		t.Fatalf("apply delete mutation failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected only knowledge-base-backed documents to remain visible, got total %d", resp.Total)
	}
	if resp.Summary.NewCount != 0 {
		t.Fatalf("expected new_count=0 for documents not yet in knowledge base, got %d", resp.Summary.NewCount)
	}
	if resp.Summary.DeletedCount != 1 {
		t.Fatalf("expected pending deleted documents to be counted, got %d", resp.Summary.DeletedCount)
	}
	itemsByPath := map[string]model.SourceDocumentItem{}
	for _, item := range resp.Items {
		itemsByPath[item.Path] = item
	}
	if len(itemsByPath) != 1 {
		t.Fatalf("expected only pending deleted file, got %+v", resp.Items)
	}
	if _, ok := itemsByPath[newPath]; ok {
		t.Fatalf("expected new file without knowledge base relation to be hidden, got %+v", itemsByPath[newPath])
	}
	if itemsByPath[deletePath].UpdateType != "DELETED" {
		t.Fatalf("expected deleted file update_type DELETED, got %+v", itemsByPath[deletePath])
	}

	filtered, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID:   src.TenantID,
		UpdateType: "NEW",
		Page:       1,
		PageSize:   20,
	})
	if err != nil {
		t.Fatalf("list source documents with update_type filter failed: %v", err)
	}
	if filtered.Total != 0 {
		t.Fatalf("expected filtered total=0 for unparsed new file, got %d", filtered.Total)
	}
	if len(filtered.Items) != 0 {
		t.Fatalf("expected no new files in document list, got %+v", filtered.Items)
	}

	deletedFiltered, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID:   src.TenantID,
		UpdateType: "DELETED",
		Page:       1,
		PageSize:   20,
	})
	if err != nil {
		t.Fatalf("list source documents with deleted update_type filter failed: %v", err)
	}
	if deletedFiltered.Total != 1 || len(deletedFiltered.Items) != 1 || deletedFiltered.Items[0].Path != deletePath {
		t.Fatalf("expected only pending deleted file %s, got total=%d items=%+v", deletePath, deletedFiltered.Total, deletedFiltered.Items)
	}
}

func TestListSourceDocumentsSkipsTransientFileRows(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	now := time.Now().UTC()
	rows := []documentEntity{
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   "/tmp/watch/normal.txt",
			DesiredVersionID: "v1",
			CurrentVersionID: "v1",
			CoreDocumentID:   "core-normal",
			ParseStatus:      "SUCCEEDED",
			UpdatedAt:        now,
		},
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   "/tmp/watch/.normal.txt.swp",
			DesiredVersionID: "v2",
			ParseStatus:      "DELETED",
			UpdatedAt:        now,
		},
	}
	if err := st.db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("seed documents failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Path != "/tmp/watch/normal.txt" {
		t.Fatalf("expected only normal document, got %+v", resp.Items)
	}
	if resp.Total != 1 || resp.Summary.TotalDocumentCount != 1 {
		t.Fatalf("expected transient row excluded from totals, total=%d summary=%d", resp.Total, resp.Summary.TotalDocumentCount)
	}
}

func TestListSourceDocumentsUsesLocalSnapshotMetadata(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-local-metadata",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/a.txt"
	modAt := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	syncAt := modAt.Add(2 * time.Minute)
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		DesiredVersionID: "v1",
		CurrentVersionID: "v1",
		LastModifiedAt:   &modAt,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeLocalFS),
		OriginPlatform:   "LOCAL",
		UpdatedAt:        syncAt.Add(30 * time.Second),
	}).Error; err != nil {
		t.Fatalf("create document failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	if err := st.db.WithContext(ctx).Create(&parseTaskEntity{
		TenantID:                src.TenantID,
		DocumentID:              doc.ID,
		TaskAction:              taskActionCreate,
		TargetVersionID:         "v1",
		Status:                  "SUCCEEDED",
		ScanOrchestrationStatus: "SUCCEEDED",
		NextRunAt:               modAt,
		FinishedAt:              &syncAt,
		CreatedAt:               modAt,
		UpdatedAt:               syncAt,
	}).Error; err != nil {
		t.Fatalf("create parse task failed: %v", err)
	}

	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    syncAt,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       path,
		SizeBytes:  1234,
		ModTime:    &modAt,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               syncAt,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.SizeBytes != 1234 {
		t.Fatalf("expected size_bytes=1234, got %d", item.SizeBytes)
	}
	if item.SourceUpdatedAt == nil || !item.SourceUpdatedAt.Equal(modAt) {
		t.Fatalf("expected source_updated_at=%v, got %v", modAt, item.SourceUpdatedAt)
	}
	if item.LastSyncedAt == nil || !item.LastSyncedAt.Equal(syncAt) {
		t.Fatalf("expected last_synced_at=%v, got %v", syncAt, item.LastSyncedAt)
	}
	if resp.Summary.StorageBytes != 1234 {
		t.Fatalf("expected storage_bytes=1234, got %d", resp.Summary.StorageBytes)
	}
}

func TestIngestScanResultsPersistsLocalSnapshotMetadata(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src := createTestSource(t, st)

	path := "/tmp/watch/initial.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	if err := st.IngestScanResults(ctx, model.ReportScanResultsRequest{
		SourceID: src.ID,
		Mode:     "full",
		Records: []model.ScanRecord{
			{
				Path:     path,
				Size:     4096,
				ModTime:  modAt,
				Checksum: "sha256-initial",
			},
		},
	}); err != nil {
		t.Fatalf("ingest scan results failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"current_version_id": strings.TrimSpace(doc.DesiredVersionID),
			"core_document_id":   "core-doc-initial",
			"parse_status":       "SUCCEEDED",
		}).Error; err != nil {
		t.Fatalf("mark ingested document synced failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	if resp.Items[0].SizeBytes != 4096 {
		t.Fatalf("expected size_bytes=4096 from scan metadata, got %d", resp.Items[0].SizeBytes)
	}
	if resp.Summary.StorageBytes != 4096 {
		t.Fatalf("expected storage_bytes=4096 from scan metadata, got %d", resp.Summary.StorageBytes)
	}
	if resp.Items[0].SourceUpdatedAt == nil || !resp.Items[0].SourceUpdatedAt.Equal(modAt) {
		t.Fatalf("expected source_updated_at=%v, got %v", modAt, resp.Items[0].SourceUpdatedAt)
	}
}

func TestIngestScanResultsMergesSnapshotMetadataAcrossBatches(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src := createTestSource(t, st)

	firstPath := "/tmp/watch/a.txt"
	secondPath := "/tmp/watch/b.txt"
	modAt := time.Now().UTC().Add(-3 * time.Minute).Truncate(time.Second)
	if err := st.IngestScanResults(ctx, model.ReportScanResultsRequest{
		SourceID: src.ID,
		Mode:     "full",
		Records: []model.ScanRecord{
			{Path: firstPath, Size: 10, ModTime: modAt},
		},
	}); err != nil {
		t.Fatalf("ingest first scan batch failed: %v", err)
	}
	if err := st.IngestScanResults(ctx, model.ReportScanResultsRequest{
		SourceID: src.ID,
		Mode:     "full",
		Records: []model.ScanRecord{
			{Path: secondPath, Size: 20, ModTime: modAt.Add(time.Second)},
		},
	}); err != nil {
		t.Fatalf("ingest second scan batch failed: %v", err)
	}
	for _, path := range []string{firstPath, secondPath} {
		doc := loadDocumentByPath(t, st, src, path)
		if err := st.db.WithContext(ctx).Model(&documentEntity{}).
			Where("id = ?", doc.ID).
			Updates(map[string]any{
				"current_version_id": strings.TrimSpace(doc.DesiredVersionID),
				"core_document_id":   "core-doc-" + filepath.Base(path),
				"parse_status":       "SUCCEEDED",
			}).Error; err != nil {
			t.Fatalf("mark ingested document %s synced failed: %v", path, err)
		}
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(resp.Items))
	}
	sizesByPath := map[string]int64{}
	for _, item := range resp.Items {
		sizesByPath[item.Path] = item.SizeBytes
	}
	if sizesByPath[firstPath] != 10 || sizesByPath[secondPath] != 20 {
		t.Fatalf("expected merged scan sizes a=10 b=20, got %#v", sizesByPath)
	}
	if resp.Summary.StorageBytes != 30 {
		t.Fatalf("expected storage_bytes=30 across scan batches, got %d", resp.Summary.StorageBytes)
	}
}

func TestListSourceDocumentsUsesFeishuCloudObjectMetadata(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-feishu",
		Name:                  "src-feishu-metadata",
		RootPath:              "/tmp/cloud/feishu",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create feishu source failed: %v", err)
	}

	path := "/tmp/cloud/feishu/docs/spec.txt"
	originRef := "obj_feishu_001"
	sourceUpdatedAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	docLastModifiedAt := sourceUpdatedAt.Add(-time.Hour)
	lastSyncedAt := sourceUpdatedAt.Add(7 * time.Minute)
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		DesiredVersionID: "v1",
		CurrentVersionID: "v1",
		LastModifiedAt:   &docLastModifiedAt,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        originRef,
		UpdatedAt:        lastSyncedAt.Add(time.Minute),
	}).Error; err != nil {
		t.Fatalf("create cloud document failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:           src.ID,
		Provider:           "feishu",
		ExternalObjectID:   originRef,
		ExternalPath:       "/docs/spec.txt",
		ExternalName:       "spec.txt",
		ExternalKind:       "file",
		ExternalVersion:    "rev1",
		ExternalModifiedAt: &sourceUpdatedAt,
		LocalAbsPath:       path,
		SizeBytes:          4567,
		LastSyncedAt:       &lastSyncedAt,
		CreatedAt:          lastSyncedAt,
		UpdatedAt:          lastSyncedAt,
	}).Error; err != nil {
		t.Fatalf("create cloud object index failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.SizeBytes != 4567 {
		t.Fatalf("expected size_bytes=4567, got %d", item.SizeBytes)
	}
	if item.SourceUpdatedAt == nil || !item.SourceUpdatedAt.Equal(sourceUpdatedAt) {
		t.Fatalf("expected source_updated_at=%v, got %v", sourceUpdatedAt, item.SourceUpdatedAt)
	}
	if item.LastSyncedAt == nil || !item.LastSyncedAt.Equal(lastSyncedAt) {
		t.Fatalf("expected last_synced_at=%v, got %v", lastSyncedAt, item.LastSyncedAt)
	}
	if resp.Summary.StorageBytes != 4567 {
		t.Fatalf("expected storage_bytes=4567, got %d", resp.Summary.StorageBytes)
	}
}

func TestListSourceDocumentsUsesCloudIndexSnapshotDiff(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-feishu-diff",
		Name:                  "src-feishu-diff",
		RootPath:              "/tmp/cloud/feishu-diff",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create feishu source failed: %v", err)
	}
	changedPath := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "changed.md")
	deletedPath := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "deleted.md")
	now := time.Now().UTC()
	docs := []documentEntity{
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   changedPath,
			DesiredVersionID: "v1",
			CurrentVersionID: "v1",
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        "obj_changed",
			UpdatedAt:        now,
		},
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   deletedPath,
			DesiredVersionID: "v1",
			CurrentVersionID: "v1",
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        "obj_deleted",
			UpdatedAt:        now,
		},
	}
	if err := st.db.WithContext(ctx).Create(&docs).Error; err != nil {
		t.Fatalf("create cloud documents failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]sourceDocumentStateEntity{
		{
			TenantID:          src.TenantID,
			SourceID:          src.ID,
			ObjectKey:         "obj_changed",
			Path:              changedPath,
			Name:              "changed.md",
			SourceExists:      true,
			KnowledgeBaseSeen: true,
			OriginType:        string(model.OriginTypeCloudSync),
			OriginPlatform:    "FEISHU",
			OriginRef:         "obj_changed",
			SourceVersion:     "v1",
			SourceState:       sourceStateUnchanged,
			SyncState:         syncStateIdle,
			PendingAction:     pendingActionNone,
			DocumentID:        docs[0].ID,
			LastDetectedAt:    now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			TenantID:          src.TenantID,
			SourceID:          src.ID,
			ObjectKey:         "obj_deleted",
			Path:              deletedPath,
			Name:              "deleted.md",
			SourceExists:      true,
			KnowledgeBaseSeen: true,
			OriginType:        string(model.OriginTypeCloudSync),
			OriginPlatform:    "FEISHU",
			OriginRef:         "obj_deleted",
			SourceVersion:     "v1",
			SourceState:       sourceStateUnchanged,
			SyncState:         syncStateIdle,
			PendingAction:     pendingActionNone,
			DocumentID:        docs[1].ID,
			LastDetectedAt:    now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}).Error; err != nil {
		t.Fatalf("create unchanged source states failed: %v", err)
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    2,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]sourceFileSnapshotItemEntity{
		{SnapshotID: committedID, Path: changedPath, SizeBytes: 10, Checksum: "rev-old", ExternalFileID: "obj_changed"},
		{SnapshotID: committedID, Path: deletedPath, SizeBytes: 20, Checksum: "rev-deleted", ExternalFileID: "obj_deleted"},
	}).Error; err != nil {
		t.Fatalf("create committed snapshot items failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "obj_changed",
			ExternalPath:     "changed",
			ExternalName:     "changed",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-new",
			LocalAbsPath:     changedPath,
			Checksum:         "rev-new",
			SizeBytes:        11,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "obj_deleted",
			ExternalPath:     "deleted",
			ExternalName:     "deleted",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-deleted",
			LocalAbsPath:     deletedPath,
			Checksum:         "rev-deleted",
			SizeBytes:        20,
			IsDeleted:        true,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("create cloud object index rows failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	itemsByPath := map[string]model.SourceDocumentItem{}
	for _, item := range resp.Items {
		itemsByPath[item.Path] = item
	}
	if itemsByPath[changedPath].UpdateType != "MODIFIED" {
		t.Fatalf("expected changed cloud doc MODIFIED, got %+v", itemsByPath[changedPath])
	}
	if itemsByPath[deletedPath].UpdateType != "DELETED" {
		t.Fatalf("expected deleted cloud doc DELETED, got %+v", itemsByPath[deletedPath])
	}
	if resp.Summary.ModifiedCount != 1 || resp.Summary.DeletedCount != 1 || resp.Summary.PendingPullCount != 2 {
		t.Fatalf("unexpected summary %+v", resp.Summary)
	}

	modified, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID:   src.TenantID,
		UpdateType: "MODIFIED",
		Page:       1,
		PageSize:   20,
	})
	if err != nil {
		t.Fatalf("list modified documents failed: %v", err)
	}
	if modified.Total != 1 || len(modified.Items) != 1 || modified.Items[0].Path != changedPath {
		t.Fatalf("expected modified filter to return changed doc, got total=%d items=%+v", modified.Total, modified.Items)
	}

	deleted, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID:   src.TenantID,
		UpdateType: "DELETED",
		Page:       1,
		PageSize:   20,
	})
	if err != nil {
		t.Fatalf("list deleted documents failed: %v", err)
	}
	if deleted.Total != 1 || len(deleted.Items) != 1 || deleted.Items[0].Path != deletedPath {
		t.Fatalf("expected deleted filter to return deleted doc, got total=%d items=%+v", deleted.Total, deleted.Items)
	}
}

func TestListSourceDocumentsUsesDeletedCloudIndexWithoutSnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-feishu-deleted-index",
		Name:                  "src-feishu-deleted-index",
		RootPath:              "/tmp/cloud/feishu-deleted-index",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create feishu source failed: %v", err)
	}

	now := time.Now().UTC()
	objectPath := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "wiki", "test2.md")
	doc := documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   objectPath,
		DesiredVersionID: "v1",
		CurrentVersionID: "v1",
		ParseStatus:      "SUCCEEDED",
		CoreDocumentID:   "core-doc-test2",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "obj_test2",
		UpdatedAt:        now,
	}
	if err := st.db.WithContext(ctx).Create(&doc).Error; err != nil {
		t.Fatalf("create cloud document failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceDocumentStateEntity{
		TenantID:          src.TenantID,
		SourceID:          src.ID,
		ObjectKey:         "obj_test2",
		Path:              objectPath,
		Name:              "test2.md",
		SourceExists:      true,
		KnowledgeBaseSeen: true,
		OriginType:        string(model.OriginTypeCloudSync),
		OriginPlatform:    "FEISHU",
		OriginRef:         "obj_test2",
		SourceVersion:     "v1",
		BaselineVersion:   "v1",
		SourceState:       sourceStateUnchanged,
		SyncState:         syncStateIdle,
		PendingAction:     pendingActionNone,
		DocumentID:        doc.ID,
		CoreDocumentID:    doc.CoreDocumentID,
		LastDetectedAt:    now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}).Error; err != nil {
		t.Fatalf("create unchanged source state failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "obj_test2",
		ExternalPath:     "wiki/test2",
		ExternalName:     "test2",
		ExternalKind:     "docx",
		LocalAbsPath:     objectPath,
		LocalRelPath:     "wiki/test2.md",
		Checksum:         "v1",
		SizeBytes:        20,
		IsDeleted:        true,
		LastSyncedAt:     &now,
		ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create deleted cloud object index row failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected one listed document, got %+v", resp.Items)
	}
	if resp.Items[0].UpdateType != "DELETED" || resp.Items[0].HasUpdate == nil || !*resp.Items[0].HasUpdate {
		t.Fatalf("expected deleted cloud object to surface as pending delete, got %+v", resp.Items[0])
	}
	if resp.Summary.DeletedCount != 1 || resp.Summary.PendingPullCount != 1 {
		t.Fatalf("expected deleted summary count, got %+v", resp.Summary)
	}

	deleted, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID:   src.TenantID,
		UpdateType: "DELETED",
		Page:       1,
		PageSize:   20,
	})
	if err != nil {
		t.Fatalf("list deleted documents failed: %v", err)
	}
	if deleted.Total != 1 || len(deleted.Items) != 1 || deleted.Items[0].Path != objectPath {
		t.Fatalf("expected deleted filter to return cloud-index-deleted doc, got total=%d items=%+v", deleted.Total, deleted.Items)
	}
}

func TestListSourceDocumentsUsesCloudIndexAgainstEmptySnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-feishu-empty-snapshot",
		Name:                  "src-feishu-empty-snapshot",
		RootPath:              "/tmp/cloud/feishu-empty-snapshot",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create feishu source failed: %v", err)
	}
	path := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "new.md")
	now := time.Now().UTC()
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		DesiredVersionID: "v1",
		ParseStatus:      "PENDING",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "obj_new",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create cloud document failed: %v", err)
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    0,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create empty committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "obj_new",
		ExternalName:     "new",
		ExternalKind:     "docx",
		ExternalVersion:  "rev-new",
		LocalAbsPath:     path,
		Checksum:         "rev-new",
		SizeBytes:        8,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create cloud object index row failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected cloud doc without knowledge base relation to be hidden, got %+v", resp.Items)
	}
	if resp.Summary.NewCount != 0 || resp.Summary.PendingPullCount != 0 {
		t.Fatalf("unexpected summary %+v", resp.Summary)
	}
}

func TestBuildMutationsFromEventsSkipsTransientFiles(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: "/tmp/watch/normal.txt", OccurredAt: time.Now().UTC()},
		{SourceID: src.ID, EventType: "modified", Path: "/tmp/watch/.normal.txt.swx", OccurredAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if len(mutations) != 1 || mutations[0].SourceObjectID != "/tmp/watch/normal.txt" {
		t.Fatalf("expected only normal mutation, got %+v", mutations)
	}
}

func TestTransientExistingDocumentsDoNotScheduleOrClaimParseTasks(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	nextParseAt := now
	doc := documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   "/tmp/watch/.normal.txt.swp",
		DesiredVersionID: "v1",
		NextParseAt:      &nextParseAt,
		ParseStatus:      "PENDING",
		UpdatedAt:        now,
	}
	if err := st.db.WithContext(ctx).Create(&doc).Error; err != nil {
		t.Fatalf("seed transient document failed: %v", err)
	}

	created, err := st.ScheduleDueParses(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected no tasks for transient document, got %d", created)
	}
	var reloaded documentEntity
	if err := st.db.WithContext(ctx).Take(&reloaded, "id = ?", doc.ID).Error; err != nil {
		t.Fatalf("reload transient document failed: %v", err)
	}
	if reloaded.NextParseAt != nil {
		t.Fatalf("expected transient document next_parse_at cleared, got %v", reloaded.NextParseAt)
	}

	task := parseTaskEntity{
		TenantID:                src.TenantID,
		DocumentID:              doc.ID,
		TaskAction:              taskActionCreate,
		TargetVersionID:         "v1",
		Status:                  "PENDING",
		ScanOrchestrationStatus: "PENDING",
		NextRunAt:               time.Now().UTC().Add(-time.Minute),
		CreatedAt:               time.Now().UTC(),
		UpdatedAt:               time.Now().UTC(),
	}
	if err := st.db.WithContext(ctx).Create(&task).Error; err != nil {
		t.Fatalf("seed transient parse task failed: %v", err)
	}
	claimed, err := st.ClaimDueTasks(ctx, "worker-1", time.Now().UTC(), 10, time.Minute)
	if err != nil {
		t.Fatalf("claim due tasks failed: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("expected no claimed transient task, got %+v", claimed)
	}
	var reloadedTask parseTaskEntity
	if err := st.db.WithContext(ctx).Take(&reloadedTask, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("reload transient task failed: %v", err)
	}
	if reloadedTask.Status != "SUPERSEDED" {
		t.Fatalf("expected transient task superseded, got %s", reloadedTask.Status)
	}
}

func TestListSourceDocumentsHidesNewDocumentUntilKnowledgeBaseSync(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	path := "/tmp/watch/resync-after-failed.txt"
	firstAt := time.Now().UTC().Add(-3 * time.Minute)

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: firstAt},
	})
	if err != nil {
		t.Fatalf("build first mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply first mutation failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, firstAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule first parse failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected first task, got %d", len(tasks))
	}
	if err := st.MarkTaskFailed(ctx, tasks[0].ID, "mock old failure"); err != nil {
		t.Fatalf("mark old task failed failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Update("parse_status", "FAILED").Error; err != nil {
		t.Fatalf("mark document failed failed: %v", err)
	}

	secondAt := firstAt.Add(2 * time.Minute)
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: secondAt},
	})
	if err != nil {
		t.Fatalf("build second mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply second mutation failed: %v", err)
	}
	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents after second mutation failed: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected unsynced new document to be hidden before parse task exists, got %+v", resp.Items)
	}

	created, err := st.ScheduleDueParses(ctx, secondAt.Add(12*time.Second))
	if err != nil {
		t.Fatalf("schedule second parse failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected second parse task to be created, got %d", created)
	}
	resp, err = st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents after second schedule failed: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected unsynced new document to remain hidden after schedule, got %+v", resp.Items)
	}
}

func TestListSourceDocumentsHidesScheduledNewSourceStateWithoutKnowledgeBase(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "scheduled-docs",
		RootPath:          "/tmp/scheduled-docs",
		AgentID:           "agent-1",
		WatchEnabled:      true,
		IdleWindowSeconds: 10,
		ReconcileSchedule: "daily@02:00:03",
	})
	if err != nil {
		t.Fatalf("create scheduled source failed: %v", err)
	}

	path := "/tmp/scheduled-docs/new.md"
	detectedAt := time.Date(2026, 5, 16, 1, 50, 0, 0, time.UTC)
	nextSyncAt := time.Date(2026, 5, 17, 2, 0, 3, 0, time.UTC)
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		DesiredVersionID: "c_rev-new",
		ParseStatus:      "PENDING",
		OriginType:       string(model.OriginTypeLocalFS),
		OriginPlatform:   "LOCAL",
		TriggerPolicy:    string(model.TriggerPolicyIdleWindow),
		NextParseAt:      &nextSyncAt,
		UpdatedAt:        detectedAt,
	}).Error; err != nil {
		t.Fatalf("seed pending document failed: %v", err)
	}
	var doc documentEntity
	if err := st.db.WithContext(ctx).Where("source_id = ? AND source_object_id = ?", src.ID, path).Take(&doc).Error; err != nil {
		t.Fatalf("load seeded document failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceDocumentStateEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		ObjectKey:        path,
		Path:             path,
		Name:             "new.md",
		SourceExists:     true,
		OriginType:       string(model.OriginTypeLocalFS),
		OriginPlatform:   "LOCAL",
		SourceVersion:    "c_rev-new",
		SourceState:      sourceStateNew,
		SyncState:        syncStateScheduled,
		PendingAction:    pendingActionCreate,
		NextSyncAt:       &nextSyncAt,
		DocumentID:       doc.ID,
		LastDetectedAt:   detectedAt,
		SourceModifiedAt: &detectedAt,
		CreatedAt:        detectedAt,
		UpdatedAt:        detectedAt,
	}).Error; err != nil {
		t.Fatalf("seed scheduled source document state failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 0 || resp.Total != 0 {
		t.Fatalf("expected scheduled new document without knowledge base relation to be hidden, total=%d items=%+v", resp.Total, resp.Items)
	}
	if resp.Summary.NewCount != 0 || resp.Summary.PendingPullCount != 0 {
		t.Fatalf("expected hidden new document not to affect document list summary, got %+v", resp.Summary)
	}
}

func TestListSourceDocumentsScheduledUpdateIgnoresStaleSuccessfulTask(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-5 * time.Minute)
	path := "/tmp/watch/stale-success-update.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build initial mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply initial mutation failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule initial parse failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected initial task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark initial task succeeded failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).Where("id = ?", doc.ID).Update("core_document_id", "core-old").Error; err != nil {
		t.Fatalf("seed core document id failed: %v", err)
	}

	updateAt := baseAt.Add(2 * time.Minute)
	nextSyncAt := updateAt.Add(24 * time.Hour)
	if err := st.db.WithContext(ctx).Model(&sourceEntity{}).
		Where("id = ?", src.ID).
		Updates(map[string]any{"reconcile_schedule": "daily@08:03:03"}).Error; err != nil {
		t.Fatalf("mark source scheduled failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"desired_version_id": "v_pending_update",
			"parse_status":       "PENDING",
			"last_modified_at":   &updateAt,
			"next_parse_at":      &nextSyncAt,
			"updated_at":         updateAt,
		}).Error; err != nil {
		t.Fatalf("mark document pending update failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&sourceDocumentStateEntity{}).
		Where("source_id = ? AND object_key = ?", src.ID, path).
		Updates(map[string]any{
			"name":                filepath.Base(path),
			"source_exists":       true,
			"source_version":      "v_pending_update",
			"baseline_version":    tasks[0].TargetVersionID,
			"source_state":        sourceStateModified,
			"sync_state":          syncStateScheduled,
			"pending_action":      pendingActionUpdate,
			"next_sync_at":        &nextSyncAt,
			"document_id":         doc.ID,
			"core_document_id":    "core-old",
			"knowledge_base_seen": true,
			"last_detected_at":    updateAt,
			"source_modified_at":  &updateAt,
			"deleted_at_source":   nil,
			"updated_at":          updateAt,
		}).Error; err != nil {
		t.Fatalf("seed scheduled modified source state failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected one document, got %+v", resp.Items)
	}
	item := resp.Items[0]
	if item.UpdateType != "MODIFIED" || item.SyncState != syncStateScheduled {
		t.Fatalf("expected scheduled modified state, got %+v", item)
	}
	if item.ParseState != "SUCCEEDED" {
		t.Fatalf("expected stale successful task not to look processing, got %s", item.ParseState)
	}
}

func TestGenerateTasksForSourceUpdatedOnly(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-2 * time.Minute)

	unchangedPath := "/tmp/watch/unchanged.txt"
	newPath := "/tmp/watch/new-for-updated-only.txt"

	// Build one unchanged document by creating and marking the parse task as succeeded.
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: unchangedPath, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, unchangedPath)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark task succeeded failed: %v", err)
	}

	treeItems := []model.TreeNode{
		{Title: "unchanged.txt", Key: unchangedPath, IsDir: false},
		{Title: "new-for-updated-only.txt", Key: newPath, IsDir: false},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, treeItems, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected non-empty selection token")
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{unchangedPath, newPath},
		UpdatedOnly:    true,
		SelectionToken: token,
	})
	if err != nil {
		t.Fatalf("generate tasks with updated_only failed: %v", err)
	}
	if resp.IgnoredUnchangedCount != 1 {
		t.Fatalf("expected ignored_unchanged_count=1, got %d", resp.IgnoredUnchangedCount)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected accepted_count=1, got %d", resp.AcceptedCount)
	}
}

func TestGenerateTasksForSourceIgnoresUnchangedSelectionPaths(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-2 * time.Minute)

	unchangedPath := "/tmp/watch/unchanged-selected.txt"
	newPath := "/tmp/watch/new-selected.txt"
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: unchangedPath, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, unchangedPath)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark task succeeded failed: %v", err)
	}

	treeItems := []model.TreeNode{
		{Title: "unchanged-selected.txt", Key: unchangedPath, IsDir: false},
		{Title: "new-selected.txt", Key: newPath, IsDir: false},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, treeItems, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{unchangedPath, newPath},
		UpdatedOnly:    false,
		SelectionToken: token,
	})
	if err != nil {
		t.Fatalf("generate tasks with selection token failed: %v", err)
	}
	if resp.IgnoredUnchangedCount != 1 {
		t.Fatalf("expected ignored_unchanged_count=1, got %d", resp.IgnoredUnchangedCount)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected accepted_count=1, got %d", resp.AcceptedCount)
	}

	unchangedTasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(unchangedTasks) != 1 {
		t.Fatalf("expected unchanged document to keep one task, got %d", len(unchangedTasks))
	}
}

func TestGenerateTasksForSourceSelectionUsesPendingSourceState(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-5 * time.Minute)
	path := "/tmp/watch/selection-state-update.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build initial mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply initial mutation failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule initial parse failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected initial task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark initial task succeeded failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).Where("id = ?", doc.ID).Update("core_document_id", "core-old").Error; err != nil {
		t.Fatalf("seed core document id failed: %v", err)
	}

	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: filepath.Base(path), Key: path, IsDir: false},
	}, nil)
	if err != nil {
		t.Fatalf("build unchanged tree state failed: %v", err)
	}
	updateAt := baseAt.Add(2 * time.Minute)
	nextSyncAt := updateAt.Add(24 * time.Hour)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"desired_version_id": "v_pending_update",
			"parse_status":       "PENDING",
			"last_modified_at":   &updateAt,
			"next_parse_at":      &nextSyncAt,
			"updated_at":         updateAt,
		}).Error; err != nil {
		t.Fatalf("mark document pending update failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&sourceDocumentStateEntity{}).
		Where("source_id = ? AND object_key = ?", src.ID, path).
		Updates(map[string]any{
			"name":                filepath.Base(path),
			"source_exists":       true,
			"source_version":      "v_pending_update",
			"baseline_version":    tasks[0].TargetVersionID,
			"source_state":        sourceStateModified,
			"sync_state":          syncStateScheduled,
			"pending_action":      pendingActionUpdate,
			"next_sync_at":        &nextSyncAt,
			"document_id":         doc.ID,
			"core_document_id":    "core-old",
			"knowledge_base_seen": true,
			"last_detected_at":    updateAt,
			"source_modified_at":  &updateAt,
			"deleted_at_source":   nil,
			"updated_at":          updateAt,
		}).Error; err != nil {
		t.Fatalf("seed scheduled modified source state failed: %v", err)
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		UpdatedOnly:    false,
		SelectionToken: token,
		TriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("generate tasks with source state update failed: %v", err)
	}
	if resp.IgnoredUnchangedCount != 0 || resp.AcceptedCount != 1 {
		t.Fatalf("expected source state update to be accepted, got %+v", resp)
	}
	if _, err := st.ScheduleDueParses(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("schedule source state update failed: %v", err)
	}
	tasks = loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 2 {
		t.Fatalf("expected second task for source state update, got %+v", tasks)
	}
	if normalizeTaskAction(tasks[1].TaskAction) != taskActionReparse {
		t.Fatalf("expected reparse task, got %+v", tasks[1])
	}
}

func TestGenerateTasksForSourceSelectionUsesPendingSourceDeleteState(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-5 * time.Minute)
	path := "/tmp/watch/selection-state-delete.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build initial mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply initial mutation failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(12*time.Second)); err != nil {
		t.Fatalf("schedule initial parse failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected initial task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark initial task succeeded failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).Where("id = ?", doc.ID).Update("core_document_id", "core-delete-old").Error; err != nil {
		t.Fatalf("seed core document id failed: %v", err)
	}

	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: filepath.Base(path), Key: path, IsDir: false},
	}, nil)
	if err != nil {
		t.Fatalf("build unchanged tree state failed: %v", err)
	}
	deleteAt := baseAt.Add(2 * time.Minute)
	nextSyncAt := deleteAt.Add(24 * time.Hour)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"desired_version_id": "d_pending_delete",
			"parse_status":       "DELETED",
			"last_modified_at":   &deleteAt,
			"next_parse_at":      &nextSyncAt,
			"updated_at":         deleteAt,
		}).Error; err != nil {
		t.Fatalf("mark document pending delete failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&sourceDocumentStateEntity{}).
		Where("source_id = ? AND object_key = ?", src.ID, path).
		Updates(map[string]any{
			"name":                filepath.Base(path),
			"source_exists":       false,
			"baseline_version":    tasks[0].TargetVersionID,
			"source_state":        sourceStateDeleted,
			"sync_state":          syncStateScheduled,
			"pending_action":      pendingActionDelete,
			"next_sync_at":        &nextSyncAt,
			"document_id":         doc.ID,
			"core_document_id":    "core-delete-old",
			"knowledge_base_seen": true,
			"last_detected_at":    deleteAt,
			"deleted_at_source":   &deleteAt,
			"updated_at":          deleteAt,
		}).Error; err != nil {
		t.Fatalf("seed scheduled deleted source state failed: %v", err)
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		UpdatedOnly:    false,
		SelectionToken: token,
		TriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("generate tasks with source state delete failed: %v", err)
	}
	if resp.IgnoredUnchangedCount != 0 || resp.AcceptedCount != 1 {
		t.Fatalf("expected source state delete to be accepted, got %+v", resp)
	}
	if _, err := st.ScheduleDueParses(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("schedule source state delete failed: %v", err)
	}
	tasks = loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 2 {
		t.Fatalf("expected second task for source state delete, got %+v", tasks)
	}
	if normalizeTaskAction(tasks[1].TaskAction) != taskActionDelete {
		t.Fatalf("expected delete task, got %+v", tasks[1])
	}
}

func TestGenerateTasksForWatchSourceRecreatesDeletedPathPresentInPreview(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-2 * time.Minute)
	path := "/tmp/watch/recreated.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build create mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply create mutation failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"core_document_id":   "core-doc-recreated",
			"current_version_id": "v_old",
			"parse_status":       "SUCCEEDED",
		}).Error; err != nil {
		t.Fatalf("seed succeeded document failed: %v", err)
	}

	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "deleted", Path: path, OccurredAt: baseAt.Add(time.Minute)},
	})
	if err != nil {
		t.Fatalf("build delete mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply delete mutation failed: %v", err)
	}
	doc = loadDocumentByPath(t, st, src, path)
	if doc.ParseStatus != "DELETED" {
		t.Fatalf("expected document to be marked deleted before preview, got %s", doc.ParseStatus)
	}

	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "recreated.txt", Key: path, IsDir: false},
	}, map[string]model.TreeFileStat{
		path: {Path: path, Size: 12},
	})
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
		TriggerPolicy:  "IMMEDIATE",
	})
	if err != nil {
		t.Fatalf("generate tasks failed: %v", err)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected accepted_count=1, got %d", resp.AcceptedCount)
	}
	doc = loadDocumentByPath(t, st, src, path)
	if doc.ParseStatus != "QUEUED" {
		t.Fatalf("expected recreated path to be queued immediately, got %s", doc.ParseStatus)
	}
	if !strings.HasPrefix(doc.DesiredVersionID, "v_") {
		t.Fatalf("expected recreated path desired version to be content version, got %s", doc.DesiredVersionID)
	}
	if doc.CurrentVersionID != "" || doc.CoreDocumentID != "" {
		t.Fatalf("expected recreated path to clear old core identity, got current=%q core=%q", doc.CurrentVersionID, doc.CoreDocumentID)
	}
}

func TestGenerateTasksForWatchSourceDeletesPathMissingFromPreview(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-2 * time.Minute)
	deletedPath := "/tmp/watch/deleted-in-source.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: deletedPath, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build create mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply create mutation failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, deletedPath)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"core_document_id":   "core-doc-deleted-in-source",
			"current_version_id": "v_old",
			"parse_status":       "SUCCEEDED",
		}).Error; err != nil {
		t.Fatalf("seed succeeded document failed: %v", err)
	}

	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "deleted", Path: deletedPath, OccurredAt: baseAt.Add(time.Minute)},
	})
	if err != nil {
		t.Fatalf("build delete mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply delete mutation failed: %v", err)
	}
	doc = loadDocumentByPath(t, st, src, deletedPath)
	if doc.ParseStatus != "DELETED" {
		t.Fatalf("expected document to be marked deleted before preview, got %s", doc.ParseStatus)
	}
	listBeforeSync, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list documents before delete sync failed: %v", err)
	}
	if listBeforeSync.Total != 1 || len(listBeforeSync.Items) != 1 || listBeforeSync.Items[0].Path != deletedPath {
		t.Fatalf("expected pending deleted document to remain visible before sync, total=%d items=%+v", listBeforeSync.Total, listBeforeSync.Items)
	}
	if listBeforeSync.Items[0].UpdateType != "DELETED" {
		t.Fatalf("expected pending deleted update_type DELETED, got %s", listBeforeSync.Items[0].UpdateType)
	}

	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, nil, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}
	node, ok := findTreeNodeByPath(tree, deletedPath)
	if !ok {
		t.Fatalf("expected deleted document to be present in tree")
	}
	if node.UpdateType != "DELETED" {
		t.Fatalf("expected deleted tree node update_type DELETED, got %s", node.UpdateType)
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{deletedPath},
		SelectionToken: token,
		TriggerPolicy:  "IMMEDIATE",
	})
	if err != nil {
		t.Fatalf("generate tasks for deleted path failed: %v", err)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected accepted_count=1, got %d", resp.AcceptedCount)
	}

	if _, err := st.ScheduleDueParses(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("schedule delete parse failed: %v", err)
	}
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 delete task, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[0].TaskAction) != taskActionDelete {
		t.Fatalf("expected delete task action, got %s", tasks[0].TaskAction)
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark delete task succeeded failed: %v", err)
	}
	listAfterSync, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list documents after delete sync failed: %v", err)
	}
	if listAfterSync.Total != 0 || len(listAfterSync.Items) != 0 {
		t.Fatalf("expected deleted document to be hidden after sync, total=%d items=%+v", listAfterSync.Total, listAfterSync.Items)
	}
	treeAfterSync, _, err := st.BuildTreeUpdateState(ctx, src.ID, nil, nil)
	if err != nil {
		t.Fatalf("build tree after delete sync failed: %v", err)
	}
	if _, ok := findTreeNodeByPath(treeAfterSync, deletedPath); ok {
		t.Fatalf("expected deleted document to disappear from tree after sync")
	}
}

func TestAutomaticWatchDeleteWaitsForReconcileSchedule(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "scheduled-watch",
		RootPath:          "/tmp/scheduled-watch",
		AgentID:           "agent-1",
		WatchEnabled:      true,
		IdleWindowSeconds: 10,
		ReconcileSeconds:  3600,
	})
	if err != nil {
		t.Fatalf("create scheduled watch source failed: %v", err)
	}
	baseAt := time.Now().UTC().Add(-2 * time.Minute)
	deletedPath := "/tmp/scheduled-watch/deleted-later.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: deletedPath, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build create mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply create mutation failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, deletedPath)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"core_document_id":   "core-doc-scheduled-delete",
			"current_version_id": "v_old",
			"parse_status":       "SUCCEEDED",
		}).Error; err != nil {
		t.Fatalf("seed succeeded document failed: %v", err)
	}

	deletedAt := baseAt.Add(time.Minute)
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "deleted", Path: deletedPath, OccurredAt: deletedAt},
	})
	if err != nil {
		t.Fatalf("build delete mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply delete mutation failed: %v", err)
	}
	doc = loadDocumentByPath(t, st, src, deletedPath)
	if doc.ParseStatus != "DELETED" {
		t.Fatalf("expected document to be marked deleted, got %s", doc.ParseStatus)
	}
	if doc.NextParseAt == nil {
		t.Fatalf("expected automatic delete to wait for next reconcile")
	}
	expectedNext := deletedAt.Add(time.Duration(src.ReconcileSeconds) * time.Second)
	if !doc.NextParseAt.Equal(expectedNext) {
		t.Fatalf("expected next_parse_at=%v, got %v", expectedNext, doc.NextParseAt)
	}
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "deleted", Path: deletedPath, OccurredAt: expectedNext.Add(time.Second)},
	})
	if err != nil {
		t.Fatalf("build repeated delete mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply repeated delete mutation failed: %v", err)
	}
	doc = loadDocumentByPath(t, st, src, deletedPath)
	if doc.NextParseAt == nil || !doc.NextParseAt.Equal(expectedNext) {
		t.Fatalf("expected repeated automatic delete to keep earliest next_parse_at=%v, got %v", expectedNext, doc.NextParseAt)
	}

	created, err := st.ScheduleDueParses(ctx, deletedAt.Add(30*time.Second))
	if err != nil {
		t.Fatalf("schedule before reconcile failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected no delete task before reconcile schedule, got %d", created)
	}
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks before reconcile schedule, got %+v", tasks)
	}
	listBeforeSchedule, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list documents before schedule failed: %v", err)
	}
	if listBeforeSchedule.Total != 1 || len(listBeforeSchedule.Items) != 1 || listBeforeSchedule.Items[0].Path != deletedPath {
		t.Fatalf("expected pending deleted document to remain visible before schedule, total=%d items=%+v", listBeforeSchedule.Total, listBeforeSchedule.Items)
	}

	created, err = st.ScheduleDueParses(ctx, expectedNext.Add(time.Second))
	if err != nil {
		t.Fatalf("schedule at reconcile failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected one delete task at reconcile schedule, got %d", created)
	}
	tasks = loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one task after reconcile schedule, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[0].TaskAction) != taskActionDelete {
		t.Fatalf("expected delete task action, got %s", tasks[0].TaskAction)
	}
	taskTarget := tasks[0].TargetVersionID
	mutations, err = st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "deleted", Path: deletedPath, OccurredAt: expectedNext.Add(2 * time.Second)},
	})
	if err != nil {
		t.Fatalf("build post-schedule delete mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply post-schedule delete mutation failed: %v", err)
	}
	doc = loadDocumentByPath(t, st, src, deletedPath)
	if doc.NextParseAt != nil {
		t.Fatalf("expected queued delete task to keep next_parse_at nil, got %v", doc.NextParseAt)
	}
	if doc.DesiredVersionID != taskTarget {
		t.Fatalf("expected queued delete task target %q to remain desired version, got %q", taskTarget, doc.DesiredVersionID)
	}
	matched, err := st.DesiredVersionMatches(ctx, doc.ID, taskTarget)
	if err != nil {
		t.Fatalf("check desired version failed: %v", err)
	}
	if !matched {
		t.Fatalf("expected queued delete task to remain executable")
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestCloudBindingUsesStoreDefaultScheduleTZ(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	st.SetDefaultCloudScheduleTZ("UTC")
	src := createTestSource(t, st)
	ctx := context.Background()

	binding, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_tz_default_001",
		ScheduleExpr:     "daily@05:00",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}
	if binding.ScheduleTZ != "UTC" {
		t.Fatalf("expected schedule_tz to fallback to store default UTC, got %s", binding.ScheduleTZ)
	}
}

func TestCloudBindingAcceptsScheduleExprWithSeconds(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	binding, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_seconds_001",
		ScheduleExpr:     "daily@02:00:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding with seconds failed: %v", err)
	}
	if binding.ScheduleExpr != "daily@02:00:00" {
		t.Fatalf("expected schedule_expr to be preserved, got %s", binding.ScheduleExpr)
	}
	if binding.NextSyncAt == nil {
		t.Fatalf("expected next_sync_at to be computed")
	}
}

func TestReconcileScheduleExprPreservesSeconds(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	st.SetDefaultCloudScheduleTZ("UTC")

	next := st.computeNextSyncAt("daily@02:00:03", "UTC", time.Date(2026, 5, 16, 1, 59, 59, 0, time.UTC))
	if next == nil {
		t.Fatalf("expected next_sync_at")
	}
	expected := time.Date(2026, 5, 16, 2, 0, 3, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected next_sync_at=%v, got %v", expected, next)
	}
	if _, _, _, _, err := parseReconcileScheduleExpr("daily@02:00:99"); err == nil {
		t.Fatalf("expected invalid seconds to fail")
	}
}

func TestCloudBindingManualScheduleClearsNextSync(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	scheduled, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_manual_schedule_001",
		ScheduleExpr:     "daily@05:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert scheduled cloud binding failed: %v", err)
	}
	if scheduled.NextSyncAt == nil {
		t.Fatalf("expected scheduled binding next_sync_at to be set")
	}

	manual, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_manual_schedule_001",
		ScheduleExpr:     "manual",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert manual cloud binding failed: %v", err)
	}
	if manual.ScheduleExpr != "" {
		t.Fatalf("expected manual schedule_expr to be cleared, got %q", manual.ScheduleExpr)
	}
	if manual.NextSyncAt != nil {
		t.Fatalf("expected manual next_sync_at to be nil, got %v", manual.NextSyncAt)
	}
}

func TestCloudBindingExistingManualScheduleCanBeUpdated(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := st.db.WithContext(ctx).Create(&cloudSourceBindingEntity{
		SourceID:              src.ID,
		TenantID:              src.TenantID,
		Provider:              "feishu",
		Enabled:               true,
		Status:                "ACTIVE",
		AuthConnectionID:      "conn_existing_manual_001",
		ScheduleExpr:          "manual",
		ScheduleTZ:            "Asia/Shanghai",
		ReconcileAfterSync:    true,
		ReconcileDelayMinutes: 10,
		CreatedAt:             now,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatalf("seed manual cloud binding failed: %v", err)
	}

	binding, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_existing_manual_002",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding with existing manual schedule failed: %v", err)
	}
	if binding.ScheduleExpr != "" {
		t.Fatalf("expected existing manual schedule_expr to be cleared, got %q", binding.ScheduleExpr)
	}
	if binding.NextSyncAt != nil {
		t.Fatalf("expected existing manual next_sync_at to be nil, got %v", binding.NextSyncAt)
	}
	if binding.AuthConnectionID != "conn_existing_manual_002" {
		t.Fatalf("expected auth connection to update, got %q", binding.AuthConnectionID)
	}
}

func TestCloudBindingUpsertAndTriggerSyncRun(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	binding, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:              "feishu",
		Enabled:               boolPtr(true),
		AuthConnectionID:      "conn_feishu_001",
		TargetType:            "wiki_space",
		TargetRef:             "wikcn_test",
		ScheduleExpr:          "daily@05:00",
		ScheduleTZ:            "Asia/Shanghai",
		ReconcileAfterSync:    boolPtr(true),
		ReconcileDelayMinutes: 10,
		IncludePatterns:       []string{"*.md", "*.docx"},
		ExcludePatterns:       []string{"*.tmp"},
		MaxObjectSizeBytes:    1024 * 1024,
		ProviderOptions: map[string]any{
			"space_name": "test-space",
		},
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}
	if binding.Provider != "feishu" {
		t.Fatalf("expected provider=feishu, got %s", binding.Provider)
	}
	if binding.AuthConnectionID != "conn_feishu_001" {
		t.Fatalf("expected auth_connection_id=conn_feishu_001, got %s", binding.AuthConnectionID)
	}
	if binding.NextSyncAt == nil {
		t.Fatalf("expected next_sync_at to be set")
	}

	run, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"})
	if err != nil {
		t.Fatalf("trigger cloud sync failed: %v", err)
	}
	if strings.TrimSpace(run.RunID) == "" {
		t.Fatalf("expected non-empty run_id")
	}
	if run.Status != "RUNNING" {
		t.Fatalf("expected run status RUNNING, got %s", run.Status)
	}

	runs, err := st.ListCloudSyncRuns(ctx, src.ID, 20)
	if err != nil {
		t.Fatalf("list cloud sync runs failed: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 cloud sync run, got %d", len(runs))
	}
	if runs[0].RunID != run.RunID {
		t.Fatalf("expected run_id=%s, got %s", run.RunID, runs[0].RunID)
	}
}

func TestTriggerCloudSyncWithManualPaths(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	_, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_feishu_manual_paths_001",
		ScheduleExpr:     "daily@05:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	run, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{
		TriggerType: "manual",
		Paths: []string{
			filepath.Join(src.RootPath, "mirror", "docs", "a.md"),
			filepath.Join(src.RootPath, "mirror", "docs", "a.md"), // duplicate
		},
	})
	if err != nil {
		t.Fatalf("trigger cloud sync with manual paths failed: %v", err)
	}
	if len(run.RequestedPaths) != 1 {
		t.Fatalf("expected 1 normalized requested path, got %d (%v)", len(run.RequestedPaths), run.RequestedPaths)
	}
	if want := filepath.Join(src.RootPath, "mirror", "docs", "a.md"); run.RequestedPaths[0] != want {
		t.Fatalf("expected requested path %s, got %s", want, run.RequestedPaths[0])
	}

	_, err = st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{
		TriggerType: "scheduled",
		Paths:       []string{filepath.Join(src.RootPath, "mirror", "docs", "b.md")},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "only supported when trigger_type is manual") {
		t.Fatalf("expected scheduled+paths to fail, got %v", err)
	}

	_, err = st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{
		TriggerType: "manual",
		Paths:       []string{"/tmp/not-under-mirror/docs/c.md"},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "paths must be inside cloud mirror root") {
		t.Fatalf("expected invalid manual paths to fail, got %v", err)
	}
}

func TestTriggerCloudSyncDoesNotAdvanceLastSyncAt(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	_, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_lastsync_001",
		ScheduleExpr:     "daily@01:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	run, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"})
	if err != nil {
		t.Fatalf("trigger cloud sync failed: %v", err)
	}

	var checkpoint cloudSyncCheckpointEntity
	if err := st.db.WithContext(ctx).Take(&checkpoint, "source_id = ?", src.ID).Error; err != nil {
		t.Fatalf("load checkpoint failed: %v", err)
	}
	if checkpoint.LastSyncAt != nil {
		t.Fatalf("expected last_sync_at to stay nil before run finishes, got %v", checkpoint.LastSyncAt)
	}
	if strings.TrimSpace(checkpoint.LastRunID) != run.RunID {
		t.Fatalf("expected last_run_id=%s, got %s", run.RunID, checkpoint.LastRunID)
	}
}

func TestTriggerCloudSyncRejectsDisabledBinding(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	_, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(false),
		AuthConnectionID: "conn_disabled_001",
		ScheduleExpr:     "daily@01:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	_, err = st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"})
	if err == nil {
		t.Fatalf("expected trigger cloud sync to fail for disabled binding")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}

	var runCount int64
	if err := st.db.WithContext(ctx).Model(&cloudSyncRunEntity{}).
		Where("source_id = ?", src.ID).
		Count(&runCount).Error; err != nil {
		t.Fatalf("count cloud sync runs failed: %v", err)
	}
	if runCount != 0 {
		t.Fatalf("expected no cloud sync run rows, got %d", runCount)
	}
}

func TestBuildCloudTreeByPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud",
		RootPath:              "/tmp/cloud-mirror/src-cloud",
		AgentID:               "agent-1",
		WatchEnabled:          true,
		IdleWindowSeconds:     10,
		DefaultOriginType:     "CLOUD_SYNC",
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	rows := []cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "fld_docs",
			ExternalName:     "docs",
			ExternalKind:     "folder",
			LocalRelPath:     "docs",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud/mirror/docs",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "doc_a",
			ExternalName:     "a.md",
			ExternalKind:     "file",
			LocalRelPath:     "docs/a.md",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud/mirror/docs/a.md",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "fld_sub",
			ExternalName:     "sub",
			ExternalKind:     "folder",
			LocalRelPath:     "docs/sub",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud/mirror/docs/sub",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "doc_b",
			ExternalName:     "b.md",
			ExternalKind:     "file",
			LocalRelPath:     "docs/sub/b.md",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud/mirror/docs/sub/b.md",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "doc_readme",
			ExternalName:     "readme.md",
			ExternalKind:     "file",
			LocalRelPath:     "readme.md",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud/mirror/readme.md",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}
	if err := st.db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("seed cloud_object_index failed: %v", err)
	}

	items, err := st.BuildCloudTreeByPath(ctx, src.ID, "/tmp/cloud-mirror/src-cloud/mirror/docs", 2, true)
	if err != nil {
		t.Fatalf("build cloud tree failed: %v", err)
	}
	nodeA, ok := findTreeNodeByPath(items, "/tmp/cloud-mirror/src-cloud/mirror/docs/a.md")
	if !ok {
		t.Fatalf("expected node a.md")
	}
	if nodeA.IsDir {
		t.Fatalf("expected a.md to be file node")
	}
	if nodeA.ExternalFileID != "doc_a" {
		t.Fatalf("expected external_file_id=doc_a, got %s", nodeA.ExternalFileID)
	}
	nodeSub, ok := findTreeNodeByPath(items, "/tmp/cloud-mirror/src-cloud/mirror/docs/sub")
	if !ok || !nodeSub.IsDir {
		t.Fatalf("expected docs/sub directory node")
	}
	if _, ok := findTreeNodeByPath(items, "/tmp/cloud-mirror/src-cloud/mirror/readme.md"); ok {
		t.Fatalf("unexpected readme.md in docs subtree")
	}
	driveDocPath := "/tmp/cloud-mirror/src-cloud/mirror/docs/report/report.md"
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "doc_report",
		ExternalName:     "report.md",
		ExternalKind:     "docx",
		ProviderMetaJSON: encodeJSON(map[string]any{"type": "docx"}),
		LocalRelPath:     "docs/report/report.md",
		LocalAbsPath:     driveDocPath,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("seed drive docx cloud_object_index failed: %v", err)
	}
	itemsWithDriveDoc, err := st.BuildCloudTreeByPath(ctx, src.ID, "/tmp/cloud-mirror/src-cloud/mirror/docs", 3, true)
	if err != nil {
		t.Fatalf("build cloud tree with drive docx failed: %v", err)
	}
	if _, ok := findTreeNodeByPath(itemsWithDriveDoc, driveDocPath); !ok {
		t.Fatalf("expected drive docx path %s to stay visible", driveDocPath)
	}
	if foldedDriveDoc, ok := findTreeNodeByPath(itemsWithDriveDoc, "/tmp/cloud-mirror/src-cloud/mirror/docs/report"); ok && !foldedDriveDoc.IsDir {
		t.Fatalf("did not expect drive docx to fold into page node: %+v", foldedDriveDoc)
	}

	itemsNoFiles, err := st.BuildCloudTreeByPath(ctx, src.ID, "/tmp/cloud-mirror/src-cloud/mirror/docs", 1, false)
	if err != nil {
		t.Fatalf("build cloud tree without files failed: %v", err)
	}
	if _, ok := findTreeNodeByPath(itemsNoFiles, "/tmp/cloud-mirror/src-cloud/mirror/docs/a.md"); ok {
		t.Fatalf("did not expect file node when include_files=false")
	}
	if _, ok := findTreeNodeByPath(itemsNoFiles, "/tmp/cloud-mirror/src-cloud/mirror/docs/sub/b.md"); ok {
		t.Fatalf("did not expect depth>1 node when max_depth=1")
	}

	_, err = st.BuildCloudTreeByPath(ctx, src.ID, "/tmp/cloud-mirror/src-cloud/mirror/not-found", 2, true)
	if !errors.Is(err, ErrTreePathInvalid) {
		t.Fatalf("expected ErrTreePathInvalid, got %v", err)
	}
}

func TestBuildTreeUpdateStateCloudDeletedNodesUseMirrorScope(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-delete-tree",
		RootPath:              "/tmp/cloud-mirror/src-cloud-delete-tree",
		AgentID:               "agent-1",
		WatchEnabled:          true,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	folderPath := filepath.Join(mirrorRoot, "folder-1")
	testPath := filepath.Join(folderPath, "test1.md")
	siblingPath := filepath.Join(mirrorRoot, "handpull-feishu-drive-2.md")
	deletedRootFile := filepath.Join(mirrorRoot, "handtouch-feishu-drive-1.md")
	deletedChildFile := filepath.Join(folderPath, "deleted-child.md")
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    4,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]sourceFileSnapshotItemEntity{
		{SnapshotID: committedID, Path: testPath, SizeBytes: 10},
		{SnapshotID: committedID, Path: siblingPath, SizeBytes: 20},
		{SnapshotID: committedID, Path: deletedRootFile, SizeBytes: 30},
		{SnapshotID: committedID, Path: deletedChildFile, SizeBytes: 40},
	}).Error; err != nil {
		t.Fatalf("create committed snapshot items failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	treeItems := []model.TreeNode{
		{
			Title:    "folder-1",
			Key:      folderPath,
			IsDir:    true,
			Children: []model.TreeNode{{Title: "test1", Key: testPath, IsDir: false}},
		},
		{Title: "handpull-feishu-drive-2", Key: siblingPath, IsDir: false},
	}
	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, treeItems, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}
	for _, leaked := range []string{filepath.Clean(src.RootPath), mirrorRoot} {
		if _, ok := findTreeNodeByPath(tree, leaked); ok {
			t.Fatalf("did not expect physical root %s to be rendered as a tree node: %+v", leaked, tree)
		}
	}
	if !hasTopLevelTreeNode(tree, deletedRootFile) {
		t.Fatalf("expected deleted root file %s to be inserted at top level, got %+v", deletedRootFile, tree)
	}
	deletedNode, ok := findTreeNodeByPath(tree, deletedRootFile)
	if !ok || deletedNode.UpdateType != "DELETED" {
		t.Fatalf("expected root deleted node update_type DELETED, got %+v", deletedNode)
	}
	folderNode, ok := findTreeNodeByPath(tree, folderPath)
	if !ok {
		t.Fatalf("missing folder node %s", folderPath)
	}
	if folderNode.UpdateType != "" && folderNode.UpdateType != "UNCHANGED" {
		t.Fatalf("expected folder with deleted child to remain unchanged, got %+v", folderNode)
	}
	if folderNode.HasUpdate != nil && *folderNode.HasUpdate {
		t.Fatalf("expected folder with deleted child not to be marked as updated, got %+v", folderNode)
	}
	if !hasTopLevelTreeNode(folderNode.Children, deletedChildFile) {
		t.Fatalf("expected deleted child file %s under folder, got %+v", deletedChildFile, folderNode.Children)
	}

	treeOnlyFolder, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{treeItems[0]}, nil)
	if err != nil {
		t.Fatalf("build single-folder tree update state failed: %v", err)
	}
	if !hasTopLevelTreeNode(treeOnlyFolder, deletedRootFile) {
		t.Fatalf("expected root deleted file %s to remain in root scope when only one folder is listed, got %+v", deletedRootFile, treeOnlyFolder)
	}
}

func TestBuildCloudTreeByPathWikiPageWithChildren(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki",
		AgentID:               "agent-1",
		WatchEnabled:          true,
		IdleWindowSeconds:     10,
		DefaultOriginType:     "CLOUD_SYNC",
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	rows := []cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_test2",
			ExternalName:     "test2",
			ExternalKind:     "docx",
			ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
			LocalRelPath:     "test2/test2.md",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test2.md",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_test222",
			ExternalParentID: "node_test2",
			ExternalName:     "test222",
			ExternalKind:     "docx",
			LocalRelPath:     "test2/test222.md",
			LocalAbsPath:     "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test222.md",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}
	if err := st.db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("seed cloud_object_index failed: %v", err)
	}

	items, err := st.BuildCloudTreeByPath(ctx, src.ID, "/tmp/cloud-mirror/src-cloud-wiki/mirror", 8, true)
	if err != nil {
		t.Fatalf("build cloud tree failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one top-level wiki node, got %+v", items)
	}
	root := items[0]
	if root.IsDir || root.Title != "test2" || root.Key != "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2" || root.ExternalFileID != "node_test2" {
		t.Fatalf("expected single selectable test2 wiki page, got %+v", root)
	}
	if _, ok := findTreeNodeByPath(root.Children, "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test2.md"); ok {
		t.Fatalf("did not expect mirrored parent page file as a visible child")
	}
	if _, ok := findTreeNodeByPath(root.Children, "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test222.md"); !ok {
		t.Fatalf("expected child wiki page")
	}
	child, _ := findTreeNodeByPath(root.Children, "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test222.md")
	if child.Title != "test222" {
		t.Fatalf("expected child page title to preserve source title, got %q", child.Title)
	}

	treeItems := []model.TreeNode{root}
	stats := map[string]model.TreeFileStat{
		"/tmp/cloud-mirror/src-cloud-wiki/mirror/test2": {
			Path:     "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2",
			Checksum: "rev-test2",
		},
		"/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test222.md": {
			Path:     "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test222.md",
			Checksum: "rev-test222",
		},
	}
	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, treeItems, stats)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}
	if len(tree) != 1 || tree[0].UpdateType != "NEW" {
		t.Fatalf("expected parent wiki node to be marked new, got %+v", tree)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Paths:          []string{"/tmp/cloud-mirror/src-cloud-wiki/mirror/test2"},
		SelectionToken: token,
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate tasks failed: %v", err)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected one accepted parent wiki task, got %+v", resp)
	}
	var doc documentEntity
	if err := st.db.WithContext(ctx).
		Where("source_id = ? AND source_object_id = ?", src.ID, "/tmp/cloud-mirror/src-cloud-wiki/mirror/test2/test2.md").
		Take(&doc).Error; err != nil {
		t.Fatalf("expected task to target mirrored parent page file: %v", err)
	}
}

func TestManualFeishuWikiDeletedAfterCloudSyncUsesDocumentState(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-delete",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-delete",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	wikiDisplayPath := filepath.Join(mirrorRoot, "test2")
	wikiObjectPath := filepath.Join(wikiDisplayPath, "test2.md")
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "node_test2",
		ExternalName:     "test2",
		ExternalKind:     "docx",
		ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
		LocalRelPath:     "test2/test2.md",
		LocalAbsPath:     wikiObjectPath,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}

	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       wikiDisplayPath,
		SizeBytes:  100,
		Checksum:   "rev-old",
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "test2", Key: wikiDisplayPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		wikiDisplayPath: {Path: wikiDisplayPath, Size: 100, Checksum: "rev-old"},
	})
	if err != nil {
		t.Fatalf("build unchanged wiki preview failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}
	node, ok := findTreeNodeByPath(tree, wikiDisplayPath)
	if !ok {
		t.Fatalf("expected wiki display node in tree")
	}
	if node.UpdateType != "UNCHANGED" {
		t.Fatalf("expected initial wiki preview unchanged, got %s", node.UpdateType)
	}

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{
			SourceID:       src.ID,
			EventType:      "deleted",
			Path:           wikiObjectPath,
			OccurredAt:     now.Add(-time.Minute),
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			OriginRef:      "node_test2",
		},
	})
	if err != nil {
		t.Fatalf("build cloud delete mutation failed: %v", err)
	}
	for i := range mutations {
		mutations[i].ManualSync = true
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply cloud delete mutation failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, wikiObjectPath)
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"core_document_id":   "core-doc-wiki-test2",
			"current_version_id": "v_old",
		}).Error; err != nil {
		t.Fatalf("seed deleted wiki core identity failed: %v", err)
	}

	treeAfterDelete, _, err := st.BuildTreeUpdateState(ctx, src.ID, nil, nil)
	if err != nil {
		t.Fatalf("build tree update state after delete failed: %v", err)
	}
	deletedNode, ok := findTreeNodeByPath(treeAfterDelete, wikiDisplayPath)
	if !ok {
		t.Fatalf("expected deleted wiki display path to be shown after cloud delete, got %+v", treeAfterDelete)
	}
	if deletedNode.UpdateType != "DELETED" {
		t.Fatalf("expected deleted wiki node update_type DELETED, got %+v", deletedNode)
	}
	if deletedNode.Title != "test2" {
		t.Fatalf("expected deleted wiki node title to use source title, got %q", deletedNode.Title)
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{wikiDisplayPath},
		SelectionToken: token,
		TriggerPolicy:  string(model.TriggerPolicyImmediate),
		UpdatedOnly:    false,
	})
	if err != nil {
		t.Fatalf("generate tasks for deleted wiki display path failed: %v", err)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected one accepted wiki delete task, got %+v", resp)
	}
	doc = loadDocumentByPath(t, st, src, wikiObjectPath)
	if doc.ParseStatus != "DELETED" {
		t.Fatalf("expected wiki document to remain DELETED, got %s", doc.ParseStatus)
	}
	if _, err := st.ScheduleDueParses(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("schedule wiki delete parse failed: %v", err)
	}
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one wiki delete task, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[0].TaskAction) != taskActionDelete {
		t.Fatalf("expected wiki delete task action, got %s", tasks[0].TaskAction)
	}
}

func TestBuildTreeUpdateStateDeletedWikiLeafUsesExternalTitle(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-deleted-title",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-deleted-title",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	objectPath := filepath.Join(mirrorRoot, "zhouqi-feishu-wiki", "test2.md")
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "node_test2",
		ExternalName:     "test2",
		ExternalKind:     "docx",
		ExternalPath:     "zhouqi-feishu-wiki/test2",
		LocalRelPath:     "zhouqi-feishu-wiki/test2.md",
		LocalAbsPath:     objectPath,
		IsDeleted:        true,
		ProviderMetaJSON: encodeJSON(map[string]any{
			"has_child":  false,
			"node_token": "node_test2",
			"obj_type":   "docx",
			"space_id":   "space_1",
		}),
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed deleted cloud object index failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   objectPath,
		CoreDocumentID:   "core-doc-test2",
		CurrentVersionID: "v_old",
		DesiredVersionID: "v_old",
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_test2",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("seed existing wiki document failed: %v", err)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, nil, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	deletedNode, ok := findTreeNodeByPath(tree, objectPath)
	if !ok {
		t.Fatalf("expected deleted wiki file node, got %+v", tree)
	}
	if deletedNode.UpdateType != "DELETED" {
		t.Fatalf("expected deleted wiki node update_type DELETED, got %+v", deletedNode)
	}
	if deletedNode.Title != "test2" {
		t.Fatalf("expected deleted wiki node title to use source title, got %q", deletedNode.Title)
	}
}

func TestBuildTreeUpdateStateWikiSettledPageUsesObjectPathBaseline(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-settled",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-settled",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Minute)
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	displayPath := filepath.Join(mirrorRoot, "test4", "11111")
	objectPath := filepath.Join(displayPath, "11111.md")
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "node_11111",
		ExternalName:     "11111",
		ExternalKind:     "docx",
		ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
		LocalRelPath:     "test4/11111/11111.md",
		LocalAbsPath:     objectPath,
		Checksum:         "rev-1",
		SizeBytes:        14,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("seed wiki cloud object failed: %v", err)
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       objectPath,
		IsDir:      false,
		SizeBytes:  14,
		Checksum:   "rev-1",
		ModTime:    &now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   objectPath,
		CoreDocumentID:   "core-wiki-11111",
		DesiredVersionID: "rev-1",
		CurrentVersionID: "rev-1",
		LastModifiedAt:   &now,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_11111",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create synced wiki document failed: %v", err)
	}

	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "11111", Key: displayPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		displayPath: {Path: displayPath, Size: 14, Checksum: "rev-1", ModTime: &now},
	})
	if err != nil {
		t.Fatalf("build wiki tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, displayPath)
	if !ok {
		t.Fatalf("missing wiki display node in %+v", tree)
	}
	if node.UpdateType != "UNCHANGED" {
		t.Fatalf("expected synced wiki page unchanged, got %+v", node)
	}
	payload, ok, err := decodeReadOnlySelectionToken(token, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("decode selection token failed ok=%v err=%v", ok, err)
	}
	if payload.Diff[displayPath] != "UNCHANGED" {
		t.Fatalf("expected selection token keyed by display path unchanged, got %+v", payload.Diff)
	}
	pullResp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{displayPath},
		SelectionToken: token,
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate unchanged wiki pull failed: %v", err)
	}
	if pullResp.AcceptedCount != 0 || pullResp.IgnoredUnchangedCount != 1 {
		t.Fatalf("expected unchanged wiki pull ignored, got %+v", pullResp)
	}
	treeAfterPull, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "11111", Key: displayPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		displayPath: {Path: displayPath, Size: 14, Checksum: "rev-1", ModTime: &now},
	})
	if err != nil {
		t.Fatalf("build wiki tree after ignored pull failed: %v", err)
	}
	nodeAfterPull, ok := findTreeNodeByPath(treeAfterPull, displayPath)
	if !ok {
		t.Fatalf("missing wiki display node after pull in %+v", treeAfterPull)
	}
	if nodeAfterPull.UpdateType != "UNCHANGED" {
		t.Fatalf("expected synced wiki page unchanged after ignored pull, got %+v", nodeAfterPull)
	}
}

func TestBuildTreeUpdateStateWikiModifiedPageUsesObjectPathBaseline(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-modified",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-modified",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Minute)
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	displayPath := filepath.Join(mirrorRoot, "test4", "11111")
	objectPath := filepath.Join(displayPath, "11111.md")
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "node_11111",
		ExternalName:     "11111",
		ExternalKind:     "docx",
		ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
		LocalRelPath:     "test4/11111/11111.md",
		LocalAbsPath:     objectPath,
		Checksum:         "rev-2",
		SizeBytes:        18,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("seed wiki cloud object failed: %v", err)
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       objectPath,
		IsDir:      false,
		SizeBytes:  14,
		Checksum:   "rev-1",
		ModTime:    &now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   objectPath,
		CoreDocumentID:   "core-wiki-11111",
		DesiredVersionID: "rev-1",
		CurrentVersionID: "rev-1",
		LastModifiedAt:   &now,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_11111",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create synced wiki document failed: %v", err)
	}

	changedAt := now.Add(time.Minute)
	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "11111", Key: displayPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		displayPath: {Path: displayPath, Size: 18, Checksum: "rev-2", ModTime: &changedAt},
	})
	if err != nil {
		t.Fatalf("build wiki tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, displayPath)
	if !ok {
		t.Fatalf("missing wiki display node in %+v", tree)
	}
	if node.UpdateType != "MODIFIED" {
		t.Fatalf("expected synced wiki page modified, got %+v", node)
	}
	payload, ok, err := decodeReadOnlySelectionToken(token, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("decode selection token failed ok=%v err=%v", ok, err)
	}
	if payload.Diff[displayPath] != "MODIFIED" {
		t.Fatalf("expected selection token keyed by display path modified, got %+v", payload.Diff)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{displayPath},
		SelectionToken: token,
		TriggerPolicy:  string(model.TriggerPolicyImmediate),
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate modified wiki pull failed: %v", err)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected modified wiki pull accepted, got %+v", resp)
	}
	doc := loadDocumentByPath(t, st, src, objectPath)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 || normalizeTaskAction(tasks[0].TaskAction) != taskActionReparse {
		t.Fatalf("expected one wiki reparse task, got %+v", tasks)
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark modified wiki task succeeded failed: %v", err)
	}
	treeAfterSuccess, tokenAfterSuccess, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "11111", Key: displayPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		displayPath: {Path: displayPath, Size: 18, Checksum: "rev-2", ModTime: &changedAt},
	})
	if err != nil {
		t.Fatalf("build wiki tree state after success failed: %v", err)
	}
	nodeAfterSuccess, ok := findTreeNodeByPath(treeAfterSuccess, displayPath)
	if !ok {
		t.Fatalf("missing wiki display node after success in %+v", treeAfterSuccess)
	}
	if nodeAfterSuccess.UpdateType != "UNCHANGED" {
		t.Fatalf("expected wiki page unchanged after parse success committed snapshot, got %+v", nodeAfterSuccess)
	}
	payloadAfterSuccess, ok, err := decodeReadOnlySelectionToken(tokenAfterSuccess, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("decode selection token after success failed ok=%v err=%v", ok, err)
	}
	if payloadAfterSuccess.Diff[displayPath] != "UNCHANGED" {
		t.Fatalf("expected selection token after success keyed by display path unchanged, got %+v", payloadAfterSuccess.Diff)
	}
	var relation sourceSnapshotRelationEntity
	if err := st.db.WithContext(ctx).Take(&relation, "source_id = ?", src.ID).Error; err != nil {
		t.Fatalf("load snapshot relation after success failed: %v", err)
	}
	itemsByPath, err := st.snapshotItemsByPath(ctx, relation.LastCommittedSnapshotID)
	if err != nil {
		t.Fatalf("load committed snapshot items after success failed: %v", err)
	}
	item, ok := itemsByPath[objectPath]
	if !ok {
		t.Fatalf("expected committed snapshot item for object path %s, got %+v", objectPath, itemsByPath)
	}
	if item.SizeBytes != 18 || item.Checksum != "rev-2" || item.ExternalFileID != "node_11111" {
		t.Fatalf("expected committed snapshot advanced to rev-2, got %+v", item)
	}
}

func TestBuildTreeUpdateStateWikiDeletedLeafUsesDisplayPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-leaf-delete",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-leaf-delete",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	parentPath := filepath.Join(mirrorRoot, "test2")
	leafPath := filepath.Join(parentPath, "q we q w.md")
	if err := st.db.WithContext(ctx).Create(&[]cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_test2",
			ExternalName:     "test2",
			ExternalKind:     "docx",
			ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
			LocalRelPath:     "test2/test2.md",
			LocalAbsPath:     filepath.Join(parentPath, "test2.md"),
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_leaf",
			ExternalParentID: "node_test2",
			ExternalName:     "q we q w",
			ExternalKind:     "docx",
			LocalRelPath:     "test2/q we q w.md",
			LocalAbsPath:     leafPath,
			IsDeleted:        true,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceDocumentStateEntity{
		SourceID:          src.ID,
		TenantID:          src.TenantID,
		ObjectKey:         "node_leaf",
		Path:              leafPath,
		Name:              "q we q w.md",
		SourceExists:      false,
		KnowledgeBaseSeen: true,
		SourceState:       sourceStateDeleted,
		SyncState:         syncStateScheduled,
		PendingAction:     pendingActionDelete,
		NextSyncAt:        &now,
		LastDetectedAt:    now,
		OriginType:        string(model.OriginTypeCloudSync),
		OriginPlatform:    "FEISHU",
		OriginRef:         "node_leaf",
		CreatedAt:         now,
		UpdatedAt:         now,
	}).Error; err != nil {
		t.Fatalf("seed deleted source document state failed: %v", err)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "test2", Key: parentPath, IsDir: false, Children: []model.TreeNode{
			{Title: "test2222222", Key: filepath.Join(parentPath, "test2222222.md"), IsDir: false},
		}},
	}, map[string]model.TreeFileStat{
		parentPath: {Path: parentPath, Checksum: "rev-parent"},
		filepath.Join(parentPath, "test2222222.md"): {Path: filepath.Join(parentPath, "test2222222.md"), Checksum: "rev-child"},
	})
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	root, ok := findTreeNodeByPath(tree, parentPath)
	if !ok {
		t.Fatalf("missing parent wiki node in %+v", tree)
	}
	deletedLeaf, ok := findTreeNodeByPath(root.Children, leafPath)
	if !ok {
		t.Fatalf("expected deleted leaf to remain visible under parent, got %+v", root.Children)
	}
	if deletedLeaf.UpdateType != "DELETED" || deletedLeaf.SourceState != sourceStateDeleted {
		t.Fatalf("expected deleted leaf state, got %+v", deletedLeaf)
	}
}

func TestBuildTreeUpdateStateDriveSettledFileUsesObjectPathBaseline(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-drive-settled",
		RootPath:              "/tmp/cloud-mirror/src-cloud-drive-settled",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Minute)
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	objectPath := filepath.Join(mirrorRoot, "test4", "drive-doc.md")
	if err := st.db.WithContext(ctx).Create(&cloudObjectIndexEntity{
		SourceID:         src.ID,
		Provider:         "feishu",
		ExternalObjectID: "drive_doc_1",
		ExternalName:     "drive-doc.md",
		ExternalKind:     "file",
		LocalRelPath:     "test4/drive-doc.md",
		LocalAbsPath:     objectPath,
		Checksum:         "drive-rev-1",
		SizeBytes:        14,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("seed drive cloud object failed: %v", err)
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       objectPath,
		IsDir:      false,
		SizeBytes:  14,
		Checksum:   "drive-rev-1",
		ModTime:    &now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   objectPath,
		CoreDocumentID:   "core-drive-doc",
		DesiredVersionID: "drive-rev-1",
		CurrentVersionID: "drive-rev-1",
		LastModifiedAt:   &now,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "drive_doc_1",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create synced drive document failed: %v", err)
	}

	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "drive-doc.md", Key: objectPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		objectPath: {Path: objectPath, Size: 14, Checksum: "drive-rev-1", ModTime: &now},
	})
	if err != nil {
		t.Fatalf("build drive tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, objectPath)
	if !ok {
		t.Fatalf("missing drive node in %+v", tree)
	}
	if node.UpdateType != "UNCHANGED" {
		t.Fatalf("expected synced drive file unchanged, got %+v", node)
	}
	pullResp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{objectPath},
		SelectionToken: token,
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate unchanged drive pull failed: %v", err)
	}
	if pullResp.AcceptedCount != 0 || pullResp.IgnoredUnchangedCount != 1 {
		t.Fatalf("expected unchanged drive pull ignored, got %+v", pullResp)
	}
}

func TestBuildTreeUpdateStateDriveDeletedCloudFileRemainsSelectable(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-drive-delete",
		RootPath:              "/tmp/cloud-mirror/src-cloud-drive-delete",
		AgentID:               "agent-1",
		WatchEnabled:          true,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-time.Minute)
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	currentPath := filepath.Join(mirrorRoot, "folder", "current.md")
	deletedPath := filepath.Join(mirrorRoot, "folder", "deleted.md")
	if err := st.db.WithContext(ctx).Create(&[]cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "drive_current",
			ExternalName:     "current.md",
			ExternalKind:     "file",
			LocalRelPath:     "folder/current.md",
			LocalAbsPath:     currentPath,
			Checksum:         "rev-current",
			SizeBytes:        10,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "drive_deleted",
			ExternalName:     "deleted.md",
			ExternalKind:     "file",
			LocalRelPath:     "folder/deleted.md",
			LocalAbsPath:     deletedPath,
			Checksum:         "rev-deleted",
			SizeBytes:        9,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("seed drive cloud object index failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]documentEntity{
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   currentPath,
			CoreDocumentID:   "core-current",
			DesiredVersionID: "rev-current",
			CurrentVersionID: "rev-current",
			LastModifiedAt:   &now,
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        "drive_current",
			UpdatedAt:        now,
		},
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   deletedPath,
			CoreDocumentID:   "core-deleted",
			DesiredVersionID: "rev-deleted",
			CurrentVersionID: "rev-deleted",
			LastModifiedAt:   &now,
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        "drive_deleted",
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("seed drive documents failed: %v", err)
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    2,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]sourceFileSnapshotItemEntity{
		{SnapshotID: committedID, Path: currentPath, SizeBytes: 10, Checksum: "rev-current"},
		{SnapshotID: committedID, Path: deletedPath, SizeBytes: 9, Checksum: "rev-deleted"},
	}).Error; err != nil {
		t.Fatalf("create committed snapshot items failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{{
		Title:    "folder",
		Key:      filepath.Join(mirrorRoot, "folder"),
		IsDir:    true,
		Children: []model.TreeNode{{Title: "current.md", Key: currentPath, IsDir: false}},
	}}, map[string]model.TreeFileStat{
		currentPath: {Path: currentPath, Size: 10, Checksum: "rev-current"},
	})
	if err != nil {
		t.Fatalf("build drive tree state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}
	deletedNode, ok := findTreeNodeByPath(tree, deletedPath)
	if !ok {
		t.Fatalf("expected deleted drive file %s to remain visible, got %+v", deletedPath, tree)
	}
	if deletedNode.UpdateType != "DELETED" {
		t.Fatalf("expected deleted drive file update_type DELETED, got %+v", deletedNode)
	}
	folderNode, ok := findTreeNodeByPath(tree, filepath.Join(mirrorRoot, "folder"))
	if !ok {
		t.Fatalf("expected folder node, got %+v", tree)
	}
	if folderNode.UpdateType != "" && folderNode.UpdateType != "UNCHANGED" {
		t.Fatalf("expected folder with deleted file to remain unchanged, got %+v", folderNode)
	}
	if folderNode.HasUpdate != nil && *folderNode.HasUpdate {
		t.Fatalf("expected folder with deleted file not to be marked as updated, got %+v", folderNode)
	}
	if deletedNode.Selectable == nil || !*deletedNode.Selectable {
		t.Fatalf("expected deleted drive file selectable, got %+v", deletedNode)
	}
	pullResp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{deletedPath},
		SelectionToken: token,
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate deleted drive pull failed: %v", err)
	}
	if pullResp.AcceptedCount != 1 {
		t.Fatalf("expected deleted drive pull accepted, got %+v", pullResp)
	}
}

func TestBuildTreeUpdateStateDriveDeletedCloudFileWithoutSnapshotUsesDocuments(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-drive-delete-no-snapshot",
		RootPath:              "/tmp/cloud-mirror/src-cloud-drive-delete-no-snapshot",
		AgentID:               "agent-1",
		WatchEnabled:          true,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-time.Minute)
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	currentPath := filepath.Join(mirrorRoot, "folder-1", "q we q we.md")
	deletedPath := filepath.Join(mirrorRoot, "folder-1", "test1.md")
	if err := st.db.WithContext(ctx).Create(&[]cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "drive_current",
			ExternalName:     "q we q we",
			ExternalKind:     "docx",
			LocalRelPath:     "folder-1/q we q we.md",
			LocalAbsPath:     currentPath,
			Checksum:         "rev-current",
			SizeBytes:        43,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "drive_deleted",
			ExternalName:     "test1",
			ExternalKind:     "docx",
			LocalRelPath:     "folder-1/test1.md",
			LocalAbsPath:     deletedPath,
			Checksum:         "rev-deleted",
			SizeBytes:        24,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("seed drive cloud object index failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]documentEntity{
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   currentPath,
			CoreDocumentID:   "core-current",
			DesiredVersionID: "rev-current",
			CurrentVersionID: "rev-current",
			LastModifiedAt:   &now,
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        "drive_current",
			UpdatedAt:        now,
		},
		{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   deletedPath,
			CoreDocumentID:   "core-deleted",
			DesiredVersionID: "rev-deleted",
			CurrentVersionID: "rev-deleted",
			LastModifiedAt:   &now,
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        "drive_deleted",
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("seed drive documents failed: %v", err)
	}

	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{{
		Title:    "folder-1",
		Key:      filepath.Join(mirrorRoot, "folder-1"),
		IsDir:    true,
		Children: []model.TreeNode{{Title: "q we q we", Key: currentPath, IsDir: false}},
	}}, map[string]model.TreeFileStat{
		currentPath: {Path: currentPath, Size: 43, Checksum: "rev-current"},
	})
	if err != nil {
		t.Fatalf("build drive tree state without snapshot failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected selection token")
	}
	deletedNode, ok := findTreeNodeByPath(tree, deletedPath)
	if !ok {
		t.Fatalf("expected documents fallback to render deleted drive file %s, got %+v", deletedPath, tree)
	}
	if deletedNode.UpdateType != "DELETED" {
		t.Fatalf("expected deleted drive file update_type DELETED, got %+v", deletedNode)
	}
	if deletedNode.Selectable == nil || !*deletedNode.Selectable {
		t.Fatalf("expected deleted drive file selectable, got %+v", deletedNode)
	}
}

func TestBuildTreeUpdateStateMergesDeletedWikiChildIntoExistingPageNode(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-merge-deleted",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-merge-deleted",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	rootPath := filepath.Join(mirrorRoot, "zhouqi-feishu-wiki")
	existingChildPath := filepath.Join(rootPath, "test1")
	deletedChildPath := filepath.Join(rootPath, "test3.md")
	if err := st.db.WithContext(ctx).Create(&sourceDocumentStateEntity{
		SourceID:          src.ID,
		TenantID:          src.TenantID,
		ObjectKey:         "wiki-deleted-child",
		Path:              deletedChildPath,
		Name:              "test3.md",
		IsDir:             false,
		SourceExists:      false,
		KnowledgeBaseSeen: true,
		SourceState:       sourceStateDeleted,
		SyncState:         syncStateScheduled,
		PendingAction:     pendingActionDelete,
		NextSyncAt:        &now,
		LastDetectedAt:    now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}).Error; err != nil {
		t.Fatalf("seed deleted source document state failed: %v", err)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{
			Title: "zhouqi-feishu-wiki",
			Key:   rootPath,
			IsDir: false,
			Children: []model.TreeNode{
				{Title: "test1", Key: existingChildPath, IsDir: false},
			},
		},
	}, map[string]model.TreeFileStat{
		existingChildPath: {Path: existingChildPath, Checksum: "rev-test1"},
	})
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if len(tree) != 1 {
		t.Fatalf("expected one merged top-level wiki node, got %+v", tree)
	}
	root := tree[0]
	if root.Key != rootPath {
		t.Fatalf("expected root path %s, got %+v", rootPath, root)
	}
	if countTreeNodesByPath(tree, rootPath) != 1 {
		t.Fatalf("expected one root node after merging deleted child, got %+v", tree)
	}
	deletedChild, ok := findTreeNodeByPath(root.Children, deletedChildPath)
	if !ok {
		t.Fatalf("expected deleted child under existing root, got %+v", root.Children)
	}
	if deletedChild.UpdateType != "DELETED" || deletedChild.SourceState != sourceStateDeleted || deletedChild.SyncState != syncStateScheduled {
		t.Fatalf("expected deleted child state to be preserved, got %+v", deletedChild)
	}
}

func TestBuildTreeUpdateStateMergesSnapshotDeletedWikiChildIntoExistingPageNode(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-wiki-snapshot-child-delete",
		RootPath:              "/tmp/cloud-mirror/src-cloud-wiki-snapshot-child-delete",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	rootPath := filepath.Join(mirrorRoot, "test4")
	parentPath := filepath.Join(rootPath, "11111")
	deletedChildPath := filepath.Join(parentPath, "33333.md")
	leafPath := filepath.Join(rootPath, "55555.md")
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    4,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]sourceFileSnapshotItemEntity{
		{SnapshotID: committedID, Path: rootPath, SizeBytes: 6, Checksum: "rev-root", ExternalFileID: "node_test4"},
		{SnapshotID: committedID, Path: parentPath, SizeBytes: 29, Checksum: "rev-parent", ExternalFileID: "node_11111"},
		{SnapshotID: committedID, Path: deletedChildPath, SizeBytes: 50, Checksum: "rev-deleted", ExternalFileID: "node_33333"},
		{SnapshotID: committedID, Path: leafPath, SizeBytes: 17, Checksum: "rev-leaf", ExternalFileID: "node_55555"},
	}).Error; err != nil {
		t.Fatalf("create committed snapshot items failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]cloudObjectIndexEntity{
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_test4",
			ExternalName:     "test4",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-root",
			LocalAbsPath:     filepath.Join(rootPath, "test4.md"),
			Checksum:         "rev-root",
			SizeBytes:        6,
			ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_11111",
			ExternalParentID: "node_test4",
			ExternalName:     "11111",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-parent",
			LocalAbsPath:     filepath.Join(parentPath, "11111.md"),
			Checksum:         "rev-parent",
			SizeBytes:        29,
			ProviderMetaJSON: encodeJSON(map[string]any{"has_child": true}),
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_33333",
			ExternalParentID: "node_11111",
			ExternalName:     "33333",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-deleted",
			LocalAbsPath:     deletedChildPath,
			Checksum:         "rev-deleted",
			SizeBytes:        50,
			IsDeleted:        true,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			SourceID:         src.ID,
			Provider:         "feishu",
			ExternalObjectID: "node_55555",
			ExternalParentID: "node_test4",
			ExternalName:     "55555",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-leaf",
			LocalAbsPath:     leafPath,
			Checksum:         "rev-leaf",
			SizeBytes:        17,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}).Error; err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{
			Title:      "test4",
			Key:        rootPath,
			IsDir:      false,
			UpdateType: "UNCHANGED",
			Children: []model.TreeNode{
				{Title: "11111", Key: parentPath, IsDir: false, UpdateType: "UNCHANGED"},
				{Title: "55555", Key: leafPath, IsDir: false, UpdateType: "UNCHANGED"},
			},
		},
	}, map[string]model.TreeFileStat{
		rootPath:   {Path: rootPath, Checksum: "rev-root"},
		parentPath: {Path: parentPath, Checksum: "rev-parent"},
		leafPath:   {Path: leafPath, Checksum: "rev-leaf"},
	})
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if countTreeNodesByPath(tree, parentPath) != 1 {
		t.Fatalf("expected one 11111 node after merging deleted child, got %+v", tree)
	}
	parent, ok := findTreeNodeByPath(tree, parentPath)
	if !ok {
		t.Fatalf("expected parent wiki page %s, got %+v", parentPath, tree)
	}
	if parent.UpdateType != "UNCHANGED" {
		t.Fatalf("expected parent wiki page to remain unchanged, got %+v", parent)
	}
	deletedChild, ok := findTreeNodeByPath(parent.Children, deletedChildPath)
	if !ok {
		t.Fatalf("expected deleted child under existing parent, got %+v", parent.Children)
	}
	if deletedChild.UpdateType != "DELETED" {
		t.Fatalf("expected deleted child update_type DELETED, got %+v", deletedChild)
	}
}

func TestBuildTreeUpdateStateDoesNotMutateCloudSourceDocumentStates(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-observe-key",
		RootPath:              "/tmp/cloud-mirror/src-cloud-observe-key",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	objectPath := filepath.Join(mirrorRoot, "test4", "222222.md")
	externalID := "feishu-node-222222"
	if err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []CloudObjectIndexRecord{
		{
			ExternalObjectID: externalID,
			ExternalName:     "222222",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-1",
			LocalRelPath:     "test4/222222.md",
			LocalAbsPath:     objectPath,
			Checksum:         "rev-1",
			SizeBytes:        12,
		},
	}, now); err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}
	nextSyncAt := now.Add(24 * time.Hour)
	if err := st.db.WithContext(ctx).Create(&[]sourceDocumentStateEntity{
		{
			TenantID:       src.TenantID,
			SourceID:       src.ID,
			ObjectKey:      externalID,
			Path:           objectPath,
			Name:           "222222.md",
			SourceExists:   true,
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			OriginRef:      externalID,
			SourceVersion:  "c_rev-1",
			SourceChecksum: "rev-1",
			SourceState:    sourceStateNew,
			SyncState:      syncStateScheduled,
			PendingAction:  pendingActionCreate,
			NextSyncAt:     &nextSyncAt,
			LastDetectedAt: now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			TenantID:       src.TenantID,
			SourceID:       src.ID,
			ObjectKey:      objectPath,
			Path:           objectPath,
			Name:           "222222.md",
			SourceExists:   true,
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			SourceVersion:  "c_rev-1",
			SourceChecksum: "rev-1",
			SourceState:    sourceStateNew,
			SyncState:      syncStateScheduled,
			PendingAction:  pendingActionCreate,
			NextSyncAt:     &nextSyncAt,
			LastDetectedAt: now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("seed duplicate source document states failed: %v", err)
	}

	if _, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "222222", Key: objectPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		objectPath: {Path: objectPath, Size: 12, Checksum: "rev-2"},
	}); err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}

	var states []sourceDocumentStateEntity
	if err := st.db.WithContext(ctx).
		Where("source_id = ? AND path = ?", src.ID, objectPath).
		Order("id ASC").
		Find(&states).Error; err != nil {
		t.Fatalf("load source document states failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected tree query not to collapse duplicate states, got %+v", states)
	}
	for _, state := range states {
		if state.SourceChecksum != "rev-1" || state.SourceVersion != "c_rev-1" {
			t.Fatalf("expected tree query not to update source state, got %+v", state)
		}
	}
}

func TestCloudSyncClaimAndFinishLifecycle(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	_, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_001",
		ScheduleExpr:     "daily@02:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}
	run, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"})
	if err != nil {
		t.Fatalf("trigger cloud sync failed: %v", err)
	}

	now := time.Now().UTC().Add(2 * time.Second)
	claims, err := st.ClaimDueCloudSources(ctx, "ut-lock-owner", now, 10, 2*time.Minute)
	if err != nil {
		t.Fatalf("claim due cloud sources failed: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 due claim, got %d", len(claims))
	}
	claim := claims[0]
	if claim.SourceID != src.ID {
		t.Fatalf("expected source_id=%s, got %s", src.ID, claim.SourceID)
	}
	if claim.ExistingRunID != run.RunID {
		t.Fatalf("expected existing_run_id=%s, got %s", run.RunID, claim.ExistingRunID)
	}

	startedRun, err := st.StartCloudSyncRun(ctx, src.ID, "manual", claim.ExistingRunID, now)
	if err != nil {
		t.Fatalf("start cloud sync run failed: %v", err)
	}
	if startedRun.RunID != run.RunID {
		t.Fatalf("expected reused run_id=%s, got %s", run.RunID, startedRun.RunID)
	}

	if err := st.FinishCloudSyncRun(ctx, src.ID, CloudSyncRunFinalize{
		RunID:        run.RunID,
		Status:       "SUCCEEDED",
		FinishedAt:   now.Add(5 * time.Second),
		RemoteTotal:  5,
		CreatedCount: 2,
		UpdatedCount: 1,
		DeletedCount: 1,
		SkippedCount: 1,
		FailedCount:  0,
	}); err != nil {
		t.Fatalf("finish cloud sync run failed: %v", err)
	}

	runs, err := st.ListCloudSyncRuns(ctx, src.ID, 10)
	if err != nil {
		t.Fatalf("list cloud sync runs failed: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "SUCCEEDED" {
		t.Fatalf("expected run status SUCCEEDED, got %s", runs[0].Status)
	}
	if runs[0].FinishedAt == nil || runs[0].FinishedAt.IsZero() {
		t.Fatalf("expected finished_at to be set")
	}

	var checkpoint cloudSyncCheckpointEntity
	if err := st.db.WithContext(ctx).Take(&checkpoint, "source_id = ?", src.ID).Error; err != nil {
		t.Fatalf("load checkpoint failed: %v", err)
	}
	if strings.TrimSpace(checkpoint.LockOwner) != "" || checkpoint.LockUntil != nil {
		t.Fatalf("expected checkpoint lock released, got owner=%q lock_until=%v", checkpoint.LockOwner, checkpoint.LockUntil)
	}
	if checkpoint.LastSuccessAt == nil || checkpoint.LastSuccessAt.IsZero() {
		t.Fatalf("expected checkpoint.last_success_at to be set")
	}
}

func TestCloudSyncClaimDueDoesNotReuseFinishedRun(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()

	_, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn_002",
		ScheduleExpr:     "daily@02:00",
		ScheduleTZ:       "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	firstRun, err := st.TriggerCloudSync(ctx, src.ID, model.TriggerCloudSyncRequest{TriggerType: "manual"})
	if err != nil {
		t.Fatalf("trigger cloud sync failed: %v", err)
	}
	finishedAt := time.Now().UTC().Add(2 * time.Second)
	if err := st.FinishCloudSyncRun(ctx, src.ID, CloudSyncRunFinalize{
		RunID:      firstRun.RunID,
		Status:     "SUCCEEDED",
		FinishedAt: finishedAt,
	}); err != nil {
		t.Fatalf("finish first cloud sync run failed: %v", err)
	}

	dueAt := finishedAt.Add(2 * time.Second)
	if err := st.db.WithContext(ctx).Model(&cloudSyncCheckpointEntity{}).
		Where("source_id = ?", src.ID).
		Updates(map[string]any{
			"next_sync_at": &dueAt,
			"lock_owner":   "",
			"lock_until":   nil,
			"updated_at":   dueAt,
		}).Error; err != nil {
		t.Fatalf("force next_sync_at failed: %v", err)
	}

	claims, err := st.ClaimDueCloudSources(ctx, "ut-lock-owner-2", dueAt.Add(time.Second), 10, 2*time.Minute)
	if err != nil {
		t.Fatalf("claim due cloud sources failed: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 due claim, got %d", len(claims))
	}
	if claims[0].ExistingRunID != "" {
		t.Fatalf("expected empty existing_run_id for finished run, got %s", claims[0].ExistingRunID)
	}

	secondRun, err := st.StartCloudSyncRun(ctx, src.ID, "scheduled", claims[0].ExistingRunID, dueAt.Add(time.Second))
	if err != nil {
		t.Fatalf("start scheduled cloud sync run failed: %v", err)
	}
	if secondRun.RunID == firstRun.RunID {
		t.Fatalf("expected new run_id for scheduled run, got reused %s", secondRun.RunID)
	}
}

func TestCloudObjectIndexUpsertAndMarkDelete(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	now := time.Now().UTC()

	err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []CloudObjectIndexRecord{
		{
			ExternalObjectID: "obj_a",
			ExternalPath:     "docs/a.md",
			ExternalName:     "a.md",
			ExternalKind:     "file",
			ExternalVersion:  "v1",
			LocalRelPath:     "docs/a.md",
			LocalAbsPath:     filepath.Join(src.RootPath, "docs/a.md"),
			Checksum:         "sha-a",
			SizeBytes:        11,
		},
		{
			ExternalObjectID: "obj_b",
			ExternalPath:     "docs/b.md",
			ExternalName:     "b.md",
			ExternalKind:     "file",
			ExternalVersion:  "v1",
			LocalRelPath:     "docs/b.md",
			LocalAbsPath:     filepath.Join(src.RootPath, "docs/b.md"),
			Checksum:         "sha-b",
			SizeBytes:        22,
		},
	}, now)
	if err != nil {
		t.Fatalf("upsert cloud object index failed: %v", err)
	}

	items, err := st.ListCloudObjectIndex(ctx, src.ID)
	if err != nil {
		t.Fatalf("list cloud object index failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 index records, got %d", len(items))
	}

	if err := st.MarkCloudObjectsDeleted(ctx, src.ID, []string{"obj_b"}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark cloud object deleted failed: %v", err)
	}
	items, err = st.ListCloudObjectIndex(ctx, src.ID)
	if err != nil {
		t.Fatalf("list cloud object index failed: %v", err)
	}
	deleted := 0
	for _, item := range items {
		if item.IsDeleted {
			deleted++
		}
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted record, got %d", deleted)
	}
}

func TestBuildTreeUpdateState(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-3 * time.Minute)

	newPath := "/tmp/watch/tree-new.txt"
	unchangedPath := "/tmp/watch/tree-unchanged.txt"

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: newPath, OccurredAt: baseAt.Add(1 * time.Second)},
		{SourceID: src.ID, EventType: "modified", Path: unchangedPath, OccurredAt: baseAt.Add(2 * time.Second)},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(20*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}

	unchangedDoc := loadDocumentByPath(t, st, src, unchangedPath)
	unchangedTasks := loadTasksByDocumentID(t, st, unchangedDoc.ID)
	if len(unchangedTasks) != 1 {
		t.Fatalf("expected unchanged path to have one task, got %d", len(unchangedTasks))
	}
	if err := st.MarkTaskSucceeded(ctx, unchangedTasks[0].ID, unchangedDoc.ID, unchangedTasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark unchanged task succeeded failed: %v", err)
	}

	treeItems := []model.TreeNode{
		{Title: "tree-new.txt", Key: newPath, IsDir: false},
		{Title: "tree-unchanged.txt", Key: unchangedPath, IsDir: false},
	}
	items, token, err := st.BuildTreeUpdateState(ctx, src.ID, treeItems, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if token == "" {
		t.Fatalf("expected non-empty selection token")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 tree items, got %d", len(items))
	}

	var newItem, unchangedItem *model.TreeNode
	for i := range items {
		if items[i].Key == newPath {
			newItem = &items[i]
		}
		if items[i].Key == unchangedPath {
			unchangedItem = &items[i]
		}
	}
	if newItem == nil || unchangedItem == nil {
		t.Fatalf("missing expected tree items after enrichment: %+v", items)
	}
	if newItem.UpdateType != "NEW" {
		t.Fatalf("expected new item update_type NEW, got %s", newItem.UpdateType)
	}
	if newItem.HasUpdate == nil || !*newItem.HasUpdate {
		t.Fatalf("expected new item has_update=true, got %+v", newItem.HasUpdate)
	}
	if newItem.StatusSource != "DOCUMENTS" {
		t.Fatalf("expected new item status_source DOCUMENTS, got %s", newItem.StatusSource)
	}

	if unchangedItem.UpdateType != "UNCHANGED" {
		t.Fatalf("expected unchanged item update_type UNCHANGED, got %s", unchangedItem.UpdateType)
	}
	if unchangedItem.HasUpdate == nil || *unchangedItem.HasUpdate {
		t.Fatalf("expected unchanged item has_update=false, got %+v", unchangedItem.HasUpdate)
	}
	if unchangedItem.StatusSource != "DOCUMENTS" {
		t.Fatalf("expected unchanged item status_source DOCUMENTS, got %s", unchangedItem.StatusSource)
	}
}

func TestBuildTreeUpdateStateIgnoresStaleParseQueueTask(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	src := createTestSource(t, st)
	ctx := context.Background()
	baseAt := time.Now().UTC().Add(-3 * time.Minute)

	path := "/tmp/watch/tree-stale-task.txt"
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: baseAt},
	})
	if err != nil {
		t.Fatalf("build mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutation failed: %v", err)
	}
	if _, err := st.ScheduleDueParses(ctx, baseAt.Add(20*time.Second)); err != nil {
		t.Fatalf("schedule parse failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one parse task, got %d", len(tasks))
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"desired_version_id": tasks[0].TargetVersionID + "_new",
			"current_version_id": tasks[0].TargetVersionID,
			"parse_status":       "PENDING",
		}).Error; err != nil {
		t.Fatalf("seed newer desired version failed: %v", err)
	}

	treeItems := []model.TreeNode{{Title: "tree-stale-task.txt", Key: path, IsDir: false}}
	items, _, err := st.BuildTreeUpdateState(ctx, src.ID, treeItems, nil)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(items, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.ParseQueueState != "" {
		t.Fatalf("expected stale task not to set parse_queue_state, got %s", node.ParseQueueState)
	}
	if node.UpdateType != "MODIFIED" {
		t.Fatalf("expected document update_type MODIFIED, got %s", node.UpdateType)
	}
}

func TestNonWatchSnapshotDiffAndSelectionToken(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-non-watch",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	pathA := "/tmp/watch/a.txt"
	pathB := "/tmp/watch/b.txt"
	pathC := "/tmp/watch/c.txt"
	modA1 := time.Now().UTC().Add(-2 * time.Minute)
	modB1 := modA1

	items := []model.TreeNode{
		{Title: "a.txt", Key: pathA, IsDir: false},
		{Title: "b.txt", Key: pathB, IsDir: false},
	}
	stats1 := map[string]model.TreeFileStat{
		pathA: {Path: pathA, Size: 10, ModTime: &modA1},
		pathB: {Path: pathB, Size: 20, ModTime: &modB1},
	}
	tree1, token1, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats1)
	if err != nil {
		t.Fatalf("build first snapshot tree state failed: %v", err)
	}
	if token1 == "" {
		t.Fatalf("expected first selection token")
	}
	for _, item := range tree1 {
		if item.UpdateType != "NEW" {
			t.Fatalf("expected first preview update_type NEW, got %s for %s", item.UpdateType, item.Key)
		}
		if item.StatusSource != "SNAPSHOT" {
			t.Fatalf("expected first preview status_source SNAPSHOT, got %s", item.StatusSource)
		}
	}

	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{pathA, pathB},
		SelectionToken: token1,
	})
	if err != nil {
		t.Fatalf("generate tasks with first selection token failed: %v", err)
	}
	if resp.AcceptedCount != 2 {
		t.Fatalf("expected accepted_count=2, got %d", resp.AcceptedCount)
	}

	var relation sourceSnapshotRelationEntity
	if err := st.db.WithContext(ctx).Take(&relation, "source_id = ?", src.ID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("load snapshot relation failed: %v", err)
	}
	if relation.LastCommittedSnapshotID != "" {
		t.Fatalf("expected manual generate not to commit snapshot before sync, got %s", relation.LastCommittedSnapshotID)
	}
	if relation.LastPreviewSnapshotID != "" {
		t.Fatalf("expected tree query not to persist preview snapshot, got %s", relation.LastPreviewSnapshotID)
	}
	for _, path := range []string{pathA, pathB} {
		doc := loadDocumentByPath(t, st, src, path)
		tasks := loadTasksByDocumentID(t, st, doc.ID)
		if len(tasks) != 1 {
			t.Fatalf("expected %s to have one queued task, got %d", path, len(tasks))
		}
	}

	modA2 := modA1.Add(3 * time.Minute)
	modC2 := modA1.Add(1 * time.Minute)
	items2 := []model.TreeNode{
		{Title: "a.txt", Key: pathA, IsDir: false},
		{Title: "c.txt", Key: pathC, IsDir: false},
	}
	stats2 := map[string]model.TreeFileStat{
		pathA: {Path: pathA, Size: 11, ModTime: &modA2},
		pathC: {Path: pathC, Size: 30, ModTime: &modC2},
	}
	tree2, token2, err := st.BuildTreeUpdateState(ctx, src.ID, items2, stats2)
	if err != nil {
		t.Fatalf("build second snapshot tree state failed: %v", err)
	}
	if token2 == "" {
		t.Fatalf("expected second selection token")
	}
	nodeA, ok := findTreeNodeByPath(tree2, pathA)
	if !ok {
		t.Fatalf("missing node for path %s", pathA)
	}
	if nodeA.UpdateType != "NEW" {
		t.Fatalf("expected %s to remain NEW before first sync succeeds, got %s", pathA, nodeA.UpdateType)
	}
	nodeB, ok := findTreeNodeByPath(tree2, pathB)
	if !ok {
		t.Fatalf("missing node for path %s", pathB)
	}
	if nodeB.UpdateType != "DELETED" {
		t.Fatalf("expected %s DELETED, got %s", pathB, nodeB.UpdateType)
	}
	nodeC, ok := findTreeNodeByPath(tree2, pathC)
	if !ok {
		t.Fatalf("missing node for path %s", pathC)
	}
	if nodeC.UpdateType != "NEW" {
		t.Fatalf("expected %s NEW, got %s", pathC, nodeC.UpdateType)
	}

	_, err = st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{"/tmp/watch/not-in-preview.txt"},
		SelectionToken: token2,
	})
	if err == nil {
		t.Fatalf("expected error when path is not in selected snapshot")
	}

	resp, err = st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{pathA, pathB, pathC},
		UpdatedOnly:    true,
		SelectionToken: token2,
	})
	if err != nil {
		t.Fatalf("generate tasks with updated_only + selection token failed: %v", err)
	}
	if resp.IgnoredUnchangedCount != 0 {
		t.Fatalf("expected ignored_unchanged_count=0, got %d", resp.IgnoredUnchangedCount)
	}
	if resp.AcceptedCount != 3 {
		t.Fatalf("expected accepted_count=3, got %d", resp.AcceptedCount)
	}
	docB := loadDocumentByPath(t, st, src, pathB)
	if docB.ParseStatus != "DELETED" {
		t.Fatalf("expected deleted path parse_status=DELETED, got %s", docB.ParseStatus)
	}
}

func TestScheduledTreeObservedSourceStateMaterializesAtDueTime(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "scheduled-tree-observed",
		RootPath:          "/tmp/scheduled-tree-observed",
		AgentID:           "agent-1",
		WatchEnabled:      true,
		IdleWindowSeconds: 10,
		ReconcileSchedule: "daily@02:00:03",
	})
	if err != nil {
		t.Fatalf("create scheduled source failed: %v", err)
	}

	path := "/tmp/scheduled-tree-observed/new-from-tree.txt"
	modAt := time.Date(2026, 5, 16, 1, 59, 0, 0, time.UTC)
	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{SourceID: src.ID, EventType: "modified", Path: path, OccurredAt: modAt},
	})
	if err != nil {
		t.Fatalf("build scheduled source mutation failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply scheduled source mutation failed: %v", err)
	}
	var state sourceDocumentStateEntity
	if err := st.db.WithContext(ctx).Where("source_id = ? AND path = ?", src.ID, path).Take(&state).Error; err != nil {
		t.Fatalf("load source state before tree query failed: %v", err)
	}
	if state.NextSyncAt == nil {
		t.Fatalf("expected next_sync_at before tree query")
	}

	items := []model.TreeNode{{Title: "new-from-tree.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{path: {Path: path, Size: 10, ModTime: &modAt}}
	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing tree node %s", path)
	}
	if node.UpdateType != "NEW" || node.SyncState != syncStateScheduled {
		t.Fatalf("expected scheduled NEW state before due time, got update=%s sync=%s", node.UpdateType, node.SyncState)
	}

	beforeDue := state.NextSyncAt.Add(-time.Second)
	created, err := st.ScheduleDueParses(ctx, beforeDue)
	if err != nil {
		t.Fatalf("schedule before due failed: %v", err)
	}
	if created != 0 {
		t.Fatalf("expected no task before due time, got %d", created)
	}

	due := state.NextSyncAt.Add(time.Second)
	created, err = st.ScheduleDueParses(ctx, due)
	if err != nil {
		t.Fatalf("schedule at due time failed: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected one task at due time, got %d", created)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}
	if normalizeTaskAction(tasks[0].TaskAction) != taskActionCreate {
		t.Fatalf("expected create task, got %s", tasks[0].TaskAction)
	}
	var queuedState sourceDocumentStateEntity
	if err := st.db.WithContext(ctx).Where("source_id = ? AND path = ?", src.ID, path).Take(&queuedState).Error; err != nil {
		t.Fatalf("load source state failed: %v", err)
	}
	if queuedState.SyncState != syncStatePending || queuedState.ActiveTaskID != tasks[0].ID || queuedState.NextSyncAt != nil {
		t.Fatalf("expected pending queued state, got sync=%s active=%d next=%v", queuedState.SyncState, queuedState.ActiveTaskID, queuedState.NextSyncAt)
	}
}

func TestNonWatchManualPullDoesNotCommitUnselectedFiles(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-partial-selection",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	pathA := "/tmp/watch/a.txt"
	pathB := "/tmp/watch/b.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	items := []model.TreeNode{
		{Title: "a.txt", Key: pathA, IsDir: false},
		{Title: "b.txt", Key: pathB, IsDir: false},
	}
	stats := map[string]model.TreeFileStat{
		pathA: {Path: pathA, Size: 10, ModTime: &modAt},
		pathB: {Path: pathB, Size: 20, ModTime: &modAt},
	}

	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{pathA},
		SelectionToken: token,
	}); err != nil {
		t.Fatalf("generate tasks with partial selection failed: %v", err)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state after partial selection failed: %v", err)
	}
	nodeA, ok := findTreeNodeByPath(tree, pathA)
	if !ok {
		t.Fatalf("missing node for path %s", pathA)
	}
	if nodeA.UpdateType != "NEW" {
		t.Fatalf("expected selected path %s to remain NEW until sync succeeds, got %s", pathA, nodeA.UpdateType)
	}
	nodeB, ok := findTreeNodeByPath(tree, pathB)
	if !ok {
		t.Fatalf("missing node for path %s", pathB)
	}
	if nodeB.UpdateType != "NEW" {
		t.Fatalf("expected unselected path %s to remain NEW, got %s", pathB, nodeB.UpdateType)
	}
}

func TestNonWatchSnapshotNewDoesNotOverrideParsedDocument(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		Name:                  "src-parsed-missing-snapshot",
		RootPath:              "/tmp/watch",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/Test.md"
	now := time.Now().UTC()
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		DesiredVersionID: "v1",
		CurrentVersionID: "v1",
		LastModifiedAt:   &now,
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "doc_test",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create parsed document failed: %v", err)
	}

	items := []model.TreeNode{{Title: "Test.md", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, ModTime: &now, Checksum: "rev1"},
	}
	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.UpdateType != "UNCHANGED" {
		t.Fatalf("expected parsed document to ignore snapshot NEW, got %s", node.UpdateType)
	}
	if node.HasUpdate == nil || *node.HasUpdate {
		t.Fatalf("expected has_update=false for parsed document, got %+v", node.HasUpdate)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	if resp.Items[0].UpdateType != "UNCHANGED" {
		t.Fatalf("expected list update_type UNCHANGED, got %s", resp.Items[0].UpdateType)
	}
	if resp.Summary.PendingPullCount != 0 {
		t.Fatalf("expected pending_pull_count=0, got %d", resp.Summary.PendingPullCount)
	}

	pullResp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate tasks failed: %v", err)
	}
	if pullResp.AcceptedCount != 0 || pullResp.IgnoredUnchangedCount != 1 {
		t.Fatalf("expected parsed path ignored, got accepted=%d ignored=%d", pullResp.AcceptedCount, pullResp.IgnoredUnchangedCount)
	}

	treeAfter, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree after ignored pull failed: %v", err)
	}
	nodeAfter, ok := findTreeNodeByPath(treeAfter, path)
	if !ok {
		t.Fatalf("missing node after ignored pull for path %s", path)
	}
	if nodeAfter.UpdateType != "UNCHANGED" {
		t.Fatalf("expected snapshot baseline to be promoted, got %s", nodeAfter.UpdateType)
	}
}

func TestNonWatchSnapshotNewDoesNotOverrideSucceededLatestTask(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-succeeded-task-missing-snapshot",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/Test.md"
	now := time.Now().UTC()
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		DesiredVersionID: "v1",
		LastModifiedAt:   &now,
		ParseStatus:      "PENDING",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create document failed: %v", err)
	}
	doc := loadDocumentByPath(t, st, src, path)
	if err := st.db.WithContext(ctx).Create(&parseTaskEntity{
		TenantID:                src.TenantID,
		DocumentID:              doc.ID,
		TaskAction:              taskActionCreate,
		TargetVersionID:         "v1",
		Status:                  "SUCCEEDED",
		ScanOrchestrationStatus: "SUCCEEDED",
		NextRunAt:               now,
		FinishedAt:              &now,
		CreatedAt:               now,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create parse task failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&documentEntity{}).
		Where("id = ?", doc.ID).
		Updates(map[string]any{
			"current_version_id": "v1",
			"core_document_id":   "core-doc-succeeded-task",
			"parse_status":       "SUCCEEDED",
		}).Error; err != nil {
		t.Fatalf("mark document synced failed: %v", err)
	}

	items := []model.TreeNode{{Title: "Test.md", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, ModTime: &now, Checksum: "rev1"},
	}
	tree, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.UpdateType != "UNCHANGED" {
		t.Fatalf("expected succeeded task to ignore snapshot NEW, got %s", node.UpdateType)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].UpdateType != "UNCHANGED" {
		t.Fatalf("expected list update_type UNCHANGED, got %+v", resp.Items)
	}

	pullResp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
		UpdatedOnly:    true,
	})
	if err != nil {
		t.Fatalf("generate tasks failed: %v", err)
	}
	if pullResp.AcceptedCount != 0 || pullResp.IgnoredUnchangedCount != 1 {
		t.Fatalf("expected succeeded task path ignored, got accepted=%d ignored=%d", pullResp.AcceptedCount, pullResp.IgnoredUnchangedCount)
	}
}

func TestNonWatchTreeStateSkipsTransientCommittedSnapshotItems(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-transient-committed",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	now := time.Now().UTC()
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    2,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	normalPath := "/tmp/watch/normal.txt"
	transientPath := "/tmp/watch/.normal.txt.swp"
	if err := st.db.WithContext(ctx).Create(&[]sourceFileSnapshotItemEntity{
		{SnapshotID: committedID, Path: normalPath, SizeBytes: 10},
		{SnapshotID: committedID, Path: transientPath, SizeBytes: 1},
	}).Error; err != nil {
		t.Fatalf("create committed snapshot items failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, []model.TreeNode{
		{Title: "normal.txt", Key: normalPath, IsDir: false},
	}, map[string]model.TreeFileStat{
		normalPath: {Path: normalPath, Size: 10},
	})
	if err != nil {
		t.Fatalf("build tree update state failed: %v", err)
	}
	if _, ok := findTreeNodeByPath(tree, transientPath); ok {
		t.Fatalf("transient committed snapshot item should not be reintroduced into tree state")
	}
}

func TestListSourceDocumentsKeepsUnselectedManualPreviewUpdates(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-residual-preview",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	pathA := "/tmp/watch/a.txt"
	pathB := "/tmp/watch/b.txt"
	firstAt := time.Now().UTC().Add(-5 * time.Minute)
	items := []model.TreeNode{
		{Title: "a.txt", Key: pathA, IsDir: false},
		{Title: "b.txt", Key: pathB, IsDir: false},
	}
	firstStats := map[string]model.TreeFileStat{
		pathA: {Path: pathA, Size: 10, ModTime: &firstAt},
		pathB: {Path: pathB, Size: 20, ModTime: &firstAt},
	}
	_, token1, err := st.BuildTreeUpdateState(ctx, src.ID, items, firstStats)
	if err != nil {
		t.Fatalf("build initial tree state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{pathA, pathB},
		SelectionToken: token1,
	}); err != nil {
		t.Fatalf("generate initial tasks failed: %v", err)
	}

	secondAt := firstAt.Add(3 * time.Minute)
	secondStats := map[string]model.TreeFileStat{
		pathA: {Path: pathA, Size: 11, ModTime: &secondAt},
		pathB: {Path: pathB, Size: 21, ModTime: &secondAt},
	}
	_, token2, err := st.BuildTreeUpdateState(ctx, src.ID, items, secondStats)
	if err != nil {
		t.Fatalf("build second tree state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{pathA},
		SelectionToken: token2,
	}); err != nil {
		t.Fatalf("generate partial second tasks failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	updates := make(map[string]string, len(resp.Items))
	for _, item := range resp.Items {
		updates[item.Path] = item.UpdateType
	}
	if len(updates) != 0 {
		t.Fatalf("expected unsynced new paths to stay out of document list, got %+v", updates)
	}

	docA := loadDocumentByPath(t, st, src, pathA)
	tasksA := loadTasksByDocumentID(t, st, docA.ID)
	if len(tasksA) != 1 {
		t.Fatalf("expected selected path %s to have one task, got %d", pathA, len(tasksA))
	}
	if err := st.MarkTaskSucceeded(ctx, tasksA[0].ID, docA.ID, tasksA[0].TargetVersionID); err != nil {
		t.Fatalf("mark selected task succeeded failed: %v", err)
	}

	resp, err = st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents after selected sync failed: %v", err)
	}
	updates = make(map[string]string, len(resp.Items))
	for _, item := range resp.Items {
		updates[item.Path] = item.UpdateType
	}
	if updates[pathA] != "UNCHANGED" {
		t.Fatalf("expected selected path %s UNCHANGED after sync succeeds, got %s", pathA, updates[pathA])
	}
	if _, ok := updates[pathB]; ok {
		t.Fatalf("expected unselected new path %s to stay out of document list before sync, got %s", pathB, updates[pathB])
	}
}

func TestSnapshotSourceAckDoesNotReplaceCommittedSnapshotItems(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-ack-baseline",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/a.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	items := []model.TreeNode{{Title: "a.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, ModTime: &modAt},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build initial tree state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
	}); err != nil {
		t.Fatalf("generate tasks with selection token failed: %v", err)
	}

	var before sourceSnapshotRelationEntity
	beforeErr := st.db.WithContext(ctx).Take(&before, "source_id = ?", src.ID).Error
	if beforeErr != nil && !errors.Is(beforeErr, gorm.ErrRecordNotFound) {
		t.Fatalf("load relation before ack failed: %v", beforeErr)
	}
	if before.LastCommittedSnapshotID != "" {
		t.Fatalf("expected manual generate not to commit snapshot before sync, got %s", before.LastCommittedSnapshotID)
	}
	if before.LastPreviewSnapshotID != "" {
		t.Fatalf("expected tree query not to persist preview snapshot, got %s", before.LastPreviewSnapshotID)
	}

	pulled, err := st.PullPendingCommands(ctx, model.PullCommandsRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("pull commands failed: %v", err)
	}
	if len(pulled.Commands) != 1 || pulled.Commands[0].Type != model.CommandSnapshotSource {
		t.Fatalf("expected one snapshot_source command, got %+v", pulled.Commands)
	}
	resultJSON := `{"snapshot_ref":"local://snapshot/src-ack-baseline/snapshot.json","file_count":1,"taken_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`
	if err := st.AckCommand(ctx, model.AckCommandRequest{
		AgentID:    "agent-1",
		CommandID:  pulled.Commands[0].ID,
		Success:    true,
		ResultJSON: resultJSON,
	}); err != nil {
		t.Fatalf("ack snapshot_source failed: %v", err)
	}

	var after sourceSnapshotRelationEntity
	afterErr := st.db.WithContext(ctx).Take(&after, "source_id = ?", src.ID).Error
	if afterErr != nil && !errors.Is(afterErr, gorm.ErrRecordNotFound) {
		t.Fatalf("load relation after ack failed: %v", afterErr)
	}
	if strings.TrimSpace(before.LastCommittedSnapshotID) != "" && after.LastCommittedSnapshotID != before.LastCommittedSnapshotID {
		t.Fatalf("snapshot_source ack replaced committed snapshot: before=%s after=%s", before.LastCommittedSnapshotID, after.LastCommittedSnapshotID)
	}
	if strings.TrimSpace(before.LastCommittedSnapshotID) == "" && strings.TrimSpace(after.LastCommittedSnapshotID) != "" {
		t.Fatalf("snapshot_source ack should not synthesize committed item snapshots, got %s", after.LastCommittedSnapshotID)
	}

	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state after ack failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.UpdateType != "NEW" {
		t.Fatalf("expected source state to remain NEW after snapshot_source ack before sync, got %s", node.UpdateType)
	}
}

func TestSourceFileSnapshotSelectionTokenIndexAllowsCommittedSnapshotsWithoutTokens(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.db.WithContext(ctx).Exec("DROP INDEX IF EXISTS idx_source_file_snapshots_selection_token").Error; err != nil {
		t.Fatalf("drop selection token index failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Exec("CREATE UNIQUE INDEX idx_source_file_snapshots_selection_token ON source_file_snapshots (selection_token)").Error; err != nil {
		t.Fatalf("create legacy selection token index failed: %v", err)
	}
	if err := st.ensureSourceFileSnapshotIndexes(ctx); err != nil {
		t.Fatalf("rebuild legacy selection token index failed: %v", err)
	}

	src := createTestSource(t, st)
	now := time.Now().UTC()

	committed := []sourceFileSnapshotEntity{
		{
			SnapshotID:   "ss_empty_token_1",
			SourceID:     src.ID,
			TenantID:     src.TenantID,
			SnapshotType: "COMMITTED",
			FileCount:    1,
			CreatedAt:    now,
		},
		{
			SnapshotID:   "ss_empty_token_2",
			SourceID:     src.ID,
			TenantID:     src.TenantID,
			SnapshotType: "COMMITTED",
			FileCount:    2,
			CreatedAt:    now.Add(time.Nanosecond),
		},
	}
	for _, snap := range committed {
		if err := st.db.WithContext(ctx).Create(&snap).Error; err != nil {
			t.Fatalf("create committed snapshot with empty selection token failed: %v", err)
		}
	}

	token := "sel_duplicate_token"
	preview := sourceFileSnapshotEntity{
		SnapshotID:     "ss_token_1",
		SourceID:       src.ID,
		TenantID:       src.TenantID,
		SnapshotType:   "PREVIEW",
		SelectionToken: token,
		FileCount:      1,
		CreatedAt:      now.Add(2 * time.Nanosecond),
	}
	if err := st.db.WithContext(ctx).Create(&preview).Error; err != nil {
		t.Fatalf("create preview snapshot with token failed: %v", err)
	}
	preview.SnapshotID = "ss_token_2"
	preview.CreatedAt = now.Add(3 * time.Nanosecond)
	if err := st.db.WithContext(ctx).Create(&preview).Error; err == nil {
		t.Fatalf("expected duplicate non-empty selection token to fail")
	}
}

func TestListSourceDocumentsUsesLatestNonWatchSnapshotState(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-non-watch-doc-list",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/a.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	items := []model.TreeNode{{Title: "a.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, ModTime: &modAt},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build initial tree state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
	}); err != nil {
		t.Fatalf("generate tasks with selection token failed: %v", err)
	}

	_, _, err = st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build pending preview failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected source state NEW to stay out of document list until sync, got %+v", resp.Items)
	}
	if resp.Summary.NewCount != 0 || resp.Summary.PendingPullCount != 0 {
		t.Fatalf("expected hidden source state not to affect document list summary, got new=%d pending=%d", resp.Summary.NewCount, resp.Summary.PendingPullCount)
	}

	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one parse task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark task succeeded failed: %v", err)
	}

	resp, err = st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents after sync failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document after sync, got %d", len(resp.Items))
	}
	if resp.Items[0].UpdateType != "UNCHANGED" {
		t.Fatalf("expected sync success to clear source state, got %s", resp.Items[0].UpdateType)
	}
	if resp.Summary.PendingPullCount != 0 {
		t.Fatalf("expected pending_pull_count=0 after sync success, got %d", resp.Summary.PendingPullCount)
	}
}

func TestListSourceDocumentsIgnoresConsumedNonWatchPreview(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-consumed-preview-doc-list",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/a.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	items := []model.TreeNode{{Title: "a.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, ModTime: &modAt},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build initial tree state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
	}); err != nil {
		t.Fatalf("generate tasks with selection token failed: %v", err)
	}

	var persistedPreviewCount int64
	if err := st.db.WithContext(ctx).Model(&sourceFileSnapshotEntity{}).
		Where("source_id = ? AND selection_token = ?", src.ID, token).
		Count(&persistedPreviewCount).Error; err != nil {
		t.Fatalf("count persisted previews failed: %v", err)
	}
	if persistedPreviewCount != 0 {
		t.Fatalf("expected read-only selection token not to persist preview snapshots, got %d", persistedPreviewCount)
	}

	doc := loadDocumentByPath(t, st, src, path)
	if _, err := st.ScheduleDueParses(ctx, time.Now().UTC().Add(20*time.Second)); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 parse task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark task succeeded failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	if resp.Items[0].UpdateType != "UNCHANGED" {
		t.Fatalf("expected consumed preview to leave document UNCHANGED, got %s", resp.Items[0].UpdateType)
	}
	if resp.Items[0].HasUpdate == nil || *resp.Items[0].HasUpdate {
		t.Fatalf("expected has_update=false after consumed preview, got %+v", resp.Items[0].HasUpdate)
	}
	if resp.Summary.PendingPullCount != 0 {
		t.Fatalf("expected pending_pull_count=0 after consumed preview, got %d", resp.Summary.PendingPullCount)
	}
}

func TestListSourceDocumentsUsesConsumedWatchPreviewMetadata(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src := createTestSource(t, st)

	path := "/tmp/watch/a.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	items := []model.TreeNode{{Title: "a.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 1234, ModTime: &modAt},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build watch tree state failed: %v", err)
	}
	if _, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{path},
		SelectionToken: token,
	}); err != nil {
		t.Fatalf("generate watch tasks with selection token failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&sourceDocumentStateEntity{}).
		Where("source_id = ? AND path = ?", src.ID, path).
		Updates(map[string]any{
			"source_size_bytes":  1234,
			"source_modified_at": &modAt,
		}).Error; err != nil {
		t.Fatalf("seed source state metadata failed: %v", err)
	}
	var persistedPreviewCount int64
	if err := st.db.WithContext(ctx).Model(&sourceFileSnapshotEntity{}).
		Where("source_id = ? AND selection_token = ?", src.ID, token).
		Count(&persistedPreviewCount).Error; err != nil {
		t.Fatalf("count persisted watch previews failed: %v", err)
	}
	if persistedPreviewCount != 0 {
		t.Fatalf("expected read-only selection token not to persist watch preview snapshots, got %d", persistedPreviewCount)
	}
	doc := loadDocumentByPath(t, st, src, path)
	tasks := loadTasksByDocumentID(t, st, doc.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected one parse task, got %d", len(tasks))
	}
	if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
		t.Fatalf("mark task succeeded failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	if resp.Items[0].SizeBytes != 1234 {
		t.Fatalf("expected size_bytes=1234 from source state metadata, got %d", resp.Items[0].SizeBytes)
	}
	if resp.Items[0].SourceUpdatedAt == nil || !resp.Items[0].SourceUpdatedAt.Equal(modAt) {
		t.Fatalf("expected source_updated_at=%v, got %v", modAt, resp.Items[0].SourceUpdatedAt)
	}
}

func TestGenerateTasksWithoutSelectionTokenConsumesNonWatchPreview(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-no-token-preview",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/a.txt"
	items := []model.TreeNode{{Title: "a.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10},
	}
	_, token, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build initial tree state failed: %v", err)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:  "partial",
		Paths: []string{path},
	})
	if err != nil {
		t.Fatalf("generate tasks without selection token failed: %v", err)
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("expected accepted_count=1, got %d", resp.AcceptedCount)
	}
	var persistedPreviewCount int64
	if err := st.db.WithContext(ctx).Model(&sourceFileSnapshotEntity{}).
		Where("source_id = ? AND selection_token = ?", src.ID, token).
		Count(&persistedPreviewCount).Error; err != nil {
		t.Fatalf("count persisted previews failed: %v", err)
	}
	if persistedPreviewCount != 0 {
		t.Fatalf("expected tokenless generate to work without persisted previews, got %d", persistedPreviewCount)
	}
}

func TestBuildTreeUpdateStateFallsBackFromEmptyCommittedSnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-empty-committed",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	path := "/tmp/watch/a.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	items := []model.TreeNode{{Title: "a.txt", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, ModTime: &modAt},
	}
	validCommittedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   validCommittedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create valid committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: validCommittedID,
		Path:       path,
		IsDir:      false,
		SizeBytes:  10,
		ModTime:    &modAt,
	}).Error; err != nil {
		t.Fatalf("create valid committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: validCommittedID,
		UpdatedAt:               time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	emptyCommittedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:     emptyCommittedID,
		SourceID:       src.ID,
		TenantID:       src.TenantID,
		SnapshotType:   "COMMITTED",
		BaseSnapshotID: validCommittedID,
		FileCount:      1,
		CreatedAt:      time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("create empty committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Model(&sourceSnapshotRelationEntity{}).
		Where("source_id = ?", src.ID).
		Updates(map[string]any{
			"last_committed_snapshot_id": emptyCommittedID,
			"updated_at":                 time.Now().UTC(),
		}).Error; err != nil {
		t.Fatalf("point relation at empty committed snapshot failed: %v", err)
	}

	tree, token2, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state with empty committed baseline failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.UpdateType != "UNCHANGED" {
		t.Fatalf("expected fallback baseline to mark unchanged, got %s", node.UpdateType)
	}
	payload, ok, err := decodeReadOnlySelectionToken(token2, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("expected read-only selection token, ok=%v err=%v token=%s", ok, err, token2)
	}
	if payload.Diff[path] != "UNCHANGED" {
		t.Fatalf("expected read-only token to preserve unchanged diff, got %+v", payload.Diff)
	}
	var persistedPreviewCount int64
	if err := st.db.WithContext(ctx).Model(&sourceFileSnapshotEntity{}).
		Where("source_id = ? AND selection_token = ?", src.ID, token2).
		Count(&persistedPreviewCount).Error; err != nil {
		t.Fatalf("count persisted previews failed: %v", err)
	}
	if persistedPreviewCount != 0 {
		t.Fatalf("expected tree query not to persist preview snapshot, got %d", persistedPreviewCount)
	}
}

func TestNonWatchSnapshotDiffMixedDirectoryAndSiblingFiles(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-mixed-tree",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      false,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create non-watch source failed: %v", err)
	}

	dirPath := "/tmp/watch/test"
	nestedPath := "/tmp/watch/test/perm_1.md"
	siblingPath := "/tmp/watch/alpha.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	items := []model.TreeNode{
		{
			Title: "test",
			Key:   dirPath,
			IsDir: true,
			Children: []model.TreeNode{
				{Title: "perm_1", Key: nestedPath, IsDir: false},
			},
		},
		{Title: "alpha.txt", Key: siblingPath, IsDir: false},
	}
	stats := map[string]model.TreeFileStat{
		nestedPath:  {Path: nestedPath, Size: 10, Checksum: "v1", ModTime: &modAt},
		siblingPath: {Path: siblingPath, Size: 20, Checksum: "v1", ModTime: &modAt},
	}

	_, token1, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build first snapshot tree state failed: %v", err)
	}
	resp, err := st.GenerateTasksForSource(ctx, src.ID, model.GenerateTasksRequest{
		Mode:           "partial",
		Paths:          []string{nestedPath, siblingPath},
		SelectionToken: token1,
	})
	if err != nil {
		t.Fatalf("generate tasks with first selection token failed: %v", err)
	}
	if resp.AcceptedCount != 2 {
		t.Fatalf("expected accepted_count=2, got %d", resp.AcceptedCount)
	}

	tree2, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build second snapshot tree state failed: %v", err)
	}
	for _, path := range []string{nestedPath, siblingPath} {
		node, ok := findTreeNodeByPath(tree2, path)
		if !ok {
			t.Fatalf("missing node for path %s", path)
		}
		if node.UpdateType != "NEW" {
			t.Fatalf("expected %s to remain NEW until sync succeeds, got %s", path, node.UpdateType)
		}
		if node.HasUpdate == nil || !*node.HasUpdate {
			t.Fatalf("expected %s has_update=true before sync, got %+v", path, node.HasUpdate)
		}
	}

	for _, path := range []string{nestedPath, siblingPath} {
		doc := loadDocumentByPath(t, st, src, path)
		tasks := loadTasksByDocumentID(t, st, doc.ID)
		if len(tasks) != 1 {
			t.Fatalf("expected %s to have one task, got %d", path, len(tasks))
		}
		if err := st.MarkTaskSucceeded(ctx, tasks[0].ID, doc.ID, tasks[0].TargetVersionID); err != nil {
			t.Fatalf("mark task succeeded for %s failed: %v", path, err)
		}
	}

	tree3, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build tree state after sync failed: %v", err)
	}
	for _, path := range []string{nestedPath, siblingPath} {
		node, ok := findTreeNodeByPath(tree3, path)
		if !ok {
			t.Fatalf("missing node for path %s after sync", path)
		}
		if node.UpdateType != "UNCHANGED" {
			t.Fatalf("expected %s UNCHANGED after sync succeeds, got %s", path, node.UpdateType)
		}
		if node.HasUpdate == nil || *node.HasUpdate {
			t.Fatalf("expected %s has_update=false after sync, got %+v", path, node.HasUpdate)
		}
	}
}

func TestWatchTreeUsesPendingDocumentUpdateOverUnchangedSnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src := createTestSource(t, st)

	path := "/tmp/watch/auto-updated.md"
	modAt := time.Now().UTC().Add(-2 * time.Minute)
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    modAt,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       path,
		IsDir:      false,
		SizeBytes:  10,
		Checksum:   "rev1",
		ModTime:    &modAt,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               modAt,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		CoreDocumentID:   "core-doc-1",
		DesiredVersionID: "v2",
		CurrentVersionID: "v1",
		LastModifiedAt:   &modAt,
		ParseStatus:      "PENDING",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_auto_updated",
		TriggerPolicy:    string(model.TriggerPolicyImmediate),
		UpdatedAt:        modAt,
	}).Error; err != nil {
		t.Fatalf("create pending document failed: %v", err)
	}

	items := []model.TreeNode{{Title: "auto-updated.md", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, Checksum: "rev1", ModTime: &modAt},
	}
	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build watch tree state failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.UpdateType != "MODIFIED" {
		t.Fatalf("expected pending document update to win over unchanged snapshot, got %s", node.UpdateType)
	}
	if node.StatusSource != "DOCUMENTS" {
		t.Fatalf("expected status_source DOCUMENTS, got %s", node.StatusSource)
	}
	if node.HasUpdate == nil || !*node.HasUpdate {
		t.Fatalf("expected has_update=true, got %+v", node.HasUpdate)
	}
}

func TestListSourceDocumentsKeepsPendingDocumentUpdateOverUnchangedSnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-auto-doc-list",
		RootPath:              "/tmp/cloud-auto-doc-list",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Minute)
	path := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "docs", "auto-updated.md")
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       path,
		IsDir:      false,
		SizeBytes:  10,
		Checksum:   "rev1",
		ModTime:    &now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		CoreDocumentID:   "core-doc-cloud-1",
		DesiredVersionID: "v2",
		CurrentVersionID: "v1",
		LastModifiedAt:   &now,
		NextParseAt:      &now,
		ParseStatus:      "PENDING",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_cloud_auto_updated",
		TriggerPolicy:    string(model.TriggerPolicyImmediate),
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create pending cloud document failed: %v", err)
	}

	items := []model.TreeNode{{Title: "auto-updated.md", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, Checksum: "rev1", ModTime: &now},
	}
	if _, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats); err != nil {
		t.Fatalf("build unchanged cloud preview failed: %v", err)
	}

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	if resp.Items[0].UpdateType != "MODIFIED" {
		t.Fatalf("expected pending document update to remain MODIFIED, got %s", resp.Items[0].UpdateType)
	}
	if resp.Summary.ModifiedCount != 1 || resp.Summary.PendingPullCount != 1 {
		t.Fatalf("expected modified=1 pending=1, got modified=%d pending=%d", resp.Summary.ModifiedCount, resp.Summary.PendingPullCount)
	}

	overviews, err := st.ListSourceDocumentOverviews(ctx, []model.Source{src})
	if err != nil {
		t.Fatalf("list source document overviews failed: %v", err)
	}
	overview := overviews[src.ID]
	if overview.Summary.TotalDocumentCount != 1 || overview.Summary.ModifiedCount != 1 || overview.Summary.PendingPullCount != 1 {
		t.Fatalf("expected overview summary to match document list, got %+v", overview.Summary)
	}
	if len(overview.Items) != 1 || overview.Items[0].UpdateType != "MODIFIED" {
		t.Fatalf("expected overview item to remain MODIFIED, got %+v", overview.Items)
	}
}

func TestListSourceDocumentOverviewsReturnsAllVisibleDocuments(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud-overview",
		Name:                  "src-cloud-overview",
		RootPath:              "/tmp/cloud-overview",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Minute)
	paths := []string{
		filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "changed.md"),
		filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "same-1.md"),
		filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "same-2.md"),
		filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "same-3.md"),
	}
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    int64(len(paths)),
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}

	docs := make([]documentEntity, 0, len(paths))
	snapshotItems := make([]sourceFileSnapshotItemEntity, 0, len(paths))
	indexRows := make([]cloudObjectIndexEntity, 0, len(paths))
	for i, path := range paths {
		current := "v1"
		desired := "v1"
		sourceVersion := "rev1"
		if i == 0 {
			desired = "v2"
			sourceVersion = "rev2"
		}
		docs = append(docs, documentEntity{
			TenantID:         src.TenantID,
			SourceID:         src.ID,
			SourceObjectID:   path,
			CoreDocumentID:   fmt.Sprintf("core-doc-%d", i),
			DesiredVersionID: desired,
			CurrentVersionID: current,
			LastModifiedAt:   &now,
			ParseStatus:      "SUCCEEDED",
			OriginType:       string(model.OriginTypeCloudSync),
			OriginPlatform:   "FEISHU",
			OriginRef:        fmt.Sprintf("node_%d", i),
			TriggerPolicy:    string(model.TriggerPolicyImmediate),
			UpdatedAt:        now.Add(time.Duration(i) * time.Second),
		})
		snapshotItems = append(snapshotItems, sourceFileSnapshotItemEntity{
			SnapshotID:     committedID,
			Path:           path,
			IsDir:          false,
			SizeBytes:      10,
			Checksum:       "rev1",
			ExternalFileID: fmt.Sprintf("node_%d", i),
			ModTime:        &now,
		})
		indexRows = append(indexRows, cloudObjectIndexEntity{
			SourceID:           src.ID,
			Provider:           "feishu",
			ExternalObjectID:   fmt.Sprintf("node_%d", i),
			ExternalPath:       filepath.Base(path),
			ExternalName:       filepath.Base(path),
			ExternalKind:       "docx",
			ExternalVersion:    sourceVersion,
			ExternalModifiedAt: &now,
			LocalAbsPath:       path,
			Checksum:           sourceVersion,
			SizeBytes:          10,
			LastSyncedAt:       &now,
			CreatedAt:          now,
			UpdatedAt:          now,
		})
	}
	if err := st.db.WithContext(ctx).Create(&docs).Error; err != nil {
		t.Fatalf("create overview documents failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&snapshotItems).Error; err != nil {
		t.Fatalf("create snapshot items failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&indexRows).Error; err != nil {
		t.Fatalf("create cloud object index rows failed: %v", err)
	}

	overviews, err := st.ListSourceDocumentOverviews(ctx, []model.Source{src})
	if err != nil {
		t.Fatalf("list source document overviews failed: %v", err)
	}
	overview := overviews[src.ID]
	if overview.Total != 4 || overview.Summary.TotalDocumentCount != 4 {
		t.Fatalf("expected overview total=4, got total=%d summary=%d", overview.Total, overview.Summary.TotalDocumentCount)
	}
	if len(overview.Items) != 4 {
		t.Fatalf("expected overview to include all visible documents, got %+v", overview.Items)
	}
	if overview.Summary.ModifiedCount != 1 || overview.Summary.PendingPullCount != 1 {
		t.Fatalf("expected modified=1 pending=1, got %+v", overview.Summary)
	}
	updates := map[string]int{}
	for _, item := range overview.Items {
		updates[item.UpdateType]++
	}
	if updates["MODIFIED"] != 1 || updates["UNCHANGED"] != 3 {
		t.Fatalf("expected one modified and three unchanged items, got %+v items=%+v", updates, overview.Items)
	}
}

func TestListSourceDocumentOverviewsCountsPendingCloudNewState(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud-overview-new",
		Name:                  "src-cloud-overview-new",
		RootPath:              "/tmp/cloud-mirror/src-cloud-overview-new",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC()
	visiblePath := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "wiki", "synced.md")
	pendingPath := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "wiki", "new.md")
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   visiblePath,
		CoreDocumentID:   "core-doc-synced",
		CurrentVersionID: "v1",
		DesiredVersionID: "v1",
		ParseStatus:      "SUCCEEDED",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "obj_synced",
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create visible cloud document failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&[]sourceDocumentStateEntity{
		{
			TenantID:          src.TenantID,
			SourceID:          src.ID,
			ObjectKey:         "obj_synced",
			Path:              visiblePath,
			Name:              "synced.md",
			SourceExists:      true,
			KnowledgeBaseSeen: true,
			OriginType:        string(model.OriginTypeCloudSync),
			OriginPlatform:    "FEISHU",
			OriginRef:         "obj_synced",
			SourceVersion:     "v1",
			BaselineVersion:   "v1",
			SourceState:       sourceStateUnchanged,
			SyncState:         syncStateIdle,
			PendingAction:     pendingActionNone,
			CoreDocumentID:    "core-doc-synced",
			LastDetectedAt:    now,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			TenantID:       src.TenantID,
			SourceID:       src.ID,
			ObjectKey:      "obj_new",
			Path:           pendingPath,
			Name:           "new.md",
			SourceExists:   true,
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			OriginRef:      "obj_new",
			SourceVersion:  "v2",
			SourceState:    sourceStateNew,
			SyncState:      syncStateScheduled,
			PendingAction:  pendingActionCreate,
			LastDetectedAt: now,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create source document states failed: %v", err)
	}

	overviews, err := st.ListSourceDocumentOverviews(ctx, []model.Source{src})
	if err != nil {
		t.Fatalf("list source document overviews failed: %v", err)
	}
	overview := overviews[src.ID]
	if overview.Summary.TotalDocumentCount != 1 {
		t.Fatalf("expected total document count to remain visible synced docs only, got %+v", overview.Summary)
	}
	if overview.Summary.NewCount != 1 || overview.Summary.PendingPullCount != 1 {
		t.Fatalf("expected pending cloud new state to be counted, got %+v", overview.Summary)
	}
	if len(overview.Items) != 1 {
		t.Fatalf("expected overview items to keep visible document list only, got %+v", overview.Items)
	}
}

func TestNonWatchCloudTreeKeepsPendingDocumentUpdateOverUnchangedSnapshot(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-cloud",
		Name:                  "src-cloud-auto-tree",
		RootPath:              "/tmp/cloud-auto-tree",
		AgentID:               "agent-1",
		WatchEnabled:          false,
		IdleWindowSeconds:     10,
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	})
	if err != nil {
		t.Fatalf("create cloud source failed: %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Minute)
	path := filepath.Join(sourcelayout.CloudMirrorRoot(src.RootPath), "docs", "auto-updated.md")
	committedID := sourceSnapshotID()
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotEntity{
		SnapshotID:   committedID,
		SourceID:     src.ID,
		TenantID:     src.TenantID,
		SnapshotType: "COMMITTED",
		FileCount:    1,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceFileSnapshotItemEntity{
		SnapshotID: committedID,
		Path:       path,
		IsDir:      false,
		SizeBytes:  10,
		Checksum:   "rev1",
		ModTime:    &now,
	}).Error; err != nil {
		t.Fatalf("create committed snapshot item failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&sourceSnapshotRelationEntity{
		SourceID:                src.ID,
		LastCommittedSnapshotID: committedID,
		UpdatedAt:               now,
	}).Error; err != nil {
		t.Fatalf("create snapshot relation failed: %v", err)
	}
	if err := st.db.WithContext(ctx).Create(&documentEntity{
		TenantID:         src.TenantID,
		SourceID:         src.ID,
		SourceObjectID:   path,
		CoreDocumentID:   "core-doc-cloud-tree-1",
		DesiredVersionID: "v2",
		CurrentVersionID: "v1",
		LastModifiedAt:   &now,
		NextParseAt:      &now,
		ParseStatus:      "PENDING",
		OriginType:       string(model.OriginTypeCloudSync),
		OriginPlatform:   "FEISHU",
		OriginRef:        "node_cloud_auto_tree_updated",
		TriggerPolicy:    string(model.TriggerPolicyImmediate),
		UpdatedAt:        now,
	}).Error; err != nil {
		t.Fatalf("create pending cloud document failed: %v", err)
	}

	items := []model.TreeNode{{Title: "auto-updated.md", Key: path, IsDir: false}}
	stats := map[string]model.TreeFileStat{
		path: {Path: path, Size: 10, Checksum: "rev1", ModTime: &now},
	}
	tree, _, err := st.BuildTreeUpdateState(ctx, src.ID, items, stats)
	if err != nil {
		t.Fatalf("build non-watch cloud tree failed: %v", err)
	}
	node, ok := findTreeNodeByPath(tree, path)
	if !ok {
		t.Fatalf("missing node for path %s", path)
	}
	if node.UpdateType != "MODIFIED" {
		t.Fatalf("expected pending cloud document update to remain MODIFIED, got %s", node.UpdateType)
	}
	if node.StatusSource != "DOCUMENTS" {
		t.Fatalf("expected status_source DOCUMENTS, got %s", node.StatusSource)
	}
	if node.HasUpdate == nil || !*node.HasUpdate {
		t.Fatalf("expected has_update=true, got %+v", node.HasUpdate)
	}
}
