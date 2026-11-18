package executor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

const workerPrefix = "workflow-worker:"

type workerGrant struct {
	AttemptID string `json:"attempt_id"`
	Revision  string `json:"revision"`
	Step      string `json:"step"`
}

// A worker credential is valid only for tool discovery/calls, never control or
// publication. The current lease signs it; re-claiming fences every old worker.
func workerSignature(handle, id string) string {
	mac := hmac.New(sha256.New, []byte(handle))
	_, _ = mac.Write([]byte("workflow.worker.v1:" + id))
	return hex.EncodeToString(mac.Sum(nil))
}

func GrantToolWorker(ctx context.Context, db *gorm.DB, owner, sessionID, attemptID, handle, commandID string) (string, error) {
	if strings.TrimSpace(commandID) == "" || len(commandID) > 256 {
		return "", controlstore.Reject("INVALID_WORKER_GRANT", "a stable command_id of at most 256 characters is required")
	}
	contract, err := AuthorizeExternalExecution(ctx, db, owner, sessionID, attemptID, handle)
	if err != nil {
		return "", err
	}
	id := controlstore.Hash([]byte(attemptID + ":" + commandID))
	token := workerPrefix + id + "." + workerSignature(handle, id)
	payload, _ := json.Marshal(workerGrant{attemptID, contract.WorkflowRevision, contract.StepID})
	row := orm.WorkflowCommand{CommandID: workerPrefix + id, OwnerUserID: owner, SessionID: sessionID,
		ContractVersion: "workflow.worker.v1", RequestHash: controlstore.Hash([]byte(token)),
		HTTPStatus: 200, ResponseJSON: payload, CreatedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return "", err
	}
	var previous orm.WorkflowCommand
	if err := db.WithContext(ctx).Where("command_id = ?", row.CommandID).First(&previous).Error; err != nil {
		return "", err
	}
	if previous.OwnerUserID != owner || previous.SessionID != sessionID || previous.RequestHash != row.RequestHash {
		return "", controlstore.Reject("COMMAND_CONFLICT", "worker grant command belongs to a different lease")
	}
	// Recheck after writing: revocation may race grant creation.
	if _, err := AuthorizeExternalExecution(ctx, db, owner, sessionID, attemptID, handle); err != nil {
		return "", err
	}
	return token, nil
}

// ResolveToolWorker is used only by tool endpoints; all control paths keep their
// ordinary execution-handle validation. No parent handle is sent to the worker.
func ResolveToolWorker(ctx context.Context, db *gorm.DB, owner, sessionID, attemptID, handle string) (string, error) {
	if !strings.HasPrefix(handle, workerPrefix) {
		return handle, nil
	}
	denied := func() (string, error) {
		return "", controlstore.Reject("WORKER_FENCED", "worker authorization is unavailable or expired")
	}
	id, signature, ok := strings.Cut(strings.TrimPrefix(handle, workerPrefix), ".")
	if !ok || len(id) != 64 || len(signature) != 64 {
		return denied()
	}
	var grant orm.WorkflowCommand
	if err := db.WithContext(ctx).Where("command_id = ? AND owner_user_id = ? AND session_id = ? AND contract_version = ?", workerPrefix+id, owner, sessionID, "workflow.worker.v1").First(&grant).Error; err != nil {
		return denied()
	}
	var data workerGrant
	if json.Unmarshal(grant.ResponseJSON, &data) != nil || data.AttemptID != attemptID || grant.HTTPStatus != 200 {
		return denied()
	}
	var row orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Where("id = ? AND session_id = ?", attemptID, sessionID).First(&row).Error; err != nil {
		return denied()
	}
	if row.LeaseToken == "" || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) || !hmac.Equal([]byte(signature), []byte(workerSignature(row.LeaseToken, id))) ||
		grant.RequestHash != controlstore.Hash([]byte(handle)) {
		return denied()
	}
	contract, err := AuthorizeExternalExecution(ctx, db, owner, sessionID, attemptID, row.LeaseToken)
	if err != nil {
		return "", err
	}
	if contract.WorkflowRevision != data.Revision || contract.StepID != data.Step {
		return denied()
	}
	return row.LeaseToken, nil
}
