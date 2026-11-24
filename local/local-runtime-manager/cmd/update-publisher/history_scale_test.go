package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// Every weekly build has native compatibility evidence for all previous builds.
// At v20 the OS minimum increases. One component class on v0 requires v1 first.
func TestPublishDenseHistoryPreservesSupportedClientRoutes(t *testing.T) {
	records, plan, key := publisherFixture(t)
	cfg := records["r1"].Config
	cfg.UpdaterProtocol = 2
	const versions, oldOSLast = 66, 19
	releases := make([]update.Release, versions)
	for i := range releases {
		r := records["r1"].Release
		r.ID, r.Version = fmt.Sprintf("v%02d", i), fmt.Sprintf("1.%d.0", i)
		r.UpdaterProtocol, r.MinUpdaterProtocol, r.Package.UnpackedBytes = 2, 2, 100
		prefix := "https://fixture.invalid/releases/" + r.ID + "/"
		r.Package.URL, r.Feed.URL, r.Components.URL = prefix+"app.zip", prefix+"feed", prefix+"components"
		r.Components.SHA256 = fmt.Sprintf("%064x", i+1)
		r.MinimumSystemVersion = "12.0.0"
		if i > oldOSLast {
			r.MinimumSystemVersion = "14.0.0"
		}
		releases[i] = r
	}
	cfg.ReleaseID, cfg.Version = releases[0].ID, releases[0].Version
	profile := "format-v2-" + releases[0].Components.SHA256
	normal := update.ComponentSourceClass{Tag: fmt.Sprintf("%064x", 701), BaseFingerprint: fmt.Sprintf("%064x", 702),
		PythonABI: "cp311", Packages: map[string]string{"milvus-lite": "3.0"}}
	bridge := normal
	bridge.Tag = fmt.Sprintf("%064x", 703)
	edge := func(from, to update.Release, classes ...update.ComponentSourceClass) update.Edge {
		return update.Edge{SourceID: from.ID, TargetID: to.ID, Priority: 1, MigrationProfile: "format-v2",
			SourceDataProfiles: []string{profile}, TargetDataProfile: profile,
			ComponentTransitions: map[string]update.ComponentTransition{"rag": {SourceClasses: classes, TargetComponentsSHA256: to.Components.SHA256}}}
	}
	failed := releases[0]
	failed.ID, failed.Version = "withdrawn", "1.0.1"
	otherSource, otherTarget := releases[0], releases[1]
	otherSource.ID, otherSource.Version, otherSource.SigningIdentity = "other0", "2.0.0", "OTHERTEAM"
	otherTarget.ID, otherTarget.Version, otherTarget.SigningIdentity = "other1", "2.1.0", "OTHERTEAM"
	first := edge(releases[0], releases[1], normal, bridge)
	start := update.Policy{SchemaVersion: 2, MinUpdaterProtocol: 2, Sequence: 1,
		TrustDomain: cfg.TrustDomain, Audience: cfg.Audience, Channel: cfg.Channel, IssuedAt: plan.IssuedAt, ExpiresAt: plan.ExpiresAt,
		Releases: []update.Release{releases[0], releases[1], failed, otherSource, otherTarget},
		Edges:    []update.Edge{first, edge(otherSource, otherTarget, normal)}, Revoked: []string{failed.ID},
		RepairAcceptances: []update.RepairAcceptance{{SourceID: releases[0].ID, FailedTargetID: failed.ID, RepairID: releases[1].ID,
			SourceDataProfile: profile, TargetDataProfile: profile, TargetComponentsSHA256: releases[1].Components.SHA256,
			ComponentTransitions: first.ComponentTransitions}}}
	layers := make([][]update.Edge, versions)
	for target := 2; target < versions; target++ {
		for source := 0; source < target; source++ {
			layers[target] = append(layers[target], edge(releases[source], releases[target], normal))
		}
	}
	// An explicit, client-readable witness establishes that these particular
	// routes fit the existing wire limits without dropping a supported source.
	witness := start
	witness.Releases = append(witness.Releases, releases[2:]...)
	witness.Edges = append(append([]update.Edge{}, start.Edges...), layers[versions-1]...)
	witness.Edges = append(witness.Edges, layers[oldOSLast]...)
	dense := witness
	dense.Edges = append([]update.Edge{}, start.Edges...)
	for _, edges := range layers {
		dense.Edges = append(dense.Edges, edges...)
	}
	payload, err := json.Marshal(dense)
	if err != nil {
		t.Fatal(err)
	}
	if len(dense.Edges) <= 2048 || base64.StdEncoding.EncodedLen(len(payload)) <= update.MaxEnvelopeBytes {
		t.Fatal("unabridged fixture must exceed both client limits")
	}
	now := time.Now().UTC()
	if _, err := update.SignPublication(dense, cfg, "fixture", key, now); err == nil {
		t.Fatal("unmodified dense graph was accepted by the client wire contract")
	}
	expected, err := update.SignPublication(witness, cfg, "fixture", key, now)
	if err != nil {
		t.Fatalf("compact witness rejected: %v", err)
	}
	t.Logf("unabridged: %d edges, %d payload bytes before envelope; compact witness: %d edges, %d signed bytes",
		len(dense.Edges), len(payload), len(witness.Edges), len(expected))
	check := func(t *testing.T, envelope []byte, latest int) {
		t.Helper()
		trust := t.TempDir()
		oldTarget := min(latest, oldOSLast)
		assertRoute := func(source update.Release, system string, class *update.ComponentSourceClass, status string, route ...string) {
			t.Helper()
			client := cfg
			client.ReleaseID, client.Version, client.SigningIdentity = source.ID, source.Version, source.SigningIdentity
			state := update.LocalState{SystemVersion: system, DataProfile: profile}
			if class != nil {
				meta := update.ComponentDescriptor{ID: "rag", Revision: normal.Tag, SHA256: normal.Tag, Platform: cfg.Platform, Arch: cfg.Arch,
					Tag: class.Tag, PythonABI: class.PythonABI, BaseFingerprint: class.BaseFingerprint, Packages: class.Packages}
				state.Components = map[string]string{"rag": meta.Revision}
				state.ComponentMetadata = map[string]update.ComponentDescriptor{"rag": meta}
			}
			got, err := update.Check(t.Context(), envelope, client, state, trust, false, now)
			if err != nil || got.Status != status || !reflect.DeepEqual(got.Route, route) {
				t.Fatalf("client %s on OS %s with class %+v: status=%s route=%v err=%v, want %s %v", source.ID, system, class, got.Status, got.Route, err, status, route)
			}
		}
		for i, source := range releases[:latest] {
			assertRoute(source, "14.0.0", &normal, "direct", releases[latest].ID)
			if i < oldTarget {
				assertRoute(source, "12.0.0", nil, "direct", releases[oldTarget].ID)
			}
		}
		assertRoute(releases[0], "14.0.0", &bridge, "bridge", releases[1].ID, releases[latest].ID)
		assertRoute(releases[0], "12.0.0", &bridge, "bridge", releases[1].ID, releases[oldTarget].ID)
		assertRoute(releases[1], "14.0.0", &bridge, "blocked")
		assertRoute(failed, "14.0.0", nil, "blocked")
		assertRoute(otherSource, "14.0.0", &normal, "direct", otherTarget.ID)
		client := cfg
		client.ReleaseID, client.Version = releases[1].ID, releases[1].Version
		request, err := json.Marshal(map[string]any{"sourceReleaseId": releases[0].ID, "failedTargetReleaseId": failed.ID,
			"currentReleaseId": client.ReleaseID, "dataProfile": profile, "sourceHadRAG": false, "components": map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		repair, err := update.CheckRepairAcceptance(t.Context(), envelope, client, request, trust, now)
		if err != nil || repair.Status != "accepted" || repair.Next == nil || repair.Next.ID != client.ReleaseID {
			t.Fatalf("published forward repair lost: %+v, %v", repair, err)
		}
	}
	check(t, expected, versions-1)
	addition := func(i int, release update.Release) update.Policy {
		return update.Policy{SchemaVersion: 2, MinUpdaterProtocol: 2, Sequence: uint64(i), TrustDomain: cfg.TrustDomain,
			Audience: cfg.Audience, Channel: cfg.Channel, IssuedAt: plan.IssuedAt, ExpiresAt: plan.ExpiresAt,
			Releases: []update.Release{release}, Edges: layers[i]}
	}
	t.Run("different-OS-minimum-at-every-release", func(t *testing.T) {
		// Every target is the highest supported release on its minimum OS.
		// A missing edge loses a direct update for that OS and source.
		history := start
		history.Releases = append([]update.Release{}, start.Releases...)
		history.Releases[1].MinimumSystemVersion = "13.0.0"
		for i := 2; i < versions; i++ {
			release := releases[i]
			release.MinimumSystemVersion = fmt.Sprintf("%d.0.0", 12+i)
			next := addition(i, release)
			full := history
			full.Releases = append(append([]update.Release{}, history.Releases...), release)
			full.Edges = append(append([]update.Edge{}, history.Edges...), next.Edges...)
			full.Sequence = next.Sequence
			_, fullErr := update.SignPublication(full, cfg, "fixture", key, now)
			result, err := extendPublication(history, next)
			if err == nil {
				var signed []byte
				signed, err = update.SignPublication(result, cfg, "fixture", key, now)
				if err == nil {
					history, err = update.ValidatePublication(signed, cfg, now)
				}
			}
			if err != nil {
				if fullErr == nil {
					t.Fatalf("rejected representable direct routes at %s: %v", release.ID, err)
				}
				return
			}
			pairs := map[[2]string]bool{}
			for _, edge := range history.Edges {
				pairs[[2]string{edge.SourceID, edge.TargetID}] = true
			}
			for _, old := range full.Edges {
				if !pairs[[2]string{old.SourceID, old.TargetID}] {
					t.Fatalf("published without required direct edge %s -> %s", old.SourceID, old.TargetID)
				}
			}
		}
		t.Fatal("published every irreducible pair despite the deployed client limits")
	})
	history := start
	for i := 2; i < versions; i++ {
		next := addition(i, releases[i])
		before := append(append([]update.Edge{}, history.Edges...), next.Edges...)
		merged, err := extendPublication(history, next)
		if err != nil {
			t.Fatalf("extend with %s: %v", releases[i].ID, err)
		}
		signed, err := update.SignPublication(merged, cfg, "fixture", key, now)
		if err != nil {
			t.Fatalf("cannot publish %s (%d accumulated edges): %v", releases[i].ID, len(merged.Edges), err)
		}
		history, err = update.ValidatePublication(signed, cfg, now)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(history.Revoked, start.Revoked) || !reflect.DeepEqual(history.RepairAcceptances, start.RepairAcceptances) {
			t.Fatalf("publication %s changed revocations or forward-repair authorizations", releases[i].ID)
		}
		pairs := map[[2]string]bool{}
		for _, edge := range history.Edges {
			pairs[[2]string{edge.SourceID, edge.TargetID}] = true
		}
		oldTarget := min(i, oldOSLast)
		for j, source := range releases[:i] {
			if !pairs[[2]string{source.ID, releases[i].ID}] || (j < oldTarget && !pairs[[2]string{source.ID, releases[oldTarget].ID}]) {
				t.Fatalf("publication %s lost a required direct update for source %s", releases[i].ID, source.ID)
			}
		}
		if i == versions-1 || !reflect.DeepEqual(before, history.Edges) {
			check(t, signed, i)
		}
	}
}
