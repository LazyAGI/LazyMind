package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type RuntimeUpdateBootstrap struct {
	TransactionID string                         `json:"transactionId"`
	Owner         string                         `json:"owner"`
	ReleaseID     string                         `json:"releaseId"`
	DataProfile   string                         `json:"dataProfile"`
	Storage       *UpdateStorageSnapshot         `json:"storage"`
	ExpectedPeers []string                       `json:"expectedPeers"`
	Actor         *desktopupdate.ProcessIdentity `json:"actor,omitempty"`
}

func bootstrapTransaction(cfg RuntimeConfig) string {
	if cfg.UpdateBootstrap != nil {
		return cfg.UpdateBootstrap.TransactionID
	}
	return ""
}
func updateBootstrapEnvironment(cfg RuntimeConfig) []string {
	if cfg.UpdateBootstrap == nil {
		if cfg.clearInheritedBootstrap {
			return []string{"LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION=", "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=", "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE="}
		}
		return nil
	}
	raw, _ := json.Marshal(cfg.UpdateBootstrap.Storage)
	return []string{"LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION=" + cfg.UpdateBootstrap.TransactionID, "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=" + cfg.UpdateBootstrap.Owner, "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE=" + string(raw)}
}
func readNativeConfiguration(ctx context.Context, paths RuntimePaths) (desktopupdate.Config, error) {
	var result struct {
		Status string
		Config desktopupdate.Config
	}
	// Allow the helper's bounded 60-second signature verification to finish.
	bounded, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, filepath.Join(paths.ResourcesRoot, "bin", executableName("lazymind-update-helper")), "verify-identity")
	raw, err := cmd.Output()
	if err != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &result) != nil || result.Status != "enabled" || desktopupdate.ValidateConfig(result.Config) != nil {
		return result.Config, updateStateFailure("native_install_unavailable")
	}
	return result.Config, nil
}
func nativeIdentityMatches(record *desktopupdate.NativeRecord, c desktopupdate.Config, paths RuntimePaths) bool {
	return c.TrustDomain == record.TrustDomain && c.Audience == record.Audience && c.Channel == record.Channel && c.SigningIdentity == record.SigningIdentity && c.Platform == record.Platform && c.Arch == record.Arch && sameRuntimePath(paths.ResourcesRoot, record.InstallationRoot) && sameRuntimePath(paths.RuntimeRoot, record.RuntimeRoot)
}
func nativeStorageSnapshot(record *desktopupdate.NativeRecord) *UpdateStorageSnapshot {
	return &UpdateStorageSnapshot{Version: 2, Databases: record.SourceStorage.Databases, SegmentStore: record.SourceStorage.SegmentStore, VectorEngine: record.SourceStorage.VectorEngine, VectorPath: record.SourceStorage.VectorPath, LayoutSource: record.SourceStorage.LayoutSource, OptionalAbsent: append([]string(nil), record.SourceStorage.OptionalAbsent...)}
}
func compareNativeStorage(cfg RuntimeConfig, record *desktopupdate.NativeRecord) error {
	if cfg.UpdateStorage == nil || len(cfg.UpdateStorage.Databases) != 9 || cfg.UpdateStorage.SegmentStore != record.SourceStorage.SegmentStore || cfg.ModeProfile.VectorStore.Engine != record.SourceStorage.VectorEngine || !sameRuntimePath(cfg.ModeProfile.VectorStore.DBPath, record.SourceStorage.VectorPath) {
		return updateStateFailure("update_storage_mismatch")
	}
	for family, path := range record.SourceStorage.Databases {
		if !sameRuntimePath(cfg.UpdateStorage.Databases[family], path) {
			return updateStateFailure("update_storage_mismatch")
		}
	}
	return nil
}
func nativeScopeIntersects(cfg RuntimeConfig, paths RuntimePaths, record *desktopupdate.NativeRecord) bool {
	scope, err := runtimeUpdateScope(cfg, paths)
	if err != nil {
		return true
	}
	intersects, err := desktopupdate.ScopesIntersect(scope, record.Scope)
	return err != nil || intersects
}

func guardNativeMutation(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return err
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		return err
	}
	if record != nil && nativeScopeIntersects(cfg, paths, record) {
		return updateStateFailure("update_incomplete")
	}
	return ctx.Err()
}
func bindNativeStartup(ctx context.Context, cfg *RuntimeConfig, paths *RuntimePaths, tx, owner string) error {
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return err
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		return err
	}
	if record == nil {
		if tx != "" {
			return updateStateFailure("update_incomplete")
		}
		return nil
	}
	if !nativeScopeIntersects(*cfg, *paths, record) {
		return nil
	}
	identity, err := readNativeConfiguration(ctx, *paths)
	if err != nil {
		return err
	}
	if !nativeIdentityMatches(record, identity, *paths) {
		return updateStateFailure("update_transaction_conflict")
	}
	scope, err := runtimeUpdateScope(*cfg, *paths)
	if err != nil {
		return err
	}
	admitted, err := desktopupdate.NativeStartupPermission(ctx, root, scope, identity.ReleaseID, tx)
	if err != nil {
		return err
	}
	if admitted == nil {
		return nil
	}
	if owner == "" || owner != cfg.OwnerToken {
		return updateStateFailure("update_transaction_conflict")
	}
	if err := resolveBootstrapStorage(cfg, paths, record); err != nil {
		return err
	}
	if err := compareNativeStorage(*cfg, record); err != nil {
		return err
	}
	profile, dirty, err := inspectStoredUpdateFormat(ctx, nativeStorageSnapshot(record), !record.SourceHadRAG)
	if err != nil {
		return err
	}
	release, _, want, _ := record.EffectiveTarget()
	if dirty || profile != want {
		return updateStateFailure("update_startup_failed")
	}
	cfg.UpdateStorage = nativeStorageSnapshot(record)
	cfg.UpdateBootstrap = &RuntimeUpdateBootstrap{TransactionID: tx, Owner: owner, ReleaseID: release, DataProfile: profile, Storage: cloneUpdateStorage(cfg.UpdateStorage), ExpectedPeers: append([]string(nil), record.ExpectedPeers...), Actor: record.TargetActor}
	return nil
}
func nativeShellStartup(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return err
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil || record == nil {
		return err
	}
	if !nativeScopeIntersects(cfg, paths, record) {
		return nil
	}
	identity, err := readNativeConfiguration(ctx, paths)
	if err != nil {
		return err
	}
	if !nativeIdentityMatches(record, identity, paths) {
		return updateStateFailure("update_transaction_conflict")
	}
	// Shell admission is only for diagnostics/maintenance. Normal up separately
	// requires an activated record and exact effective target.
	return nil
}
func localUpdateSystemVersion(ctx context.Context) (string, error) {
	var raw []byte
	var err error
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if runtime.GOOS == "darwin" {
		raw, err = exec.CommandContext(bounded, "/usr/bin/sw_vers", "-productVersion").Output()
	} else if runtime.GOOS == "windows" {
		raw, err = exec.CommandContext(bounded, "cmd.exe", "/c", "ver").Output()
	} else {
		return "", updateStateFailure("unsupported_storage")
	}
	if err != nil {
		return "", updateStateFailure("system_unknown")
	}
	version := regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?`).FindString(string(raw))
	if strings.Count(version, ".") == 1 {
		version += ".0"
	}
	if version == "" {
		return "", updateStateFailure("system_unknown")
	}
	return version, nil
}
func sourceUpdateState(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) (desktopupdate.LocalState, error) {
	state, err := measureDesktopUpdateState(ctx, cfg, paths)
	if err != nil {
		return state, err
	}
	state.SystemVersion, err = localUpdateSystemVersion(ctx)
	return state, err
}
func readTargetCatalog(ctx context.Context, filename string, artifact desktopupdate.Artifact) ([]byte, *desktopupdate.ComponentDescriptor, error) {
	if artifact.Size > 1024*1024 {
		return nil, nil, updateStateFailure("artifact_size")
	}
	if err := desktopupdate.VerifyArtifact(ctx, filename, artifact); err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, updateStateFailure("component_update_incompatible")
	}
	var catalog struct {
		SchemaVersion  int
		Platform, Arch string
		Components     map[string]desktopupdate.ComponentDescriptor
	}
	if json.Unmarshal(raw, &catalog) != nil || catalog.SchemaVersion != 1 || catalog.Platform != runtime.GOOS || catalog.Arch != runtime.GOARCH || len(catalog.Components) != 1 {
		return nil, nil, updateStateFailure("component_update_incompatible")
	}
	rag, ok := catalog.Components["rag"]
	if !ok || rag.ID != "rag" || !componentDigestPattern.MatchString(rag.Revision) || !componentDigestPattern.MatchString(rag.SHA256) || !componentDigestPattern.MatchString(rag.BaseFingerprint) || !componentDigestPattern.MatchString(rag.Tag) || rag.PythonABI == "" || rag.SizeBytes <= 0 || rag.UnpackedBytes <= 0 {
		return nil, nil, updateStateFailure("component_update_incompatible")
	}
	return raw, &rag, nil
}
func fullSourceDescriptor(paths RuntimePaths, local desktopupdate.LocalState) (*desktopupdate.ComponentDescriptor, error) {
	if local.Components["rag"] == "" {
		return nil, nil
	}
	catalog, err := loadPythonComponentCatalogForUpdate(paths)
	if err != nil || catalog == nil {
		return nil, updateStateFailure("component_state_unknown")
	}
	entry := catalog.Components["rag"]
	if entry.CatalogURL == "" || !componentDigestPattern.MatchString(entry.Tag) {
		return nil, updateStateFailure("component_state_unknown")
	}
	raw, err := os.ReadFile(filepath.Join(paths.RuntimeRoot, "deps", "python-components", "rag", "catalog-"+entry.Tag+".json"))
	var descriptor desktopupdate.ComponentDescriptor
	if err != nil || json.Unmarshal(raw, &descriptor) != nil || descriptor.Revision != local.Components["rag"] {
		return nil, updateStateFailure("component_state_unknown")
	}
	return &descriptor, nil
}
func invokeCoreMaintenance(ctx context.Context, paths RuntimePaths, action, tx string) (*desktopupdate.NativeComponentStage, error) {
	request, _ := json.Marshal(map[string]string{"transactionId": tx})
	command := exec.Command(paths.CoreBin, "--component-maintenance", action)
	command.Env = append(os.Environ(), "LAZYMIND_RUNTIME_ROOT="+paths.RuntimeRoot, "LAZYMIND_RUNTIME_RESOURCES_ROOT="+paths.ResourcesRoot)
	input, err := command.StdinPipe()
	if err != nil {
		return nil, updateStateFailure("component_maintenance_failed")
	}
	var output boundedUpdateOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		input.Close()
		return nil, updateStateFailure("component_maintenance_failed")
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_, _ = input.Write([]byte{10})
			_ = input.Close()
		case <-done:
		}
	}()
	if _, err := input.Write(append(request, byte(10))); err != nil {
		_ = input.Close()
	}
	waitErr := command.Wait()
	close(done)
	_ = input.Close()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, updateStateFailure("component_maintenance_failed")
	}
	var response struct {
		Status, Reason                                            string
		TransactionID, ComponentMode, StagePath, Revision, SHA256 string
	}
	if json.Unmarshal(output.Bytes(), &response) != nil {
		return nil, updateStateFailure("component_maintenance_failed")
	}
	if response.Status == "blocked" {
		return nil, updateStateFailure(response.Reason)
	}
	if response.TransactionID != tx || (action == "stage" && response.Status != "prepared") || (action == "activate" && response.Status != "activated") {
		return nil, updateStateFailure("component_maintenance_failed")
	}
	return &desktopupdate.NativeComponentStage{Status: response.Status, StagePath: response.StagePath, Revision: response.Revision, SHA256: response.SHA256, TransactionID: tx, ComponentMode: response.ComponentMode}, nil
}

type boundedUpdateOutput struct{ data []byte }

func (w *boundedUpdateOutput) Write(raw []byte) (int, error) {
	count := len(raw)
	if len(w.data) < 1024*1024 {
		remaining := 1024*1024 - len(w.data)
		if len(raw) > remaining {
			raw = raw[:remaining]
		}
		w.data = append(w.data, raw...)
	}
	return count, nil
}
func (w *boundedUpdateOutput) Bytes() []byte { return w.data }
func nativeCommand(ctx context.Context, root, action string, value any) error {
	raw, _ := json.Marshal(value)
	output, err := desktopupdate.NativeUpdateStateCommand(ctx, root, action, raw)
	if err != nil {
		return err
	}
	var response struct{ Status, Reason string }
	if json.Unmarshal(output, &response) != nil {
		return updateStateFailure("update_record_read")
	}
	if response.Status == "blocked" {
		return updateStateFailure(response.Reason)
	}
	return nil
}
func nativeSummary(record *desktopupdate.NativeRecord, owner string) map[string]any {
	target, version, profile, rag := record.EffectiveTarget()
	view := map[string]any{"runtimeRoot": record.RuntimeRoot, "version": 2, "phase": record.Phase, "transactionId": record.TransactionID, "owner": owner, "sourceVersion": record.SourceVersion, "releaseId": target, "nextVersion": version, "targetReleaseId": target, "targetVersion": version, "dataProfile": profile, "packagePath": record.PackagePath, "sourceHadRAG": record.SourceHadRAG, "componentMode": record.ComponentMode, "expectedPeers": record.ExpectedPeers, "componentsPrepared": record.ComponentStage != nil}
	if rag != nil {
		view["targetRAG"] = rag
		view["targetRAGRevision"] = rag.Revision
	}
	return view
}

func (c *CLI) prepareNativeComponents(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, identity desktopupdate.Config, root, releaseID, packagePath, componentsPath string) (*desktopupdate.NativeRecord, error) {
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		return nil, err
	}
	if record != nil {
		if !nativeIdentityMatches(record, identity, paths) {
			return nil, updateStateFailure("update_transaction_conflict")
		}
		effective, _, _, _ := record.EffectiveTarget()
		if record.Phase == "repair" && record.RepairTarget != nil && identity.ReleaseID == effective {
			stage, err := invokeCoreMaintenance(ctx, paths, "stage", record.TransactionID)
			if err != nil {
				return nil, err
			}
			if err := nativeCommand(ctx, root, "prepared", map[string]any{"transactionId": record.TransactionID, "stage": stage}); err != nil {
				return nil, err
			}
			return desktopupdate.ReadNativeRecord(root)
		}
		if record.Phase != "pending" || identity.ReleaseID != record.SourceReleaseID || (releaseID != "" && record.TargetReleaseID != releaseID) {
			return nil, updateStateFailure("update_incomplete")
		}
	}
	running, _, err := desktopUpdateRuntime(cfg, paths)
	if err != nil {
		return nil, err
	}
	cfg = running
	if record != nil {
		if releaseID == "" {
			releaseID = record.TargetReleaseID
		}
		if packagePath == "" {
			packagePath = record.PackagePath
		}
		if componentsPath == "" {
			componentsPath = record.ComponentsPath
		}
	}
	local, err := sourceUpdateState(ctx, cfg, paths)
	if err != nil {
		return nil, err
	}
	if cfg.UpdateStorage == nil || cfg.UpdateStorage.Version != 2 {
		return nil, updateStateFailure("updater_protocol")
	}
	envelope, err := desktopupdate.ReadAcceptedEnvelope(identity, root)
	if err != nil {
		return nil, err
	}
	decision, err := desktopupdate.Check(ctx, envelope, identity, local, root, true, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if (decision.Status != "direct" && decision.Status != "bridge") || decision.Next == nil || decision.Next.ID != releaseID {
		return nil, updateStateFailure("offer_changed")
	}
	target := decision.Next
	if record == nil && target.Package.UnpackedBytes <= 0 {
		return nil, updateStateFailure("native_space_unknown")
	}
	if err := desktopupdate.VerifyArtifact(ctx, packagePath, target.Package); err != nil {
		return nil, err
	}
	_, rag, err := readTargetCatalog(ctx, componentsPath, target.Components)
	if err != nil {
		return nil, err
	}
	source, err := fullSourceDescriptor(paths, local)
	if err != nil {
		return nil, err
	}
	if source != nil && source.PythonABI != rag.PythonABI {
		return nil, updateStateFailure("component_update_incompatible")
	}
	if record == nil {
		tx, err := randomHexToken()
		if err != nil {
			return nil, err
		}
		if len(tx) > 32 {
			tx = tx[:32]
		}
		actor, err := desktopupdate.IdentifyProcess(os.Getppid())
		if err != nil {
			return nil, err
		}
		scope, err := runtimeUpdateScope(cfg, paths)
		if err != nil {
			return nil, err
		}
		peers := []string{"core"}
		if buildRuntimeProcessPlan(cfg).includes(scanControlPlaneProcessName) {
			peers = append(peers, "scan")
		}
		record = &desktopupdate.NativeRecord{Version: 2, TransactionID: tx, Phase: "pending", SourceReleaseID: identity.ReleaseID, SourceVersion: identity.Version, TargetReleaseID: target.ID, TargetVersion: target.Version, TrustDomain: identity.TrustDomain, Audience: identity.Audience, Channel: identity.Channel, SigningIdentity: identity.SigningIdentity, Platform: identity.Platform, Arch: identity.Arch, RuntimeRoot: paths.RuntimeRoot, InstallationRoot: paths.ResourcesRoot, Scope: scope, SourceDataProfile: local.DataProfile, TargetDataProfile: decision.Edge.TargetDataProfile, SourceStorage: desktopupdate.NativeStorage{Version: 2, Databases: cloneUpdateStorage(cfg.UpdateStorage).Databases, SegmentStore: cfg.UpdateStorage.SegmentStore, VectorEngine: cfg.ModeProfile.VectorStore.Engine, VectorPath: cfg.ModeProfile.VectorStore.DBPath, LayoutSource: "resolved-runtime", OptionalAbsent: []string{}}, SourceHadRAG: source != nil, ComponentMode: "absent", SourceComponent: source, ExpectedPeers: peers, PackagePath: packagePath, PackageSHA256: target.Package.SHA256, PackageSize: target.Package.Size, PackageUnpackedBytes: target.Package.UnpackedBytes, ComponentsPath: componentsPath, ComponentsSHA256: target.Components.SHA256, ComponentsSize: target.Components.Size, PolicyDigest: decision.PolicyDigest, PolicySequence: decision.PolicySequence, SourceOwnerDigest: digestOwner(cfg.OwnerToken), SourceActor: &actor}
		if source != nil {
			record.ComponentMode = "sync-target"
			record.TargetComponent = rag
		}
		if err := nativeCommand(ctx, root, "create", record); err != nil {
			return nil, err
		}
	} else {
		if record.SourceDataProfile != local.DataProfile || record.SourceHadRAG != (source != nil) || record.PackageSHA256 != target.Package.SHA256 || record.ComponentsSHA256 != target.Components.SHA256 || !reflect.DeepEqual(record.SourceComponent, source) {
			return nil, updateStateFailure("offer_changed")
		}
	}
	stage, err := invokeCoreMaintenance(ctx, paths, "stage", record.TransactionID)
	if err != nil {
		return nil, err
	}
	if err := nativeCommand(ctx, root, "prepared", map[string]any{"transactionId": record.TransactionID, "stage": stage}); err != nil {
		return nil, err
	}
	return desktopupdate.ReadNativeRecord(root)
}
func digestOwner(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:])
}

func (c *CLI) updateBootstrapCommand(ctx context.Context, manager *RuntimeManager, args []string) error {
	flags := flag.NewFlagSet("update-bootstrap", flag.ContinueOnError)
	flags.SetOutput(c.errOut)
	options := addRuntimeFlags(flags)
	action := flags.String("action", "inspect", "")
	tx := flags.String("transaction-id", "", "")
	releaseID := flags.String("release-id", "", "")
	packagePath := flags.String("package", "", "")
	componentsPath := flags.String("components", "", "")
	epoch := flags.Uint64("epoch", 0, "")
	controlStdin := flags.Bool("control-stdin", false, "")
	if err := flags.Parse(args); err != nil {
		return err
	}
	emit := func(value any) error { return json.NewEncoder(c.out).Encode(value) }
	blocked := func(err error) error {
		return emit(map[string]string{"status": "blocked", "reason": desktopupdate.ErrorCode(err)})
	}
	if len(flags.Args()) != 0 {
		return blocked(updateStateFailure("update_request_invalid"))
	}
	if *controlStdin {
		controlled, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = controlled
		go func() { var one [1]byte; _, _ = os.Stdin.Read(one[:]); cancel() }()
	}
	opts := options()
	opts, err := nativeRuntimeOptions(ctx, opts)
	if err != nil {
		return blocked(err)
	}
	cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
	if err != nil {
		return blocked(updateStateFailure("data_unknown"))
	}
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return blocked(err)
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		return blocked(err)
	}
	if *action == "inspect" || *action == "inspect-store" {
		if record == nil {
			if err := recoverCommittedBootstrap(ctx, manager, &cfg, &paths); err != nil {
				return blocked(err)
			}
			return emit(map[string]any{"status": "allowed", "runtimeRoot": paths.RuntimeRoot})
		}
		if !nativeScopeIntersects(cfg, paths, record) {
			return emit(map[string]any{"status": "allowed"})
		}
		identity, err := readNativeConfiguration(ctx, paths)
		if err != nil {
			return blocked(err)
		}
		if !nativeIdentityMatches(record, identity, paths) {
			return blocked(updateStateFailure("update_transaction_conflict"))
		}
		view := nativeSummary(record, cfg.OwnerToken)
		effective, _, _, _ := record.EffectiveTarget()
		status := "repair"
		if record.Phase == "pending" && identity.ReleaseID == record.SourceReleaseID {
			status = "allowed"
		} else if record.Phase != "repair" && identity.ReleaseID == effective {
			status = "maintenance_required"
		}
		view["status"] = status
		view["currentReleaseId"] = identity.ReleaseID
		return emit(view)
	}
	identity, err := readNativeConfiguration(ctx, paths)
	if err != nil {
		return blocked(err)
	}
	if *action == "prepare-components" {
		if record != nil && *tx != "" && *tx != record.TransactionID {
			return blocked(updateStateFailure("update_transaction_conflict"))
		}
		prepared, err := c.prepareNativeComponents(ctx, cfg, paths, identity, root, *releaseID, *packagePath, *componentsPath)
		if err != nil {
			return blocked(err)
		}
		view := nativeSummary(prepared, cfg.OwnerToken)
		view["status"] = "prepared"
		return emit(view)
	}
	if record == nil || *tx == "" || record.TransactionID != *tx || !nativeIdentityMatches(record, identity, paths) {
		return blocked(updateStateFailure("update_transaction_conflict"))
	}
	effective, version, profile, rag := record.EffectiveTarget()
	switch *action {
	case "verify-components", "installing":
		if record.Phase != "pending" || identity.ReleaseID != record.SourceReleaseID {
			return blocked(updateStateFailure("update_incomplete"))
		}
		local, err := sourceUpdateState(ctx, cfg, paths)
		if err != nil {
			return blocked(err)
		}
		source, err := fullSourceDescriptor(paths, local)
		if err != nil {
			return blocked(err)
		}
		if local.Dirty || local.DataProfile != record.SourceDataProfile || !reflect.DeepEqual(source, record.SourceComponent) {
			return blocked(updateStateFailure("offer_changed"))
		}
		envelope, err := desktopupdate.ReadAcceptedEnvelope(identity, root)
		if err != nil {
			return blocked(err)
		}
		decision, err := desktopupdate.Check(ctx, envelope, identity, local, root, true, time.Now().UTC())
		if err != nil {
			return blocked(err)
		}
		if decision.Next == nil || decision.Next.ID != record.TargetReleaseID || decision.Next.Package.SHA256 != record.PackageSHA256 || decision.Next.Components.SHA256 != record.ComponentsSHA256 {
			return blocked(updateStateFailure("offer_changed"))
		}
		if err := desktopupdate.VerifyArtifact(ctx, record.PackagePath, decision.Next.Package); err != nil {
			return blocked(err)
		}
		if _, _, err := readTargetCatalog(ctx, record.ComponentsPath, decision.Next.Components); err != nil {
			return blocked(err)
		}
		if *action == "installing" {
			if *epoch == 0 {
				return blocked(updateStateFailure("update_request_invalid"))
			}
			running, _, err := desktopUpdateRuntime(cfg, paths)
			if err != nil {
				return blocked(err)
			}
			paused, err := controlDesktopUpdate(ctx, running, "pause", *epoch)
			if err != nil {
				return blocked(err)
			}
			if paused.Active != 0 || !paused.Paused {
				return blocked(updateStateFailure("tasks_active"))
			}
			boot, err := currentUpdateBootID(ctx)
			if err != nil {
				return blocked(err)
			}
			if err := nativeCommand(ctx, root, "installing", map[string]any{"transactionId": *tx, "dataProfile": local.DataProfile, "componentMode": record.ComponentMode, "sourcePaused": true, "sourceActive": 0, "bootId": boot}); err != nil {
				return blocked(err)
			}
		}
		return emit(map[string]any{"status": map[bool]string{true: "installing", false: "compatible"}[*action == "installing"], "dataProfile": local.DataProfile})
	case "activate":
		if err := stopPreviousBootstrap(ctx, manager, cfg, paths, record.TransactionID); err != nil {
			return blocked(err)
		}
		if identity.ReleaseID != effective || identity.Version != version || record.Phase == "pending" {
			return blocked(updateStateFailure("update_incomplete"))
		}
		if err := resolveBootstrapStorage(&cfg, &paths, record); err != nil {
			return blocked(err)
		}
		if err := compareNativeStorage(cfg, record); err != nil {
			return blocked(err)
		}
		actual, dirty, err := inspectStoredUpdateFormat(ctx, nativeStorageSnapshot(record), !record.SourceHadRAG)
		if err != nil {
			return blocked(err)
		}
		if dirty || actual != profile {
			return blocked(updateStateFailure("update_startup_failed"))
		}
		artifact := desktopupdate.Artifact{SHA256: record.ComponentsSHA256, Size: record.ComponentsSize}
		filename := record.ComponentsPath
		if record.RepairTarget != nil {
			artifact.SHA256 = record.RepairTarget.ComponentsSHA256
			artifact.Size = record.RepairTarget.ComponentsSize
			filename = record.RepairTarget.ComponentsPath
		}
		// The final installed bundle must contain the exact artifact already bound.
		if err := desktopupdate.VerifyArtifact(ctx, filepath.Join(paths.ResourcesRoot, "config", "python-components.json"), artifact); err != nil {
			return blocked(err)
		}
		if _, _, err := readTargetCatalog(ctx, filename, artifact); err != nil {
			return blocked(err)
		}
		if _, err := invokeCoreMaintenance(ctx, paths, "activate", *tx); err != nil {
			_ = nativeCommand(ctx, root, "repair", map[string]string{"transactionId": *tx, "reason": "component_target_verification_failed"})
			return blocked(err)
		}
		components := map[string]string{}
		if record.SourceHadRAG {
			components["rag"] = rag.Revision
		}
		actor, err := desktopupdate.IdentifyProcess(os.Getppid())
		if err != nil {
			return blocked(err)
		}
		if err := nativeCommand(ctx, root, "activated", map[string]any{"transactionId": *tx, "releaseId": effective, "dataProfile": profile, "componentMode": record.ComponentMode, "components": components, "owner": cfg.OwnerToken, "actor": actor}); err != nil {
			return blocked(err)
		}
		return emit(map[string]any{"status": "activated", "transactionId": *tx, "owner": cfg.OwnerToken})
	case "up":
		if record.Phase != "activated" || identity.ReleaseID != effective {
			return blocked(updateStateFailure("update_incomplete"))
		}
		if err := stopPreviousBootstrap(ctx, manager, cfg, paths, *tx); err != nil {
			return blocked(err)
		}
		if err := bindNativeStartup(ctx, &cfg, &paths, *tx, cfg.OwnerToken); err != nil {
			return blocked(err)
		}
		if err := c.startSealedRuntime(ctx, cfg, paths, *tx); err != nil {
			return blocked(err)
		}
		return emit(map[string]any{"status": "ready", "transactionId": *tx, "owner": cfg.OwnerToken, "bootstrap": true})
	case "verify":
		proof, err := verifyNativeStartup(ctx, cfg, paths, record)
		if err != nil {
			return blocked(err)
		}
		return emit(proof)
	case "complete":
		proof, err := verifyNativeStartup(ctx, cfg, paths, record)
		if err != nil {
			return blocked(err)
		}
		// This terminal ACK receipt lives in the existing runtime state, not a second
		// install journal. An active intent always takes precedence over it.
		state, err := readRuntimeState(paths.StateFile)
		if err != nil {
			return blocked(err)
		}
		state.Config.NativeCommittedTx = *tx
		state.Config.NativeCommittedReleaseID = identity.ReleaseID
		storage := nativeStorageSnapshot(record)
		state.Config.UpdateStorage = cloneUpdateStorage(storage)
		state.Config.ModeProfile.VectorStore.Engine = storage.VectorEngine
		state.Config.ModeProfile.VectorStore.DBPath = storage.VectorPath
		state.Config.AuthService.DatabaseURL = "sqlite:///" + filepath.ToSlash(storage.Databases["auth"])
		if err := writeRuntimeState(paths.StateFile, state); err != nil {
			return blocked(err)
		}
		// The native ACK accepts the verified facts, not public response metadata.
		delete(proof, "status")
		delete(proof, "version")
		if err := nativeCommand(ctx, root, "complete", proof); err != nil {
			return blocked(err)
		}
		if err := releaseNativePeers(ctx, applyStateConfig(cfg, state), *tx, cfg.OwnerToken); err != nil {
			return blocked(err)
		}
		released := map[string]bool{}
		for _, peer := range record.ExpectedPeers {
			released[peer] = true
		}
		state.Config.UpdateBootstrap = nil
		if err := writeRuntimeState(paths.StateFile, state); err != nil {
			return blocked(err)
		}
		return emit(map[string]any{"status": "completed", "transactionId": *tx, "owner": cfg.OwnerToken, "peersReleased": released})
	case "cancel":
		if identity.ReleaseID != record.SourceReleaseID {
			return blocked(updateStateFailure("update_incomplete"))
		}
		if err := nativeCommand(ctx, root, "cancel", map[string]string{"transactionId": *tx}); err != nil {
			return blocked(err)
		}
		return emit(map[string]string{"status": "cancelled"})
	case "repair":
		if err := nativeCommand(ctx, root, "repair", map[string]string{"transactionId": *tx, "reason": "update_incomplete"}); err != nil {
			return blocked(err)
		}
		return emit(map[string]string{"status": "repair"})
	case "repair-adopt-current":
		adopted, err := adoptNativeRepair(ctx, cfg, paths, identity, record, root)
		if err != nil {
			return blocked(err)
		}
		view := nativeSummary(adopted, cfg.OwnerToken)
		view["status"] = "adopted"
		view["originalSourceReleaseId"] = record.SourceReleaseID
		view["originalTargetReleaseId"] = record.TargetReleaseID
		return emit(view)
	default:
		return blocked(updateStateFailure("update_request_invalid"))
	}
}

type nativePeerProof struct {
	TransactionID string `json:"transactionId"`
	Owner         string `json:"owner"`
	Ready         bool   `json:"ready"`
	Sealed        bool   `json:"sealed"`
}

func bootstrapPeerRequest(ctx context.Context, cfg RuntimeConfig, name, action, tx, owner string) (nativePeerProof, error) {
	port := cfg.LocalProxy.CoreHostPort
	if name == "scan" {
		port = cfg.LocalProxy.ScanHostPort
	}
	raw, _ := json.Marshal(map[string]any{"action": action, "transactionId": tx, "owner": owner})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/internal/desktop-update", bytes.NewReader(raw))
	if err != nil {
		return nativePeerProof{}, err
	}
	request.Header.Set("X-LazyMind-Internal-Token", internalServiceToken())
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nativePeerProof{}, updateStateFailure("update_startup_failed")
	}
	defer response.Body.Close()
	var wire struct {
		Active    int64
		Bootstrap struct {
			TransactionID, Owner string
			Sealed               bool
		}
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&wire) != nil {
		return nativePeerProof{}, updateStateFailure("update_startup_failed")
	}
	if action == "bootstrap-complete" {
		if wire.Bootstrap.Sealed {
			return nativePeerProof{}, updateStateFailure("update_release_failed")
		}
		return nativePeerProof{TransactionID: tx, Owner: owner, Ready: true, Sealed: false}, nil
	}
	proof := nativePeerProof{TransactionID: wire.Bootstrap.TransactionID, Owner: wire.Bootstrap.Owner, Ready: wire.Active == 0, Sealed: wire.Bootstrap.Sealed}
	if proof.TransactionID != tx || proof.Owner != owner || !proof.Ready || !proof.Sealed {
		return proof, updateStateFailure("update_startup_failed")
	}
	return proof, nil
}
func verifyNativeStartup(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, record *desktopupdate.NativeRecord) (map[string]any, error) {
	identity, err := readNativeConfiguration(ctx, paths)
	if err != nil {
		return nil, err
	}
	target, version, want, rag := record.EffectiveTarget()
	if record.Phase != "activated" || identity.ReleaseID != target || identity.Version != version || !nativeIdentityMatches(record, identity, paths) {
		return nil, updateStateFailure("update_startup_failed")
	}
	state, err := readRuntimeState(paths.StateFile)
	if err != nil || state.OverallStatus != "ready" || state.OwnerToken != cfg.OwnerToken || state.Config.UpdateBootstrap == nil || state.Config.UpdateBootstrap.TransactionID != record.TransactionID {
		return nil, updateStateFailure("update_startup_failed")
	}
	running := applyStateConfig(cfg, state)
	if err := compareNativeStorage(running, record); err != nil {
		return nil, err
	}
	profile, dirty, err := inspectStoredUpdateFormat(ctx, nativeStorageSnapshot(record), !record.SourceHadRAG)
	if err != nil {
		return nil, err
	}
	if dirty || profile != want {
		return nil, updateStateFailure("update_startup_failed")
	}
	components, err := measuredUpdateComponents(paths, running.Algorithm.RAGDisabled)
	if err != nil {
		return nil, err
	}
	if record.SourceHadRAG {
		if rag == nil || components["rag"] != rag.Revision || len(components) != 1 {
			return nil, updateStateFailure("component_state_unknown")
		}
	} else if len(components) != 0 {
		return nil, updateStateFailure("component_state_unknown")
	}
	expected := []string{"core"}
	if buildRuntimeProcessPlan(running).includes(scanControlPlaneProcessName) {
		expected = append(expected, "scan")
	}
	if !reflect.DeepEqual(expected, record.ExpectedPeers) {
		return nil, updateStateFailure("update_startup_failed")
	}
	peers := map[string]nativePeerProof{}
	for _, name := range expected {
		proof, err := bootstrapPeerRequest(ctx, running, name, "status", record.TransactionID, cfg.OwnerToken)
		if err != nil {
			return nil, err
		}
		peers[name] = proof
	}
	return map[string]any{"status": "ready", "transactionId": record.TransactionID, "owner": cfg.OwnerToken, "releaseId": target, "version": version, "dataProfile": profile, "dirty": false, "components": components, "peers": peers}, nil
}
func releaseNativePeers(ctx context.Context, cfg RuntimeConfig, tx, owner string) error {
	expected := []string{"core"}
	if cfg.UpdateBootstrap != nil {
		expected = cfg.UpdateBootstrap.ExpectedPeers
	}
	for _, name := range expected {
		if _, err := bootstrapPeerRequest(ctx, cfg, name, "bootstrap-complete", tx, owner); err != nil {
			return err
		}
	}
	return nil
}
func currentUpdateBootID(ctx context.Context) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.CommandContext(bounded, "/usr/sbin/sysctl", "-n", "kern.boottime")
	} else if runtime.GOOS == "windows" {
		command = exec.CommandContext(bounded, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc()")
	} else {
		return "", updateStateFailure("unsupported_storage")
	}
	raw, err := command.Output()
	if err != nil || len(raw) > 4096 || len(bytes.TrimSpace(raw)) == 0 {
		return "", updateStateFailure("handoff_state_unknown")
	}
	sum := sha256.Sum256(bytes.TrimSpace(raw))
	return hex.EncodeToString(sum[:]), nil
}
func adoptNativeRepair(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, identity desktopupdate.Config, record *desktopupdate.NativeRecord, root string) (*desktopupdate.NativeRecord, error) {
	if record.Phase != "repair" || !nativeIdentityMatches(record, identity, paths) {
		return nil, updateStateFailure("repair_target_not_accepted")
	}
	if record.SourceActor != nil {
		alive, err := desktopupdate.SameProcess(*record.SourceActor)
		if err != nil || alive {
			return nil, updateStateFailure("handoff_state_unknown")
		}
	}
	if record.HandoffActor != nil {
		alive, err := desktopupdate.SameProcess(*record.HandoffActor)
		if err != nil || alive {
			return nil, updateStateFailure("handoff_state_unknown")
		}
	} else {
		boot, err := currentUpdateBootID(ctx)
		if err != nil || record.BootID == "" || boot == record.BootID {
			return nil, updateStateFailure("handoff_restart_required")
		}
	}
	if err := compareNativeStorage(cfg, record); err != nil {
		return nil, err
	}
	profile, dirty, err := inspectStoredUpdateFormat(ctx, nativeStorageSnapshot(record), !record.SourceHadRAG)
	if err != nil {
		return nil, err
	}
	if dirty || profile != record.SourceDataProfile {
		return nil, updateStateFailure("repair_target_not_accepted")
	}
	components := map[string]string{}
	metadata := map[string]desktopupdate.ComponentDescriptor{}
	if record.SourceHadRAG {
		if record.SourceComponent == nil {
			return nil, updateStateFailure("component_state_unknown")
		}
		components["rag"] = record.SourceComponent.Revision
		metadata["rag"] = *record.SourceComponent
	}
	system, err := localUpdateSystemVersion(ctx)
	if err != nil {
		return nil, err
	}
	// Explicit adoption refreshes only the signed policy; it never installs an app.
	local := desktopupdate.LocalState{SystemVersion: system, DataProfile: profile, Components: components, ComponentMetadata: metadata}
	if _, err := desktopupdate.Discover(ctx, identity, local, root, nil, time.Now().UTC()); err != nil {
		return nil, err
	}
	envelope, err := desktopupdate.ReadAcceptedEnvelope(identity, root)
	if err != nil {
		return nil, err
	}
	request, _ := json.Marshal(map[string]any{"sourceReleaseId": record.SourceReleaseID, "failedTargetReleaseId": record.TargetReleaseID, "currentReleaseId": identity.ReleaseID, "dataProfile": profile, "sourceHadRAG": record.SourceHadRAG, "components": components, "componentMetadata": metadata})
	accepted, err := desktopupdate.CheckRepairAcceptance(ctx, envelope, identity, request, root, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if accepted.Status != "accepted" || accepted.Next == nil {
		return nil, updateStateFailure("repair_target_not_accepted")
	}
	target := accepted.Next
	filename := filepath.Join(paths.ResourcesRoot, "config", "python-components.json")
	_, rag, err := readTargetCatalog(ctx, filename, target.Components)
	if err != nil {
		return nil, err
	}
	repair := &desktopupdate.NativeRepairTarget{ReleaseID: target.ID, Version: target.Version, PackageSHA256: target.Package.SHA256, ComponentsSHA256: target.Components.SHA256, ComponentsSize: target.Components.Size, ComponentsPath: filename, DataProfile: profile}
	if record.SourceHadRAG {
		if rag.PythonABI != record.SourceComponent.PythonABI {
			return nil, updateStateFailure("component_update_incompatible")
		}
		repair.TargetComponent = rag
	}
	if err := nativeCommand(ctx, root, "adopt-repair", map[string]any{"transactionId": record.TransactionID, "sourceReleaseId": record.SourceReleaseID, "failedTargetReleaseId": record.TargetReleaseID, "acceptanceDigest": accepted.PolicyDigest, "handoffTerminated": true, "repairTarget": repair}); err != nil {
		return nil, err
	}
	return desktopupdate.ReadNativeRecord(root)
}

// Only an already-verified bootstrap from this transaction may be cleaned up
// after its owner died. Ordinary running instances retain their existing guard.
func stopPreviousBootstrap(ctx context.Context, manager *RuntimeManager, cfg RuntimeConfig, paths RuntimePaths, tx string) error {
	state, err := readRuntimeState(paths.StateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	old := state.Config.UpdateBootstrap
	if old == nil || old.TransactionID != tx || state.OwnerToken == cfg.OwnerToken || state.OverallStatus == "stopped" {
		return nil
	}
	if !sameRuntimePath(state.ResourcesRoot, paths.ResourcesRoot) || !sameRuntimePath(state.RuntimeRoot, paths.RuntimeRoot) {
		return updateStateFailure("runtime_owner_mismatch")
	}
	if old.Actor == nil {
		return updateStateFailure("runtime_owner_mismatch")
	}
	alive, err := desktopupdate.SameProcess(*old.Actor)
	if err != nil || alive {
		return updateStateFailure("runtime_owner_mismatch")
	}
	owned := applyStateConfig(cfg, state)
	owned.OwnerToken = state.OwnerToken
	return manager.Down(ctx, owned, paths)
}

func (c *CLI) startSealedRuntime(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, tx string) error {
	if state, err := readRuntimeState(paths.StateFile); err == nil && state.OwnerToken == cfg.OwnerToken && state.OverallStatus == "ready" && state.Config.UpdateBootstrap != nil && state.Config.UpdateBootstrap.TransactionID == tx {
		return nil
	}
	if err := os.MkdirAll(paths.LogsDir, 0700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(paths.LogsDir, "update-bootstrap.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	args := []string{"up", "--profile", "desktop", "--owner-token", cfg.OwnerToken, "--repo-root", paths.RepoRoot, "--resources-root", paths.ResourcesRoot, "--runtime-root", paths.RuntimeRoot, "--update-bootstrap", tx, "--bootstrap-owner", cfg.OwnerToken}
	command := exec.Command(c.execPath, args...)
	command.Env = os.Environ()
	command.Stdout = log
	command.Stderr = log
	configureChildProcess(command, true)
	if err := command.Start(); err != nil {
		log.Close()
		return updateStateFailure("update_startup_failed")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait(); log.Close() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if state, err := readRuntimeState(paths.StateFile); err == nil && state.OwnerToken == cfg.OwnerToken && state.OverallStatus == "ready" && state.Config.UpdateBootstrap != nil && state.Config.UpdateBootstrap.TransactionID == tx {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = command.Process.Kill()
			return ctx.Err()
		case <-done:
			return updateStateFailure("update_startup_failed")
		case <-ticker.C:
		}
	}
}

func recoverCommittedBootstrap(ctx context.Context, manager *RuntimeManager, cfg *RuntimeConfig, paths *RuntimePaths) error {
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return err
	}
	active, err := desktopupdate.ReadNativeRecord(root)
	if err != nil || active != nil {
		return err
	}
	state, err := readRuntimeState(paths.StateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	bootstrap := state.Config.UpdateBootstrap
	if bootstrap == nil {
		storage, err := committedRuntimeStorage(*cfg, *paths, state)
		if err != nil || storage == nil {
			return err
		}
		if _, err := readNativeConfiguration(ctx, *paths); err != nil {
			return err
		}
		if _, dirty, err := inspectStoredUpdateFormat(ctx, storage, state.Config.Algorithm.RAGDisabled); err != nil {
			return err
		} else if dirty {
			return updateStateFailure("update_startup_failed")
		}
		return resolveRuntimeStorage(cfg, paths, storage)
	}
	if state.Config.NativeCommittedTx != bootstrap.TransactionID {
		return updateStateFailure("update_record_read")
	}
	identity, err := readNativeConfiguration(ctx, *paths)
	if err != nil {
		return err
	}
	if identity.ReleaseID != bootstrap.ReleaseID || !sameRuntimePath(state.ResourcesRoot, paths.ResourcesRoot) || !sameRuntimePath(state.RuntimeRoot, paths.RuntimeRoot) {
		return updateStateFailure("runtime_owner_mismatch")
	}
	profile, dirty, err := inspectStoredUpdateFormat(ctx, bootstrap.Storage, state.Config.Algorithm.RAGDisabled)
	if err != nil {
		return err
	}
	if dirty || profile != bootstrap.DataProfile {
		return updateStateFailure("update_startup_failed")
	}
	owned := applyStateConfig(*cfg, state)
	owned.OwnerToken = state.OwnerToken
	if state.OwnerToken == cfg.OwnerToken {
		if err := releaseNativePeers(ctx, owned, bootstrap.TransactionID, bootstrap.Owner); err != nil {
			return err
		}
	} else {
		if bootstrap.Actor == nil {
			return updateStateFailure("runtime_owner_mismatch")
		}
		alive, err := desktopupdate.SameProcess(*bootstrap.Actor)
		if err != nil || alive {
			return updateStateFailure("runtime_owner_mismatch")
		}
		if err := manager.Down(ctx, owned, *paths); err != nil {
			return err
		}
	}
	state, err = readRuntimeState(paths.StateFile)
	if err != nil {
		return err
	}
	state.Config.UpdateBootstrap = nil
	// Releasing the seal does not discard its verified runtime configuration.
	state.Config.UpdateStorage = cloneUpdateStorage(bootstrap.Storage)
	state.Config.ModeProfile.VectorStore.Engine = bootstrap.Storage.VectorEngine
	state.Config.ModeProfile.VectorStore.DBPath = bootstrap.Storage.VectorPath
	state.Config.AuthService.DatabaseURL = "sqlite:///" + filepath.ToSlash(bootstrap.Storage.Databases["auth"])
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		return err
	}
	cfg.UpdateBootstrap = nil
	if err := resolveRuntimeStorage(cfg, paths, state.Config.UpdateStorage); err != nil {
		return err
	}
	return nil
}

func committedRuntimeStorage(cfg RuntimeConfig, paths RuntimePaths, state RuntimeState) (*UpdateStorageSnapshot, error) {
	if state.Config.NativeCommittedTx == "" && state.Config.NativeCommittedReleaseID == "" {
		return nil, nil
	}
	if len(state.Config.NativeCommittedTx) != 32 || !componentDigestPattern.MatchString(state.Config.NativeCommittedTx+state.Config.NativeCommittedTx) || state.Config.NativeCommittedReleaseID == "" {
		return nil, updateStateFailure("update_record_read")
	}
	if cfg.Profile != "desktop" || state.Profile != "desktop" || !sameRuntimePath(state.RuntimeRoot, paths.RuntimeRoot) || !sameRuntimePath(state.ResourcesRoot, paths.ResourcesRoot) {
		return nil, updateStateFailure("runtime_owner_mismatch")
	}
	storage := state.Config.UpdateStorage
	if storage == nil || storage.Version != 2 || len(storage.Databases) != 9 || storage.SegmentStore != "SQLiteStore" || storage.VectorEngine != "milvus-lite" || !filepath.IsAbs(storage.VectorPath) {
		return nil, updateStateFailure("update_storage_mismatch")
	}
	for _, family := range []string{"core", "auth", "lazyllm", "segments", "channel", "scan", "core-state", "auth-state", "scan-state"} {
		if !filepath.IsAbs(storage.Databases[family]) {
			return nil, updateStateFailure("update_storage_mismatch")
		}
	}
	return storage, nil
}

// A managed service uses the owning supervisor's persisted configuration after
// bootstrap has ended. This restores paths without restoring its admission seal.
func inheritCommittedRuntimeStorage(cfg *RuntimeConfig, paths *RuntimePaths) error {
	if cfg.UpdateBootstrap != nil {
		return nil
	}
	state, err := readRuntimeState(paths.StateFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	storage, err := committedRuntimeStorage(*cfg, *paths, state)
	if err != nil || storage == nil {
		return err
	}
	if cfg.OwnerToken == "" || cfg.OwnerToken != state.OwnerToken {
		return updateStateFailure("runtime_owner_mismatch")
	}
	return resolveRuntimeStorage(cfg, paths, storage)
}

func (c *CLI) installComponentCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("install-component", flag.ContinueOnError)
	flags.SetOutput(c.errOut)
	options := addRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, paths, err := newRuntimeConfigWithOptions(options(), true)
	if err != nil {
		return err
	}
	if cfg.Profile != "desktop" {
		return updateStateFailure("component_state_unknown")
	}
	lease, err := registerRuntimeUpdateLease(ctx, cfg, paths)
	if err != nil {
		return err
	}
	defer lease.Close()
	command := exec.Command(paths.CoreBin, "--component-maintenance", "independent")
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	command.Env = append(os.Environ(), "LAZYMIND_RUNTIME_ROOT="+paths.RuntimeRoot, "LAZYMIND_RUNTIME_RESOURCES_ROOT="+paths.ResourcesRoot)
	command.Stdout = c.out
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		input.Close()
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_, _ = input.Write([]byte{10})
			input.Close()
		case <-done:
		}
	}()
	request, _ := json.Marshal(map[string]string{"id": "rag"})
	_, _ = input.Write(append(request, byte(10)))
	err = command.Wait()
	close(done)
	input.Close()
	return err
}

// Resolve from the single fixed intent when native relaunch dropped the old
// custom-runtime argument. Only a matching packaged trust/installation may use it.
func nativeRuntimeOptions(ctx context.Context, options RuntimeConfigOptions) (RuntimeConfigOptions, error) {
	if options.Profile != "desktop" || options.RuntimeRoot != "" || os.Getenv(runtimeRootEnvVar) != "" {
		return options, nil
	}
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return options, err
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		return options, err
	}
	resources := options.ResourcesRoot
	if resources == "" {
		resources = os.Getenv(runtimeResourcesRootEnvVar)
	}
	if record == nil {
		runtimeRoot, err := committedRuntimeRoot(ctx, root, resources)
		if err != nil {
			return options, err
		}
		options.RuntimeRoot = runtimeRoot
		return options, nil
	}
	if !sameRuntimePath(resources, record.InstallationRoot) {
		return options, nil
	}
	identity, err := readNativeConfiguration(ctx, RuntimePaths{ResourcesRoot: resources})
	if err != nil {
		return options, err
	}
	if identity.TrustDomain != record.TrustDomain || identity.Audience != record.Audience || identity.SigningIdentity != record.SigningIdentity || identity.Platform != record.Platform || identity.Arch != record.Arch {
		return options, updateStateFailure("update_transaction_conflict")
	}
	options.RuntimeRoot = record.RuntimeRoot
	return options, nil
}

func committedRuntimeRoot(ctx context.Context, updaterRoot, resources string) (string, error) {
	if !filepath.IsAbs(resources) {
		return "", nil
	}
	scopes, err := desktopupdate.RegisteredCoordinatorScopes(ctx, updaterRoot)
	if err != nil {
		return "", err
	}
	var candidates []RuntimeState
	seen := map[string]bool{}
	for _, scope := range scopes {
		same, err := desktopupdate.ScopesIntersect(desktopupdate.UpdateScope{Installation: resources}, desktopupdate.UpdateScope{Installation: scope.Installation})
		if err != nil || !same {
			continue
		}
		for _, dataRoot := range scope.DataRoots {
			if seen[dataRoot] {
				continue
			}
			seen[dataRoot] = true
			info, err := os.Stat(dataRoot)
			if err != nil || !info.IsDir() {
				continue
			}
			filename := filepath.Join(dataRoot, "state", stateFileName)
			info, err = os.Lstat(filename)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
				return "", updateStateFailure("update_record_read")
			}
			state, err := readRuntimeState(filename)
			if err != nil {
				return "", updateStateFailure("update_record_read")
			}
			if state.Profile != "desktop" || !sameNativeDirectory(state.RuntimeRoot, dataRoot) || !sameNativeDirectory(state.ResourcesRoot, resources) || len(state.Config.NativeCommittedTx) != 32 || !componentDigestPattern.MatchString(state.Config.NativeCommittedTx+state.Config.NativeCommittedTx) {
				continue
			}
			if state.Config.NativeCommittedReleaseID == "" && state.Config.UpdateBootstrap == nil {
				continue
			}
			candidates = append(candidates, state)
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}
	identity, err := readNativeConfiguration(ctx, RuntimePaths{ResourcesRoot: resources})
	if err != nil {
		return "", err
	}
	var selected string
	for _, state := range candidates {
		release := state.Config.NativeCommittedReleaseID
		if release == "" && state.Config.UpdateBootstrap != nil && state.Config.UpdateBootstrap.TransactionID == state.Config.NativeCommittedTx {
			release = state.Config.UpdateBootstrap.ReleaseID
		}
		if release != identity.ReleaseID {
			continue
		}
		if selected != "" && !sameNativeDirectory(selected, state.RuntimeRoot) {
			return "", updateStateFailure("update_transaction_conflict")
		}
		selected = state.RuntimeRoot
	}
	return selected, nil
}

func sameNativeDirectory(a, b string) bool {
	left, e1 := os.Stat(a)
	right, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && left.IsDir() && right.IsDir() && os.SameFile(left, right)
}

func restoreRuntimeStorage(cfg *RuntimeConfig, paths *RuntimePaths, storage *UpdateStorageSnapshot) error {
	if storage == nil || storage.Version != 2 || len(storage.Databases) != 9 {
		return updateStateFailure("update_storage_mismatch")
	}
	cfg.UpdateStorage = cloneUpdateStorage(storage)
	paths.CoreDBPath = storage.Databases["core"]
	paths.AuthServiceDBPath = storage.Databases["auth"]
	paths.LazyLLMDBPath = storage.Databases["lazyllm"]
	paths.ChannelGatewayDBPath = storage.Databases["channel"]
	paths.ScanDBPath = storage.Databases["scan"]
	paths.CoreStateDir = filepath.Dir(storage.Databases["core-state"])
	paths.AuthServiceStateDir = filepath.Dir(storage.Databases["auth-state"])
	paths.ScanControlPlaneStateDir = filepath.Dir(storage.Databases["scan-state"])
	paths.FrozenUpdateStorage = cloneUpdateStorage(storage)
	cfg.AuthService.DatabaseURL = "sqlite:///" + filepath.ToSlash(storage.Databases["auth"])
	cfg.ModeProfile.VectorStore.Engine = storage.VectorEngine
	cfg.ModeProfile.VectorStore.DBPath = storage.VectorPath
	return nil
}
func resolveBootstrapStorage(cfg *RuntimeConfig, paths *RuntimePaths, record *desktopupdate.NativeRecord) error {
	return resolveRuntimeStorage(cfg, paths, nativeStorageSnapshot(record))
}

func resolveRuntimeStorage(cfg *RuntimeConfig, paths *RuntimePaths, storage *UpdateStorageSnapshot) error {
	// Explicit new overrides conflict; absent overrides inherit the frozen layout.
	if storage == nil {
		return updateStateFailure("update_storage_mismatch")
	}
	if os.Getenv(authServiceDatabaseURLEnvVar) != "" && !sameRuntimePath(updateAuthSQLitePath(cfg.AuthService.DatabaseURL), storage.Databases["auth"]) {
		return updateStateFailure("update_storage_mismatch")
	}
	if os.Getenv(localSQLiteDirEnvVar) != "" && !sameRuntimePath(cfg.UpdateStorage.Databases["core"], storage.Databases["core"]) {
		return updateStateFailure("update_storage_mismatch")
	}
	if (os.Getenv(localMilvusLiteDataDirEnvVar) != "" || os.Getenv(localMilvusLiteDBPathEnvVar) != "") && !sameRuntimePath(cfg.ModeProfile.VectorStore.DBPath, storage.VectorPath) {
		return updateStateFailure("update_storage_mismatch")
	}
	segment := strings.TrimSpace(os.Getenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH"))
	if segment != "" && !strings.HasPrefix(segment, "sqliteproxy://") && !sameRuntimePath(localSegmentStoreBackingPath(*paths), storage.Databases["segments"]) {
		return updateStateFailure("update_storage_mismatch")
	}
	return restoreRuntimeStorage(cfg, paths, storage)
}

// Managed process restarts inherit their verified startup config. Once the
// single intent is committed, clear the inherited fence instead of resealing.
func inheritBootstrapRuntime(cfg *RuntimeConfig, paths *RuntimePaths) error {
	tx := os.Getenv("LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION")
	if tx == "" {
		return nil
	}
	owner := os.Getenv("LAZYMIND_UPDATE_BOOTSTRAP_OWNER")
	var storage UpdateStorageSnapshot
	if json.Unmarshal([]byte(os.Getenv("LAZYMIND_UPDATE_BOOTSTRAP_STORAGE")), &storage) != nil || storage.Version != 2 || owner == "" || cfg.OwnerToken != owner {
		return updateStateFailure("update_bootstrap_invalid")
	}
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return err
	}
	record, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		return err
	}
	if record == nil {
		state, err := readRuntimeState(paths.StateFile)
		if err != nil || state.Config.NativeCommittedTx != tx || state.OwnerToken != owner {
			return updateStateFailure("update_record_read")
		}
		if err := restoreRuntimeStorage(cfg, paths, &storage); err != nil {
			return err
		}
		cfg.clearInheritedBootstrap = true
		return nil
	}
	if record.TransactionID != tx || record.Phase != "activated" || record.Owner != owner || !reflect.DeepEqual(nativeStorageSnapshot(record), &storage) {
		return updateStateFailure("update_bootstrap_invalid")
	}
	if err := restoreRuntimeStorage(cfg, paths, &storage); err != nil {
		return err
	}
	target, _, profile, _ := record.EffectiveTarget()
	cfg.UpdateBootstrap = &RuntimeUpdateBootstrap{TransactionID: tx, Owner: owner, ReleaseID: target, DataProfile: profile, Storage: cloneUpdateStorage(&storage), ExpectedPeers: record.ExpectedPeers, Actor: record.TargetActor}
	return nil
}
func bootstrapStateEnvironment(cfg RuntimeConfig, family string) []string {
	result := updateBootstrapEnvironment(cfg)
	if cfg.UpdateStorage != nil && cfg.UpdateStorage.Version == 2 && cfg.UpdateStorage.Databases[family] != "" {
		result = append(result, "LAZYMIND_STATE_SQLITE_PATH="+cfg.UpdateStorage.Databases[family])
	}
	return result
}
