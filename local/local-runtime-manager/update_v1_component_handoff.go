package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func v1SelectionDigest(catalog *pythonComponentCatalog, installed map[string]string) (string, error) {
	exact := make(map[string]pythonComponent, len(installed))
	for id, revision := range installed {
		entry := catalog.Components[id]
		if entry.Revision != revision {
			return "", updateStateFailure("component_state_unknown")
		}
		exact[id] = entry
	}
	raw, err := json.Marshal(exact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type v1HandoffRequest struct {
	target, packageFile, attempt string
	prepare                      bool
	actor                        *update.ProcessIdentity
}

func v1SourceConfigDigest(source update.Config) (string, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func validateV1Caller(actor *update.ProcessIdentity) error {
	if actor == nil || os.Getppid() != actor.PID {
		return updateStateFailure("process_identity")
	}
	if expected := os.Getenv(desktopOwnerPIDEnvVar); expected != "" && expected != strconv.Itoa(actor.PID) {
		return updateStateFailure("process_identity")
	}
	alive, err := update.SameProcess(*actor)
	if err != nil || !alive {
		return updateStateFailure("process_identity")
	}
	return nil
}

// The component writer lock serializes this check with takeover, cancellation
// and the final commit. A different token cannot displace a living source.
func eligibleV1SourceOwner(r *update.V1ComponentHandoff, owner string, caller *update.ProcessIdentity, allowTakeover bool) error {
	if r.SourceActor == nil || caller == nil {
		return updateStateFailure("update_transaction_conflict")
	}
	if r.SourceOwner == owner {
		if *r.SourceActor == *caller {
			return nil
		}
		return updateStateFailure("update_transaction_conflict")
	}
	if !allowTakeover {
		return updateStateFailure("update_transaction_conflict")
	}
	alive, err := update.SameProcess(*r.SourceActor)
	if err != nil || alive {
		return updateStateFailure("update_transaction_conflict")
	}
	return nil
}

// The caller holds the writer lock and has authenticated the prior receipt,
// source owner and CAS token. A caller cannot nominate an earlier attempt.
func assignPreparedV1Attempt(next, prior *update.V1ComponentHandoff) error {
	next.Phase = "prepared"
	if prior != nil && prior.SourceOwner == next.SourceOwner {
		next.AttemptID, next.PreviousAttemptID = prior.AttemptID, prior.PreviousAttemptID
		return nil
	}
	id, err := randomHexToken()
	if err != nil {
		return updateStateFailure("update_record_write")
	}
	next.AttemptID = id
	if prior != nil {
		next.PreviousAttemptID = prior.AttemptID
	}
	return nil
}

func saveV1ComponentHandoff(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, root string, request v1HandoffRequest, artifact update.Artifact, local update.LocalState, targetCatalog *pythonComponentCatalog) (*update.V1ComponentHandoff, error) {
	source, err := readNativeConfiguration(ctx, paths)
	if err != nil {
		return nil, err
	}
	record := update.V1ComponentHandoff{SourceID: source.ReleaseID, SourceVersion: source.Version, TargetID: request.target, Installation: paths.ResourcesRoot, Runtime: paths.RuntimeRoot,
		PolicyURL: source.PolicyURL, SourceOwner: digestOwner(cfg.OwnerToken), DataProfile: local.DataProfile}
	record.SelectionDigest, err = v1SelectionDigest(targetCatalog, local.Components)
	if err != nil {
		return nil, err
	}
	target, edge, err := record.VerifyEdge(ctx, source, root, local.Components)
	if err != nil {
		return nil, err
	}
	if target.Components.Size != artifact.Size || target.Components.SHA256 != artifact.SHA256 {
		return nil, updateStateFailure("artifact_hash")
	}
	if err = update.VerifyArtifact(ctx, request.packageFile, target.Package); err != nil {
		return nil, err
	}
	manifest, err := os.Open(filepath.Join(paths.ResourcesRoot, runtimeManifestFileName))
	if err != nil {
		return nil, updateStateFailure("source_identity")
	}
	defer manifest.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, manifest); err != nil || hex.EncodeToString(hash.Sum(nil)) != edge.Recovery.SourceManifestSHA256 {
		return nil, updateStateFailure("source_identity")
	}
	_, running, err := desktopUpdateRuntime(cfg, paths)
	if err != nil || !sameNativeDirectory(running.ResourcesRoot, paths.ResourcesRoot) {
		return nil, updateStateFailure("runtime_owner_mismatch")
	}
	record.TargetVersion = target.Version
	prior, err := update.ReadV1ComponentHandoff(paths.StateDir)
	if err != nil {
		return nil, err
	}
	record.SourceConfigDigest, err = v1SourceConfigDigest(source)
	if err != nil {
		return nil, err
	}
	if prior == nil {
		if request.attempt != "" {
			return nil, updateStateFailure("update_transaction_conflict")
		}
	} else {
		if prior.Phase != "prepared" || request.attempt == "" || prior.AttemptID != request.attempt ||
			prior.TargetID != record.TargetID || prior.EdgeDigest != record.EdgeDigest || prior.SelectionDigest != record.SelectionDigest ||
			prior.SourceConfigDigest != record.SourceConfigDigest || !sameNativeDirectory(prior.Runtime, paths.RuntimeRoot) ||
			!sameNativeDirectory(prior.Installation, paths.ResourcesRoot) {
			return nil, updateStateFailure("update_transaction_conflict")
		}
		if err = eligibleV1SourceOwner(prior, record.SourceOwner, request.actor, request.prepare); err != nil {
			return nil, err
		}
		record.AttemptID = prior.AttemptID
	}
	record.Phase = "authorized"
	if request.prepare {
		if err := assignPreparedV1Attempt(&record, prior); err != nil {
			return nil, err
		}
	}
	if request.actor != nil || record.AttemptID != "" {
		// Re-read the actual parent after the potentially lengthy package/signature checks.
		if err = validateV1Caller(request.actor); err != nil {
			return nil, err
		}
		record.SourceActor = request.actor
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err := record.Save(paths.StateDir); err != nil {
		return nil, err
	}
	return &record, nil
}

func inspectOrDiscardV1Handoff(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, discard, attempt string, caller *update.ProcessIdentity) (updateComponentResult, error) {
	var view updateComponentResult
	lock, err := update.NativeComponentLock(paths.RuntimeRoot)
	if err != nil {
		return view, err
	}
	defer update.ReleaseNativeLock(lock)
	record, err := update.ReadV1ComponentHandoff(paths.StateDir)
	if err != nil {
		return view, err
	}
	if record == nil {
		if discard != "" {
			view.Status = "compatible"
		} else {
			view.Status = "absent"
		}
		return view, nil
	}
	conflict := updateStateFailure("update_transaction_conflict")
	if !sameNativeDirectory(record.Runtime, paths.RuntimeRoot) || !sameNativeDirectory(record.Installation, paths.ResourcesRoot) ||
		(discard != "" && discard != record.TargetID) {
		return view, conflict
	}
	owner := digestOwner(cfg.OwnerToken)
	// A definite shutdown/installation failure may be compensated only by the
	// live process which received authorization. Later/retry always supply an
	// attempt and cannot reopen an authorized or unphased receipt.
	confirmedFailure := record.Phase == "authorized" && discard != "" && attempt == "" && owner == record.SourceOwner
	if record.Phase != "prepared" && !confirmedFailure {
		return view, conflict
	}
	if record.Phase == "prepared" && discard != "" && (attempt == "" || attempt != record.AttemptID) {
		return view, conflict
	}
	source, err := readNativeConfiguration(ctx, paths)
	if err != nil {
		return view, err
	}
	digest, err := v1SourceConfigDigest(source)
	if err != nil || digest != record.SourceConfigDigest {
		return view, conflict
	}
	running, err := readRuntimeState(paths.StateFile)
	if err != nil || cfg.Profile != "desktop" || running.Profile != "desktop" || activeRuntimeOwnershipError(running, cfg) != nil ||
		!sameNativeDirectory(running.RuntimeRoot, paths.RuntimeRoot) || !sameNativeDirectory(running.ResourcesRoot, paths.ResourcesRoot) ||
		running.Config.MaintenanceMode != "" || (running.OverallStatus != "ready" && !(confirmedFailure && running.OverallStatus == "stopped")) {
		return view, updateStateFailure("runtime_owner_mismatch")
	}
	if err = validateV1Caller(caller); err != nil {
		return view, err
	}
	if err = eligibleV1SourceOwner(record, owner, caller, !confirmedFailure); err != nil {
		return view, err
	}
	if discard != "" {
		view.Status = "compatible"
		return view, record.Discard(paths.StateDir)
	}
	return updateComponentResult{Status: "prepared", ReleaseID: record.TargetID, AttemptID: record.AttemptID, PreviousAttemptID: record.PreviousAttemptID}, nil
}

func consumeV1ComponentHandoff(ctx context.Context, paths RuntimePaths, version string) (bool, error) {
	// Ordinary upgrades never invoke the native signature provider or acquire
	// the component writer lock just to discover that no handoff exists.
	record, err := update.ReadV1ComponentHandoff(paths.StateDir)
	if err != nil || record == nil {
		return false, err
	}
	lock, err := update.NativeComponentLock(paths.RuntimeRoot)
	if err != nil {
		return false, err
	}
	defer update.ReleaseNativeLock(lock)
	record, err = update.ReadV1ComponentHandoff(paths.StateDir)
	if err != nil || record == nil {
		return false, err
	}
	if !sameNativeDirectory(paths.RuntimeRoot, record.Runtime) {
		if len(record.MarkerChanges) != 0 {
			return false, updateStateFailure("update_transaction_conflict")
		}
		return false, update.ClearV1ComponentHandoff(paths.StateDir)
	}
	if !sameNativeDirectory(paths.ResourcesRoot, record.Installation) || (version != record.SourceVersion && version != record.TargetVersion) {
		return false, record.Discard(paths.StateDir)
	}
	identity, err := readNativeConfiguration(ctx, paths)
	if err != nil {
		return false, err
	}
	if identity.ReleaseID == record.SourceID && identity.Version == record.SourceVersion && version == record.SourceVersion && identity.PolicyURL == record.PolicyURL {
		// The OS may still finish a dispatched install after the source reopens.
		// Only a known cancellation/failure may discard that pending decision.
		return false, record.UndoMarkers(paths.StateDir)
	}
	// A merely prepared source has not authorized retaining components in B.
	if record.Phase == "prepared" || identity.ReleaseID != record.TargetID || identity.Version != record.TargetVersion || version != record.TargetVersion {
		return false, record.Discard(paths.StateDir)
	}
	data, err := readV1ComponentCatalog(filepath.Join(paths.ResourcesRoot, "config", "python-components.json"))
	if err != nil {
		return false, err
	}
	catalog, err := resolvePythonComponentCatalog(paths, data, true)
	if err != nil || catalog == nil {
		return false, updateStateFailure("component_state_unknown")
	}
	installed, err := measuredComponentsFromCatalog(paths, true, catalog)
	if err != nil {
		return false, err
	}
	digest, err := v1SelectionDigest(catalog, installed)
	if err != nil || digest != record.SelectionDigest {
		return false, updateStateFailure("component_update_incompatible")
	}
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		return false, err
	}
	target, _, err := record.VerifyEdge(ctx, identity, root, installed)
	if err != nil {
		return false, err
	}
	if err = verifyV1CatalogBytes(data, target.Components); err != nil {
		return false, err
	}
	ids := make([]string, 0, len(catalog.Components))
	for id := range catalog.Components {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return true, record.Consume(paths.StateDir, ids)
}
