package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowhost"
)

// dshInstalledBuild records a successful native install for integrity checks
// and restart verification. Connect resolves its source from the distribution.
type dshInstalledBuild struct {
	Profile      string `json:"profile"`
	Release      string `json:"release"`
	Build        string `json:"build"`
	RestartAfter int64  `json:"restart_after"`
}

func (a *Adapter) dshBuildPath() string {
	return filepath.Join(strings.TrimSuffix(a.dshRecordPath(), ".json"), a.dshProfile()+".build.json")
}

func (a *Adapter) readDSHBuild() (dshInstalledBuild, error) {
	path := a.dshBuildPath()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return dshInstalledBuild{}, nil
	}
	if err != nil {
		return dshInstalledBuild{}, err
	}
	var build dshInstalledBuild
	if json.Unmarshal(body, &build) != nil || build.Profile != filepath.Dir(a.dshConfigPath()) ||
		build.Release == "" || !workflowconnector.ValidDSHBuild(build.Build) || build.RestartAfter <= 0 {
		return dshInstalledBuild{}, fmt.Errorf("invalid DSH installed build record %s", path)
	}
	return build, nil
}

func (a *Adapter) lockDSHProfile(ctx context.Context) (func(), error) {
	name := a.dshProfile()
	if name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return nil, errors.New("invalid DSH profile name")
	}
	path := a.dshBuildPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return localfile.LockContext(ctx, path+".lock")
}

func (a *Adapter) saveDSHBuild(build dshInstalledBuild) error {
	body, err := json.Marshal(build)
	if err != nil {
		return err
	}
	return localfile.Write(a.dshBuildPath(), body, 0600)
}

func (r dshInstalledBuild) matchesLoad(value workflowhost.RuntimeStatus) bool {
	return value.Build == r.Build && value.Instance != "" && value.StartedAt > r.RestartAfter
}

func dshBundleEnabled(profile string) (bool, error) {
	var manifest struct {
		DSH struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	body, err := os.ReadFile(filepath.Join(profile, "package.json"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return false, err
	}
	return slices.Contains(manifest.DSH.Profile.Bundles, dshWorkflowPackage), nil
}

func (a *Adapter) checkDSHInstall(source workflowconnector.Installation) error {
	dir, err := dshModuleDir(filepath.Dir(a.dshConfigPath()), dshWorkflowPackage)
	if err != nil {
		return err
	}
	return source.CheckDSHInstall(dir)
}

func (a *Adapter) installDSHBuild(ctx context.Context, source workflowconnector.Installation) error {
	installed, err := a.readDSHBuild()
	if err != nil {
		return err
	}
	next := dshInstalledBuild{
		Profile: filepath.Dir(a.dshConfigPath()), Release: source.Release, Build: source.Version,
	}
	if err := a.checkDSHInstall(source); err != nil {
		if err := a.runNativeDSHInstall(ctx, source.EntryPath()); err != nil {
			return err
		}
		if err := a.checkDSHInstall(source); err != nil {
			return fmt.Errorf("verify native DSH installation: %w", err)
		}
		next.RestartAfter = time.Now().UnixMilli()
	} else if installed.Build != next.Build {
		// A previous attempt may have installed the build before recording it.
		next.RestartAfter = time.Now().UnixMilli()
	} else {
		next.RestartAfter = installed.RestartAfter
	}
	if next == installed {
		return nil
	}
	return a.saveDSHBuild(next)
}

func (a *Adapter) dshProfileRunning() (bool, error) {
	profile := filepath.Dir(a.dshConfigPath())
	config, err := connectorconfig.Load(connectorconfig.Path(a.home, string(DeepSeekHarness), profile))
	if err != nil {
		// The initial installation has no LazyMind dispatcher.
		return false, nil
	}
	if filepath.Dir(config.PairingFile) != filepath.Join(a.home, "workflow-hosts") || config.Profile != profile {
		return false, nil
	}
	body, err := os.ReadFile(config.PairingFile + ".runtime.lock")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var lock struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(body, &lock) != nil || lock.PID < 1 {
		return false, errors.New("invalid DSH dispatcher process record")
	}
	return !workflowhost.DSHProcessExited(lock.PID), nil
}
