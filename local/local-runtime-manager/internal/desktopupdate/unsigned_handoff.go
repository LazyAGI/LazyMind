//go:build darwin && lazymind_unsigned_test

package desktopupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// UnsignedHandoffRecord binds the isolated installer to the v2 state produced
// from the signed offer. No installer-supplied path or target identity is trusted.
func UnsignedHandoffRecord(c Config, p UnsignedTestPaths, caller ProcessIdentity, phase string) (*NativeRecord, error) {
	r, err := ReadNativeRecord(p.Updater)
	if err != nil || r == nil || r.Phase != phase || r.TrustDomain != c.TrustDomain || r.Audience != c.Audience ||
		r.Channel != c.Channel || r.Platform != c.Platform || r.Arch != c.Arch || r.SigningIdentity != c.SigningIdentity ||
		r.InstallationRoot != filepath.Join(p.Application, "Contents", "Resources", "runtime") || r.RuntimeRoot != p.Runtime ||
		r.SourceReleaseID != c.ReleaseID || r.SourceVersion != c.Version || r.SourceActor == nil || *r.SourceActor != caller ||
		r.RepairTarget != nil || r.HandoffActor != nil ||
		!within(filepath.Join(p.Updater, "downloads"), r.PackagePath) ||
		NoUnsignedPathAliases(r.PackagePath) != nil {
		return nil, failure("update_transaction_conflict", err)
	}
	return r, nil
}

// WithUnsignedV2Handoff serializes replacement against other native update
// transitions. The helper must finish before the target launch can acquire
// the state lock and verify its identity.
func WithUnsignedV2Handoff(ctx context.Context, c Config, p UnsignedTestPaths, caller ProcessIdentity, action func(*NativeRecord) error) error {
	lock, err := nativeStateLock(ctx, p.Updater)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	r, err := UnsignedHandoffRecord(c, p, caller, "installing")
	if err != nil {
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
