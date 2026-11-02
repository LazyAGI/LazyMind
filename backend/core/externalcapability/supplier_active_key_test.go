package externalcapability

import (
	"context"
	"crypto/sha256"

	"fmt"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/capability"
)

func TestExternalModelUsesOnlySupplierEnabledKeyAcrossConnections(t *testing.T) {
	headers := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	db := testDB(t)
	seedModel(t, db, server.URL+"/v1", "first-key\nsecond-key")
	other := orm.UserModelProviderGroup{ID: "group-2", UserModelProviderID: "provider-1", Name: "Other", BaseURL: "https://unused.invalid/v1", APIKey: "enabled-key\ninactive-key", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "user-1"}}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	activeID := fmt.Sprintf("%x", sha256.Sum256([]byte("group-2\x00enabled-key")))
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-1").Updates(map[string]any{"active_key_initialized": true, "active_key_group_id": "group-2", "active_key_id": activeID}).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, server.Client())
	for i, expected := range []string{"Bearer enabled-key", "Bearer enabled-key", "Bearer enabled-key"} {
		call := capability.InvocationContext{
			Principal:     capability.Principal{UserID: "user-1", Permissions: capability.NewPermissionSet(capability.RequiredPermission)},
			ExternalAgent: "codex", InvocationID: fmt.Sprintf("single-key-%d", i),
		}
		_, err := service.InvokeExternalModel(context.Background(), call, capability.InvokeExternalModelInput{
			ModelID: "model-1", Messages: []capability.ExternalModelMessage{{Role: "user", Content: "test"}},
		})
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if got := <-headers; got != expected {
			t.Fatalf("request %d sent an unexpected authorization header", i)
		}
	}
	var saved orm.UserModelProviderGroup
	if err := db.First(&saved, "id = ?", "group-1").Error; err != nil || saved.APIKey != "first-key\nsecond-key" {
		t.Fatal("runtime changed the saved keys")
	}
	if err := db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-1").Updates(map[string]any{"active_key_group_id": "", "active_key_id": ""}).Error; err != nil {
		t.Fatal(err)
	}
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1", Permissions: capability.NewPermissionSet(capability.RequiredPermission)}, ExternalAgent: "codex", InvocationID: "disabled"}
	_, err := service.InvokeExternalModel(context.Background(), call, capability.InvokeExternalModelInput{ModelID: "model-1", Messages: []capability.ExternalModelMessage{{Role: "user", Content: "test"}}})
	if err == nil {
		t.Fatal("disabled supplier still allowed an external model request")
	}
	if len(headers) != 0 {
		t.Fatal("disabled supplier sent a request")
	}
}
