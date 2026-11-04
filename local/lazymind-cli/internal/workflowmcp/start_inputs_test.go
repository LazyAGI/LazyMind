package workflowmcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/connectorconfig"
)

func inputToolSession(t *testing.T, client *Client) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "input-test", Version: "1"}, nil)
	Register(server, client)
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "external-agent-test", Version: "1"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func inputToolCall(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStartImportsAndBindsFilesForEveryHost(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	for _, provider := range []string{"codex", "cursor", "workbuddy", "deepseek-harness", "traework", "raccoon", ""} {
		t.Run(provider, func(t *testing.T) {
			// Both sources live outside the MCP process cwd, just like attachments
			// or files in an independently selected conversation workspace.
			brief := filepath.Join(t.TempDir(), "brief.pdf")
			chart := filepath.Join(t.TempDir(), "chart.png")
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
			if err := os.WriteFile(brief, []byte("user's PDF source bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(chart, png, 0600); err != nil {
				t.Fatal(err)
			}
			resources := map[string]InputResource{"existing": {ResourceID: "existing", Revision: 2, MIMEType: "text/plain", ContentHash: "sha256:existing", ContentBase64: base64.StdEncoding.EncodeToString([]byte("existing source"))}}
			byHash := map[string]string{}
			var preparation []byte
			var bindings map[string]any
			prepared, consumed := 0, 0
			client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
				var response any
				switch {
				case r.URL.Path == "/api/core/workflow-input-resources":
					var resource InputResource
					if err := json.NewDecoder(r.Body).Decode(&resource); err != nil {
						t.Error(err)
					}
					content, err := base64.StdEncoding.DecodeString(resource.ContentBase64)
					sum := sha256.Sum256(content)
					if err != nil || resource.Size != int64(len(content)) || resource.ContentHash != "sha256:"+hex.EncodeToString(sum[:]) {
						t.Errorf("corrupted upload: %+v", resource)
					}
					id := byHash[resource.ContentHash]
					if id == "" {
						id = fmt.Sprintf("file-%d", len(byHash)+1)
						byHash[resource.ContentHash] = id
						resource.ResourceID, resource.Revision = id, 1
						resources[id] = resource
					}
					response = resources[id]
				case r.URL.Path == "/api/core/workflow-preparations":
					body, _ := io.ReadAll(r.Body)
					if strings.Contains(string(body), brief) || strings.Contains(string(body), chart) || strings.Contains(string(body), "local_path") {
						t.Error("Connector-host paths leaked into Core preparation")
					}
					if preparation != nil && string(body) != string(preparation) {
						w.WriteHeader(http.StatusConflict)
						w.Write([]byte(`{"ok":false,"error":{"code":"IDEMPOTENCY_CONFLICT","message":"idempotency key was used with another payload"}}`))
						return
					}
					preparation = body
					var request struct {
						InputBindings map[string]any `json:"input_bindings"`
					}
					if err := json.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					bindings = request.InputBindings
					prepared++
					response = map[string]any{"preparation_id": "prep-1"}
				case r.URL.Path == "/api/core/workflow-preparations/prep-1:consume":
					consumed++
					response = map[string]any{"session_id": "run-1", "workflow_id": "ppt-workflow", "workflow_revision_id": "revision-1"}
				case r.URL.Path == "/api/core/workflow-sessions/run-1/projection":
					response = map[string]any{"session_id": "run-1", "state_version": 1, "control": map[string]any{"protocol": ControlProtocol, "continuation": "continue", "admission": map[string]any{"can_begin": true}}}
				case r.URL.Path == "/api/core/workflow-sessions/run-1/executions:begin":
					response = map[string]any{"receipt": map[string]any{"execution_id": "execution-1"}}
				case r.URL.Path == "/api/core/workflow-sessions/run-1/hosted-attempts/execution-1:begin":
					response = map[string]any{"execution_id": "execution-1", "execution_handle": "handle", "executor_host": "external-agent", "step_contract": map[string]any{"inputs": bindings}}
				case strings.HasPrefix(r.URL.Path, "/api/core/workflow-input-resources/"):
					response = resources[strings.TrimPrefix(r.URL.Path, "/api/core/workflow-input-resources/")]
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			})
			client.HostProvider = provider
			session := inputToolSession(t, client)
			args := map[string]any{"workflow_id": "ppt-workflow", "idempotency_key": "start-with-materials-retry", "inputs": map[string]any{"uploaded_materials": []any{
				map[string]any{"local_path": brief}, map[string]any{"resource_id": "existing", "revision": 2, "content_hash": "sha256:existing"}, map[string]any{"local_path": chart}, map[string]any{"local_path": brief},
			}}, "input_bindings": map[string]any{"other_source": map[string]any{"resource_id": "existing"}}}
			for range 2 {
				result := inputToolCall(t, session, "workflow.start", args)
				if result.IsError {
					t.Fatalf("start failed: %+v", result.Content)
				}
			}
			if prepared != 2 || consumed != 2 || len(byHash) != 2 || bindings["other_source"] == nil {
				t.Fatalf("missing imports/bindings: prepared=%d consumed=%d resources=%d bindings=%v", prepared, consumed, len(byHash), bindings)
			}
			step, err := client.Begin(t.Context(), BeginInput{SessionID: "run-1", StepID: "read_sources"})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, item := range step.Execution.StepContract.Inputs["uploaded_materials"].([]any) {
				ref := item.(map[string]any)
				id := ref["resource_id"].(string)
				got = append(got, id)
				read := inputToolCall(t, session, "workflow.input.get", map[string]any{"resource_id": id})
				if read.IsError {
					t.Fatalf("bound source unavailable: %s", id)
				}
				if id == "file-2" {
					if len(read.Content) != 2 {
						t.Fatalf("missing native image content: %+v", read.Content)
					}
					image, ok := read.Content[1].(*mcp.ImageContent)
					if !ok || !reflect.DeepEqual(image.Data, png) {
						t.Fatal("step could not read the exact chart bytes")
					}
				} else {
					encoded, _ := json.Marshal(read.StructuredContent)
					if !strings.Contains(string(encoded), resources[id].ContentBase64) {
						t.Fatal("step could not read the exact bound source bytes")
					}
				}
			}
			if !reflect.DeepEqual(got, []string{"file-1", "existing", "file-2", "file-1"}) {
				t.Fatalf("source order or repetition lost: %v", got)
			}
			if err := os.WriteFile(brief, []byte("changed source bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			conflict := inputToolCall(t, session, "workflow.start", args)
			encoded, _ := json.Marshal(conflict.Content)
			if !conflict.IsError || !strings.Contains(string(encoded), "IDEMPOTENCY_CONFLICT") || consumed != 2 {
				t.Fatalf("changed source reused a start identity: %s consumed=%d", encoded, consumed)
			}
		})
	}
}

func TestStartInputFailuresDoNotPrepareOrConsumeRun(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	file := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(file, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(t.TempDir(), "large.txt")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxLocalArtifactBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		inputs   map[string]any
		bindings map[string]any
		want     string
	}{
		{"missing file", map[string]any{"sources": []any{map[string]any{"local_path": file}, map[string]any{"local_path": file + ".missing"}}}, nil, "resolve local_path"},
		{"relative path", map[string]any{"sources": []any{map[string]any{"local_path": "source.txt"}}}, nil, "absolute path"},
		{"directory", map[string]any{"sources": []any{map[string]any{"local_path": filepath.Dir(file)}}}, nil, "regular file"},
		{"oversized", map[string]any{"sources": []any{map[string]any{"local_path": large}}}, nil, "20 MiB"},
		{"two source forms", map[string]any{"sources": []any{map[string]any{"local_path": file, "resource_id": "existing"}}}, nil, "exactly one"},
		{"empty source", map[string]any{"sources": []any{map[string]any{}}}, nil, "exactly one"},
		{"empty list", map[string]any{"sources": []any{}}, nil, "must not be empty"},
		{"empty material", map[string]any{"": []any{map[string]any{"local_path": file}}}, nil, "material ID"},
		{"duplicate material", map[string]any{"sources": []any{map[string]any{"local_path": file}}}, map[string]any{"sources": map[string]any{"resource_id": "existing"}}, "not both"},
		{"file revision", map[string]any{"sources": []any{map[string]any{"local_path": file, "revision": 1}}}, nil, "only valid with resource_id"},
		{"zero revision", map[string]any{"sources": []any{map[string]any{"resource_id": "existing", "revision": 0}}}, nil, "positive revision"},
		{"upload rejected", map[string]any{"sources": []any{map[string]any{"local_path": file}}}, nil, "import failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if tc.name != "upload rejected" || r.URL.Path != "/api/core/workflow-input-resources" {
					t.Errorf("invalid input contacted Core: %s", r.URL.Path)
				}
				w.WriteHeader(503)
				w.Write([]byte(`{"ok":false,"error":{"code":"INPUT_IMPORT_FAILED","message":"storage unavailable"}}`))
			})
			args := map[string]any{"workflow_id": "wf", "inputs": tc.inputs}
			if tc.bindings != nil {
				args["input_bindings"] = tc.bindings
			}
			result := inputToolCall(t, inputToolSession(t, client), "workflow.start", args)
			encoded, _ := json.Marshal(result.Content)
			if !result.IsError || !strings.Contains(string(encoded), tc.want) {
				t.Fatalf("result=%s, want %s", encoded, tc.want)
			}
			if tc.name != "upload rejected" && requests != 0 {
				t.Fatal("invalid local input caused an upload")
			}
		})
	}
}

func TestCancelledStartDoesNotImportFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := (&Client{}).prepareStartInputs(ctx, map[string][]InputSource{"sources": {{ResourceID: "existing"}}}, nil)
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

func TestStartUploadFailureDoesNotConsumePartiallyPreparedSources(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	var sources []InputSource
	for _, name := range []string{"brief.txt", "chart.txt"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, InputSource{LocalPath: path})
	}
	uploads := 0
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/core/workflow-input-resources" {
			t.Errorf("partial input failure created a run or deleted a shared resource: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		uploads++
		if uploads == 1 {
			w.Write([]byte(`{"resource_id":"reusable-brief","revision":1,"content_hash":"sha256:brief"}`))
		} else {
			w.WriteHeader(503)
			w.Write([]byte(`{"ok":false,"error":{"code":"INPUT_IMPORT_FAILED","message":"storage unavailable"}}`))
		}
	})
	_, err := client.Start(t.Context(), StartInput{WorkflowID: "wf", Inputs: map[string][]InputSource{"sources": sources}})
	if uploads != 2 || err == nil || !strings.Contains(err.Error(), `inputs["sources"][1] (chart.txt): import failed`) {
		t.Fatalf("partial upload failure lost: uploads=%d err=%v", uploads, err)
	}
}

func TestConcurrentStartsKeepConversationFilesSeparate(t *testing.T) {
	t.Setenv(connectorconfig.EnvironmentKey, "")
	var mu sync.Mutex
	prepared := map[string]string{}
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var response any
		switch {
		case r.URL.Path == "/api/core/workflow-input-resources":
			var resource InputResource
			if err := json.NewDecoder(r.Body).Decode(&resource); err != nil {
				t.Error(err)
			}
			content, _ := base64.StdEncoding.DecodeString(resource.ContentBase64)
			resource.ResourceID, resource.Revision = string(content), 1
			response = resource
		case r.URL.Path == "/api/core/workflow-preparations":
			var request struct {
				RequestContext string                     `json:"request_context"`
				InputBindings  map[string][]InputResource `json:"input_bindings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			items := request.InputBindings["sources"]
			if len(items) != 1 || items[0].ResourceID != request.RequestContext {
				t.Errorf("conversation input mixed: %+v", request)
			}
			prepared[request.RequestContext] = request.RequestContext
			response = map[string]any{"preparation_id": request.RequestContext}
		case strings.HasSuffix(r.URL.Path, ":consume"):
			prep := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/core/workflow-preparations/"), ":consume")
			response = map[string]any{"session_id": prepared[prep], "workflow_id": "wf"}
		case strings.HasSuffix(r.URL.Path, "/projection"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/core/workflow-sessions/"), "/projection")
			response = map[string]any{"session_id": id}
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	var work sync.WaitGroup
	for _, id := range []string{"conversation-a", "conversation-b"} {
		path := filepath.Join(t.TempDir(), "source.txt")
		if err := os.WriteFile(path, []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		work.Go(func() {
			result, err := client.Start(t.Context(), StartInput{WorkflowID: "wf", RequestContext: id, IdempotencyKey: id, Inputs: map[string][]InputSource{"sources": {{LocalPath: path}}}})
			if err != nil || result.SessionID != id {
				t.Errorf("start %s: %+v %v", id, result, err)
			}
		})
	}
	work.Wait()
	if len(prepared) != 2 {
		t.Fatalf("missing conversation inputs: %v", prepared)
	}
}
