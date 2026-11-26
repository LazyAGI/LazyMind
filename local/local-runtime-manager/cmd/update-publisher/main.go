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
	"reflect"
	"slices"
	"strings"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

type record struct {
	FormatProfile       string                      `json:"formatProfile,omitempty"`
	ComponentDescriptor *update.ComponentDescriptor `json:"componentDescriptor,omitempty"`
	Config              update.Config               `json:"config"`
	Release             update.Release              `json:"release"`
	ManifestSHA256      string                      `json:"manifestSHA256"`
}
type edgePlan struct {
	ComponentTransitions map[string]update.ComponentTransition `json:"componentTransitions,omitempty"`
	FormatEvidence       string                                `json:"formatEvidence,omitempty"`
	Source               string                                `json:"sourceReleaseId"`
	Target               string                                `json:"targetReleaseId"`
	Priority             int                                   `json:"priority"`
	DataProfile          string                                `json:"dataProfile"`
	RequiredComponents   map[string]string                     `json:"requiredComponents"`
}
type plan struct {
	RepairFormatEvidence map[string]string         `json:"repairFormatEvidence,omitempty"`
	RepairAcceptances    []update.RepairAcceptance `json:"repairAcceptances,omitempty"`
	Sequence             uint64                    `json:"policySequence"`
	IssuedAt             string                    `json:"issuedAt"`
	ExpiresAt            string                    `json:"expiresAt"`
	ReleaseIDs           []string                  `json:"releaseIds"`
	Edges                []edgePlan                `json:"edges"`
	Revoked              []string                  `json:"revoked"`
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
			policy.SchemaVersion = config.UpdaterProtocol
			policy.MinUpdaterProtocol = config.UpdaterProtocol
			policy.RepairAcceptances = p.RepairAcceptances
			policy.TrustDomain = config.TrustDomain
			policy.Audience = config.Audience
			policy.Channel = config.Channel
		}
		if r.Config.TrustDomain != config.TrustDomain || r.Config.Audience != config.Audience || r.Config.Channel != config.Channel || r.Config.PolicyURL != config.PolicyURL || r.Config.UpdaterProtocol != config.UpdaterProtocol {
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
		if config.UpdaterProtocol == 2 {
			target := selected[edge.Target]
			if source.FormatProfile == "" || edge.DataProfile != source.FormatProfile || edge.DataProfile != target.FormatProfile || target.ComponentDescriptor == nil || len(edge.RequiredComponents) != 0 {
				return policy, config, errors.New("explicit same-format records are required")
			}
			if err := verifyFormatEvidence(edge.FormatEvidence, edge.Source, edge.Target, edge.DataProfile, target.Release.Components.SHA256, edge.ComponentTransitions["rag"].SourceClasses); err != nil {
				return policy, config, err
			}
			policy.Edges = append(policy.Edges, update.Edge{SourceID: edge.Source, TargetID: edge.Target, Priority: edge.Priority, MigrationProfile: "format-v2", SourceDataProfiles: []string{edge.DataProfile}, TargetDataProfile: edge.DataProfile, ComponentTransitions: edge.ComponentTransitions})
			continue
		}
		policy.Edges = append(policy.Edges, update.Edge{SourceID: edge.Source, TargetID: edge.Target, Priority: edge.Priority, MigrationProfile: "same-v1",
			SourceDataProfiles: []string{edge.DataProfile}, TargetDataProfile: edge.DataProfile, RequiredComponents: edge.RequiredComponents,
			Recovery: update.RecoveryContract{Version: 1, SourcePackageSHA256: source.Release.Package.SHA256, SourceManifestSHA256: source.ManifestSHA256, HelperProtocolMin: 1, HelperProtocolMax: 1, Direction: "target-to-source"}})
	}
	if config.UpdaterProtocol == 2 {
		for _, acceptance := range p.RepairAcceptances {
			source, ok := selected[acceptance.SourceID]
			target, ok2 := selected[acceptance.RepairID]
			if !ok || !ok2 || source.FormatProfile != acceptance.SourceDataProfile || target.FormatProfile != acceptance.TargetDataProfile {
				return policy, config, errors.New("repair requires verified same-format release records")
			}
			key := acceptance.SourceID + "/" + acceptance.FailedTargetID + "/" + acceptance.RepairID
			if err := verifyFormatEvidence(p.RepairFormatEvidence[key], acceptance.SourceID, acceptance.RepairID, acceptance.SourceDataProfile, target.Release.Components.SHA256, acceptance.ComponentTransitions["rag"].SourceClasses); err != nil {
				return policy, config, err
			}
		}
	}
	return policy, config, nil
}

func readPublication(name string, config update.Config, now time.Time, historical bool) (update.Policy, string, error) {
	var empty update.Policy
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > update.MaxEnvelopeBytes {
		return empty, "", errors.New("invalid envelope file")
	}
	file, err := os.Open(name)
	if err != nil {
		return empty, "", errors.New("cannot open signed envelope")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return empty, "", errors.New("invalid envelope file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, update.MaxEnvelopeBytes+1))
	if err != nil || len(raw) > update.MaxEnvelopeBytes {
		return empty, "", errors.New("invalid envelope file")
	}
	var envelope struct {
		KeyID   string `json:"keyId"`
		Payload string `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return empty, "", errors.New("invalid signed envelope")
	}
	if historical {
		payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
		if err != nil {
			return empty, "", errors.New("invalid historical payload")
		}
		var value struct {
			IssuedAt string `json:"issuedAt"`
		}
		if json.Unmarshal(payload, &value) != nil {
			return empty, "", errors.New("invalid historical payload")
		}
		now, err = time.Parse(time.RFC3339, value.IssuedAt)
		if err != nil {
			return empty, "", err
		}
		// This time is trusted only after the original policy signature passes validation.
	}
	policy, err := update.ValidatePublication(raw, config, now)
	return policy, envelope.KeyID, err
}

func selectPublishedSource(policy update.Policy, target update.Config) (update.Release, error) {
	var best update.Release
	if policy.MinUpdaterProtocol > target.UpdaterProtocol {
		return best, errors.New("published channel requires a newer updater protocol")
	}
	for _, release := range policy.Releases {
		if release.Platform != target.Platform || release.Arch != target.Arch || release.SigningIdentity != target.SigningIdentity ||
			release.UpdaterProtocol != target.UpdaterProtocol || slices.Contains(policy.Revoked, release.ID) {
			continue
		}
		if best.ID == "" {
			best = release
			continue
		}
		cmp, err := releaseversion.Compare(best.Version, release.Version)
		if err != nil || cmp == 0 {
			return best, errors.New("published source version is ambiguous")
		}
		if cmp < 0 {
			best = release
		}
	}
	if best.ID == "" {
		return best, errors.New("no published source matches the target platform and signing identity")
	}
	cmp, err := releaseversion.Compare(best.Version, target.Version)
	if err != nil || cmp >= 0 || best.ID == target.ReleaseID {
		return best, errors.New("target version must be newer than the active published source")
	}
	return best, nil
}

// Native verification produced these records by opening each signed source package
// on its own platform. A target's keyring cannot speak for another installed app.
func proveSharedKey(previous update.Policy, config update.Config, keyID, directory string) error {
	rejected := errors.New("shared policy key rotation requires every active source to trust the same key ID and bytes")
	expectedKey := config.PublicKeys[keyID]
	if directory == "" || expectedKey == "" {
		return rejected
	}
	missing := map[string]update.Release{}
	for _, release := range previous.Releases {
		if !slices.Contains(previous.Revoked, release.ID) {
			missing[release.ID] = release
		}
	}
	err := filepath.WalkDir(directory, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "release.json" {
			return err
		}
		var source record
		if err := readJSON(name, &source); err != nil {
			return err
		}
		prior, ok := missing[source.Release.ID]
		c := source.Config
		if !ok || source.Release != prior || c.ReleaseID != prior.ID || c.Version != prior.Version ||
			c.Platform != prior.Platform || c.Arch != prior.Arch || c.SigningIdentity != prior.SigningIdentity ||
			c.TrustDomain != config.TrustDomain || c.Channel != config.Channel || c.Audience != config.Audience ||
			c.PolicyURL != config.PolicyURL || c.Mode != config.Mode || c.PublicKeys[keyID] != expectedKey {
			return rejected
		}
		delete(missing, prior.ID)
		return nil
	})
	if err != nil || len(missing) != 0 {
		return rejected
	}
	return nil
}

func extendPublication(previous, next update.Policy) (update.Policy, error) {
	if next.SchemaVersion != 2 || previous.SchemaVersion != next.SchemaVersion || next.Sequence <= previous.Sequence {
		return next, errors.New("extension requires a newer sequence in the same v2 channel")
	}
	if len(next.Revoked) != 0 || len(next.RepairAcceptances) != 0 {
		return next, errors.New("extension cannot change revocations or repair acceptances")
	}

	published := make(map[string]update.Release, len(previous.Releases))
	platforms := make(map[string]bool)
	for _, release := range previous.Releases {
		published[release.ID] = release
		platforms[release.Platform] = true
	}
	added := make(map[string]bool, len(next.Releases))
	releases := slices.Clone(previous.Releases)
	for _, release := range next.Releases {
		if existing, found := published[release.ID]; found {
			if release != existing {
				return next, errors.New("source release record does not match published policy")
			}
			continue
		}
		added[release.ID] = true
		releases = append(releases, release)
	}
	linked := make(map[string]bool, len(added))
	for _, edge := range next.Edges {
		if _, found := published[edge.SourceID]; !found || slices.Contains(previous.Revoked, edge.SourceID) || !added[edge.TargetID] {
			return next, errors.New("extension edges must connect an active published source to a new release")
		}
		linked[edge.TargetID] = true
	}
	if len(added) == 0 {
		return next, errors.New("extension requires a new release")
	}
	for _, release := range next.Releases {
		if !added[release.ID] || linked[release.ID] {
			continue
		}
		// There is no installed source for the first official release of a new OS.
		// Subsequent releases (including after revocation) always require an edge.
		if next.Audience != "official" || platforms[release.Platform] {
			return next, errors.New("each new release on an existing platform requires a verified upgrade edge")
		}
		platforms[release.Platform] = true
	}
	next.Releases = releases
	next.Edges = append(slices.Clone(previous.Edges), next.Edges...)
	next.Revoked = previous.Revoked
	next.RepairAcceptances = previous.RepairAcceptances
	next.MinUpdaterProtocol = max(next.MinUpdaterProtocol, previous.MinUpdaterProtocol)
	return update.CompactPublicationHistory(next, len(previous.Edges))
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: update-publisher sign|verify")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	historical := flags.Bool("historical", false, "verify an existing policy at its issue time for sequence comparison only")
	selectSource := flags.Bool("select-source", false, "return the latest matching source from the verified historical policy")
	configPath := flags.String("config", "", "public configuration (verify)")
	envelopePath := flags.String("envelope", "", "signed policy (verify)")
	previousPath := flags.String("previous-envelope", "", "extend an already published signed v2 policy (sign)")
	verifiedSources := flags.String("verified-source-records", "", "records recovered by native verification of installed source packages")
	nextKey := flags.String("check-shared-key", "", "verify that all active sources accept this key (historical verify)")
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
		policy, _, err := readPublication(*envelopePath, config, now, *historical)
		if err != nil {
			return err
		}
		if *nextKey != "" {
			if !*historical || *selectSource {
				return errors.New("shared key verification requires historical policy verification")
			}
			if err := proveSharedKey(policy, config, *nextKey, *verifiedSources); err != nil {
				return err
			}
		}
		if *selectSource {
			source, err := selectPublishedSource(policy, config)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(source)
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
	for _, id := range p.ReleaseIDs {
		if !slices.Contains(policy.Revoked, id) && records[id].Config.PublicKeys[*keyID] != config.PublicKeys[*keyID] {
			return errors.New("a selected release does not trust the publication signing key")
		}
	}
	if *previousPath != "" {
		previous, priorKeyID, err := readPublication(*previousPath, config, now, true)
		if err != nil {
			return err
		}
		// Match the URLs the signer serializes, including their actual encoded size.
		for i := range policy.Releases {
			r := &policy.Releases[i]
			for _, artifact := range []*update.Artifact{&r.Package, &r.Feed, &r.Components} {
				artifact.URL, err = update.ResolveURL(config, artifact.URL)
				if err != nil {
					return err
				}
			}
		}
		policy, err = extendPublication(previous, policy)
		if err != nil {
			return err
		}
		// One shared envelope is read by installed clients on every listed
		// platform. Their original trust sets need not contain a new key ID.
		if *keyID != priorKeyID && slices.ContainsFunc(policy.Releases, func(r update.Release) bool { return r.Platform != config.Platform }) {
			if err := proveSharedKey(previous, config, *keyID, *verifiedSources); err != nil {
				return err
			}
		}
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

// The report is an offline release gate, not a remotely executable migration.
// It must come from the native old-index/new-environment verification job.
func verifyFormatEvidence(filename, source, target, profile, componentsSHA string, classes []update.ComponentSourceClass) error {
	var evidence struct {
		SourceClasses []update.ComponentSourceClass `json:"sourceClasses"`
		Source        string                        `json:"sourceReleaseId"`
		Target        string                        `json:"targetReleaseId"`
		Profile       string                        `json:"dataProfile"`
		ComponentsSHA string                        `json:"targetComponentsSHA256"`
		FixtureSHA    string                        `json:"fixtureSHA256"`
		Passed        bool                          `json:"passed"`
		ReadOnly      bool                          `json:"readOnly"`
	}
	if filename == "" || readJSON(filename, &evidence) != nil || evidence.Source != source || evidence.Target != target || evidence.Profile != profile || evidence.ComponentsSHA != componentsSHA || len(evidence.FixtureSHA) != 64 || !evidence.Passed || !evidence.ReadOnly || !reflect.DeepEqual(classes, evidence.SourceClasses) {
		return errors.New("native same-index format evidence is required before signing")
	}
	return nil
}
