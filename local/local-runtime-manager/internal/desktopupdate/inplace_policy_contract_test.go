package desktopupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

// Run both build flavors with the same release IDs, one updater directory and
// matching signing keys across two different policy envelopes. This ensures
// the channel check contributes separately from the signature check.
func TestInPlacePoliciesAreBoundToBuildChannelKeyAndRound(t *testing.T) {
	official := newV2PolicyFixture(t, false)
	personal := newV2PolicyFixture(t, false)
	personal.private = official.private
	personal.config.PublicKeys = official.config.PublicKeys
	personal.config.Mode, personal.config.Channel, personal.config.Audience = "unsigned-test", "personal-test", "fork-test"
	personal.config.TrustDomain = "inplace-417-" + strings.Repeat("a", 64)
	personal.config.SigningIdentity = "adhoc"
	base := "https://modelscope.cn/datasets/example/releases/resolve/master/updates/unsigned-test/in-place/round-417/"
	personal.config.PolicyURL = base + "policy.json"
	personal.config.AllowedOrigins = []string{"https://modelscope.cn"}
	personal.policy["trustDomain"], personal.policy["channel"], personal.policy["audience"] = personal.config.TrustDomain, personal.config.Channel, personal.config.Audience
	for _, entry := range personal.policy["releases"].([]any) {
		r := entry.(map[string]any)
		r["signingIdentity"] = personal.config.SigningIdentity
		for _, artifact := range []string{"package", "feed", "components"} {
			r[artifact].(map[string]any)["url"] = base + "releases/" + r["releaseId"].(string) + "/" + artifact
		}
	}
	selected, foreign := official, personal
	if UnsignedTestBuild {
		selected, foreign = personal, official
	}
	root := t.TempDir()
	valid := selected.envelope(t)
	if d, err := Check(t.Context(), valid, selected.config, selected.state, root, false, policyTime); err != nil || d.Status != "direct" || d.Next == nil || d.Next.ID != "r2" {
		t.Fatalf("valid signed update: %+v %v", d, err)
	}
	reject := func(t *testing.T, env []byte, c Config) {
		t.Helper()
		if d, err := Check(t.Context(), env, c, selected.state, root, false, policyTime); err == nil {
			t.Fatalf("unexpected acceptance: %+v", d)
		}
	}
	t.Run("other flavor configuration", func(t *testing.T) { reject(t, foreign.envelope(t), foreign.config) })
	t.Run("same key but foreign channel", func(t *testing.T) { reject(t, foreign.envelope(t), selected.config) })
	t.Run("foreign signer for selected channel", func(t *testing.T) {
		invalid := *selected
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		invalid.private = key
		reject(t, invalid.envelope(t), selected.config)
	})
	if UnsignedTestBuild {
		t.Run("different test round", func(t *testing.T) {
			other := *personal
			other.config.PolicyURL = strings.Replace(other.config.PolicyURL, "round-417", "round-418", 1)
			other.config.TrustDomain = "inplace-418-" + strings.Repeat("b", 64)
			reject(t, valid, other.config)
			if _, err := ReadAcceptedEnvelope(other.config, root); err == nil {
				t.Fatal("another round read the original signed policy as its own")
			}
		})
	}
	accepted, err := ReadAcceptedEnvelope(selected.config, root)
	if err != nil || !bytes.Equal(accepted, valid) {
		t.Fatalf("previous signed baseline changed: %v", err)
	}
}
