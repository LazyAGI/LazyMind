package academic

import (
	"encoding/json"
	"lazymind/core/acl"
	"lazymind/core/paperlibrary"
	"testing"
)

func TestPersonalReadingStatusPermissionsAndIsolation(t *testing.T) {
	paperLibraryTestDB(t)
	paper := importDraft(t, "papers", "seed", libraryDraft("a", "A", "10.1234/a")).Data.Papers[0]
	acl.GetStore().AddACL(acl.ResourceTypeDB, "papers", "user", "reader", acl.PermissionDatasetRead, "owner", nil)
	read := func(user string) paperlibrary.Paper {
		t.Helper()
		w := libraryRequest(t, "GET", "/library", "papers", user, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var view paperlibrary.View
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view.Data.Papers[0]
	}
	if read("reader").Status != "unread" || read("owner").Status != "unread" {
		t.Fatal("new users must start unread")
	}
	for _, status := range []string{"reading", "read", "unread"} {
		w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "reader", map[string]any{"status": status})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		current := read("reader")
		if current.Status != status || current.Revision != paper.Revision || read("owner").Status != "unread" {
			t.Fatal("personal update changed shared state", current)
		}
	}
	for _, body := range []map[string]any{
		{"revision": paper.Revision, "tags": []string{"forbidden"}},
		{"revision": paper.Revision, "metadata": paper.Metadata, "status": "read"},
		{"revision": paper.Revision, "primary_attachment_id": "missing"},
	} {
		w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "reader", body)
		if w.Code != 403 {
			t.Fatal("reader modified shared fields", w.Code, w.Body.String())
		}
	}
	if read("reader").Status != "unread" {
		t.Fatal("mixed forbidden patch partially saved")
	}
	for _, user := range []string{"reader", "owner"} {
		w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", user, map[string]any{"status": "invalid"})
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "stranger", map[string]any{"status": "read"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := libraryRequest(t, "PATCH", "/library/papers/a", "other", "owner", map[string]any{"status": "read"}); w.Code != 404 {
		t.Fatal(w.Code)
	}
	// A stale shared revision is irrelevant to a personal status-only update.
	w := libraryRequest(t, "PATCH", "/library/papers/a", "papers", "reader", map[string]any{"revision": 99, "status": "reading"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, important := range []bool{true, false, true} {
		w = libraryRequest(t, "PUT", "/papers/"+paper.AcademicWorkID+"/importance", "papers", "reader", map[string]any{"dataset_id": "papers", "is_important": important})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if current := read("reader"); current.Status != "reading" || current.Important != important {
			t.Fatal("importance overwrote status", current)
		}
	}
	w = libraryRequest(t, "PATCH", "/library/papers/a", "papers", "reader", map[string]any{"status": "read"})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if current := read("reader"); current.Status != "read" || !current.Important {
		t.Fatal("status overwrote importance", current)
	}
	if current := read("owner"); current.Status != "unread" || current.Important {
		t.Fatal("personal fields leaked", current)
	}
	// The same scholarly work in another collection has separate personal state.
	importDraft(t, "other", "other-seed", libraryDraft("b", "A", "10.1234/a"))
	if current := readLibrary(t, "other").Data.Papers[0]; current.Status != "unread" || current.Important {
		t.Fatal("state leaked across collections")
	}
}
