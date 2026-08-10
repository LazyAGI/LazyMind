package externalagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type forkTransport struct {
	responses chan []byte
}

func newForkTransport() *forkTransport {
	return &forkTransport{responses: make(chan []byte, 4)}
}

func (f *forkTransport) ReadMessage() ([]byte, error) {
	return <-f.responses, nil
}

func (f *forkTransport) WriteMessage(payload []byte) error {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return err
	}
	if len(request.ID) == 0 {
		return nil
	}
	result := map[string]any{}
	if request.Method == "thread/fork" {
		result["thread"] = map[string]any{"id": "thread-2"}
	}
	response, err := json.Marshal(map[string]any{
		"id":     request.ID,
		"result": result,
	})
	if err != nil {
		return err
	}
	f.responses <- response
	return nil
}

func (f *forkTransport) Close() error { return nil }

type reconnectTransport struct {
	responses  chan []byte
	closed     chan struct{}
	closeOnce  sync.Once
	failMethod string
}

func newReconnectTransport(failMethod string) *reconnectTransport {
	return &reconnectTransport{
		responses:  make(chan []byte, 4),
		closed:     make(chan struct{}),
		failMethod: failMethod,
	}
}

func (f *reconnectTransport) ReadMessage() ([]byte, error) {
	select {
	case payload := <-f.responses:
		return payload, nil
	case <-f.closed:
		return nil, io.EOF
	}
}

func (f *reconnectTransport) WriteMessage(payload []byte) error {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return err
	}
	if request.Method == f.failMethod {
		f.failMethod = ""
		return io.ErrUnexpectedEOF
	}
	if len(request.ID) == 0 {
		return nil
	}
	result := map[string]any{}
	if request.Method == "thread/list" {
		result["data"] = []map[string]any{{"id": "thread-after-reconnect"}}
	}
	response, err := json.Marshal(map[string]any{
		"id": request.ID, "result": result,
	})
	if err != nil {
		return err
	}
	f.responses <- response
	return nil
}

func (f *reconnectTransport) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.ExternalAgentBinding{}, &orm.ExternalAgentRun{}, &orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestValidateRequestResponseDecisions(t *testing.T) {
	cases := []struct {
		kind    string
		payload string
		ok      bool
	}{
		{"command_approval", `{"decision":"accept"}`, true},
		{"command_approval", `{"decision":"nope"}`, false},
		{"file_change_approval", `{"decision":"acceptForSession"}`, true},
		{"permissions_approval", `{"permissions":{}}`, true},
		{"user_input", `{"answers":{"q1":"a"}}`, true},
	}
	for _, tc := range cases {
		err := validateRequestResponse(tc.kind, json.RawMessage(tc.payload))
		if tc.ok && err != nil {
			t.Fatalf("%s should be valid: %v", tc.kind, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s should be invalid", tc.kind)
		}
	}
}

func TestBindIsIdempotent(t *testing.T) {
	db := testDB(t)
	service := &Service{db: db, byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{}, requests: map[string]*pendingRequest{}, loaded: map[string]int64{}}
	first, err := service.Bind(context.Background(), BindInput{
		Provider: ProviderCodex, ProviderThreadID: "thread-1", ConversationID: "conv-1",
		CreatedByUserID: "u1", Managed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Bind(context.Background(), BindInput{
		Provider: ProviderCodex, ProviderThreadID: "thread-1", ConversationID: "conv-2",
		CreatedByUserID: "u2", Managed: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.ConversationID != "conv-1" {
		t.Fatalf("binding not idempotent: %#v %#v", first, second)
	}
}

func TestCompletedExecutionReplaysTerminal(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	history := orm.ChatHistory{
		ID: "h1", Seq: 1, ConversationID: "c1", Content: "hi", Result: "done",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	run := orm.ExternalAgentRun{
		ID: "r1", RequestID: "req-1", ConversationID: "c1", HistoryID: "h1",
		Provider: ProviderCodex, ProviderThreadID: "t1", ActorUserID: "u1",
		Action: "start", Status: runStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{db: db, byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{}, requests: map[string]*pendingRequest{}, loaded: map[string]int64{}}
	execution, ok, err := service.completedExecution(context.Background(), ChatInput{
		Provider: ProviderCodex, RequestID: "req-1", ActorUserID: "u1", ConversationID: "c1", ProviderThreadID: "t1",
	})
	if err != nil || !ok {
		t.Fatalf("completedExecution: ok=%v err=%v", ok, err)
	}
	event := <-execution.Events
	if !event.Terminal || event.Type != "turn_completed" || event.Message != "done" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestManagedRunSubscribeReplaysBuffer(t *testing.T) {
	run := newManagedRun(orm.ExternalAgentRun{ID: "r1", HistoryID: "h1"}, "q", 1)
	if got := run.appendAnswer("hello "); got != "hello " {
		t.Fatalf("first cumulative answer = %q", got)
	}
	if got := run.appendAnswer("world"); got != "hello world" {
		t.Fatalf("second cumulative answer = %q", got)
	}
	run.broadcast(Event{Type: "progress", Summary: "one"})
	run.broadcast(Event{Type: "agent_message_delta", Delta: "hi", Message: "hi"})
	ch := run.subscribe()
	first := <-ch
	second := <-ch
	if first.Type != "progress" || second.Delta != "hi" {
		t.Fatalf("unexpected replay: %#v %#v", first, second)
	}
}

func TestFinishActiveClearsLoadedThread(t *testing.T) {
	record := orm.ExternalAgentRun{
		ID:               "run-1",
		Provider:         ProviderCodex,
		ProviderThreadID: "thread-1",
		RequestID:        "request-1",
	}
	run := newManagedRun(record, "query", 1)
	service := &Service{
		byThread: map[string]*managedRun{
			"thread-1": run,
		},
		byRequest: map[string]*managedRun{
			ProviderCodex + "\x00request-1": run,
		},
		requests: map[string]*pendingRequest{},
		loaded:   map[string]int64{"thread-1": 1},
	}

	service.finishActive(run)

	if _, ok := service.loaded["thread-1"]; ok {
		t.Fatal("loaded thread was not cleared")
	}
	if _, ok := service.byThread["thread-1"]; ok {
		t.Fatal("active thread was not cleared")
	}
}

func TestForkBusyThreadRebindsRunAndConversation(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	binding := orm.ExternalAgentBinding{
		ID: "binding-1", ConversationID: "conversation-1", Provider: ProviderCodex,
		ProviderThreadID: "thread-1", CreatedByUserID: "user-1",
		CreatedAt: now, UpdatedAt: now,
	}
	record := orm.ExternalAgentRun{
		ID: "run-1", RequestID: "request-1", ConversationID: "conversation-1",
		HistoryID: "history-1", Provider: ProviderCodex, ProviderThreadID: "thread-1",
		ActorUserID: "user-1", Status: runStatusStarting, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	run := newManagedRun(record, "query", 1)
	transport := newForkTransport()
	client := &CodexClient{
		factory: func() (messageTransport, error) { return transport, nil },
		events:  make(chan rpcMessage, 4),
		pending: make(map[string]chan rpcMessage),
	}
	service := &Service{
		db: db, client: client,
		byThread: map[string]*managedRun{"thread-1": run},
		byRequest: map[string]*managedRun{
			ProviderCodex + "\x00request-1": run,
		},
		requests: map[string]*pendingRequest{},
		loaded:   map[string]int64{},
	}

	if err := service.forkBusyThread(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	var updatedBinding orm.ExternalAgentBinding
	if err := db.Where("id = ?", binding.ID).First(&updatedBinding).Error; err != nil {
		t.Fatal(err)
	}
	if updatedBinding.ProviderThreadID != "thread-2" || !updatedBinding.ManagedByLazyMind {
		t.Fatalf("binding not moved to fork: %#v", updatedBinding)
	}
	var updatedRun orm.ExternalAgentRun
	if err := db.Where("id = ?", record.ID).First(&updatedRun).Error; err != nil {
		t.Fatal(err)
	}
	if updatedRun.ProviderThreadID != "thread-2" {
		t.Fatalf("run not moved to fork: %#v", updatedRun)
	}
	if service.byThread["thread-2"] != run || service.byThread["thread-1"] != nil {
		t.Fatalf("active run map not moved: %#v", service.byThread)
	}
	event := <-run.subscribe()
	if event.Type != "thread_forked" || event.ThreadID != "thread-2" {
		t.Fatalf("unexpected fork event: %#v", event)
	}
}

func TestIsActiveWriterError(t *testing.T) {
	if !isActiveWriterError(
		fmt.Errorf("request failed: thread already has an active writer"),
	) {
		t.Fatal("active writer error was not detected")
	}
	if isActiveWriterError(fmt.Errorf("request failed: unrelated")) {
		t.Fatal("unrelated error was detected as an active writer")
	}
}

func TestClientReconnectsAfterTransportFailure(t *testing.T) {
	first := newReconnectTransport("thread/list")
	second := newReconnectTransport("")
	transports := []messageTransport{first, second}
	created := 0
	client := &CodexClient{
		factory: func() (messageTransport, error) {
			transport := transports[created]
			created++
			return transport, nil
		},
		events:  make(chan rpcMessage, 4),
		pending: make(map[string]chan rpcMessage),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var page ThreadPage
	if err := client.Call(ctx, "thread/list", map[string]any{"limit": 1}, &page); err != nil {
		t.Fatal(err)
	}
	if created != 2 || len(page.Data) != 1 || page.Data[0].ID != "thread-after-reconnect" {
		t.Fatalf("reconnect failed: created=%d page=%#v", created, page)
	}
}

func TestDisconnectKeepsManagedRunRecoverable(t *testing.T) {
	record := orm.ExternalAgentRun{
		ID: "run-1", Provider: ProviderCodex,
		ProviderThreadID: "thread-1", ProviderTurnID: "turn-1",
		Status: runStatusRunning,
	}
	run := newManagedRun(record, "query", 1)
	service := &Service{byThread: map[string]*managedRun{"thread-1": run}}
	service.notifyActiveRunsDisconnected()
	event := <-run.subscribe()
	if event.Type != "progress" || event.Terminal || run.finished() {
		t.Fatalf("disconnect terminated recoverable run: %#v", event)
	}
}

func TestDisconnectInterruptsUnrecoverablePendingRequest(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	record := orm.ExternalAgentRun{
		ID: "run-waiting", RequestID: "run-request",
		ConversationID: "conversation-1", HistoryID: "history-1",
		Provider: ProviderCodex, ProviderThreadID: "thread-1",
		ProviderTurnID: "turn-1", ActorUserID: "user-1",
		Status: runStatusWaiting, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID: "history-1", ConversationID: "conversation-1", Seq: 1,
		Content: "query", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	run := newManagedRun(record, "query", 1)
	service := &Service{
		db: db, byThread: map[string]*managedRun{"thread-1": run},
		byRequest: map[string]*managedRun{ProviderCodex + "\x00run-request": run},
		requests: map[string]*pendingRequest{
			"approval-1": {ID: "approval-1", Run: run},
		},
		loaded: map[string]int64{},
	}
	service.notifyActiveRunsDisconnected()
	event := <-run.subscribe()
	if event.Type != "turn_interrupted" || !event.Terminal {
		t.Fatalf("pending request was left hanging: %#v", event)
	}
}

func TestCodexConfigurationFailureDoesNotRetryUntilTimeout(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	client := NewCodexClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	err := client.Call(ctx, "thread/list", map[string]any{}, &map[string]any{})
	if !isCodexConfigurationError(err) {
		t.Fatalf("Call error = %v, want codexConfigurationError", err)
	}
	if !strings.Contains(err.Error(), "install and sign in") {
		t.Fatalf("Call error = %q, want actionable install guidance", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("configuration failure retried instead of failing immediately: %s", time.Since(started))
	}
}
