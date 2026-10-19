package documentdirectory

import (
	"lazymind/core/common/orm"
	"testing"
)

func TestAcknowledgeOnlyAfterEachChildResult(t *testing.T) {
	db := testDB(t)
	for _, id := range []string{"parent", "a", "b"} {
		parent := "parent"
		if id == "parent" {
			parent = ""
		}
		must(t, db.Create(&orm.DocumentDirectoryBatch{ID: id, UserID: "u", OwnerKey: "u", TriggerID: id, ParentID: parent, Status: "succeeded", InputJSON: encoded(Input{})}).Error)
	}
	command(t, db, Command{Operation: "acknowledge", BatchID: "a"})
	var parent orm.DocumentDirectoryBatch
	must(t, db.Where("id=?", "parent").Take(&parent).Error)
	if parent.Acknowledged {
		t.Fatal("unread child acknowledged")
	}
	command(t, db, Command{Operation: "acknowledge", BatchID: "b"})
	must(t, db.Where("id=?", "parent").Take(&parent).Error)
	if !parent.Acknowledged {
		t.Fatal("completed parent remains unread")
	}
}
