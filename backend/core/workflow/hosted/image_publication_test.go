package hosted

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
	"lazymind/core/workflow/executor"
)

func TestImagePublicationNormalizesURLAndReplaysWithoutAnotherRevision(t *testing.T) {
	s, db, grant := controlledService(t)
	const source = "http://localhost:5024/api/core/static-files/ai_generated/background.png?expires=1&sig=expired"
	raw, _ := json.Marshal(source)
	input := Publication{ExecutionHandle: grant.ExecutionHandle, Artifact: executor.Artifact{
		Slot: "report", ContentType: "image", Seq: 1, Value: raw,
	}}
	for _, value := range []json.RawMessage{raw, json.RawMessage(`{"url":"` + source + `"}`)} {
		input.Artifact.Value = value
		if err := s.Publish(t.Context(), "owner", "session-1", "attempt-1", input); err != nil {
			t.Fatal(err)
		}
	}
	var rows []orm.WorkflowHumanArtifact
	if err := db.Find(&rows).Error; err != nil || len(rows) != 1 {
		t.Fatalf("publication did not retain one revision: %d %v", len(rows), err)
	}
	var stored map[string]any
	if err := json.Unmarshal(rows[0].Value, &stored); err != nil || stored["path"] != "/static-files/ai_generated/background.png" {
		t.Fatalf("image reference was not normalized: %s %v", rows[0].Value, err)
	}
	// Reading a saved URL string and reading the new canonical value must both
	// produce the same browser shape, with a fresh signature rather than expiry=1.
	for _, value := range []json.RawMessage{raw, rows[0].Value} {
		var displayed map[string]any
		if err := json.Unmarshal(subagent.SignArtifactImageValue("image", value), &displayed); err != nil {
			t.Fatal(err)
		}
		url, _ := displayed["url"].(string)
		if !strings.HasPrefix(url, "/static-files/ai_generated/background.png?") || strings.Contains(url, "sig=expired") || displayed["path"] != nil {
			t.Fatalf("panel received an unusable image: %#v", displayed)
		}
	}
	// An unchanged retry also works for an already-saved string-valued image.
	if err := db.Model(&rows[0]).Update("value", raw).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(t.Context(), "owner", "session-1", "attempt-1", input); err != nil {
		t.Fatalf("same saved image stopped being replayable: %v", err)
	}
}

func TestImagePublicationRejectsMissingSourceBeforeSaving(t *testing.T) {
	s, db, grant := controlledService(t)
	for _, raw := range []string{`""`, `"not an image URL"`, `[]`, `{}`, `{"url":null}`, `"https://"`} {
		err := s.Publish(t.Context(), "owner", "session-1", "attempt-1", Publication{
			ExecutionHandle: grant.ExecutionHandle, Artifact: executor.Artifact{
				Slot: "report", ContentType: "image", Seq: 1, Value: json.RawMessage(raw),
			},
		})
		if err == nil || !strings.Contains(err.Error(), "INVALID_ARTIFACT") {
			t.Fatalf("invalid image was accepted: %s %v", raw, err)
		}
	}
	var count int64
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid image created an artifact: %d %v", count, err)
	}
}
