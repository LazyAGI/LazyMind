package chat

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestExternalAgentPromptPreservesSelectedWorkflow(t *testing.T) {
	body := map[string]any{
		"enable_workflow":       true,
		"allowed_workflow_refs": []string{"ppt-workflow"},
		"history":               []string{"must not copy unrelated history"},
	}
	prompt := externalAgentPrompt("生成四页 PPT", body)
	for _, want := range []string{"生成四页 PPT", "ppt-workflow", "workflow.start", "workflow.artifact.publish"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "unrelated history") {
		t.Fatal("copied unrelated request data")
	}
	body["workflow_context"] = map[string]any{"session_id": "existing-session", "current_step": "generate_ppt"}
	prompt = externalAgentPrompt("继续", body)
	if !strings.Contains(prompt, "existing-session") || !strings.Contains(prompt, "workflow.state") || strings.Contains(prompt, "call workflow.start") {
		t.Fatalf("must resume the existing workflow: %s", prompt)
	}
	body["enable_workflow"] = false
	if got := externalAgentPrompt("普通对话", body); got != "普通对话" {
		t.Fatalf("disabled workflow injected: %s", got)
	}
}

func TestExternalPPTAttachmentIsValidatedAndManaged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	t.Setenv("LAZYMIND_FILE_URL_SIGN_SECRET", "external-ppt-test-secret")
	app, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	createExternalChatTestRun(t, app, "run-ppt")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	for _, name := range []string{"[Content_Types].xml", "ppt/presentation.xml"} {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("<fixture/>")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	publish := func(owner string, content []byte) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{
			"host_id": job.HostID, "lease_token": job.LeaseToken, "event_id": "ppt-file",
			"filename": "deck.pptx", "media_type": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			"content_base64": base64.StdEncoding.EncodeToString(content),
		})
		req := httptest.NewRequest(http.MethodPost, "/external-chat/runs/run-ppt:attachment", bytes.NewReader(body))
		req.Header.Set("X-User-Id", owner)
		req = mux.SetURLVars(req, map[string]string{"run_id": job.RunID})
		rec := httptest.NewRecorder()
		PublishExternalChatAttachment(rec, req)
		return rec
	}
	if rec := publish("user-1", []byte("not a PPT")); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid file accepted: %d", rec.Code)
	}
	if rec := publish("user-2", content.Bytes()); rec.Code == http.StatusOK {
		t.Fatal("another user uploaded a file to this run")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if rec := publish("user-1", content.Bytes()); rec.Code != http.StatusOK {
			t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
		}
	}
	if _, err := app.appendEvent(context.Background(), "user-1", job.RunID, job.HostID, job.LeaseToken,
		externalChatEvent{EventID: "done", Type: "completed"}); err != nil {
		t.Fatal(err)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id = ?", job.HistoryID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(history.Result, "[deck.pptx](/static-files/external-agent-attachments/") || strings.Count(history.Result, "[deck.pptx]") != 1 || strings.Contains(history.Result, "![") {
		t.Fatalf("bad download reference: %s", history.Result)
	}
	reference := strings.Split(strings.Split(history.Result, "](")[1], ")")[0]
	body, _ := json.Marshal(map[string]any{"paths": []string{reference}})
	req := httptest.NewRequest(http.MethodPost, "/static-files:sign", bytes.NewReader(body))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	doc.SignStaticFiles(rec, req)
	var signed struct {
		URLs map[string]string `json:"urls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil || signed.URLs[reference] == "" {
		t.Fatalf("download URL not signed: %s err=%v", rec.Body.String(), err)
	}
	req = httptest.NewRequest(http.MethodGet, signed.URLs[reference], nil)
	req = mux.SetURLVars(req, map[string]string{"path": strings.TrimPrefix(reference, "/static-files/")})
	rec = httptest.NewRecorder()
	doc.GetSignedStaticFile(rec, req)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content.Bytes()) {
		t.Fatalf("download differs: status=%d bytes=%d", rec.Code, rec.Body.Len())
	}
	files := 0
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files++
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, content.Bytes()) || filepath.Ext(path) != ".pptx" {
				t.Fatalf("saved file differs: %v", err)
			}
		}
		return nil
	}); err != nil || files != 1 {
		t.Fatalf("files=%d err=%v", files, err)
	}
}

func TestExternalModelEventAPI(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	createExternalChatTestRun(t, app, "run-model")
	job, err := app.claim(context.Background(), "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim: %v", err)
	}
	for _, tc := range []struct {
		model string
		want  int
	}{{" ", http.StatusBadRequest}, {strings.Repeat("x", 257), http.StatusBadRequest}, {"actual-model", http.StatusOK}} {
		body, _ := json.Marshal(map[string]any{"host_id": job.HostID, "lease_token": job.LeaseToken,
			"event_id": "model", "type": "model", "text": tc.model})
		req := httptest.NewRequest(http.MethodPost, "/external-chat/runs/run-model:event", bytes.NewReader(body))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"run_id": job.RunID})
		rec := httptest.NewRecorder()
		PublishExternalChatEvent(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
		}
	}
	projections, err := app.executionProjections(context.Background(), "user-1", []string{job.HistoryID})
	if err != nil || projections[job.HistoryID].Model != "actual-model" {
		t.Fatalf("models=%#v err=%v", projections, err)
	}
}

func TestExternalTerminalExecutionReachesChatSSE(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			app, db := newExternalChatTestApplication(t)
			createExternalChatTestRun(t, app, "run-handoff")
			ctx := context.Background()
			job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
			if err != nil || job == nil {
				t.Fatalf("claim: %v", err)
			}
			for _, event := range []externalChatEvent{
				{EventID: "model", Type: "model", Text: "actual-codex-model"},
				{EventID: "message", Type: "message", Text: "result"},
				{EventID: "terminal", Type: status},
			} {
				if _, err := app.appendEvent(ctx, "user-1", job.RunID, job.HostID, job.LeaseToken, event); err != nil {
					t.Fatal(err)
				}
			}
			recorder := httptest.NewRecorder()
			streamSingleAnswer(ctx, ctx, recorder, recorder, db, nil, "", map[string]any{
				"_chat_executor": ChatExecutorCodex, "_external_run_id": job.RunID,
				"user_id": "user-1", "run_id": job.RunID,
			}, job.ConversationID, "question", job.HistoryID, chatPersistTarget{HistoryID: job.HistoryID, Seq: 1}, json.RawMessage(`{}`))
			for _, line := range strings.Split(recorder.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				var envelope struct {
					Result ChatChunkResponse `json:"result"`
				}
				if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &envelope) != nil {
					continue
				}
				chunk := envelope.Result
				if chunk.RuntimeEvent == nil || chunk.RuntimeEvent.Type != RuntimeEventRunFinished {
					continue
				}
				if chunk.Execution == nil || chunk.Execution.Status != status || chunk.Execution.Model != "actual-codex-model" {
					t.Fatalf("terminal lost execution metadata: %#v", chunk.Execution)
				}
				return
			}
			t.Fatalf("missing terminal event: %s", recorder.Body.String())
		})
	}
}
