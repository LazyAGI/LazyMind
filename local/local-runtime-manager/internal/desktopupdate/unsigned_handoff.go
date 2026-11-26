//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"
)

// UnsignedHandoffRecord binds the test installer to the v2 state produced
// from the signed offer. No installer-supplied path or target identity is trusted.
func UnsignedHandoffRecord(c Config, p UnsignedTestPaths, caller ProcessIdentity, phase string) (*NativeRecord, error) {
	r, err := ReadNativeRecord(p.Updater)
	partition := sha256.Sum256([]byte(c.TrustDomain + "\x00" + c.Audience + "\x00" + c.Channel))
	cache := filepath.Join(p.Updater, "downloads", hex.EncodeToString(partition[:]))
	if ValidateConfig(c) != nil || err != nil || r == nil || r.Phase != phase || r.TrustDomain != c.TrustDomain || r.Audience != c.Audience ||
		r.Channel != c.Channel || r.Platform != c.Platform || r.Arch != c.Arch || r.SigningIdentity != c.SigningIdentity ||
		r.InstallationRoot != filepath.Join(p.Application, "Contents", "Resources", "runtime") || r.RuntimeRoot != p.Runtime ||
		r.SourceReleaseID != c.ReleaseID || r.SourceVersion != c.Version || r.SourceActor == nil || *r.SourceActor != caller ||
		r.RepairTarget != nil || r.HandoffActor != nil ||
		!within(cache, r.PackagePath) ||
		NoUnsignedPathAliases(r.PackagePath) != nil {
		return nil, failure("update_transaction_conflict", err)
	}
	return r, nil
}

// Resolve the data root only from the matching live transaction. The source
// app is already known from the OS process. The signed ledger, not the
// receipt alone, grants permission to use this exact package and catalog.
func AcceptedUnsignedHandoff(ctx context.Context, c Config, p UnsignedTestPaths, caller ProcessIdentity, phase string) (*NativeRecord, error) {
	r, err := ReadNativeRecord(p.Updater)
	if err != nil || r == nil || NoUnsignedPathAliases(r.RuntimeRoot) != nil {
		return nil, failure("update_transaction_conflict", err)
	}
	p.Runtime = r.RuntimeRoot
	r, err = UnsignedHandoffRecord(c, p, caller, phase)
	if err != nil {
		return nil, err
	}
	if err := CheckStartup(ctx, p.Updater, r.Scope); err != nil {
		return nil, err
	}
	if err := CheckUnsignedHandoffOffer(ctx, c, p.Updater, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Recheck the accepted envelope after preparation and immediately before
// replacement. A later signed renewal must still offer the identical target.
func CheckUnsignedHandoffOffer(ctx context.Context, c Config, root string, r *NativeRecord) error {
	envelope, err := ReadAcceptedEnvelope(c, root)
	if err != nil {
		return err
	}
	osVersion, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return err
	}
	version := strings.TrimSpace(string(osVersion))
	if strings.Count(version, ".") == 1 {
		version += ".0"
	}
	components := r.SourceComponents()
	state := LocalState{SystemVersion: version, DataProfile: r.SourceDataProfile, Components: map[string]string{}, ComponentMetadata: components}
	for id, entry := range components {
		state.Components[id] = entry.Revision
	}
	decision, err := Check(ctx, envelope, c, state, root, true, time.Now().UTC())
	t := decision.Next
	if err != nil || (decision.Status != "direct" && decision.Status != "bridge") || t == nil || decision.Edge == nil ||
		decision.PolicySequence < r.PolicySequence || (decision.PolicySequence == r.PolicySequence && decision.PolicyDigest != r.PolicyDigest) || decision.Edge.TargetDataProfile != r.TargetDataProfile ||
		t.ID != r.TargetReleaseID || t.Version != r.TargetVersion || t.Package.SHA256 != r.PackageSHA256 || t.Package.Size != r.PackageSize ||
		t.Package.UnpackedBytes <= 0 || t.Package.UnpackedBytes != r.PackageUnpackedBytes || t.Components.SHA256 != r.ComponentsSHA256 || t.Components.Size != r.ComponentsSize {
		return failure("offer_changed", err)
	}
	if filepath.Dir(r.ComponentsPath) != filepath.Dir(r.PackagePath) || NoUnsignedPathAliases(r.ComponentsPath) != nil {
		return failure("update_transaction_conflict", nil)
	}
	if err := VerifyArtifact(ctx, r.ComponentsPath, t.Components); err != nil {
		return err
	}
	return nil
}

// A slow prepare cannot reopen a workspace after another process cancels the
// same pending record. Use the same lock order as the final handoff.
func WithAcceptedUnsignedPreparation(ctx context.Context, c Config, p UnsignedTestPaths, caller ProcessIdentity, expected *NativeRecord, prepare func() error) error {
	admission, err := acquireAdmission(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(admission)
	lock, err := nativeStateLock(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	r, err := UnsignedHandoffRecord(c, p, caller, "pending")
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(r, expected) {
		return failure("update_transaction_conflict", nil)
	}
	scope, err := normalizeScope(r.Scope)
	if err != nil {
		return err
	}
	if err := checkJournals(p.Updater, scope); err != nil {
		return err
	}
	if err := CheckUnsignedHandoffOffer(ctx, c, p.Updater, r); err != nil {
		return err
	}
	return prepare()
}

// WithUnsignedV2Handoff serializes replacement against other native update
// transitions. The helper must finish before the target launch can acquire
// the state lock and verify its identity.
func WithUnsignedV2Handoff(ctx context.Context, c Config, p UnsignedTestPaths, caller ProcessIdentity, action func(*NativeRecord) error) error {
	return WithAcceptedUnsignedV2Handoff(ctx, c, p, caller, nil, nil, action)
}

// expected is the record whose signed offer and paths the installer checked.
// No change between that read and the locked handoff can authorize a swap.
func WithAcceptedUnsignedV2Handoff(ctx context.Context, c Config, p UnsignedTestPaths, caller ProcessIdentity, expected *NativeRecord, beforeClaim, action func(*NativeRecord) error) error {
	admission, err := acquireAdmission(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(admission)
	lock, err := nativeStateLock(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	r, err := UnsignedHandoffRecord(c, p, caller, "installing")
	if err != nil {
		return err
	}
	if expected != nil && !reflect.DeepEqual(r, expected) {
		return failure("update_transaction_conflict", nil)
	}
	scope, err := normalizeScope(r.Scope)
	if err != nil {
		return err
	}
	if err := checkJournals(p.Updater, scope); err != nil {
		return err
	}
	component, err := NativeComponentLock(p.Runtime)
	if err != nil {
		return err
	}
	defer ReleaseNativeLock(component)
	alive, err := SameProcess(caller)
	if err != nil || !alive {
		return failure("update_transaction_conflict", err)
	}
	if beforeClaim != nil {
		if err := beforeClaim(r); err != nil {
			return err
		}
	}
	if expected != nil {
		if err := CheckUnsignedHandoffOffer(ctx, c, p.Updater, r); err != nil {
			return err
		}
	}
	actor, err := IdentifyProcess(os.Getpid())
	if err != nil {
		return err
	}
	r.HandoffActor = &actor
	write := func() error {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		return atomicWrite(NativeRecordPath(p.Updater), raw)
	}
	if err := write(); err != nil {
		return err
	}
	if err := action(r); err != nil {
		r.Phase = "repair"
		r.Reason = "native_handoff_unknown"
		_ = write()
		return err
	}
	return nil
}

// RecoverInterruptedUnsignedInstall only repairs the missing-name window on a
// filesystem without directory exchange. This is not authority to install B.
func RecoverInterruptedUnsignedInstall(ctx context.Context, c Config, p UnsignedTestPaths, system, tx string, verify func(string, string, string) error) error {
	rejected := failure("unsigned_test_recovery_unavailable", nil)
	if ValidateConfig(c) != nil || len(tx) != 32 || !digestPattern.MatchString(tx+tx) {
		return rejected
	}
	admission, err := acquireAdmission(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(admission)
	lock, err := nativeStateLock(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	r, err := ReadNativeRecord(p.Updater)
	if err != nil || r == nil || r.TransactionID != tx || (r.Phase != "installing" && r.Phase != "repair") || r.RepairTarget != nil ||
		r.TrustDomain != c.TrustDomain || r.Audience != c.Audience || r.Channel != c.Channel || r.SigningIdentity != c.SigningIdentity ||
		r.Platform != c.Platform || r.Arch != c.Arch || r.SourceReleaseID != c.ReleaseID || r.SourceVersion != c.Version || r.SourceActor == nil || r.HandoffActor == nil {
		return rejected
	}
	app := filepath.Clean(filepath.Join(r.InstallationRoot, "..", "..", ".."))
	if r.InstallationRoot != filepath.Join(app, "Contents", "Resources", "runtime") || !IsUnsignedApplication(app, p, system) {
		return rejected
	}
	if _, e := os.Lstat(app); !os.IsNotExist(e) {
		return rejected
	}
	for _, actor := range []*ProcessIdentity{r.SourceActor, r.HandoffActor} {
		deadline := time.Now().Add(2 * time.Second)
		for {
			live, e := SameProcess(*actor)
			if e != nil {
				return rejected
			}
			if !live {
				break
			}
			if time.Now().After(deadline) {
				return rejected
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Millisecond):
			}
		}
	}
	scope, err := normalizeScope(r.Scope)
	if err != nil {
		return err
	}
	if err := checkJournals(p.Updater, scope); err != nil {
		return err
	}
	// A restored source must itself still be identified by the signed ledger.
	// It need not authorize installing a successor in order to restore A.
	raw, err := ReadAcceptedEnvelope(c, p.Updater)
	if err != nil {
		return err
	}
	policy, payload, err := decodeSigned(raw, c)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if policy.Sequence < r.PolicySequence || (policy.Sequence == r.PolicySequence && hex.EncodeToString(digest[:]) != r.PolicyDigest) {
		return rejected
	}
	sourceAuthorized := false
	for _, id := range policy.Revoked {
		if id == r.SourceReleaseID {
			return rejected
		}
	}
	for _, v := range policy.Releases {
		if v.ID == r.SourceReleaseID && v.Version == r.SourceVersion && v.Platform == r.Platform && v.Arch == r.Arch && v.SigningIdentity == r.SigningIdentity {
			sourceAuthorized = true
			break
		}
	}
	if !sourceAuthorized {
		return rejected
	}
	p.Application = app
	w, err := openUnsignedWorkspace(p, tx, false)
	if err != nil {
		return err
	}
	defer w.Close()
	undo := filepath.Join(w.Directory, "undo", TestAppName+".app")
	backup := filepath.Join(p.Updater, "installs", tx, "previous", TestAppName+".app")
	info, e := os.Lstat(undo)
	if e != nil || !info.IsDir() || w.location.SourceSeal == "" {
		return rejected
	}
	stat := info.Sys().(*syscall.Stat_t)
	if w.location.SourceDevice != uint64(stat.Dev) || w.location.SourceInode != stat.Ino {
		return rejected
	}
	if err := verify(undo, backup, w.location.SourceSeal); err != nil {
		return err
	}
	if err := RestoreUnsignedWorkspaceSource(w, app); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(app)); err != nil {
		return err
	}
	r.Phase, r.Reason = "repair", "native_handoff_interrupted"
	return writeNativeRecord(p.Updater, r)
}
