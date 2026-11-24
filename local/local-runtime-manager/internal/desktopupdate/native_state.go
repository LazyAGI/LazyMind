package desktopupdate

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

type NativeStorage struct {
	Version        int               `json:"version"`
	Databases      map[string]string `json:"databases"`
	SegmentStore   string            `json:"segmentStore"`
	VectorEngine   string            `json:"vectorEngine"`
	VectorPath     string            `json:"vectorPath"`
	LayoutSource   string            `json:"layoutSource"`
	OptionalAbsent []string          `json:"optionalAbsent"`
}
type NativeComponentUpdate struct {
	Source ComponentDescriptor `json:"source"`
	Target ComponentDescriptor `json:"target"`
}

type NativeComponentStageItem struct {
	StagePath string `json:"stagePath"`
	Revision  string `json:"revision"`
	SHA256    string `json:"sha256"`
}

type NativeComponentStage struct {
	Status        string `json:"status"`
	StagePath     string `json:"stagePath,omitempty"`
	Revision      string `json:"revision,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	TransactionID string `json:"transactionId,omitempty"`
	ComponentMode string `json:"componentMode,omitempty"`

	Components map[string]NativeComponentStageItem `json:"components,omitempty"`
}
type NativeRepairTarget struct {
	ReleaseID        string               `json:"releaseId"`
	Version          string               `json:"version"`
	PackageSHA256    string               `json:"packageSHA256"`
	ComponentsSHA256 string               `json:"componentsSHA256"`
	DataProfile      string               `json:"dataProfile"`
	ComponentsPath   string               `json:"componentsPath,omitempty"`
	ComponentsSize   int64                `json:"componentsSize,omitempty"`
	TargetComponent  *ComponentDescriptor `json:"targetComponent,omitempty"`
	AcceptanceDigest string               `json:"acceptanceDigest,omitempty"`

	TargetComponents map[string]ComponentDescriptor `json:"targetComponents,omitempty"`
}
type NativeRecord struct {
	Version              int                   `json:"version"`
	TransactionID        string                `json:"transactionId"`
	Phase                string                `json:"phase"`
	SourceReleaseID      string                `json:"sourceReleaseId"`
	SourceVersion        string                `json:"sourceVersion"`
	TargetReleaseID      string                `json:"targetReleaseId"`
	TargetVersion        string                `json:"targetVersion"`
	TrustDomain          string                `json:"trustDomain"`
	Audience             string                `json:"audience"`
	Channel              string                `json:"channel"`
	SigningIdentity      string                `json:"signingIdentity"`
	Platform             string                `json:"platform"`
	Arch                 string                `json:"arch"`
	RuntimeRoot          string                `json:"runtimeRoot"`
	InstallationRoot     string                `json:"installationRoot"`
	Scope                UpdateScope           `json:"scope"`
	SourceDataProfile    string                `json:"sourceDataProfile"`
	TargetDataProfile    string                `json:"targetDataProfile"`
	SourceStorage        NativeStorage         `json:"sourceStorage"`
	SourceHadRAG         bool                  `json:"sourceHadRAG"`
	ComponentMode        string                `json:"componentMode"`
	SourceComponent      *ComponentDescriptor  `json:"sourceComponent,omitempty"`
	TargetComponent      *ComponentDescriptor  `json:"targetComponent,omitempty"`
	ComponentStage       *NativeComponentStage `json:"componentStage,omitempty"`
	ExpectedPeers        []string              `json:"expectedPeers"`
	PackagePath          string                `json:"packagePath"`
	PackageSHA256        string                `json:"packageSHA256"`
	PackageUnpackedBytes int64                 `json:"packageUnpackedBytes,omitempty"`
	PackageSize          int64                 `json:"packageSize"`
	ComponentsPath       string                `json:"componentsPath"`
	ComponentsSHA256     string                `json:"componentsSHA256"`
	ComponentsSize       int64                 `json:"componentsSize"`
	PolicyDigest         string                `json:"policyDigest"`
	PolicySequence       uint64                `json:"policySequence"`
	SourceOwnerDigest    string                `json:"sourceOwnerDigest"`
	SourceActor          *ProcessIdentity      `json:"sourceActor,omitempty"`
	TargetActor          *ProcessIdentity      `json:"targetActor,omitempty"`
	HandoffActor         *ProcessIdentity      `json:"handoffActor,omitempty"`
	BootID               string                `json:"bootId,omitempty"`
	RepairTarget         *NativeRepairTarget   `json:"repairTarget,omitempty"`
	Reason               string                `json:"reason,omitempty"`
	Activation           json.RawMessage       `json:"activation,omitempty"`
	Owner                string                `json:"owner,omitempty"`

	ComponentUpdates map[string]NativeComponentUpdate `json:"componentUpdates,omitempty"`
}

func (r *NativeRecord) EffectiveTarget() (string, string, string, *ComponentDescriptor) {
	var rag *ComponentDescriptor
	if component, ok := r.EffectiveComponents()["rag"]; ok {
		rag = &component
	}
	if r.RepairTarget != nil {
		return r.RepairTarget.ReleaseID, r.RepairTarget.Version, r.RepairTarget.DataProfile, rag
	}
	return r.TargetReleaseID, r.TargetVersion, r.TargetDataProfile, rag
}

// A v2 transaction created before multiple components used single RAG fields.
func legacyRAGComponent(component *ComponentDescriptor) map[string]ComponentDescriptor {
	if component == nil {
		return nil
	}
	return map[string]ComponentDescriptor{"rag": *component}
}

func (r *NativeRecord) SourceComponents() map[string]ComponentDescriptor {
	if len(r.ComponentUpdates) == 0 {
		return legacyRAGComponent(r.SourceComponent)
	}
	components := make(map[string]ComponentDescriptor, len(r.ComponentUpdates))
	for id, update := range r.ComponentUpdates {
		components[id] = update.Source
	}
	return components
}

func (r *NativeRecord) EffectiveComponents() map[string]ComponentDescriptor {
	if r.RepairTarget != nil {
		return r.RepairTarget.components()
	}
	return r.targetComponents()
}

func (r *NativeRecord) targetComponents() map[string]ComponentDescriptor {
	if len(r.ComponentUpdates) == 0 {
		return legacyRAGComponent(r.TargetComponent)
	}
	components := make(map[string]ComponentDescriptor, len(r.ComponentUpdates))
	for id, update := range r.ComponentUpdates {
		components[id] = update.Target
	}
	return components
}

func (r *NativeRepairTarget) components() map[string]ComponentDescriptor {
	if len(r.TargetComponents) != 0 {
		return r.TargetComponents
	}
	return legacyRAGComponent(r.TargetComponent)
}
func NativeRecordPath(root string) string { return filepath.Join(root, "native-v2", "active.json") }

func writeNativeRecord(root string, r *NativeRecord) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicWrite(NativeRecordPath(root), raw)
}

func validateNativeRecord(r *NativeRecord) error {
	bad := func() error { return failure("update_request_invalid", nil) }
	if r.Version != 2 || len(r.TransactionID) != 32 || !digestPattern.MatchString(r.TransactionID+r.TransactionID) || !identifier.MatchString(r.SourceReleaseID) || !identifier.MatchString(r.TargetReleaseID) || r.SourceDataProfile == "" || r.SourceDataProfile != r.TargetDataProfile {
		return bad()
	}
	if r.Phase != "pending" && r.Phase != "installing" && r.Phase != "activated" && r.Phase != "repair" {
		return bad()
	}
	for _, v := range []string{r.TrustDomain, r.Audience, r.SigningIdentity, r.Platform, r.Arch, r.SourceVersion, r.TargetVersion} {
		if v == "" {
			return bad()
		}
	}
	if !filepath.IsAbs(r.RuntimeRoot) || !filepath.IsAbs(r.InstallationRoot) || !digestPattern.MatchString(r.PackageSHA256) || !digestPattern.MatchString(r.ComponentsSHA256) || !digestPattern.MatchString(r.PolicyDigest) || r.PackageSize <= 0 || r.ComponentsSize <= 0 || !filepath.IsAbs(r.PackagePath) || !filepath.IsAbs(r.ComponentsPath) {
		return bad()
	}
	scope, err := normalizeScope(r.Scope)
	if err != nil {
		return bad()
	}
	installation, err := canonicalPath(r.InstallationRoot)
	if err != nil || pathKey(scope.Installation) != pathKey(installation) {
		return bad()
	}
	covered := func(path string) bool {
		canonical, err := canonicalPath(path)
		if err != nil {
			return false
		}
		for _, root := range scope.DataRoots {
			if within(root, canonical) {
				return true
			}
		}
		return false
	}
	storage := r.SourceStorage
	if storage.Version != 2 || len(storage.Databases) != 9 || storage.SegmentStore != "SQLiteStore" || storage.VectorEngine != "milvus-lite" || !covered(storage.VectorPath) || !covered(r.RuntimeRoot) {
		return bad()
	}
	seen := map[string]bool{}
	for _, family := range []string{"core", "auth", "lazyllm", "segments", "channel", "scan", "core-state", "auth-state", "scan-state"} {
		path := storage.Databases[family]
		if !covered(path) {
			return bad()
		}
		canonical, err := canonicalPath(path)
		if err != nil || seen[pathKey(canonical)] {
			return bad()
		}
		seen[pathKey(canonical)] = true
	}
	if len(r.ExpectedPeers) == 0 || len(r.ExpectedPeers) > 2 {
		return bad()
	}
	peers := map[string]bool{}
	for _, p := range r.ExpectedPeers {
		if p != "core" && p != "scan" || peers[p] {
			return bad()
		}
		peers[p] = true
	}
	if !peers["core"] {
		return bad()
	}
	if len(r.ComponentUpdates) != 0 && (r.SourceComponent != nil || r.TargetComponent != nil) {
		return bad()
	}
	source := r.SourceComponents()
	_, hadRAG := source["rag"]
	if r.SourceHadRAG != hadRAG || (hadRAG && r.ComponentMode != "sync-target") || (!hadRAG && r.ComponentMode != "absent") ||
		!validNativeComponentUpdates(source, r.targetComponents(), r.Platform, r.Arch) {
		return bad()
	}
	if r.RepairTarget != nil && !validNativeRepairComponents(r, r.RepairTarget) {
		return bad()
	}
	if r.ComponentStage != nil && !validNativePreparedStage(r, r.EffectiveComponents(), r.ComponentStage) {
		return bad()
	}
	return nil
}

func validNativeComponentUpdates(source, target map[string]ComponentDescriptor, platform, arch string) bool {
	if len(source) != len(target) {
		return false
	}
	for id, before := range source {
		after, ok := target[id]
		if !ok || before.ID != id || after.ID != id || !validUpdateComponent(before) || !validUpdateComponent(after) ||
			before.Platform != platform || after.Platform != platform || before.Arch != arch || after.Arch != arch || before.PythonABI != after.PythonABI {
			return false
		}
	}
	return true
}

func validNativeRepairComponents(r *NativeRecord, target *NativeRepairTarget) bool {
	return (len(target.TargetComponents) == 0 || target.TargetComponent == nil) &&
		validNativeComponentUpdates(r.SourceComponents(), target.components(), r.Platform, r.Arch)
}

func validNativePreparedStage(r *NativeRecord, targets map[string]ComponentDescriptor, stage *NativeComponentStage) bool {
	if stage.Status != "prepared" {
		return false
	}
	if len(r.ComponentUpdates) == 0 && (r.RepairTarget == nil || len(r.RepairTarget.TargetComponents) == 0) {
		rag, ok := targets["rag"]
		return len(stage.Components) == 0 && (!ok || filepath.IsLocal(stage.StagePath) && stage.Revision == rag.Revision && stage.SHA256 == rag.SHA256)
	}
	if len(stage.Components) != len(targets) || stage.TransactionID != r.TransactionID || stage.ComponentMode != r.ComponentMode ||
		stage.StagePath != "" || stage.Revision != "" || stage.SHA256 != "" {
		return false
	}
	for id, target := range targets {
		item, ok := stage.Components[id]
		if !ok || !filepath.IsLocal(item.StagePath) || item.Revision != target.Revision || item.SHA256 != target.SHA256 {
			return false
		}
	}
	return true
}

func ReadNativeRecord(root string) (*NativeRecord, error) {
	path := NativeRecordPath(root)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512*1024 {
		return nil, failure("update_record_read", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, failure("update_record_read", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, failure("update_record_read", err)
	}
	var r NativeRecord
	raw, err := io.ReadAll(io.LimitReader(file, 512*1024+1))
	if err != nil || len(raw) > 512*1024 || decodeStrict(raw, &r) != nil || validateNativeRecord(&r) != nil {
		return nil, failure("update_record_read", err)
	}
	return &r, nil
}

func nativeStateLock(ctx context.Context, root string) (*os.File, error) {
	dir := filepath.Join(root, "native-v2")
	if err := ensureDirectory(dir); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		ok, err := tryLock(file)
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
			return nil, failure("update_transaction_busy", deadline.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// State lock -> nonblocking component lock is the only nested lock order.
func NativeComponentLock(runtimeRoot string) (*os.File, error) {
	dir := filepath.Join(runtimeRoot, "deps", "python-components")
	base, e := canonicalPath(runtimeRoot)
	resolved, e2 := canonicalPath(dir)
	if e != nil || e2 != nil || !within(base, resolved) {
		return nil, failure("path_invalid", nil)
	}
	if err := ensureDirectory(dir); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, ".native-update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok, err := tryLock(file)
	if err != nil || !ok {
		file.Close()
		return nil, failure("update_transaction_busy", err)
	}
	return file, nil
}
func ReleaseNativeLock(file *os.File) { releaseLock(file) }

// The caller owns signature, process identity and live-peer verification. This
// boundary owns the single atomic record and enforces immutable transitions.
func NativeUpdateStateCommand(ctx context.Context, root, action string, input []byte) ([]byte, error) {
	result := func(status, reason string) ([]byte, error) {
		return json.Marshal(map[string]any{"status": status, "reason": reason})
	}
	blocked := func(reason string) ([]byte, error) { return result("blocked", reason) }
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input) > 512*1024 || !filepath.IsAbs(root) {
		return blocked("update_request_invalid")
	}
	var req struct {
		TransactionID string            `json:"transactionId"`
		ReleaseID     string            `json:"releaseId"`
		DataProfile   string            `json:"dataProfile"`
		ComponentMode string            `json:"componentMode"`
		SourcePaused  bool              `json:"sourcePaused"`
		SourceActive  int64             `json:"sourceActive"`
		Dirty         bool              `json:"dirty"`
		BootID        string            `json:"bootId,omitempty"`
		Components    map[string]string `json:"components"`
		Owner         string            `json:"owner"`
		Actor         *ProcessIdentity  `json:"actor,omitempty"`
		Peers         map[string]struct {
			TransactionID string `json:"transactionId"`
			Owner         string `json:"owner"`
			Ready         bool   `json:"ready"`
			Sealed        bool   `json:"sealed"`
		} `json:"peers"`
		Stage             *NativeComponentStage `json:"stage"`
		Reason            string                `json:"reason"`
		SourceID          string                `json:"sourceReleaseId"`
		FailedID          string                `json:"failedTargetReleaseId"`
		AcceptanceDigest  string                `json:"acceptanceDigest"`
		HandoffTerminated bool                  `json:"handoffTerminated"`
		RepairTarget      *NativeRepairTarget   `json:"repairTarget"`
	}
	if action != "create" && decodeStrict(input, &req) != nil {
		return blocked("update_request_invalid")
	}
	lock, err := nativeStateLock(ctx, root)
	if err != nil {
		return blocked(ErrorCode(err))
	}
	defer releaseLock(lock)
	r, err := ReadNativeRecord(root)
	if err != nil {
		return blocked("update_record_read")
	}
	if action == "create" {
		if r != nil {
			return blocked("update_transaction_conflict")
		}
		var candidate NativeRecord
		if decodeStrict(input, &candidate) != nil || candidate.Phase != "pending" || validateNativeRecord(&candidate) != nil {
			return blocked("update_request_invalid")
		}
		component, err := NativeComponentLock(candidate.RuntimeRoot)
		if err != nil {
			return blocked("update_transaction_busy")
		}
		defer releaseLock(component)
		// An older target can complete without knowing about the sidecar.
		// A new transaction never inherits an observation from a reused ID.
		if removeBootObservation(root, candidate.TransactionID) != nil {
			return blocked("update_record_write")
		}
		if writeNativeRecord(root, &candidate) != nil {
			return blocked("update_record_write")
		}
		return result("pending", "")
	}
	if r == nil || req.TransactionID != r.TransactionID {
		return blocked("update_transaction_conflict")
	}
	component, err := NativeComponentLock(r.RuntimeRoot)
	if err != nil {
		return blocked("update_transaction_busy")
	}
	defer releaseLock(component)
	effective, _, profile, _ := r.EffectiveTarget()
	targets := r.EffectiveComponents()
	matchesComponents := func() bool {
		if len(req.Components) != len(targets) {
			return false
		}
		for id, target := range targets {
			if req.Components[id] != target.Revision {
				return false
			}
		}
		return true
	}
	status := ""
	switch action {
	case "cancel":
		if r.Phase != "pending" {
			return blocked("native_handoff_unknown")
		}
		if removeNativeRecord(root, r.TransactionID) != nil {
			return blocked("update_record_write")
		}
		releaseNativeComponentFiles(r, nil)
		return result("cancelled", "")
	case "prepared":
		if (r.Phase != "pending" && r.Phase != "repair") || req.Stage == nil || req.Stage.Status != "prepared" {
			return blocked("update_transaction_conflict")
		}
		if !validNativePreparedStage(r, targets, req.Stage) {
			return blocked("component_state_unknown")
		}
		r.ComponentStage = req.Stage
		status = "prepared"
	case "installing":
		if r.Phase != "pending" || req.DataProfile != r.SourceDataProfile || req.ComponentMode != r.ComponentMode || !req.SourcePaused || req.SourceActive != 0 || len(targets) != 0 && r.ComponentStage == nil {
			return blocked("update_transaction_conflict")
		}
		r.Phase = "installing"
		r.BootID = req.BootID
		if r.Platform == "windows" && req.BootID != "" && !digestPattern.MatchString(req.BootID) {
			return blocked("handoff_state_unknown")
		}
		if r.Platform == "darwin" && req.BootID != "" {
			if !validDarwinSession(req.BootID) {
				return blocked("handoff_state_unknown")
			}
			if err := writeBootObservation(root, r.TransactionID, req.BootID); err != nil {
				return blocked("update_record_write")
			}
			r.BootID = "" // Old readers compare any nonempty value to an unversioned hash.
		}
		status = "installing"
	case "activated":
		if r.Phase == "pending" || req.ReleaseID != effective || req.DataProfile != profile || req.ComponentMode != r.ComponentMode || !matchesComponents() {
			return blocked("update_transaction_conflict")
		}
		if verifyNativeTargetSelections(r.RuntimeRoot, targets) != nil {
			return blocked("component_state_unknown")
		}
		r.Phase = "activated"
		r.Owner = req.Owner
		r.TargetActor = req.Actor
		r.Activation = append(json.RawMessage(nil), input...)
		status = "activated"
	case "repair":
		r.Phase = "repair"
		r.Reason = req.Reason
		status = "repair"
	case "adopt-repair":
		if (r.Phase != "repair" && r.Phase != "installing") || req.SourceID != r.SourceReleaseID || req.FailedID != r.TargetReleaseID || !digestPattern.MatchString(req.AcceptanceDigest) || req.RepairTarget == nil || req.RepairTarget.DataProfile != r.SourceDataProfile {
			return blocked("repair_target_not_accepted")
		}
		if !identifier.MatchString(req.RepairTarget.ReleaseID) || !digestPattern.MatchString(req.RepairTarget.PackageSHA256) || !digestPattern.MatchString(req.RepairTarget.ComponentsSHA256) {
			return blocked("repair_target_not_accepted")
		}
		if !validNativeRepairComponents(r, req.RepairTarget) {
			return blocked("repair_target_not_accepted")
		}
		_, currentVersion, _, _ := r.EffectiveTarget()
		comparison, err := releaseversion.Compare(req.RepairTarget.Version, currentVersion)
		if err != nil || comparison < 0 {
			return blocked("repair_target_not_accepted")
		}
		if comparison == 0 {
			if r.RepairTarget == nil {
				return blocked("repair_target_not_accepted")
			}
			previous, requested := *r.RepairTarget, *req.RepairTarget
			previous.AcceptanceDigest, requested.AcceptanceDigest = "", ""
			if !reflect.DeepEqual(previous, requested) {
				return blocked("repair_target_not_accepted")
			}
		}
		if !req.HandoffTerminated {
			if req.BootID == "" {
				return blocked("repair_target_not_accepted")
			}
			if err := verifyRepairBoot(root, r, req.BootID); err != nil {
				return blocked(ErrorCode(err))
			}
		}
		r.Phase = "repair"
		r.RepairTarget = req.RepairTarget
		r.RepairTarget.AcceptanceDigest = req.AcceptanceDigest
		r.ComponentStage = nil
		status = "repair"
	case "complete":
		if r.Phase != "activated" || req.ReleaseID != effective || req.DataProfile != profile || req.Dirty || !matchesComponents() {
			return blocked("update_startup_failed")
		}
		if verifyNativeTargetSelections(r.RuntimeRoot, targets) != nil {
			return blocked("component_state_unknown")
		}
		owner := req.Owner
		if r.Owner != "" && owner != r.Owner {
			return blocked("update_startup_failed")
		}
		for _, peer := range r.ExpectedPeers {
			p, ok := req.Peers[peer]
			if owner == "" {
				owner = p.Owner
			}
			if !ok || p.TransactionID != r.TransactionID || p.Owner == "" || p.Owner != owner || !p.Ready || !p.Sealed {
				return blocked("update_startup_failed")
			}
		}
		if removeNativeRecord(root, r.TransactionID) != nil {
			return blocked("update_record_write")
		}
		releaseNativeComponentFiles(r, targets)
		return result("completed", "")
	default:
		return blocked("update_request_invalid")
	}
	if err := writeNativeRecord(root, r); err != nil {
		return blocked("update_record_write")
	}
	return result(status, "")
}

// Final permission is separate from the pure observation used by startup UI.
func NativeStartupPermission(ctx context.Context, root string, scope UpdateScope, releaseID, tx string) (*NativeRecord, error) {
	lock, err := nativeStateLock(ctx, root)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lock)
	r, err := ReadNativeRecord(root)
	if err != nil || r == nil {
		return r, err
	}
	current, err := normalizeScope(scope)
	if err != nil {
		return nil, err
	}
	recorded, _ := normalizeScope(r.Scope)
	if !scopesOverlap(current, recorded) {
		return nil, nil
	}
	if r.Phase == "pending" && releaseID == r.SourceReleaseID {
		return nil, nil
	}
	effective, _, _, _ := r.EffectiveTarget()
	if tx == "" || tx != r.TransactionID || r.Phase != "activated" || releaseID != effective {
		return nil, failure("update_incomplete", nil)
	}
	component, err := NativeComponentLock(r.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	defer releaseLock(component)
	if err := verifyNativeTargetSelections(r.RuntimeRoot, r.EffectiveComponents()); err != nil {
		return nil, err
	}
	return r, nil
}

func verifyNativeTargetSelections(root string, targets map[string]ComponentDescriptor) error {
	for _, target := range targets {
		if err := verifyNativeTargetSelection(root, target); err != nil {
			return err
		}
	}
	return nil
}

func verifyNativeTargetSelection(root string, target ComponentDescriptor) error {
	componentRoot := filepath.Join(root, "deps", "python-components", target.ID)
	if err := verifyNativeComponentPayload(root, filepath.Join(componentRoot, target.Revision), target); err != nil {
		return err
	}
	var selected ComponentDescriptor
	if readMetadata(filepath.Join(componentRoot, "catalog-"+target.Tag+".json"), &selected) != nil || !reflect.DeepEqual(selected, target) {
		return failure("component_state_unknown", nil)
	}
	return nil
}

func verifyNativeComponentPayload(root, dir string, target ComponentDescriptor) error {
	bad := func() error { return failure("component_state_unknown", nil) }
	if !validUpdateComponent(target) {
		return bad()
	}
	base, err := canonicalPath(root)
	canonical, err2 := canonicalPath(dir)
	if err != nil || err2 != nil || !within(base, canonical) {
		return bad()
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".ready"))
	if err != nil || stringTrim(marker) != target.SHA256 {
		return bad()
	}
	// The bundle owner adds build metadata; selection receipts remain protocol DTOs.
	var manifest struct {
		ComponentDescriptor
		SchemaVersion *int   `json:"schemaVersion"`
		SigningMode   string `json:"signingMode"`
	}
	if readMetadata(filepath.Join(dir, "bundle-manifest.json"), &manifest) != nil ||
		(manifest.SchemaVersion != nil && *manifest.SchemaVersion != 1) ||
		manifest.ID != target.ID || manifest.Revision != target.Revision || manifest.Platform != target.Platform || manifest.Arch != target.Arch ||
		manifest.PythonABI != target.PythonABI || manifest.BaseFingerprint != target.BaseFingerprint || !reflect.DeepEqual(manifest.Packages, target.Packages) {
		return bad()
	}
	return nil
}
func stringTrim(raw []byte) string { return strings.TrimSpace(string(raw)) }

// Run only after the intent has been durably removed, while both locks are held.
// Cleanup failures leave unused files behind without changing the outcome.
func releaseNativeComponentFiles(record *NativeRecord, targets map[string]ComponentDescriptor) {
	runtime, err := os.OpenRoot(record.RuntimeRoot)
	if err != nil {
		return
	}
	defer runtime.Close()
	componentPath := filepath.Join("deps", "python-components")
	for _, path := range []string{"deps", componentPath} {
		if info, err := runtime.Lstat(path); err != nil || !info.IsDir() {
			return
		}
	}
	components, err := runtime.OpenRoot(componentPath)
	if err != nil {
		return
	}
	defer components.Close()
	if info, err := components.Lstat(".native-stages"); err == nil && info.IsDir() {
		_ = components.RemoveAll(filepath.Join(".native-stages", record.TransactionID))
	}
	for id, source := range record.SourceComponents() {
		if target, ok := targets[id]; ok && source.Revision != target.Revision {
			releaseNativeSourceComponent(components, source, target)
		}
	}
}

func releaseNativeSourceComponent(components *os.Root, source, target ComponentDescriptor) {
	if info, err := components.Lstat(source.ID); err != nil || !info.IsDir() {
		return
	}
	dir := filepath.Join(source.ID, source.Revision)
	if info, err := components.Lstat(dir); err != nil || !info.IsDir() ||
		verifyNativeComponentPayload(components.Name(), filepath.Join(components.Name(), dir), source) != nil {
		return
	}
	receiptName := "catalog-" + source.Tag + ".json"
	entries, err := fs.ReadDir(components.FS(), source.ID)
	if err != nil {
		return
	}
	sourceReceiptFound := false
	for _, entry := range entries {
		tagPart, ok := strings.CutPrefix(entry.Name(), "catalog-")
		tag, jsonFile := strings.CutSuffix(tagPart, ".json")
		if !ok || !jsonFile {
			continue
		}
		var selected ComponentDescriptor
		if readMetadata(filepath.Join(components.Name(), source.ID, entry.Name()), &selected) != nil ||
			!validUpdateComponent(selected) || selected.ID != source.ID || selected.Tag != tag {
			return
		}
		if entry.Name() == receiptName {
			expected := source
			if source.Tag == target.Tag {
				expected = target
			}
			if !reflect.DeepEqual(selected, expected) {
				return
			}
			sourceReceiptFound = true
		} else if selected.Revision == source.Revision {
			return
		}
	}
	if sourceReceiptFound && components.RemoveAll(dir) == nil && source.Tag != target.Tag {
		_ = components.Remove(filepath.Join(source.ID, receiptName))
	}
}

// Independent source installation owns its actual long write, while OTA create
// uses the same mutex. The legacy admission lock is never held over this work.
func BeginIndependentComponentWrite(ctx context.Context, root, runtimeRoot string) (*os.File, error) {
	state, err := nativeStateLock(ctx, root)
	if err != nil {
		return nil, err
	}
	defer releaseLock(state)
	record, err := ReadNativeRecord(root)
	if err != nil {
		return nil, err
	}
	if record != nil {
		return nil, failure("update_transaction_busy", nil)
	}
	return NativeComponentLock(runtimeRoot)
}
