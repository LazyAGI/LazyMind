package desktopupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/releaseversion"
)

// Retained v1 reader for startup admission and recovery-status. No transaction
// writer or recovery executor is shipped on the current update path.
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
