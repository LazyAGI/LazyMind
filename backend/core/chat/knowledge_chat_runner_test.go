package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type fakeKnowledgeAccessChecker struct {
	errByID map[string]error
	calls   []string
}

func (f *fakeKnowledgeAccessChecker) EnsureKnowledgeReadable(_ context.Context, userID string, knowledgeID string) error {
	f.calls = append(f.calls, userID+":"+knowledgeID)
	if err := f.errByID[knowledgeID]; err != nil {
		return err
	}
	return nil
}

func newKnowledgeChatRunnerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := t.Name() + "_" + time.Now().Format("150405.000000000")
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.UserChatSettings{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newPassingKnowledgeAccessChecker() *fakeKnowledgeAccessChecker {
	return &fakeKnowledgeAccessChecker{errByID: map[string]error{}}
}

func countKnowledgeChatRows(t *testing.T, db *gorm.DB) (int64, int64) {
	t.Helper()
	var conversations int64
	var histories int64
	if err := db.Model(&orm.Conversation{}).Count(&conversations).Error; err != nil {
		t.Fatalf("count conversations: %v", err)
	}
	if err := db.Model(&orm.ChatHistory{}).Count(&histories).Error; err != nil {
		t.Fatalf("count histories: %v", err)
	}
	return conversations, histories
}

func TestKnowledgeChatRunnerValidatesInput(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	tests := []struct {
		name  string
		input KnowledgeChatRequest
	}{
		{name: "user id empty", input: KnowledgeChatRequest{UserID: " ", Query: "q", KnowledgeIDs: []string{"kb-1"}}},
		{name: "query empty", input: KnowledgeChatRequest{UserID: "u-1", Query: " ", KnowledgeIDs: []string{"kb-1"}}},
		{name: "knowledge ids empty", input: KnowledgeChatRequest{UserID: "u-1", Query: "q", KnowledgeIDs: []string{" "}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := newPassingKnowledgeAccessChecker()
			runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
				DB:            db,
				AccessChecker: access,
				StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
					t.Fatal("stream should not be called for invalid input")
					return nil, nil
				},
			})
			_, err := runner.RunKnowledgeChat(context.Background(), tt.input)
			var chatErr *KnowledgeChatError
			if !errors.As(err, &chatErr) || chatErr.Code != KnowledgeChatInvalidArgument {
				t.Fatalf("expected invalid argument error, got %#v", err)
			}
			if len(access.calls) != 0 {
				t.Fatalf("access checker should not be called for invalid input: %#v", access.calls)
			}
		})
	}
}

func TestKnowledgeChatRunnerRequiresAccessChecker(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB: db,
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			t.Fatal("stream should not be called without ACL checker")
			return nil, nil
		},
	})

	_, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1"},
	})
	var chatErr *KnowledgeChatError
	if !errors.As(err, &chatErr) || chatErr.Code != KnowledgeChatInternal {
		t.Fatalf("expected internal error, got %#v", err)
	}
}

func TestKnowledgeChatRunnerChecksKnowledgeAccessBeforeConversationAndUpstream(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	accessErr := knowledgeChatErr(KnowledgeChatForbidden, "dataset forbidden", errors.New("acl denied"))
	access := &fakeKnowledgeAccessChecker{errByID: map[string]error{"kb-denied": accessErr}}
	var upstreamCalled bool
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: access,
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			upstreamCalled = true
			return nil, nil
		},
	})

	_, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-ok", "kb-denied", "kb-late"},
	})
	var chatErr *KnowledgeChatError
	if !errors.As(err, &chatErr) || chatErr.Code != KnowledgeChatNotFound {
		t.Fatalf("expected not found ACL mapping, got %#v", err)
	}
	if !errors.Is(err, accessErr) {
		t.Fatalf("expected ACL cause to be retained")
	}
	if upstreamCalled {
		t.Fatal("upstream should not be called when ACL fails")
	}
	if got := strings.Join(access.calls, ","); got != "user-1:kb-ok,user-1:kb-denied" {
		t.Fatalf("unexpected ACL call order: %s", got)
	}
	if convs, histories := countKnowledgeChatRows(t, db); convs != 0 || histories != 0 {
		t.Fatalf("ACL failure should not create rows, conversations=%d histories=%d", convs, histories)
	}
}

func TestKnowledgeChatRunnerMapsKnowledgeAccessErrors(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	tests := []struct {
		name string
		err  error
		code KnowledgeChatErrorCode
	}{
		{name: "not found", err: knowledgeChatErr(KnowledgeChatNotFound, "missing", gorm.ErrRecordNotFound), code: KnowledgeChatNotFound},
		{name: "backend", err: knowledgeChatErr(KnowledgeChatBackendUnavailable, "scan source unavailable", errors.New("scan failed")), code: KnowledgeChatBackendUnavailable},
		{name: "internal", err: errors.New("db broken"), code: KnowledgeChatInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &fakeKnowledgeAccessChecker{errByID: map[string]error{"kb-1": tt.err}}
			runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
				DB:            db,
				AccessChecker: access,
				StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
					t.Fatal("stream should not be called when ACL fails")
					return nil, nil
				},
			})
			_, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{UserID: "user-1", Query: "q", KnowledgeIDs: []string{"kb-1"}})
			var chatErr *KnowledgeChatError
			if !errors.As(err, &chatErr) || chatErr.Code != tt.code {
				t.Fatalf("expected %s, got %#v", tt.code, err)
			}
			if convs, histories := countKnowledgeChatRows(t, db); convs != 0 || histories != 0 {
				t.Fatalf("ACL failure should not create rows, conversations=%d histories=%d", convs, histories)
			}
		})
	}
}

func TestKnowledgeChatRunnerCreatesConversationAndBuildsStrictRequest(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	access := newPassingKnowledgeAccessChecker()
	var capturedBody map[string]any
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: access,
		BaseURL:       "http://chat.local/",
		StreamChat: func(_ context.Context, baseURL string, body map[string]any) (<-chan UpstreamStreamChunk, error) {
			if baseURL != "http://chat.local" {
				t.Fatalf("unexpected base url: %q", baseURL)
			}
			capturedBody = body
			ch := make(chan UpstreamStreamChunk, 2)
			ch <- UpstreamStreamChunk{Text: "hello "}
			ch <- UpstreamStreamChunk{Text: "world", ToolCallTurns: 2}
			close(ch)
			return ch, nil
		},
	})

	result, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "what is this?",
		KnowledgeIDs: []string{"kb-1", "kb-2", "kb-1", " "},
	})
	if err != nil {
		t.Fatalf("run knowledge chat: %v", err)
	}
	if result.ConversationID == "" || result.MessageID == "" {
		t.Fatalf("expected generated ids, got %#v", result)
	}
	if result.Answer != "hello world" {
		t.Fatalf("unexpected answer: %q", result.Answer)
	}
	if result.ToolCallTurns != 2 {
		t.Fatalf("expected tool call turns 2, got %d", result.ToolCallTurns)
	}
	assertKnowledgeChatRequestScope(t, capturedBody, []string{"kb-1", "kb-2"})
	if useMemory, _ := capturedBody["use_memory"].(bool); useMemory {
		t.Fatalf("expected use_memory=false, got %#v", capturedBody["use_memory"])
	}
	if enablePlugin, _ := capturedBody["enable_plugin"].(bool); enablePlugin {
		t.Fatalf("expected enable_plugin=false, got %#v", capturedBody["enable_plugin"])
	}
	if files, ok := capturedBody["files"].(map[string][]string); !ok || len(files) != 0 {
		t.Fatalf("expected empty files map, got %#v", capturedBody["files"])
	}
	if databases, ok := capturedBody["databases"].([]any); !ok || len(databases) != 0 {
		t.Fatalf("expected empty databases, got %#v", capturedBody["databases"])
	}
	if got := strings.Join(access.calls, ","); got != "user-1:kb-1,user-1:kb-2" {
		t.Fatalf("unexpected ACL calls: %s", got)
	}
	var history orm.ChatHistory
	if err := db.Where("id = ?", result.MessageID).First(&history).Error; err != nil {
		t.Fatalf("history not persisted: %v", err)
	}
	if history.ConversationID != result.ConversationID || history.Result != "hello world" || history.ToolCallTurns != 2 {
		t.Fatalf("unexpected persisted history: %#v", history)
	}
}

func TestKnowledgeChatRunnerExplicitMemoryAndPluginOptions(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	var capturedBody map[string]any
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: newPassingKnowledgeAccessChecker(),
		StreamChat: func(_ context.Context, _ string, body map[string]any) (<-chan UpstreamStreamChunk, error) {
			capturedBody = body
			ch := make(chan UpstreamStreamChunk, 1)
			ch <- UpstreamStreamChunk{Text: "answer"}
			close(ch)
			return ch, nil
		},
	})

	_, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1"},
		UseMemory:    true,
		EnablePlugin: true,
	})
	if err != nil {
		t.Fatalf("run knowledge chat: %v", err)
	}
	if useMemory, _ := capturedBody["use_memory"].(bool); !useMemory {
		t.Fatalf("expected use_memory=true, got %#v", capturedBody["use_memory"])
	}
	if enablePlugin, _ := capturedBody["enable_plugin"].(bool); !enablePlugin {
		t.Fatalf("expected enable_plugin=true, got %#v", capturedBody["enable_plugin"])
	}
}

func TestKnowledgeChatRunnerConversationSemantics(t *testing.T) {
	tests := []struct {
		name           string
		seed           *orm.Conversation
		conversationID string
		wantCode       KnowledgeChatErrorCode
	}{
		{
			name:           "reuse owned",
			seed:           &orm.Conversation{ID: "conv-owned", DisplayName: "existing", BaseModel: orm.BaseModel{CreateUserID: "user-1"}},
			conversationID: "conv-owned",
		},
		{
			name:           "missing is not found",
			conversationID: "conv-missing",
			wantCode:       KnowledgeChatNotFound,
		},
		{
			name:           "foreign is not found",
			seed:           &orm.Conversation{ID: "conv-foreign", DisplayName: "existing", BaseModel: orm.BaseModel{CreateUserID: "other-user"}},
			conversationID: "conv-foreign",
			wantCode:       KnowledgeChatNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newKnowledgeChatRunnerTestDB(t)
			if tt.seed != nil {
				if err := db.Create(tt.seed).Error; err != nil {
					t.Fatalf("seed conversation: %v", err)
				}
			}
			upstreamCalled := false
			runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
				DB:            db,
				AccessChecker: newPassingKnowledgeAccessChecker(),
				StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
					upstreamCalled = true
					ch := make(chan UpstreamStreamChunk, 1)
					ch <- UpstreamStreamChunk{Text: "answer"}
					close(ch)
					return ch, nil
				},
			})

			result, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
				UserID:         "user-1",
				Query:          "q",
				KnowledgeIDs:   []string{"kb-1"},
				ConversationID: tt.conversationID,
			})
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("run knowledge chat: %v", err)
				}
				if !upstreamCalled {
					t.Fatal("expected upstream to be called")
				}
				if result.ConversationID != tt.conversationID {
					t.Fatalf("expected conversation %q, got %q", tt.conversationID, result.ConversationID)
				}
				return
			}
			var chatErr *KnowledgeChatError
			if !errors.As(err, &chatErr) || chatErr.Code != tt.wantCode {
				t.Fatalf("expected %s, got %#v", tt.wantCode, err)
			}
			if upstreamCalled {
				t.Fatal("upstream should not be called for invalid conversation")
			}
			var histories int64
			if db.Model(&orm.ChatHistory{}).Count(&histories); histories != 0 {
				t.Fatalf("conversation failure should not create history, got %d", histories)
			}
			var created int64
			if db.Model(&orm.Conversation{}).Where("id <> ?", tt.conversationID).Count(&created); created != 0 {
				t.Fatalf("conversation failure should not create a new conversation, got %d", created)
			}
		})
	}
}

func TestKnowledgeChatRunnerAggregatesAndSanitizesAnswer(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: newPassingKnowledgeAccessChecker(),
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			ch := make(chan UpstreamStreamChunk, 8)
			ch <- UpstreamStreamChunk{ReasoningText: "private reasoning", Text: "kept "}
			ch <- UpstreamStreamChunk{Text: `before<tool_`}
			ch <- UpstreamStreamChunk{Text: `call>{"name":"kb"}</tool_call>`}
			ch <- UpstreamStreamChunk{Text: `<tool_result>{"local_path":"/secret"}</tool_`}
			ch <- UpstreamStreamChunk{Text: `result>after<think>hidden</think>`}
			ch <- UpstreamStreamChunk{Text: `<tp>preview</`}
			ch <- UpstreamStreamChunk{Text: `tp><trp>done</trp>`}
			close(ch)
			return ch, nil
		},
	})

	result, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1"},
	})
	if err != nil {
		t.Fatalf("run knowledge chat: %v", err)
	}
	if result.Answer != "kept beforeafter" {
		t.Fatalf("unexpected sanitized answer: %q", result.Answer)
	}
	for _, forbidden := range []string{"private reasoning", "tool_call", "tool_result", "<think", "<tp", "<trp", "/secret"} {
		if strings.Contains(result.Answer, forbidden) {
			t.Fatalf("answer contains %q: %q", forbidden, result.Answer)
		}
	}
}

func TestKnowledgeChatRunnerAllowsEmptyAnswerOnNormalEOF(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: newPassingKnowledgeAccessChecker(),
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			ch := make(chan UpstreamStreamChunk)
			close(ch)
			return ch, nil
		},
	})

	result, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1"},
	})
	if err != nil {
		t.Fatalf("run knowledge chat: %v", err)
	}
	if result.Answer != "" {
		t.Fatalf("expected empty answer, got %q", result.Answer)
	}
}

func TestKnowledgeChatRunnerSanitizesAndDeduplicatesSources(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: newPassingKnowledgeAccessChecker(),
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			ch := make(chan UpstreamStreamChunk, 3)
			ch <- UpstreamStreamChunk{Sources: []any{map[string]any{
				"dataset_id":       "kb-old",
				"core_document_id": "doc-old",
				"uid":              "chunk-old",
				"content":          "old source",
			}}}
			finalSources := []any{
				map[string]any{
					"dataset_id":        "kb-1",
					"core_document_id":  "doc-core-1",
					"document_id":       "lazy-visible-should-not-win",
					"docid":             "lazy-doc-1",
					"lazyllm_doc_id":    "lazy-doc-1",
					"uid":               "chunk-1",
					"file_name":         "file.txt",
					"content":           "safe content",
					"segment_number":    float64(3),
					"local_path":        "/srv/private/file.txt",
					"stored_path":       "/srv/private/stored.txt",
					"parse_stored_path": "/srv/private/parse.txt",
					"metadata":          map[string]any{"local_path": "/srv/private/file.txt"},
					"global_metadata":   map[string]any{"docid": "lazy-doc-1"},
				},
				map[string]any{
					"dataset_id":       "kb-1",
					"core_document_id": "doc-core-1",
					"uid":              "chunk-1",
					"file_name":        "file.txt",
					"content":          "safe content",
					"segment_number":   float64(3),
				},
				map[string]any{
					"kb_id":          "kb-2",
					"docid":          "lazy-doc-2",
					"lazyllm_doc_id": "lazy-doc-2",
					"chunk_id":       "chunk-2",
					"title":          "other.txt",
					"text":           "other content",
					"number":         "4",
				},
			}
			ch <- UpstreamStreamChunk{Sources: finalSources}
			close(ch)
			return ch, nil
		},
	})

	result, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1", "kb-2"},
	})
	if err != nil {
		t.Fatalf("run knowledge chat: %v", err)
	}
	if len(result.Sources) != 2 {
		t.Fatalf("expected deduped final sources, got %#v", result.Sources)
	}
	first := result.Sources[0]
	if first.KnowledgeID != "kb-1" || first.DocumentID != "doc-core-1" || first.ChunkID != "chunk-1" || first.Title != "file.txt" || first.Text != "safe content" || first.Number != 3 {
		t.Fatalf("unexpected core source mapping: %#v", first)
	}
	second := result.Sources[1]
	if second.KnowledgeID != "kb-2" || second.DocumentID != "" || second.ChunkID != "chunk-2" || second.Text != "other content" || second.Number != 4 {
		t.Fatalf("internal docid should not be exposed: %#v", second)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, forbidden := range []string{"lazy-doc", "local_path", "stored_path", "parse_stored_path", "metadata", "global_metadata", "/srv/private"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("source result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestKnowledgeChatRunnerMapsUpstreamFailures(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	cause := errors.New("dial failed")
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: newPassingKnowledgeAccessChecker(),
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			return nil, cause
		},
	})

	_, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1"},
	})
	var chatErr *KnowledgeChatError
	if !errors.As(err, &chatErr) || chatErr.Code != KnowledgeChatBackendUnavailable {
		t.Fatalf("expected backend unavailable error, got %#v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected error to unwrap cause")
	}
	if !errors.Is(err, &KnowledgeChatError{Code: KnowledgeChatBackendUnavailable}) {
		t.Fatalf("expected errors.Is to match backend unavailable code")
	}
}

func TestKnowledgeChatRunnerMapsFailedStatus(t *testing.T) {
	db := newKnowledgeChatRunnerTestDB(t)
	runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
		DB:            db,
		AccessChecker: newPassingKnowledgeAccessChecker(),
		StreamChat: func(context.Context, string, map[string]any) (<-chan UpstreamStreamChunk, error) {
			ch := make(chan UpstreamStreamChunk, 1)
			ch <- UpstreamStreamChunk{Status: "FAILED"}
			close(ch)
			return ch, nil
		},
	})

	_, err := runner.RunKnowledgeChat(context.Background(), KnowledgeChatRequest{
		UserID:       "user-1",
		Query:        "q",
		KnowledgeIDs: []string{"kb-1"},
	})
	var chatErr *KnowledgeChatError
	if !errors.As(err, &chatErr) || chatErr.Code != KnowledgeChatBackendUnavailable || chatErr.Cause == nil {
		t.Fatalf("expected backend unavailable error with cause, got %#v", err)
	}
}

func TestKnowledgeChatRunnerMapsCanceledAndDeadlineContexts(t *testing.T) {
	tests := []struct {
		name            string
		makeCtx         func() (context.Context, func())
		triggerInStream bool
		wantIs          error
	}{
		{
			name: "canceled",
			makeCtx: func() (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				return ctx, cancel
			},
			triggerInStream: true,
			wantIs:          context.Canceled,
		},
		{
			name: "deadline exceeded",
			makeCtx: func() (context.Context, func()) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				return ctx, cancel
			},
			wantIs: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newKnowledgeChatRunnerTestDB(t)
			ctx, trigger := tt.makeCtx()
			t.Cleanup(trigger)
			runner := NewKnowledgeChatRunner(KnowledgeChatRunnerDeps{
				DB:            db,
				AccessChecker: newPassingKnowledgeAccessChecker(),
				StreamChat: func(ctx context.Context, _ string, _ map[string]any) (<-chan UpstreamStreamChunk, error) {
					ch := make(chan UpstreamStreamChunk)
					go func() {
						if tt.triggerInStream {
							trigger()
						}
						<-ctx.Done()
						close(ch)
					}()
					return ch, nil
				},
			})

			_, err := runner.RunKnowledgeChat(ctx, KnowledgeChatRequest{
				UserID:       "user-1",
				Query:        "q",
				KnowledgeIDs: []string{"kb-1"},
			})
			var chatErr *KnowledgeChatError
			if !errors.As(err, &chatErr) || chatErr.Code != KnowledgeChatBackendUnavailable {
				t.Fatalf("expected backend unavailable error, got %#v", err)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Fatalf("expected preserved context cause %v, got %#v", tt.wantIs, err)
			}
		})
	}
}

func assertKnowledgeChatRequestScope(t *testing.T, body map[string]any, want []string) {
	t.Helper()
	filters, ok := body["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters missing from upstream body: %#v", body)
	}
	kbIDs, ok := filters["kb_id"].([]string)
	if !ok {
		t.Fatalf("filters.kb_id has unexpected type: %#v", filters["kb_id"])
	}
	if strings.Join(kbIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected filters.kb_id: %#v", kbIDs)
	}
	conversation, ok := body["conversation"].(map[string]any)
	if !ok {
		t.Fatalf("conversation missing from upstream body: %#v", body)
	}
	searchConfig, ok := conversation["search_config"].(map[string]any)
	if !ok {
		t.Fatalf("conversation.search_config missing: %#v", conversation)
	}
	datasetList, ok := searchConfig["dataset_list"].([]map[string]any)
	if !ok {
		t.Fatalf("dataset_list has unexpected type: %#v", searchConfig["dataset_list"])
	}
	if len(datasetList) != len(want) {
		t.Fatalf("dataset_list length = %d, want %d", len(datasetList), len(want))
	}
	for i, item := range datasetList {
		if item["id"] != want[i] {
			t.Fatalf("dataset_list[%d] = %#v, want %q", i, item, want[i])
		}
	}
}
