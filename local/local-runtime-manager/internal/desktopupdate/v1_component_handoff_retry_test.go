package desktopupdate

import (
	"strings"
	"testing"
	"time"
)

func TestV1HandoffTracksTheSignedEdgeAcrossPublication(t *testing.T) {
	for _, change := range []string{"renew", "another release", "source", "package", "catalog", "recovery", "required selection", "revocation"} {
		t.Run(change, func(t *testing.T) {
			f := newPolicyFixture(t)
			p := publicationPolicy(t, f)
			now := time.Now().UTC()
			p.IssuedAt, p.ExpiresAt = now.Add(-time.Minute).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)
			accept := func() {
				t.Helper()
				raw, err := SignPublication(p, f.config, "key1", f.private, now)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = Check(t.Context(), raw, f.config, f.state, f.root, false, now); err != nil {
					t.Fatal(err)
				}
			}
			accept()
			r := V1ComponentHandoff{SourceID: f.config.ReleaseID, SourceVersion: f.config.Version, TargetID: "r2", DataProfile: f.state.DataProfile, PolicyURL: f.config.PolicyURL}
			target, _, err := r.VerifyEdge(t.Context(), f.config, f.root, f.state.Components)
			if err != nil {
				t.Fatal(err)
			}
			identity := f.config
			identity.ReleaseID, identity.Version = target.ID, target.Version
			p.Sequence++
			switch change {
			case "another release":
				other := p.Releases[2]
				other.ID, other.Version = "r4", "4.0.0"
				p.Releases = append(p.Releases, other)
			case "source":
				p.Releases[0].Feed.Size++
			case "package":
				p.Releases[1].Package.Size++
			case "catalog":
				p.Releases[1].Components.SHA256 = strings.Repeat("f", 64)
			case "recovery":
				p.Edges[0].Recovery.SourceManifestSHA256 = strings.Repeat("d", 64)
			case "required selection":
				p.Edges[0].RequiredComponents = map[string]string{"rag": "replaced"}
			case "revocation":
				p.Revoked = []string{target.ID}
			}
			accept()
			_, _, err = r.VerifyEdge(t.Context(), identity, f.root, f.state.Components)
			valid := change == "renew" || change == "another release"
			if (err == nil) != valid {
				t.Fatalf("target validation after %s: %v", change, err)
			}
		})
	}
}
