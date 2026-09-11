package conversationgroup

import (
	"encoding/json"
	"testing"
)

func TestDirectoryIDsPersistCreateMergeAndRejectUnknown(t *testing.T) {
	cards := map[string]directoryCard{
		"formal-uuid": {ID: "formal-uuid", Kind: "existing", Name: "正式组"},
	}
	cp := incrementalCheckpoint{}
	if !ensureGroupIDs(&cp, cards) || cp.GroupIDs["formal-uuid"] != "g1" {
		t.Fatal(cp)
	}
	out := organizerStepOutput{Operations: []candidateOperation{
		{Op: "create", ID: "new_1", Name: "邮件", Scope: "收发邮件"},
		{Op: "create", ID: "new_2", Name: "发信", Scope: "发送邮件"},
		{Op: "merge", SourceIDs: []string{"new_1", "new_2"}, TargetID: "new_1", Name: "邮件处理", Scope: "收发邮件"},
	}, Assignments: []incrementalAssignment{{ID: "c1", GroupID: "new_2"}, {ID: "c2", GroupID: "g1"}}}
	ids, next, err := decodeGroupIDs(&out, cp, cards)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.GroupIDs) != 1 {
		t.Fatal("tentative mapping changed committed checkpoint")
	}
	for _, op := range out.Operations {
		if _, err = applyCandidateOperation(cards, op); err != nil {
			t.Fatal(err)
		}
	}
	cp.GroupIDs, cp.NextGroupNumber = ids, next
	raw, _ := json.Marshal(cp)
	var restored incrementalCheckpoint
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if ensureGroupIDs(&restored, cards) || restored.NextGroupNumber != 3 {
		t.Fatal("mapping changed on recovery")
	}
	directory := mappedDirectory(cards, restored)
	if len(directory) != 2 {
		t.Fatalf("merged source leaked: %+v", directory)
	}
	followup := organizerStepOutput{Assignments: []incrementalAssignment{{ID: "c3", GroupID: "g3"}}}
	if _, _, err = decodeGroupIDs(&followup, restored, cards); err != nil {
		t.Fatal(err)
	}
	target, _ := directoryTarget(cards, out.Assignments[0].GroupID)
	if followup.Assignments[0].GroupID != target || out.Assignments[1].GroupID != "formal-uuid" {
		t.Fatal("alias or formal mapping mismatch")
	}
	invalid := organizerStepOutput{Assignments: []incrementalAssignment{{ID: "c4", GroupID: "g999"}}}
	if _, _, err = decodeGroupIDs(&invalid, restored, cards); err == nil {
		t.Fatal("accepted unknown reference")
	}
	nextOut := organizerStepOutput{Operations: []candidateOperation{{Op: "create", ID: "new_1", Name: "新场景", Scope: "新场景"}}}
	more, n, err := decodeGroupIDs(&nextOut, restored, cards)
	if err != nil || n != 4 || more[nextOut.Operations[0].ID] != "g4" {
		t.Fatal("reused retired number", err)
	}
}
