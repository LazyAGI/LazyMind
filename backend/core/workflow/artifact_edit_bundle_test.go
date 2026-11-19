package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func TestHTMLDeckEditsAndDisplaysWithoutLegacyPPTX(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "mixed-ppt", ConversationID: "conv", WorkflowID: "ppt-workflow", Status: "completed", CreateUserID: "owner"}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, row := range []orm.WorkflowSlotRevision{
		{ID: "format", SlotID: "presentation_output_format", StepID: "analyze_requirements", ContentSnapshot: json.RawMessage(`{"data":"html"}`)},
		{ID: "slide", SlotID: "preview_html", StepID: "generate_ppt", ListIndex: &zero, ContentSnapshot: json.RawMessage(`"<html><body>Slide</body></html>"`)},
		{ID: "legacy-export", SlotID: "native_pptx", StepID: "generate_ppt", ContentSnapshot: json.RawMessage(`{"path":"/static-files/deck.pptx"}`)},
	} {
		row.SessionID, row.Slot, row.Selected, row.Revision, row.Attempt = session.ID, row.SlotID, true, 1, 1
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.WorkflowSlotOrder{SessionID: session.ID, SlotID: "preview_html", OrderList: json.RawMessage(`[0]`)}).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := LoadDisplaySlots(t.Context(), db.DB, session.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("unexpected display artifacts: %+v, %v", rows, err)
	}
	artifacts, err := workflowstore.New(db.DB).ListArtifacts(t.Context(), "owner", session.ID)
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("unexpected artifacts sent to the agent: %+v, %v", artifacts, err)
	}
	for i := range rows {
		if rows[i].SlotID == "native_pptx" || artifacts[i].SlotID == "native_pptx" {
			t.Fatal("legacy PPTX leaked into HTML output")
		}
	}
	policy := artifactEditPolicy{Slots: []string{"preview_html", "native_pptx"}, RequiredIfPresent: []string{"native_pptx"}}
	edit := artifactEditBundle{StateVersion: session.StateVersion, Slots: map[string][]artifactEditItem{
		"preview_html": {{ListIndex: &zero, ContentType: "text", Value: json.RawMessage(`"<html><body>Moved slide title</body></html>"`)}},
	}}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, edit, policy); err != nil {
		t.Fatalf("HTML edit required the historical PPTX: %v", err)
	}
	var updated orm.WorkflowSlotRevision
	if err := db.First(&updated, "session_id = ? AND slot_id = ? AND selected = ?", session.ID, "preview_html", true).Error; err != nil || updated.Revision != 2 {
		t.Fatalf("HTML edit did not create the next revision: %+v, %v", updated, err)
	}
	var archived orm.WorkflowSlotRevision
	if err := db.First(&archived, "id = ?", "legacy-export").Error; err != nil || archived.Revision != 1 {
		t.Fatalf("historical file was changed: %+v, %v", archived, err)
	}
}

func TestCompletedEditBundleInsertDeleteAndConflict(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "bundle", ConversationID: "conv", WorkflowID: "test", Status: "completed", CreateUserID: "owner"}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	// Seed two synchronized page lists through the same native revision writer.
	for _, slot := range []string{"pages", "notes"} {
		for i := 0; i < 2; i++ {
			value, _ := json.Marshal(map[string]any{"text": fmt.Sprintf("%s%d", slot, i)})
			if _, err := WriteSlotRevisionWithHumanArtifact(t.Context(), db.DB, session.ID, slot, slot, "generate", 1, "list", nil, "text", value, nil, "human", nil, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := WriteSlotRevisionWithHumanArtifact(t.Context(), db.DB, session.ID, "download", "download", "generate", 1, "single", nil, "text", json.RawMessage(`"original"`), nil, "human", nil, nil); err != nil {
		t.Fatal(err)
	}
	current, _ := GetSession(t.Context(), db.DB, session.ID)
	one := 1
	input := artifactEditBundle{StateVersion: current.StateVersion, Slots: map[string][]artifactEditItem{
		"pages":    {{ListIndex: &one}, {ContentType: "text", Value: json.RawMessage(`"new page"`)}},
		"notes":    {{ListIndex: &one}, {ContentType: "text", Value: json.RawMessage(`"new notes"`)}},
		"download": {{ContentType: "text", Value: json.RawMessage(`"new download"`)}},
	}}
	policy := artifactEditPolicy{Slots: []string{"pages", "notes", "download"}, LinkedLists: []string{"pages", "notes"}, Required: []string{"download"}}
	bad := input
	bad.Slots = map[string][]artifactEditItem{"pages": input.Slots["pages"], "download": input.Slots["download"]}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, bad, policy); err == nil {
		t.Fatal("accepted missing notes")
	}
	broken := input
	broken.Slots = map[string][]artifactEditItem{"pages": input.Slots["pages"], "notes": input.Slots["notes"], "download": {{ContentType: "file", Value: json.RawMessage(`{"storage":"inline_base64","content_base64":"invalid"}`)}}}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, broken, policy); err == nil {
		t.Fatal("accepted invalid download")
	}
	unchanged, _ := GetSession(t.Context(), db.DB, session.ID)
	if unchanged.StateVersion != input.StateVersion {
		t.Fatal("failed bundle partially committed")
	}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, input, policy); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadSelectedSlots(t.Context(), db.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.SlotID]++
		if row.ListIndex != nil && *row.ListIndex == 0 {
			t.Fatal("deleted page remains selected")
		}
		if row.ListIndex != nil && *row.ListIndex == 1 && row.Revision != 1 {
			t.Fatal("untouched page rewritten")
		}
	}
	if counts["pages"] != 2 || counts["notes"] != 2 || counts["download"] != 1 {
		t.Fatal(counts)
	}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, input, policy); err == nil {
		t.Fatal("accepted stale retry")
	}
	current, _ = GetSession(t.Context(), db.DB, session.ID)
	if current.Status != "completed" {
		t.Fatal(current.Status)
	}
}

func TestCompletedEditRequiresDownloadOnlyWhenPresent(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "html-edit", ConversationID: "conv", WorkflowID: "test", Status: "completed", CreateUserID: "owner"}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"html", "notes"} {
		if _, err := WriteSlotRevisionWithHumanArtifact(t.Context(), db.DB, session.ID, slot, slot, "generate", 1, "list", nil, "text", json.RawMessage(`"original"`), nil, "human", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	policy := artifactEditPolicy{Slots: []string{"html", "notes", "download"}, LinkedLists: []string{"html", "notes"}, RequiredIfPresent: []string{"download"}}
	zero := 0
	current, _ := GetSession(t.Context(), db.DB, session.ID)
	input := artifactEditBundle{StateVersion: current.StateVersion, Slots: map[string][]artifactEditItem{
		"html":  {{ListIndex: &zero, ContentType: "text", Value: json.RawMessage(`"edited"`)}},
		"notes": {{ListIndex: &zero}},
	}}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, input, policy); err != nil {
		t.Fatalf("HTML-only edit should not require a download: %v", err)
	}
	if _, err := WriteSlotRevisionWithHumanArtifact(t.Context(), db.DB, session.ID, "download", "download", "generate", 1, "single", nil, "text", json.RawMessage(`"original download"`), nil, "human", nil, nil); err != nil {
		t.Fatal(err)
	}
	current, _ = GetSession(t.Context(), db.DB, session.ID)
	input.StateVersion = current.StateVersion
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, input, policy); err == nil || !strings.Contains(err.Error(), "updated download required") {
		t.Fatalf("existing downloadable file must remain synchronized: %v", err)
	}
	unchanged, _ := GetSession(t.Context(), db.DB, session.ID)
	if unchanged.StateVersion != current.StateVersion {
		t.Fatal("rejected edit mutated the session")
	}
	input.Slots["download"] = []artifactEditItem{{ContentType: "text", Value: json.RawMessage(`"updated download"`)}}
	if err := commitArtifactEditBundle(t.Context(), db.DB, session.ID, input, policy); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadSelectedSlots(t.Context(), db.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.SlotID == "notes" && row.Revision != 1 {
			t.Fatal("unchanged notes were rewritten")
		}
	}
}
