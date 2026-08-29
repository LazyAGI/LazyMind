package historyinjection

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestResolveImportedOwnerRequiresEveryBundleAndOneOwner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE conversations (
        id TEXT PRIMARY KEY, create_user_id TEXT NOT NULL, create_user_name TEXT NOT NULL
    )`).Error; err != nil {
		t.Fatal(err)
	}
	sources := []BundleSource{
		{Manifest: Manifest{ConversationID: "conversation-1"}},
		{Manifest: Manifest{ConversationID: "conversation-2"}},
	}
	if owner, imported, err := ResolveImportedOwner(t.Context(), db, sources); err != nil || imported || owner.ID != "" {
		t.Fatalf("empty database result = %#v, %t, %v", owner, imported, err)
	}
	if err := db.Exec("INSERT INTO conversations VALUES (?, ?, ?)", "conversation-1", "owner-1", "admin").Error; err != nil {
		t.Fatal(err)
	}
	if _, imported, err := ResolveImportedOwner(t.Context(), db, sources); err != nil || imported {
		t.Fatalf("partial import was considered complete: imported=%t err=%v", imported, err)
	}
	if err := db.Exec("INSERT INTO conversations VALUES (?, ?, ?)", "conversation-2", "owner-1", "admin").Error; err != nil {
		t.Fatal(err)
	}
	owner, imported, err := ResolveImportedOwner(t.Context(), db, sources)
	if err != nil || !imported || owner.ID != "owner-1" || owner.Username != "admin" {
		t.Fatalf("complete import result = %#v, %t, %v", owner, imported, err)
	}
	if err := db.Exec("UPDATE conversations SET create_user_id = 'owner-2' WHERE id = 'conversation-2'").Error; err != nil {
		t.Fatal(err)
	}
	if _, imported, err := ResolveImportedOwner(t.Context(), db, sources); err != nil || imported {
		t.Fatalf("mixed owners were considered complete: imported=%t err=%v", imported, err)
	}
}

func TestResolveOwnerSupportsAuthServiceEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/auth/login":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"code": 200, "message": "success", "data": map[string]string{"access_token": "test-token"},
			})
		case "/auth/me":
			if request.Header.Get("Authorization") != "Bearer test-token" {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"code": 200, "message": "success", "data": map[string]string{"user_id": "target-user", "username": "admin"},
			})
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	owner, err := resolveOwnerOnce(t.Context(), server.Client(), server.URL, "admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if owner.ID != "target-user" || owner.Username != "admin" {
		t.Fatalf("unexpected owner: %#v", owner)
	}
}
