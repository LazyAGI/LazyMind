package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func resultReadDB(t *testing.T, ids ...string) *gorm.DB {
	t.Helper()
	db, cache := runningStatusDB(t, ids...)
	store.Init(db, nil, cache)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

func seedResult(t *testing.T, db *gorm.DB, id, status string) {
	t.Helper()
	seedRunningRecord(t, db, &orm.ChatHistory{ID: "reply-" + id, ConversationID: id, Seq: 1, RunID: "run-" + id, RunStatus: status})
}

func resultVersion(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	rows, err := conversationTerminalStatuses(t.Context(), db, []string{id})
	if err != nil || rows[id].Version == "" {
		t.Fatalf("read result version: rows=%v err=%v", rows, err)
	}
	return rows[id].Version
}

func queryResultRead(t *testing.T, owner, id string) bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/core/conversations:batchStatus", strings.NewReader(fmt.Sprintf(`{"conversation_ids":[%q]}`, id)))
	r.Header.Set("X-User-Id", owner)
	w := httptest.NewRecorder()
	BatchConversationStatus(w, r)
	var body struct {
		Statuses []struct {
			TerminalRead *bool `json:"terminal_read"`
		} `json:"statuses"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Statuses) != 1 || body.Statuses[0].TerminalRead == nil {
		t.Fatalf("missing authoritative terminal_read: HTTP %d %s", w.Code, w.Body.String())
	}
	return *body.Statuses[0].TerminalRead
}

func acknowledgeResult(t *testing.T, owner, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/core/conversations/"+id+":readResult", strings.NewReader(body))
	r = mux.SetURLVars(r, map[string]string{"conversation_id": id})
	if owner != "" {
		r.Header.Set("X-User-Id", owner)
	}
	w := httptest.NewRecorder()
	ReadConversationResult(w, r)
	return w
}

func readVersionBody(version string) string { return fmt.Sprintf(`{"terminal_version":%q}`, version) }

func TestResultReadBaselineIncludesOldResultsAndSurvivesRestart(t *testing.T) {
	db := resultReadDB(t, "completed", "failed", "canceled", "archived", "other", "running", "child")
	for _, id := range []string{"completed", "failed", "canceled", "archived", "other", "child"} {
		status := id
		if id == "archived" || id == "other" || id == "child" {
			status = "completed"
		}
		seedResult(t, db, id, status)
	}
	seedResult(t, db, "running", "generating")
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "archived").Update("archived_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "other").Update("create_user_id", "u2").Error; err != nil {
		t.Fatal(err)
	}
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "child-task", ConversationID: "child", TriggerHistoryID: "reply-child", AgentType: "research", Status: "running"})
	if err := InitializeConversationResultReads(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "archived").Update("archived_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"completed", "failed", "canceled", "archived"} {
		if !queryResultRead(t, "u1", id) {
			t.Errorf("old %s result is unread", id)
		}
	}
	if !queryResultRead(t, "u2", "other") {
		t.Error("baseline lost other owner's result")
	}
	if err := db.Model(&orm.ChatHistory{}).Where("conversation_id IN ?", []string{"completed", "running"}).Updates(map[string]any{"run_id": "new-run", "run_status": "completed"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "child-task").Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	// Re-enter the initializer as another server startup would. New results must
	// remain unread, including results produced while the user was offline.
	if err := InitializeConversationResultReads(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"completed", "running", "child"} {
		if queryResultRead(t, "u1", id) {
			t.Errorf("new %s result was swallowed by baseline", id)
		}
	}
	if !queryResultRead(t, "u1", "failed") {
		t.Error("unchanged baseline disappeared")
	}
}

func TestResultReadBaselineHandlesMultipleBatchesAndConcurrentStarts(t *testing.T) {
	db := resultReadDB(t)
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("conv-%03d", i)
		seedRunningRecord(t, db, &orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u1"}})
		seedResult(t, db, id, "completed")
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; done <- InitializeConversationResultReads(t.Context(), db) }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&orm.ConversationResultRead{}).Count(&count).Error; err != nil || count != 205 {
		t.Fatalf("baseline count=%d err=%v", count, err)
	}
	for _, id := range []string{"conv-000", "conv-100", "conv-204"} {
		if !queryResultRead(t, "u1", id) {
			t.Errorf("baseline omitted %s", id)
		}
	}
}

func TestResultReadBaselineRollbackAndRetry(t *testing.T) {
	db := resultReadDB(t, "a")
	seedResult(t, db, "a", "completed")
	var create, remove []string
	if db.Dialector.Name() == "postgres" {
		create = []string{`CREATE FUNCTION reject_result_read_baseline() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.initialized THEN RAISE EXCEPTION 'injected baseline commit failure'; END IF; RETURN NEW; END $$`, `CREATE TRIGGER reject_result_read_baseline BEFORE INSERT OR UPDATE ON conversation_result_read_state FOR EACH ROW EXECUTE FUNCTION reject_result_read_baseline()`}
		remove = []string{`DROP TRIGGER reject_result_read_baseline ON conversation_result_read_state`, `DROP FUNCTION reject_result_read_baseline()`}
	} else {
		for _, op := range []string{"INSERT", "UPDATE"} {
			name := "reject_baseline_" + strings.ToLower(op)
			create = append(create, "CREATE TRIGGER "+name+" BEFORE "+op+" ON conversation_result_read_state WHEN NEW.initialized = 1 BEGIN SELECT RAISE(ABORT, 'injected baseline commit failure'); END")
			remove = append(remove, "DROP TRIGGER "+name)
		}
	}
	for _, sql := range create {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := InitializeConversationResultReads(t.Context(), db); err == nil {
		t.Fatal("baseline failure was hidden")
	}
	var count int64
	if err := db.Model(&orm.ConversationResultRead{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial receipts persisted: count=%d err=%v", count, err)
	}
	if err := db.Model(&orm.ConversationResultReadState{}).Where("initialized = ?", true).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed baseline marked initialized: count=%d err=%v", count, err)
	}
	for _, sql := range remove {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := InitializeConversationResultReads(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if !queryResultRead(t, "u1", "a") {
		t.Fatal("baseline retry did not finish")
	}
}

func TestResultReadAcknowledgementIsDurableIdempotentAndVersionScoped(t *testing.T) {
	db := resultReadDB(t, "a")
	seedResult(t, db, "a", "completed")
	v1 := resultVersion(t, db, "a")
	if queryResultRead(t, "u1", "a") {
		t.Fatal("new result starts read")
	}
	for i := 0; i < 2; i++ {
		w := acknowledgeResult(t, "u1", "a", readVersionBody(v1))
		if w.Code != http.StatusNoContent {
			t.Fatalf("ack: %d %s", w.Code, w.Body.String())
		}
	}
	var count int64
	if err := db.Model(&orm.ConversationResultRead{}).Where("user_id = ? AND conversation_id = ? AND terminal_version = ?", "u1", "a", v1).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("durable receipt count=%d err=%v", count, err)
	}
	// Open another database handle: the read state must not live in process memory.
	fresh, err := gorm.Open(db.Dialector, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := fresh.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	store.Init(fresh, nil, store.State())
	if !queryResultRead(t, "u1", "a") {
		t.Fatal("receipt not visible through new database handle")
	}
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "reply-a").Update("run_id", "new-run").Error; err != nil {
		t.Fatal(err)
	}
	v2 := resultVersion(t, db, "a")
	if v2 == v1 {
		t.Fatal("new run retained the old version")
	}
	w := acknowledgeResult(t, "u1", "a", readVersionBody(v1))
	if w.Code != http.StatusConflict {
		t.Fatalf("stale confirmation: %d %s", w.Code, w.Body.String())
	}
	if queryResultRead(t, "u1", "a") {
		t.Fatal("stale request acknowledged new result")
	}
	if w := acknowledgeResult(t, "u1", "a", readVersionBody(v2)); w.Code != http.StatusNoContent {
		t.Fatalf("new confirmation: %d", w.Code)
	}
	_ = acknowledgeResult(t, "u1", "a", readVersionBody(v1))
	if !queryResultRead(t, "u1", "a") {
		t.Fatal("old request reverted new receipt")
	}
}

func TestResultReadAcknowledgementRejectsInaccessibleOrInvalidRequests(t *testing.T) {
	db := resultReadDB(t, "a", "foreign", "archived", "deleted", "ephemeral")
	for _, id := range []string{"a", "foreign", "archived", "deleted", "ephemeral"} {
		seedResult(t, db, id, "completed")
	}
	for id, patch := range map[string]map[string]any{"foreign": {"create_user_id": "u2"}, "archived": {"archived_at": time.Now()}, "deleted": {"deleted_at": time.Now()}, "ephemeral": {"is_ephemeral": true}} {
		if err := db.Model(&orm.Conversation{}).Where("id = ?", id).Updates(patch).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"foreign", "archived", "deleted", "ephemeral", "missing"} {
		version := strings.Repeat("a", 64)
		if id != "missing" {
			version = resultVersion(t, db, id)
		}
		if w := acknowledgeResult(t, "u1", id, readVersionBody(version)); w.Code != http.StatusNotFound {
			t.Errorf("%s: HTTP %d", id, w.Code)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"terminal_version":""}`, `{"terminal_version":1}`, `{"terminal_version":"v","extra":true}`, `{} {}`, readVersionBody(strings.Repeat("x", 33000))} {
		if w := acknowledgeResult(t, "u1", "a", body); w.Code != http.StatusBadRequest {
			t.Errorf("invalid body: HTTP %d", w.Code)
		}
	}
	var count int64
	if err := db.Model(&orm.ConversationResultRead{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected requests wrote receipts: count=%d err=%v", count, err)
	}
}

func TestResultReadConcurrentAcknowledgements(t *testing.T) {
	db := resultReadDB(t, "a")
	seedResult(t, db, "a", "completed")
	body := readVersionBody(resultVersion(t, db, "a"))
	start, done := make(chan struct{}), make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; done <- acknowledgeResult(t, "u1", "a", body).Code }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if status := <-done; status != http.StatusNoContent {
			t.Errorf("concurrent confirmation: HTTP %d", status)
		}
	}
	var count int64
	if err := db.Model(&orm.ConversationResultRead{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("concurrent receipts: count=%d err=%v", count, err)
	}
}

func TestResultReadAcknowledgementStorageFailureIsNotSuccess(t *testing.T) {
	db := resultReadDB(t, "a")
	seedResult(t, db, "a", "completed")
	body := readVersionBody(resultVersion(t, db, "a"))
	if err := db.Migrator().DropTable(&orm.ConversationResultRead{}); err != nil {
		t.Fatal(err)
	}
	w := acknowledgeResult(t, "u1", "a", body)
	if w.Code < 500 {
		t.Fatalf("storage failure reported success: HTTP %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "conversation_result_reads") {
		t.Fatal("database implementation leaked in error")
	}
	if err := db.AutoMigrate(&orm.ConversationResultRead{}); err != nil {
		t.Fatal(err)
	}
	if w := acknowledgeResult(t, "u1", "a", body); w.Code != http.StatusNoContent {
		t.Fatalf("retry: HTTP %d %s", w.Code, w.Body.String())
	}
	if !queryResultRead(t, "u1", "a") {
		t.Fatal("retry receipt missing")
	}
}

func isResultReadHistoryQuery(tx *gorm.DB) bool {
	return strings.Contains(tx.Statement.SQL.String(), "chat_histories")
}

func TestResultReadSnapshotBarrierRecognizesMetadataQuery(t *testing.T) {
	db := resultReadDB(t, "a")
	seedResult(t, db, "a", "completed")
	seen := false
	const callback = "test:verify_baseline_history_barrier"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if isResultReadHistoryQuery(tx) {
			seen = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callback) })
	_ = resultVersion(t, db, "a")
	if !seen {
		t.Fatal("snapshot barrier cannot recognize the real aliased history query")
	}
}

func TestResultReadBaselineUsesOnePostgresSnapshot(t *testing.T) {
	db := resultReadDB(t, "a")
	if db.Dialector.Name() != "postgres" {
		t.Skip("PostgreSQL snapshot isolation; SQLite concurrent initializers are covered separately")
	}
	seedResult(t, db, "a", "completed")
	seedRunningRecord(t, db, &orm.WorkflowSession{ID: "workflow", ConversationID: "a", TriggerHistoryID: "reply-a", Status: "active"})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	queried, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	const callback = "test:pause_baseline_after_history"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if isResultReadHistoryQuery(tx) {
			once.Do(func() {
				close(queried)
				select {
				case <-resume:
				case <-ctx.Done():
					tx.AddError(ctx.Err())
				}
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(callback) })
	go func() { done <- InitializeConversationResultReads(ctx, db) }()
	select {
	case err := <-done:
		t.Fatalf("initializer returned before scanning history: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-queried:
	}
	// A different connection completes work after the baseline snapshot exists,
	// but before the initializer has queried workflow metadata.
	err := db.WithContext(ctx).Model(&orm.WorkflowSession{}).Where("id = ?", "workflow").Update("status", "completed").Error
	close(resume)
	initErr := <-done
	if err != nil || initErr != nil {
		t.Fatalf("concurrent completion=%v baseline=%v", err, initErr)
	}
	if queryResultRead(t, "u1", "a") {
		t.Fatal("baseline swallowed a workflow completed after its snapshot")
	}
}
