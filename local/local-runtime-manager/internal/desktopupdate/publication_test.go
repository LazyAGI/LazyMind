package desktopupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func publicationPolicy(t *testing.T, f *policyFixture) Policy {
	t.Helper()
	raw, _ := json.Marshal(f.policy)
	var policy Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestPublicationUsesExistingClientContract(t *testing.T) {
	f := newPolicyFixture(t)
	raw, err := SignPublication(publicationPolicy(t, f), f.config, "key1", f.private, policyTime)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ValidatePublication(raw, f.config, policyTime)
	if err != nil || p.Sequence != 42 {
		t.Fatalf("policy=%+v err=%v", p, err)
	}
	entries, _ := os.ReadDir(f.root)
	if len(entries) != 0 {
		t.Fatal("offline verification wrote trust state")
	}
	decision, err := Check(context.Background(), raw, f.config, f.state, f.root, false, policyTime)
	if err != nil || decision.Status != "bridge" || decision.Next.ID != "r2" {
		t.Fatalf("%+v %v", decision, err)
	}
}

func TestPublicationRejectsUnsafePlansBeforeSigning(t *testing.T) {
	cases := map[string]func(*Policy){
		"downgrade":               func(p *Policy) { p.Releases[1].Version = "0.9.0" },
		"same version":            func(p *Policy) { p.Releases[1].Version = "1.0.0" },
		"unknown profile":         func(p *Policy) { p.Edges[0].SourceDataProfiles = nil },
		"different profile":       func(p *Policy) { p.Edges[0].TargetDataProfile = "other" },
		"unimplemented migration": func(p *Policy) { p.Edges[0].MigrationProfile = "future-v2" },
		"different platform":      func(p *Policy) { p.Releases[1].Platform = "windows" },
		"different signer":        func(p *Policy) { p.Releases[1].SigningIdentity = "other-team" },
		"bad source hash":         func(p *Policy) { p.Edges[0].Recovery.SourcePackageSHA256 = "wrong" },
		"bad manifest hash":       func(p *Policy) { p.Edges[0].Recovery.SourceManifestSHA256 = "wrong" },
		"untrusted artifact":      func(p *Policy) { p.Releases[1].Package.URL = "https://other.invalid/package" },
		"expired":                 func(p *Policy) { p.ExpiresAt = policyTime.Format("2006-01-02T15:04:05Z07:00") },
		"wrong channel":           func(p *Policy) { p.Channel = "beta" },
		"unknown revoked id":      func(p *Policy) { p.Revoked = []string{"missing"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPolicyFixture(t)
			p := publicationPolicy(t, f)
			change(&p)
			if raw, err := SignPublication(p, f.config, "key1", f.private, policyTime); err == nil || len(raw) != 0 {
				t.Fatalf("signed unsafe plan: %s %v", raw, err)
			}
		})
	}
}

func TestPublicationRejectsWrongKeysAndTampering(t *testing.T) {
	f := newPolicyFixture(t)
	p := publicationPolicy(t, f)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for _, key := range []ed25519.PrivateKey{nil, other} {
		if _, err := SignPublication(p, f.config, "key1", key, policyTime); err == nil {
			t.Fatal("accepted wrong key")
		}
	}
	if _, err := SignPublication(p, f.config, "missing", f.private, policyTime); err == nil {
		t.Fatal("accepted unknown key ID")
	}
	raw, err := SignPublication(p, f.config, "key1", f.private, policyTime)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]string
	_ = json.Unmarshal(raw, &envelope)
	p.Sequence++
	changed, _ := json.Marshal(p)
	envelope["payload"] = base64.StdEncoding.EncodeToString(changed)
	tampered, _ := json.Marshal(envelope)
	if _, err = ValidatePublication(tampered, f.config, policyTime); err == nil {
		t.Fatal("accepted tampered policy")
	}
	_ = json.Unmarshal(raw, &envelope)
	envelope["signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(other, changed))
	tampered, _ = json.Marshal(envelope)
	if _, err = ValidatePublication(tampered, f.config, policyTime); err == nil {
		t.Fatal("accepted wrong signature over a valid policy")
	}
}

func TestPublicationAllowsBootstrapWithoutGuessingUpgradeEdges(t *testing.T) {
	f := newPolicyFixture(t)
	p := publicationPolicy(t, f)
	p.Releases = p.Releases[:1]
	p.Edges = nil
	raw, err := SignPublication(p, f.config, "key1", f.private, policyTime)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := Check(context.Background(), raw, f.config, f.state, f.root, false, policyTime)
	if err != nil || decision.Status != "up_to_date" {
		t.Fatalf("%+v %v", decision, err)
	}
}
