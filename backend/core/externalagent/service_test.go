package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

type scriptedTransport struct {
	responses chan []byte
	mu        sync.Mutex
	calls     []rpcMessage
	results   map[string][]any
}

type scriptedRPCError struct {
	Code    int
	Message string
}

type releaseBarrierTransport struct {
	responses          chan []byte
	unsubscribeStarted chan struct{}
	allowUnsubscribe   chan struct{}
	startOnce          sync.Once
}

func TestNewServiceFailsClosedWhenOwnershipRecoveryFails(t *testing.T) {
	db := testDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(db, nil); err == nil {
		t.Fatal("service became ready without recovering external-agent ownership")
	}
}

func newReleaseBarrierTransport() *releaseBarrierTransport {
	return &releaseBarrierTransport{
		responses:          make(chan []byte, 4),
		unsubscribeStarted: make(chan struct{}),
		allowUnsubscribe:   make(chan struct{}),
	}
}

func (t *releaseBarrierTransport) ReadMessage() ([]byte, error) {
	return <-t.responses, nil
}

func (t *releaseBarrierTransport) WriteMessage(payload []byte) error {
	var request rpcMessage
	if err := json.Unmarshal(payload, &request); err != nil {
		return err
	}
	if len(request.ID) == 0 {
		return nil
	}
	result := any(map[string]any{})
	if request.Method == "thread/unsubscribe" {
		t.startOnce.Do(func() { close(t.unsubscribeStarted) })
		<-t.allowUnsubscribe
		result = map[string]any{"status": "unsubscribed"}
	}
	response, err := json.Marshal(map[string]any{"id": request.ID, "result": result})
	if err != nil {
		return err
	}
	t.responses <- response
	return nil
}

func (t *releaseBarrierTransport) Close() error { return nil }

func newScriptedTransport(results map[string][]any) *scriptedTransport {
	return &scriptedTransport{
		responses: make(chan []byte, 16),
		results:   results,
	}
}

func (s *scriptedTransport) ReadMessage() ([]byte, error) {
	return <-s.responses, nil
}

func (s *scriptedTransport) WriteMessage(payload []byte) error {
	var request rpcMessage
	if err := json.Unmarshal(payload, &request); err != nil {
		return err
	}
	if len(request.ID) == 0 {
		return nil
	}
	s.mu.Lock()
	s.calls = append(s.calls, request)
	result := any(map[string]any{})
	if results, configured := s.results[request.Method]; configured {
		if len(results) == 0 {
			s.mu.Unlock()
			return fmt.Errorf("unexpected extra call to %s", request.Method)
		}
		result = results[0]
		s.results[request.Method] = results[1:]
	}
	s.mu.Unlock()
	responseBody := map[string]any{"id": request.ID, "result": result}
	if rpcErr, ok := result.(scriptedRPCError); ok {
		delete(responseBody, "result")
		responseBody["error"] = map[string]any{
			"code": rpcErr.Code, "message": rpcErr.Message,
		}
	}
	response, err := json.Marshal(responseBody)
	if err != nil {
		return err
	}
	s.responses <- response
	return nil
}

func (s *scriptedTransport) Close() error { return nil }

func (s *scriptedTransport) lastParams(t *testing.T, method string) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := len(s.calls) - 1; index >= 0; index-- {
		if s.calls[index].Method != method {
			continue
		}
		var params map[string]any
		if err := json.Unmarshal(s.calls[index].Params, &params); err != nil {
			t.Fatal(err)
		}
		return params
	}
	t.Fatalf("method %s was not called", method)
	return nil
}

func clientForTransport(transport messageTransport) *CodexClient {
	return &CodexClient{
		factory: func() (messageTransport, error) { return transport, nil },
		events:  make(chan rpcMessage, 16),
		pending: make(map[string]chan rpcMessage),
	}
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
	if err := db.AutoMigrate(&orm.ExternalAgentBinding{}, &orm.ExternalAgentRun{}, &orm.ExternalAgentOperation{}, &orm.Conversation{}, &orm.ChatHistory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestExternalAgentOperationReceiptIsAtMostOnce(t *testing.T) {
	service := &Service{db: testDB(t)}
	request := map[string]any{"cwd": "/repo"}
	if result, claimed, err := service.ClaimOperation(
		context.Background(), "user-1", "operation-1", "create_thread", request,
	); err != nil || !claimed || len(result) != 0 {
		t.Fatalf("first claim = (%s, %v, %v)", result, claimed, err)
	}
	if _, claimed, err := service.ClaimOperation(
		context.Background(), "user-1", "operation-1", "create_thread", request,
	); !errors.Is(err, ErrOperationPending) || claimed {
		t.Fatalf("concurrent claim = (%v, %v), want pending", claimed, err)
	}
	want := map[string]any{"thread_id": "thread-1"}
	if err := service.CompleteOperation(
		context.Background(), "user-1", "operation-1", "create_thread", want,
	); err != nil {
		t.Fatal(err)
	}
	result, claimed, err := service.ClaimOperation(
		context.Background(), "user-1", "operation-1", "create_thread", request,
	)
	if err != nil || claimed {
		t.Fatalf("replay claim = (%v, %v)", claimed, err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil || got["thread_id"] != "thread-1" {
		t.Fatalf("replayed result = %s, err=%v", result, err)
	}
	if _, _, err := service.ClaimOperation(
		context.Background(), "user-1", "operation-1", "create_thread", map[string]any{"cwd": "/other"},
	); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("mismatched replay = %v, want request mismatch", err)
	}
}

func TestProjectExternalRequestActionsMapExactProviderResponses(t *testing.T) {
	cases := []struct {
		kind         string
		request      string
		actionKind   string
		wantResponse string
	}{
		{"command_approval", `{"command":"pwd"}`, "allow_once", `{"decision":"accept"}`},
		{"command_approval", `{"command":"git status","availableDecisions":[{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git status"]}}]}`, "policy", `{"decision":{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git status"]}}}`},
		{"file_change_approval", `{"itemId":"item-1","changes":[{"path":"README.md","kind":{"type":"update"},"diff":"+ok"}]}`, "deny", `{"decision":"decline"}`},
		{"permissions_approval", `{"permissions":{"network":{"enabled":true}}}`, "grant_turn", `{"permissions":{"network":{"enabled":true}},"scope":"turn"}`},
	}
	for _, tc := range cases {
		view, responses := projectExternalRequest(
			"request-1", tc.kind, "summary", json.RawMessage(tc.request),
		)
		var actionID string
		for _, action := range view.Actions {
			if action.Kind == tc.actionKind {
				actionID = action.ID
				break
			}
		}
		if actionID == "" {
			t.Fatalf("%s missing %s action: %#v", tc.kind, tc.actionKind, view)
		}
		if got := string(responses[actionID]); got != tc.wantResponse {
			t.Fatalf("%s response=%s, want %s", tc.kind, got, tc.wantResponse)
		}
	}
}

func TestProjectExternalRequestRejectsInvalidProviderChoices(t *testing.T) {
	view, responses := projectExternalRequest(
		"request-1", "command_approval", "summary",
		json.RawMessage(`{"availableDecisions":["accept","unknown"]}`),
	)
	if view.Error == "" || len(view.Actions) != 0 || len(responses) != 0 {
		t.Fatalf("invalid provider choices were exposed: view=%#v responses=%#v", view, responses)
	}
}

func TestProjectCommandRequestFailsClosedWithoutReviewableDetails(t *testing.T) {
	for _, payload := range []string{
		`{}`,
		`{"command":123}`,
		`{"command":"pwd","cwd":123}`,
		`{"command":"pwd","additionalPermissions":{"value":"` + strings.Repeat("x", 3000) + `"}}`,
		`{"networkApprovalContext":"api.example.com"}`,
		`{"networkApprovalContext":{"host":"api.example.com"},"proposedExecpolicyAmendment":"` + strings.Repeat("x", 4000) + `"}`,
	} {
		view, _ := projectExternalRequest(
			"request-1", "command_approval", "summary", json.RawMessage(payload),
		)
		if view.Error == "" {
			t.Fatalf("payload should fail closed: %s", payload)
		}
		for _, action := range view.Actions {
			if action.Kind != "deny" {
				t.Fatalf("payload=%s exposed positive action %#v", payload, action)
			}
		}
	}
}

func TestProjectCommandRequestDisclosesNetworkAndPolicyAmendment(t *testing.T) {
	payload := json.RawMessage(`{
		"networkApprovalContext":{"host":"api.example.com","protocol":"https"},
		"availableDecisions":[
			{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git status","git diff"]}},
			"decline"
		]
	}`)
	view, _ := projectExternalRequest(
		"request-1", "command_approval", "summary", payload,
	)
	if view.Error != "" {
		t.Fatalf("network-only request was rejected: %#v", view)
	}
	var disclosed bool
	for _, field := range view.Fields {
		if strings.HasPrefix(field.Kind, "策略 ") && strings.Contains(field.Value, "git diff") {
			disclosed = true
		}
	}
	if !disclosed {
		t.Fatalf("policy amendment was not disclosed: %#v", view.Fields)
	}
}

func TestProjectCommandRequestBindsEachPolicyButtonToItsDisclosure(t *testing.T) {
	payload := json.RawMessage(`{
		"command":"",
		"networkApprovalContext":{"host":"api.example.com"},
		"availableDecisions":[
			{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git status"]}},
			{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git diff"]}},
			"decline"
		]
	}`)
	view, _ := projectExternalRequest(
		"request-1", "command_approval", "summary", payload,
	)
	if view.Error != "" {
		t.Fatalf("valid network-only request was rejected: %#v", view)
	}
	policyActions := make([]ExternalRequestAction, 0, 2)
	for _, action := range view.Actions {
		if action.Kind == "policy" {
			policyActions = append(policyActions, action)
		}
	}
	if len(policyActions) != 2 ||
		!strings.Contains(policyActions[0].Label, "策略 1") ||
		!strings.Contains(policyActions[0].Label, "git status") ||
		!strings.Contains(policyActions[1].Label, "策略 2") ||
		!strings.Contains(policyActions[1].Label, "git diff") {
		t.Fatalf("policy buttons are not uniquely disclosed: %#v", policyActions)
	}
	for index, field := range view.Fields[len(view.Fields)-2:] {
		want := fmt.Sprintf("策略 %d", index+1)
		if !strings.Contains(field.Kind, want) {
			t.Fatalf("policy field %#v does not match %s", field, want)
		}
	}
}

func TestRespondRequestHTTPRejectsOversizedAndTrailingBodies(t *testing.T) {
	for _, body := range []string{
		`{} {}`,
		`{"decision":"` + strings.Repeat("x", 64<<10) + `"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/external-agent-requests/request-1:respond", strings.NewReader(body))
		response := httptest.NewRecorder()
		RespondRequestHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body length=%d status=%d, want %d", len(body), response.Code, http.StatusBadRequest)
		}
	}
}

func TestRequestTimeoutResponseHonorsAvailableDecisions(t *testing.T) {
	cases := []struct {
		payload  string
		decision string
		ok       bool
	}{
		{`{"command":"pwd"}`, "decline", true},
		{`{"availableDecisions":["cancel"]}`, "cancel", true},
		{`{"availableDecisions":["accept","decline"]}`, "decline", true},
		{`{"availableDecisions":[]}`, "", false},
		{`{"availableDecisions":{}}`, "", false},
	}
	for _, tc := range cases {
		response, ok := requestTimeoutResponse(
			"command_approval",
			json.RawMessage(tc.payload),
		)
		if ok != tc.ok {
			t.Fatalf("payload=%s ok=%v, want %v", tc.payload, ok, tc.ok)
		}
		decision, _ := response.(map[string]any)["decision"].(string)
		if decision != tc.decision {
			t.Fatalf("payload=%s decision=%q, want %q", tc.payload, decision, tc.decision)
		}
	}
}

func TestPermissionTimeoutResponseMatchesStrictContract(t *testing.T) {
	response, ok := requestTimeoutResponse("permissions_approval", nil)
	if !ok {
		t.Fatal("permission timeout response was not available")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"permissions":{},"scope":"turn"}` {
		t.Fatalf("permission timeout response=%s", encoded)
	}
}

func TestValidateExternalRequestAnswersCountsUnicodeCharacters(t *testing.T) {
	answer := strings.Repeat("中", 1000)
	err := validateExternalRequestAnswers(
		[]ExternalRequestQuestion{{ID: "q1", Question: "Answer"}},
		map[string]ExternalRequestAnswer{"q1": {Answers: []string{answer}}},
	)
	if err != nil {
		t.Fatalf("1000-character answer should be valid: %v", err)
	}
}

func TestRequestSummaryIsBounded(t *testing.T) {
	command := strings.Repeat("命", 2000)
	summary := requestSummary(
		"command_approval",
		json.RawMessage(`{"command":"`+command+`"}`),
	)
	if got := len([]rune(summary)); got != 1000 {
		t.Fatalf("summary runes=%d, want 1000", got)
	}
	if !strings.HasSuffix(summary, "…") {
		t.Fatalf("summary=%q, want ellipsis suffix", summary)
	}
}

func TestExpiredRequestRemainsPendingWhenProviderResponseFails(t *testing.T) {
	transport := newReconnectTransport("")
	client := clientForTransport(transport)
	client.transport = transport
	run := newManagedRun(orm.ExternalAgentRun{}, "", 0)
	request := &pendingRequest{
		ID:        "request-1",
		RPCID:     json.RawMessage(`1`),
		Kind:      "command_approval",
		Payload:   json.RawMessage(`{"availableDecisions":["decline"]}`),
		Run:       run,
		ExpiresAt: time.Now(),
	}
	service := &Service{
		client:   client,
		requests: map[string]*pendingRequest{request.ID: request},
	}
	service.expireRequest(request)
	if service.requests[request.ID] != request {
		t.Fatal("failed timeout response removed the pending request")
	}
}

func TestRequestResolutionDoesNotReviveTerminalRun(t *testing.T) {
	db := testDB(t)
	record := orm.ExternalAgentRun{
		ID:             "terminal-run",
		ConversationID: "conversation-1",
		Status:         runStatusCompleted,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	run := newManagedRun(record, "", 0)
	service := &Service{db: db}
	service.markRequestResolved(&pendingRequest{
		ID:  "request-1",
		Run: run,
	}, "request resolved")
	var persisted orm.ExternalAgentRun
	if err := db.First(&persisted, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != runStatusCompleted {
		t.Fatalf("status=%q, want %q", persisted.Status, runStatusCompleted)
	}
}

func TestInteractiveRequestsRoundTripThroughService(t *testing.T) {
	cases := []struct {
		method     string
		kind       string
		params     string
		actionKind string
		answers    map[string]ExternalRequestAnswer
	}{
		{
			"item/commandExecution/requestApproval",
			"command_approval",
			`{"command":"pwd"}`,
			"allow_once",
			nil,
		},
		{
			"item/fileChange/requestApproval",
			"file_change_approval",
			`{"reason":"update requested file","itemId":"item-1","changes":[{"path":"README.md","kind":{"type":"update"},"diff":"+ok"}]}`,
			"allow_once",
			nil,
		},
		{
			"item/permissions/requestApproval",
			"permissions_approval",
			`{"reason":"network required","permissions":{"network":{"enabled":true}}}`,
			"grant_turn",
			nil,
		},
		{
			"item/tool/requestUserInput",
			"user_input",
			`{"questions":[{"id":"color","question":"Pick a color"}]}`,
			"submit",
			map[string]ExternalRequestAnswer{"color": {Answers: []string{"Blue"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			db := testDB(t)
			now := time.Now()
			record := orm.ExternalAgentRun{
				ID: "run-" + tc.kind, RequestID: "request-" + tc.kind,
				ConversationID: "conversation-1", HistoryID: "history-1",
				Provider: ProviderCodex, ProviderThreadID: "thread-1",
				ProviderTurnID: "turn-1", ActorUserID: "user-1",
				Status: runStatusRunning, CreatedAt: now, UpdatedAt: now,
			}
			if err := db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			transport := newScriptedTransport(nil)
			client := clientForTransport(transport)
			client.transport = transport
			run := newManagedRun(record, "query", 1)
			events := run.subscribe()
			service := &Service{
				db: db, client: client,
				requests: map[string]*pendingRequest{},
			}
			service.handleServerRequest(run, rpcMessage{
				ID: json.RawMessage("77"), Method: tc.method,
				Params: json.RawMessage(tc.params),
			})
			required := <-events
			if required.Type != "request_required" || required.Request == nil ||
				required.Request.Kind != tc.kind || required.Request.RequestID == "" {
				t.Fatalf("unexpected request event: %#v", required)
			}
			var actionID string
			for _, action := range required.Request.Actions {
				if action.Kind == tc.actionKind {
					actionID = action.ID
					break
				}
			}
			if actionID == "" {
				t.Fatalf("request missing %s action: %#v", tc.actionKind, required.Request)
			}
			if err := service.RespondRequest(RequestResponse{
				RequestID: required.Request.RequestID, ActionID: actionID,
				Answers: tc.answers, ActorUserID: "user-1",
			}); err != nil {
				t.Fatal(err)
			}
			resolved := <-events
			if resolved.Type != "progress" || resolved.Status != runStatusRunning {
				t.Fatalf("unexpected resolved event: %#v", resolved)
			}
			service.mu.Lock()
			_, stillPending := service.requests[required.Request.RequestID]
			service.mu.Unlock()
			if stillPending {
				t.Fatal("request remained pending after response")
			}
		})
	}
}

func TestListThreadsPassesExactCWD(t *testing.T) {
	available := true
	transport := newScriptedTransport(map[string][]any{
		"thread/list": {map[string]any{
			"data": []map[string]any{
				{
					"id": "thread-1", "cwd": "/tmp/workspace/project",
					"status":               map[string]any{"type": "idle"},
					"canAcceptDirectInput": available,
				},
				{"id": "thread-1", "cwd": "/tmp/workspace/project"},
				{"id": "thread-foreign", "cwd": "/tmp/workspace/other"},
			},
		}},
	})
	service := &Service{
		db: testDB(t), client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	page, err := service.ListThreads(
		context.Background(), "", "/tmp/workspace/project", 20, "user-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || !page.Data[0].Available {
		t.Fatalf("unexpected thread page: %#v", page)
	}
	if got := transport.lastParams(t, "thread/list")["cwd"]; got != "/tmp/workspace/project" {
		t.Fatalf("cwd = %#v, want exact project path", got)
	}
}

func TestListThreadsRejectsInvalidCursor(t *testing.T) {
	service := &Service{
		db: testDB(t), client: clientForTransport(newScriptedTransport(nil)),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	_, err := service.ListThreads(
		context.Background(), "not-an-offset", "/workspace/a", 20, "user-1",
	)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("ListThreads error = %v, want ErrInvalidCursor", err)
	}
}

func TestReadThreadOmitsIncludeTurnsBeforeFirstMessage(t *testing.T) {
	transport := newScriptedTransport(map[string][]any{
		"thread/read": {map[string]any{
			"thread": map[string]any{
				"id":                   "thread-new",
				"status":               map[string]any{"type": "notLoaded"},
				"canAcceptDirectInput": true,
			},
		}},
	})
	service := &Service{
		db:       testDB(t),
		client:   clientForTransport(transport),
		byThread: map[string]*managedRun{},
	}

	thread, err := service.readThread(context.Background(), "thread-new", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread-new" || !thread.Available {
		t.Fatalf("unexpected thread: %#v", thread)
	}
	params := transport.lastParams(t, "thread/read")
	if _, exists := params["includeTurns"]; exists {
		t.Fatalf("includeTurns must be omitted before the first message: %#v", params)
	}
}

func TestReadThreadPageFallsBackForUnmaterializedBoundThread(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "binding-new", ConversationID: "conversation-new",
		Provider: ProviderCodex, ProviderThreadID: "thread-new",
		ManagedByLazyMind: true, CreatedByUserID: "user-1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/read": {
			scriptedRPCError{
				Code:    -32600,
				Message: "thread thread-new is not materialized yet; includeTurns is unavailable before first user message",
			},
			map[string]any{"thread": map[string]any{
				"id": "thread-new", "cwd": "/workspace/new",
				"status":               map[string]any{"type": "notLoaded"},
				"canAcceptDirectInput": true,
			}},
		},
	})
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}

	page, err := service.ReadThreadPage(
		context.Background(), "thread-new", 0, 1, true, "user-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalTurns != 0 || len(page.Turns) != 0 {
		t.Fatalf("unmaterialized thread exposed turns: %#v", page)
	}
	if !page.Thread.Available || page.Thread.ConversationID != "conversation-new" {
		t.Fatalf("unexpected unmaterialized thread projection: %#v", page.Thread)
	}
	transport.mu.Lock()
	calls := make([]rpcMessage, 0, 2)
	for _, call := range transport.calls {
		if call.Method == "thread/read" {
			calls = append(calls, call)
		}
	}
	transport.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("thread/read calls = %d, want 2", len(calls))
	}
	var first, second map[string]any
	if err := json.Unmarshal(calls[0].Params, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(calls[1].Params, &second); err != nil {
		t.Fatal(err)
	}
	if first["includeTurns"] != true {
		t.Fatalf("first read did not request turns: %#v", first)
	}
	if _, exists := second["includeTurns"]; exists {
		t.Fatalf("fallback read still requested turns: %#v", second)
	}
}

func TestReadThreadPageDoesNotHideOtherReadErrors(t *testing.T) {
	transport := newScriptedTransport(map[string][]any{
		"thread/read": {scriptedRPCError{Code: -32600, Message: "invalid thread/read request"}},
	})
	service := &Service{
		db: testDB(t), client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}

	_, err := service.ReadThreadPage(
		context.Background(), "thread-invalid", 0, 1, true, "user-1",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid thread/read request") {
		t.Fatalf("unexpected read error: %v", err)
	}
	transport.mu.Lock()
	calls := make([]rpcMessage, 0, 1)
	for _, call := range transport.calls {
		if call.Method == "thread/read" {
			calls = append(calls, call)
		}
	}
	transport.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("thread/read calls = %d, want 1", len(calls))
	}
}

func TestReadThreadPageReportsLightweightFallbackFailure(t *testing.T) {
	transport := newScriptedTransport(map[string][]any{
		"thread/read": {
			scriptedRPCError{
				Code:    -32600,
				Message: "thread thread-new is not materialized yet; includeTurns is unavailable before first user message",
			},
			scriptedRPCError{Code: -32001, Message: "lightweight read denied"},
		},
	})
	service := &Service{
		db: testDB(t), client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}

	_, err := service.ReadThreadPage(
		context.Background(), "thread-new", 0, 1, true, "user-1",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "is not materialized yet") ||
		!strings.Contains(err.Error(), "lightweight read denied") {
		t.Fatalf("fallback error lost diagnostic context: %v", err)
	}
}

func TestReadThreadPageReturnsTailSummariesAndSnapshot(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "binding-1", ConversationID: "conversation-1",
		Provider: ProviderCodex, ProviderThreadID: "thread-1",
		CreatedByUserID: "user-1", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	turn := func(question, answer string) map[string]any {
		return map[string]any{"items": []map[string]any{
			{"type": "userMessage", "content": question},
			{"type": "agentMessage", "text": answer, "phase": "final_answer"},
		}}
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/read": {map[string]any{"thread": map[string]any{
			"id": "thread-1", "cwd": "/workspace/a",
			"turns": []map[string]any{turn("question 1", "answer 1"), turn("question 2", "answer 2")},
		}}},
	})
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	page, err := service.ReadThreadPage(
		context.Background(), "thread-1", 0, 1, true, "user-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Offset != 1 || page.TotalTurns != 2 || page.HasMore {
		t.Fatalf("unexpected tail page: %#v", page)
	}
	if len(page.Turns) != 2 || page.Turns[0] != (TurnSummary{Role: "user", Text: "question 2"}) || page.Turns[1] != (TurnSummary{Role: "assistant", Text: "answer 2"}) {
		t.Fatalf("unexpected summaries: %#v", page.Turns)
	}
	if page.Snapshot == nil || page.Snapshot.ConversationID != "conversation-1" || page.Snapshot.Status != "idle" {
		t.Fatalf("unexpected snapshot: %#v", page.Snapshot)
	}
}

func TestListProjectsFiltersOtherUsersBindings(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "binding-secret", ConversationID: "conversation-secret",
		Provider: ProviderCodex, ProviderThreadID: "thread-secret",
		CreatedByUserID: "user-2", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "binding-shared-project", ConversationID: "conversation-shared-project",
		Provider: ProviderCodex, ProviderThreadID: "thread-shared-project",
		CreatedByUserID: "user-2", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/list": {
			map[string]any{
				"data": []map[string]any{
					{"id": "thread-a", "cwd": "/workspace/a", "updatedAt": 20},
					{"id": "thread-b", "cwd": "/workspace/a", "updatedAt": 10},
					{"id": "thread-shared-project", "cwd": "/workspace/a", "updatedAt": 15},
					{"id": "thread-secret", "cwd": "/private/secret", "updatedAt": 30},
				},
			},
			map[string]any{
				"data": []map[string]any{
					{"id": "thread-a", "cwd": "/workspace/a", "updatedAt": 20},
					{"id": "thread-b", "cwd": "/workspace/a", "updatedAt": 10},
					{"id": "thread-shared-project", "cwd": "/workspace/a", "updatedAt": 15},
				},
			},
		},
	})
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	projects, err := service.ListProjects(context.Background(), "", 20, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects.Data) != 1 || projects.Data[0].Cwd != "/workspace/a" || projects.Data[0].ThreadCount != 2 {
		t.Fatalf("unexpected project projection: %#v", projects)
	}
	page, err := service.ListThreads(context.Background(), "", "/workspace/a", 20, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || page.Total != 2 || page.HasMore || page.NextCursor != nil {
		t.Fatalf("unexpected visible threads: %#v", page)
	}
}

func TestListThreadsPaginatesActorProjection(t *testing.T) {
	providerPage := map[string]any{
		"data": []map[string]any{
			{"id": "thread-3", "cwd": "/workspace/a", "updatedAt": 30},
			{"id": "thread-2", "cwd": "/workspace/a", "updatedAt": 20},
			{"id": "thread-1", "cwd": "/workspace/a", "updatedAt": 10},
		},
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/list": {providerPage, providerPage},
	})
	service := &Service{
		db: testDB(t), client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	first, err := service.ListThreads(context.Background(), "", "/workspace/a", 2, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 2 || first.Total != 3 || !first.HasMore || first.NextCursor == nil || *first.NextCursor != "2" {
		t.Fatalf("unexpected first projected page: %#v", first)
	}
	second, err := service.ListThreads(context.Background(), *first.NextCursor, "/workspace/a", 2, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].ID != "thread-1" || second.Total != 3 || second.HasMore || second.NextCursor != nil {
		t.Fatalf("unexpected second projected page: %#v", second)
	}
}

func TestListProjectsOwnsPagination(t *testing.T) {
	providerPage := map[string]any{
		"data": []map[string]any{
			{"id": "thread-c", "cwd": "/workspace/c", "updatedAt": 30},
			{"id": "thread-b", "cwd": "/workspace/b", "updatedAt": 20},
			{"id": "thread-a", "cwd": "/workspace/a", "updatedAt": 10},
		},
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/list": {providerPage, providerPage},
	})
	service := &Service{
		db: testDB(t), client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	first, err := service.ListProjects(context.Background(), "", 2, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 2 || first.Total != 3 || !first.HasMore || first.NextCursor == nil || *first.NextCursor != "2" {
		t.Fatalf("unexpected first project page: %#v", first)
	}
	second, err := service.ListProjects(context.Background(), *first.NextCursor, 2, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].Cwd != "/workspace/a" || second.Total != 3 || second.HasMore || second.NextCursor != nil {
		t.Fatalf("unexpected second project page: %#v", second)
	}
	if _, err := service.ListProjects(context.Background(), "invalid", 2, "user-1"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("ListProjects error = %v, want ErrInvalidCursor", err)
	}
}

func TestReadThreadHidesOtherUsersBinding(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	if err := db.Create(&orm.ExternalAgentBinding{
		ID: "binding-hidden", ConversationID: "conversation-hidden",
		Provider: ProviderCodex, ProviderThreadID: "thread-hidden",
		CreatedByUserID: "user-2", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/read": {map[string]any{
			"thread": map[string]any{"id": "thread-hidden", "cwd": "/private/secret"},
		}},
	})
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	if _, err := service.ReadThread(context.Background(), "thread-hidden", "user-1"); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("ReadThread error = %v, want ErrThreadNotFound", err)
	}
}

func TestReleaseThreadRetriesUntilUnsubscribed(t *testing.T) {
	transport := newScriptedTransport(map[string][]any{
		"thread/unsubscribe": {
			map[string]any{"status": "busy"},
			map[string]any{"status": "unsubscribed"},
		},
	})
	service := &Service{client: clientForTransport(transport)}
	status, err := service.releaseThreadWithRetry("thread-1")
	if err != nil || status != "unsubscribed" {
		t.Fatalf("release status=%q err=%v", status, err)
	}
	transport.mu.Lock()
	calls := 0
	for _, call := range transport.calls {
		if call.Method == "thread/unsubscribe" {
			calls++
		}
	}
	transport.mu.Unlock()
	if calls != 2 {
		t.Fatalf("unsubscribe calls = %d, want 2", calls)
	}
}

func TestInterruptRejectsChangedRunBeforeProviderCall(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	binding := orm.ExternalAgentBinding{
		ID: "binding-interrupt", ConversationID: "conversation-interrupt",
		Provider: ProviderCodex, ProviderThreadID: "thread-interrupt",
		CreatedByUserID: "user-interrupt", CreatedAt: now, UpdatedAt: now,
	}
	record := orm.ExternalAgentRun{
		ID: "run-interrupt", RequestID: "request-interrupt",
		ConversationID: binding.ConversationID, HistoryID: "history-interrupt",
		Provider: ProviderCodex, ProviderThreadID: binding.ProviderThreadID,
		ProviderTurnID: "turn-interrupt", ActorUserID: binding.CreatedByUserID,
		Status: runStatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	transport := newScriptedTransport(nil)
	run := newManagedRun(record, "query", 1)
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{record.ProviderThreadID: run},
		byRequest: map[string]*managedRun{
			ProviderCodex + "\x00" + record.RequestID: run,
		},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}

	err := service.Interrupt(
		context.Background(), binding.ConversationID, binding.CreatedByUserID,
		"run-that-is-no-longer-active",
	)
	if err == nil || !strings.Contains(err.Error(), "run changed") {
		t.Fatalf("Interrupt error = %v, want changed-run conflict", err)
	}
	transport.mu.Lock()
	providerCalls := len(transport.calls)
	transport.mu.Unlock()
	if providerCalls != 0 {
		t.Fatalf("changed run reached provider: %d calls", providerCalls)
	}

	if err := service.Interrupt(
		context.Background(), binding.ConversationID, binding.CreatedByUserID,
		record.ID,
	); err != nil {
		t.Fatal(err)
	}
	params := transport.lastParams(t, "turn/interrupt")
	if params["threadId"] != record.ProviderThreadID || params["turnId"] != record.ProviderTurnID {
		t.Fatalf("unexpected interrupt params: %#v", params)
	}
}

func TestTerminalStateRemainsControlledUntilUnsubscribe(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	binding := orm.ExternalAgentBinding{
		ID: "binding-release", ConversationID: "conversation-release",
		Provider: ProviderCodex, ProviderThreadID: "thread-release",
		CreatedByUserID: "user-release", CreatedAt: now, UpdatedAt: now,
	}
	record := orm.ExternalAgentRun{
		ID: "run-release", RequestID: "request-release",
		ConversationID: binding.ConversationID, HistoryID: "history-release",
		Provider: ProviderCodex, ProviderThreadID: binding.ProviderThreadID,
		ActorUserID: binding.CreatedByUserID, Action: "start",
		Status: runStatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ChatHistory{
		ID: record.HistoryID, ConversationID: record.ConversationID, Seq: 1,
		Content: "query", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}).Error; err != nil {
		t.Fatal(err)
	}

	transport := newReleaseBarrierTransport()
	run := newManagedRun(record, "query", 1)
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{record.ProviderThreadID: run},
		byRequest: map[string]*managedRun{
			ProviderCodex + "\x00" + record.RequestID: run,
		},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	events := run.subscribe()
	go service.finishTerminal(run, Event{
		Type: "turn_completed", Provider: ProviderCodex,
		ThreadID: record.ProviderThreadID, RunID: record.ID,
		Status: runStatusCompleted, Terminal: true,
	})

	select {
	case <-transport.unsubscribeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("thread/unsubscribe did not start")
	}
	available := true
	threads := []Thread{{
		ID: record.ProviderThreadID, Status: ThreadStatus{Type: "idle"},
		CanAcceptInput: &available,
	}}
	service.markThreadAvailability(threads)
	if threads[0].Available || !threads[0].ControlledByLazyMind {
		t.Fatalf("thread escaped release barrier: %#v", threads[0])
	}
	snapshot, err := service.SnapshotConversation(
		context.Background(), binding.ConversationID, binding.CreatedByUserID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runStatusReleasing || snapshot.ControlRelease != controlReleasePending {
		t.Fatalf("snapshot did not expose the release barrier: %#v", snapshot)
	}
	select {
	case event := <-events:
		t.Fatalf("terminal escaped before unsubscribe: %#v", event)
	default:
	}

	close(transport.allowUnsubscribe)
	select {
	case event := <-events:
		if !event.Terminal || event.ControlRelease != "unsubscribed" {
			t.Fatalf("unexpected terminal event: %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal event did not follow unsubscribe")
	}
	var persisted orm.ExternalAgentRun
	if err := db.Where("id = ?", record.ID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ControlRelease != "unsubscribed" || persisted.ControlError != "" {
		t.Fatalf("release state was not persisted: %#v", persisted)
	}
	replay, ok, err := service.completedExecution(context.Background(), ChatInput{
		Provider: ProviderCodex, RequestID: record.RequestID,
		ActorUserID: record.ActorUserID, ConversationID: record.ConversationID,
		ProviderThreadID: record.ProviderThreadID, Query: "query",
	})
	if err != nil || !ok {
		t.Fatalf("completed replay: ok=%v err=%v", ok, err)
	}
	if event := <-replay.Events; event.ControlRelease != "unsubscribed" {
		t.Fatalf("completed replay lost release state: %#v", event)
	}
}

func TestFailedTerminalReleaseRemainsControlledUntilExplicitRelease(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	binding := orm.ExternalAgentBinding{
		ID: "binding-release-failed", ConversationID: "conversation-release-failed",
		Provider: ProviderCodex, ProviderThreadID: "thread-release-failed",
		CreatedByUserID: "user-release-failed", CreatedAt: now, UpdatedAt: now,
	}
	record := orm.ExternalAgentRun{
		ID: "run-release-failed", RequestID: "request-release-failed",
		ConversationID: binding.ConversationID, HistoryID: "history-release-failed",
		Provider: ProviderCodex, ProviderThreadID: binding.ProviderThreadID,
		ActorUserID: binding.CreatedByUserID, Action: "start",
		Status: runStatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/unsubscribe": {
			map[string]any{"status": "busy"},
			map[string]any{"status": "busy"},
			map[string]any{"status": "busy"},
			map[string]any{"status": "unsubscribed"},
		},
	})
	run := newManagedRun(record, "query", 1)
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{record.ProviderThreadID: run},
		byRequest: map[string]*managedRun{
			ProviderCodex + "\x00" + record.RequestID: run,
		},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	events := run.subscribe()
	go service.finishTerminal(run, Event{
		Type: "turn_completed", Provider: ProviderCodex,
		ThreadID: record.ProviderThreadID, RunID: record.ID,
		Status: runStatusCompleted, Terminal: true,
	})
	select {
	case event := <-events:
		if event.ControlRelease != "failed" {
			t.Fatalf("unexpected terminal release: %#v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("failed release did not emit a terminal event")
	}
	service.mu.Lock()
	retained := service.byThread[record.ProviderThreadID] == run
	service.mu.Unlock()
	if !retained {
		t.Fatal("failed release dropped the controlled thread")
	}
	var failed orm.ExternalAgentRun
	if err := db.Where("id = ?", record.ID).First(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if failed.ControlRelease != "failed" || failed.ControlError == "" {
		t.Fatalf("failed release state was not persisted: %#v", failed)
	}
	snapshot, err := service.SnapshotConversation(
		context.Background(), binding.ConversationID, binding.CreatedByUserID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ControlRelease != "failed" {
		t.Fatalf("snapshot lost failed release state: %#v", snapshot)
	}
	if err := service.Release(
		context.Background(), binding.ConversationID, binding.CreatedByUserID,
	); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	retained = service.byThread[record.ProviderThreadID] == run
	service.mu.Unlock()
	if retained {
		t.Fatal("explicit successful release kept the controlled thread")
	}
	var released orm.ExternalAgentRun
	if err := db.Where("id = ?", record.ID).First(&released).Error; err != nil {
		t.Fatal(err)
	}
	if released.ControlRelease != "unsubscribed" || released.ControlError != "" {
		t.Fatalf("explicit release did not clear failure: %#v", released)
	}
}

func TestRecoveredTerminalWaitsForUnsubscribe(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	record := orm.ExternalAgentRun{
		ID: "run-recovered", RequestID: "request-recovered",
		ConversationID: "conversation-recovered", HistoryID: "history-recovered",
		Provider: ProviderCodex, ProviderThreadID: "thread-recovered",
		ActorUserID: "user-recovered", Action: "start",
		Status: runStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	transport := newReleaseBarrierTransport()
	service := &Service{db: db, client: clientForTransport(transport)}
	type result struct {
		execution Execution
		err       error
	}
	resultCh := make(chan result, 1)
	go func() {
		execution, err := service.terminalExecution(
			record, "turn_completed", "recovered answer",
		)
		resultCh <- result{execution: execution, err: err}
	}()
	select {
	case <-transport.unsubscribeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("recovered terminal did not start thread/unsubscribe")
	}
	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatal(got.err)
		}
		select {
		case event := <-got.execution.Events:
			t.Fatalf("recovered terminal escaped release barrier: %#v", event)
		default:
		}
		resultCh <- got
	case <-time.After(2 * time.Second):
		t.Fatal("recovered execution was not attached")
	}
	close(transport.allowUnsubscribe)
	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatal(got.err)
		}
		event := <-got.execution.Events
		if event.ControlRelease != "unsubscribed" || !event.Terminal {
			t.Fatalf("unexpected recovered terminal: %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recovered terminal did not follow unsubscribe")
	}
}

func TestPersistedPendingTerminalRecoversOnStartup(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	binding := orm.ExternalAgentBinding{
		ID: "binding-pending-recovery", ConversationID: "conversation-pending-recovery",
		Provider: ProviderCodex, ProviderThreadID: "thread-pending-recovery",
		CreatedByUserID: "user-pending-recovery", CreatedAt: now, UpdatedAt: now,
	}
	record := orm.ExternalAgentRun{
		ID: "run-pending-recovery", RequestID: "request-pending-recovery",
		ConversationID: binding.ConversationID, HistoryID: "history-pending-recovery",
		Provider: ProviderCodex, ProviderThreadID: binding.ProviderThreadID,
		ActorUserID: binding.CreatedByUserID, Action: "start",
		Status: runStatusCompleted, ControlRelease: controlReleasePending,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	transport := newReleaseBarrierTransport()
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	if err := service.recoverActiveRuns(); err != nil {
		t.Fatal(err)
	}
	available := true
	threads := []Thread{{
		ID: record.ProviderThreadID, Status: ThreadStatus{Type: "idle"},
		CanAcceptInput: &available,
	}}
	service.markThreadAvailability(threads)
	if threads[0].Available || !threads[0].ControlledByLazyMind {
		t.Fatalf("pending startup release exposed before recovery: %#v", threads[0])
	}
	select {
	case <-transport.unsubscribeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("startup recovery did not resume pending release")
	}
	close(transport.allowUnsubscribe)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var persisted orm.ExternalAgentRun
		if err := db.Where("id = ?", record.ID).First(&persisted).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.ControlRelease == "unsubscribed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending release did not complete: %#v", persisted)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminalPersistenceFailureBlocksReleaseAndTerminal(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	record := orm.ExternalAgentRun{
		ID: "run-persist-failure", RequestID: "request-persist-failure",
		ConversationID: "conversation-persist-failure", HistoryID: "history-persist-failure",
		Provider: ProviderCodex, ProviderThreadID: "thread-persist-failure",
		ActorUserID: "user-persist-failure", Action: "start",
		Status: runStatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&orm.ExternalAgentRun{}); err != nil {
		t.Fatal(err)
	}
	transport := newReleaseBarrierTransport()
	run := newManagedRun(record, "query", 1)
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{record.ProviderThreadID: run},
		byRequest: map[string]*managedRun{
			ProviderCodex + "\x00" + record.RequestID: run,
		},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	events := run.subscribe()
	service.finishTerminal(run, Event{
		Type: "turn_completed", Provider: ProviderCodex,
		ThreadID: record.ProviderThreadID, RunID: record.ID,
		Status: runStatusCompleted, Terminal: true,
	})
	select {
	case <-transport.unsubscribeStarted:
		t.Fatal("unsubscribe started before terminal state was persisted")
	default:
	}
	event := <-events
	if event.Terminal || event.Type != "control_release_failed" {
		t.Fatalf("persistence failure escaped as terminal: %#v", event)
	}
	service.mu.Lock()
	retained := service.byThread[record.ProviderThreadID] == run
	service.mu.Unlock()
	if !retained {
		t.Fatal("persistence failure dropped controlled thread")
	}
}

func TestFailedReleaseRestoresControlledProjectionOnStartup(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	record := orm.ExternalAgentRun{
		ID: "run-failed-recovery", RequestID: "request-failed-recovery",
		ConversationID: "conversation-failed-recovery", HistoryID: "history-failed-recovery",
		Provider: ProviderCodex, ProviderThreadID: "thread-failed-recovery",
		ActorUserID: "user-failed-recovery", Action: "start",
		Status: runStatusCompleted, ControlRelease: controlReleaseFailed,
		ControlError: "provider remained subscribed", CreatedAt: now, UpdatedAt: now,
	}
	binding := orm.ExternalAgentBinding{
		ID: "binding-failed-recovery", ConversationID: record.ConversationID,
		Provider: ProviderCodex, ProviderThreadID: record.ProviderThreadID,
		CreatedByUserID: record.ActorUserID, CreatedAt: now, UpdatedAt: now,
	}
	history := orm.ChatHistory{
		ID: record.HistoryID, Seq: 1, ConversationID: record.ConversationID,
		Content: "query", Result: "done",
		TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now},
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	transport := newScriptedTransport(map[string][]any{
		"thread/unsubscribe": {map[string]any{"status": "unsubscribed"}},
	})
	service := &Service{
		db: db, client: clientForTransport(transport),
		byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{},
		requests: map[string]*pendingRequest{}, loaded: map[string]int64{},
	}
	if err := service.recoverActiveRuns(); err != nil {
		t.Fatal(err)
	}
	available := true
	threads := []Thread{{
		ID: record.ProviderThreadID, Status: ThreadStatus{Type: "idle"},
		CanAcceptInput: &available,
	}}
	service.markThreadAvailability(threads)
	if threads[0].Available || !threads[0].ControlledByLazyMind {
		t.Fatalf("failed startup release lost ownership: %#v", threads[0])
	}
	execution, err := service.StartOrSteer(context.Background(), ChatInput{
		Provider: ProviderCodex, RequestID: record.RequestID,
		ActorUserID: record.ActorUserID, ConversationID: record.ConversationID,
		ProviderThreadID: record.ProviderThreadID, Query: "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event, ok := <-execution.Events:
		if !ok || !event.Terminal || event.ControlRelease != controlReleaseFailed {
			t.Fatalf("failed release replay = %#v, open=%v", event, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("failed release replay blocked")
	}
	if err := service.Release(
		context.Background(), record.ConversationID, record.ActorUserID,
	); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	active := service.byThread[record.ProviderThreadID]
	service.mu.Unlock()
	if active != nil {
		t.Fatal("successful explicit release retained ownership")
	}
}

func TestControlReleaseClaimIsCompareAndSwap(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	record := orm.ExternalAgentRun{
		ID: "run-release-claim", RequestID: "request-release-claim",
		ConversationID: "conversation-release-claim", HistoryID: "history-release-claim",
		Provider: ProviderCodex, ProviderThreadID: "thread-release-claim",
		ActorUserID: "user-release-claim", Action: "start",
		Status: runStatusCompleted, ControlRelease: controlReleaseFailed,
		ControlError: "busy", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{db: db}
	claimed, err := service.claimControlRelease(context.Background(), record)
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}
	claimed, err = service.claimControlRelease(context.Background(), record)
	if claimed || !errors.Is(err, ErrReleasePending) {
		t.Fatalf("second claim: claimed=%v err=%v", claimed, err)
	}
}

func TestControlReleaseClaimReloadsStaleInMemoryState(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	persisted := orm.ExternalAgentRun{
		ID: "run-release-stale", RequestID: "request-release-stale",
		ConversationID: "conversation-release-stale", HistoryID: "history-release-stale",
		Provider: ProviderCodex, ProviderThreadID: "thread-release-stale",
		ActorUserID: "user-release-stale", Action: "start",
		Status: runStatusCompleted, ControlRelease: "", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	stale := persisted
	stale.ControlRelease = controlReleaseFailed
	service := &Service{db: db}
	claimed, err := service.claimControlRelease(context.Background(), stale)
	if err != nil || !claimed {
		t.Fatalf("stale claim: claimed=%v err=%v", claimed, err)
	}
}

func TestPersistControlReleaseRejectsMissingRun(t *testing.T) {
	service := &Service{db: testDB(t)}
	err := service.persistControlRelease(
		"missing-run", runStatusCompleted, "unsubscribed", "", nil,
	)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("persistControlRelease error = %v, want record not found", err)
	}
}

func TestBindIsIdempotent(t *testing.T) {
	db := testDB(t)
	service := &Service{db: db, byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{}, requests: map[string]*pendingRequest{}, loaded: map[string]int64{}}
	first, err := service.Bind(context.Background(), BindInput{
		Provider: ProviderCodex, ProviderThreadID: "thread-1", ConversationID: "conv-1",
		CreatedByUserID: "u1", CreatedByLazyMind: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Bind(context.Background(), BindInput{
		Provider: ProviderCodex, ProviderThreadID: "thread-1", ConversationID: "conv-2",
		CreatedByUserID: "u1", CreatedByLazyMind: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.ConversationID != "conv-1" {
		t.Fatalf("binding not idempotent: %#v %#v", first, second)
	}
}

func TestBindRejectsAnotherOwner(t *testing.T) {
	db := testDB(t)
	service := &Service{db: db, byThread: map[string]*managedRun{}, byRequest: map[string]*managedRun{}, requests: map[string]*pendingRequest{}, loaded: map[string]int64{}}
	if _, err := service.Bind(context.Background(), BindInput{
		Provider: ProviderCodex, ProviderThreadID: "thread-1", ConversationID: "conv-1",
		CreatedByUserID: "u1", CreatedByLazyMind: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Bind(context.Background(), BindInput{
		Provider: ProviderCodex, ProviderThreadID: "thread-1", ConversationID: "conv-2",
		CreatedByUserID: "u2", CreatedByLazyMind: false,
	}); !errors.Is(err, ErrThreadBusy) {
		t.Fatalf("Bind error = %v, want ErrThreadBusy", err)
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
		Provider: ProviderCodex, RequestID: "req-1", ActorUserID: "u1", ConversationID: "c1", ProviderThreadID: "t1", Query: "hi",
	})
	if err != nil || !ok {
		t.Fatalf("completedExecution: ok=%v err=%v", ok, err)
	}
	event := <-execution.Events
	if !event.Terminal || event.Type != "turn_completed" || event.Message != "done" {
		t.Fatalf("unexpected event: %#v", event)
	}
	if _, ok, err := service.completedExecution(context.Background(), ChatInput{
		Provider: ProviderCodex, RequestID: "req-1", ActorUserID: "u1",
		ConversationID: "c1", ProviderThreadID: "t1", Query: "different",
	}); !ok || !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("changed replay was not rejected: ok=%v err=%v", ok, err)
	}
}

func TestExecutionCancelRemovesSubscriber(t *testing.T) {
	run := newManagedRun(orm.ExternalAgentRun{
		ID: "run-subscription", HistoryID: "history-subscription",
	}, "query", 1)
	execution := run.execution()
	run.mu.Lock()
	before := len(run.subscribers)
	run.mu.Unlock()
	if before != 1 {
		t.Fatalf("subscriber count before cancel = %d", before)
	}
	execution.Cancel()
	run.mu.Lock()
	after := len(run.subscribers)
	run.mu.Unlock()
	if after != 0 {
		t.Fatalf("subscriber count after cancel = %d", after)
	}
	if _, open := <-execution.Events; open {
		t.Fatal("cancelled subscription remained open")
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

func TestDetachActiveClearsLoadedThread(t *testing.T) {
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

	service.detachActive(run)

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

func TestClientEnablesExperimentalInteractionRequests(t *testing.T) {
	transport := newScriptedTransport(map[string][]any{
		"thread/list": {map[string]any{"data": []any{}}},
	})
	client := clientForTransport(transport)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var page ThreadPage
	if err := client.Call(ctx, "thread/list", map[string]any{}, &page); err != nil {
		t.Fatal(err)
	}
	params := transport.lastParams(t, "initialize")
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok || capabilities["experimentalApi"] != true {
		t.Fatalf("experimental interaction API was not enabled: %#v", params)
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

func TestFileChangeApprovalPayloadIncludesBoundedReview(t *testing.T) {
	run := newManagedRun(orm.ExternalAgentRun{}, "", 0)
	changes := json.RawMessage(`[{"path":"a.txt","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-old\n+new"}]`)
	run.setFileChanges("item-1", changes)
	payload := fileChangeApprovalPayload(
		run,
		json.RawMessage(`{"itemId":"item-1","reason":"write"}`),
	)
	var decoded struct {
		ItemID    string          `json:"itemId"`
		Changes   json.RawMessage `json:"changes"`
		Truncated bool            `json:"changesTruncated"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ItemID != "item-1" || decoded.Truncated || string(decoded.Changes) != string(changes) {
		t.Fatalf("unexpected enriched payload: %s", payload)
	}

	run.setFileChanges("item-2", json.RawMessage(`"`+strings.Repeat("x", 64*1024)+`"`))
	payload = fileChangeApprovalPayload(
		run,
		json.RawMessage(`{"itemId":"item-2"}`),
	)
	decoded = struct {
		ItemID    string          `json:"itemId"`
		Changes   json.RawMessage `json:"changes"`
		Truncated bool            `json:"changesTruncated"`
	}{}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Truncated || len(decoded.Changes) != 0 {
		t.Fatalf("oversized review was not rejected: %s", payload)
	}
}
