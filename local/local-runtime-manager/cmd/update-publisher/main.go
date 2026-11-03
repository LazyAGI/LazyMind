// Offline policy assembly/signing and stateless verification. Never bundled into the App.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

type record struct {
	Config         update.Config  `json:"config"`
	Release        update.Release `json:"release"`
	ManifestSHA256 string         `json:"manifestSHA256"`
}
type edgePlan struct {
	Source             string            `json:"sourceReleaseId"`
	Target             string            `json:"targetReleaseId"`
	Priority           int               `json:"priority"`
	DataProfile        string            `json:"dataProfile"`
	RequiredComponents map[string]string `json:"requiredComponents"`
}
type plan struct {
	Sequence   uint64     `json:"policySequence"`
	IssuedAt   string     `json:"issuedAt"`
	ExpiresAt  string     `json:"expiresAt"`
	ReleaseIDs []string   `json:"releaseIds"`
	Edges      []edgePlan `json:"edges"`
	Revoked    []string   `json:"revoked"`
}

func readJSON(name string, value any) error {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > update.MaxEnvelopeBytes {
		return errors.New("invalid input file")
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("extra JSON value")
	}
	return nil
}
func writeNew(name string, raw []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func assemble(p plan, records map[string]record) (update.Policy, update.Config, error) {
	policy := update.Policy{SchemaVersion: 1, MinUpdaterProtocol: 1, Sequence: p.Sequence, IssuedAt: p.IssuedAt, ExpiresAt: p.ExpiresAt, Revoked: p.Revoked}
	var config update.Config
	if len(p.ReleaseIDs) == 0 {
		return policy, config, errors.New("at least one release is required")
	}
	selected := map[string]record{}
	for _, id := range p.ReleaseIDs {
		r, ok := records[id]
		if !ok {
			return policy, config, errors.New("missing release record")
		}
		if _, exists := selected[id]; exists {
			return policy, config, errors.New("duplicate release")
		}
		if err := update.ValidateConfig(r.Config); err != nil {
			return policy, config, err
		}
		if r.Release.ID != r.Config.ReleaseID || r.Release.Version != r.Config.Version || r.Release.Platform != r.Config.Platform || r.Release.Arch != r.Config.Arch || r.Release.SigningIdentity != r.Config.SigningIdentity || r.Release.UpdaterProtocol != r.Config.UpdaterProtocol {
			return policy, config, errors.New("release record identity mismatch")
		}
		if len(selected) == 0 {
			config = r.Config
			policy.TrustDomain = config.TrustDomain
			policy.Audience = config.Audience
			policy.Channel = config.Channel
		}
		if r.Config.TrustDomain != config.TrustDomain || r.Config.Audience != config.Audience || r.Config.Channel != config.Channel || r.Config.PolicyURL != config.PolicyURL {
			return policy, config, errors.New("release trust configuration mismatch")
		}
		selected[id] = r
		policy.Releases = append(policy.Releases, r.Release)
	}
	for _, edge := range p.Edges {
		source, hasSource := selected[edge.Source]
		_, hasTarget := selected[edge.Target]
		if !hasSource || !hasTarget || edge.DataProfile == "" {
			return policy, config, errors.New("edge requires known releases and an explicit compatible data profile")
		}
		policy.Edges = append(policy.Edges, update.Edge{SourceID: edge.Source, TargetID: edge.Target, Priority: edge.Priority, MigrationProfile: "same-v1",
			SourceDataProfiles: []string{edge.DataProfile}, TargetDataProfile: edge.DataProfile, RequiredComponents: edge.RequiredComponents,
			Recovery: update.RecoveryContract{Version: 1, SourcePackageSHA256: source.Release.Package.SHA256, SourceManifestSHA256: source.ManifestSHA256, HelperProtocolMin: 1, HelperProtocolMax: 1, Direction: "target-to-source"}})
	}
	return policy, config, nil
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: update-publisher sign|verify")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	historical := flags.Bool("historical", false, "verify an existing policy at its issue time for sequence comparison only")
	configPath := flags.String("config", "", "public configuration (verify)")
	envelopePath := flags.String("envelope", "", "signed policy (verify)")
	planPath := flags.String("plan", "", "explicit release/compatibility plan")
	recordsPath := flags.String("records", "", "directory containing release.json records")
	keyPath := flags.String("key-file", "", "PKCS8 Ed25519 PEM private key")
	keyID := flags.String("key-id", "", "trusted public key ID")
	output := flags.String("output", "", "new signed policy path")
	configOutput := flags.String("config-output", "", "new public verification configuration path")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	now := time.Now().UTC()
	if args[0] == "verify" {
		var config update.Config
		if err := readJSON(*configPath, &config); err != nil {
			return err
		}
		info, err := os.Stat(*envelopePath)
		if err != nil || info.Size() > update.MaxEnvelopeBytes {
			return errors.New("invalid envelope file")
		}
		raw, err := os.ReadFile(*envelopePath)
		if err != nil {
			return err
		}
		if *historical {
			var envelope struct {
				Payload string `json:"payload"`
			}
			if json.Unmarshal(raw, &envelope) != nil {
				return errors.New("invalid historical envelope")
			}
			payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
			if err != nil {
				return errors.New("invalid historical payload")
			}
			var value struct {
				IssuedAt string `json:"issuedAt"`
			}
			if json.Unmarshal(payload, &value) != nil {
				return errors.New("invalid historical payload")
			}
			now, err = time.Parse(time.RFC3339, value.IssuedAt)
			if err != nil {
				return err
			}
			// This timestamp is not trusted until the original signature and contract pass below.
		}
		policy, err := update.ValidatePublication(raw, config, now)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(policy)
	}
	if args[0] != "sign" || *recordsPath == "" || *output == "" || *configOutput == "" {
		return errors.New("missing signing arguments")
	}
	var p plan
	if err := readJSON(*planPath, &p); err != nil {
		return err
	}
	records := map[string]record{}
	if err := filepath.WalkDir(*recordsPath, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "release.json" {
			return nil
		}
		var r record
		if err := readJSON(name, &r); err != nil {
			return err
		}
		if _, exists := records[r.Release.ID]; exists {
			return errors.New("duplicate release record")
		}
		records[r.Release.ID] = r
		return nil
	}); err != nil {
		return err
	}
	policy, config, err := assemble(p, records)
	if err != nil {
		return err
	}
	keyRaw, err := os.ReadFile(*keyPath)
	if err != nil {
		return errors.New("cannot read signing key")
	}
	block, rest := pem.Decode(keyRaw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("expected one PKCS8 Ed25519 private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return errors.New("invalid PKCS8 key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return errors.New("expected Ed25519 key")
	}
	raw, err := update.SignPublication(policy, config, *keyID, key, now)
	if err != nil {
		return err
	}
	for _, id := range p.ReleaseIDs {
		if _, err = update.ValidatePublication(raw, records[id].Config, now); err != nil {
			return err
		}
	}
	public, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err = writeNew(*configOutput, public); err != nil {
		return err
	}
	return writeNew(*output, raw)
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
