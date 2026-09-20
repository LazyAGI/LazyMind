package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func humanArtifactValueHash(value json.RawMessage) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func bindSelectedHumanDraft(t *testing.T, db *orm.DB, validity string) {
	t.Helper()
	if err := db.AutoMigrate(
		&orm.WorkflowSessionStep{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowInputBinding{},
	); err != nil {
		t.Fatalf("migrate input bindings: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSessionStep{
		ID: "attempt-consumer", SessionID: "session-draft", StepID: "consume_document",
		Attempt: 1, TaskID: "task-consumer", Status: "succeeded", Validity: validity,
		ProgressJSON: `{}`, ResultJSON: `{}`, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create %s consumer attempt: %v", validity, err)
	}
	if err := db.Create(&orm.WorkflowAttemptInputBinding{
		ID: "binding-draft", SessionID: "session-draft", AttemptID: "attempt-consumer",
		MaterialID: "draft_document", MaterialRevisionID: "revision-draft",
		SourceType: "artifact", CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("bind selected draft: %v", err)
	}
}

func TestAttemptInputBindingHashesHumanArtifactValue(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	if err := db.AutoMigrate(&orm.WorkflowInputBinding{}); err != nil {
		t.Fatalf("migrate workflow input bindings: %v", err)
	}
	rawValue := json.RawMessage("{\n  \"z\": 1,\n  \"text\": \"original\",\n  \"a\": 2\n}")
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "human-draft").
		Update("value", rawValue).Error; err != nil {
		t.Fatalf("set non-canonical artifact value: %v", err)
	}
	artifact, _ := loadSelectedHumanDraft(t, db)
	var logicalValue any
	if err := json.Unmarshal(artifact.Value, &logicalValue); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	canonicalValue, err := json.Marshal(logicalValue)
	if err != nil {
		t.Fatalf("canonicalize fixture: %v", err)
	}
	if humanArtifactValueHash(artifact.Value) == humanArtifactValueHash(canonicalValue) {
		t.Fatal("fixture does not distinguish raw-byte hashing from JSON normalization")
	}

	binding := attemptInputBindingFromWitness(
		db.DB, "session-draft", "attempt-consumer",
		graphengine.Witness{MaterialID: "draft_document", RevisionID: "revision-draft"},
		time.Now().UTC(),
	)

	if binding.SourceType != "artifact" || binding.MaterialRevisionID != "revision-draft" {
		t.Fatalf("binding identity = %#v", binding)
	}
	if want := humanArtifactValueHash(artifact.Value); binding.ContentHash != want {
		t.Fatalf("content hash = %q, want %q", binding.ContentHash, want)
	}
}

func TestBoundHumanDraftSaveUsesCopyOnWrite(t *testing.T) {
	for _, validity := range []string{"effective", "stale"} {
		t.Run(validity, func(t *testing.T) {
			db := seedSelectedHumanDraft(t)
			bindSelectedHumanDraft(t, db, validity)

			recorder := patchSelectedHumanDraft(t, "draft", "updated", 1)
			data := responseData(t, recorder)
			if recorder.Code != http.StatusOK || data["revision"] != float64(2) ||
				data["draft_version"] != float64(1) {
				t.Fatalf("bound draft save: status=%d body=%s", recorder.Code, recorder.Body.String())
			}

			var revisions []orm.WorkflowSlotRevision
			if err := db.Where(
				"session_id = ? AND slot_id = ?", "session-draft", "draft_document",
			).Order("revision ASC").Find(&revisions).Error; err != nil {
				t.Fatalf("load revisions: %v", err)
			}
			if len(revisions) != 2 || revisions[0].ID != "revision-draft" ||
				revisions[0].Revision != 1 || revisions[0].Selected ||
				revisions[1].ID == "revision-draft" || revisions[1].Revision != 2 ||
				!revisions[1].Selected || revisions[0].HumanArtifactID == nil ||
				revisions[1].HumanArtifactID == nil ||
				*revisions[0].HumanArtifactID != "human-draft" ||
				*revisions[1].HumanArtifactID == "human-draft" {
				t.Fatalf("copy-on-write revisions = %#v", revisions)
			}
			oldArtifact, oldDraftVersion := loadSelectedHumanDraft(t, db)
			assertArtifactJSON(t, oldArtifact.Value, `{"text":"original"}`)
			if oldDraftVersion != 1 {
				t.Fatalf("old draft version = %d, want 1", oldDraftVersion)
			}
			var newArtifact orm.WorkflowHumanArtifact
			if err := db.First(&newArtifact, "id = ?", *revisions[1].HumanArtifactID).Error; err != nil {
				t.Fatalf("load new artifact: %v", err)
			}
			assertArtifactJSON(t, newArtifact.Value, `{"text":"updated"}`)
			if newArtifact.DraftVersion != 1 {
				t.Fatalf("new draft version = %d, want 1", newArtifact.DraftVersion)
			}
			var artifacts int64
			if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
				t.Fatalf("count artifacts: %v", err)
			}
			if artifacts != 2 {
				t.Fatalf("artifacts = %d, want exactly 2", artifacts)
			}
			var binding orm.WorkflowAttemptInputBinding
			if err := db.First(&binding, "id = ?", "binding-draft").Error; err != nil {
				t.Fatalf("load binding: %v", err)
			}
			if binding.MaterialRevisionID != "revision-draft" {
				t.Fatalf("binding revision = %q, want revision-draft", binding.MaterialRevisionID)
			}
			var boundRevision orm.WorkflowSlotRevision
			if err := db.First(&boundRevision, "id = ?", binding.MaterialRevisionID).Error; err != nil {
				t.Fatalf("bound revision no longer resolves: %v", err)
			}
			if boundRevision.Revision != 1 || boundRevision.HumanArtifactID == nil ||
				*boundRevision.HumanArtifactID != "human-draft" {
				t.Fatalf("bound revision changed = %#v", boundRevision)
			}
		})
	}
}

func TestBoundHumanDraftConcurrentSavesCreateOneRevision(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	bindSelectedHumanDraft(t, db, "effective")
	start := make(chan struct{})
	type result struct {
		text string
		code int
		data map[string]any
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, text := range []string{"first", "second"} {
		workers.Add(1)
		go func(text string) {
			defer workers.Done()
			<-start
			recorder := patchSelectedHumanDraft(t, "draft", text, 1)
			results <- result{text: text, code: recorder.Code, data: responseData(t, recorder)}
		}(text)
	}
	close(start)
	workers.Wait()
	close(results)

	statuses := make([]int, 0, 2)
	winner := ""
	for result := range results {
		statuses = append(statuses, result.code)
		if result.code == http.StatusOK {
			winner = result.text
			if result.data["revision"] != float64(2) || result.data["draft_version"] != float64(1) {
				t.Fatalf("winning versions = %#v", result.data)
			}
		} else if result.code == http.StatusConflict && result.data["code"] != "REVISION_CONFLICT" {
			t.Fatalf("losing conflict = %#v", result.data)
		}
	}
	sort.Ints(statuses)
	if fmt.Sprint(statuses) != "[200 409]" || winner == "" {
		t.Fatalf("statuses=%v winner=%q", statuses, winner)
	}

	oldArtifact, oldDraftVersion := loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, oldArtifact.Value, `{"text":"original"}`)
	if oldDraftVersion != 1 {
		t.Fatalf("old draft version = %d, want 1", oldDraftVersion)
	}
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where(
		"session_id = ? AND slot_id = ?", "session-draft", "draft_document",
	).Order("revision ASC").Find(&revisions).Error; err != nil {
		t.Fatalf("load revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0].ID != "revision-draft" ||
		revisions[0].Revision != 1 || revisions[0].Selected ||
		revisions[1].ID == "revision-draft" || revisions[1].Revision != 2 ||
		!revisions[1].Selected || revisions[0].HumanArtifactID == nil ||
		*revisions[0].HumanArtifactID != "human-draft" || revisions[1].HumanArtifactID == nil {
		t.Fatalf("revisions = %#v", revisions)
	}
	var binding orm.WorkflowAttemptInputBinding
	if err := db.First(&binding, "id = ?", "binding-draft").Error; err != nil {
		t.Fatalf("load binding: %v", err)
	}
	if binding.MaterialRevisionID != "revision-draft" {
		t.Fatalf("binding revision = %q, want revision-draft", binding.MaterialRevisionID)
	}
	var boundRevision orm.WorkflowSlotRevision
	if err := db.First(&boundRevision, "id = ?", binding.MaterialRevisionID).Error; err != nil {
		t.Fatalf("bound revision no longer resolves: %v", err)
	}
	if boundRevision.Revision != 1 || boundRevision.HumanArtifactID == nil ||
		*boundRevision.HumanArtifactID != "human-draft" {
		t.Fatalf("bound revision changed = %#v", boundRevision)
	}
	var newArtifact orm.WorkflowHumanArtifact
	if err := db.First(&newArtifact, "id = ?", *revisions[1].HumanArtifactID).Error; err != nil {
		t.Fatalf("load winning artifact: %v", err)
	}
	assertArtifactJSON(t, newArtifact.Value, fmt.Sprintf(`{"text":%q}`, winner))
	if newArtifact.DraftVersion != 1 {
		t.Fatalf("new draft version = %d, want 1", newArtifact.DraftVersion)
	}
	var artifacts int64
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if artifacts != 2 {
		t.Fatalf("artifacts = %d, want exactly 2 without loser orphan", artifacts)
	}
}
