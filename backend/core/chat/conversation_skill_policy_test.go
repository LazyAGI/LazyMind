package chat

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	skillservice "lazymind/core/skillv2/service"
	skilltestutil "lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestChatFeishuQueryDoesNotInstallSkill(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	// Chat must work even when the optional builtin catalog is not materialized.
	t.Chdir(t.TempDir())
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/chat/stream" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			return
		}
		called = true
		var payload LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(payload.Conversation.RunID, true)}})
	}))
	defer server.Close()
	for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(name, server.URL)
	}
	r := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"optional-skill-chat","query":"飞书是什么","stream":false}`, nil)
	w := httptest.NewRecorder()
	ChatConversations(w, r)
	if w.Code != http.StatusOK || !called || !strings.Contains(w.Body.String(), "answer") {
		t.Fatalf("Chat failed without builtin Skill: status=%d called=%v body=%s", w.Code, called, w.Body.String())
	}
	var count int64
	if err := db.Model(&orm.SkillV2Skill{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("Chat must not install Skills: count=%d err=%v", count, err)
	}
}

func TestChatResolvesExplicitBareSkillNameBeforeCallingUpstream(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Chdir(t.TempDir())
	fixtureDB := &skilltestutil.TestDB{DB: db.DB}
	skilltestutil.SeedSkillWithRevision(t, fixtureDB, "skill-explicit", "rev-explicit")
	if err := db.Model(&orm.SkillV2Skill{}).Where("id = ?", "skill-explicit").Updates(map[string]any{
		"category":      "external",
		"skill_name":    "requested-skill",
		"relative_root": "external/requested-skill",
	}).Error; err != nil {
		t.Fatalf("update skill identity: %v", err)
	}

	var upstreamRequest LazyChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/chat/stream" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(upstreamRequest.Conversation.RunID, true)}})
	}))
	defer server.Close()
	for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(name, server.URL)
	}

	r := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user_001", `{
		"conversation_id":"explicit-bare-skill-chat",
		"query":"use the requested skill",
		"stream":false,
		"explicit_resource_bindings":{"skill_names":["requested-skill"]}
	}`, nil)
	w := httptest.NewRecorder()
	ChatConversations(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("Chat status=%d body=%s", w.Code, w.Body.String())
	}
	got := upstreamRequest.ExplicitResources.SkillNames
	if len(got) != 1 || got[0] != "external/requested-skill" {
		t.Fatalf("upstream explicit skill names = %#v, want canonical name", got)
	}
}

func TestChatResolvesImportedManifestAliasBeforeCallingUpstream(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Chdir(t.TempDir())

	const downloadURL = "https://example.test/manifest-alias.zip"
	zipPath := writeChatSkillPackageZip(t, `---
name: canonical-skill
description: Canonical package description.
---
# Canonical Skill
`)
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:         db.DB,
		Downloader: skillservice.NewFakeZipDownloader(map[string]string{downloadURL: zipPath}),
		BlobStore:  skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(t.TempDir())),
	})
	created, err := service.CreateSkill(context.Background(), skillservice.CreateSkillRequest{
		OwnerUserID:  "user_001",
		CreateUserID: "user_001",
		Name:         "Manifest Skill",
		Description:  "Manifest description",
		Source:       skillservice.SourceInput{Type: "url", URL: downloadURL},
	})
	if err != nil {
		t.Fatalf("CreateSkill returned error: %v", err)
	}
	if created.CanonicalRuntimeName != "external/canonical-skill" || len(created.Aliases) != 1 || created.Aliases[0] != "Manifest Skill" {
		t.Fatalf("CreateSkill response = %#v, want canonical name plus manifest alias", created)
	}

	var upstreamRequest LazyChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/chat/stream" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(upstreamRequest.Conversation.RunID, true)}})
	}))
	defer server.Close()
	for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(name, server.URL)
	}

	for index, requestedName := range []string{
		"Manifest Skill",
		"  manifest_skill  ",
		"CANONICAL_SKILL",
		" External / Canonical_Skill ",
	} {
		upstreamRequest = LazyChatRequest{}
		requestBody, err := json.Marshal(map[string]any{
			"conversation_id": fmt.Sprintf("explicit-manifest-alias-chat-%d", index),
			"query":           "use the imported skill",
			"stream":          false,
			"explicit_resource_bindings": map[string]any{
				"skill_names": []string{requestedName},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		r := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user_001", string(requestBody), nil)
		w := httptest.NewRecorder()
		ChatConversations(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("Chat(%q) status=%d body=%s", requestedName, w.Code, w.Body.String())
		}
		got := upstreamRequest.ExplicitResources.SkillNames
		if len(got) != 1 || got[0] != "external/canonical-skill" {
			t.Fatalf("upstream explicit skill names for %q = %#v, want canonical name", requestedName, got)
		}
	}
}

func TestChatReturnsStructuredSkillBindingNotFound(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Chdir(t.TempDir())

	upstreamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(name, server.URL)
	}

	r := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user_001", `{
		"conversation_id":"missing-skill-binding-chat",
		"query":"use the missing skill",
		"stream":false,
		"explicit_resource_bindings":{"skill_names":["missing_skill"]}
	}`, nil)
	w := httptest.NewRecorder()
	ChatConversations(w, r)

	if upstreamCalled {
		t.Fatal("chat upstream was called for an unresolved skill binding")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Chat status=%d body=%s", w.Code, w.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data, dataOK := response.Data.(map[string]any)
	detail, detailOK := data["detail"].(map[string]any)
	if response.Code != 2003100 || !dataOK || !detailOK || detail["reason"] != "skill_binding_not_found" || detail["requested_name"] != "missing_skill" {
		t.Fatalf("response = %#v, want structured skill_binding_not_found", response)
	}
}

func writeChatSkillPackageZip(t *testing.T, skillMD string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "skill.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("SKILL.md")
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(skillMD)); err != nil {
		_ = writer.Close()
		_ = file.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}
