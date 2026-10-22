package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
)

var dshDesktopProfiles = []string{"desktop", "web"}

func (a *Adapter) dshProfile() string {
	if a.dshTarget != "" {
		return a.dshTarget
	}
	return dshProfileName()
}

func (a *Adapter) dshConfigPath() string {
	return filepath.Join(dshHome(), "profiles", a.dshProfile(), "cordis.patch.yml")
}

func (a *Adapter) desktopDSHMode() bool {
	if a.kind != DeepSeekHarness || a.dshTarget != "" {
		return false
	}
	_, err := agentexec.FindDSHDesktopCLI()
	if err == nil || pathExists(filepath.Join(dshHome(), "profiles", "desktop")) || pathExists(a.dshRecordPath()) {
		return true
	}
	// Preserve an explicitly configured legacy CLI or existing Web installation.
	// A new installation receives the Desktop prerequisites by default.
	return strings.TrimSpace(connectorconfig.Value("LAZYMIND_DSH_PATH")) == "" && !pathExists(filepath.Join(dshHome(), "profiles", "web", "package.json"))
}

func (a *Adapter) forDSHProfile(name, cli string) *Adapter {
	copy := *a
	copy.dshTarget, copy.dshCLI = name, cli
	return &copy
}

// Records preserve retry intent and diagnostics. Readiness is checked against disk.
type dshInstallRecord struct {
	Action   string            `json:"action,omitempty"`
	Root     string            `json:"root"`
	Profiles map[string]string `json:"profiles"`
}

func (a *Adapter) dshRecordPath() string {
	sum := sha256.Sum256([]byte(dshHome()))
	return filepath.Join(a.home, "integrations", "dsh-"+hex.EncodeToString(sum[:16])+".json")
}

func (a *Adapter) dshRecord() (dshInstallRecord, error) {
	r := dshInstallRecord{Root: dshHome(), Profiles: map[string]string{}}
	body, err := os.ReadFile(a.dshRecordPath())
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return r, err
	}
	if r.Root != dshHome() || r.Profiles == nil {
		return r, errors.New("invalid DSH installation record")
	}
	return r, nil
}

func validDSHDesktopManifest() error {
	path := filepath.Join(dshHome(), "profiles", "desktop", "package.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("start DSH Desktop once before connecting (%s): %w", path, err)
	}
	var manifest struct {
		DSH struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("invalid Desktop manifest %s: %w", path, err)
	}
	for _, bundle := range manifest.DSH.Profile.Bundles {
		if bundle == "@deepseek-ai/dsh-web-app" {
			return nil
		}
	}
	return fmt.Errorf("Desktop manifest %s is incomplete; start DSH Desktop once", path)
}

// Catch hand-written entries, including entries in multi-row inserts. We only
// edit the exact row shape created by this installation.
func hasLazyMindEntry(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.MappingNode && (scalarValue(node, "id") == "mcp-lazymind" || scalarValue(node, "id") == dshWorkflowRow || scalarValue(node, "serverName") == serverName) {
		return true
	}
	for _, child := range node.Content {
		if hasLazyMindEntry(child) {
			return true
		}
	}
	return false
}

func (a *Adapter) checkDSHOwnership() error {
	path := a.dshConfigPath()
	doc, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	mcpCount, workflowCount := 0, 0
	for _, row := range yamlSequence(doc).Content {
		if dshEntryID(row) == "mcp-lazymind" {
			mcpCount++
			if reference := decodeDSHStdio(row).Env[connectorconfig.EnvironmentKey]; reference != "" {
				c, err := connectorconfig.Load(reference)
				if err != nil {
					return err
				}
				if c.Profile != filepath.Dir(path) {
					return fmt.Errorf("MCP connection does not belong to %s", path)
				}
			}
			if !stateForStdio(decodeDSHStdio(row), a.self, a.home, a.hostID, DeepSeekHarness).owned {
				return fmt.Errorf("unmanaged LazyMind MCP entry in %s", path)
			}
		} else if scalarValue(row, "id") == dshWorkflowRow {
			workflowCount++
			file := scalarValue(mappingValue(row, "config"), "pairingFile")
			if reference := scalarValue(mappingValue(row, "config"), "connectorConfig"); reference != "" {
				connection, err := connectorconfig.Load(reference)
				if err != nil {
					return err
				}
				if connection.Home != a.home || connection.Provider != string(DeepSeekHarness) || connection.Profile != filepath.Dir(a.dshConfigPath()) {
					return errors.New("DSH connection ownership mismatch")
				}
				file = connection.PairingFile
			}
			// A disabled row with no config is the tombstone left by disconnect.
			if file == "" && scalarValue(row, "disabled") == "true" {
				continue
			}
			if file == "" || filepath.Dir(filepath.Clean(file)) != filepath.Join(a.home, "workflow-hosts") {
				return fmt.Errorf("unmanaged Workflow entry in %s", path)
			}
			body, err := os.ReadFile(file)
			var pair struct {
				Profile string `json:"profile"`
			}
			if err != nil || json.Unmarshal(body, &pair) != nil || pair.Profile != filepath.Dir(path) {
				return fmt.Errorf("Workflow pairing does not belong to %s", path)
			}
		} else if hasLazyMindEntry(row) {
			return fmt.Errorf("hand-written LazyMind entry in %s; reconcile it before connecting", path)
		}
	}
	if mcpCount > 1 || workflowCount > 1 {
		return fmt.Errorf("duplicate LazyMind entries in %s", path)
	}
	return nil
}

type dshDesktopRuntime struct {
	cli         string
	cliErr      error
	manifestErr error
	inspection  dshRuntimeInspection
	issue       *agentintegration.Issue
}

func (a *Adapter) inspectDSHDesktop(ctx context.Context) dshDesktopRuntime {
	var result dshDesktopRuntime
	result.cli, result.cliErr = agentexec.FindDSHDesktopCLI()
	result.manifestErr = validDSHDesktopManifest()
	if result.cliErr == nil && result.manifestErr == nil {
		var err error
		result.inspection, err = a.inspectDSH(ctx, result.cli)
		result.issue = dshInspectionIssue(result.inspection, err, result.cli)
	} else if result.cliErr == nil && pathExists(filepath.Join(dshHome(), "profiles", "desktop", "package.json")) {
		result.issue = dshIssue("dependency_environment", filepath.Join(dshHome(), "profiles", "desktop"))
	}
	return result
}

func (a *Adapter) dshDesktopStatus(ctx context.Context) agentintegration.Status {
	return a.dshDesktopStatusWithRuntime(a.inspectDSHDesktop(ctx))
}

// Re-read managed files on every call. A disconnect never changes dependencies,
// so it can reuse its runtime inspection while checking fresh configuration.
func (a *Adapter) dshDesktopStatusWithRuntime(runtime dshDesktopRuntime) (s agentintegration.Status) {
	defer func() { sanitizeDSHStatus(&s) }()
	s = agentintegration.Status{Agent: string(DeepSeekHarness), DisplayName: "DeepSeek Harness", State: agentintegration.Ready}
	cli, cliErr, manifestErr := runtime.cli, runtime.cliErr, runtime.manifestErr
	s.Requirements = []agentintegration.Requirement{
		{ID: "dsh_desktop", Description: "Install DeepSeek Harness Desktop.", Satisfied: cliErr == nil},
		{ID: "dsh_desktop_initialized", Description: "Start DSH Desktop once before connecting.", Satisfied: manifestErr == nil},
	}
	inspection, runtimeIssue := runtime.inspection, runtime.issue
	record, err := a.dshRecord()
	if err != nil {
		return agentintegration.Fail(s, err.Error())
	}
	globalPath := filepath.Join(dshHome(), "cordis.patch.yml")
	global, err := readYAMLDocument(globalPath)
	if err != nil {
		return agentintegration.Fail(s, err.Error())
	}
	conflict := hasLazyMindEntry(global)
	if conflict {
		s.Message = "Hand-written LazyMind configuration conflicts with this connection: " + globalPath
	}
	complete, partial, failed := 0, false, false
	for _, name := range dshDesktopProfiles {
		target := a.forDSHProfile(name, cli)
		for _, inspected := range inspection.Profiles {
			if inspected.Name == name {
				target.dshMCPReady = inspected.Diagnostic == "" && inspected.MCPPath != "" && inspected.MCPVersion != ""
			}
		}
		p := agentintegration.ProfileStatus{Name: name, State: agentintegration.Ready}
		if err := target.checkDSHOwnership(); err != nil {
			p.State, p.Message = agentintegration.Conflict, err.Error()
			conflict = true
		} else {
			state, err := readDSHConfig(target.dshConfigPath(), a.self, a.home, a.hostID)
			if err != nil {
				p.State, p.Message = agentintegration.Failed, err.Error()
				failed = true
			} else {
				managed := state.configured && state.owned
				_, recorded := record.Profiles[name]
				s.CanDisconnect = s.CanDisconnect || managed || recorded
				partial = partial || managed
				if managed && state.current && target.dshWorkflowConfigured() {
					p.State = agentintegration.Enabled
					complete++
				}
				if message := record.Profiles[name]; message != "" {
					p.State, p.Message = agentintegration.Failed, message
					if s.Issue == nil {
						s.Issue = dshRecordedIssue(message, name)
					}
					failed = true
				}
			}
		}
		s.Profiles = append(s.Profiles, p)
		if p.Message != "" {
			if s.Message != "" {
				s.Message += "\n"
			}
			s.Message += name + ": " + p.Message
		}
	}
	s.CanConnect = !conflict && cliErr == nil && manifestErr == nil && runtimeIssue == nil
	switch {
	case runtimeIssue != nil:
		s.State, s.Issue = agentintegration.Failed, runtimeIssue
	case conflict:
		s.State = agentintegration.Conflict
	case complete == len(dshDesktopProfiles) && !failed:
		s.State = agentintegration.Enabled
		s.Message = "Desktop and Web are configured. Restart DSH to activate plugins. This is installation status, not online status."
	case cliErr != nil:
		s.State = agentintegration.RequirementsMissing
		s.Message = cliErr.Error()
	case manifestErr != nil:
		s.State = agentintegration.RequirementsMissing
		s.Message = manifestErr.Error()
	case partial || failed:
		s.State = agentintegration.Failed
		if s.Message == "" {
			s.Message = "DSH connection is incomplete. Retry to finish Desktop and Web, or disconnect to remove managed settings."
		}
	}
	if s.CanDisconnect && record.Action == "disconnect" && s.State != agentintegration.Enabled {
		s.RetryAction = "disconnect"
	}
	return s
}

func (a *Adapter) runBundledDSHPlugin(ctx context.Context, profile string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var output strings.Builder
	err := (agentexec.StreamCommand{Binary: a.dshCLI, Arguments: append([]string{"plugin", "--profile", profile}, args...), Environment: agentexec.SafeEnvironment("DSH_HOME=" + dshHome())}).Run(ctx, func(line []byte) error {
		if output.Len() < 8192 {
			output.Write(line)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("DSH %s installation failed: %w (%s)", profile, err, strings.TrimSpace(output.String()))
	}
	return nil
}

type dshFileSnapshot struct {
	path   string
	body   []byte
	exists bool
}

func snapshotDSHFile(path string) (dshFileSnapshot, error) {
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return dshFileSnapshot{}, err
	}
	return dshFileSnapshot{path, body, err == nil}, nil
}
func (s dshFileSnapshot) restore() error {
	if s.exists {
		return writeConfigFile(s.path, s.body)
	}
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Package manager changes are intentionally retained. Roll back only the
// profile patch and pairing if publication of the managed connection fails.
func (a *Adapter) changeDSHProfile(ctx context.Context, connect bool) (err error) {
	// Cooperate with DSH's atomic writer, which uses an exclusive .lock file.
	if err = os.MkdirAll(filepath.Dir(a.dshConfigPath()), 0700); err != nil {
		return err
	}
	var unlockPatch func()
	publishing := false
	var snapshots []dshFileSnapshot
	lockPatch := func() error {
		var e error
		unlockPatch, e = lockDSHPatch(ctx, a.dshConfigPath())
		if e != nil {
			return e
		}
		if e = a.checkDSHOwnership(); e != nil {
			return e
		}
		for i, s := range snapshots {
			snapshots[i], e = snapshotDSHFile(s.path)
			if e != nil {
				return e
			}
		}
		publishing = true
		return nil
	}
	defer func() {
		if unlockPatch != nil {
			unlockPatch()
		}
	}()
	if !connect {
		if err = lockPatch(); err != nil {
			return err
		}
	}
	patch, err := snapshotDSHFile(a.dshConfigPath())
	if err != nil {
		return err
	}
	connection, err := snapshotDSHFile(connectorconfig.Path(a.home, string(DeepSeekHarness), filepath.Dir(a.dshConfigPath())))
	if err != nil {
		return err
	}
	snapshots = []dshFileSnapshot{patch, connection}
	if connect {
		store, e := credentials.NewStore(a.home, "")
		if e != nil {
			return e
		}
		account, e := store.AccountScope()
		if e != nil {
			return e
		}
		sum := sha256.Sum256([]byte(account + "\x00" + filepath.Dir(a.dshConfigPath())))
		pair, e := snapshotDSHFile(filepath.Join(a.home, "workflow-hosts", "dsh-"+hex.EncodeToString(sum[:16])+".json"))
		if e != nil {
			return e
		}
		snapshots = append(snapshots, pair)
	} else {
		doc, e := readYAMLDocument(a.dshConfigPath())
		if e != nil {
			return e
		}
		for _, row := range yamlSequence(doc).Content {
			if scalarValue(row, "id") != dshWorkflowRow {
				continue
			}
			file := scalarValue(mappingValue(row, "config"), "pairingFile")
			if reference := scalarValue(mappingValue(row, "config"), "connectorConfig"); reference != "" {
				connection, err := connectorconfig.Load(reference)
				if err != nil {
					return err
				}
				if connection.Home != a.home || connection.Provider != string(DeepSeekHarness) || connection.Profile != filepath.Dir(a.dshConfigPath()) {
					return errors.New("DSH connection ownership mismatch")
				}
				file = connection.PairingFile
			}
			if file != "" {
				pair, e := snapshotDSHFile(file)
				if e != nil {
					return e
				}
				snapshots = append(snapshots, pair)
			}
		}
	}
	defer func() {
		if err != nil && publishing {
			for _, s := range snapshots {
				err = errors.Join(err, s.restore())
			}
		}
	}()
	if connect {
		if _, err = a.installDSHWorkflow(ctx, lockPatch); err != nil {
			return err
		}
		return writeManagedConfig(DeepSeekHarness, a.dshConfigPath(), a.self, a.home, a.hostID, true)
	}
	if err = a.disconnectDSHWorkflow(); err != nil {
		return err
	}
	return removeManagedConfig(DeepSeekHarness, a.dshConfigPath())
}

func (a *Adapter) connectDSHDesktop(ctx context.Context) agentintegration.Status {
	return a.changeDSHDesktop(ctx, true)
}
func (a *Adapter) disconnectDSHDesktop(ctx context.Context) agentintegration.Status {
	return a.changeDSHDesktop(ctx, false)
}

func (a *Adapter) changeDSHDesktop(ctx context.Context, connect bool) agentintegration.Status {
	timeout := 45 * time.Second // Leave room inside the host's 60-second disconnect budget.
	if connect {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	runtime := a.inspectDSHDesktop(ctx)
	s := a.dshDesktopStatusWithRuntime(runtime)
	if connect && (!s.CanConnect || s.State == agentintegration.Enabled) {
		return s
	}
	if !connect && !s.CanDisconnect {
		return s
	}
	if err := os.MkdirAll(filepath.Dir(a.dshRecordPath()), 0700); err != nil {
		return dshFailure(s, err)
	}
	unlock, err := localfile.Lock(a.dshRecordPath() + ".lock")
	if err != nil {
		return dshFailure(s, err)
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return dshFailure(s, err)
	}
	// Recheck after acquiring the operation lock; a second click may have waited.
	if connect {
		runtime = a.inspectDSHDesktop(ctx)
	}
	s = a.dshDesktopStatusWithRuntime(runtime)
	if connect && (!s.CanConnect || s.State == agentintegration.Enabled) {
		return s
	}
	r, err := a.dshRecord()
	if err != nil {
		return dshFailure(s, err)
	}
	r.Action = "connect"
	s.RetryAction = ""
	if !connect {
		r.Action = "disconnect"
		s.RetryAction = "disconnect"
	}
	// Keep retry direction across refreshes and process restarts, including
	// failures before the first profile can be changed.
	body, err := json.Marshal(r)
	if err != nil {
		return dshFailure(s, err)
	}
	if err := writeConfigFile(a.dshRecordPath(), body); err != nil {
		return dshFailure(s, err)
	}
	cli, _ := agentexec.FindDSHDesktopCLI()
	if running, err := agentexec.DSHDesktopRunning(ctx, cli); err != nil {
		return dshFailure(s, fmt.Errorf("check running Desktop: %w", err))
	} else if running {
		return dshFailure(s, errors.New("fully quit DSH Desktop before changing the connection"))
	}
	if connect {
		if _, err := a.bridge.Probe(ctx); err != nil {
			return dshFailure(s, fmt.Errorf("LazyMind MCP is unavailable: %w", err))
		}
	}
	for _, p := range s.Profiles {
		if connect && p.State == agentintegration.Enabled {
			continue
		}
		target := a.forDSHProfile(p.Name, cli)
		if err := target.checkDSHOwnership(); err != nil {
			r.Profiles[p.Name] = err.Error()
			log.Printf("DSH %s integration: %v", p.Name, err)
		} else if err := target.changeDSHProfile(ctx, connect); err != nil {
			r.Profiles[p.Name] = err.Error()
			log.Printf("DSH %s integration: %v", p.Name, err)
		} else {
			if connect {
				r.Profiles[p.Name] = ""
			} else {
				delete(r.Profiles, p.Name)
			}
		}
		body, _ := json.Marshal(r)
		if err := writeConfigFile(a.dshRecordPath(), body); err != nil {
			return dshFailure(a.dshDesktopStatusWithRuntime(runtime), fmt.Errorf("save DSH installation result: %w", err))
		}
	}
	if connect {
		return a.dshDesktopStatus(ctx)
	}
	return a.dshDesktopStatusWithRuntime(runtime)
}
