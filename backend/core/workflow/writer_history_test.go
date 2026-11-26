package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestWriterFinalVersionTimeline(t *testing.T) {
	for _, slot := range []string{"flat_draft_document", "draft_document"} {
		t.Run(slot, func(t *testing.T) {
			db := seedSelectedHumanDraft(t)
			ok := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			ok(db.AutoMigrate(&orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}))
			ok(db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-draft").Update("create_user_id", "owner").Error)
			ok(db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "revision-draft").Updates(map[string]any{"slot_id": slot, "change_source": "host"}).Error)
			original := json.RawMessage(`{"schema":"text/markdown","data":"# Original\n\n![Illustration](image.png)"}`)
			edited := json.RawMessage(`{"schema":"text/markdown","data":"# Edited\n\n![Illustration](image.png)"}`)
			if slot == "draft_document" {
				original = json.RawMessage(`{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"w","blocks":[{"type":"paragraph","text":"original"},{"type":"image","src":"image.png","width":420}]}}`)
				edited = json.RawMessage(`{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"w","blocks":[{"type":"image","src":"image.png","width":320},{"type":"paragraph","text":"edit"}]}}`)
			}
			ok(db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "human-draft").Updates(map[string]any{"content_type": "json", "value": original}).Error)
			var selected orm.WorkflowSlotRevision
			ok(db.First(&selected, "id = ?", "revision-draft").Error)
			version := int64(1)
			save := func(value json.RawMessage, draft bool) {
				t.Helper()
				row, v, _, err := SaveHumanArtifactValue(t.Context(), db.DB, selected.SessionID, slot, slot, selected.StepID, selected.Attempt, "single", nil, "json", value, nil, &selected.Revision, &version, draft)
				ok(err)
				selected = *row
				version = v
			}
			save(edited, true)
			save(original, false)
			if selected.ID != "revision-draft" {
				t.Fatal("undo created another version")
			}
			save(edited, true)
			save(edited, false)
			if selected.ChangeSource != "writer_edit" {
				t.Fatal("edit was not checkpointed")
			}
			published := selected
			save(edited, false)
			if selected.ID != published.ID {
				t.Fatal("unchanged checkpoint duplicated")
			}
			group := "group"
			if slot == "draft_document" {
				group = ""
			}
			op, err := PrepareDocumentPublication(t.Context(), db.DB, DocumentPublicationInput{PublicationID: group, OwnerUserID: "owner", SessionID: selected.SessionID, SlotID: slot, BaseRevision: selected.Revision, BaseDraftVersion: &version, Provider: "feishu", IdempotencyKey: "edit-to-feishu"})
			ok(err)
			ok(ClaimDocumentPublicationWrite(t.Context(), db.DB, "owner", op.ID))
			// The user edits again while the provider is writing the submitted snapshot.
			save(original, false)
			receipt := DocumentPublicationReceipt{Provider: "feishu", TargetDocument: json.RawMessage(`{"adapter":"feishu","uri":"https://example.feishu.cn/docx/fixture"}`), ContentType: "json", Value: edited}
			ok(ConfirmDocumentPublication(t.Context(), db.DB, "owner", op.ID, receipt))
			_, err = FinalizeDocumentPublication(t.Context(), db.DB, "owner", op.ID)
			ok(err)
			if group == "" {
				for _, row := range []orm.WorkflowSlotRevision{published, selected} {
					state := writerWriteBackState(t.Context(), db.DB, row.SessionID, slotDTO{SlotID: slot, ArtifactID: row.ID, Revision: row.Revision, HumanArtifactID: row.HumanArtifactID}, nil, nil)
					want := writerWriteBackSyncedDirty
					if row.ID == published.ID {
						want = writerWriteBackSyncedClean
					}
					if state.State != want || state.LastSyncedRevision == nil || *state.LastSyncedRevision != published.Revision {
						t.Fatalf("write-back state for %s: %+v, want %s", row.ID, state, want)
					}
				}
			}
			restored, err := RollbackSlotRevision(t.Context(), db.DB, selected.SessionID, slot, nil, published.Revision, slot)
			ok(err)
			if restored.Revision <= selected.Revision || restored.RestoredFromRevision == nil || *restored.RestoredFromRevision != published.Revision {
				t.Fatalf("invalid restore: %+v", restored)
			}
			raw, err := LoadSlotRevisionValue(t.Context(), db.DB, *restored)
			ok(err)
			assertArtifactJSON(t, raw, string(edited))
			// Read historical receipts, including a repeat publish and a failed platform.
			addReceipt := func(id, provider, status, target string, at time.Time) {
				t.Helper()
				raw, err := json.Marshal(DocumentPublicationReceipt{Provider: provider, TargetDocument: json.RawMessage(target)})
				ok(err)
				ok(db.Create(&orm.DocumentPublicationOperation{
					ID: id, IdempotencyKey: id, OwnerUserID: "owner", SessionID: published.SessionID,
					SlotID: slot, ItemIndex: -1, SourceRevisionID: published.ID, Provider: provider,
					Status: status, ReceiptJSON: raw, UpdatedAt: at,
				}).Error)
			}
			now := time.Now().UTC()
			addReceipt("notion-first", "notion", "succeeded", `{"uri":"https://www.notion.so/old"}`, now.Add(-time.Minute))
			addReceipt("notion-second", "notion", "succeeded", `{"uri":"https://www.notion.so/new"}`, now)
			addReceipt("github-failed", "github", "failed", `{}`, now)
			req := httptest.NewRequest(http.MethodGet, "/versions", nil)
			req.Header.Set("X-User-Id", "owner")
			req = mux.SetURLVars(req, map[string]string{"session_id": selected.SessionID, "slot_id": slot, "list_index": "-1"})
			rec := httptest.NewRecorder()
			GetSlotItemVersionsByIndex(rec, req)
			var history struct {
				Data struct {
					Versions []struct {
						ID        string `json:"artifact_id"`
						Version   int
						From      int `json:"restored_from_version"`
						Platforms map[string]struct {
							URL string `json:"target_url"`
							At  string `json:"published_at"`
						} `json:"publications"`
						Published *struct {
							URL string `json:"target_url"`
							At  string `json:"published_at"`
						} `json:"feishu_publication"`
					}
				}
			}
			ok(json.Unmarshal(rec.Body.Bytes(), &history))
			rows := history.Data.Versions
			if len(rows) != 4 || rows[1].ID != published.ID || rows[1].Published == nil || rows[1].Published.URL != "https://example.feishu.cn/docx/fixture" || rows[1].Published.At == "" || rows[2].Published != nil || rows[3].Published != nil || rows[3].From != 2 || rows[3].Version != 4 {
				t.Fatalf("unexpected history %s", rec.Body.String())
			}
			for index, row := range rows {
				if index != 1 && len(row.Platforms) != 0 {
					t.Fatalf("version %d inherited publication markers: %+v", row.Version, row.Platforms)
				}
			}
			platforms := rows[1].Platforms
			if len(platforms) != 2 || platforms["feishu"].URL != rows[1].Published.URL || platforms["notion"].URL != "https://www.notion.so/new" || platforms["notion"].At == "" {
				t.Fatalf("incorrect platform markers: %+v", platforms)
			}

		})
	}
}
