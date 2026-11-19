package hosted

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

type PreviewInput struct {
	ExecutionHandle string `json:"execution_handle"`
	Slot            string `json:"slot"`
	Seq             int    `json:"seq"`
	Text            string `json:"text"`
	State           string `json:"state"`
}

func (h Handler) Preview(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := owner(w, r)
	if !ok {
		return
	}
	var input PreviewInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		reply(w, 422, nil, &responseError{Code: "INVALID_EXECUTION", Message: "invalid artifact preview"})
		return
	}
	vars := mux.Vars(r)
	if err := h.Service.Preview(r.Context(), ownerID, vars["session_id"], vars["attempt_id"], input); err != nil {
		writeServiceError(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"accepted": true, "seq": input.Seq}, nil)
}

func (s *Service) Preview(ctx context.Context, owner, sessionID, attemptID string, input PreviewInput) error {
	if input.Seq < 1 || len(input.Text) > 512<<10 || !utf8.ValidString(input.Text) ||
		!slices.Contains([]string{"streaming", "ended", "aborted"}, input.State) {
		return controlstore.Reject("INVALID_EXECUTION", "preview requires positive seq, bounded UTF-8 text and state streaming, ended or aborted")
	}
	if err := s.Store.AuthorizeSession(ctx, sessionID, owner); err != nil {
		return err
	}
	// Validate and save under the same session lock as stop, resume and completion.
	return controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if !controlstore.Controlled(*session) {
			return controlstore.Reject("EXTERNAL_EXECUTION_REQUIRED", "previews require a controlled external execution")
		}
		if err := controlstore.ValidateExecution(tx, *session, attemptID, input.ExecutionHandle); err != nil {
			return err
		}
		var row orm.WorkflowSessionStep
		if err := tx.First(&row, "id = ? AND session_id = ?", attemptID, sessionID).Error; err != nil {
			return err
		}
		if row.ExecutorHost != HostName || row.LeaseOwner != executorID(owner) {
			return controlstore.Reject("EXECUTOR_MISMATCH", "the hosted controller does not own this execution")
		}
		contract, err := (executor.DBContextLoader{DB: tx}).LoadAttemptContext(ctx, attemptID)
		if err != nil {
			return err
		}
		// Paper public document slots opt in; PPT retains its existing per-page publication.
		if session.WorkflowID != "paper-writing" || contract.Execution == nil || !contract.Execution.Supported() ||
			contract.Execution.OutputValidator != graphengine.PaperAgentOutputValidator ||
			input.Slot != row.StepID+"_document" || !slices.Contains(contract.DeclaredOutputs, input.Slot) {
			return controlstore.Reject("OUTPUT_SLOT_UNDECLARED", "this execution does not declare a paper document preview")
		}
		preview := workflowstore.ArtifactPreview{AttemptID: row.ID, StepID: row.StepID, Attempt: row.Attempt,
			Slot: input.Slot, StreamID: fmt.Sprintf("%s:%d", row.ID, row.FencingGeneration),
			Seq: input.Seq, Content: input.Text, State: input.State}
		if input.State == "aborted" {
			preview.Content = ""
		}
		previous, err := workflowstore.LatestArtifactPreview(ctx, tx, sessionID, attemptID)
		if err != nil {
			return err
		}
		if previous != nil && previous.StreamID == preview.StreamID && previous.Seq >= preview.Seq {
			if *previous == preview {
				return nil
			}
			return controlstore.Reject("COMMAND_CONFLICT", "preview seq must increase; reuse a seq only with the identical snapshot")
		}
		payload, err := json.Marshal(preview)
		if err != nil {
			return err
		}
		return tx.Create(&orm.WorkflowEvent{SessionID: sessionID, OwnerUserID: owner,
			ContractVersion: ContractVersion, EventType: "artifact.preview", EntityID: attemptID,
			StateVersion: session.StateVersion, PayloadJSON: payload}).Error
	})
}
