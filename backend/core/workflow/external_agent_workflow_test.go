package workflow

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func TestWorkflowModelConfigurationReportsOnlySelectedIdentity(t *testing.T) {
	config := map[string]any{"llm": map[string]any{
		"source": "openai", "model": "Qwen/Qwen3.8-Flash-Next",
		"api_key": "private-key", "base_url": "https://private.example/v1",
	}}
	result := workflowModelConfiguration("owner-1", config, nil)
	if result["ready"] != true || result["provider"] != "openai" || result["model"] != "Qwen/Qwen3.8-Flash-Next" || result["user_id"] != "owner-1" {
		t.Fatalf("unexpected model identity: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "api_key") {
		t.Fatalf("model identity leaked credentials: %s", encoded)
	}
	for _, config := range []map[string]any{nil, {}, {"llm": map[string]any{"model": "missing-provider"}}, {"vlm": map[string]any{"source": "openai", "model": "vision"}}} {
		result = workflowModelConfiguration("owner-1", config, nil)
		if result["ready"] != false || result["error_code"] != "MODEL_NOT_CONFIGURED" {
			t.Fatalf("missing chat model must not be ready: %#v", result)
		}
	}
	result = workflowModelConfiguration("owner-1", config, errors.New("private database details"))
	if result["ready"] != false || result["error_code"] != "MODEL_CONFIG_LOAD_FAILED" {
		t.Fatalf("load failure must not be ready: %#v", result)
	}
}

func TestCreateExternalAgentWorkflowTaskStartsHostedConversion(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skill-1", "# Demo Skill\n\nUse this skill to transform a user request into a complete workflow, keep observable outputs, preserve acceptance criteria, and avoid asking the external agent for mid-run confirmation.")
	seedExternalAgentSkillSource(t, db, "user-1", "skill-1", "demo-skill", "https://skillhub.cn/skills/user_demo/demo-skill")

	req := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", strings.NewReader(`{
		"agent_type":"Codex",
		"skill":{"name":"demo-skill","url":"https://skillhub.cn/skills/user_demo/demo-skill"},
		"task_description":"Run the skill for the external user.",
		"external_thread_id":"codex-thread",
		"idempotency_key":"request-1"
	}`))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	CreateExternalAgentWorkflowTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	taskID, _ := envelope.Data["task_id"].(string)
	if taskID == "" || envelope.Data["status"] != externalTaskStatusQueued || envelope.Data["draft_id"] != "" {
		t.Fatalf("unexpected task response: %#v", envelope.Data)
	}
	if _, exists := envelope.Data["skill_id"]; exists {
		t.Fatalf("external response leaked internal skill_id: %#v", envelope.Data)
	}

	duplicate := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", strings.NewReader(`{
		"agent_type":"codex",
		"skill":{"name":"demo-skill","url":"https://skillhub.cn/skills/user_demo/demo-skill"},
		"task_description":"Run the skill for the external user.",
		"external_thread_id":"codex-thread",
		"idempotency_key":"request-1"
	}`))
	duplicate.Header.Set("X-User-Id", "user-1")
	dupRec := httptest.NewRecorder()
	CreateExternalAgentWorkflowTask(dupRec, duplicate)
	if dupRec.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", dupRec.Code, dupRec.Body.String())
	}
	var duplicateEnvelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(dupRec.Body.Bytes(), &duplicateEnvelope); err != nil {
		t.Fatal(err)
	}
	if duplicateEnvelope.Data["task_id"] != taskID {
		t.Fatalf("idempotent request returned different task: %q vs %q", duplicateEnvelope.Data["task_id"], taskID)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/external-agent/workflow-tasks/"+taskID, nil)
	getReq.Header.Set("X-User-Id", "user-1")
	getReq = mux.SetURLVars(getReq, map[string]string{"task_id": taskID})
	getRec := httptest.NewRecorder()
	GetExternalAgentWorkflowTask(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var queued orm.ExternalAgentWorkflowTask
	if err := db.First(&queued, "id=?", taskID).Error; err != nil {
		t.Fatal(err)
	}
	if queued.Status != externalTaskStatusQueued || queued.DraftID != "" {
		t.Fatalf("GET must not drive task execution: %#v", queued)
	}
	var job orm.AsyncJob
	if err := db.First(&job, "resource_id=? AND job_type=?", taskID, externalWorkflowTaskJobType).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := handleExternalWorkflowTaskJob(context.Background(), asyncjob.Job{
		ID: job.ID, ResourceID: taskID, CreateUserID: "user-1",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&queued, "id=?", taskID).Error; err != nil {
		t.Fatal(err)
	}
	if queued.Status != externalTaskStatusConverting || queued.DraftID == "" {
		t.Fatalf("background job did not start conversion: %#v", queued)
	}
	var successors int64
	db.Model(&orm.AsyncJob{}).Where("job_type=? AND idempotency_key=?", externalWorkflowTaskJobType, taskID+":"+job.ID).Count(&successors)
	if successors != 1 {
		t.Fatalf("expected one durable continuation, got %d", successors)
	}
}

func TestCreateExternalAgentWorkflowTaskRejectsUnsupportedAgent(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", strings.NewReader(`{
		"agent_type":"unknown",
		"skill":{"name":"demo-skill","url":"https://skillhub.cn/skills/user_demo/demo-skill"},
		"task_description":"Run it."
	}`))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	CreateExternalAgentWorkflowTask(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNormalizeExternalAgentTypeAliases(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/fixtures/external_agent_types.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input     string
		Canonical string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		if got := normalizeExternalAgentType(test.Input); got != test.Canonical {
			t.Fatalf("normalizeExternalAgentType(%q)=%q want %q", test.Input, got, test.Canonical)
		}
	}
}

func TestCreateExternalAgentWorkflowTaskNormalizesLegacyRaccoonMarkers(t *testing.T) {
	newHandlerTestDB(t)
	for _, agentType := range []string{"xiaohuanxiong", "xiaohuan-xiong", "小浣熊", "raccoon"} {
		req := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", strings.NewReader(`{
			"agent_type":"`+agentType+`",
			"skill":{"name":"demo-skill","url":"https://skillhub.cn/skills/user_demo/demo-skill"},
			"task_description":"Run it."
		}`))
		req.Header.Set("X-User-Id", "user-1")
		rec := httptest.NewRecorder()
		CreateExternalAgentWorkflowTask(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("agent_type=%q status=%d body=%s", agentType, rec.Code, rec.Body.String())
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data["agent_type"] != "raccoon-work" {
			t.Fatalf("agent_type=%q was not normalized: %#v", agentType, envelope.Data)
		}
	}
}

func TestCreateExternalAgentWorkflowTaskRejectsUnknownSchemaFields(t *testing.T) {
	newHandlerTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", strings.NewReader(`{
		"agent_type":"codex",
		"skill_id":"skill-1",
		"skill":{"name":"demo-skill","url":"https://skillhub.cn/skills/user_demo/demo-skill"},
		"task_description":"Run it."
	}`))
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	CreateExternalAgentWorkflowTask(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResolveExternalAgentSkillSourceInstallsZipBase64(t *testing.T) {
	db := newHandlerTestDB(t)
	t.Setenv("LAZYMIND_SKILL_OBJECT_ROOT", t.TempDir())
	zipContent := skillZipBytes(t, map[string][]byte{
		"SKILL.md": []byte("---\nname: zip-skill\ndescription: Installed from zip\n---\n# Zip Skill\n"),
	})
	resolved, err := resolveExternalAgentSkillSource(context.Background(), db.DB, "user-1", "User One", externalAgentWorkflowSkillInput{
		Name:      "zip-skill",
		ZipBase64: base64.StdEncoding.EncodeToString(zipContent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SkillID == "" || resolved.SourceType != "zip" || resolved.InstallStatus != "installed" {
		t.Fatalf("resolved = %#v", resolved)
	}
	var skill orm.SkillV2Skill
	if err := db.Where("id=? AND owner_user_id=?", resolved.SkillID, "user-1").First(&skill).Error; err != nil {
		t.Fatal(err)
	}
	if skill.SkillName != "zip-skill" || skill.HeadRevisionID == nil {
		t.Fatalf("installed skill = %#v", skill)
	}
	var mapping orm.ExternalAgentSkillSource
	if err := db.Where("owner_user_id=? AND source_type=? AND source_key=?", "user-1", "zip", resolved.SourceKey).First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if mapping.ResolvedSkillID != resolved.SkillID {
		t.Fatalf("mapping = %#v", mapping)
	}
}

func TestResolveExternalAgentSkillSourceReusesNativeSource(t *testing.T) {
	db := newHandlerTestDB(t)
	seedSkillForWorkflowConversion(t, db, "user-1", "skillhub-installed", "# SkillHub Finder\n\nFind skills on SkillHub.")
	if err := db.Model(&orm.SkillV2Skill{}).Where("id=?", "skillhub-installed").Updates(map[string]any{
		"category":      "external",
		"skill_name":    "find-skill-skillhub",
		"relative_root": "external/find-skill-skillhub",
	}).Error; err != nil {
		t.Fatal(err)
	}
	addSkillRevisionFileForWorkflowConversion(t, db, "skillhub-installed-rev", "_meta.json", `{"ownerId":"421931","slug":"find-skill-skillhub","version":"1.0.2"}`, "text")
	if err := db.Model(&orm.SkillV2Revision{}).Where("id=?", "skillhub-installed-rev").Updates(map[string]any{
		"source_ref_type": "url", "source_ref_id": "https://skillhub.cn/skills/user_290ac21c/find-skill-skillhub",
	}).Error; err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveExternalAgentSkillSource(context.Background(), db.DB, "user-1", "User One", externalAgentWorkflowSkillInput{
		Name: "find-skill-skillhub-codex-test",
		URL:  "https://skillhub.cn/skills/user_290ac21c/find-skill-skillhub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SkillID != "skillhub-installed" || resolved.InstallStatus != "reused" || resolved.SourceType != "url" {
		t.Fatalf("expected installed SkillHub skill to be reused, got %#v", resolved)
	}
	var skills int64
	if err := db.Model(&orm.SkillV2Skill{}).Where("owner_user_id=?", "user-1").Count(&skills).Error; err != nil {
		t.Fatal(err)
	}
	if skills != 1 {
		t.Fatalf("expected reuse without installing another skill, got %d skills", skills)
	}
	var mapping orm.ExternalAgentSkillSource
	if err := db.Where("owner_user_id=? AND source_type=? AND source_key=?", "user-1", "url", resolved.SourceKey).First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if mapping.ResolvedSkillID != "skillhub-installed" || mapping.InstallStatus != "reused" {
		t.Fatalf("mapping not backfilled: %#v", mapping)
	}
}

func TestInputBindingsFromExternalRequestImportsInputFiles(t *testing.T) {
	db := newHandlerTestDB(t)
	existing, _, err := workflowstore.New(db.DB).ImportInputResource(context.Background(), "user-1", "existing.txt", "text/plain", "sha256:existing", []byte("existing"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("hello from external agent")
	files := []any{map[string]any{
		"material_id":    "brief",
		"name":           "brief.txt",
		"mime_type":      "text/plain",
		"content_base64": base64.StdEncoding.EncodeToString(content),
	}}
	bindings, err := inputBindingsFromExternalRequest(
		context.Background(), db.DB, "user-1",
		map[string]any{"existing": map[string]any{"resource_id": existing.ID}},
		files,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings len=%d want 2: %#v", len(bindings), bindings)
	}
	var resource orm.WorkflowInputResource
	if err := db.Where("owner_user_id=? AND name=?", "user-1", "brief.txt").First(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if string(resource.Content) != string(content) || resource.ContentHash == "" {
		t.Fatalf("resource not imported correctly: %#v", resource)
	}
	if bindings[1].MaterialID != "brief" || bindings[1].ResourceID != resource.ID ||
		bindings[1].ContentHash != resource.ContentHash {
		t.Fatalf("file binding mismatch: %#v resource=%#v", bindings[1], resource)
	}
}

func TestExternalTaskResponseIncludesDisplayStatus(t *testing.T) {
	response := externalTaskResponse(orm.ExternalAgentWorkflowTask{
		ID: "task-1", AgentType: "codex", Status: externalTaskStatusConverting,
		ResultSummaryJSON: json.RawMessage(`{}`), ResultArtifactsJSON: json.RawMessage(`[]`),
	})
	if response["display_status"] != "running" {
		t.Fatalf("display_status=%v", response["display_status"])
	}
}

func seedExternalAgentSkillSource(t *testing.T, db *orm.DB, userID, skillID, name, sourceURL string) {
	t.Helper()
	sourceKey := "sha256:" + sha256Hex([]byte(canonicalExternalSkillURL(sourceURL)))
	now := time.Now().UTC()
	if err := db.Create(&orm.ExternalAgentSkillSource{
		ID: uuid.NewString(), OwnerUserID: userID, SourceType: "url", SourceKey: sourceKey,
		SourceName: name, SourceURL: sourceURL, ResolvedSkillID: skillID, InstallStatus: "reused",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func skillZipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExternalTaskStrictValidationBeforeEnqueue(t *testing.T) {
	db := newHandlerTestDB(t)
	valid := `{"agent_type":"codex","skill":{"name":"demo","url":"https://example.com/demo.zip"},"task_description":"Run"}`
	for _, body := range []string{
		valid + `{}`, "null", strings.Replace(valid, `"task_description":"Run"`, `"task_description":"Run","config":{"ignored":true}`, 1),
		strings.Replace(valid, `"task_description":"Run"`, `"task_description":"Run","input_bindings":{"brief":"wrong"}`, 1),
		strings.Replace(valid, `"task_description":"Run"`, `"task_description":"Run","input_files":[{"material_id":"brief","name":"a","mime_type":"text/plain","content_base64":"invalid"}]`, 1),
	} {
		req := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", strings.NewReader(body))
		req.Header.Set("X-User-Id", "user-1")
		rec := httptest.NewRecorder()
		CreateExternalAgentWorkflowTask(rec, req)
		if rec.Code < 400 {
			t.Fatalf("accepted invalid request %s: %s", body, rec.Body.String())
		}
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 0 {
		t.Fatalf("invalid input queued %d jobs", count)
	}
}

func TestExternalTaskIdempotencyRejectsChangedRequest(t *testing.T) {
	newHandlerTestDB(t)
	for index, description := range []string{"First task", "Different task"} {
		body := map[string]any{"agent_type": "codex", "skill": map[string]any{"name": "demo", "url": "https://example.com/demo.zip"}, "task_description": description, "idempotency_key": "same-key"}
		req := httptest.NewRequest(http.MethodPost, "/external-agent/workflow-tasks", bytes.NewReader(mustJSON(body)))
		req.Header.Set("X-User-Id", "user-1")
		rec := httptest.NewRecorder()
		CreateExternalAgentWorkflowTask(rec, req)
		if index == 0 && rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		if index == 1 && (rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_CONFLICT")) {
			t.Fatalf("changed request should conflict: %s", rec.Body.String())
		}
	}
}

func TestExternalSkillSameNameDifferentSourceDoesNotReuse(t *testing.T) {
	db := newHandlerTestDB(t)
	t.Setenv("LAZYMIND_SKILL_OBJECT_ROOT", t.TempDir())
	var original string
	for i, description := range []string{"Original", "Different content"} {
		archive := skillZipBytes(t, map[string][]byte{"SKILL.md": []byte("---\nname: same-name\ndescription: " + description + "\n---\n# Skill\n")})
		input := externalAgentWorkflowSkillInput{Name: "same-name", ZipBase64: base64.StdEncoding.EncodeToString(archive)}
		resolved, err := resolveExternalAgentSkillSource(context.Background(), db.DB, "user-1", "user-1", input)
		if i == 0 {
			if err != nil {
				t.Fatal(err)
			}
			original = resolved.SkillID
			reused, err := resolveExternalAgentSkillSource(context.Background(), db.DB, "user-1", "user-1", input)
			if err != nil || reused.SkillID != original || reused.InstallStatus != "reused" {
				t.Fatalf("exact source should reuse: %#v %v", reused, err)
			}
		} else if err == nil || skillSourceErrorCode(err) != "SKILL_NAME_CONFLICT" {
			t.Fatalf("different source silently reused: %#v %v", resolved, err)
		}
	}
}

func TestExternalTaskReadIsolationAndOutcome(t *testing.T) {
	db := newHandlerTestDB(t)
	task := orm.ExternalAgentWorkflowTask{ID: uuid.NewString(), OwnerUserID: "owner", IdempotencyKey: "key", Status: externalTaskStatusWaitingUserAction, Stage: "execute", ErrorCode: "USER_ACTION_REQUIRED"}
	task.RequestJSON, task.ResultSummaryJSON, task.ResultArtifactsJSON = mustJSON(map[string]any{}), mustJSON(map[string]any{}), mustJSON([]any{})
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"other", "owner"} {
		req := httptest.NewRequest(http.MethodGet, "/external-agent/workflow-tasks/task/result", nil)
		req.Header.Set("X-User-Id", user)
		req = mux.SetURLVars(req, map[string]string{"task_id": task.ID})
		rec := httptest.NewRecorder()
		GetExternalAgentWorkflowTaskResult(rec, req)
		if user == "other" && rec.Code != http.StatusNotFound {
			t.Fatal(rec.Body.String())
		}
		if user == "owner" && (rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"next_action":"open_lazymind"`)) {
			t.Fatal(rec.Body.String())
		}
	}
}

func TestExternalTaskConversationsAreOwnedAndTaskScoped(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, owner := range []string{"user-1", "user-1", "user-2"} {
		task := orm.ExternalAgentWorkflowTask{ID: uuid.NewString(), OwnerUserID: owner, AgentType: "codex", ExternalThreadID: "same-thread", IdempotencyKey: uuid.NewString()}
		task.RequestJSON, task.ResultSummaryJSON, task.ResultArtifactsJSON = mustJSON(map[string]any{}), mustJSON(map[string]any{}), mustJSON([]any{})
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		id, err := ensureExternalWorkflowConversation(context.Background(), db.DB, task)
		if err != nil || seen[id] {
			t.Fatalf("conversation collision %q: %v", id, err)
		}
		seen[id] = true
		repeat, err := ensureExternalWorkflowConversation(context.Background(), db.DB, task)
		if err != nil || repeat != id {
			t.Fatalf("conversation retry not idempotent: %q %v", repeat, err)
		}
		var conv orm.Conversation
		if err := db.First(&conv, "id=?", id).Error; err != nil {
			t.Fatal(err)
		}
		if conv.CreateUserID != owner || conv.ChatExecutor != "lazymind" || conv.SourceType != "external_agent_workflow" {
			t.Fatalf("invalid source ownership: %#v", conv)
		}
	}
}

func TestExternalTaskResponseActionContract(t *testing.T) {
	for _, test := range []struct {
		status, action string
		done           bool
	}{
		{externalTaskStatusQueued, "poll", false}, {externalTaskStatusRunning, "poll", false},
		{externalTaskStatusSucceeded, "read_result", true}, {externalTaskStatusFailed, "review_error", true},
		{externalTaskStatusWaitingUserAction, "open_lazymind", false},
	} {
		response := externalTaskResponse(orm.ExternalAgentWorkflowTask{Status: test.status})
		if response["next_action"] != test.action || response["done"] != test.done {
			t.Fatalf("unexpected response: %#v", response)
		}
		if test.action != "poll" && response["poll_after_seconds"] != 0 {
			t.Fatalf("unnecessary polling: %#v", response)
		}
		if response["lazymind_url"] == "" {
			t.Fatal("missing handoff URL")
		}
	}
}

func TestExternalTaskHostedSessionAndResults(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	seedWorkflowModelSelection(t, db, "owner")
	resource := orm.WorkflowResource{ID: "resource", WorkflowRef: "user:owner:report", WorkflowID: "report", OwnerUserID: "owner", Status: "active", HeadRevisionID: "revision"}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	revision := orm.WorkflowRevision{ID: "revision", WorkflowResourceID: resource.ID, RevisionNo: 1, TreeHash: "tree", CompiledGraph: mustJSON(map[string]any{})}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.ExternalAgentWorkflowTask{ID: uuid.NewString(), OwnerUserID: "owner", AgentType: "codex", IdempotencyKey: "run", TaskDescription: "Make report", WorkflowRef: resource.WorkflowRef, WorkflowRevisionID: revision.ID,
		RequestJSON: mustJSON(map[string]any{}), ResultSummaryJSON: mustJSON(map[string]any{}), ResultArtifactsJSON: mustJSON([]any{})}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	task = ensureExternalWorkflowTaskSession(context.Background(), db.DB, task)
	if task.SessionID == "" || task.Status != externalTaskStatusRunning {
		t.Fatalf("prepare failed: %#v", task)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id=?", task.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.OriginHost != "external-agent" || session.ControllerHost != "lazymind" {
		t.Fatalf("hosted execution must be owned by LazyMind: %#v", session)
	}
	if session.TriggerHistoryID == "" {
		t.Fatalf("hosted external workflow session must be attached to chat history: %#v", session)
	}
	var history orm.ChatHistory
	if err := db.First(&history, "id=?", session.TriggerHistoryID).Error; err != nil {
		t.Fatal(err)
	}
	if history.ConversationID != task.ConversationID || history.RawContent != "Make report" || history.RunStatus != "generating" {
		t.Fatalf("wrong chat anchor: %#v", history)
	}
	if !strings.Contains(history.Result, "<think>") || !strings.Contains(history.Result, "LazyMind") {
		t.Fatalf("chat anchor must expose workflow progress as reasoning content: %#v", history.Result)
	}
	for _, validity := range []string{"effective", "deleted", "stale"} {
		artifact := orm.WorkflowSlotRevision{ID: uuid.NewString(), SessionID: task.SessionID, SlotID: validity, Slot: validity, Revision: 1, Selected: true, Validity: validity, ContentSnapshot: mustJSON(map[string]any{"text": "Report content"})}
		if err := db.Create(&artifact).Error; err != nil {
			t.Fatal(err)
		}
	}
	task = completeExternalTask(context.Background(), db.DB, task)
	if task.Status != externalTaskStatusSucceeded {
		t.Fatalf("result collection failed: %#v", task)
	}
	artifacts := decodeJSONList(task.ResultArtifactsJSON)
	if len(artifacts) != 1 || !strings.Contains(string(task.ResultArtifactsJSON), "Report content") {
		t.Fatalf("wrong results: %s", task.ResultArtifactsJSON)
	}
	if err := db.First(&history, "id=?", session.TriggerHistoryID).Error; err != nil {
		t.Fatal(err)
	}
	if history.RunStatus != "completed" || !strings.Contains(history.Result, "<think>") || !strings.Contains(history.Result, "Workflow 已完成") || !strings.Contains(history.Result, "产物数量：1") {
		t.Fatalf("workflow result was not projected to chat history: %#v", history)
	}
	if err := db.Model(&task).Updates(map[string]any{"status": externalTaskStatusWaitingUserAction, "error_code": "USER_ACTION_REQUIRED"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&session).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id=?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != externalTaskStatusSucceeded || task.ErrorCode != "" {
		t.Fatalf("handoff completion was not observed: %#v", task)
	}
}

func TestExternalTaskGenerationReceivesOnlyInputMetadata(t *testing.T) {
	task := orm.ExternalAgentWorkflowTask{TaskDescription: "Make report", RequestJSON: mustJSON(map[string]any{
		"input_files": []any{map[string]any{"material_id": "brief", "name": "brief.txt", "mime_type": "text/plain", "content_base64": "private-file-bytes"}},
	})}
	text := externalTaskGenerationDescription(task)
	if !strings.Contains(text, "{{user_input}}") || strings.Contains(text, "Make report") {
		t.Fatalf("generation context must stay task-agnostic and runtime-input driven: %s", text)
	}
	if !strings.Contains(text, `"material_id":"brief"`) || strings.Contains(text, "private-file-bytes") {
		t.Fatalf("wrong generation context: %s", text)
	}
}

func TestLatestLinkedWorkflowSkipsTaskSpecificLegacyWorkflow(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	snapshot := workflowSourceSkillSnapshot{SkillID: "skill-1", TreeHash: "tree-1"}
	legacy := orm.WorkflowResource{ID: "legacy-resource", WorkflowRef: "user:owner:legacy", WorkflowID: "legacy", OwnerUserID: "owner", SourceType: "skill",
		SourceSkillID: snapshot.SkillID, SourceSkillTreeHash: snapshot.TreeHash, Status: "active", HeadRevisionID: "legacy-revision", RelativeRoot: "workflows/owner/legacy", UpdatedAt: now}
	generic := orm.WorkflowResource{ID: "generic-resource", WorkflowRef: "user:owner:generic", WorkflowID: "generic", OwnerUserID: "owner", SourceType: "skill",
		SourceSkillID: snapshot.SkillID, SourceSkillTreeHash: snapshot.TreeHash, Status: "active", HeadRevisionID: "generic-revision", RelativeRoot: "workflows/owner/generic", UpdatedAt: now.Add(-time.Minute)}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&generic).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "legacy-revision", WorkflowResourceID: legacy.ID, RevisionNo: 1, TreeHash: "legacy-tree",
		CompiledGraph: mustJSON(map[string]any{"nodes": map[string]any{"search": map[string]any{"prompt": "搜索写代码相关 Skill"}}})}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "generic-revision", WorkflowResourceID: generic.ID, RevisionNo: 1, TreeHash: "generic-tree",
		CompiledGraph: mustJSON(map[string]any{"nodes": map[string]any{"search": map[string]any{"prompt": "根据 {{user_input}} 搜索 SkillHub 并推荐技能"}}})}).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.ExternalAgentWorkflowTask{TaskDescription: "查找 PDF 文本提取、文档摘要、表格提取相关 Skill", RequestJSON: mustJSON(map[string]any{})}
	reused, ok := latestLinkedWorkflowForSkill(context.Background(), db.DB, "owner", snapshot, task)
	if !ok || reused.WorkflowRef != generic.WorkflowRef {
		t.Fatalf("expected reusable runtime-input workflow, got ok=%v workflow=%#v", ok, reused)
	}
}

func TestExternalTaskRecoveryRestoresMissingJobAndReportsFailure(t *testing.T) {
	db := newHandlerTestDB(t)
	task := orm.ExternalAgentWorkflowTask{ID: uuid.NewString(), OwnerUserID: "owner", AgentType: "codex", IdempotencyKey: "recover", Status: externalTaskStatusQueued,
		RequestJSON: mustJSON(map[string]any{}), ResultSummaryJSON: mustJSON(map[string]any{}), ResultArtifactsJSON: mustJSON([]any{})}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Where("resource_id=?", task.ID).Count(&count)
	if count != 1 {
		t.Fatalf("recovery created %d jobs", count)
	}
	if err := db.Model(&orm.AsyncJob{}).Where("resource_id=?", task.ID).Update("status", string(asyncjob.StatusFailed)).Error; err != nil {
		t.Fatal(err)
	}
	if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id=?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != externalTaskStatusFailed || task.ErrorCode != "TASK_WORKER_FAILED" {
		t.Fatalf("worker failure was hidden: %#v", task)
	}
}
