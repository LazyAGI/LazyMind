package systemdeps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"
)

type PythonComponentMaintenanceContext struct {
	RuntimeRoot, ResourcesRoot, UpdaterRoot string
	ReleaseID, Python, AlgorithmSource      string
	Client                                  *http.Client
}

type componentMaintenanceUpdate struct {
	Source PythonComponent `json:"source"`
	Target PythonComponent `json:"target"`
}

type componentMaintenanceStageItem struct {
	StagePath string `json:"stagePath"`
	Revision  string `json:"revision"`
	SHA256    string `json:"sha256"`
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
	ComponentUpdates                 map[string]componentMaintenanceUpdate `json:"componentUpdates"`
	ComponentsPath                   string                                `json:"componentsPath"`
	ComponentsSHA256                 string                                `json:"componentsSHA256"`
	ComponentsSize                   int64                                 `json:"componentsSize"`
	ComponentStage                   *struct {
		componentMaintenanceStageItem
		Components map[string]componentMaintenanceStageItem `json:"components"`
	} `json:"componentStage"`
	RepairTarget *struct {
		AcceptanceDigest string                     `json:"acceptanceDigest"`
		ReleaseID        string                     `json:"releaseId"`
		TargetComponent  *PythonComponent           `json:"targetComponent"`
		TargetComponents map[string]PythonComponent `json:"targetComponents"`
		ComponentsPath   string                     `json:"componentsPath"`
		ComponentsSHA256 string                     `json:"componentsSHA256"`
		ComponentsSize   int64                      `json:"componentsSize"`
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
func sourceComponentEvidence(root, id string) bool {
	entries, err := os.ReadDir(filepath.Join(root, "deps", "python-components", id))
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
	if id, err := hex.DecodeString(request.TransactionID); err != nil || hex.EncodeToString(id) != request.TransactionID {
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
		record.ComponentsPath = record.RepairTarget.ComponentsPath
		record.ComponentsSHA256 = record.RepairTarget.ComponentsSHA256
		record.ComponentsSize = record.RepairTarget.ComponentsSize
	}
	if action == "stage" && !((record.Phase == "pending" && environment.ReleaseID == record.SourceReleaseID) || (record.Phase == "repair" && record.RepairTarget != nil && environment.ReleaseID == targetRelease)) || action == "activate" && (environment.ReleaseID != targetRelease || record.Phase == "pending") {
		releaseComponentLock(stateLock)
		return blocked("update_transaction_conflict")
	}
	releaseComponentLock(stateLock)

	updates, err := record.effectiveUpdates()
	if err != nil {
		return blocked("component_state_unknown")
	}
	response := map[string]any{"transactionId": request.TransactionID, "componentMode": record.ComponentMode}
	status := "activated"
	if action == "stage" {
		status = "prepared"
	}
	for _, id := range pythonComponentIDs {
		if _, installed := updates[id]; !installed && sourceComponentEvidence(environment.RuntimeRoot, id) {
			return blocked("component_state_unknown")
		}
	}
	if len(updates) == 0 {
		return result(status, "", response)
	}
	catalog, err := readMaintenanceCatalog(record)
	if err != nil {
		return blocked(err.Error())
	}
	ids := slices.Sorted(maps.Keys(updates))
	for _, id := range ids {
		update := updates[id]
		source, target := update.Source, update.Target
		if action == "stage" && record.Phase == "pending" && !exactComponentSelection(environment.RuntimeRoot, source) {
			return blocked("component_state_unknown")
		}
		if source.PythonABI != target.PythonABI || target.Platform != source.Platform || target.Arch != source.Arch ||
			!reflect.DeepEqual(catalog.Components[id], target) {
			return blocked("component_update_incompatible")
		}
	}

	root, err := os.OpenRoot(environment.RuntimeRoot)
	if err != nil {
		return blocked("component_state_unknown")
	}
	defer root.Close()
	stages := make(map[string]componentMaintenanceStageItem, len(ids))
	probes := make(map[string]componentProbeInput, len(ids))
	legacy := record.ComponentUpdates == nil
	if action == "activate" && (record.ComponentStage == nil || (!legacy && len(record.ComponentStage.Components) != len(updates)) || (legacy && len(record.ComponentStage.Components) != 0)) {
		return blocked("component_state_unknown")
	}
	for _, id := range ids {
		target := updates[id].Target
		if action == "stage" {
			stage, err := stageTargetComponent(ctx, environment, root, record, target)
			if err != nil {
				return blocked(err.Error())
			}
			stages[id] = stage
		} else {
			stage := record.ComponentStage.Components[id]
			if legacy {
				stage = record.ComponentStage.componentMaintenanceStageItem
			}
			path, err := resolveTargetComponentStage(environment.RuntimeRoot, request.TransactionID, stage, target)
			if err != nil {
				return blocked(err.Error())
			}
			probes[id] = componentProbeInput{Path: filepath.Join(path, "site-packages"), Descriptor: target}
			stages[id] = stage
		}
	}
	if action == "activate" {
		if err := verifyTargetComponents(ctx, probes, catalog.RuntimeBaseFingerprint, environment); err != nil {
			return blocked("component_target_verification_failed")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := activateTargetComponents(environment.RuntimeRoot, root, stages, updates, ids); err != nil {
			return blocked("component_state_unknown")
		}
	}
	if legacy {
		stage := stages["rag"]
		if action == "stage" {
			response["stagePath"] = stage.StagePath
		}
		response["revision"], response["sha256"] = stage.Revision, stage.SHA256
	} else if action == "stage" {
		response["components"] = stages
	}
	return result(status, "", response)
}

func (r componentMaintenanceRecord) effectiveUpdates() (map[string]componentMaintenanceUpdate, error) {
	invalid := errors.New("component_state_unknown")
	updates := maps.Clone(r.ComponentUpdates)
	if updates == nil {
		updates = map[string]componentMaintenanceUpdate{}
		if r.SourceComponent != nil && r.TargetComponent != nil {
			updates["rag"] = componentMaintenanceUpdate{Source: *r.SourceComponent, Target: *r.TargetComponent}
		} else if r.SourceComponent != nil || r.TargetComponent != nil {
			return nil, invalid
		}
	} else if r.SourceComponent != nil || r.TargetComponent != nil {
		return nil, invalid
	}
	if repair := r.RepairTarget; repair != nil {
		if !pythonComponentDigest.MatchString(repair.AcceptanceDigest) {
			return nil, invalid
		}
		targets := repair.TargetComponents
		if repair.TargetComponent != nil {
			if targets != nil {
				return nil, invalid
			}
			targets = map[string]PythonComponent{"rag": *repair.TargetComponent}
		}
		if len(targets) != len(updates) {
			return nil, invalid
		}
		for id, update := range updates {
			update.Target = targets[id]
			updates[id] = update
		}
	}
	_, hasRAG := updates["rag"]
	if r.SourceHadRAG != hasRAG || (hasRAG && r.ComponentMode != "sync-target") || (!hasRAG && r.ComponentMode != "absent") {
		return nil, invalid
	}
	for id, update := range updates {
		source, target := update.Source, update.Target
		if pythonComponentRuleFor(id) == nil || source.ID != id || target.ID != id ||
			!pythonComponentDigest.MatchString(source.Revision) || (source.CatalogURL != "" && !pythonComponentDigest.MatchString(source.Tag)) {
			return nil, invalid
		}
	}
	return updates, nil
}

type maintenanceCatalog struct {
	pythonCatalog
	RuntimeBaseFingerprint string `json:"runtimeBaseFingerprint"`
}

func readMaintenanceCatalog(record componentMaintenanceRecord) (maintenanceCatalog, error) {
	var catalog maintenanceCatalog
	raw, err := componentFileBytes(record.ComponentsPath, 1024*1024)
	if err != nil || int64(len(raw)) != record.ComponentsSize {
		return catalog, errors.New("artifact_size")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != record.ComponentsSHA256 {
		return catalog, errors.New("artifact_hash")
	}
	incompatible := errors.New("component_update_incompatible")
	if json.Unmarshal(raw, &catalog) != nil || catalog.SchemaVersion != 1 || catalog.Platform != runtime.GOOS || catalog.Arch != runtime.GOARCH ||
		(catalog.RuntimeBaseFingerprint != "" && !pythonComponentDigest.MatchString(catalog.RuntimeBaseFingerprint)) {
		return catalog, incompatible
	}
	for id, target := range catalog.Components {
		if pythonComponentRuleFor(id) == nil || target.ID != id || target.Platform != catalog.Platform ||
			target.Arch != catalog.Arch || target.PythonABI == "" || !pythonComponentDigest.MatchString(target.Revision) ||
			!pythonComponentDigest.MatchString(target.SHA256) || !pythonComponentDigest.MatchString(target.BaseFingerprint) ||
			!pythonComponentDigest.MatchString(target.Tag) || target.SizeBytes <= 0 || target.UnpackedBytes <= 0 ||
			target.Filename == "" || validateAssetMirrors(target.URL, target.FallbackURL, target.Filename) != nil {
			return catalog, incompatible
		}
	}
	return catalog, nil
}

func stageTargetComponent(ctx context.Context, environment PythonComponentMaintenanceContext, root *os.Root, record componentMaintenanceRecord, target PythonComponent) (result componentMaintenanceStageItem, stageErr error) {
	result = componentMaintenanceStageItem{Revision: target.Revision, SHA256: target.SHA256}
	invalid := errors.New("component_state_unknown")
	final := pythonComponentDir(environment.RuntimeRoot, target)
	if info, err := os.Lstat(final); err == nil {
		repair := record.Phase == "repair" && record.RepairTarget != nil
		installed := pythonComponentInstalled(environment.RuntimeRoot, target)
		if !info.IsDir() || (!installed && !repair) {
			return result, invalid
		}
		// Keep a completed payload on retry. An adopted repair without a prior
		// stage still retains the original evidence and downloads the fixed ZIP.
		if repair && (!installed || record.ComponentStage == nil) {
			if err := preserveRepairComponent(environment.RuntimeRoot, record.TransactionID, target); err != nil {
				return result, invalid
			}
		} else {
			result.StagePath, _ = filepath.Rel(environment.RuntimeRoot, final)
			return result, nil
		}
	} else if !os.IsNotExist(err) {
		return result, invalid
	}
	free, err := componentFreeSpace(environment.RuntimeRoot)
	required := uint64(target.SizeBytes) + uint64(target.UnpackedBytes) + 256*1024*1024
	if err != nil || free < required {
		return result, errors.New("component_space_insufficient")
	}
	stageDir := filepath.Join("deps", "python-components", ".native-stages", record.TransactionID)
	if record.ComponentUpdates != nil {
		stageDir = filepath.Join(stageDir, target.ID)
	}
	result.StagePath = filepath.Join(stageDir, "payload")
	if err := root.MkdirAll(stageDir, 0700); err != nil {
		return result, invalid
	}
	stage := filepath.Join(environment.RuntimeRoot, result.StagePath)
	archivePath := filepath.Join(stageDir, "component.zip")
	if err := root.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return result, invalid
	}
	archive := filepath.Join(environment.RuntimeRoot, archivePath)
	defer func() {
		if err := root.Remove(archivePath); stageErr == nil && err != nil && !os.IsNotExist(err) {
			stageErr = invalid
		}
	}()
	// A retry discards only the incomplete private payload of this component.
	if err := root.RemoveAll(result.StagePath); err != nil {
		return result, invalid
	}
	client := environment.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	if err := downloadFixedComponent(ctx, client, target, archive); err != nil {
		return result, errors.New("artifact_hash")
	}
	if err := extractPythonComponent(ctx, archive, stage, target.UnpackedBytes); err != nil {
		return result, errors.New("component_archive_invalid")
	}
	if err := validatePythonComponent(stage, target); err != nil {
		return result, errors.New("component_archive_invalid")
	}
	if err := syncComponentTree(stage); err != nil {
		return result, invalid
	}
	return result, nil
}

func resolveTargetComponentStage(runtimeRoot, tx string, stage componentMaintenanceStageItem, target PythonComponent) (string, error) {
	invalid := errors.New("component_state_unknown")
	if !filepath.IsLocal(stage.StagePath) || stage.Revision != target.Revision || stage.SHA256 != target.SHA256 {
		return "", invalid
	}
	path := filepath.Join(runtimeRoot, stage.StagePath)
	final := pythonComponentDir(runtimeRoot, target)
	if path != final && !strings.HasPrefix(stage.StagePath, filepath.Join("deps", "python-components", ".native-stages", tx)+string(filepath.Separator)) {
		return "", invalid
	}
	if err := validatePythonComponent(path, target); err != nil {
		if !pythonComponentInstalled(runtimeRoot, target) {
			return "", invalid
		}
		path = final
	}
	if info, err := os.Lstat(final); err == nil && (!info.IsDir() || !pythonComponentInstalled(runtimeRoot, target)) {
		return "", invalid
	}
	return path, nil
}

func activateTargetComponents(runtimeRoot string, root *os.Root, stages map[string]componentMaintenanceStageItem, updates map[string]componentMaintenanceUpdate, ids []string) error {
	type receipt struct {
		path           string
		previous, next []byte
	}
	receipts := make([]receipt, 0, len(ids))
	for _, id := range ids {
		target := updates[id].Target
		path := pythonDescriptorPath(runtimeRoot, target)
		var previous []byte
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			previous, err = componentFileBytes(path, 1024*1024)
			if err != nil {
				return err
			}
		}
		next, err := json.Marshal(target)
		if err != nil {
			return err
		}
		receipts = append(receipts, receipt{path: path, previous: previous, next: next})
		if pythonComponentInstalled(runtimeRoot, target) {
			continue
		}
		stage := stages[id]
		stageDir := filepath.Join(runtimeRoot, stage.StagePath)
		if err := componentAtomicWrite(filepath.Join(stageDir, ".ready"), []byte(target.SHA256+"\n")); err != nil {
			return err
		}
		final := pythonComponentDir(runtimeRoot, target)
		finalRelative, _ := filepath.Rel(runtimeRoot, final)
		if err := root.MkdirAll(filepath.Dir(finalRelative), 0700); err != nil {
			return err
		}
		if err := root.Rename(stage.StagePath, finalRelative); err != nil {
			return err
		}
	}
	// Keep source selections until all verified payloads have reached their final
	// paths. A failed receipt write can occur after rename, so restore that
	// receipt too. The intent remains active and can retry from the ready cache.
	for i, selection := range receipts {
		if err := componentAtomicWrite(selection.path, selection.next); err != nil {
			for _, written := range receipts[:i+1] {
				if written.previous != nil {
					err = errors.Join(err, componentAtomicWrite(written.path, written.previous))
				} else if removeErr := os.Remove(written.path); !os.IsNotExist(removeErr) {
					err = errors.Join(err, removeErr)
				}
			}
			return err
		}
	}
	return nil
}

func preserveRepairComponent(runtimeRoot, tx string, target PythonComponent) error {
	// Every ancestor below the selected runtime must be a real directory. An
	// alias or unexpected object cannot identify the revision we may preserve.
	path := runtimeRoot
	for _, part := range []string{"deps", "python-components", target.ID, target.Revision} {
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
	from := filepath.Join("deps", "python-components", target.ID, target.Revision)
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

type componentProbeInput struct {
	Path        string          `json:"-"`
	Descriptor  PythonComponent `json:"descriptor"`
	Modules     []string        `json:"modules"`
	FromOverlay bool            `json:"fromOverlay"`
}

func verifyTargetComponents(ctx context.Context, probes map[string]componentProbeInput, runtimeBaseFingerprint string, environment PythonComponentMaintenanceContext) error {
	if !filepath.IsAbs(environment.Python) || !filepath.IsAbs(environment.AlgorithmSource) {
		return errors.New("component_target_verification_failed")
	}
	code := `import hashlib, importlib, importlib.metadata as metadata, json, os, platform, re, site, sys, sysconfig
from pathlib import Path
expected_json, base_fingerprint, source, probe_home, *payloads = sys.argv[1:]
expected = json.loads(expected_json)
abi = f"cp{sys.version_info.major}{sys.version_info.minor}"
host = "windows" if sys.platform == "win32" else sys.platform
arch = "amd64" if platform.machine().lower() in {"amd64", "x86_64"} else "arm64"
identity = {"schemaVersion": 1, "platform": host, "arch": arch, "pythonAbi": abi}
def normalized_name(name):
    return re.sub(r"[-_.]+", "-", name).lower()
def distributions(paths):
    return {normalized_name(d.metadata["Name"]): d for d in metadata.distributions(path=paths) if d.metadata.get("Name")}
def fingerprint(paths):
    installed = distributions(paths)
    versions = {name: d.version for name, d in installed.items()}
    records = {name: hashlib.sha256((d.read_text("RECORD") or "").encode()).hexdigest() for name, d in installed.items()}
    encoded = (json.dumps({"identity": identity, "versions": versions, "records": records}, sort_keys=True, separators=(",", ":")) + "\n").encode()
    return hashlib.sha256(encoded).hexdigest()
os.environ["LAZYLLM_HOME"] = probe_home
os.environ["NUMBA_CACHE_DIR"] = os.path.join(probe_home, "numba")
os.environ["HF_HUB_OFFLINE"] = "1"
os.environ["TRANSFORMERS_OFFLINE"] = "1"
os.environ["PIP_NO_INDEX"] = "1"
base_sites = [sysconfig.get_path("purelib")]
# Shared wheels were part of the build fingerprint before the builder moved
# them out of purelib. Include only active paths in the bundled shared layout.
prefix = Path(sys.prefix)
shared_root = prefix.resolve().parent / "shared"
shared_roots = {shared_root, prefix.parent / "shared"}
for entry in sys.path:
    path = Path(entry)
    if path.parent not in shared_roots or not re.fullmatch(r"[a-f0-9]{64}", path.name):
        continue
    resolved = path.resolve()
    if resolved == shared_root / path.name and str(resolved) not in base_sites:
        base_sites.append(str(resolved))
if base_fingerprint:
    assert fingerprint(base_sites) == base_fingerprint
else:
    legacy_fingerprint = fingerprint([*base_sites, *payloads])
    assert all(legacy_fingerprint == probe["descriptor"]["baseFingerprint"] for probe in expected)
sources = os.path.join(source, "lazyllm")
if os.path.isdir(os.path.join(sources, "lazyllm")):
    sys.path.insert(0, sources)
for payload in payloads:
    site.addsitedir(payload)
for probe, payload in zip(expected, payloads):
    descriptor = probe["descriptor"]
    assert (abi, host, arch) == (descriptor["pythonAbi"], descriptor["platform"], descriptor["arch"])
    overlay = distributions([payload])
    for name, version in descriptor["packages"].items():
        assert overlay[normalized_name(name)].version == version
    for name in probe["modules"]:
        module = importlib.import_module(name)
        if probe["fromOverlay"]:
            root = os.path.normcase(os.path.realpath(payload))
            loaded = os.path.normcase(os.path.realpath(module.__file__))
            assert os.path.commonpath((root, loaded)) == root
`
	var payloads []string
	var expected []componentProbeInput
	var timeout time.Duration
	for _, id := range slices.Sorted(maps.Keys(probes)) {
		probe, rule := probes[id], pythonComponentRuleFor(id)
		if rule == nil {
			return errors.New("component_target_verification_failed")
		}
		payloads = append(payloads, probe.Path)
		probe.Modules = rule.modules
		probe.FromOverlay = rule.fromOverlay
		expected = append(expected, probe)
		timeout = max(timeout, rule.timeout)
	}
	raw, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	probeHome, err := os.MkdirTemp(filepath.Join(environment.RuntimeRoot, "deps", "python-components"), ".probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probeHome)
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"-I", "-B", "-c", code, string(raw), runtimeBaseFingerprint, environment.AlgorithmSource, probeHome}, payloads...)
	command := exec.CommandContext(checkCtx, environment.Python, args...)
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
