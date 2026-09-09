package conversationgroup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type organizerStream struct {
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
	var response struct {
		Settled bool `json:"settled"`
	}
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/organizer-executions/"+state.ExecutionID+":cancel"), map[string]any{}, nil, &response, 10*time.Second)
	return err == nil && response.Settled
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
	payload, _ := json.Marshal(map[string]any{"mode": "llm", "task_type": organizerTaskType, "input": map[string]any{"data": input}, "llm_config": config, "options": map[string]any{"timeout_seconds": 310, "max_retries": 1}})
	// No total deadline: Chat enforces first-response and meaningful-data idle deadlines.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, e := http.NewRequestWithContext(streamCtx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/organizer-executions/"+state.ExecutionID+":stream"), bytes.NewReader(payload))
	if e != nil {
		return out, e
	}
	req.Header.Set("Content-Type", "application/json")
	// This watchdog detects a broken Chat transport, not model generation time.
	watchdog := time.AfterFunc(30*time.Second, cancel)
	defer watchdog.Stop()
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		return out, e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return out, fmt.Errorf("organizer stream returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	lastSave := time.Time{}
	for {
		var event struct {
			FirstResponseAt string              `json:"first_response_at"`
			LastActivityAt  string              `json:"last_activity_at"`
			Type            string              `json:"type"`
			State           string              `json:"state"`
			ReceivedChars   int64               `json:"received_chars"`
			ElapsedSeconds  int64               `json:"elapsed_seconds"`
			IdleSeconds     int64               `json:"idle_seconds"`
			Result          organizerTaskResult `json:"result"`
		}
		if e = decoder.Decode(&event); e != nil {
			return out, e
		}
		watchdog.Reset(30 * time.Second)
		if event.Type == "result" {
			state.Settled = true
			state.State = "completed"
			if e = save(); e != nil {
				return out, e
			}
			return event.Result, nil
		}
		if event.Type != "progress" {
			continue
		}
		changed := state.State != event.State
		state.State = event.State
		state.ReceivedChars = event.ReceivedChars
		state.ElapsedSeconds = event.ElapsedSeconds
		state.IdleSeconds = event.IdleSeconds
		state.FirstResponseAt = event.FirstResponseAt
		state.LastActivityAt = event.LastActivityAt
		if changed || time.Since(lastSave) >= time.Second {
			if e = save(); e != nil {
				return out, e
			}
			lastSave = time.Now()
		}
	}
}
