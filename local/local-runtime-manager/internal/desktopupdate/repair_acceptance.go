package desktopupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

func CheckRepairAcceptance(ctx context.Context, envelope []byte, c Config, request []byte, root string, now time.Time) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if err := ValidateConfig(c); err != nil {
		return Decision{}, err
	}
	policy, payload, err := decodeSigned(envelope, c)
	if err != nil {
		return Decision{}, err
	}
	if err := validatePolicy(&policy, c, now); err != nil {
		return Decision{}, err
	}
	var req struct {
		SourceID     string                         `json:"sourceReleaseId"`
		FailedID     string                         `json:"failedTargetReleaseId"`
		CurrentID    string                         `json:"currentReleaseId"`
		DataProfile  string                         `json:"dataProfile"`
		SourceHadRAG bool                           `json:"sourceHadRAG"`
		Components   map[string]string              `json:"components"`
		Metadata     map[string]ComponentDescriptor `json:"componentMetadata"`
	}
	if decodeStrict(request, &req) != nil {
		return Decision{}, failure("request_invalid", nil)
	}
	blocked := Decision{Status: "blocked", Reason: "repair_target_not_accepted"}
	if c.UpdaterProtocol != 2 || req.CurrentID != c.ReleaseID || req.SourceHadRAG != (len(req.Components) != 0) {
		return blocked, nil
	}
	for _, id := range policy.Revoked {
		if id == req.SourceID || id == c.ReleaseID {
			return blocked, nil
		}
	}
	for _, acceptance := range policy.RepairAcceptances {
		if acceptance.SourceID != req.SourceID || acceptance.FailedTargetID != req.FailedID || acceptance.RepairID != c.ReleaseID || acceptance.SourceDataProfile != req.DataProfile {
			continue
		}
		if matchSourceComponents(acceptance.ComponentTransitions, LocalState{Components: req.Components, ComponentMetadata: req.Metadata}, c) != "" {
			continue
		}
		for _, target := range policy.Releases {
			if target.ID == c.ReleaseID {
				hash := sha256.Sum256(payload)
				digest := hex.EncodeToString(hash[:])
				if err := acceptPolicy(ctx, root, c, policy.Sequence, digest, envelope, false); err != nil {
					return Decision{}, err
				}
				return Decision{Status: "accepted", Next: &target, PolicyDigest: digest, PolicySequence: policy.Sequence}, nil
			}
		}
	}
	return blocked, nil
}
