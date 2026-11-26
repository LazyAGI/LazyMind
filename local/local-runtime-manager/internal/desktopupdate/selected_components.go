package desktopupdate

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// SelectedComponents contains user intent, not executable paths or artifact
// addresses. The verified target release and its packaged catalog own those.
type SelectedComponents struct {
	ReleaseID          string            `json:"releaseId"`
	ComponentsSHA256   string            `json:"componentsSHA256"`
	TransactionID      string            `json:"transactionId"`
	Wanted             []string          `json:"wanted"`
	SourceRevisions    map[string]string `json:"sourceRevisions,omitempty"`
	VerifiedComponents map[string]string `json:"verifiedComponents,omitempty"`
}

func sortedComponentIDs(ids []string) []string {
	ordered := slices.Clone(ids)
	slices.Sort(ordered)
	return ordered
}

func validWantedComponents(ids []string) bool {
	if len(ids) > len(UpdateComponentIDs()) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !IsUpdateComponent(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (s *SelectedComponents) valid() bool {
	for id, revision := range s.SourceRevisions {
		if !slices.Contains(s.Wanted, id) || !digestPattern.MatchString(revision) {
			return false
		}
	}
	for id, revision := range s.VerifiedComponents {
		if !slices.Contains(s.Wanted, id) || !digestPattern.MatchString(revision) {
			return false
		}
	}
	return identifier.MatchString(s.ReleaseID) && digestPattern.MatchString(s.ComponentsSHA256) && validID(s.TransactionID) && validWantedComponents(s.Wanted)
}

func selectedComponentsPath(root string) string {
	return filepath.Join(root, "deps", "python-components", "selected-update.json")
}

func ReadSelectedComponents(root string) (*SelectedComponents, error) {
	if !filepath.IsAbs(root) {
		return nil, failure("component_state_unknown", nil)
	}
	real, err := filepath.EvalSymlinks(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, failure("component_state_unknown", err)
	}
	path := selectedComponentsPath(real)
	if NoUnsignedPathAliases(path) != nil {
		return nil, failure("component_state_unknown", nil)
	}
	var state SelectedComponents
	err = readMetadata(path, &state)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !state.valid() {
		return nil, failure("component_state_unknown", err)
	}
	slices.Sort(state.Wanted)
	return &state, nil
}

// Caller holds the component writer lock.
func SaveSelectedComponents(root string, s SelectedComponents) error {
	if !s.valid() || !filepath.IsAbs(root) {
		return failure("component_state_unknown", nil)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	file := selectedComponentsPath(real)
	if err := NoUnsignedPathAliases(file); err != nil {
		return err
	}
	if err := ensureDirectory(filepath.Dir(file)); err != nil {
		return err
	}
	s.Wanted = sortedComponentIDs(s.Wanted)
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWrite(file, raw)
}

type ComponentProof struct {
	Components            map[string]string `json:"components"`
	PendingComponents     map[string]string `json:"pendingComponents"`
	UnavailableComponents []string          `json:"unavailableComponents"`
}

func NativeComponentProof(r *NativeRecord) (ComponentProof, error) {
	result := ComponentProof{Components: map[string]string{}, PendingComponents: map[string]string{}, UnavailableComponents: []string{}}
	targets := r.EffectiveComponents()
	if len(r.WantedComponents) == 0 {
		if err := verifyNativeTargetSelections(r.RuntimeRoot, targets); err != nil {
			return result, err
		}
		for id, d := range targets {
			result.Components[id] = d.Revision
		}
		return result, nil
	}
	release, _, _, _ := r.EffectiveTarget()
	digest := r.ComponentsSHA256
	if r.RepairTarget != nil {
		digest = r.RepairTarget.ComponentsSHA256
	}
	s, err := ReadSelectedComponents(r.RuntimeRoot)
	if err != nil || s == nil || s.ReleaseID != release || s.ComponentsSHA256 != digest || s.TransactionID != r.TransactionID || !slices.Equal(s.Wanted, sortedComponentIDs(r.WantedComponents)) {
		return result, failure("component_state_unknown", err)
	}
	for _, id := range s.Wanted {
		d, ok := targets[id]
		switch {
		case !ok:
			result.UnavailableComponents = append(result.UnavailableComponents, id)
		case s.VerifiedComponents[id] == d.Revision && verifyNativeTargetSelection(r.RuntimeRoot, d) == nil:
			result.Components[id] = d.Revision
		default:
			result.PendingComponents[id] = d.Revision
		}
	}
	return result, nil
}

func (p ComponentProof) matches(active, pending map[string]string, unavailable []string) bool {
	if !slices.Equal(p.UnavailableComponents, sortedComponentIDs(unavailable)) {
		return false
	}
	// A verified pending->active transition between verify and complete is
	// monotonic. An unverified payload can never satisfy an active claim.
	latest := maps.Clone(p.Components)
	for id, revision := range pending {
		if _, both := active[id]; both {
			return false
		}
		if current, ok := latest[id]; ok && current == revision {
			delete(latest, id)
			continue
		}
		if current, ok := p.PendingComponents[id]; !ok || current != revision {
			return false
		}
	}
	return maps.Equal(active, latest) && len(active)+len(pending) == len(p.Components)+len(p.PendingComponents)
}
