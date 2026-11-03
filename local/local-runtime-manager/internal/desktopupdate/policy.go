package desktopupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

const MaxEnvelopeBytes = 1024 * 1024
const Protocol = 1

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Failure struct {
	Code  string
	Cause error
}

func (e *Failure) Error() string             { return e.Code }
func (e *Failure) Unwrap() error             { return e.Cause }
func failure(code string, cause error) error { return &Failure{Code: code, Cause: cause} }
func ErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var f *Failure
	if errors.As(err, &f) {
		return f.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "network_timeout"
	}
	return "check_failed"
}

func decodeStrict(raw []byte, result any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(result); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("extra JSON value")
	}
	return nil
}

func ValidateConfig(c Config) error {
	if !identifier.MatchString(c.TrustDomain) || !identifier.MatchString(c.Audience) || !identifier.MatchString(c.ReleaseID) || c.SigningIdentity == "" || c.UpdaterProtocol != Protocol {
		return failure("configuration_invalid", nil)
	}
	if _, err := releaseversion.Compare(c.Version, c.Version); err != nil {
		return failure("configuration_invalid", err)
	}
	if !(c.Platform == "darwin" && (c.Arch == "arm64" || c.Arch == "amd64") || c.Platform == "windows" && c.Arch == "amd64") {
		return failure("configuration_invalid", nil)
	}
	if c.Mode == "official" {
		if c.Audience != "official" || !(c.Channel == "stable" || c.Channel == "beta" || c.Channel == "canary") {
			return failure("configuration_invalid", nil)
		}
	} else {
		return failure("configuration_invalid", nil)
	}
	if len(c.AllowedOrigins) == 0 || len(c.PublicKeys) == 0 {
		return failure("configuration_invalid", nil)
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" {
			return failure("configuration_invalid", nil)
		}
	}
	for keyID, encoded := range c.PublicKeys {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if !identifier.MatchString(keyID) || err != nil || len(key) != ed25519.PublicKeySize {
			return failure("configuration_invalid", err)
		}
	}
	if _, err := ResolveURL(c, c.PolicyURL); err != nil {
		return failure("configuration_invalid", err)
	}
	return nil
}

// ResolveURL is also used for every redirect/download in the coordinator.
// Downloaded content can select only origins already allowed by the build.
func ResolveURL(c Config, raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", failure("source_not_allowed", nil)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", failure("source_not_allowed", err)
	}
	if !u.IsAbs() {
		base, err := url.Parse(c.PolicyURL)
		if err != nil || !base.IsAbs() {
			return "", failure("source_not_allowed", err)
		}
		u = base.ResolveReference(u)
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Scheme != "https" {
		return "", failure("source_not_allowed", nil)
	}
	for _, origin := range c.AllowedOrigins {
		if strings.TrimSuffix(origin, "/") == u.Scheme+"://"+u.Host {
			return u.String(), nil
		}
	}
	return "", failure("source_not_allowed", nil)
}

func decodeSigned(raw []byte, c Config) (Policy, []byte, error) {
	var policy Policy
	var envelope struct {
		KeyID     string `json:"keyId"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if len(raw) > MaxEnvelopeBytes || decodeStrict(raw, &envelope) != nil {
		return policy, nil, failure("policy_invalid", nil)
	}
	key, err := base64.StdEncoding.DecodeString(c.PublicKeys[envelope.KeyID])
	if err != nil || len(key) != ed25519.PublicKeySize {
		return policy, nil, failure("policy_signature", nil)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return policy, nil, failure("policy_invalid", err)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(key, payload, signature) {
		return policy, nil, failure("policy_signature", nil)
	}
	if err := decodeStrict(payload, &policy); err != nil {
		return policy, nil, failure("policy_invalid", err)
	}
	if policy.SchemaVersion != 1 {
		return policy, nil, failure("policy_schema", nil)
	}
	if policy.TrustDomain != c.TrustDomain || policy.Audience != c.Audience || policy.Channel != c.Channel {
		return policy, nil, failure("policy_identity", nil)
	}
	if policy.Sequence == 0 || policy.Sequence > 9007199254740991 {
		return policy, nil, failure("policy_invalid", nil)
	}
	return policy, payload, nil
}

func validatePolicy(p *Policy, c Config, now time.Time) error {
	issued, e1 := time.Parse(time.RFC3339, p.IssuedAt)
	expires, e2 := time.Parse(time.RFC3339, p.ExpiresAt)
	if e1 != nil || e2 != nil || !expires.After(now) || !expires.After(issued) || issued.After(now.Add(5*time.Minute)) {
		return failure("policy_time", nil)
	}
	if p.MinUpdaterProtocol < 1 || len(p.Releases) > 512 || len(p.Edges) > 2048 {
		return failure("policy_invalid", nil)
	}
	releases := map[string]*Release{}
	for index := range p.Releases {
		r := &p.Releases[index]
		if !identifier.MatchString(r.ID) || releases[r.ID] != nil || r.SigningIdentity == "" || r.MinUpdaterProtocol < 1 || r.UpdaterProtocol < 1 {
			return failure("policy_invalid", nil)
		}
		if _, err := releaseversion.Compare(r.Version, r.Version); err != nil {
			return failure("policy_invalid", err)
		}
		if _, err := releaseversion.Compare(r.MinimumSystemVersion, r.MinimumSystemVersion); err != nil {
			return failure("policy_invalid", err)
		}
		if r.Platform == "" || r.Arch == "" {
			return failure("policy_invalid", nil)
		}
		for _, artifact := range []*Artifact{&r.Package, &r.Feed, &r.Components} {
			if !digestPattern.MatchString(artifact.SHA256) || artifact.Size <= 0 {
				return failure("policy_invalid", nil)
			}
			resolved, err := ResolveURL(c, artifact.URL)
			if err != nil {
				return err
			}
			artifact.URL = resolved
		}
		releases[r.ID] = r
	}
	if current := releases[c.ReleaseID]; current != nil && (current.Version != c.Version || current.Platform != c.Platform || current.Arch != c.Arch || current.SigningIdentity != c.SigningIdentity) {
		return failure("source_identity", nil)
	}
	seen := map[string]bool{}
	for _, edge := range p.Edges {
		source, target := releases[edge.SourceID], releases[edge.TargetID]
		if source == nil || target == nil || seen[edge.SourceID+"\x00"+edge.TargetID] || edge.MigrationProfile == "" || edge.TargetDataProfile == "" || len(edge.SourceDataProfiles) == 0 {
			return failure("policy_invalid", nil)
		}
		order, _ := releaseversion.Compare(source.Version, target.Version)
		if order >= 0 {
			return failure("policy_invalid", nil)
		}
		seen[edge.SourceID+"\x00"+edge.TargetID] = true
		if edge.MigrationProfile == "same-v1" && (len(edge.SourceDataProfiles) != 1 || edge.SourceDataProfiles[0] != edge.TargetDataProfile) {
			return failure("migration_contract_invalid", nil)
		}
		r := edge.Recovery
		if r.Version != 1 || r.Direction != "target-to-source" || r.HelperProtocolMin < 1 || r.HelperProtocolMax < r.HelperProtocolMin || r.SourcePackageSHA256 != source.Package.SHA256 || !digestPattern.MatchString(r.SourceManifestSHA256) {
			return failure("recovery_contract_invalid", nil)
		}
		for _, profile := range edge.SourceDataProfiles {
			if profile == "" {
				return failure("policy_invalid", nil)
			}
		}
		for component, identity := range edge.RequiredComponents {
			if component == "" || identity == "" {
				return failure("policy_invalid", nil)
			}
		}
	}
	return nil
}

func Check(ctx context.Context, envelope []byte, c Config, state LocalState, trustRoot string, requireExisting bool, now time.Time) (Decision, error) {
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
	digest := sha256.Sum256(payload)
	digestHex := hex.EncodeToString(digest[:])
	if err := acceptPolicy(ctx, trustRoot, c, policy.Sequence, digestHex, envelope, requireExisting); err != nil {
		return Decision{}, err
	}
	decision := decide(policy, c, state)
	decision.PolicySequence = policy.Sequence
	decision.PolicyDigest = digestHex
	return decision, nil
}
