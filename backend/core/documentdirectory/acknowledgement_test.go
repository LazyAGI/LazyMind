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

func TestChildAcknowledgementDoesNotWaitForParentLeaseFinalization(t *testing.T) {
	db := testDB(t)
	d := create(t, db, "parent-lease")
	parent, _ := batch(t, db, d, Input{})
	must(t, db.Model(&parent).Update("status", "succeeded").Error)
	child := orm.DocumentDirectoryBatch{ID: "visible-child", UserID: "u", OwnerKey: "u", TriggerID: "visible-child", ParentID: parent.ID, Status: "succeeded", InputJSON: encoded(Input{})}
	must(t, db.Create(&child).Error)
	command(t, db, Command{Operation: "acknowledge", BatchID: child.ID})
	must(t, db.Where("id=?", parent.ID).Take(&parent).Error)
	must(t, db.Where("id=?", child.ID).Take(&child).Error)
	if !child.Acknowledged || !parent.Acknowledged {
		t.Fatal("parent lease finalization prevented result acknowledgement")
	}
}
