package systemdeps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

type PythonComponentMaintenanceContext struct {
	RuntimeRoot, ResourcesRoot, UpdaterRoot string
	ReleaseID, Python, AlgorithmSource      string
	Client                                  *http.Client
}
type componentMaintenanceRecord struct {
	Version                          int    `json:"version"`
	TransactionID                    string `json:"transactionId"`
	Phase                            string `json:"phase"`
	SourceReleaseID                  string `json:"sourceReleaseId"`
	TargetReleaseID                  string `json:"targetReleaseId"`
	RuntimeRoot                      string `json:"runtimeRoot"`
	SourceHadRAG                     bool   `json:"sourceHadRAG"`
	ComponentMode                    string `json:"componentMode"`
	SourceComponent, TargetComponent *PythonComponent
	ComponentsPath                   string `json:"componentsPath"`
	ComponentsSHA256                 string `json:"componentsSHA256"`
	ComponentsSize                   int64  `json:"componentsSize"`
	ComponentStage                   *struct {
		StagePath string `json:"stagePath"`
		Revision  string `json:"revision"`
		SHA256    string `json:"sha256"`
	} `json:"componentStage"`
	RepairTarget *struct {
		AcceptanceDigest string           `json:"acceptanceDigest"`
		ReleaseID        string           `json:"releaseId"`
		TargetComponent  *PythonComponent `json:"targetComponent"`
		ComponentsPath   string           `json:"componentsPath"`
		ComponentsSHA256 string           `json:"componentsSHA256"`
		ComponentsSize   int64            `json:"componentsSize"`
	} `json:"repairTarget"`
}

func filepathDir(path string) string { return filepath.Dir(path) }
func componentMaintenanceStateLock(ctx context.Context, root string) (*os.File, error) {
	dir := filepath.Join(root, "native-v2")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		ok, err := tryComponentLock(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if ok {
			return file, nil
		}
		select {
		case <-deadline.Done():
			file.Close()
			return nil, deadline.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func acquireComponentWriter(root string) (*os.File, error) {
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	if err := base.MkdirAll(filepath.Join("deps", "python-components"), 0700); err != nil {
		return nil, err
	}
	file, err := base.OpenFile(filepath.Join("deps", "python-components", ".native-update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok, err := tryComponentLock(file)
	if err != nil || !ok {
		file.Close()
		return nil, errors.New("update_transaction_busy")
	}
	return file, nil
}
func componentFileBytes(filename string, limit int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("component_file_invalid")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("component_file_invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(raw)) > limit {
		return nil, errors.New("component_file_invalid")
	}
	return raw, err
}
func sourceComponentEvidence(root string) bool {
	entries, err := os.ReadDir(filepath.Join(root, "deps", "python-components", "rag"))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	return len(entries) != 0
}
func exactComponentSelection(root string, entry PythonComponent) bool {
	if !pythonComponentInstalled(root, entry) {
		return false
	}
	if entry.CatalogURL == "" {
		return true
	}
	raw, err := componentFileBytes(pythonDescriptorPath(root, entry), 1024*1024)
	var selected PythonComponent
	return err == nil && json.Unmarshal(raw, &selected) == nil && reflect.DeepEqual(selected, entry)
}
func componentAtomicWrite(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".component-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return replaceComponentFile(name, path)
}

func RunPythonComponentMaintenance(ctx context.Context, action string, input []byte, environment PythonComponentMaintenanceContext) ([]byte, error) {
	result := func(status, reason string, extra map[string]any) ([]byte, error) {
		if extra == nil {
			extra = map[string]any{}
		}
		extra["status"] = status
		if reason != "" {
			extra["reason"] = reason
		}
		return json.Marshal(extra)
	}
	blocked := func(reason string) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return result("blocked", reason, nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var request struct {
		TransactionID string `json:"transactionId"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if len(input) > 4096 || decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || len(request.TransactionID) != 32 || (action != "stage" && action != "activate") {
		return blocked("update_request_invalid")
	}
	for _, path := range []string{environment.RuntimeRoot, environment.ResourcesRoot, environment.UpdaterRoot} {
		if !filepath.IsAbs(path) {
			return blocked("update_request_invalid")
		}
	}
	stateLock, err := componentMaintenanceStateLock(ctx, environment.UpdaterRoot)
	if err != nil {
		return blocked("update_transaction_busy")
	}
	writer, err := acquireComponentWriter(environment.RuntimeRoot)
	if err != nil {
		releaseComponentLock(stateLock)
		return blocked("update_transaction_busy")
	}
	defer releaseComponentLock(writer)
	// Actual writer owns the long lock. It never acquires a short/legacy lock again.
	raw, err := componentFileBytes(filepath.Join(environment.UpdaterRoot, "native-v2", "active.json"), 512*1024)
	var record componentMaintenanceRecord
	if err != nil || json.Unmarshal(raw, &record) != nil || record.Version != 2 || record.TransactionID != request.TransactionID || filepath.Clean(record.RuntimeRoot) != filepath.Clean(environment.RuntimeRoot) {
		releaseComponentLock(stateLock)
		return blocked("update_transaction_conflict")
	}
	targetRelease := record.TargetReleaseID
	if record.RepairTarget != nil {
		targetRelease = record.RepairTarget.ReleaseID
		record.TargetComponent = record.RepairTarget.TargetComponent
		record.ComponentsPath = record.RepairTarget.ComponentsPath
		record.ComponentsSHA256 = record.RepairTarget.ComponentsSHA256
		record.ComponentsSize = record.RepairTarget.ComponentsSize
	}
	if action == "stage" && !((record.Phase == "pending" && environment.ReleaseID == record.SourceReleaseID) || (record.Phase == "repair" && record.RepairTarget != nil && environment.ReleaseID == targetRelease)) || action == "activate" && (environment.ReleaseID != targetRelease || record.Phase == "pending") {
		releaseComponentLock(stateLock)
		return blocked("update_transaction_conflict")
	}
	releaseComponentLock(stateLock)
	if !record.SourceHadRAG {
		if record.ComponentMode != "absent" || sourceComponentEvidence(environment.RuntimeRoot) {
			return blocked("component_state_unknown")
		}
		return result(map[bool]string{true: "prepared", false: "activated"}[action == "stage"], "", map[string]any{"transactionId": request.TransactionID, "componentMode": "absent"})
	}
	if record.ComponentMode != "sync-target" || record.SourceComponent == nil || record.TargetComponent == nil {
		return blocked("component_state_unknown")
	}
	source, target := *record.SourceComponent, *record.TargetComponent
	if action == "stage" && record.Phase == "pending" && !pythonComponentInstalled(environment.RuntimeRoot, source) {
		return blocked("component_state_unknown")
	}
	if action == "stage" && record.Phase == "pending" && !exactComponentSelection(environment.RuntimeRoot, source) {
		return blocked("component_state_unknown")
	}
	if source.PythonABI != target.PythonABI || target.Platform != source.Platform || target.Arch != source.Arch || target.Platform != runtimePlatform() || target.Arch != runtimeArch() {
		return blocked("component_update_incompatible")
	}
	catalogBytes, err := componentFileBytes(record.ComponentsPath, 1024*1024)
	if err != nil {
		return blocked("artifact_size")
	}
	if int64(len(catalogBytes)) != record.ComponentsSize {
		return blocked("artifact_size")
	}
	sum := sha256.Sum256(catalogBytes)
	if hex.EncodeToString(sum[:]) != record.ComponentsSHA256 {
		return blocked("artifact_hash")
	}
	var catalog pythonCatalog
	if json.Unmarshal(catalogBytes, &catalog) != nil || catalog.SchemaVersion != 1 || catalog.Platform != target.Platform || catalog.Arch != target.Arch || !reflect.DeepEqual(catalog.Components["rag"], target) || !pythonComponentDigest.MatchString(target.Revision) || !pythonComponentDigest.MatchString(target.SHA256) || !pythonComponentDigest.MatchString(target.BaseFingerprint) || !pythonComponentDigest.MatchString(target.Tag) || target.SizeBytes <= 0 || target.UnpackedBytes <= 0 || target.Filename == "" || validateAssetMirrors(target.URL, target.FallbackURL, target.Filename) != nil {
		return blocked("component_update_incompatible")
	}
	stageRelative := filepath.Join("deps", "python-components", ".native-stages", request.TransactionID, "payload")
	stage := filepath.Join(environment.RuntimeRoot, stageRelative)
	final := pythonComponentDir(environment.RuntimeRoot, target)
	if action == "stage" {
		if info, err := os.Lstat(final); err == nil {
			repair := record.Phase == "repair" && record.RepairTarget != nil && pythonComponentDigest.MatchString(record.RepairTarget.AcceptanceDigest)
			installed := pythonComponentInstalled(environment.RuntimeRoot, target)
			if !info.IsDir() || (!installed && !repair) {
				return blocked("component_state_unknown")
			}
			// Explicit adoption starts with a fresh payload, preserving any old
			// revision evidence. A same-transaction activation checkpoint may reuse
			// its already verified final payload and still runs the target probe.
			if repair && (!installed || record.ComponentStage == nil) {
				if err := preserveRepairComponent(environment.RuntimeRoot, request.TransactionID, target); err != nil {
					return blocked("component_state_unknown")
				}
			} else {
				stageRelative, _ = filepath.Rel(environment.RuntimeRoot, final)
				return result("prepared", "", map[string]any{"transactionId": request.TransactionID, "componentMode": "sync-target", "stagePath": stageRelative, "revision": target.Revision, "sha256": target.SHA256})
			}
		} else if !os.IsNotExist(err) {
			return blocked("component_state_unknown")
		}
		free, err := componentFreeSpace(environment.RuntimeRoot)
		required := uint64(target.SizeBytes) + uint64(target.UnpackedBytes) + 256*1024*1024
		if err != nil || free < required {
			return blocked("component_space_insufficient")
		}
		root, err := os.OpenRoot(environment.RuntimeRoot)
		if err != nil {
			return blocked("component_state_unknown")
		}
		defer root.Close()
		if err := root.MkdirAll(filepath.Dir(stageRelative), 0700); err != nil {
			return blocked("component_state_unknown")
		}
		archive := filepath.Join(filepath.Dir(stage), "component.zip")
		// A retry discards only this transaction's incomplete private payload.
		if err := root.RemoveAll(stageRelative); err != nil {
			return blocked("component_state_unknown")
		}
		client := environment.Client
		if client == nil {
			client = &http.Client{Timeout: 30 * time.Minute}
		}
		if err := downloadFixedComponent(ctx, client, target, archive); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return blocked("artifact_hash")
		}
		if err := extractPythonComponent(ctx, archive, stage, target.UnpackedBytes); err != nil {
			return blocked("component_archive_invalid")
		}
		if err := validatePythonComponent(stage, target); err != nil {
			return blocked("component_archive_invalid")
		}
		if err := syncComponentTree(stage); err != nil {
			return blocked("component_state_unknown")
		}
		return result("prepared", "", map[string]any{"transactionId": request.TransactionID, "componentMode": "sync-target", "stagePath": stageRelative, "revision": target.Revision, "sha256": target.SHA256})
	}
	if record.ComponentStage == nil || !filepath.IsLocal(record.ComponentStage.StagePath) || record.ComponentStage.Revision != target.Revision || record.ComponentStage.SHA256 != target.SHA256 {
		return blocked("component_state_unknown")
	}
	stageRelative = record.ComponentStage.StagePath
	stage = filepath.Join(environment.RuntimeRoot, stageRelative)
	if filepath.Clean(stage) != filepath.Clean(final) && !strings.HasPrefix(stageRelative, filepath.Join("deps", "python-components", ".native-stages", request.TransactionID)+string(filepath.Separator)) {
		return blocked("component_state_unknown")
	}
	if err := validatePythonComponent(stage, target); err != nil {
		if !pythonComponentInstalled(environment.RuntimeRoot, target) {
			return blocked("component_state_unknown")
		}
		stage = final
	}
	if info, err := os.Lstat(final); err == nil && (!info.IsDir() || !pythonComponentInstalled(environment.RuntimeRoot, target)) {
		return blocked("component_state_unknown")
	}
	if err := verifyTargetComponent(ctx, stage, target, environment); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return blocked("component_target_verification_failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !pythonComponentInstalled(environment.RuntimeRoot, target) {
		if err := componentAtomicWrite(filepath.Join(stage, ".ready"), []byte(target.SHA256+"\n")); err != nil {
			return blocked("component_state_unknown")
		}
		root, err := os.OpenRoot(environment.RuntimeRoot)
		if err != nil {
			return blocked("component_state_unknown")
		}
		defer root.Close()
		finalRelative, _ := filepath.Rel(environment.RuntimeRoot, final)
		if err := root.MkdirAll(filepath.Dir(finalRelative), 0700); err != nil {
			return blocked("component_state_unknown")
		}
		if err := root.Rename(stageRelative, finalRelative); err != nil {
			return blocked("component_state_unknown")
		}
	}
	descriptor, _ := json.Marshal(target)
	if err := componentAtomicWrite(pythonDescriptorPath(environment.RuntimeRoot, target), descriptor); err != nil {
		return blocked("component_state_unknown")
	}
	return result("activated", "", map[string]any{"transactionId": request.TransactionID, "componentMode": "sync-target", "revision": target.Revision, "sha256": target.SHA256})
}

func preserveRepairComponent(runtimeRoot, tx string, target PythonComponent) error {
	// Every ancestor below the selected runtime must be a real directory. An
	// alias or unexpected object cannot identify the revision we may preserve.
	path := runtimeRoot
	for _, part := range []string{"deps", "python-components", "rag", target.Revision} {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("component_state_unknown")
		}
	}
	root, err := os.OpenRoot(runtimeRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	parent := filepath.Join("deps", "python-components", ".native-stages", tx, "preserved")
	if err := root.MkdirAll(parent, 0700); err != nil {
		return err
	}
	from := filepath.Join("deps", "python-components", "rag", target.Revision)
	to := filepath.Join(parent, target.Revision+"-"+hex.EncodeToString(nonce[:]))
	if err := root.Rename(from, to); err != nil {
		return err
	}
	if err := syncComponentDirectory(filepath.Join(runtimeRoot, filepath.Dir(from))); err != nil {
		return err
	}
	return syncComponentDirectory(filepath.Join(runtimeRoot, parent))
}

func runtimePlatform() string { return runtime.GOOS }
func runtimeArch() string     { return runtime.GOARCH }

func downloadFixedComponent(ctx context.Context, client *http.Client, entry PythonComponent, destination string) error {
	return downloadMirroredAsset(ctx, client, entry.URL, entry.FallbackURL, entry.Filename, destination, entry.SizeBytes, entry.SHA256, primaryMirrorPolicy)
}
func syncComponentTree(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("component_path_invalid")
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		return file.Sync()
	})
}

type boundedComponentOutput struct{ bytes int }

func (w *boundedComponentOutput) Write(raw []byte) (int, error) {
	w.bytes += len(raw)
	return len(raw), nil
}

func verifyTargetComponent(ctx context.Context, dir string, entry PythonComponent, environment PythonComponentMaintenanceContext) error {
	if !filepath.IsAbs(environment.Python) || !filepath.IsAbs(environment.AlgorithmSource) {
		return errors.New("component_target_verification_failed")
	}
	// Reconstruct the build fingerprint from the actual target base plus overlay
	// metadata. Import checks load native extensions under that actual interpreter.
	code := `import hashlib, importlib.metadata as metadata, json, os, platform, re, site, sys, sysconfig
payload, expected_json, source, runtime_root, probe_home = sys.argv[1:]
expected = json.loads(expected_json)
abi = f"cp{sys.version_info.major}{sys.version_info.minor}"
host = "windows" if sys.platform == "win32" else sys.platform
arch = "amd64" if platform.machine().lower() in {"amd64", "x86_64"} else "arm64"
assert (abi, host, arch) == (expected["pythonAbi"], expected["platform"], expected["arch"])
os.environ["LAZYLLM_HOME"] = probe_home
os.environ["HF_HUB_OFFLINE"] = "1"
os.environ["TRANSFORMERS_OFFLINE"] = "1"
os.environ["PIP_NO_INDEX"] = "1"
base_site = sysconfig.get_path("purelib")
site.addsitedir(payload)
sources = os.path.join(source, "lazyllm")
if os.path.isdir(os.path.join(sources, "lazyllm")):
    sys.path.insert(0, sources)
distributions = {}
for distribution in metadata.distributions(path=[base_site, payload]):
    name = distribution.metadata.get("Name")
    if name:
        distributions[re.sub(r"[-_.]+", "-", name).lower()] = distribution
versions = {name: d.version for name, d in distributions.items()}
records = {name: hashlib.sha256((d.read_text("RECORD") or "").encode()).hexdigest() for name, d in distributions.items()}
identity = {"schemaVersion": 1, "platform": host, "arch": arch, "pythonAbi": abi}
encoded = (json.dumps({"identity": identity, "versions": versions, "records": records}, sort_keys=True, separators=(",", ":")) + "\n").encode()
assert hashlib.sha256(encoded).hexdigest() == expected["baseFingerprint"]
for name, version in expected["packages"].items():
    assert versions[re.sub(r"[-_.]+", "-", name).lower()] == version
import lazyllm.tools.rag, pymilvus, milvus_lite, spacy
`
	raw, _ := json.Marshal(entry)
	probeHome, err := os.MkdirTemp(filepath.Join(environment.RuntimeRoot, "deps", "python-components"), ".probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probeHome)
	checkCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	command := exec.CommandContext(checkCtx, environment.Python, "-I", "-B", "-c", code, filepath.Join(dir, "site-packages"), string(raw), environment.AlgorithmSource, environment.ResourcesRoot, probeHome)
	configureComponentProbe(command)
	output := &boundedComponentOutput{}
	command.Stdout = output
	command.Stderr = output
	err = command.Run() // Wait includes stdio close before the writing lock releases.
	if checkCtx.Err() != nil {
		return checkCtx.Err()
	}
	return err
}

func ComponentUpdaterRoot() (string, error) { return componentUpdaterRoot() }

func RunIndependentPythonComponent(ctx context.Context, environment PythonComponentMaintenanceContext) ([]byte, error) {
	emit := func(status, reason string) ([]byte, error) {
		return json.Marshal(map[string]string{"status": status, "reason": reason})
	}
	state, err := componentMaintenanceStateLock(ctx, environment.UpdaterRoot)
	if err != nil {
		return emit("blocked", "update_transaction_busy")
	}
	file, err := acquireComponentWriter(environment.RuntimeRoot)
	if err != nil {
		releaseComponentLock(state)
		return emit("blocked", "update_transaction_busy")
	}
	defer releaseComponentLock(file)
	if _, err := os.Lstat(filepath.Join(environment.UpdaterRoot, "native-v2", "active.json")); !os.IsNotExist(err) {
		releaseComponentLock(state)
		return emit("blocked", "update_transaction_busy")
	}
	releaseComponentLock(state)
	raw, err := componentFileBytes(filepath.Join(environment.ResourcesRoot, "config", "python-components.json"), 1024*1024)
	var catalog pythonCatalog
	if err != nil || json.Unmarshal(raw, &catalog) != nil {
		return emit("blocked", "component_state_unknown")
	}
	entry, ok := catalog.Components["rag"]
	if !ok {
		return emit("blocked", "component_state_unknown")
	}
	_ = os.Setenv("LAZYMIND_ALGORITHM_PYTHON", environment.Python)
	_ = os.Setenv("LAZYMIND_ALGORITHM_SOURCE", environment.AlgorithmSource)
	if err := installPythonComponent(ctx, environment.RuntimeRoot, entry, entry.URL); err != nil {
		return emit("blocked", "component_install_failed")
	}
	return emit("installed", "")
}

type boundedComponentResponse struct{ raw []byte }

func (w *boundedComponentResponse) Write(raw []byte) (int, error) {
	n := len(raw)
	if len(w.raw) < 1024*1024 {
		left := 1024*1024 - len(w.raw)
		if len(raw) > left {
			raw = raw[:left]
		}
		w.raw = append(w.raw, raw...)
	}
	return n, nil
}

var componentStateNamespace = "LazyMindUpdater"
