package desktopupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

var transactionIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validID(id string) bool { return transactionIDPattern.MatchString(id) }
func terminalStage(stage string) bool {
	return stage == "ABORTED" || stage == "COMMITTED" || stage == "RECOVERED"
}
func knownStage(stage string) bool {
	switch stage {
	case "PREPARED", "DRAINING", "STOPPED", "SNAPSHOT_PREPARING", "SNAPSHOT_READY", "INSTALL_HANDOFF", "CANDIDATE_VALIDATING", "RECOVERING_SOURCE", "SOURCE_RESTORED", "RESTORING_DATA", "RECOVERY_VALIDATING", "ABORTED", "COMMITTED", "RECOVERED":
		return true
	}
	return false
}

type UpdateTransaction struct {
	mu               sync.Mutex
	root             string
	journal          UpdateJournal
	locks            []*os.File
	closed, poisoned bool
}

func validatePlanIdentity(plan TransactionPlan) error {
	order, err := releaseversion.Compare(plan.SourceVersion, plan.TargetVersion)
	if err != nil || order >= 0 || plan.Scope.Installation == "" || plan.SourceID == "" || plan.TargetID == "" || plan.SourceID == plan.TargetID || plan.DataProfile == "" || plan.TargetDataProfile == "" || plan.UpdaterProtocol != 1 || plan.PolicySequence == 0 || plan.TrustDomain == "" || plan.Audience == "" || plan.Channel == "" || plan.SigningIdentity == "" {
		return failure("plan_invalid", err)
	}
	if (plan.Mode != "official" && plan.Mode != "unsigned-test") || (plan.Platform != "darwin" && plan.Platform != "windows") || (plan.Arch != "arm64" && plan.Arch != "x64" && plan.Arch != "amd64") {
		return failure("plan_invalid", nil)
	}
	for _, hash := range []string{plan.SourceManifestSHA256, plan.SourcePackageSHA256, plan.TargetPackageSHA256, plan.PolicyDigest} {
		if !digestPattern.MatchString(hash) {
			return failure("plan_invalid", nil)
		}
	}
	if len(plan.Units) == 0 || len(plan.Units) > 128 || len(plan.Reservations) == 0 || len(plan.Reservations) > 16 {
		return failure("plan_invalid", nil)
	}
	return nil
}

func normalizePlan(root string, plan TransactionPlan) (TransactionPlan, error) {
	if err := validatePlanIdentity(plan); err != nil {
		return plan, err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	var copied TransactionPlan
	if err = json.Unmarshal(raw, &copied); err != nil {
		return plan, err
	}
	plan = copied
	plan.Scope, err = normalizeScope(plan.Scope)
	if err != nil {
		return plan, err
	}
	for _, path := range append(append([]string{}, plan.Scope.DataRoots...), plan.Scope.Installation) {
		if overlaps(root, path) {
			return plan, failure("scope_invalid", nil)
		}
	}
	for i := range plan.Units {
		unit := &plan.Units[i]
		unit.Path, err = canonicalPath(unit.Path)
		if err != nil {
			return plan, err
		}
		covered := false
		for _, path := range plan.Scope.DataRoots {
			if within(path, unit.Path) {
				covered = true
			}
		}
		if !covered || overlaps(root, unit.Path) || overlaps(plan.Scope.Installation, unit.Path) {
			return plan, failure("scope_invalid", nil)
		}
		for j := 0; j < i; j++ {
			if overlaps(plan.Units[j].Path, unit.Path) {
				return plan, failure("scope_invalid", nil)
			}
		}
		for _, exclude := range unit.Exclude {
			if exclude == "" || filepath.IsAbs(exclude) || exclude == "." || filepath.Clean(exclude) != exclude || exclude == ".." || strings.HasPrefix(exclude, ".."+string(filepath.Separator)) {
				return plan, failure("plan_invalid", nil)
			}
		}
	}
	for i := range plan.SQLiteFiles {
		plan.SQLiteFiles[i], err = canonicalPath(plan.SQLiteFiles[i])
		if err != nil {
			return plan, err
		}
		covered := false
		for _, unit := range plan.Units {
			if !within(unit.Path, plan.SQLiteFiles[i]) {
				continue
			}
			// A main file alone is not a cold SQLite unit: its committed WAL
			// must travel in the same directory switch.
			covered = within(unit.Path, plan.SQLiteFiles[i]+"-wal")
			for _, excluded := range unit.Exclude {
				path := filepath.Join(unit.Path, excluded)
				if within(path, plan.SQLiteFiles[i]) || within(path, plan.SQLiteFiles[i]+"-wal") {
					return plan, failure("plan_invalid", nil)
				}
			}
		}
		if !covered {
			return plan, failure("plan_invalid", nil)
		}
	}
	for i := range plan.Reservations {
		r := &plan.Reservations[i]
		r.Directory, err = canonicalPath(r.Directory)
		if err != nil {
			return plan, err
		}
		if r.InstallBytes < 0 || r.RecoveryBytes <= 0 || r.InstallBytes > 1<<60 || r.RecoveryBytes > 1<<60 {
			return plan, failure("plan_invalid", nil)
		}
		for _, unit := range plan.Units {
			if within(unit.Path, r.Directory) {
				return plan, failure("scope_invalid", nil)
			}
		}
	}
	return plan, nil
}

func readJournals(root string) ([]UpdateJournal, error) {
	dir := filepath.Join(root, "transactions", "v1")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, failure("journal_read", err)
	}
	var journals []UpdateJournal
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			return nil, failure("journal_corrupt", nil)
		}
		var j UpdateJournal
		if err = readMetadata(filepath.Join(dir, entry.Name(), "journal.json"), &j); err != nil {
			return nil, failure("journal_corrupt", err)
		}
		if j.Version != 1 || j.ID != entry.Name() || j.Revision == 0 || !knownStage(j.Stage) {
			return nil, failure("journal_corrupt", nil)
		}
		if err = validatePlanIdentity(j.Plan); err != nil {
			return nil, failure("journal_corrupt", err)
		}
		// Completed history has no admission authority over later user path
		// changes. Active transactions must still match their pinned scope.
		if !terminalStage(j.Stage) {
			checked, err := normalizePlan(root, j.Plan)
			if err != nil {
				return nil, failure("journal_corrupt", err)
			}
			if !reflect.DeepEqual(checked.Scope, j.ResolvedScope) {
				return nil, failure("journal_corrupt", nil)
			}
		}
		if j.SnapshotPath != "" && (j.SnapshotPath != filepath.Join(dir, j.ID, "snapshot") || !digestPattern.MatchString(j.SnapshotSHA256)) {
			return nil, failure("journal_corrupt", nil)
		}
		journals = append(journals, j)
	}
	return journals, nil
}

func ReadUpdateJournals(ctx context.Context, root string) ([]UpdateJournal, error) {
	root, err := canonicalPath(root)
	if err != nil {
		return nil, err
	}
	if _, err = os.Stat(root); os.IsNotExist(err) {
		return nil, ctx.Err()
	} else if err != nil {
		return nil, err
	}
	lock, err := acquireAdmission(ctx, root)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lock)
	return readJournals(root)
}

func transactionLocks(root string, scope UpdateScope) ([]*os.File, error) {
	dir := filepath.Join(root, "locks", "v1")
	if err := ensureDirectory(dir); err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, path := range append(append([]string{}, scope.DataRoots...), scope.Installation) {
		keys[digestBytes([]byte(pathKey(path)))] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var locks []*os.File
	for _, key := range ordered {
		f, err := os.OpenFile(filepath.Join(dir, key+".lock"), os.O_CREATE|os.O_RDWR, 0600)
		if err == nil {
			var ok bool
			ok, err = tryLock(f)
			if err == nil && !ok {
				err = failure("journal_busy", nil)
			}
		}
		if err != nil {
			if f != nil {
				_ = f.Close()
			}
			for _, held := range locks {
				releaseLock(held)
			}
			return nil, err
		}
		locks = append(locks, f)
	}
	return locks, nil
}

func BeginUpdateTransaction(ctx context.Context, root string, plan TransactionPlan) (*UpdateTransaction, error) {
	root, err := canonicalPath(root)
	if err != nil {
		return nil, err
	}
	owner := plan.OwnerToken
	if owner == "" {
		return nil, failure("plan_invalid", nil)
	}
	// JSON round-trip severs all caller-owned slices and drops the owner secret.
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var copied TransactionPlan
	if err = json.Unmarshal(raw, &copied); err != nil {
		return nil, err
	}
	plan = copied
	originalPlan := plan
	plan, err = normalizePlan(root, plan)
	if err != nil {
		return nil, err
	}
	lock, err := acquireAdmission(ctx, root)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lock)
	if err = checkJournals(root, plan.Scope); err != nil {
		return nil, err
	}
	writers, coordinators, err := sourceRegistrations(root, plan.Scope, owner)
	if err != nil {
		return nil, err
	}
	locks, err := transactionLocks(root, plan.Scope)
	if err != nil {
		return nil, err
	}
	tx := &UpdateTransaction{root: root, locks: locks}
	id, err := randomTransactionID()
	if err != nil {
		_ = tx.Close()
		return nil, err
	}
	tx.journal = UpdateJournal{Version: 1, ID: id, Stage: "PREPARED", Revision: 1, Plan: originalPlan, ResolvedScope: plan.Scope, SourceProcesses: writers, SourceCoordinators: coordinators}
	dir := filepath.Join(root, "transactions", "v1", id)
	if err = ensureDirectory(dir); err == nil {
		raw, err = json.Marshal(tx.journal)
		if err == nil {
			err = atomicWrite(filepath.Join(dir, "journal.json"), raw)
		}
	}
	if err != nil {
		_ = tx.Close()
		return nil, failure("journal_write", err)
	}
	return tx, nil
}

func ResumeUpdateTransaction(ctx context.Context, root, id string) (*UpdateTransaction, error) {
	if !validID(id) {
		return nil, failure("transaction_invalid", nil)
	}
	root, err := canonicalPath(root)
	if err != nil {
		return nil, err
	}
	lock, err := acquireAdmission(ctx, root)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lock)
	all, err := readJournals(root)
	if err != nil {
		return nil, err
	}
	for _, j := range all {
		if j.ID == id {
			locks, err := transactionLocks(root, j.ResolvedScope)
			if err != nil {
				return nil, err
			}
			return &UpdateTransaction{root: root, journal: j, locks: locks}, nil
		}
	}
	return nil, failure("transaction_missing", nil)
}

func ValidateTransactionIdentity(c Config, j UpdateJournal) error {
	p := j.Plan
	if c.TrustDomain != p.TrustDomain || c.Audience != p.Audience || c.Channel != p.Channel || c.Mode != p.Mode || c.Platform != p.Platform || c.Arch != p.Arch || c.SigningIdentity != p.SigningIdentity || c.UpdaterProtocol != p.UpdaterProtocol {
		return failure("transaction_identity", nil)
	}
	if (c.ReleaseID == p.SourceID && c.Version == p.SourceVersion) || (c.ReleaseID == p.TargetID && c.Version == p.TargetVersion) {
		return nil
	}
	return failure("transaction_identity", nil)
}

func (tx *UpdateTransaction) Journal() UpdateJournal {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	raw, _ := json.Marshal(tx.journal)
	var j UpdateJournal
	_ = json.Unmarshal(raw, &j)
	return j
}
func (tx *UpdateTransaction) Close() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil
	}
	for _, f := range tx.locks {
		releaseLock(f)
	}
	tx.locks = nil
	tx.closed = true
	return nil
}
func (tx *UpdateTransaction) require(stages ...string) error {
	if tx.closed || tx.poisoned {
		return failure("transaction_closed", nil)
	}
	for _, stage := range stages {
		if tx.journal.Stage == stage {
			return nil
		}
	}
	return failure("transaction_stage", nil)
}
func (tx *UpdateTransaction) persist(stage string) error {
	next := tx.journal
	next.Stage = stage
	next.Revision++
	raw, err := json.Marshal(next)
	if err == nil {
		err = atomicWrite(filepath.Join(tx.root, "transactions", "v1", next.ID, "journal.json"), raw)
	}
	if err != nil {
		tx.poisoned = true
		return failure("journal_write", err)
	}
	tx.journal = next
	return nil
}
func (tx *UpdateTransaction) BeginDrain() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("PREPARED"); err != nil {
		return err
	}
	return tx.persist("DRAINING")
}
func (tx *UpdateTransaction) ConfirmStopped(ctx context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("DRAINING"); err != nil {
		return err
	}
	for _, actor := range tx.journal.SourceProcesses {
		if err := ctx.Err(); err != nil {
			return err
		}
		live, err := SameProcess(actor)
		if err != nil {
			return err
		}
		if live {
			return failure("processes_running", nil)
		}
	}
	return tx.persist("STOPPED")
}
func (tx *UpdateTransaction) AbortBeforeInstall() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("PREPARED", "DRAINING", "STOPPED", "SNAPSHOT_PREPARING", "SNAPSHOT_READY"); err != nil {
		return err
	}
	if err := tx.releaseReservations(); err != nil {
		return err
	}
	return tx.persist("ABORTED")
}
func (tx *UpdateTransaction) MarkCandidateValidation() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("INSTALL_HANDOFF"); err != nil {
		return err
	}
	return tx.persist("CANDIDATE_VALIDATING")
}
func (tx *UpdateTransaction) Commit(id, profile string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("CANDIDATE_VALIDATING"); err != nil {
		return err
	}
	if id != tx.journal.Plan.TargetID || profile != tx.journal.Plan.TargetDataProfile {
		return failure("transaction_identity", nil)
	}
	if err := tx.releaseReservations(); err != nil {
		return err
	}
	return tx.persist("COMMITTED")
}
func (tx *UpdateTransaction) MarkSourceRestored(id, manifest string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("RECOVERING_SOURCE"); err != nil {
		return err
	}
	if id != tx.journal.Plan.SourceID || manifest != tx.journal.Plan.SourceManifestSHA256 {
		return failure("transaction_identity", nil)
	}
	return tx.persist("SOURCE_RESTORED")
}
func (tx *UpdateTransaction) CompleteRecovery(id, profile string) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.require("RECOVERY_VALIDATING"); err != nil {
		return err
	}
	if id != tx.journal.Plan.SourceID || profile != tx.journal.Plan.DataProfile {
		return failure("transaction_identity", nil)
	}
	return tx.persist("RECOVERED")
}
