package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const writerCredentialOwner = "credential-owner"
const writerCredentialInternal = "credential-fixture-internal"
const writerCredentialPrivate = "credential-fixture-private-error"

type writerCredentialFixture struct {
	db                    *orm.DB
	entry, provider, slot string
	document              map[string]any
	privateReads          atomic.Int32
	contentReads          atomic.Int32
}

func newWriterCredentialFixture(t *testing.T, entry, provider string) *writerCredentialFixture {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.SubAgentArtifact{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserSelectedProvider{})
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	f := &writerCredentialFixture{db: db, entry: entry, provider: provider, slot: "draft_document"}
	f.document = map[string]any{"document_id": "credential-doc", "provider_binding": map[string]any{"provider": provider, "document_id": "remote-1"}, "blocks": []any{}}
	if entry == "sync" {
		f.slot = "provider_document"
	}
	now := time.Now().UTC()
	humanID := "credential-human"
	value := map[string]any{"schema": "text/markdown", "data": "# Draft\n\nBody"}
	if entry == "sync" {
		value = map[string]any{"schema": "lazyllm.tools.writer.data_models.writer_ir.WriterDocument", "data": f.document}
	}
	for _, row := range []any{
		&orm.WorkflowSession{ID: "credential-session", ConversationID: "credential-conversation", WorkflowID: "writer-workflow", Status: "completed", CreateUserID: writerCredentialOwner, CreatedAt: now, UpdatedAt: now},
		&orm.WorkflowHumanArtifact{ID: humanID, SessionID: "credential-session", Slot: f.slot, ContentType: "json", Value: credentialJSON(t, value), DraftVersion: 1, CreatedAt: now},
		&orm.WorkflowSlotRevision{ID: "credential-revision", SessionID: "credential-session", SlotID: f.slot, Slot: f.slot, Revision: 1, Selected: true, Validity: "effective", HumanArtifactID: &humanID, ChangeSource: "human", StepID: "write_document", Attempt: 1, CreatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	privateRead := func(tx *gorm.DB) {
		sql := strings.ToLower(tx.Statement.SQL.String())
		for _, table := range []string{"plugin_slot_revisions", "plugin_human_artifacts", "sub_agent_artifacts"} {
			if tx.Statement.Table == table || strings.Contains(sql, table) {
				f.contentReads.Add(1)
				break
			}
		}
		if strings.HasPrefix(tx.Statement.Table, "user_") || strings.Contains(sql, "user_selected_providers") || strings.Contains(sql, "user_model_provider") {
			f.privateReads.Add(1)
		}
	}
	if err := db.Callback().Query().After("gorm:query").Register("writer-credential-private-read", privateRead); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().After("gorm:row").Register("writer-credential-private-row", privateRead); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Raw().After("gorm:raw").Register("writer-credential-private-raw", privateRead); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Callback().Query().Remove("writer-credential-private-read")
		db.Callback().Row().Remove("writer-credential-private-row")
		db.Callback().Raw().Remove("writer-credential-private-raw")
	})
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	return f
}
func credentialJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func (f *writerCredentialFixture) body() map[string]any {
	body := map[string]any{"base_revision": 1, "base_draft_version": 1}
	if f.entry == "sync" {
		body["source_document"] = f.document
		body["revised_document"] = f.document
		body["mode"] = "checkpoint"
	} else {
		body["slot"] = f.slot
		body["provider"] = f.provider
	}
	return body
}
func (f *writerCredentialFixture) call(t *testing.T, ctx context.Context, owner string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/writer-credentials", strings.NewReader(string(credentialJSON(t, body)))).WithContext(ctx)
	req.Header.Set("X-User-Id", owner)
	req = mux.SetURLVars(req, map[string]string{"session_id": "credential-session", "slot_id": f.slot, "list_index": "-1"})
	w := httptest.NewRecorder()
	if f.entry == "sync" {
		SyncWriterDocument(w, req)
	} else {
		WriteBackWriterDocument(w, req)
	}
	return w
}
func (f *writerCredentialFixture) snapshot(t *testing.T) string {
	t.Helper()
	var sessions []orm.WorkflowSession
	var revisions []orm.WorkflowSlotRevision
	var humans []orm.WorkflowHumanArtifact
	var events []orm.WorkflowEvent
	for _, rows := range []any{&sessions, &revisions, &humans, &events} {
		if err := f.db.Order("id").Find(rows).Error; err != nil {
			t.Fatal(err)
		}
	}
	return string(credentialJSON(t, []any{sessions, revisions, humans, events}))
}

type writerCredentialSpy struct {
	mu                 sync.Mutex
	lists              []string
	tokens             []string
	actions            []map[string]any
	mode               string
	count              int
	started, cancelled chan struct{}
	release            chan struct{}
}

func newWriterCredentialSpy(t *testing.T, f *writerCredentialFixture, count int, mode string) *writerCredentialSpy {
	t.Helper()
	spy := &writerCredentialSpy{count: count, mode: mode, started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1), release: make(chan struct{})}
	server := httptest.NewServer(adaptLegacyWriterFixture(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/authservice/v1/cloud/connections/internal/chat-enabled":
			provider := r.URL.Query().Get("provider")
			spy.mu.Lock()
			spy.lists = append(spy.lists, provider)
			spy.mu.Unlock()
			if r.Method != "GET" || r.URL.Query().Get("owner_user_id") != writerCredentialOwner || r.Header.Get("X-LazyMind-Internal-Token") != writerCredentialInternal {
				t.Error("credential list owner/internal authority mismatch")
			}
			if provider != f.provider {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}}})
				return
			}
			if mode == "cancel list" {
				spy.started <- struct{}{}
				select {
				case <-r.Context().Done():
					spy.cancelled <- struct{}{}
				case <-spy.release:
				}
				return
			}
			if mode == "list failure" {
				w.WriteHeader(503)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": writerCredentialPrivate})
				return
			}
			if mode == "invalid list JSON" {
				_, _ = w.Write([]byte(`{"data":`))
				return
			}
			if mode == "missing items" {
				_, _ = w.Write([]byte(`{"data":{}}`))
				return
			}
			if mode == "null items" {
				_, _ = w.Write([]byte(`{"data":{"items":null}}`))
				return
			}
			items := []any{}
			for i := 0; i < count; i++ {
				id := "connection-" + string(rune('1'+i))
				provider := f.provider
				if mode == "foreign provider" {
					provider = "unrelated-provider"
				}
				if mode == "missing connection" {
					id = ""
				}
				if mode == "duplicate connection" {
					id = "connection-1"
				}
				owner := writerCredentialOwner
				if mode == "foreign owner" {
					owner = "other-owner"
				}
				items = append(items, map[string]any{"connection_id": id, "provider": provider, "owner_user_id": owner, "status": "ACTIVE"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
		case strings.HasPrefix(r.URL.Path, "/api/authservice/v1/cloud/connections/") && strings.HasSuffix(r.URL.Path, "/token"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/authservice/v1/cloud/connections/"), "/token")
			spy.mu.Lock()
			spy.tokens = append(spy.tokens, id)
			spy.mu.Unlock()
			if r.Method != "GET" || r.URL.Query().Get("user_id") != writerCredentialOwner || r.Header.Get("X-LazyMind-Internal-Token") != writerCredentialInternal {
				t.Error("token owner/internal authority mismatch")
			}
			if mode == "cancel" {
				spy.started <- struct{}{}
				select {
				case <-r.Context().Done():
					spy.cancelled <- struct{}{}
				case <-spy.release:
				}
				return
			}
			if mode == "token failure" || (mode == "partial token failure" && id == "connection-2") {
				w.WriteHeader(503)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": writerCredentialPrivate})
				return
			}
			if mode == "invalid token JSON" {
				_, _ = w.Write([]byte(`{"data":`))
				return
			}
			token, provider := f.provider+"-fixture-token-"+id, f.provider
			if mode == "empty token" {
				token = ""
			}
			if mode == "foreign token provider" {
				provider = "unrelated-provider"
			}
			tokenID := id
			if mode == "foreign token connection" {
				tokenID = "other-connection"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": tokenID, "provider": provider, "status": "ACTIVE", "access_token": token}})
		case r.URL.Path == "/api/workflow/actions:invoke":
			var action map[string]any
			if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			spy.mu.Lock()
			spy.actions = append(spy.actions, action)
			spy.mu.Unlock()
			if r.Method != "POST" || r.Header.Get("X-LazyMind-Internal-Token") != "" || action["user_id"] != nil {
				t.Error("Algorithm authority or internal token leak")
			}
			if action["action"] == "convert_document" {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"provider": f.provider, "format": "native-fixture", "content": []any{}, "source_document": map[string]any{"document_id": "credential-doc", "blocks": []any{}}, "media_references": map[string]any{}}})
				return
			}
			result := map[string]any{"success": true, "changed": true, "provider_synced": true, "patch_result": map[string]any{"success": true}, "provider": f.provider, "persisted_document": f.document, "representation": "ir"}
			if f.entry == "writeback" {
				result["persisted_document"] = "# Draft\n\nBody"
				result["representation"] = "markdown"
				result["write_result"] = map[string]any{"doc_id": "remote-1"}
				result["target_document"] = map[string]any{"adapter": f.provider, "doc_id": "remote-1", "uri": f.provider + ":/fixture/remote-1"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
		default:
			spy.mu.Lock()
			spy.lists = append(spy.lists, "unexpected:"+r.URL.Path)
			spy.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}}})
		}
	})))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(spy.release) })
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", writerCredentialInternal)
	return spy
}
func (s *writerCredentialSpy) state() ([]string, []string, []map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lists...), append([]string(nil), s.tokens...), append([]map[string]any(nil), s.actions...)
}
func requireWriterCredentialPrivate(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, secret := range []string{writerCredentialPrivate, writerCredentialInternal, "-fixture-token-", "client-injected-credential"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("response leaked private credential detail: %s", secret)
		}
	}
}

func TestWriterScopedCredentialsSuccess(t *testing.T) {
	for _, entry := range []string{"sync", "writeback"} {
		for _, provider := range []string{"notion", "github", "feishu", "wechat", "obsidian"} {
			for _, count := range []int{1, 2} {
				t.Run(entry+"/"+provider+"/"+string(rune('0'+count)), func(t *testing.T) {
					f := newWriterCredentialFixture(t, entry, provider)
					credentialCount := count
					if provider == "obsidian" {
						credentialCount = 0
					}
					spy := newWriterCredentialSpy(t, f, credentialCount, "")
					body := f.body()
					body["tool_config"] = map[string]any{provider: "client-injected-credential"}
					w := f.call(t, t.Context(), writerCredentialOwner, body)
					if w.Code != 200 {
						t.Fatalf("writer success=%d %s", w.Code, w.Body.String())
					}
					requireWriterCredentialPrivate(t, w)
					lists, tokens, actions := spy.state()
					wantActions := []string{"sync_document"}
					if entry == "writeback" {
						wantActions = []string{"convert_document", "write_document"}
					}
					if len(actions) != len(wantActions) {
						t.Fatalf("Algorithm calls=%d", len(actions))
					}
					for i, action := range actions {
						if action["action"] != wantActions[i] {
							t.Error("writer action sequence changed")
						}
						config, _ := action["tool_config"].(map[string]any)
						if provider == "obsidian" {
							if len(config) != 0 {
								t.Error("local provider received cloud credentials")
							}
							continue
						}
						var expected any = provider + "-fixture-token-connection-1"
						if count == 2 {
							expected = []any{provider + "-fixture-token-connection-1", provider + "-fixture-token-connection-2"}
						}
						if !reflect.DeepEqual(config, map[string]any{provider: expected}) {
							t.Error("wrong provider credential shape or client injection")
						}
					}
					if provider == "obsidian" {
						if !reflect.DeepEqual(lists, []string{provider}) || len(tokens) != 0 {
							t.Error("local provider fetched cloud credentials")
						}
					} else {
						if !reflect.DeepEqual(lists, []string{provider}) || len(tokens) != count {
							t.Errorf("credential reads were not scoped: lists=%v token count=%d", lists, len(tokens))
						}
					}
					if f.privateReads.Load() != 0 {
						t.Errorf("unrelated search/academic/model credential reads=%d", f.privateReads.Load())
					}
					var selected orm.WorkflowSlotRevision
					if err := f.db.Where("session_id = ? AND slot_id = ? AND selected = ?", "credential-session", f.slot, true).First(&selected).Error; err != nil {
						t.Fatal(err)
					}
					if selected.Revision != 2 || selected.ChangeSource != "provider_sync" || selected.HumanArtifactID == nil {
						t.Errorf("original success persistence lost: %#v", selected)
					}
				})
			}
		}
	}
}

func TestWriterScopedCredentialsPreconditions(t *testing.T) {
	for _, entry := range []string{"sync", "writeback"} {
		for _, kind := range []string{"missing identity", "blank identity", "wrong owner", "unknown owner", "revision", "draft"} {
			t.Run(entry+"/"+kind, func(t *testing.T) {
				f := newWriterCredentialFixture(t, entry, "notion")
				spy := newWriterCredentialSpy(t, f, 1, "")
				body := f.body()
				owner := writerCredentialOwner
				status := 409
				switch kind {
				case "missing identity":
					owner = ""
					status = 400
				case "blank identity":
					owner = "  "
					status = 400
				case "wrong owner":
					owner = "other-owner"
					status = 404
				case "unknown owner":
					if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "credential-session").Update("create_user_id", "").Error; err != nil {
						t.Fatal(err)
					}
					status = 404
				case "revision":
					body["base_revision"] = 2
				case "draft":
					body["base_draft_version"] = 2
				}
				before := f.snapshot(t)
				f.contentReads.Store(0)
				w := f.call(t, t.Context(), owner, body)
				if w.Code != status {
					t.Errorf("precondition status=%d want=%d", w.Code, status)
				}
				if kind == "missing identity" || kind == "blank identity" {
					var response struct {
						Code int `json:"code"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response.Code != 2000205 {
						t.Errorf("identity error code=%d want2000205", response.Code)
					}
				}
				if kind != "revision" && kind != "draft" && f.contentReads.Load() != 0 {
					t.Error("unauthorized request read private Artifact content")
				}
				requireWriterCredentialPrivate(t, w)
				lists, tokens, actions := spy.state()
				if len(lists)+len(tokens)+len(actions) != 0 || f.privateReads.Load() != 0 {
					t.Error("precondition failed after credential or Algorithm access")
				}
				if f.snapshot(t) != before {
					t.Error("rejected writer request persisted")
				}
			})
		}
	}
}

func TestWriterScopedCredentialsMissingAndFailures(t *testing.T) {
	for _, entry := range []string{"sync", "writeback"} {
		for _, mode := range []string{"no connections", "list failure", "invalid list JSON", "missing items", "null items", "foreign provider", "foreign owner", "missing connection", "duplicate connection", "token failure", "partial token failure", "invalid token JSON", "empty token", "foreign token provider", "foreign token connection"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				f := newWriterCredentialFixture(t, entry, "notion")
				count := 1
				if mode == "no connections" {
					count = 0
				}
				if mode == "partial token failure" || mode == "duplicate connection" {
					count = 2
				}
				spy := newWriterCredentialSpy(t, f, count, mode)
				before := f.snapshot(t)
				w := f.call(t, t.Context(), writerCredentialOwner, f.body())
				status := 502

				if w.Code != status {
					t.Errorf("credential failure status=%d want=%d", w.Code, status)
				}
				requireWriterCredentialPrivate(t, w)
				lists, tokens, actions := spy.state()
				if !reflect.DeepEqual(lists, []string{"notion"}) || len(actions) != 0 || f.privateReads.Load() != 0 {
					t.Errorf("credential failure fell back or invoked writer: lists=%v actions=%d", lists, len(actions))
				}
				if mode == "no connections" || mode == "foreign provider" || mode == "foreign owner" || mode == "missing connection" || mode == "duplicate connection" {
					if len(tokens) != 0 {
						t.Error("invalid connection list reached token fetch")
					}
				}
				if f.snapshot(t) != before {
					t.Error("credential failure persisted or used partial tokens")
				}
			})
		}
	}
}

func TestWriterScopedCredentialsCancellation(t *testing.T) {
	for _, entry := range []string{"sync", "writeback"} {
		t.Run(entry, func(t *testing.T) {
			f := newWriterCredentialFixture(t, entry, "notion")
			spy := newWriterCredentialSpy(t, f, 1, "cancel")
			before := f.snapshot(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- f.call(t, ctx, writerCredentialOwner, f.body()) }()
			select {
			case <-spy.started:
			case <-done:
				t.Fatal("token cancellation boundary not reached")
			case <-time.After(2 * time.Second):
				t.Fatal("token fetch did not start")
			}
			cancel()
			select {
			case <-spy.cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("Auth token cancellation not propagated")
			}
			select {
			case w := <-done:
				if w.Code != 502 {
					t.Errorf("cancel status=%d want502", w.Code)
				}
				requireWriterCredentialPrivate(t, w)
			case <-time.After(2 * time.Second):
				t.Fatal("cancel did not finish")
			}
			lists, _, actions := spy.state()
			if !reflect.DeepEqual(lists, []string{"notion"}) || len(actions) != 0 || f.privateReads.Load() != 0 {
				t.Error("cancel continued loading other credentials or invoked Algorithm")
			}
			if f.snapshot(t) != before {
				t.Error("cancel persisted")
			}
		})
	}
}

func TestWriterScopedCredentialsDoNotLogAuthDetails(t *testing.T) {
	for _, entry := range []string{"sync", "writeback"} {
		t.Run(entry, func(t *testing.T) {
			f := newWriterCredentialFixture(t, entry, "notion")
			newWriterCredentialSpy(t, f, 1, "list failure")
			output, err := os.CreateTemp(t.TempDir(), "credential-log-")
			if err != nil {
				t.Fatal(err)
			}
			previous := os.Stdout
			os.Stdout = output
			defer func() { os.Stdout = previous; output.Close() }()
			w := f.call(t, t.Context(), writerCredentialOwner, f.body())
			os.Stdout = previous
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Clean(output.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), writerCredentialPrivate) {
				t.Error("Auth failure detail leaked through document credential logging")
			}
			requireWriterCredentialPrivate(t, w)
		})
	}
}

func TestWriterScopedCredentialsFixtureControl(t *testing.T) {
	f := newWriterCredentialFixture(t, "sync", "notion")
	spy := newWriterCredentialSpy(t, f, 1, "")
	// Direct Auth control proves the strict service fixture and selected token
	// shape work without relying on the still-broken document loader.
	url := os.Getenv("LAZYMIND_AUTH_SERVICE_URL")
	request, err := http.NewRequest("GET", url+"/api/authservice/v1/cloud/connections/internal/chat-enabled?provider=notion&owner_user_id="+writerCredentialOwner, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-LazyMind-Internal-Token", writerCredentialInternal)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.Contains(string(body), "connection-1") {
		t.Fatal("Auth fixture not usable")
	}
	lists, tokens, actions := spy.state()
	if !reflect.DeepEqual(lists, []string{"notion"}) || len(tokens)+len(actions) != 0 {
		t.Error("fixture control unexpected I/O")
	}
	request, err = http.NewRequest("GET", url+"/api/authservice/v1/cloud/connections/connection-1/token?user_id="+writerCredentialOwner, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-LazyMind-Internal-Token", writerCredentialInternal)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data struct {
			Provider     string `json:"provider"`
			ConnectionID string `json:"connection_id"`
			AccessToken  string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || result.Data.Provider != "notion" || result.Data.ConnectionID != "connection-1" || result.Data.AccessToken != "notion-fixture-token-connection-1" {
		t.Error("token fixture not usable")
	}
	// Prove the raw SQL credential-query monitor, which the real search loader uses.
	if err := f.db.Raw("SELECT * FROM user_selected_providers").Scan(&[]orm.UserSelectedProvider{}).Error; err != nil {
		t.Fatal(err)
	}
	if f.privateReads.Load() == 0 {
		t.Error("credential SQL monitor missed raw scan")
	}

}

func TestWriterScopedCredentialsListCancellation(t *testing.T) {
	for _, entry := range []string{"sync", "writeback"} {
		t.Run(entry, func(t *testing.T) {
			f := newWriterCredentialFixture(t, entry, "notion")
			spy := newWriterCredentialSpy(t, f, 1, "cancel list")
			before := f.snapshot(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- f.call(t, ctx, writerCredentialOwner, f.body()) }()
			select {
			case <-spy.started:
			case <-done:
				t.Fatal("list cancellation boundary not reached")
			case <-time.After(2 * time.Second):
				t.Fatal("list request did not start")
			}
			cancel()
			select {
			case <-spy.cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("list cancellation not propagated")
			}
			select {
			case w := <-done:
				if w.Code != 502 {
					t.Errorf("cancelled list status=%d want502", w.Code)
				}
				requireWriterCredentialPrivate(t, w)
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled list did not finish")
			}
			lists, tokens, actions := spy.state()
			if !reflect.DeepEqual(lists, []string{"notion"}) || len(tokens)+len(actions) != 0 || f.privateReads.Load() != 0 {
				t.Error("cancelled list continued credential loading or invoked writer")
			}
			if f.snapshot(t) != before {
				t.Error("cancelled list persisted")
			}
		})
	}
}
