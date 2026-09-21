package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func seedSelectedHumanDraft(t *testing.T) *orm.DB {
	t.Helper()
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatalf("migrate human artifacts: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-draft", ConversationID: "conversation-draft", WorkflowID: "writer-workflow",
		Status: SessionStatusActive, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	humanID := "human-draft"
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session-draft", Slot: "draft_document",
		ContentType: "text", Value: json.RawMessage(`{"text":"original"}`), CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create human artifact: %v", err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-draft", SessionID: "session-draft", SlotID: "draft_document",
		Revision: 1, Selected: true, ChangeSource: "human", HumanArtifactID: &humanID,
		Slot: "draft_document", StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create selected revision: %v", err)
	}
	return db
}

func patchSelectedHumanDraft(
	t *testing.T, mode, text string, baseDraftVersion int64,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPatch,
		"/workflow-sessions/session-draft/slots/draft_document/items/idx/-1",
		jsonBody(fmt.Sprintf(
			`{"value":{"text":%q},"content_type":"text","mode":%q,"base_revision":1,"base_draft_version":%d}`,
			text, mode, baseDraftVersion,
		)),
	)
	req = mux.SetURLVars(req, map[string]string{
		"session_id": "session-draft", "slot_id": "draft_document", "list_index": "-1",
	})
	rec := httptest.NewRecorder()
	PatchSlotItemByIndex(rec, req)
	return rec
}

func patchSelectedHumanDraftWithoutDraftVersion(
	t *testing.T, mode, text string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPatch,
		"/workflow-sessions/session-draft/slots/draft_document/items/idx/-1",
		jsonBody(fmt.Sprintf(
			`{"value":{"text":%q},"content_type":"text","mode":%q,"base_revision":1}`,
			text, mode,
		)),
	)
	req = mux.SetURLVars(req, map[string]string{
		"session_id": "session-draft", "slot_id": "draft_document", "list_index": "-1",
	})
	rec := httptest.NewRecorder()
	PatchSlotItemByIndex(rec, req)
	return rec
}

func responseData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	return body.Data
}

func loadSelectedHumanDraft(t *testing.T, db *orm.DB) (orm.WorkflowHumanArtifact, int64) {
	t.Helper()
	var artifact orm.WorkflowHumanArtifact
	if err := db.First(&artifact, "id = ?", "human-draft").Error; err != nil {
		t.Fatalf("load human artifact: %v", err)
	}
	var draftVersion int64
	if err := db.Raw(
		"SELECT draft_version FROM plugin_human_artifacts WHERE id = ?", "human-draft",
	).Scan(&draftVersion).Error; err != nil {
		t.Fatalf("load draft version: %v", err)
	}
	return artifact, draftVersion
}

func assertSingleSelectedRevisionOne(t *testing.T, db *orm.DB) {
	t.Helper()
	var revisions []orm.WorkflowSlotRevision
	if err := db.Where(
		"session_id = ? AND slot_id = ?", "session-draft", "draft_document",
	).Find(&revisions).Error; err != nil {
		t.Fatalf("load revisions: %v", err)
	}
	if len(revisions) != 1 || revisions[0].Revision != 1 || !revisions[0].Selected ||
		revisions[0].HumanArtifactID == nil || *revisions[0].HumanArtifactID != "human-draft" {
		t.Fatalf("revisions = %#v, want one selected revision 1", revisions)
	}
}

func assertArtifactJSON(t *testing.T, value json.RawMessage, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(value, &actual); err != nil {
		t.Fatalf("decode artifact value: %v; value=%s", err, value)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatalf("decode expected artifact value: %v; value=%s", err, want)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("artifact value = %#v, want %#v", actual, expected)
	}
}

func TestHumanArtifactTextWritePathsCanonicalizeRootStrings(t *testing.T) {
	t.Run("draft update", func(t *testing.T) {
		db := seedSelectedHumanDraft(t)
		baseRevision, baseDraftVersion := 1, int64(1)
		_, nextDraftVersion, updated, err := UpdateSelectedHumanArtifactValue(
			t.Context(), db.DB, "session-draft", "draft_document", nil,
			"text", json.RawMessage(`"# Updated\nBody"`), nil,
			&baseRevision, &baseDraftVersion,
		)
		if err != nil || !updated || nextDraftVersion != 2 {
			t.Fatalf("draft update: updated=%v draft_version=%d err=%v", updated, nextDraftVersion, err)
		}
		artifact, storedDraftVersion := loadSelectedHumanDraft(t, db)
		assertSingleSelectedRevisionOne(t, db)
		var artifacts int64
		if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
			t.Fatal(err)
		}
		if artifacts != 1 {
			t.Fatalf("human artifacts = %d, want 1 for in-place draft", artifacts)
		}
		if artifact.ID != "human-draft" || storedDraftVersion != 2 {
			t.Fatalf("in-place artifact = %q draft_version=%d", artifact.ID, storedDraftVersion)
		}
		assertArtifactJSON(t, artifact.Value, `{"text":"# Updated\nBody"}`)
	})

	t.Run("new revision", func(t *testing.T) {
		db := seedSelectedHumanDraft(t)
		baseRevision, baseDraftVersion := 1, int64(1)
		created, err := WriteSlotRevisionWithHumanArtifact(
			t.Context(), db.DB,
			"session-draft", "draft_document", "draft_document", "write_document", 1,
			"single", nil, "text/plain", json.RawMessage(`"# Checkpoint\nBody"`), nil,
			"human", &baseRevision, &baseDraftVersion,
		)
		if err != nil {
			t.Fatalf("create revision: %v", err)
		}
		if created.Revision != 2 || created.HumanArtifactID == nil {
			t.Fatalf("created revision = %#v", created)
		}
		var artifact orm.WorkflowHumanArtifact
		if err := db.First(&artifact, "id = ?", *created.HumanArtifactID).Error; err != nil {
			t.Fatal(err)
		}
		assertArtifactJSON(t, artifact.Value, `{"text":"# Checkpoint\nBody"}`)
	})

	t.Run("existing object metadata", func(t *testing.T) {
		db := seedSelectedHumanDraft(t)
		baseRevision, baseDraftVersion := 1, int64(1)
		value := json.RawMessage(`{"text":"kept","language":"zh","meta":{"anchor":"intro"}}`)
		_, _, updated, err := UpdateSelectedHumanArtifactValue(
			t.Context(), db.DB, "session-draft", "draft_document", nil,
			"text/markdown", value, nil, &baseRevision, &baseDraftVersion,
		)
		if err != nil || !updated {
			t.Fatalf("update object: updated=%v err=%v", updated, err)
		}
		artifact, _ := loadSelectedHumanDraft(t, db)
		assertArtifactJSON(t, artifact.Value, string(value))
	})
}

func TestEnrichSlotsProjectsHistoricalRootTextWithoutMutatingHistory(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}, &orm.WorkflowInputBinding{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	humanID := "historical-root-text"
	historical := json.RawMessage(`"# Historical\n\\# literal hash"`)
	if err := db.Create(&orm.WorkflowHumanArtifact{
		ID: humanID, SessionID: "session-history", Slot: "document", ContentType: "text",
		Value: historical, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	revision := orm.WorkflowSlotRevision{
		ID: "historical-root-revision", SessionID: "session-history", SlotID: "document",
		Slot: "document", StepID: "source", Revision: 1, Selected: true,
		HumanArtifactID: &humanID, Validity: "effective", CreatedAt: now,
	}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	binding := attemptInputBindingFromWitness(
		db.DB, "session-history", "consumer-attempt",
		graphengine.Witness{MaterialID: "document", RevisionID: revision.ID}, now,
	)
	if want := humanArtifactValueHash(historical); binding.ContentHash != want {
		t.Fatalf("historical root hash = %q, want raw-byte hash %q", binding.ContentHash, want)
	}

	slots := []slotDTO{toSlotDTO(&revision)}
	enrichSlots(t.Context(), db.DB, "session-history", slots)
	assertArtifactJSON(t, slots[0].ArtifactValue, `{"text":"# Historical\n\\# literal hash"}`)

	var stored orm.WorkflowHumanArtifact
	if err := db.First(&stored, "id = ?", humanID).Error; err != nil {
		t.Fatal(err)
	}
	if string(stored.Value) != string(historical) {
		t.Fatalf("historical bytes changed: got %s, want %s", stored.Value, historical)
	}
}

func TestPatchSlotItemDraftRejectsStaleDraftVersion(t *testing.T) {
	db := seedSelectedHumanDraft(t)

	first := patchSelectedHumanDraft(t, "draft", "first", 1)
	if first.Code != http.StatusOK {
		t.Fatalf("first draft save: got %d, body=%s", first.Code, first.Body.String())
	}
	second := patchSelectedHumanDraft(t, "draft", "stale", 1)
	if second.Code != http.StatusConflict {
		t.Fatalf("stale draft save: got %d, want 409; body=%s", second.Code, second.Body.String())
	}
	if code := responseData(t, second)["code"]; code != "DRAFT_VERSION_CONFLICT" {
		t.Fatalf("stale draft code = %v, want DRAFT_VERSION_CONFLICT", code)
	}
	if version := responseData(t, first)["draft_version"]; version != float64(2) {
		t.Fatalf("draft version = %v, want 2", version)
	}

	artifact, draftVersion := loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, artifact.Value, `{"text":"first"}`)
	if draftVersion != 2 {
		t.Fatalf("persisted draft version = %d, want 2", draftVersion)
	}
	assertSingleSelectedRevisionOne(t, db)

	third := patchSelectedHumanDraft(t, "draft", "second", 2)
	if third.Code != http.StatusOK {
		t.Fatalf("second current draft save: got %d, body=%s", third.Code, third.Body.String())
	}
	if version := responseData(t, third)["draft_version"]; version != float64(3) {
		t.Fatalf("draft version = %v, want 3", version)
	}
	artifact, draftVersion = loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, artifact.Value, `{"text":"second"}`)
	if draftVersion != 3 {
		t.Fatalf("artifact draft version after second current save = %d, want 3", draftVersion)
	}
	assertSingleSelectedRevisionOne(t, db)
}

func TestPatchSlotItemCheckpointRejectsStaleDraftVersion(t *testing.T) {
	db := seedSelectedHumanDraft(t)

	if rec := patchSelectedHumanDraft(t, "draft", "first", 1); rec.Code != http.StatusOK {
		t.Fatalf("first draft save: got %d, body=%s", rec.Code, rec.Body.String())
	}
	stale := patchSelectedHumanDraft(t, "checkpoint", "stale checkpoint", 1)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale checkpoint save: got %d, want 409; body=%s", stale.Code, stale.Body.String())
	}
	if code := responseData(t, stale)["code"]; code != "DRAFT_VERSION_CONFLICT" {
		t.Fatalf("stale checkpoint code = %v, want DRAFT_VERSION_CONFLICT", code)
	}

	var revisions int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).
		Where("session_id = ? AND slot_id = ?", "session-draft", "draft_document").
		Count(&revisions).Error; err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if revisions != 1 {
		t.Fatalf("revision count = %d, want 1 after rejected checkpoint", revisions)
	}
	var artifacts int64
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
		t.Fatalf("count human artifacts: %v", err)
	}
	if artifacts != 1 {
		t.Fatalf("human artifact count = %d, want 1 after rejected checkpoint", artifacts)
	}
	artifact, draftVersion := loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, artifact.Value, `{"text":"first"}`)
	if draftVersion != 2 {
		t.Fatalf("artifact draft version after rejected checkpoint = %d, want 2", draftVersion)
	}
	assertSingleSelectedRevisionOne(t, db)
}

func TestPatchSlotItemCheckpointAcceptsCurrentDraftVersion(t *testing.T) {
	db := seedSelectedHumanDraft(t)

	if rec := patchSelectedHumanDraft(t, "draft", "first", 1); rec.Code != http.StatusOK {
		t.Fatalf("draft save: got %d, body=%s", rec.Code, rec.Body.String())
	}
	checkpoint := patchSelectedHumanDraft(t, "checkpoint", "checkpoint", 2)
	if checkpoint.Code != http.StatusOK {
		t.Fatalf("current checkpoint save: got %d, body=%s", checkpoint.Code, checkpoint.Body.String())
	}
	data := responseData(t, checkpoint)
	if revision, draftVersion := data["revision"], data["draft_version"]; revision != float64(2) || draftVersion != float64(1) {
		t.Fatalf("checkpoint versions = revision %v draft %v, want 2 and 1", revision, draftVersion)
	}

	var revisions []orm.WorkflowSlotRevision
	if err := db.Where(
		"session_id = ? AND slot_id = ?", "session-draft", "draft_document",
	).Order("revision ASC").Find(&revisions).Error; err != nil {
		t.Fatalf("load revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Revision != 1 || revisions[0].Selected ||
		revisions[0].HumanArtifactID == nil || *revisions[0].HumanArtifactID != "human-draft" ||
		revisions[1].Revision != 2 || !revisions[1].Selected ||
		revisions[1].HumanArtifactID == nil || *revisions[1].HumanArtifactID == "human-draft" {
		t.Fatalf("checkpoint revisions = %#v", revisions)
	}
	var artifact orm.WorkflowHumanArtifact
	if err := db.First(&artifact, "id = ?", *revisions[1].HumanArtifactID).Error; err != nil {
		t.Fatalf("load checkpoint artifact: %v", err)
	}
	assertArtifactJSON(t, artifact.Value, `{"text":"checkpoint"}`)
	var draftVersion int64
	if err := db.Raw(
		"SELECT draft_version FROM plugin_human_artifacts WHERE id = ?", artifact.ID,
	).Scan(&draftVersion).Error; err != nil {
		t.Fatalf("load checkpoint draft version: %v", err)
	}
	if draftVersion != 1 {
		t.Fatalf("checkpoint draft version = %d, want 1", draftVersion)
	}
	oldArtifact, oldDraftVersion := loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, oldArtifact.Value, `{"text":"first"}`)
	if oldDraftVersion != 2 {
		t.Fatalf("old artifact draft version = %d, want 2", oldDraftVersion)
	}
	var artifacts int64
	if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
		t.Fatalf("count human artifacts: %v", err)
	}
	if artifacts != 2 {
		t.Fatalf("human artifact count = %d, want 2", artifacts)
	}
}

func TestPatchSlotItemSelectedHumanRequiresDraftVersion(t *testing.T) {
	for _, mode := range []string{"draft", "checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			db := seedSelectedHumanDraft(t)
			rec := patchSelectedHumanDraftWithoutDraftVersion(t, mode, "unversioned")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unversioned %s: got %d, want 400; body=%s", mode, rec.Code, rec.Body.String())
			}
			if code := responseData(t, rec)["code"]; code != "DRAFT_VERSION_REQUIRED" {
				t.Fatalf("unversioned %s code = %v, want DRAFT_VERSION_REQUIRED", mode, code)
			}
			assertSingleSelectedRevisionOne(t, db)
			var artifacts int64
			if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
				t.Fatalf("count human artifacts: %v", err)
			}
			if artifacts != 1 {
				t.Fatalf("human artifact count = %d, want 1", artifacts)
			}
			var artifact orm.WorkflowHumanArtifact
			if err := db.First(&artifact, "id = ?", "human-draft").Error; err != nil {
				t.Fatalf("load human artifact: %v", err)
			}
			assertArtifactJSON(t, artifact.Value, `{"text":"original"}`)
			var draftVersion int64
			if err := db.Raw(
				"SELECT draft_version FROM plugin_human_artifacts WHERE id = ?", artifact.ID,
			).Scan(&draftVersion).Error; err != nil {
				t.Fatalf("load draft version: %v", err)
			}
			if draftVersion != 1 {
				t.Fatalf("draft version after rejected unversioned %s = %d, want 1", mode, draftVersion)
			}
		})
	}
}

func TestPatchSlotItemStaleRevisionPrecedesDraftVersionValidation(t *testing.T) {
	for _, mode := range []string{"draft", "checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			seedSelectedHumanDraft(t)
			req := httptest.NewRequest(
				http.MethodPatch,
				"/workflow-sessions/session-draft/slots/draft_document/items/idx/-1",
				jsonBody(fmt.Sprintf(
					`{"value":{"text":"stale"},"content_type":"text","mode":%q,"base_revision":2}`,
					mode,
				)),
			)
			req = mux.SetURLVars(req, map[string]string{
				"session_id": "session-draft", "slot_id": "draft_document", "list_index": "-1",
			})
			recorder := httptest.NewRecorder()
			PatchSlotItemByIndex(recorder, req)
			if recorder.Code != http.StatusConflict ||
				responseData(t, recorder)["code"] != "REVISION_CONFLICT" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestFinalHumanArtifactCASMissingSelectedMapsToRevisionConflict(t *testing.T) {
	for _, operation := range []string{"draft", "checkpoint"} {
		t.Run(operation, func(t *testing.T) {
			db := seedSelectedHumanDraft(t)
			if err := db.Delete(&orm.WorkflowSlotRevision{}, "id = ?", "revision-draft").Error; err != nil {
				t.Fatalf("delete selected revision: %v", err)
			}
			baseRevision := 1
			baseDraftVersion := int64(1)
			var err error
			if operation == "draft" {
				_, _, _, err = UpdateSelectedHumanArtifactValue(
					t.Context(), db.DB, "session-draft", "draft_document", nil,
					"text", json.RawMessage(`{"text":"updated"}`), nil,
					&baseRevision, &baseDraftVersion,
				)
			} else {
				_, err = WriteSlotRevisionWithHumanArtifact(
					t.Context(), db.DB, "session-draft", "draft_document", "draft_document",
					"write_document", 1, "single", nil, "text",
					json.RawMessage(`{"text":"checkpoint"}`), nil, "human",
					&baseRevision, &baseDraftVersion,
				)
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("error=%v, want revision conflict", err)
			}
			var artifacts int64
			if err := db.Model(&orm.WorkflowHumanArtifact{}).Count(&artifacts).Error; err != nil {
				t.Fatalf("count artifacts: %v", err)
			}
			if artifacts != 1 {
				t.Fatalf("artifacts=%d, want no orphan", artifacts)
			}
		})
	}
}

func TestPatchSlotItemSelectedDisappearsAfterPreflightReturnsConflict(t *testing.T) {
	for _, testCase := range []struct {
		mode         string
		deleteOnRead int
	}{
		{mode: "checkpoint", deleteOnRead: 1},
		{mode: "draft", deleteOnRead: 2},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			db := newHandlerTestDB(t)
			now := time.Now().UTC()
			if err := db.Create(&orm.WorkflowSession{
				ID: "session-race", ConversationID: "conversation-race",
				WorkflowID: "writer-workflow", Status: SessionStatusActive,
				CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				t.Fatalf("create session: %v", err)
			}
			if err := db.Create(&orm.WorkflowSlotRevision{
				ID: "revision-race", SessionID: "session-race", SlotID: "draft_document",
				Revision: 1, Selected: true, ChangeSource: "ai", Slot: "draft_document",
				StepID: "write_document", Attempt: 1, CreatedAt: now,
			}).Error; err != nil {
				t.Fatalf("create revision: %v", err)
			}

			reads := 0
			var deleteErr error
			callbackName := "test:delete-selected-after-read:" + testCase.mode
			if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table != "plugin_slot_revisions" ||
					!strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "selected") {
					return
				}
				reads++
				if reads == testCase.deleteOnRead {
					deleteErr = tx.Session(&gorm.Session{NewDB: true}).
						Exec("DELETE FROM plugin_slot_revisions WHERE id = ?", "revision-race").Error
				}
			}); err != nil {
				t.Fatalf("register query callback: %v", err)
			}
			t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

			req := httptest.NewRequest(
				http.MethodPatch,
				"/workflow-sessions/session-race/slots/draft_document/items/idx/-1",
				jsonBody(fmt.Sprintf(
					`{"value":{"text":"updated"},"content_type":"text","mode":%q,"base_revision":1}`,
					testCase.mode,
				)),
			)
			req = mux.SetURLVars(req, map[string]string{
				"session_id": "session-race", "slot_id": "draft_document", "list_index": "-1",
			})
			recorder := httptest.NewRecorder()
			PatchSlotItemByIndex(recorder, req)
			if deleteErr != nil {
				t.Fatalf("delete selected revision: %v", deleteErr)
			}
			if recorder.Code != http.StatusConflict ||
				responseData(t, recorder)["code"] != "REVISION_CONFLICT" {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestPatchSlotItemArtifactBackedSourcesEnforceDraftVersion(t *testing.T) {
	for _, changeSource := range []string{"provider_sync", "host"} {
		for _, mode := range []string{"draft", "checkpoint"} {
			t.Run(changeSource+"_"+mode, func(t *testing.T) {
				db := seedSelectedHumanDraft(t)
				if err := db.Model(&orm.WorkflowSlotRevision{}).
					Where("id = ?", "revision-draft").
					Update("change_source", changeSource).Error; err != nil {
					t.Fatalf("set change source: %v", err)
				}

				missing := patchSelectedHumanDraftWithoutDraftVersion(t, mode, "missing")
				if missing.Code != http.StatusBadRequest ||
					responseData(t, missing)["code"] != "DRAFT_VERSION_REQUIRED" {
					t.Fatalf("missing baseline: status=%d body=%s", missing.Code, missing.Body.String())
				}

				stale := patchSelectedHumanDraft(t, mode, "stale", 2)
				if stale.Code != http.StatusConflict ||
					responseData(t, stale)["code"] != "DRAFT_VERSION_CONFLICT" {
					t.Fatalf("stale baseline: status=%d body=%s", stale.Code, stale.Body.String())
				}

				artifact, draftVersion := loadSelectedHumanDraft(t, db)
				assertArtifactJSON(t, artifact.Value, `{"text":"original"}`)
				if draftVersion != 1 {
					t.Fatalf("draft version = %d, want 1", draftVersion)
				}
				assertSingleSelectedRevisionOne(t, db)

				accepted := patchSelectedHumanDraft(t, mode, "accepted", 1)
				data := responseData(t, accepted)
				if accepted.Code != http.StatusOK || data["revision"] != float64(2) ||
					data["draft_version"] != float64(1) {
					t.Fatalf("accepted baseline: status=%d body=%s", accepted.Code, accepted.Body.String())
				}
				var revisions []orm.WorkflowSlotRevision
				if err := db.Where(
					"session_id = ? AND slot_id = ?", "session-draft", "draft_document",
				).Order("revision ASC").Find(&revisions).Error; err != nil {
					t.Fatalf("load revisions: %v", err)
				}
				if len(revisions) != 2 || revisions[0].Selected ||
					revisions[0].ChangeSource != changeSource || !revisions[1].Selected ||
					revisions[1].ChangeSource != "human" || revisions[1].HumanArtifactID == nil ||
					*revisions[1].HumanArtifactID == "human-draft" {
					t.Fatalf("copy-on-write revisions = %#v", revisions)
				}
				oldArtifact, oldDraftVersion := loadSelectedHumanDraft(t, db)
				assertArtifactJSON(t, oldArtifact.Value, `{"text":"original"}`)
				if oldDraftVersion != 1 {
					t.Fatalf("old draft version = %d, want 1", oldDraftVersion)
				}
			})
		}
	}
}

func TestUpdateSelectedHumanArtifactValueUsesAtomicDraftVersionPredicate(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	type capturedUpdate struct {
		sql  string
		vars []any
	}
	var updates []capturedUpdate
	callbackName := "test:capture-human-draft-cas"
	if err := db.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "plugin_human_artifacts" {
			updates = append(updates, capturedUpdate{
				sql:  tx.Statement.SQL.String(),
				vars: append([]any(nil), tx.Statement.Vars...),
			})
		}
	}); err != nil {
		t.Fatalf("register SQL capture callback: %v", err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	rec := patchSelectedHumanDraft(t, "draft", "first", 1)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft save: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(updates) != 1 {
		t.Fatalf("human artifact updates = %d, want 1: %#v", len(updates), updates)
	}
	update := updates[0]
	normalized := strings.ToLower(update.sql)
	normalized = strings.NewReplacer("`", "", `"`, "").Replace(normalized)
	normalized = regexp.MustCompile(`\$[0-9]+`).ReplaceAllString(normalized, "?")
	whereAt := strings.Index(normalized, " where ")
	if whereAt < 0 {
		t.Fatalf("human artifact update has no WHERE clause: %s", update.sql)
	}
	setClause, whereClause := normalized[:whereAt], normalized[whereAt+len(" where "):]
	if !strings.Contains(setClause, "value") || !strings.Contains(setClause, "draft_version") ||
		!strings.Contains(whereClause, "id = ?") ||
		!strings.Contains(whereClause, "draft_version = ?") ||
		!strings.Contains(whereClause, " and ") || strings.Contains(whereClause, " or ") {
		t.Fatalf("human artifact update is not an identity-and-version CAS: %s", update.sql)
	}
	if len(update.vars) < 2 || fmt.Sprint(update.vars[len(update.vars)-2]) != "human-draft" ||
		fmt.Sprint(update.vars[len(update.vars)-1]) != "1" {
		t.Fatalf("human artifact CAS vars = %#v, want trailing [human-draft 1]", update.vars)
	}
}

func TestPatchSlotItemDraftCASAllowsExactlyOneConcurrentSave(t *testing.T) {
	db := seedSelectedHumanDraft(t)
	start := make(chan struct{})
	type result struct {
		text string
		rec  *httptest.ResponseRecorder
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, text := range []string{"first", "second"} {
		workers.Add(1)
		go func(text string) {
			defer workers.Done()
			<-start
			results <- result{text: text, rec: patchSelectedHumanDraft(t, "draft", text, 1)}
		}(text)
	}
	close(start)
	workers.Wait()
	close(results)

	statuses := make([]int, 0, 2)
	collected := make([]result, 0, 2)
	winner := ""
	for result := range results {
		collected = append(collected, result)
		statuses = append(statuses, result.rec.Code)
	}
	sort.Ints(statuses)
	if len(statuses) != 2 || statuses[0] != http.StatusOK || statuses[1] != http.StatusConflict {
		t.Fatalf("concurrent statuses = %v, want [200 409]", statuses)
	}
	for _, result := range collected {
		switch result.rec.Code {
		case http.StatusOK:
			winner = result.text
			if version := responseData(t, result.rec)["draft_version"]; version != float64(2) {
				t.Fatalf("winning draft version = %v, want 2", version)
			}
		case http.StatusConflict:
			if code := responseData(t, result.rec)["code"]; code != "DRAFT_VERSION_CONFLICT" {
				t.Fatalf("losing draft code = %v, want DRAFT_VERSION_CONFLICT", code)
			}
		default:
			t.Fatalf("concurrent draft status = %d, body=%s", result.rec.Code, result.rec.Body.String())
		}
	}

	artifact, draftVersion := loadSelectedHumanDraft(t, db)
	assertArtifactJSON(t, artifact.Value, fmt.Sprintf(`{"text":%q}`, winner))
	if draftVersion != 2 {
		t.Fatalf("winning artifact draft version = %d, want 2", draftVersion)
	}
	assertSingleSelectedRevisionOne(t, db)
}

func TestPatchSlotItemByIndexHonorsBaseRevision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "draft.md"), []byte("# Draft"), 0600); err != nil {
		t.Fatal(err)
	}
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}); err != nil {
		t.Fatalf("migrate human artifacts: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{
		ID: "session-1", ConversationID: "conversation-1", WorkflowID: "writer-workflow",
		Status: SessionStatusActive, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := db.Create(&orm.WorkflowSlotRevision{
		ID: "revision-3", SessionID: "session-1", SlotID: "draft_document",
		Revision: 3, Selected: true, ChangeSource: "ai", Slot: "draft_document",
		StepID: "write_document", Attempt: 1, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create selected revision: %v", err)
	}
	for _, revisionField := range []string{"", `,"base_revision":0`} {
		req := httptest.NewRequest(
			http.MethodPatch,
			"/workflow-sessions/session-1/slots/draft_document/items/idx/-1",
			jsonBody(`{"value":{"path":"/var/lib/lazymind/uploads/draft.md"},"content_type":"file","mode":"checkpoint"`+revisionField+`}`),
		)
		req = mux.SetURLVars(req, map[string]string{
			"session_id": "session-1", "slot_id": "draft_document", "list_index": "-1",
		})
		recorder := httptest.NewRecorder()
		PatchSlotItemByIndex(recorder, req)
		if recorder.Code != http.StatusBadRequest || responseData(t, recorder)["code"] != "REVISION_REQUIRED" {
			t.Fatalf("invalid AI revision baseline %q: status=%d body=%s", revisionField, recorder.Code, recorder.Body.String())
		}
	}

	request := func(mode string, baseRevision int, baseDraftVersion *int64) *httptest.ResponseRecorder {
		draftVersionField := ""
		if baseDraftVersion != nil {
			draftVersionField = fmt.Sprintf(`,"base_draft_version":%d`, *baseDraftVersion)
		}
		req := httptest.NewRequest(
			http.MethodPatch,
			"/workflow-sessions/session-1/slots/draft_document/items/idx/-1",
			jsonBody(fmt.Sprintf(`{"value":{"path":"/var/lib/lazymind/uploads/draft.md","filename":"draft.md"},"content_type":"file","mode":%q,"base_revision":%d%s}`, mode, baseRevision, draftVersionField)),
		)
		req = mux.SetURLVars(req, map[string]string{
			"session_id": "session-1", "slot_id": "draft_document", "list_index": "-1",
		})
		rec := httptest.NewRecorder()
		PatchSlotItemByIndex(rec, req)
		return rec
	}

	if rec := request("checkpoint", 3, nil); rec.Code != http.StatusOK {
		t.Fatalf("matching revision save: got %d, body=%s", rec.Code, rec.Body.String())
	}
	draftVersion := int64(1)
	for _, mode := range []string{"draft", "checkpoint"} {
		rec := request(mode, 3, &draftVersion)
		if rec.Code != http.StatusConflict || responseData(t, rec)["code"] != "REVISION_CONFLICT" {
			t.Fatalf("stale %s revision save: got %d, body=%s", mode, rec.Code, rec.Body.String())
		}
	}

	var revisions []orm.WorkflowSlotRevision
	if err := db.Where("session_id = ? AND slot_id = ?", "session-1", "draft_document").
		Order("revision ASC").Find(&revisions).Error; err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[1].Revision != 4 || !revisions[1].Selected {
		t.Fatalf("unexpected revisions: %#v", revisions)
	}
	var artifacts []orm.WorkflowHumanArtifact
	if err := db.Find(&artifacts).Error; err != nil {
		t.Fatalf("list human artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ContentType != "file" {
		t.Fatalf("unexpected human artifacts: %#v", artifacts)
	}
}
