package externallease

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

var ErrInvalid = errors.New("external Agent lease is invalid or expired")

type Operation string

const (
	OperationCapabilityRead  Operation = "capability.read"
	OperationInvocationWrite Operation = "invocation.write"
	OperationWorkflowRead    Operation = "workflow.read"
	OperationWorkflowWrite   Operation = "workflow.write"
)

// Request is the complete capability presented by an External Chat Agent.
// The signed-in LazyMind user still establishes the owner, while these
// run-scoped values fence the request to one claimed conversation turn.
type Request struct {
	Owner          string
	RunID          string
	LeaseToken     string
	HostID         string
	ConversationID string
	Operation      Operation
}

// ValidateRequest authorizes the narrow Core surface exposed through the
// invocation-specific LazyMind MCP server. Ordinary user requests carry no
// external capability headers and remain governed by the existing auth path.
func ValidateRequest(ctx context.Context, db *gorm.DB, request Request, now time.Time) error {
	request.Owner = strings.TrimSpace(request.Owner)
	request.RunID = strings.TrimSpace(request.RunID)
	request.LeaseToken = strings.TrimSpace(request.LeaseToken)
	request.HostID = strings.TrimSpace(request.HostID)
	request.ConversationID = strings.TrimSpace(request.ConversationID)

	presented := request.RunID != "" || request.LeaseToken != "" || request.HostID != "" || request.ConversationID != ""
	if !presented {
		return nil
	}
	if db == nil || request.Owner == "" || request.RunID == "" || request.LeaseToken == "" ||
		request.HostID == "" || request.ConversationID == "" || len(request.RunID) > 255 ||
		len(request.LeaseToken) > 64 || len(request.HostID) > 128 || len(request.ConversationID) > 36 ||
		!allowedOperation(request.Operation) {
		return ErrInvalid
	}
	var run orm.ExternalChatRun
	if err := db.WithContext(ctx).Where("id = ? AND actor_user_id = ?", request.RunID, request.Owner).Take(&run).Error; err != nil {
		return ErrInvalid
	}
	if run.Status != "running" || run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(now) ||
		!secureEqual(run.LeaseToken, request.LeaseToken) || !secureEqual(run.HostID, request.HostID) ||
		!secureEqual(run.ConversationID, request.ConversationID) {
		return ErrInvalid
	}
	return nil
}

func secureEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func allowedOperation(operation Operation) bool {
	switch operation {
	case OperationCapabilityRead, OperationInvocationWrite, OperationWorkflowRead, OperationWorkflowWrite:
		return true
	default:
		return false
	}
}
