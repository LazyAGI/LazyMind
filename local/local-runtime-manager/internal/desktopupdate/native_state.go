package desktopupdate

import (
	"context"
	"encoding/json"
	"io"
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
type NativeComponentStage struct {
	Status        string `json:"status"`
	StagePath     string `json:"stagePath,omitempty"`
	Revision      string `json:"revision,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	TransactionID string `json:"transactionId,omitempty"`
	ComponentMode string `json:"componentMode,omitempty"`
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
}

func (r *NativeRecord) EffectiveTarget() (string, string, string, *ComponentDescriptor) {
	if r.RepairTarget != nil {
		return r.RepairTarget.ReleaseID, r.RepairTarget.Version, r.RepairTarget.DataProfile, r.RepairTarget.TargetComponent
	}
	return r.TargetReleaseID, r.TargetVersion, r.TargetDataProfile, r.TargetComponent
}
func NativeRecordPath(root string) string { return filepath.Join(root, "native-v2", "active.json") }

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
	if r.SourceHadRAG {
		if r.ComponentMode != "sync-target" || r.SourceComponent == nil || r.TargetComponent == nil || !validNativeComponent(r.SourceComponent) || !validNativeComponent(r.TargetComponent) {
			return bad()
		}
	} else if r.ComponentMode != "absent" || r.SourceComponent != nil || r.TargetComponent != nil {
		return bad()
	}
	return nil
}
func validNativeComponent(c *ComponentDescriptor) bool {
	return c.ID == "rag" && digestPattern.MatchString(c.Revision) && digestPattern.MatchString(c.SHA256) && digestPattern.MatchString(c.Tag) && digestPattern.MatchString(c.BaseFingerprint) && c.PythonABI != "" && len(c.Packages) != 0
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
		TransactionID    string            `json:"transactionId"`
		ReleaseID        string            `json:"releaseId"`
		RuntimeRoot      string            `json:"runtimeRoot"`
		InstallationRoot string            `json:"installationRoot"`
		Scope            UpdateScope       `json:"scope"`
		DataProfile      string            `json:"dataProfile"`
		ComponentMode    string            `json:"componentMode"`
		SourcePaused     bool              `json:"sourcePaused"`
		SourceActive     int64             `json:"sourceActive"`
		Dirty            bool              `json:"dirty"`
		BootID           string            `json:"bootId,omitempty"`
		Components       map[string]string `json:"components"`
		Owner            string            `json:"owner"`
		Actor            *ProcessIdentity  `json:"actor,omitempty"`
		Peers            map[string]struct {
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
	if action == "inspect" {
		r, err := ReadNativeRecord(root)
		if err != nil {
			return blocked("update_record_read")
		}
		if r == nil {
			return result("allowed", "")
		}
		scope := req.Scope
		if scope.Installation == "" {
			scope = UpdateScope{Installation: req.InstallationRoot, DataRoots: []string{req.RuntimeRoot}}
		}
		normalized, err := normalizeScope(scope)
		if err != nil {
			return blocked("update_request_invalid")
		}
		existing, _ := normalizeScope(r.Scope)
		if !scopesOverlap(normalized, existing) {
			return result("allowed", "")
		}
		status := "maintenance_required"
		effective, _, _, _ := r.EffectiveTarget()
		if r.Phase == "pending" && req.ReleaseID == r.SourceReleaseID {
			status = "allowed"
		} else if r.Phase == "repair" {
			status = "repair"
		} else if req.ReleaseID != effective {
			status = "repair"
		}
		raw, _ := json.Marshal(r)
		var view map[string]any
		_ = json.Unmarshal(raw, &view)
		view["status"] = status
		return json.Marshal(view)
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
		raw, _ := json.Marshal(candidate)
		if atomicWrite(NativeRecordPath(root), raw) != nil {
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
	effective, _, profile, target := r.EffectiveTarget()
	matchesComponents := func() bool {
		if !r.SourceHadRAG {
			return len(req.Components) == 0
		}
		return target != nil && len(req.Components) == 1 && req.Components["rag"] == target.Revision
	}
	status := ""
	switch action {
	case "cancel":
		if r.Phase != "pending" {
			return blocked("native_handoff_unknown")
		}
		if os.Remove(NativeRecordPath(root)) != nil || syncDirectory(filepath.Dir(NativeRecordPath(root))) != nil {
			return blocked("update_record_write")
		}
		return result("cancelled", "")
	case "prepared":
		if (r.Phase != "pending" && r.Phase != "repair") || req.Stage == nil || req.Stage.Status != "prepared" {
			return blocked("update_transaction_conflict")
		}
		if r.SourceHadRAG && (!filepath.IsLocal(req.Stage.StagePath) || req.Stage.Revision != target.Revision || req.Stage.SHA256 != target.SHA256) {
			return blocked("component_state_unknown")
		}
		r.ComponentStage = req.Stage
		status = "prepared"
	case "installing":
		if r.Phase != "pending" || req.DataProfile != r.SourceDataProfile || req.ComponentMode != r.ComponentMode || !req.SourcePaused || req.SourceActive != 0 || r.SourceHadRAG && r.ComponentStage == nil {
			return blocked("update_transaction_conflict")
		}
		r.Phase = "installing"
		r.BootID = req.BootID
		status = "installing"
	case "activated":
		if r.Phase == "pending" || req.ReleaseID != effective || req.DataProfile != profile || req.ComponentMode != r.ComponentMode || !matchesComponents() {
			return blocked("update_transaction_conflict")
		}
		if r.SourceHadRAG && verifyNativeTargetSelection(r.RuntimeRoot, target) != nil {
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
		if r.Phase != "repair" || req.SourceID != r.SourceReleaseID || req.FailedID != r.TargetReleaseID || !digestPattern.MatchString(req.AcceptanceDigest) || !req.HandoffTerminated || req.RepairTarget == nil || req.RepairTarget.DataProfile != r.SourceDataProfile {
			return blocked("repair_target_not_accepted")
		}
		if !identifier.MatchString(req.RepairTarget.ReleaseID) || !digestPattern.MatchString(req.RepairTarget.PackageSHA256) || !digestPattern.MatchString(req.RepairTarget.ComponentsSHA256) {
			return blocked("repair_target_not_accepted")
		}
		if r.SourceHadRAG && (req.RepairTarget.TargetComponent == nil || !validNativeComponent(req.RepairTarget.TargetComponent)) {
			return blocked("repair_target_not_accepted")
		}
		if !r.SourceHadRAG && req.RepairTarget.TargetComponent != nil {
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
		r.RepairTarget = req.RepairTarget
		r.RepairTarget.AcceptanceDigest = req.AcceptanceDigest
		r.ComponentStage = nil
		status = "repair"
	case "complete":
		if r.Phase != "activated" || req.ReleaseID != effective || req.DataProfile != profile || req.Dirty || !matchesComponents() {
			return blocked("update_startup_failed")
		}
		if r.SourceHadRAG && verifyNativeTargetSelection(r.RuntimeRoot, target) != nil {
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
		if os.Remove(NativeRecordPath(root)) != nil || syncDirectory(filepath.Dir(NativeRecordPath(root))) != nil {
			return blocked("update_record_write")
		}
		return result("completed", "")
	default:
		return blocked("update_request_invalid")
	}
	raw, _ := json.Marshal(r)
	if err := atomicWrite(NativeRecordPath(root), raw); err != nil {
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
	_, _, _, target := r.EffectiveTarget()
	if r.SourceHadRAG {
		if err := verifyNativeTargetSelection(r.RuntimeRoot, target); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func verifyNativeTargetSelection(root string, target *ComponentDescriptor) error {
	bad := func() error { return failure("component_state_unknown", nil) }
	if target == nil || !validNativeComponent(target) {
		return bad()
	}
	dir := filepath.Join(root, "deps", "python-components", "rag", target.Revision)
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
	var selected ComponentDescriptor
	if readMetadata(filepath.Join(root, "deps", "python-components", "rag", "catalog-"+target.Tag+".json"), &selected) != nil || !reflect.DeepEqual(selected, *target) {
		return bad()
	}
	return nil
}
func stringTrim(raw []byte) string { return strings.TrimSpace(string(raw)) }

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
