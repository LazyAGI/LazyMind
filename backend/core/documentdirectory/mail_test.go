package documentdirectory

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestMailLocatorOwnershipAndIdentity(t *testing.T) {
	db := testDB(t)
	d := create(t, db, "mail")
	e := orm.DocumentDirectoryEntry{ID: "email", DirectoryID: d.ID, ResourceKey: "email", Source: "mail", Namespace: "a@example.com", ResourceID: "<one@example.com>", LocatorJSON: encoded(map[string]string{"mailbox": "a@example.com", "folder": "INBOX", "id": "INBOX::1", "thread_id": "<one@example.com>"})}
	must(t, db.Create(&e).Error)
	locator, err := mailLocator(context.Background(), db, "u", d.ID, e.ID)
	must(t, err)
	if locator["message_id"] != e.ResourceID || locator["message_ref"] != "INBOX::1" {
		t.Fatal(locator)
	}
	for _, pair := range [][2]string{{"other-user", d.ID}, {"u", "other-directory"}} {
		if _, err := mailLocator(context.Background(), db, pair[0], pair[1], e.ID); err == nil {
			t.Fatal("accepted unowned mail")
		}
	}
	must(t, db.Model(&e).Update("excluded", true).Error)
	if _, err := mailLocator(context.Background(), db, "u", d.ID, e.ID); err == nil {
		t.Fatal("accepted excluded mail")
	}
	must(t, db.Model(&e).Updates(map[string]any{"excluded": false, "locator_json": encoded(map[string]string{"mailbox": "other@example.com", "id": "INBOX::1"})}).Error)
	if _, err := mailLocator(context.Background(), db, "u", d.ID, e.ID); err == nil {
		t.Fatal("accepted mismatched mailbox")
	}
}

func TestSelectMailCredentialDoesNotFallBack(t *testing.T) {
	items := []string{`{"email":"b@example.com","secret":"b","status":"ACTIVE"}`, `{"email":"a@example.com","secret":"a","status":"ACTIVE"}`}
	c, err := selectMailCredential(items, "a@example.com")
	must(t, err)
	if c["secret"] != "a" {
		t.Fatal("selected another account")
	}
	if _, err := selectMailCredential(items, "missing@example.com"); err == nil {
		t.Fatal("fell back to another account")
	}
	if _, err := selectMailCredential([]string{`{"email":"a@example.com","secret":"a","status":"EXPIRED"}`}, "a@example.com"); err == nil {
		t.Fatal("accepted expired account")
	}
}
