package migrate

import (
	"fmt"
	"sort"

	"lazymind/core/log"
)

func validateAppliedHistory(catalog migrationCatalog, applied []historyRecord) error {
	if err := validateKnownAppliedHistory(catalog, applied); err != nil {
		return err
	}

	appliedSet := make(map[uint64]struct{}, len(applied))
	for _, record := range applied {
		appliedSet[record.Version] = struct{}{}
	}

	for _, migration := range catalog.VersionMigrations {
		if len(migration.Supersedes) == 0 {
			continue
		}
		appliedSources, missingSources := partitionVersions(migration.Supersedes, appliedSet)
		_, targetApplied := appliedSet[migration.Version]
		if targetApplied && len(appliedSources) > 0 {
			return fmt.Errorf(
				"migration %d has mixed aggregate and superseded history records: %v",
				migration.Version,
				appliedSources,
			)
		}
		if !targetApplied && len(appliedSources) > 0 && len(missingSources) > 0 {
			return fmt.Errorf(
				"cannot canonicalize migration %d: superseded migrations are partially applied; applied=%v missing=%v",
				migration.Version,
				appliedSources,
				missingSources,
			)
		}
	}

	for _, mode := range catalog.Modes {
		if mode.Aggregate == nil || !historyContains(applied, mode.Aggregate.Version) {
			continue
		}
		if appliedPreAggregateDevVersions(mode, applied) > 0 {
			return mixedModeHistoryError(mode)
		}
	}
	return nil
}

func validateKnownAppliedHistory(catalog migrationCatalog, applied []historyRecord) error {
	known := make(map[uint64]struct{}, len(catalog.All))
	allowedSuperseded := make(map[uint64]struct{})
	for _, migration := range catalog.All {
		known[migration.Version] = struct{}{}
		for _, source := range migration.Supersedes {
			allowedSuperseded[source] = struct{}{}
		}
	}
	for _, record := range applied {
		if _, ok := known[record.Version]; ok {
			continue
		}
		if _, ok := allowedSuperseded[record.Version]; !ok {
			if isArchivedDevHistory(catalog.Modes, record.Version) {
				continue
			}
			return fmt.Errorf(
				"applied migration version %d has no migration file or release directory; refusing to execute SQL",
				record.Version,
			)
		}
	}
	return nil
}

func isArchivedDevHistory(modes []modeMigration, version uint64) bool {
	if version < devVersionBase || version%devVersionBase == 0 {
		return false
	}
	modeVersion := version / devVersionBase
	for _, mode := range modes {
		if mode.ModeVersion == modeVersion && mode.Aggregate != nil && !mode.DevDirectory {
			return true
		}
	}
	return false
}

func hasDevHistoryForMode(mode modeMigration, applied []historyRecord) bool {
	for _, record := range applied {
		if record.Version >= devVersionBase &&
			record.Version%devVersionBase != 0 &&
			record.Version/devVersionBase == mode.ModeVersion {
			return true
		}
	}
	return false
}

func (r *Runner) applyUpMigrationAutomatic(
	migration migrationFile,
	applied []historyRecord,
	currentMax uint64,
) ([]historyRecord, uint64, bool, error) {
	if len(migration.Supersedes) == 0 {
		if err := r.applyUpMigration(migration, currentMax); err != nil {
			return applied, currentMax, false, err
		}
		applied = addHistoryRecord(applied, migration)
		return applied, highestAppliedVersion(applied), true, nil
	}

	appliedSet := historyVersionSet(applied)
	appliedSources, missingSources := partitionVersions(migration.Supersedes, appliedSet)
	switch {
	case len(appliedSources) == 0:
		if err := r.applyUpMigration(migration, currentMax); err != nil {
			return applied, currentMax, false, err
		}
		applied = addHistoryRecord(applied, migration)
		return applied, highestAppliedVersion(applied), true, nil
	case len(missingSources) > 0:
		return applied, currentMax, false, fmt.Errorf(
			"cannot apply squash migration %d: superseded migrations are partially applied; applied=%v missing=%v",
			migration.Version,
			appliedSources,
			missingSources,
		)
	default:
		updated, nextVersion, err := r.canonicalizeHistory(migration, migration.Supersedes, applied)
		return updated, nextVersion, false, err
	}
}

func (r *Runner) canonicalizeHistory(
	target migrationFile,
	sources []uint64,
	applied []historyRecord,
) ([]historyRecord, uint64, error) {
	updated := replaceHistory(applied, sources, target)
	nextVersion := highestAppliedVersion(updated)

	tx, err := r.db.Begin()
	if err != nil {
		return applied, highestAppliedVersion(applied), err
	}
	for _, source := range sources {
		if err := r.deleteHistory(tx, source); err != nil {
			_ = tx.Rollback()
			return applied, highestAppliedVersion(applied), err
		}
	}
	if err := r.insertHistory(tx, target.Version, target.Name); err != nil {
		_ = tx.Rollback()
		return applied, highestAppliedVersion(applied), err
	}
	if err := r.writeState(tx, &nextVersion, false); err != nil {
		_ = tx.Rollback()
		return applied, highestAppliedVersion(applied), err
	}
	if err := tx.Commit(); err != nil {
		return applied, highestAppliedVersion(applied), err
	}

	log.Logger.Info().
		Uint64("version", target.Version).
		Str("name", target.Name).
		Interface("replaced_versions", sources).
		Msg("migration history canonicalized without executing SQL")
	return updated, nextVersion, nil
}

func (r *Runner) normalizeCanonicalHistory(
	catalog migrationCatalog,
	applied []historyRecord,
) ([]historyRecord, error) {
	var err error
	for _, migration := range catalog.VersionMigrations {
		if len(migration.Supersedes) == 0 {
			continue
		}
		appliedSet := historyVersionSet(applied)
		appliedSources, missingSources := partitionVersions(migration.Supersedes, appliedSet)
		if historyContains(applied, migration.Version) {
			if len(appliedSources) > 0 {
				applied, err = r.pruneHistory(appliedSources, applied)
				if err != nil {
					return applied, err
				}
			}
			continue
		}
		if len(appliedSources) == 0 || len(missingSources) > 0 {
			continue
		}
		applied, _, err = r.canonicalizeHistory(migration, migration.Supersedes, applied)
		if err != nil {
			return applied, err
		}
	}
	return applied, nil
}

func (r *Runner) pruneHistory(versions []uint64, applied []historyRecord) ([]historyRecord, error) {
	updated := removeHistoryRecords(applied, versions)
	nextVersion := highestAppliedVersion(updated)
	tx, err := r.db.Begin()
	if err != nil {
		return applied, err
	}
	for _, version := range versions {
		if err := r.deleteHistory(tx, version); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
	}
	if err := r.writeState(tx, &nextVersion, false); err != nil {
		_ = tx.Rollback()
		return applied, err
	}
	if err := tx.Commit(); err != nil {
		return applied, err
	}
	return updated, nil
}

func (r *Runner) recordMigrationWithoutSQL(
	migration migrationFile,
	applied []historyRecord,
) ([]historyRecord, uint64, error) {
	updated := addHistoryRecord(applied, migration)
	nextVersion := highestAppliedVersion(updated)
	tx, err := r.db.Begin()
	if err != nil {
		return applied, highestAppliedVersion(applied), err
	}
	if err := r.insertHistory(tx, migration.Version, migration.Name); err != nil {
		_ = tx.Rollback()
		return applied, highestAppliedVersion(applied), err
	}
	if err := r.writeState(tx, &nextVersion, false); err != nil {
		_ = tx.Rollback()
		return applied, highestAppliedVersion(applied), err
	}
	if err := tx.Commit(); err != nil {
		return applied, highestAppliedVersion(applied), err
	}
	return updated, nextVersion, nil
}

func removeHistoryRecords(applied []historyRecord, versions []uint64) []historyRecord {
	removed := make(map[uint64]struct{}, len(versions))
	for _, version := range versions {
		removed[version] = struct{}{}
	}
	updated := make([]historyRecord, 0, len(applied))
	for _, record := range applied {
		if _, ok := removed[record.Version]; !ok {
			updated = append(updated, record)
		}
	}
	return updated
}

func postAggregateDevMigrations(mode modeMigration) []migrationFile {
	if mode.Aggregate == nil {
		return nil
	}
	out := make([]migrationFile, 0)
	for _, migration := range mode.Dev {
		if !aggregateIncludesDevMigration(mode, migration) {
			out = append(out, migration)
		}
	}
	return out
}

// aggregateIncludesDevMigration reports whether a dev migration is covered by
// an already-applied aggregate. The aggregate version is a hard compatibility
// cutoff because older databases may contain only the aggregate history row
// after executing the aggregate directly or canonicalizing their dev history.
func aggregateIncludesDevMigration(mode modeMigration, migration migrationFile) bool {
	if mode.Aggregate == nil {
		return false
	}
	return migration.FileVersion <= mode.Aggregate.Version
}

func appliedPreAggregateDevVersions(mode modeMigration, applied []historyRecord) int {
	if mode.Aggregate == nil {
		return appliedDevVersions(mode, applied)
	}
	count := 0
	for _, migration := range mode.Dev {
		if aggregateIncludesDevMigration(mode, migration) && historyContains(applied, migration.Version) {
			count++
		}
	}
	return count
}

func orderAppliedHistory(catalog migrationCatalog, applied []historyRecord) []historyRecord {
	byVersion := make(map[uint64]historyRecord, len(applied))
	for _, record := range applied {
		byVersion[record.Version] = record
	}

	ordered := make([]historyRecord, 0, len(applied))
	appendVersion := func(version uint64) {
		if record, ok := byVersion[version]; ok {
			ordered = append(ordered, record)
			delete(byVersion, version)
		}
	}
	for _, step := range catalogExecutionSteps(catalog) {
		if step.Legacy != nil {
			appendVersion(step.Legacy.Version)
			continue
		}
		if step.Mode.Aggregate != nil {
			appendVersion(step.Mode.Aggregate.Version)
		}
		for _, migration := range step.Mode.Dev {
			appendVersion(migration.Version)
		}
	}

	if len(byVersion) > 0 {
		remaining := make([]historyRecord, 0, len(byVersion))
		for _, record := range byVersion {
			remaining = append(remaining, record)
		}
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].Version < remaining[j].Version
		})
		ordered = append(ordered, remaining...)
	}
	return ordered
}

func addHistoryRecord(applied []historyRecord, migration migrationFile) []historyRecord {
	applied = append(applied, historyRecord{Version: migration.Version, Name: migration.Name})
	sort.Slice(applied, func(i, j int) bool { return applied[i].Version < applied[j].Version })
	return applied
}

func replaceHistory(applied []historyRecord, sources []uint64, target migrationFile) []historyRecord {
	sourceSet := make(map[uint64]struct{}, len(sources))
	for _, version := range sources {
		sourceSet[version] = struct{}{}
	}
	updated := make([]historyRecord, 0, len(applied)-len(sources)+1)
	for _, record := range applied {
		if _, ok := sourceSet[record.Version]; ok {
			continue
		}
		updated = append(updated, record)
	}
	return addHistoryRecord(updated, target)
}

func historyContains(applied []historyRecord, version uint64) bool {
	for _, record := range applied {
		if record.Version == version {
			return true
		}
	}
	return false
}

func historyVersionSet(applied []historyRecord) map[uint64]struct{} {
	versions := make(map[uint64]struct{}, len(applied))
	for _, record := range applied {
		versions[record.Version] = struct{}{}
	}
	return versions
}

func partitionVersions(versions []uint64, applied map[uint64]struct{}) (present, missing []uint64) {
	for _, version := range versions {
		if _, ok := applied[version]; ok {
			present = append(present, version)
		} else {
			missing = append(missing, version)
		}
	}
	return present, missing
}

func appliedDevVersions(mode modeMigration, applied []historyRecord) int {
	count := 0
	for _, migration := range mode.Dev {
		if historyContains(applied, migration.Version) {
			count++
		}
	}
	return count
}

func migrationVersions(migrations []migrationFile) []uint64 {
	versions := make([]uint64, 0, len(migrations))
	for _, migration := range migrations {
		versions = append(versions, migration.Version)
	}
	return versions
}

func mixedModeHistoryError(mode modeMigration) error {
	return fmt.Errorf(
		"migration mode %s has both aggregate version %d and dev migration history records",
		mode.Name,
		mode.Aggregate.Version,
	)
}
