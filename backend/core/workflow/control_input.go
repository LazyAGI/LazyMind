package workflow

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
)

// The browser and every external host submit the user's answer through this
// command. It writes a human revision, never approves a review or resumes a stop.
func answerWorkflowInput(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession, command WorkflowControlCommand) error {
	answer := strings.TrimSpace(command.AnswerText)
	if answer == "" || len(answer) > 16000 || command.InputArtifactID == "" || command.InputRevision < 1 {
		return controlstore.Reject("INVALID_COMMAND", "a current question artifact and a user answer of 1–16000 bytes are required")
	}
	request, err := displaystate.PendingInput(tx, *session)
	if err != nil {
		return err
	}
	if request == nil || request.ArtifactID != command.InputArtifactID || request.Revision != command.InputRevision {
		return controlstore.Reject("STATE_VERSION_CONFLICT", "the question changed or was already answered; refresh before answering")
	}
	if err := controlstore.GuardMaterialEdit(tx, *session, request.Slot); err != nil {
		return err
	}
	request.Value["answer_text"] = answer
	request.Value["checkpoint_token"] = request.ArtifactID
	value, err := json.Marshal(map[string]any{"data": request.Value})
	if err != nil {
		return err
	}
	source := request.Source
	if _, err := writeSlotRevisionWithHumanArtifact(ctx, tx, session.ID, source.SlotID, source.Slot,
		source.StepID, source.Attempt, "single", nil, "json", value, nil, "human",
		&source.Revision, &request.DraftVersion, false, &source); err != nil {
		return err
	}
	// Material publication advanced the same locked session's event version.
	if err := tx.First(session, "id = ?", session.ID).Error; err != nil {
		return err
	}
	return controlstore.RefreshReviews(tx, session)
}
