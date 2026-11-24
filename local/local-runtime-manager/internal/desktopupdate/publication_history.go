package desktopupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"slices"
)

// CompactPublicationHistory is used only when extending verified v2 history.
// New evidence must reach the ordinary signer in full, including any invalid
// edge it must reject. Releases, revocations and repair contracts stay intact.
func CompactPublicationHistory(p Policy, historicalEdges int) (Policy, error) {
	payload, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	// The outer JSON contains base64 strings and an identifier of at most 128 bytes.
	const header = len(`{"keyId":"","payload":"","signature":""}`) + 128
	if len(p.Edges) <= 2048 && header+base64.StdEncoding.EncodedLen(ed25519.SignatureSize)+base64.StdEncoding.EncodedLen(len(payload)) <= MaxEnvelopeBytes {
		return p, nil
	}
	if p.SchemaVersion != 2 || p.MinUpdaterProtocol > 2 || len(p.Releases) > 512 || historicalEdges <= 0 || historicalEdges > len(p.Edges) {
		return p, nil
	}
	type platform struct{ os, arch, signer string }
	systems := map[platform]map[string]bool{}
	releases := map[string]Release{}
	for _, r := range p.Releases {
		releases[r.ID] = r
		k := platform{r.Platform, r.Arch, r.SigningIdentity}
		if systems[k] == nil {
			systems[k] = map[string]bool{}
		}
		systems[k][r.MinimumSystemVersion] = true
	}
	type sourceProfile struct{ source, profile string }
	classes := map[sourceProfile]map[string]ComponentSourceClass{}
	for _, e := range p.Edges {
		// Only v2's profile-preserving edges with the known component predicate
		// have the finite set of client input classes enumerated below.
		rag, ok := e.ComponentTransitions["rag"]
		source, target := releases[e.SourceID], releases[e.TargetID]
		if !ok || len(e.ComponentTransitions) != 1 || e.MigrationProfile != "format-v2" || len(e.RequiredComponents) != 0 ||
			len(e.SourceDataProfiles) != 1 || e.SourceDataProfiles[0] == "" || e.SourceDataProfiles[0] != e.TargetDataProfile ||
			source.ID == "" || target.ID == "" || source.Platform != target.Platform || source.Arch != target.Arch || source.SigningIdentity != target.SigningIdentity {
			return p, nil
		}
		k := sourceProfile{e.SourceID, e.TargetDataProfile}
		if classes[k] == nil {
			classes[k] = map[string]ComponentSourceClass{}
		}
		for _, class := range rag.SourceClasses {
			encoded, err := json.Marshal(class)
			if err != nil {
				return p, err
			}
			classes[k][string(encoded)] = class
		}
	}
	type observation struct {
		config         Config
		state          LocalState
		status, reason string
		route          []string
	}
	var observations []observation
	type pair struct{ from, to string }
	used := map[pair]bool{}
	// Each decide call may visit all releases and scan all edges per visit.
	// If proving equivalence would be too expensive, keep the input unchanged.
	// The signer applies the actual wire limits; no fallback weakens support.
	const maxEdgeVisits = 100_000_000
	maxObservations := maxEdgeVisits / max(1, len(p.Releases)) / max(1, len(p.Edges))
	for k, accepted := range classes {
		r := releases[k.source]
		cfg := Config{ReleaseID: r.ID, Version: r.Version, Platform: r.Platform, Arch: r.Arch,
			SigningIdentity: r.SigningIdentity, Channel: p.Channel, UpdaterProtocol: 2}
		thresholds := systems[platform{r.Platform, r.Arch, r.SigningIdentity}]
		if (len(accepted)+2)*len(thresholds) > maxObservations-len(observations) {
			return p, nil
		}
		// Valid installations without RAG match every transition. Any malformed
		// inventory or unlisted RAG class fails every first-hop component check.
		states := []LocalState{{DataProfile: k.profile}, {DataProfile: k.profile, Components: map[string]string{"rag": ""}}}
		for _, class := range accepted {
			meta := ComponentDescriptor{ID: "rag", Revision: class.Tag, SHA256: class.Tag, Tag: class.Tag, Platform: r.Platform, Arch: r.Arch,
				BaseFingerprint: class.BaseFingerprint, PythonABI: class.PythonABI, Packages: class.Packages}
			states = append(states, LocalState{DataProfile: k.profile, Components: map[string]string{"rag": meta.Revision},
				ComponentMetadata: map[string]ComponentDescriptor{"rag": meta}})
		}
		for _, state := range states {
			for version := range thresholds {
				state.SystemVersion = version
				result := decide(p, cfg, state)
				observations = append(observations, observation{cfg, state, result.Status, result.Reason, result.Route})
				from := r.ID
				for _, to := range result.Route {
					used[pair{from, to}] = true
					from = to
				}
			}
		}
	}
	reduced := p
	reduced.Edges = make([]Edge, 0, len(used)+len(p.Edges)-historicalEdges)
	for i, e := range p.Edges {
		if i >= historicalEdges || used[pair{e.SourceID, e.TargetID}] {
			reduced.Edges = append(reduced.Edges, e)
		}
	}
	if len(reduced.Edges) == len(p.Edges) {
		return p, nil
	}
	// Unknown sources, dirty/unknown data, and OS versions below the first
	// threshold have edge-independent outcomes. An unlisted nonempty profile
	// cannot traverse a v2 edge. For all remaining cases, use the real client.
	for _, before := range observations {
		after := decide(reduced, before.config, before.state)
		if after.Status != before.status || after.Reason != before.reason || !slices.Equal(after.Route, before.route) {
			return p, nil
		}
	}
	return reduced, nil
}
