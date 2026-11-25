// Package workflowconnector provisions and selects the editable desktop Connector packages.
package workflowconnector

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"lazymind/agentconnector/internal/localfile"
)

const packageDirectory = "workflow-connectors"
const manifestName = "connector.json"

const DSHHost = "dsh"
const dshArchiveName = "dsh-workflow.tgz"

var hosts = []string{"codex", "workbuddy", DSHHost}

func distributionHosts(executable string) []string {
	dir := filepath.Join(filepath.Dir(executable), packageDirectory)
	absent := func(host string) bool {
		_, err := os.Lstat(filepath.Join(dir, host))
		return errors.Is(err, os.ErrNotExist)
	}
	if absent(DSHHost) {
		return hosts[:2]
	}
	if absent("codex") && absent("workbuddy") {
		return hosts[2:]
	}
	return hosts
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,79}$`)
var modulePattern = regexp.MustCompile(`^([a-zA-Z0-9_-]+/)*[a-zA-Z0-9_.-]+\.mjs$`)

// PathError exposes only a managed package path to setup diagnostics.
type PathError struct {
	Host string
	Item string
	Path string
	Err  error
}

func (e *PathError) Error() string {
	return fmt.Sprintf("%s Connector %s %s: %v", displayHost(e.Host), e.Item, e.Path, e.Err)
}

func (e *PathError) Unwrap() error { return e.Err }

type Manifest struct {
	Schema       int      `json:"schema"`
	Host         string   `json:"host"`
	Release      string   `json:"release"`
	Version      string   `json:"version"`
	Entry        string   `json:"entry"`
	Presentation string   `json:"presentation"`
	Control      string   `json:"control"`
	Files        []string `json:"files"`
}

type Installation struct {
	Manifest
	Dir string
}

func RecordPath(home, host string) string {
	return filepath.Join(home, host+"-workflow", manifestName)
}

func displayHost(host string) string {
	switch host {
	case DSHHost:
		return "DeepSeek Harness"
	case "workbuddy":
		return "WorkBuddy"
	default:
		return "Codex"
	}
}

func readManifest(path, host string) (Manifest, error) {
	var manifest Manifest
	body, err := os.ReadFile(path)
	if err != nil {
		return manifest, &PathError{Host: host, Item: "installation record", Path: path, Err: err}
	}
	if json.Unmarshal(body, &manifest) != nil || manifest.Schema != 1 || manifest.Host != host ||
		!digestPattern.MatchString(manifest.Release) || !versionPattern.MatchString(manifest.Version) || len(manifest.Files) > 128 {
		return Manifest{}, fmt.Errorf("invalid %s Connector installation record %s", displayHost(host), path)
	}
	if host == DSHHost {
		if manifest.Entry != dshArchiveName || !slices.Equal(manifest.Files, []string{dshArchiveName}) || manifest.Presentation != "" || manifest.Control != "" {
			return Manifest{}, fmt.Errorf("invalid %s Connector archive layout in %s", displayHost(host), path)
		}
		return manifest, nil
	}
	files := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		if !fs.ValidPath(file) || !modulePattern.MatchString(file) || files[file] {
			return Manifest{}, fmt.Errorf("invalid %s Connector module paths in %s", displayHost(host), path)
		}
		files[file] = true
	}
	if !files[manifest.Entry] || !files[manifest.Presentation] || !files[manifest.Control] {
		return Manifest{}, fmt.Errorf("missing %s Connector module paths in %s", displayHost(host), path)
	}
	return manifest, nil
}

func Read(home, host string) (Installation, error) {
	if !slices.Contains(hosts, host) {
		return Installation{}, fmt.Errorf("unsupported Workflow Connector %q", host)
	}
	path := RecordPath(home, host)
	manifest, err := readManifest(path, host)
	if err != nil {
		return Installation{}, err
	}
	return Installation{Manifest: manifest, Dir: filepath.Join(filepath.Dir(path), "connectors", manifest.Release)}, nil
}

func (i Installation) modulePath(file string) string {
	return filepath.Join(i.Dir, filepath.FromSlash(file))
}

func (i Installation) EntryPath() string        { return i.modulePath(i.Entry) }
func (i Installation) PresentationPath() string { return i.modulePath(i.Presentation) }
func (i Installation) ControlPath() string      { return i.modulePath(i.Control) }

func (i Installation) checkFile(file string) error {
	path := i.modulePath(file)
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("expected a regular file")
	}
	if err != nil {
		return &PathError{Host: i.Host, Item: "module", Path: path, Err: err}
	}
	return nil
}

func Resolve(home, host string) (Installation, error) {
	installation, err := Read(home, host)
	if err != nil {
		return installation, err
	}
	if host == DSHHost {
		return installation, installation.verifyRelease("")
	}
	for _, file := range installation.Files {
		if err := installation.checkFile(file); err != nil {
			return installation, err
		}
	}
	return installation, nil
}

func packaged(executable, host string) (Installation, error) {
	dir := filepath.Join(filepath.Dir(executable), packageDirectory, host)
	manifest, err := readManifest(filepath.Join(dir, manifestName), host)
	return Installation{Manifest: manifest, Dir: dir}, err
}

func (i Installation) revision(copyTo string) (string, error) {
	hash := sha256.New()
	for _, file := range i.Files {
		path := i.modulePath(file)
		body, err := os.ReadFile(path)
		if err != nil {
			return "", &PathError{Host: i.Host, Item: "module", Path: path, Err: err}
		}
		if i.Host == DSHHost {
			if _, err := dshPluginFiles(body, i.Version); err != nil {
				return "", &PathError{Host: i.Host, Item: "archive", Path: path, Err: err}
			}
		}
		sum := sha256.Sum256(body)
		hash.Write([]byte(file))
		hash.Write([]byte{0})
		hash.Write(sum[:])
		if copyTo != "" {
			if err := localfile.Write(filepath.Join(copyTo, filepath.FromSlash(file)), body, 0600); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (i Installation) Revision() (string, error) { return i.revision("") }

func (i Installation) configureCopy(destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".connector-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := i.verifyRelease(staging); err != nil {
		return err
	}
	if err := writeManifest(filepath.Join(staging, manifestName), i.Manifest); err != nil {
		return err
	}
	return os.Rename(staging, destination)
}

func (i Installation) verifyRelease(copyTo string) error {
	revision, err := i.revision(copyTo)
	if err != nil {
		return err
	}
	if revision != i.Release {
		return fmt.Errorf("%s Connector release digest mismatch at %s", displayHost(i.Host), i.Dir)
	}
	return nil
}

func writeManifest(path string, manifest Manifest) error {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return localfile.Write(path, append(body, '\n'), 0600)
}

func (i Installation) install(ctx context.Context, home string, upgrade bool) error {
	record := RecordPath(home, i.Host)
	if err := os.MkdirAll(filepath.Dir(record), 0700); err != nil {
		return err
	}
	unlock, err := localfile.LockContext(ctx, record+".lock")
	if err != nil {
		return err
	}
	defer unlock()
	previous, err := Read(home, i.Host)
	if err == nil && (!upgrade || previous.Release == i.Release) {
		// Same-release editable reconnects keep local changes. DSH archives
		// are immutable and must still pass verification.
		if i.Host == DSHHost {
			return i.verifyRelease("")
		}
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := i.retain(home); err != nil {
		return err
	}
	return writeManifest(record, i.Manifest)
}

func (i Installation) retain(home string) (Installation, error) {
	dir := filepath.Join(filepath.Dir(RecordPath(home, i.Host)), "connectors", i.Release)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := i.configureCopy(dir); err != nil {
			return Installation{}, err
		}
	} else if err != nil {
		return Installation{}, err
	}
	pending := Installation{Manifest: i.Manifest, Dir: dir}
	if i.Host == DSHHost {
		return pending, pending.verifyRelease("")
	}
	return pending, pending.checkFile(pending.Entry)
}

// Provision is called by the desktop or local install entry point. A standalone
// Go build can still provide Workflow MCP and Bridge without a sidecar package.
func Provision(ctx context.Context, home, executable string, upgrade bool) error {
	_, err := os.Stat(filepath.Join(filepath.Dir(executable), packageDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var result error
	for _, host := range distributionHosts(executable) {
		source, err := packaged(executable, host)
		if err == nil {
			err = source.install(ctx, home, upgrade || host == DSHHost)
		}
		result = errors.Join(result, err)
	}
	return result
}

// Ensure installs a host sidecar on first connection, for CLI callers that use
// a packaged executable directly before running Assistant Bridge.
func Ensure(ctx context.Context, home, executable, host string) error {
	if _, err := Read(home, host); errors.Is(err, os.ErrNotExist) {
		source, packageErr := packaged(executable, host)
		if packageErr != nil {
			return packageErr
		}
		if err := source.install(ctx, home, false); err != nil {
			return err
		}
	}
	_, err := Resolve(home, host)
	return err
}

// Package is the set of verified sidecars available beside an executable.
type Package struct {
	identity string
	sources  []Installation
}

// ReadPackage returns the usable sidecars together with diagnostics for any
// failed hosts. A missing package directory is valid for standalone builds.
func ReadPackage(executable string) (Package, error) {
	var result Package
	_, err := os.Stat(filepath.Join(filepath.Dir(executable), packageDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	releases := make([]string, 0, len(hosts))
	var failures error
	for _, host := range distributionHosts(executable) {
		source, err := packaged(executable, host)
		releases = append(releases, "")
		if err == nil {
			err = source.verifyRelease("")
		}
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		result.sources = append(result.sources, source)
		releases[len(releases)-1] = source.Release
	}
	if len(result.sources) > 0 {
		// Empty release slots distinguish partial packages from each other and
		// from a full package while keeping existing full-package identities.
		sum := sha256.Sum256([]byte(strings.Join(releases, "\x00")))
		result.identity = hex.EncodeToString(sum[:])
	}
	return result, failures
}

func (p Package) Identity() string { return p.identity }

func (p Package) Stage(destination string) error {
	var failures error
	for _, source := range p.sources {
		staged, err := packaged(destination, source.Host)
		if errors.Is(err, os.ErrNotExist) {
			err = source.configureCopy(staged.Dir)
			if err != nil {
				// Another launcher may have published the same release.
				if published, readErr := packaged(destination, source.Host); readErr == nil {
					staged, err = published, nil
				}
			} else {
				continue
			}
		}
		if err == nil {
			if staged.Release != source.Release {
				err = fmt.Errorf("expected release %s, found %s", source.Release, staged.Release)
			} else {
				err = staged.verifyRelease("")
			}
		}
		if err != nil {
			failures = errors.Join(failures, &PathError{Host: source.Host, Item: "staged package", Path: staged.Dir, Err: err})
		}
	}
	return failures
}

func reloadPath(home, host string) string {
	return filepath.Join(home, host+"-workflow", "connector-reload")
}

func ReloadRequest(home, host string) string {
	body, err := os.ReadFile(reloadPath(home, host))
	if err != nil {
		return ""
	}
	return string(body)
}

func RequestReload(home, host string) (string, error) {
	if _, err := Resolve(home, host); err != nil {
		return "", err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	request := hex.EncodeToString(nonce[:])
	return request, localfile.Write(reloadPath(home, host), []byte(request), 0600)
}

// Runtime identifies the contents selected for a running Node process.
type Runtime struct {
	ConnectorEntry    string `json:"connector_entry"`
	ConnectorVersion  string `json:"connector_version"`
	ConnectorRevision string `json:"connector_revision"`
	ConnectorReload   string `json:"connector_reload"`
}

func (i Installation) ConfigureCommand(cmd *exec.Cmd, reload string) error {
	revision, err := i.Revision()
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Environ(), "LAZYMIND_WORKFLOW_CONNECTOR_VERSION="+i.Version,
		"LAZYMIND_WORKFLOW_CONNECTOR_REVISION="+revision, "LAZYMIND_WORKFLOW_CONNECTOR_RELOAD="+reload)
	return nil
}

func (i Installation) Matches(r Runtime) bool {
	if r.ConnectorVersion != i.Version || !digestPattern.MatchString(r.ConnectorRevision) || !filepath.IsAbs(r.ConnectorEntry) {
		return false
	}
	if filepath.Clean(r.ConnectorEntry) == i.EntryPath() {
		return true
	}
	// Node canonicalizes import.meta.url, including macOS /var and Windows
	// junctions. A canonical alias for the selected file is the same entry.
	expected, err := os.Stat(i.EntryPath())
	if err != nil {
		return false
	}
	actual, err := os.Stat(r.ConnectorEntry)
	return err == nil && os.SameFile(expected, actual)
}
