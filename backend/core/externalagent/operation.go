package externalagent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
)

var ErrOperationPending = errors.New("external agent operation outcome is pending")
var ErrOperationMismatch = errors.New("external agent operation request mismatch")
var ErrInvalidOperationIdentity = errors.New("invalid external agent operation identity")

const (
	operationPending   = "pending"
	operationCompleted = "completed"
)

func (s *Service) ClaimOperation(
	ctx context.Context,
	actorUserID, operationID, kind string,
	request any,
) (json.RawMessage, bool, error) {
	actorUserID = strings.TrimSpace(actorUserID)
	operationID = strings.TrimSpace(operationID)
	kind = strings.TrimSpace(kind)
	if actorUserID == "" || operationID == "" || kind == "" ||
		len(actorUserID) > 255 || len(operationID) > 255 || len(kind) > 64 {
		return nil, false, ErrInvalidOperationIdentity
	}
	requestHash, err := operationRequestHash(request)
	if err != nil {
		return nil, false, err
	}
	now := time.Now()
	receipt := orm.ExternalAgentOperation{
		ID: uuid.NewString(), ActorUserID: actorUserID,
		OperationID: operationID, Kind: kind, RequestHash: requestHash, Status: operationPending,
		CreatedAt: now, UpdatedAt: now,
	}
	created := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
	if created.Error != nil {
		return nil, false, created.Error
	}
	if created.RowsAffected == 1 {
		return nil, true, nil
	}
	receipt = orm.ExternalAgentOperation{}
	if err := s.db.WithContext(ctx).Where(
		"actor_user_id = ? AND operation_id = ? AND kind = ?",
		actorUserID, operationID, kind,
	).First(&receipt).Error; err != nil {
		return nil, false, err
	}
	if len(receipt.RequestHash) != len(requestHash) ||
		subtle.ConstantTimeCompare([]byte(receipt.RequestHash), []byte(requestHash)) != 1 {
		return nil, false, ErrOperationMismatch
	}
	if receipt.Status != operationCompleted || len(receipt.Result) == 0 {
		return nil, false, ErrOperationPending
	}
	return append(json.RawMessage(nil), receipt.Result...), false, nil
}

func operationRequestHash(request any) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	if len(encoded) > 64*1024 {
		return "", errors.New("external agent operation request is too large")
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
}

func (s *Service) CompleteOperation(
	_ context.Context,
	actorUserID, operationID, kind string,
	value any,
) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > 64*1024 {
		return errors.New("external agent operation result is too large")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := s.db.WithContext(ctx).Model(&orm.ExternalAgentOperation{}).
		Where(
			"actor_user_id = ? AND operation_id = ? AND kind = ? AND status = ?",
			actorUserID, operationID, kind, operationPending,
		).
		Updates(map[string]any{
			"status":     operationCompleted,
			"result":     json.RawMessage(encoded),
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
