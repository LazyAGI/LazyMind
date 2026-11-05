package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"lazymind/core/common/orm"
)

func TestPPTTextEditPreservesOtherPagesAndNotes(t *testing.T) {
	for _, tc := range []struct {
		name, workflow, source string
		missing                bool
		wantSelected           int
	}{
		{"text-edit", "ppt-workflow", "ppt_edit_page_html", false, 4},
		{"full-generation", "ppt-workflow", "ppt_publish_pages", false, 1},
		{"other-workflow", "other", "ppt_edit_page_html", false, 1},
		{"rollback", "ppt-workflow", "ppt_edit_page_html", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSlotRevision{},
				&orm.WorkflowHumanArtifact{}, &orm.WorkflowEvent{}, &orm.WorkflowRevision{},
				&orm.WorkflowSlotOrder{}, &orm.WorkflowSessionStep{})
			if err := db.Create(&orm.WorkflowSession{ID: "session", WorkflowID: tc.workflow}).Error; err != nil {
				t.Fatal(err)
			}
			sink := DBArtifactSink{DB: db}
			attempt := AttemptContext{AttemptID: "first", SessionID: "session", StepID: "generate_ppt", AttemptNo: 1,
				OutputCardinality: map[string]string{"preview_html": "list", "preview_notes": "list"}}
			for _, slot := range []string{"preview_html", "preview_notes"} {
				for index := 0; index < 3; index++ {
					value := json.RawMessage(fmt.Sprintf(`{"text":"original %s %d","list_index":%d}`, slot, index, index))
					if err := sink.Save(context.Background(), attempt, Artifact{Slot: slot, Seq: index + 1, ContentType: "text", Value: value}); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Rewind invalidates the old producer; index 2 was previously deleted.
			if err := db.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ?", "session").
				Updates(map[string]any{"selected": false, "validity": "stale"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSlotOrder{}).Where("session_id = ?", "session").
				Update("order_list", json.RawMessage(`[0,1]`)).Error; err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				var last orm.WorkflowSlotRevision
				db.Where("slot_id = ? AND list_index = ?", "preview_notes", 1).First(&last)
				if err := db.Delete(&orm.WorkflowHumanArtifact{}, "id = ?", *last.HumanArtifactID).Error; err != nil {
					t.Fatal(err)
				}
			}
			attempt.AttemptID, attempt.AttemptNo = "edit", 2
			edit := Artifact{Slot: "preview_html", ContentType: "text", Seq: 1,
				Value: json.RawMessage(fmt.Sprintf(`{"text":"new title","list_index":1,"_source_tool":%q}`, tc.source))}
			err := sink.Save(context.Background(), attempt, edit)
			if (err != nil) != tc.missing {
				t.Fatalf("publication error=%v", err)
			}
			var rows []orm.WorkflowSlotRevision
			if err := db.Where("selected = ?", true).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.wantSelected {
				t.Fatalf("selected=%d, want=%d", len(rows), tc.wantSelected)
			}
			for _, row := range rows {
				if row.ProducerAttemptID != "edit" || row.Validity != "effective" || *row.ListIndex > 1 {
					t.Fatalf("invalid carried revision: %+v", row)
				}
				var value orm.WorkflowHumanArtifact
				if err := db.First(&value, "id = ?", *row.HumanArtifactID).Error; err != nil {
					t.Fatal(err)
				}
				var content map[string]any
				_ = json.Unmarshal(value.Value, &content)
				want := fmt.Sprintf("original %s %d", row.SlotID, *row.ListIndex)
				if row.SlotID == "preview_html" && *row.ListIndex == 1 {
					want = "new title"
				}
				if content["text"] != want {
					t.Fatalf("changed untouched content: %s", value.Value)
				}
			}
			var before, after int64
			db.Model(&orm.WorkflowSlotRevision{}).Count(&before)
			if !tc.missing {
				if err := sink.Save(context.Background(), attempt, edit); err != nil {
					t.Fatal(err)
				}
			}
			db.Model(&orm.WorkflowSlotRevision{}).Count(&after)
			if after != before || (tc.missing && after != 6) {
				t.Fatalf("replay/rollback changed revisions: %d -> %d", before, after)
			}
		})
	}
}
