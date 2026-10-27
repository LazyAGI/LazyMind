package desktopupdate

import (
	"sort"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

func channelAllows(channel, version string) bool {
	core := strings.SplitN(version, "+", 2)[0]
	_, pre, hasPre := strings.Cut(core, "-")
	if !hasPre {
		return true
	}
	switch channel {
	case "canary", "personal-test":
		return true
	case "beta":
		return strings.HasPrefix(pre, "beta.") || pre == "beta" || strings.HasPrefix(pre, "rc.") || pre == "rc"
	default:
		return false
	}
}

func decide(p Policy, c Config, state LocalState) Decision {
	result := func(status, reason string) Decision { return Decision{Status: status, Reason: reason} }
	if p.MinUpdaterProtocol > c.UpdaterProtocol {
		return result("manual_required", "updater_protocol")
	}
	revoked := map[string]bool{}
	for _, id := range p.Revoked {
		revoked[id] = true
	}
	if revoked[c.ReleaseID] {
		return result("blocked", "source_revoked")
	}
	releases := map[string]*Release{}
	for index := range p.Releases {
		releases[p.Releases[index].ID] = &p.Releases[index]
	}
	source := releases[c.ReleaseID]
	if _, err := releaseversion.Compare(state.SystemVersion, state.SystemVersion); err != nil {
		return result("blocked", "system_unknown")
	}
	var candidates []*Release
	versionOwners := map[string]string{}
	systemBlocked := false
	clientAhead, seenPlatformRelease := source == nil, false
	for index := range p.Releases {
		r := &p.Releases[index]
		if r.Platform != c.Platform || r.Arch != c.Arch || revoked[r.ID] || !channelAllows(c.Channel, r.Version) {
			continue
		}
		precedenceVersion := strings.SplitN(r.Version, "+", 2)[0]
		if owner, found := versionOwners[precedenceVersion]; found && owner != r.ID {
			return result("blocked", "version_conflict")
		}
		versionOwners[precedenceVersion] = r.ID
		order, _ := releaseversion.Compare(r.Version, c.Version)
		seenPlatformRelease = true
		if order >= 0 {
			clientAhead = false
		}
		if order == 0 && r.ID != c.ReleaseID {
			return result("blocked", "version_conflict")
		}
		if order <= 0 || r.SigningIdentity != c.SigningIdentity {
			continue
		}
		osOrder, _ := releaseversion.Compare(state.SystemVersion, r.MinimumSystemVersion)
		if osOrder < 0 {
			systemBlocked = true
			continue
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		if systemBlocked {
			return result("unsupported_system", "system_version")
		}
		if clientAhead && seenPlatformRelease {
			return result("up_to_date", "client_ahead")
		}
		return result("up_to_date", "no_newer_compatible_release")
	}
	// Migration prerequisites matter only when there is a newer candidate.
	// A development build ahead of the source must never be sent backwards to
	// an older bootstrap merely because its release ID has not been published.
	if source == nil {
		return result("manual_required", "source_not_published")
	}
	if state.Dirty {
		return result("blocked", "data_dirty")
	}
	if state.DataProfile == "" {
		return result("blocked", "data_unknown")
	}
	sort.Slice(candidates, func(i, j int) bool {
		order, _ := releaseversion.Compare(candidates[i].Version, candidates[j].Version)
		if order == 0 {
			return candidates[i].ID < candidates[j].ID
		}
		return order > 0
	})
	allowed := map[string]bool{}
	for _, r := range candidates {
		allowed[r.ID] = true
	}
	edges := append([]Edge{}, p.Edges...)
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].Priority != edges[j].Priority {
			return edges[i].Priority < edges[j].Priority
		}
		return edges[i].TargetID < edges[j].TargetID
	})
	type routeState struct {
		id, profile string
		protocol    int
		route       []string
		first       *Edge
	}
	queue := []routeState{{id: c.ReleaseID, profile: state.DataProfile, protocol: c.UpdaterProtocol}}
	visited := map[string]routeState{c.ReleaseID: queue[0]}
	unknownMigration, componentBlocked, protocolBlocked := false, false, false
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if len(node.route) >= 8 {
			continue
		}
		for index := range edges {
			e := &edges[index]
			if e.SourceID != node.id || !allowed[e.TargetID] {
				continue
			}
			if _, exists := visited[e.TargetID]; exists {
				continue
			}
			if e.MigrationProfile != "same-v1" {
				unknownMigration = true
				continue
			}
			if e.SourceDataProfiles[0] != node.profile {
				continue
			}
			next := releases[e.TargetID]
			if next.MinUpdaterProtocol > node.protocol || e.Recovery.HelperProtocolMin > node.protocol || e.Recovery.HelperProtocolMax < node.protocol {
				protocolBlocked = true
				continue
			}
			// Future hops are predictions, not installation authorizations. Their
			// actual component/data state is measured again after the next restart.
			if len(node.route) == 0 {
				match := true
				for name, identity := range e.RequiredComponents {
					if state.Components[name] != identity {
						match = false
					}
				}
				if !match {
					componentBlocked = true
					continue
				}
			}
			first := node.first
			if first == nil {
				first = e
			}
			path := append(append([]string{}, node.route...), e.TargetID)
			discovered := routeState{id: e.TargetID, profile: e.TargetDataProfile, protocol: next.UpdaterProtocol, route: path, first: first}
			visited[e.TargetID] = discovered
			queue = append(queue, discovered)
		}
	}
	for _, target := range candidates {
		if route, ok := visited[target.ID]; ok {
			status := "direct"
			if len(route.route) > 1 {
				status = "bridge"
			}
			return Decision{Status: status, Reason: "compatible", Route: route.route, Next: releases[route.route[0]], Source: source, Edge: route.first, TargetVersion: target.Version}
		}
	}
	if componentBlocked {
		return result("blocked", "component_mismatch")
	}
	if unknownMigration {
		return result("manual_required", "migration_profile")
	}
	if protocolBlocked {
		return result("manual_required", "updater_protocol")
	}
	return result("manual_required", "no_compatible_path")
}
