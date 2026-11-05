package desktopupdate

// Offline release tooling shares the client's policy contract, without writing a trust ledger.
import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

func ValidatePublication(raw []byte, c Config, now time.Time) (Policy, error) {
	var empty Policy
	if err := ValidateConfig(c); err != nil {
		return empty, err
	}
	p, _, err := decodeSigned(raw, c)
	if err != nil {
		return empty, err
	}
	if err = validatePolicy(&p, c, now); err != nil {
		return empty, err
	}
	return p, nil
}

func SignPublication(p Policy, c Config, keyID string, key ed25519.PrivateKey, now time.Time) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, failure("publication_key", nil)
	}
	public, err := base64.StdEncoding.DecodeString(c.PublicKeys[keyID])
	if err != nil || !bytes.Equal(public, key.Public().(ed25519.PublicKey)) {
		return nil, failure("publication_key", nil)
	}
	if err = ValidateConfig(c); err != nil {
		return nil, err
	}
	if err = validatePolicy(&p, c, now); err != nil {
		return nil, err
	}
	releases := map[string]Release{}
	versions := map[string]bool{}
	for _, r := range p.Releases {
		identity := r.Platform + "/" + r.Arch + "/" + strings.SplitN(r.Version, "+", 2)[0]
		if versions[identity] || !channelAllows(c.Channel, r.Version) {
			return nil, failure("publication_release", nil)
		}
		versions[identity] = true
		releases[r.ID] = r
	}
	for _, id := range p.Revoked {
		if _, ok := releases[id]; !ok {
			return nil, failure("publication_revoked", nil)
		}
	}
	for _, edge := range p.Edges {
		source, target := releases[edge.SourceID], releases[edge.TargetID]
		if (p.SchemaVersion == 1 && edge.MigrationProfile != "same-v1" || p.SchemaVersion == 2 && edge.MigrationProfile != "format-v2") || source.Platform != target.Platform || source.Arch != target.Arch || source.SigningIdentity != target.SigningIdentity {
			return nil, failure("publication_compatibility", nil)
		}
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, failure("publication_encode", err)
	}
	raw, err := json.Marshal(map[string]string{"keyId": keyID, "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		return nil, failure("publication_encode", err)
	}
	if _, err = ValidatePublication(raw, c, now); err != nil {
		return nil, err
	}
	return raw, nil
}
