package desktopupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

// The receipt authorizes retaining an exact local selection. It has no
// authority to install a package or to start a v2 recovery transaction.
type V1ComponentHandoff struct {
	SourceID, SourceVersion, TargetID, TargetVersion    string
	Installation, Runtime, SourceOwner                  string
	DataProfile, PolicyURL, EdgeDigest, SelectionDigest string
	Phase                                               string           `json:"phase,omitempty"`
	AttemptID                                           string           `json:"attemptId,omitempty"`
	PreviousAttemptID                                   string           `json:"previousAttemptId,omitempty"`
	SourceActor                                         *ProcessIdentity `json:"sourceActor,omitempty"`
	SourceConfigDigest                                  string           `json:"sourceConfigDigest,omitempty"`
	// Previous contents for markers this receipt is replacing. nil means absent.
	MarkerChanges map[string]*string `json:"markerChanges,omitempty"`
}

const v1ComponentReceipt = "v1-component-handoff.json"

func ReadV1ComponentHandoff(dir string) (*V1ComponentHandoff, error) {
	var r V1ComponentHandoff
	err := readMetadata(filepath.Join(dir, v1ComponentReceipt), &r)
	if os.IsNotExist(err) {
		return nil, nil
	}
	order, versionErr := releaseversion.Compare(r.SourceVersion, r.TargetVersion)
	if err != nil || !identifier.MatchString(r.SourceID) || !identifier.MatchString(r.TargetID) ||
		versionErr != nil || order >= 0 || r.SourceID == r.TargetID || r.PolicyURL == "" || r.DataProfile == "" ||
		!digestPattern.MatchString(r.EdgeDigest) || !digestPattern.MatchString(r.SelectionDigest) ||
		!digestPattern.MatchString(r.SourceOwner) || !filepath.IsAbs(r.Runtime) || !filepath.IsAbs(r.Installation) {
		return nil, failure("update_record_read", err)
	}
	if r.PreviousAttemptID != "" && (r.Phase != "prepared" || !validID(r.PreviousAttemptID) || r.PreviousAttemptID == r.AttemptID) {
		return nil, failure("update_record_read", nil)
	}
	switch r.Phase {
	case "": // Already-issued receipts have no proof that dispatch was still pending.
		if r.AttemptID != "" || r.SourceActor != nil || r.SourceConfigDigest != "" {
			return nil, failure("update_record_read", nil)
		}
	case "prepared", "authorized":
		if !digestPattern.MatchString(r.SourceConfigDigest) || (r.Phase == "prepared" && (r.AttemptID == "" || len(r.MarkerChanges) != 0)) ||
			(r.AttemptID != "" && (!validID(r.AttemptID) || r.SourceActor == nil || r.SourceActor.PID <= 0 || r.SourceActor.StartID == 0)) {
			return nil, failure("update_record_read", nil)
		}
	default:
		return nil, failure("update_record_read", nil)
	}
	for id, prior := range r.MarkerChanges {
		if !IsUpdateComponent(id) || prior != nil && len(*prior) > 1024 {
			return nil, failure("update_record_read", nil)
		}
	}
	return &r, nil
}

func (r *V1ComponentHandoff) Save(dir string) error {
	raw, err := json.Marshal(r)
	if err == nil {
		err = atomicWrite(filepath.Join(dir, v1ComponentReceipt), raw)
	}
	if err != nil {
		return failure("update_record_write", err)
	}
	return nil
}

func ClearV1ComponentHandoff(dir string) error {
	err := os.Remove(filepath.Join(dir, v1ComponentReceipt))
	if os.IsNotExist(err) {
		return nil
	}
	if err == nil {
		err = syncDirectory(dir)
	}
	if err != nil {
		return failure("update_record_write", err)
	}
	return nil
}

func readVersionMarker(filename string) (*string, error) {
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return nil, failure("update_record_read", err)
	}
	raw, err := os.ReadFile(filename)
	if err != nil || len(raw) > 1024 {
		return nil, failure("update_record_read", err)
	}
	value := string(raw)
	return &value, nil
}

// Restore only the files this transaction claimed before writing and only
// while they still contain the target marker. Keep the receipt on any error.
func (r *V1ComponentHandoff) UndoMarkers(dir string) error {
	if len(r.MarkerChanges) == 0 {
		return nil
	}
	for id, prior := range r.MarkerChanges {
		filename := filepath.Join(dir, id+"-component-app-version")
		current, err := readVersionMarker(filename)
		if err != nil {
			return err
		}
		if current == nil || *current != r.TargetVersion+"\n" {
			continue
		}
		if prior == nil {
			err = os.Remove(filename)
			if err == nil {
				err = syncDirectory(dir)
			}
		} else {
			err = atomicWrite(filename, []byte(*prior))
		}
		if err != nil {
			return failure("update_record_write", err)
		}
	}
	r.MarkerChanges = nil
	return r.Save(dir)
}

// Discard revokes a known unsuccessful/inapplicable handoff in the runtime
// that owns it, undoing any marker changes before ordinary cleanup resumes.
func (r *V1ComponentHandoff) Discard(dir string) error {
	if err := r.UndoMarkers(dir); err != nil {
		return err
	}
	return ClearV1ComponentHandoff(dir)
}

func (r *V1ComponentHandoff) Consume(dir string, deferred []string) error {
	for _, id := range deferred {
		if !IsUpdateComponent(id) {
			return failure("component_state_unknown", nil)
		}
		filename := filepath.Join(dir, id+"-component-app-version")
		current, err := readVersionMarker(filename)
		if err != nil {
			return err
		}
		if current != nil && *current == r.TargetVersion+"\n" {
			continue
		}
		if prior, claimed := r.MarkerChanges[id]; claimed {
			if (prior == nil) != (current == nil) || prior != nil && *prior != *current {
				return failure("update_transaction_conflict", nil)
			}
		} else {
			if r.MarkerChanges == nil {
				r.MarkerChanges = make(map[string]*string)
			}
			r.MarkerChanges[id] = current
			if err := r.Save(dir); err != nil {
				return err
			}
		}
		if err := atomicWrite(filename, []byte(r.TargetVersion+"\n")); err != nil {
			return failure("update_record_write", err)
		}
	}
	return ClearV1ComponentHandoff(dir)
}

// Verify the component-preservation edge against the latest accepted ledger.
// Discovery and the native installer own the OS/version installation decision.
func (r *V1ComponentHandoff) VerifyEdge(ctx context.Context, c Config, root string, components map[string]string) (*Release, *Edge, error) {
	rejected := failure("offer_changed", nil)
	if c.UpdaterProtocol != 1 || c.PolicyURL != r.PolicyURL || (c.ReleaseID != r.SourceID && c.ReleaseID != r.TargetID) {
		return nil, nil, rejected
	}
	raw, err := ReadAcceptedEnvelope(c, root)
	if err != nil {
		return nil, nil, err
	}
	p, payload, err := decodeSigned(raw, c)
	if err != nil {
		return nil, nil, err
	}
	if err = validatePolicy(&p, c, time.Now().UTC()); err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	if err = acceptPolicy(ctx, root, c, p.Sequence, digest, raw, true); err != nil {
		return nil, nil, err
	}
	if p.MinUpdaterProtocol > 1 ||
		slices.Contains(p.Revoked, r.SourceID) || slices.Contains(p.Revoked, r.TargetID) {
		return nil, nil, rejected
	}
	var source, target *Release
	for i := range p.Releases {
		release := &p.Releases[i]
		if release.ID != r.SourceID && release.ID != r.TargetID {
			continue
		}
		if release.Platform != c.Platform || release.Arch != c.Arch || release.SigningIdentity != c.SigningIdentity ||
			release.UpdaterProtocol != 1 || release.MinUpdaterProtocol > 1 || !channelAllows(c.Channel, release.Version) {
			return nil, nil, rejected
		}
		if release.ID == r.SourceID {
			source = release
		}
		if release.ID == r.TargetID {
			target = release
		}
	}
	if source == nil || source.Version != r.SourceVersion || target == nil {
		return nil, nil, rejected
	}
	for i := range p.Edges {
		edge := &p.Edges[i]
		if edge.SourceID != r.SourceID || edge.TargetID != r.TargetID {
			continue
		}
		if edge.MigrationProfile != "same-v1" || edge.TargetDataProfile != r.DataProfile || edge.Recovery.HelperProtocolMin > 1 {
			return nil, nil, rejected
		}
		for id, revision := range edge.RequiredComponents {
			if components[id] != revision {
				return nil, nil, rejected
			}
		}
		// A signed renewal or a release on another branch may advance the
		// ledger without changing this exact authorized source, target or edge.
		facts, err := json.Marshal(struct {
			Source, Target *Release
			Edge           *Edge
		}{source, target, edge})
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(facts)
		edgeDigest := hex.EncodeToString(sum[:])
		if r.EdgeDigest != "" && r.EdgeDigest != edgeDigest {
			return nil, nil, rejected
		}
		r.EdgeDigest = edgeDigest
		return target, edge, nil
	}
	return nil, nil, rejected
}
