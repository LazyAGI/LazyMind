//go:build !windows

package unsignedupdate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUnsignedHandoffBlocksBothStartupEntrypoints(t *testing.T) {
	i, r := fixture(t, nil)
	parent, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	id, err := i.Prepare(context.Background(), r, parent)
	if err != nil {
		t.Fatal(err)
	}
	checked, accepted := false, false
	i.SameProcess = func(desktopupdate.ProcessIdentity) (bool, error) {
		if !accepted {
			return true, nil
		}
		if !checked {
			checked = true
			scope := desktopupdate.UpdateScope{Installation: filepath.Join(i.Paths.Application, "Contents", "Resources", "runtime"), DataRoots: []string{i.Paths.Runtime}}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			if err := desktopupdate.CheckStartup(ctx, i.Paths.Updater, scope); err == nil {
				t.Error("public launcher admitted startup during handoff")
			}
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			lease, err := desktopupdate.BeginStartup(ctx, i.Paths.Updater, scope, "reopened", "coordinator", parent)
			cancel()
			if err == nil {
				lease.Close()
				t.Error("runtime admitted startup during handoff")
			}
		}
		return false, nil
	}
	if err = i.Install(context.Background(), id, parent, func() error { accepted = true; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestUnsignedStaleCallerCannotConsumePreparedHandoff(t *testing.T) {
	for _, dead := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-caller", true: "dead-coordinator"}[dead], func(t *testing.T) {
			i, r := fixture(t, nil)
			parent := desktopupdate.ProcessIdentity{PID: 123, StartID: 456}
			id, err := i.Prepare(context.Background(), r, parent)
			if err != nil {
				t.Fatal(err)
			}
			caller := parent
			if dead {
				i.SameProcess = func(desktopupdate.ProcessIdentity) (bool, error) { return false, nil }
			} else {
				caller.StartID++
			}
			if err = i.Install(context.Background(), id, caller, func() error { t.Error("stale handoff accepted"); return nil }); err == nil {
				t.Fatal("stale install succeeded")
			}
			if err = desktopupdate.CheckStartup(context.Background(), i.Paths.Updater, desktopupdate.UpdateScope{Installation: i.Paths.Application}); err != nil {
				t.Fatal("failed handoff fenced startup", err)
			}
		})
	}
}

func TestUnsignedInterruptedHandoffReleasesGateWithoutReplay(t *testing.T) {
	i, r := fixture(t, nil)
	parent := desktopupdate.ProcessIdentity{PID: 123, StartID: 456}
	id, err := i.Prepare(context.Background(), r, parent)
	if err != nil {
		t.Fatal(err)
	}
	i.SameProcess = func(desktopupdate.ProcessIdentity) (bool, error) { return true, nil }
	ctx, cancel := context.WithCancel(context.Background())
	err = i.Install(ctx, id, parent, func() error { cancel(); return nil })
	if err == nil {
		t.Fatal("interrupted helper succeeded")
	}
	scope := desktopupdate.UpdateScope{Installation: i.Paths.Application}
	if err = desktopupdate.CheckStartup(context.Background(), i.Paths.Updater, scope); err != nil {
		t.Fatal("cannot reopen for repair", err)
	}
	if err = i.Install(context.Background(), id, parent, func() error { t.Error("unknown handoff replayed"); return nil }); err == nil {
		t.Fatal("repair record replayed")
	}
}

func TestUnsignedOtherCoordinatorPreventsReplacement(t *testing.T) {
	i, r := fixture(t, nil)
	parent := desktopupdate.ProcessIdentity{PID: 123, StartID: 456}
	id, err := i.Prepare(context.Background(), r, parent)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	scope := desktopupdate.UpdateScope{Installation: filepath.Join(i.Paths.Application, "Contents", "Resources", "runtime")}
	lease, err := desktopupdate.BeginStartup(context.Background(), i.Paths.Updater, scope, "other-owner", "coordinator", actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Install(context.Background(), id, parent, func() error { t.Error("accepted while another coordinator was alive"); return nil }); err == nil {
		t.Fatal("concurrent installation succeeded")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = i.Install(context.Background(), id, parent, func() error { return nil }); err != nil {
		t.Fatal("cannot proceed after other coordinator exits", err)
	}
}
