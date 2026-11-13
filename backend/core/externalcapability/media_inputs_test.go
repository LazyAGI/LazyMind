package externalcapability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
)

func TestDeterministicExternalToolsNeedNoModelConfiguration(t *testing.T) {
	db := testDB(t)
	// A tool-only deployment need not have any model selections at all. This
	// deliberately removes that table to detect accidental model dependencies.
	if err := db.Migrator().DropTable(&orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if config, _ := payload["llm_config"].(map[string]any); len(config) != 0 {
			t.Error("model configuration leaked into a deterministic call")
		}
		if strings.HasSuffix(r.URL.Path, "external-catalog") {
			_, _ = w.Write([]byte(`{"items":[{"name":"url_fetch","available":true,"input_schema":{"type":"object"}}]}`))
		} else {
			_, _ = w.Write([]byte(`{"result":{"text":"source content"}}`))
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	service := New(db, server.Client())
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex"}
	listed, err := service.ListExternalTools(t.Context(), call)
	if err != nil || len(listed.Items) != 1 {
		t.Fatalf("catalog=%+v err=%v", listed, err)
	}
	if _, err := service.InvokeExternalTool(t.Context(), call, capability.InvokeExternalToolInput{ToolID: "builtin:url_fetch", Arguments: map[string]any{"url": "https://example.com"}}); err != nil {
		t.Fatal(err)
	}
	if value, err := modelconfig.LoadOwnedToolModels(t.Context(), nil, "user-1", nil); err != nil || len(value) != 0 {
		t.Fatalf("empty roles unexpectedly require a database: %v", err)
	}
}

func TestMediaInputResourcesAreOwnedImmutableImages(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&orm.WorkflowInputResource{}); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	content := buffer.Bytes()
	resource := orm.WorkflowInputResource{ID: "image-1", OwnerUserID: "user-1", Name: "image.png", MimeType: "image/png", Content: content, Size: int64(len(content)), ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(content)), Revision: 1}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, nil)
	input := map[string]any{"prompt": "animate", "first_frame_url": "resource:image-1"}
	result, err := service.resolveMediaInputs(context.Background(), "user-1", "video_generator", input)
	if err != nil || !strings.HasPrefix(fmt.Sprint(result["first_frame_url"]), "data:image/png;base64,") {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if input["first_frame_url"] != "resource:image-1" {
		t.Fatal("caller arguments were mutated")
	}
	vision, err := service.resolveMediaInputs(t.Context(), "user-1", "multimodal", map[string]any{
		"url": "resource:image-1", "instruction": "Describe the image",
	})
	if err != nil || vision["url"] != result["first_frame_url"] || vision["instruction"] != "Describe the image" {
		t.Fatalf("vision input=%v err=%v", vision, err)
	}
	for _, value := range []any{"/etc/passwd", "https://example.com/image.png", "data:image/png;base64,ignored", "resource:missing", map[string]any{"path": "/etc/passwd"}} {
		if _, err := service.resolveMediaInputs(t.Context(), "user-1", "image_editor", map[string]any{"urls": []any{value}}); err == nil {
			t.Fatalf("unsafe input accepted: %v", value)
		}
		if _, err := service.resolveMediaInputs(t.Context(), "user-1", "multimodal", map[string]any{"url": value}); err == nil {
			t.Fatalf("unsafe vision input accepted: %v", value)
		}
	}
	if _, err := service.resolveMediaInputs(t.Context(), "user-2", "image_editor", map[string]any{"urls": []string{"resource:image-1"}}); err == nil {
		t.Fatal("another user's resource was accepted")
	}
	if _, err := service.resolveMediaInputs(t.Context(), "user-2", "multimodal", map[string]any{"url": "resource:image-1"}); err == nil {
		t.Fatal("another user's vision input was accepted")
	}
	if _, err := service.resolveMediaInputs(t.Context(), "user-1", "video_generator", map[string]any{"prompt": "text only"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&resource).Update("content_hash", "sha256:changed").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveMediaInputs(t.Context(), "user-1", "image_editor", map[string]any{"urls": []string{"resource:image-1"}}); err == nil {
		t.Fatal("changed resource accepted")
	}
}

func TestExternalVisionCatalogLoadsOnlyTheSelectedVLMWhenAvailable(t *testing.T) {
	db := testDB(t)
	seedModel(t, db, "https://model.example/v1", "fixture-key")
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-1").Updates(map[string]any{
		"name": "OpenAI", "is_custom": true, "protocol_name": "OpenAI",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "model-1").Updates(map[string]any{
		"provider_name": "OpenAI", "vision": true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, role := range []string{"llm", "vlm"} {
		if err := db.Create(&orm.UserSelectedModel{
			UserID: "user-1", ModelKey: role, UserModelProviderGroupModelID: "model-1",
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ToolName string         `json:"tool_name"`
			Config   map[string]any `json:"llm_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		if request.ToolName == "multimodal" && (len(request.Config) != 1 || request.Config["vlm"] == nil) {
			t.Errorf("vision catalog selected %d roles (VLM=%t, main=%t)", len(request.Config), request.Config["vlm"] != nil, request.Config["llm"] != nil)
		}
		_, _ = w.Write([]byte(`{"items":[{"name":"multimodal","available":true,"input_schema":{"type":"object"}}]}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "deepseek-harness"}
	listed, err := New(db, server.Client()).ListExternalTools(t.Context(), call)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != "builtin:multimodal" {
		t.Fatalf("vision tool not listed: %+v %v", listed, err)
	}
}

func TestExternalVideoResultUsesPublicURLAndRetainsSignedQuery(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://lazy.example/api/core")
	result := externalImageResult([]byte(`{"video_url":"/static-files/ai_generated/movie.mp4?sig=valid","video_markdown":"![video](/static-files/ai_generated/movie.mp4?sig=valid)","image_url":"https://unrelated.example/image.png"}`))
	if strings.Count(string(result), "https://lazy.example/api/core/static-files/ai_generated/movie.mp4?sig=valid") != 2 || !strings.Contains(string(result), "https://unrelated.example/image.png") {
		t.Fatalf("invalid video result: %s", result)
	}
}
