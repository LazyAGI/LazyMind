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

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowhost"
)

type dshPluginRelease struct {
	Release string `json:"release,omitempty"`
	Version string `json:"version,omitempty"`
}

type dshPluginRecord struct {
	Profile      string           `json:"profile"`
	Selected     dshPluginRelease `json:"selected"`
	Installed    dshPluginRelease `json:"installed"`
	Previous     dshPluginRelease `json:"previous"`
	Action       string           `json:"action,omitempty"`
	Step         string           `json:"step,omitempty"`
	RestartAfter int64            `json:"restart_after,omitempty"`
	KeepEnabled  *bool            `json:"keep_enabled,omitempty"`
}

func (a *Adapter) dshPluginRecordPath() string {
	return filepath.Join(strings.TrimSuffix(a.dshRecordPath(), ".json"), a.dshProfile()+".json")
}

func (a *Adapter) dshPluginRecord() (dshPluginRecord, error) {
	profile := filepath.Dir(a.dshConfigPath())
	record := dshPluginRecord{Profile: profile}
	path := a.dshPluginRecordPath()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(body, &record); err != nil || record.Profile != profile || record.Selected.Release == "" {
		return dshPluginRecord{}, fmt.Errorf("invalid DSH plugin installation record %s", path)
	}
	return record, nil
}

func (a *Adapter) lockDSHPlugin(ctx context.Context) (func(), error) {
	name := a.dshProfile()
	if name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return nil, errors.New("invalid DSH profile name")
	}
	path := a.dshPluginRecordPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return localfile.LockContext(ctx, path+".lock")
}

func (a *Adapter) saveDSHPlugin(record dshPluginRecord) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return localfile.Write(a.dshPluginRecordPath(), body, 0600)
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

func (a *Adapter) checkDSHPlugin(release string) (workflowconnector.Installation, error) {
	packageDir, err := dshModuleDir(filepath.Dir(a.dshConfigPath()), dshWorkflowPackage)
	if err != nil {
		return workflowconnector.Installation{}, err
	}
	target, err := workflowconnector.DSHRelease(context.Background(), a.home, "", "", release)
	if err != nil {
		return target, err
	}
	return target, target.CheckDSHInstall(packageDir)
}

func (a *Adapter) selectDSHPlugin(ctx context.Context, action, directory string) (dshPluginRecord, workflowconnector.Installation, error) {
	record, err := a.dshPluginRecord()
	if err != nil {
		return record, workflowconnector.Installation{}, err
	}
	release := ""
	switch {
	case action == "connect", record.Action == action && record.Step != "restart" && directory == "":
		release = record.Selected.Release
	case action == "rollback":
		release = record.Previous.Release
		if release == "" {
			return record, workflowconnector.Installation{}, errors.New("no previous DSH Connector build is available for this profile")
		}
	}
	packageSource, err := workflowconnector.DSHRelease(ctx, a.home, a.diagnosticExecutable(), directory, release)
	if err != nil {
		return record, packageSource, err
	}
	selected := dshPluginRelease{Release: packageSource.Release, Version: packageSource.Version}
	if record.Selected == selected && record.Step != "restart" && (action == "connect" || record.Action == action) {
		return record, packageSource, nil
	}
	if record.Installed != selected || action == "rollback" && record.Action != action {
		if record.Installed.Release != "" && action != "rollback" {
			if _, err := a.checkDSHPlugin(record.Installed.Release); err == nil {
				record.Previous = record.Installed
			}
		}
		record.Step = "install"
	}
	if action != "connect" {
		enabled, err := dshBundleEnabled(record.Profile)
		if err != nil {
			return record, packageSource, err
		}
		record.KeepEnabled = &enabled
	}
	record.Selected = selected
	record.Action = action
	if err := a.saveDSHPlugin(record); err != nil {
		return record, packageSource, err
	}
	return record, packageSource, nil
}

func (a *Adapter) installDSHPlugin(ctx context.Context, action, directory string) (dshPluginRecord, error) {
	record, source, err := a.selectDSHPlugin(ctx, action, directory)
	if err != nil {
		return record, err
	}
	_, installErr := a.checkDSHPlugin(record.Selected.Release)
	if installErr != nil || record.Action == "rollback" && record.Step == "install" {
		if err := a.runNativeDSHInstall(ctx, source.EntryPath()); err != nil {
			return record, err
		}
		if _, err := a.checkDSHPlugin(record.Selected.Release); err != nil {
			return record, fmt.Errorf("verify native DSH installation: %w", err)
		}
		record.RestartAfter = time.Now().UnixMilli()
	} else if record.Installed != record.Selected {
		// An interrupted operation might have installed the package before it
		// could update the local record. Require a fresh process on recovery.
		record.RestartAfter = time.Now().UnixMilli()
	}
	record.Installed = record.Selected
	if record.Step != "restart" {
		record.Step = "configuration"
	}
	if err := a.saveDSHPlugin(record); err != nil {
		return record, err
	}
	if record.KeepEnabled != nil {
		if err := setDSHWorkflowBundle(ctx, record.Profile, *record.KeepEnabled); err != nil {
			return record, err
		}
	}
	return record, nil
}

func (r dshPluginRecord) matchesLoad(value workflowhost.RuntimeStatus) bool {
	return r.Installed == r.Selected && r.Step == "restart" && value.Build == r.Installed.Version &&
		value.Instance != "" && r.RestartAfter > 0 && value.StartedAt > r.RestartAfter
}

func (a *Adapter) dshPluginStatus() *agentintegration.PluginStatus {
	record, err := a.dshPluginRecord()
	if err != nil {
		return &agentintegration.PluginStatus{State: "record_invalid"}
	}
	if record.Selected.Release == "" {
		return nil
	}
	status := &agentintegration.PluginStatus{
		SelectedVersion: record.Selected.Version, InstalledVersion: record.Installed.Version,
		PreviousVersion: record.Previous.Version, State: record.Step,
	}
	if record.Step == "restart" {
		status.State = "restart_required"
	}
	doc, err := readYAMLDocument(a.dshConfigPath())
	if err != nil {
		return status
	}
	var reference string
	for _, row := range yamlSequence(doc).Content {
		if scalarValue(row, "id") == dshWorkflowRow && scalarValue(row, "disabled") != "true" {
			config := mappingValue(row, "config")
			if scalarValue(config, "bundleHash") == record.Installed.Release {
				reference = scalarValue(config, "connectorConfig")
			}
		}
	}
	if reference != connectorconfig.Path(a.home, string(DeepSeekHarness), record.Profile) {
		return status
	}
	config, err := connectorconfig.Load(reference)
	if err != nil || config.Home != a.home || config.Provider != string(DeepSeekHarness) || config.Profile != record.Profile || config.HostID != a.hostID || !config.Controlled {
		return status
	}
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return status
	}
	account, err := store.AccountScope()
	if err != nil {
		return status
	}
	id := strings.TrimSuffix(filepath.Base(config.PairingFile), ".json")
	pair, err := workflowhost.Load(a.home, id, account)
	if err != nil || pair.Profile != record.Profile || pair.Provider != string(DeepSeekHarness) {
		return status
	}
	if observed, err := workflowhost.ReadLastRuntimeStatus(a.home, pair); err == nil {
		if r := time.Now().UnixMilli() - observed.CheckedAt; r >= 0 && r <= 12000 {
			status.RunningVersion, status.ProcessInstance = observed.Build, observed.Instance
			status.RunningClientVersion = observed.ClientBuild
		}
		if record.matchesLoad(observed) {
			status.State = "verified"
			if status.RunningClientVersion != "" && status.RunningClientVersion != record.Installed.Version {
				status.State = "client_mismatch"
			}
		}
	}
	return status
}

// ChangeDSHPlugin updates a single connected profile through DSH's installer.
func (a *Adapter) ChangeDSHPlugin(ctx context.Context, profile, action, directory string) (agentintegration.Status, error) {
	if a.kind != DeepSeekHarness || !slices.Contains(dshDesktopProfiles, profile) ||
		(action != "upgrade" && action != "rollback") || (action == "rollback" && directory != "") {
		return agentintegration.Status{}, errors.New("select a DSH Desktop or Web profile and a supported plugin operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var cli string
	if a.desktopDSHMode() {
		var err error
		cli, err = agentexec.FindDSHDesktopCLI()
		if err != nil {
			return agentintegration.Status{}, err
		}
	} else if profile != a.dshProfile() {
		return agentintegration.Status{}, errors.New("the standalone DSH launcher serves a different profile")
	}
	target := a.forDSHProfile(profile, cli)
	unlock, err := target.lockDSHPlugin(ctx)
	if err != nil {
		return agentintegration.Status{}, err
	}
	defer unlock()
	if running, err := target.dshProfileRunning(ctx); err != nil {
		return agentintegration.Status{}, err
	} else if running {
		return agentintegration.RequireQuit(a.status(ctx), action), nil
	}
	if err := target.checkDSHOwnership(); err != nil {
		return agentintegration.Status{}, err
	}
	managed, err := readDSHConfig(target.dshConfigPath(), a.self, a.home, a.hostID)
	if err != nil {
		return agentintegration.Status{}, err
	}
	if !managed.configured || !managed.owned {
		return agentintegration.Status{}, errors.New("connect the selected DSH profile before changing its plugin")
	}
	record, err := target.installDSHPlugin(ctx, action, directory)
	if err != nil {
		return agentintegration.Status{}, err
	}
	patchUnlock, err := lockDSHPatch(ctx, target.dshConfigPath())
	if err != nil {
		return agentintegration.Status{}, err
	}
	defer patchUnlock()
	if err := target.setDSHPluginHash(record.Selected.Release); err != nil {
		return agentintegration.Status{}, err
	}
	record.Step = "restart"
	record.KeepEnabled = nil
	if err := target.saveDSHPlugin(record); err != nil {
		return agentintegration.Status{}, err
	}
	status := a.status(ctx)
	a.inspectConfiguration(&status)
	a.inspectDSHConnection(ctx, &status)
	return status, nil
}

func (a *Adapter) dshProfileRunning(ctx context.Context) (bool, error) {
	if a.dshProfile() == "desktop" && a.dshCLI != "" {
		return a.isDSHDesktopRunning(ctx, a.dshCLI)
	}
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
	return !dshProcessExited(lock.PID), nil
}
