package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/winfile"
)

type pythonComponent struct {
	ID               string            `json:"id"`
	Revision         string            `json:"revision"`
	SHA256           string            `json:"sha256"`
	BaseFingerprint  string            `json:"baseFingerprint"`
	Platform         string            `json:"platform"`
	Arch             string            `json:"arch"`
	PythonABI        string            `json:"pythonAbi"`
	Packages         map[string]string `json:"packages"`
	CatalogURL       string            `json:"catalogUrl,omitempty"`
	Tag              string            `json:"tag,omitempty"`
	selectionInvalid bool
	awaitingProbe    bool
}

type pythonComponentCatalog struct {
	managed        bool
	pendingRelease string
	SchemaVersion  int                        `json:"schemaVersion"`
	Platform       string                     `json:"platform"`
	Arch           string                     `json:"arch"`
	Components     map[string]pythonComponent `json:"components"`
}

var componentDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var pythonComponentIDs = desktopupdate.UpdateComponentIDs()

// Run with the startup lock held after stopping the previous runtime.
func prepareDesktopPythonComponents(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	version := strings.TrimSpace(os.Getenv(desktopAppVersionEnvVar))
	if cfg.Profile != "desktop" || version == "" {
		return nil
	}
	if cfg.UpdateBootstrap == nil {
		if retained, err := consumeV1ComponentHandoff(ctx, paths, version); err != nil || retained {
			return err
		}
		work, e := desktopupdate.ReadSelectedComponents(paths.RuntimeRoot)
		if e != nil {
			return nil
		} // do not destroy evidence of an interrupted selection write
		if work != nil {
			return nil // A restart never resets a user's selection.
		}
		if desktopupdate.UnsignedTestBuild || cfg.UpdateStorage != nil && cfg.UpdateStorage.Version == 2 {
			identity, identityErr := readNativeConfiguration(ctx, paths)
			if identityErr == nil && identity.UpdaterProtocol == 2 && identity.Version == version {
				if err := retainPinnedSelections(ctx, paths); err != nil {
					return err
				}
				return migrateManualComponentSelection(paths, identity)
			}
			if desktopupdate.UnsignedTestBuild {
				return retainPinnedSelections(ctx, paths)
			}
			if identityErr != nil {
				return nil
			} // leave existing evidence for a verified startup
		}
	}
	catalog, err := loadPythonComponentCatalog(paths)
	if err != nil || catalog == nil {
		return err
	}
	for _, id := range pythonComponentIDs {
		if _, deferred := catalog.Components[id]; !deferred {
			continue
		}
		// Native maintenance verifies the selection and retains the source for recovery.
		preserveInstallation := cfg.UpdateBootstrap != nil
		if err := prepareDesktopPythonComponentVersion(ctx, paths, id, version, preserveInstallation); err != nil {
			return fmt.Errorf("prepare Python component %s for app version %s: %w", id, version, err)
		}
	}
	return nil
}

// A v0.5 offline pin may have no selection receipt. An online selection may
// refer to the previous distribution URL. Rebind only bytes independently
// identified by this app's own pin. Preserve any other evidence for recovery.
func retainPinnedSelections(ctx context.Context, paths RuntimePaths) error {
	lock, err := desktopupdate.NativeComponentLock(paths.RuntimeRoot)
	if desktopupdate.ErrorCode(err) == "update_transaction_busy" {
		return nil // Leave the installer's selection alone while it is changing.
	}
	if err != nil {
		return err
	}
	defer desktopupdate.ReleaseNativeLock(lock)
	var pins struct{ Components map[string]json.RawMessage }
	raw, err := os.ReadFile(filepath.Join(paths.ResourcesRoot, "config", "python-components.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &pins); err != nil {
		return err
	}
	catalog, err := resolvePythonComponentCatalog(paths, raw, false)
	if err != nil {
		return err
	}
	for _, id := range pythonComponentIDs {
		entry, ok := catalog.Components[id]
		if !ok || entry.selectionInvalid || entry.CatalogURL == "" || installedPythonComponentPath(paths, entry) == "" {
			continue
		}
		var pin pythonComponent
		if err := json.Unmarshal(pins.Components[id], &pin); err != nil {
			return err
		}
		if !reflect.DeepEqual(entry, pin) {
			continue // A valid newer selection on the current tag already has its own receipt.
		}
		file := filepath.Join(paths.RuntimeRoot, "deps", "python-components", id, "catalog-"+pin.Tag+".json")
		if err := desktopupdate.NoUnsignedPathAliases(file); err != nil {
			return err
		}
		receipt, err := os.ReadFile(file)
		if err == nil {
			var previous pythonComponent
			if json.Unmarshal(receipt, &previous) != nil || previous.CatalogURL == pin.CatalogURL {
				continue
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := writeFileAtomically(ctx, file, pins.Components[id], 0600, nil); err != nil {
			return err
		}
	}
	return nil
}

func prepareDesktopPythonComponentVersion(ctx context.Context, paths RuntimePaths, id, version string, preserveInstallation bool) error {
	marker := filepath.Join(paths.StateDir, id+"-component-app-version")
	previous, err := os.ReadFile(marker)
	if err == nil && strings.TrimSpace(string(previous)) == version {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !preserveInstallation {
		componentDir := filepath.Join(paths.RuntimeRoot, "deps", "python-components", id)
		if err := winfile.RetryOperation(ctx, func() error {
			return os.RemoveAll(componentDir)
		}, winfile.RetryOptions{MaxWait: pythonPayloadRenameWait}); err != nil {
			return err
		}
	}
	return writeFileAtomically(ctx, marker, []byte(version+"\n"), 0o644, nil)
}

func sourceRAGInstalled(paths RuntimePaths) bool {
	data, err := os.ReadFile(filepath.Join(paths.RuntimeRoot, "deps", "python-components", "rag", "source.ready"))
	return err == nil && strings.TrimSpace(string(data)) == paths.AlgorithmPython
}

func loadPythonComponentCatalog(paths RuntimePaths) (*pythonComponentCatalog, error) {
	return loadPythonComponentCatalogWithInstallationCheck(paths, false)
}

// Normal startup may degrade gracefully when an optional component is damaged.
// Update planning must instead preserve that evidence, so it cannot mistake a
// previously installed capability for a component the user never installed.
func loadPythonComponentCatalogForUpdate(paths RuntimePaths) (*pythonComponentCatalog, error) {
	return loadPythonComponentCatalogWithInstallationCheck(paths, true)
}

func loadPythonComponentCatalogWithInstallationCheck(paths RuntimePaths, strictInstallation bool) (*pythonComponentCatalog, error) {
	data, err := os.ReadFile(filepath.Join(paths.ResourcesRoot, "config", "python-components.json"))
	if os.IsNotExist(err) {
		if strictInstallation {
			for _, id := range pythonComponentIDs {
				installed, evidenceErr := otherPythonComponentInstallation(paths, id, "")
				if evidenceErr != nil || installed {
					return nil, fmt.Errorf("component installation evidence has no catalog")
				}
			}
		}
		return nil, nil // Older full bundles and source development.
	}
	if err != nil {
		return nil, err
	}
	return resolvePythonComponentCatalog(paths, data, strictInstallation)
}

// Share selection rules between startup and the read-only update preflight.
func resolvePythonComponentCatalog(paths RuntimePaths, data []byte, strictInstallation bool) (*pythonComponentCatalog, error) {
	var catalog pythonComponentCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("read Python component catalog: %w", err)
	}
	if catalog.SchemaVersion != 1 || catalog.Platform != runtime.GOOS || catalog.Arch != runtime.GOARCH {
		return nil, fmt.Errorf("Python component catalog does not match %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bound, readErr := desktopupdate.ReadSelectedComponents(paths.RuntimeRoot)
	digest := sha256.Sum256(data)
	catalog.managed = readErr != nil || bound != nil
	current := readErr == nil && bound != nil && bound.ComponentsSHA256 == hex.EncodeToString(digest[:])
	if current {
		catalog.pendingRelease = bound.ReleaseID
	}
	if current && len(bound.VerifiedComponents) != 0 {
		// The signed package writes this side by side with its runtime. A
		// replacement release sharing an identical catalog cannot run B's work.
		var meta struct{ LazyMind struct{ ReleaseID string } }
		raw, e := os.ReadFile(filepath.Join(filepath.Dir(paths.ResourcesRoot), "app-update.yml"))
		current = e == nil && json.Unmarshal(raw, &meta) == nil && meta.LazyMind.ReleaseID == bound.ReleaseID
	}
	for _, id := range pythonComponentIDs {
		entry, ok := catalog.Components[id]
		if !ok {
			continue // Older installers and full builds include clustering in base Python.
		}
		if entry.ID != id || !componentDigestPattern.MatchString(entry.Revision) ||
			!componentDigestPattern.MatchString(entry.SHA256) || !componentDigestPattern.MatchString(entry.BaseFingerprint) {
			return nil, fmt.Errorf("invalid Python component descriptor: %s", id)
		}
		if catalog.managed && !current {
			entry.selectionInvalid = true
			catalog.Components[id] = entry
			continue
		}
		if current {
			revision := bound.VerifiedComponents[id]
			var selected pythonComponent
			file := filepath.Join(paths.RuntimeRoot, "deps", "python-components", id, "catalog-"+entry.Tag+".json")
			receipt, e := os.ReadFile(file)
			valid := slices.Contains(bound.Wanted, id) && componentDigestPattern.MatchString(revision) && e == nil && json.Unmarshal(receipt, &selected) == nil &&
				selected.ID == id && componentDigestPattern.MatchString(selected.SHA256) && selected.Revision == revision && selected.Tag == entry.Tag && selected.Platform == entry.Platform && selected.Arch == entry.Arch &&
				selected.CatalogURL == entry.CatalogURL && selected.PythonABI == entry.PythonABI && selected.BaseFingerprint == entry.BaseFingerprint &&
				reflect.DeepEqual(selected.Packages, entry.Packages) && installedPythonComponentPath(paths, selected) != ""
			if valid {
				catalog.Components[id] = selected
			} else {
				entry.selectionInvalid = true
				entry.awaitingProbe = slices.Contains(bound.Wanted, id) && revision == ""
				catalog.Components[id] = entry
			}
			continue
		}
		if entry.CatalogURL != "" {
			if !componentDigestPattern.MatchString(entry.Tag) {
				return nil, fmt.Errorf("invalid Python component tag")
			}
			data, err := os.ReadFile(filepath.Join(paths.RuntimeRoot, "deps", "python-components", id, "catalog-"+entry.Tag+".json"))
			var selected pythonComponent
			validSelection := err == nil && json.Unmarshal(data, &selected) == nil
			if validSelection &&
				selected.ID == entry.ID && selected.Tag == entry.Tag && selected.CatalogURL == entry.CatalogURL &&
				selected.Platform == entry.Platform && selected.Arch == entry.Arch && selected.PythonABI == entry.PythonABI &&
				selected.BaseFingerprint == entry.BaseFingerprint && reflect.DeepEqual(selected.Packages, entry.Packages) &&
				componentDigestPattern.MatchString(selected.Revision) && componentDigestPattern.MatchString(selected.SHA256) &&
				installedPythonComponentPath(paths, selected) != "" {
				catalog.Components[id] = selected
			} else if desktopupdate.UnsignedTestBuild && !os.IsNotExist(err) && !strictInstallation {
				// An old origin can attest the user's selection, but never
				// supplies code or URLs for the new channel. Only its exact
				// installed payload may match the current packaged pin.
				previous := selected
				previous.CatalogURL = entry.CatalogURL
				entry.selectionInvalid = !validSelection || !reflect.DeepEqual(previous, entry)
				catalog.Components[id] = entry
			} else if strictInstallation {
				if !os.IsNotExist(err) {
					return nil, fmt.Errorf("component selection evidence is invalid")
				}
				installed, evidenceErr := otherPythonComponentInstallation(paths, id, entry.Revision)
				if evidenceErr != nil || installed {
					return nil, fmt.Errorf("component selection evidence is missing")
				}
			}
		}
	}
	return &catalog, nil
}

func otherPythonComponentInstallation(paths RuntimePaths, id, pinnedRevision string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(paths.RuntimeRoot, "deps", "python-components", id))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() == "source.ready" || (componentDigestPattern.MatchString(entry.Name()) && entry.Name() != pinnedRevision) {
			return true, nil
		}
	}
	return false, nil
}

func installedPythonComponentPath(paths RuntimePaths, entry pythonComponent) string {
	if entry.selectionInvalid || !desktopupdate.IsUpdateComponent(entry.ID) || !componentDigestPattern.MatchString(entry.Revision) {
		return ""
	}
	root := filepath.Join(paths.RuntimeRoot, "deps", "python-components", entry.ID, entry.Revision)
	marker, err := os.ReadFile(filepath.Join(root, ".ready"))
	if err != nil || strings.TrimSpace(string(marker)) != entry.SHA256 {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "bundle-manifest.json"))
	var installed pythonComponent
	if err != nil || json.Unmarshal(data, &installed) != nil || installed.ID != entry.ID ||
		installed.Revision != entry.Revision || installed.BaseFingerprint != entry.BaseFingerprint ||
		installed.Platform != entry.Platform || installed.Arch != entry.Arch || installed.PythonABI != entry.PythonABI ||
		!reflect.DeepEqual(installed.Packages, entry.Packages) {
		return ""
	}
	site := filepath.Join(root, "site-packages")
	if info, err := os.Stat(site); err != nil || !info.IsDir() {
		return ""
	}
	return site
}

func (m *RuntimeManager) prepareDesktopCluster(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) {
	if cfg.Profile != "desktop" || cfg.UpdateBootstrap != nil || desktopupdate.UnsignedTestBuild || cfg.UpdateStorage != nil && cfg.UpdateStorage.Version == 2 {
		return
	}
	if work, e := desktopupdate.ReadSelectedComponents(paths.RuntimeRoot); work != nil || e != nil {
		return
	}
	if state, e := readRuntimeState(paths.StateFile); e == nil && state.Config.NativeCommittedTx != "" {
		return
	}
	catalog, err := loadPythonComponentCatalog(paths)
	if err != nil {
		m.progressf("could not read desktop Python component catalog: %v", err)
		return
	}
	if catalog == nil {
		return
	}
	cluster, deferred := catalog.Components["cluster"]
	if !deferred || installedPythonComponentPath(paths, cluster) != "" {
		return
	}

	started := m.now()
	m.startupEvent("phase.started", "python-cluster", started, nil)
	prepareCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	environment := append(serviceRuntimeEnv(paths), pythonComponentEnvironment(paths, cfg.Algorithm.RAGDisabled)...)
	result, err := m.runner.Run(prepareCtx, Command{
		Name: paths.CoreBin,
		Args: []string{"--prepare-cluster-component"},
		Dir:  filepath.Join(paths.RepoRoot, coreSourceDirName),
		Env: append(environment,
			"LAZYMIND_RUNTIME_MODE=local",
			"LAZYMIND_RUNTIME_ROOT="+paths.RuntimeRoot),
	})
	if err != nil {
		m.progressf("desktop clustering preparation unavailable; will retry on the next start: %v %s", err, commandResultDetail(result))
		m.startupEvent("phase.skipped", "python-cluster", started, nil)
		return
	}
	m.startupEvent("phase.completed", "python-cluster", started, nil)
}

func pythonComponentEnvironment(paths RuntimePaths, ragDisabled bool) []string {
	values := []string{
		"LAZYMIND_COMPONENT_MAINTENANCE_MANAGER=" + filepath.Join(paths.BuildRoot, "bin", executableName("local-runtime-manager")),
		"LAZYMIND_RUNTIME_RESOURCES_ROOT=" + paths.ResourcesRoot,
		"LAZYMIND_PYTHON_COMPONENT_CATALOG=" + filepath.Join(paths.ResourcesRoot, "config", "python-components.json"),
		"LAZYMIND_ALGORITHM_PYTHON=" + paths.AlgorithmPython,
		"LAZYMIND_ALGORITHM_SOURCE=" + filepath.Join(paths.RepoRoot, "algorithm"),
	}
	catalog, err := loadPythonComponentCatalog(paths)
	if err != nil || catalog == nil {
		if desktopupdate.UnsignedTestBuild {
			return append(values, ragEnabledEnvVar+"=0", "LAZYMIND_CLUSTER_ENABLED=0", "LAZYMIND_PYTHON_COMPONENT_PATHS=")
		}
		if ragDisabled {
			values = append(values, ragEnabledEnvVar+"=0")
		} else {
			values = append(values, ragEnabledEnvVar+"=1")
		}
		if paths.RepoRoot != "" {
			values = append(values, "LAZYMIND_SOURCE_COMPONENT_INSTALLER="+filepath.Join(paths.BuildRoot, "bin", executableName("local-runtime-manager")))
		}
		return values
	}
	var sites []string
	for _, id := range pythonComponentIDs {
		entry, deferred := catalog.Components[id]
		if !deferred && !catalog.managed {
			continue
		}
		enabled := "0"
		if site := installedPythonComponentPath(paths, entry); site != "" && (id != "rag" || !ragDisabled) {
			sites = append(sites, site)
			enabled = "1"
		}
		values = append(values, "LAZYMIND_"+strings.ToUpper(id)+"_ENABLED="+enabled)
	}
	return append(values, "LAZYMIND_PYTHON_COMPONENT_PATHS="+strings.Join(sites, string(os.PathListSeparator)))
}

func pythonComponentSites(paths RuntimePaths) []string {
	catalog, err := loadPythonComponentCatalog(paths)
	if err != nil || catalog == nil {
		return nil
	}
	var sites []string
	for _, id := range pythonComponentIDs {
		if entry, deferred := catalog.Components[id]; deferred {
			if site := installedPythonComponentPath(paths, entry); site != "" {
				sites = append(sites, site)
			}
		}
	}
	return sites
}
