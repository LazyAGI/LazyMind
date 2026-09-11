package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

type organizerStream struct {
	ErrorCode       string `json:"error_code,omitempty"`
	Retryable       *bool  `json:"retryable,omitempty"`
	FirstResponseAt string `json:"first_response_at"`
	LastActivityAt  string `json:"last_activity_at"`
	ExecutionID     string `json:"execution_id"`
	Settled         bool   `json:"settled"`
	State           string `json:"state"`
	ReceivedChars   int64  `json:"received_chars"`
	ElapsedSeconds  int64  `json:"elapsed_seconds"`
	IdleSeconds     int64  `json:"idle_seconds"`
}

var errCancellationUnconfirmed = errors.New("previous organizer execution has not confirmed termination")

// Cancellation is idempotent and fences even a POST that has not yet reached Chat.
func settleOrganizerStream(ctx context.Context, raw json.RawMessage) bool {
	var state organizerStream
	if len(raw) == 0 {
		return true
	}
	if json.Unmarshal(raw, &state) != nil {
		return false
	}
	if state.Settled || state.ExecutionID == "" {
		return true
	}
	settled, err := algo.CancelConversationGrouping(ctx, state.ExecutionID)
	return err == nil && settled
}

func callOrganizerStream(ctx context.Context, db *gorm.DB, run *orm.ConversationOrganizerRun, job asyncjob.Job, input map[string]any, config map[string]any) (out organizerTaskResult, err error) {
	// Recovery must settle the persisted previous execution before creating a new one.
	if !settleOrganizerStream(ctx, run.StreamJSON) {
		return out, errCancellationUnconfirmed
	}
	state := organizerStream{ExecutionID: uuid.NewString(), State: "waiting"}
	save := func() error {
		raw, _ := json.Marshal(state)
		if e := ownedRunUpdate(ctx, db, run.ID, job, "running", map[string]any{"stream_json": raw}); e != nil {
			return e
		}
		run.StreamJSON = raw
		return nil
	}
	if err = save(); err != nil {
		return out, err
	}
	defer func() {
		if state.Settled {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if !settleOrganizerStream(cleanup, run.StreamJSON) {
			err = errCancellationUnconfirmed
			return
		}
		state.Settled = true
		raw, _ := json.Marshal(state)
		// Never replace a newer attempt's execution checkpoint.
		db.WithContext(cleanup).Model(&orm.ConversationOrganizerRun{}).Where("id=? AND job_id=? AND CAST(stream_json AS TEXT)=?", run.ID, job.ID, string(run.StreamJSON)).Update("stream_json", raw)
		run.StreamJSON = raw
	}()
	lastSave := time.Time{}
	result, err := algo.StreamConversationGrouping(ctx, state.ExecutionID, algo.ConversationGroupingRequest{
		Input: input, LLMConfig: config,
		Options: map[string]any{"timeout_seconds": 310, "max_retries": 1,
			"execution_issued_at": float64(time.Now().UnixMilli()) / 1000},
	}, func(event algo.ConversationGroupingProgress) error {
		changed := state.State != event.State
		state.State = event.State
		state.ReceivedChars = event.ReceivedChars
		state.ElapsedSeconds = event.ElapsedSeconds
		state.IdleSeconds = event.IdleSeconds
		state.FirstResponseAt = event.FirstResponseAt
		state.LastActivityAt = event.LastActivityAt
		if changed || time.Since(lastSave) >= time.Second {
			if err := save(); err != nil {
				return err
			}
			lastSave = time.Now()
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	// A terminal frame confirms worker settlement even if its business output is invalid.
	state.Settled = true
	state.State = "completed"
	state.ErrorCode = result.ErrorCode
	state.Retryable = &result.Retryable
	if err := save(); err != nil {
		return out, err
	}
	out.Status, out.ErrorCode, out.Retryable, out.Usage = result.Status, result.ErrorCode, result.Retryable, result.Usage
	if len(result.Output) > 0 {
		err = json.Unmarshal(result.Output, &out.Output)
	}
	return out, err
}

// Persist successful settlement so later recovery decisions do not use a stale fence.
func settledOrganizerStream(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var state organizerStream
	if json.Unmarshal(raw, &state) != nil {
		return raw
	}
	state.Settled = true
	settled, _ := json.Marshal(state)
	return settled
}
