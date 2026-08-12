package attempt

import (
	"context"
	"encoding/json"
	"errors"
)

// FakeExecutor is deterministic test infrastructure. It exercises the same
// claim/heartbeat/progress/terminal API as a real Host executor.
type FakeExecutor struct {
	Service    *Service
	ExecutorID string
}

func (f FakeExecutor) RunOne(ctx context.Context, fail bool) (Claim, error) {
	claim, err := f.Service.Claim(ctx, f.ExecutorID)
	if err != nil {
		return Claim{}, err
	}
	if _, err := f.Service.Heartbeat(ctx, claim.AttemptID, claim.LeaseToken); err != nil {
		return claim, err
	}
	if err := f.Service.Progress(ctx, claim.AttemptID, claim.LeaseToken, json.RawMessage(`{"pct":50}`)); err != nil {
		return claim, err
	}
	if fail {
		err = f.Service.Fail(ctx, claim.AttemptID, claim.LeaseToken, "FAKE_FAILURE", json.RawMessage(`{}`))
	} else {
		err = f.Service.Complete(ctx, claim.AttemptID, claim.LeaseToken, json.RawMessage(`{"ok":true}`))
	}
	return claim, err
}

func IsProtocolError(err error, code string) bool {
	return errors.Is(err, protocolError(code))
}
