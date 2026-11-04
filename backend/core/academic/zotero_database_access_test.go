package academic

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestZoteroDatabaseOwnerAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, mode, bootstrap, mountedOwner, directory, username, role string
		allowed, authenticate                                          bool
	}{
		{name: "shared host cannot expose database", username: "admin", role: "system-admin"},
		{name: "directory alone does not expose database", directory: t.TempDir(), bootstrap: "admin", username: "admin", role: "system-admin"},
		{name: "native verified owner", mode: "local", bootstrap: "admin", username: "admin", role: "system-admin", allowed: true, authenticate: true},
		{name: "explicit container owner", mountedOwner: "admin", directory: t.TempDir(), username: "admin", role: "system-admin", allowed: true, authenticate: true},
		{name: "other admin cannot read owner database", mountedOwner: "admin", directory: t.TempDir(), username: "other", role: "system-admin", authenticate: true},
		{name: "demoted owner", mountedOwner: "admin", directory: t.TempDir(), username: "admin", role: "user", authenticate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", tc.mode)
			t.Setenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", tc.bootstrap)
			t.Setenv("LAZYMIND_ZOTERO_LOCAL_OWNER", tc.mountedOwner)
			t.Setenv("LAZYMIND_ZOTERO_DATA_DIR", tc.directory)
			calls := 0
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer owner-token" || r.Header.Get("X-User-Role") != "" {
					t.Fatal("local library identity must be independently verified")
				}
				fmt.Fprintf(w, `{"user_id":"owner-id","username":%q,"role":%q}`, tc.username, tc.role)
			}))
			defer auth.Close()
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
			r := httptest.NewRequest("POST", "/academic/zotero/preview", nil)
			r.Header.Set("Authorization", "Bearer owner-token")
			r.Header.Set("X-User-Id", "owner-id")
			r.Header.Set("X-User-Role", "system-admin")
			err := authorizeLocalZoteroSource(r, "database")
			if (err == nil) != tc.allowed || (calls == 1) != tc.authenticate {
				t.Fatalf("database authorization: allowed=%t calls=%d err=%v", err == nil, calls, err)
			}
		})
	}
}
