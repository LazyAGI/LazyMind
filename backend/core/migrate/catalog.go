package migrate

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

const (
	versionModeDirName = "version_mode"
	devModeDirName     = "dev_mode"
	devVersionBase     = uint64(100000000000000)
)

var releaseVersionPattern = regexp.MustCompile(`^v0_([1-9]\d*)$`)

type modeMigration struct {
	Name        string
	ModeVersion uint64
	Aggregate   *migrationFile
	Dev         []migrationFile
}

type migrationCatalog struct {
	Legacy            []migrationFile
	Modes             []modeMigration
	All               []migrationFile
	VersionMigrations []migrationFile
}

type catalogStep struct {
	Legacy *migrationFile
	Mode   *modeMigration
}

func combineDevVersion(modeVersion, devVersion uint64) (uint64, error) {
	if modeVersion == 0 {
		return 0, fmt.Errorf("mode version must be > 0")
	}
	if devVersion == 0 || devVersion >= devVersionBase {
		return 0, fmt.Errorf("dev version %d must be between 1 and %d", devVersion, devVersionBase-1)
	}
	if modeVersion > (uint64(math.MaxInt64)-devVersion)/devVersionBase {
		return 0, fmt.Errorf("combined dev version v%d/%d exceeds bigint", modeVersion, devVersion)
	}
	return modeVersion*devVersionBase + devVersion, nil
}

func (r *Runner) loadCatalog() (migrationCatalog, error) {
	versionDir := filepath.Join(r.dir, versionModeDirName)
	devDir := filepath.Join(r.dir, devModeDirName)

	structured := pathExists(versionDir) || pathExists(devDir)
	if !structured {
		migrations, err := loadMigrationDir(r.dir, "", 0)
		if err != nil {
			return migrationCatalog{}, err
		}
		if err := validateSupersededFiles(migrations); err != nil {
			return migrationCatalog{}, err
		}
		return newCatalog(migrations, nil, migrations), nil
	}

	modeByNumber := make(map[uint64]*modeMigration)
	versionMigrations, err := loadVersionModeDirectories(versionDir, modeByNumber)
	if err != nil {
		return migrationCatalog{}, err
	}

	if pathExists(devDir) {
		entries, err := os.ReadDir(devDir)
		if err != nil {
			return migrationCatalog{}, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			release := entry.Name()
			modeVersion, err := parseReleaseVersion(release)
			if err != nil {
				return migrationCatalog{}, err
			}
			mode := ensureMode(modeByNumber, release, modeVersion)
			devMigrations, err := loadMigrationDir(filepath.Join(devDir, release), release, modeVersion)
			if err != nil {
				return migrationCatalog{}, fmt.Errorf("load %s/%s: %w", devModeDirName, release, err)
			}
			for _, migration := range devMigrations {
				if len(migration.Supersedes) > 0 {
					return migrationCatalog{}, fmt.Errorf(
						"dev migration %s/%d must not declare Supersedes",
						release,
						migration.FileVersion,
					)
				}
			}
			mode.Dev = devMigrations
		}
	}

	modes := make([]modeMigration, 0, len(modeByNumber))
	for _, mode := range modeByNumber {
		modes = append(modes, *mode)
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i].ModeVersion < modes[j].ModeVersion })
	for i := 0; i+1 < len(modes); i++ {
		if modes[i].Aggregate == nil {
			return migrationCatalog{}, fmt.Errorf(
				"open migration mode %s must be the latest mode",
				modes[i].Name,
			)
		}
	}
	var previousAggregate uint64
	for _, mode := range modes {
		if mode.Aggregate == nil {
			continue
		}
		if mode.Aggregate.Version <= previousAggregate {
			return migrationCatalog{}, fmt.Errorf(
				"aggregate migration IDs must increase with release versions; %s has %d after %d",
				mode.Name,
				mode.Aggregate.Version,
				previousAggregate,
			)
		}
		previousAggregate = mode.Aggregate.Version
	}

	all := make([]migrationFile, 0, len(versionMigrations))
	all = append(all, versionMigrations...)
	for _, mode := range modes {
		all = append(all, mode.Dev...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].Version < all[j].Version })
	if err := validateUniqueMigrationVersions(all); err != nil {
		return migrationCatalog{}, err
	}

	return migrationCatalog{
		Modes:             modes,
		All:               all,
		VersionMigrations: versionMigrations,
	}, nil
}

func catalogExecutionSteps(catalog migrationCatalog) []catalogStep {
	steps := make([]catalogStep, 0, len(catalog.Legacy)+len(catalog.Modes))
	legacyIndex := 0
	for modeIndex := range catalog.Modes {
		mode := &catalog.Modes[modeIndex]
		if mode.Aggregate == nil {
			for legacyIndex < len(catalog.Legacy) {
				steps = append(steps, catalogStep{Legacy: &catalog.Legacy[legacyIndex]})
				legacyIndex++
			}
			steps = append(steps, catalogStep{Mode: mode})
			continue
		}
		for legacyIndex < len(catalog.Legacy) &&
			catalog.Legacy[legacyIndex].Version < mode.Aggregate.Version {
			steps = append(steps, catalogStep{Legacy: &catalog.Legacy[legacyIndex]})
			legacyIndex++
		}
		steps = append(steps, catalogStep{Mode: mode})
	}
	for legacyIndex < len(catalog.Legacy) {
		steps = append(steps, catalogStep{Legacy: &catalog.Legacy[legacyIndex]})
		legacyIndex++
	}
	return steps
}

func loadVersionModeDirectories(
	versionDir string,
	modes map[uint64]*modeMigration,
) ([]migrationFile, error) {
	if !pathExists(versionDir) {
		return nil, nil
	}
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", versionModeDirName, err)
	}

	versionMigrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			if migrationFilePattern.MatchString(entry.Name()) {
				return nil, fmt.Errorf(
					"%s must contain v0_N directories; found migration file %s",
					versionModeDirName,
					entry.Name(),
				)
			}
			continue
		}

		release := entry.Name()
		modeVersion, err := parseReleaseVersion(release)
		if err != nil {
			return nil, err
		}
		migrations, err := loadMigrationDir(filepath.Join(versionDir, release), "", 0)
		if err != nil {
			return nil, fmt.Errorf("load %s/%s: %w", versionModeDirName, release, err)
		}
		if len(migrations) != 1 {
			return nil, fmt.Errorf(
				"%s/%s must contain exactly one aggregate migration; found %d",
				versionModeDirName,
				release,
				len(migrations),
			)
		}
		migration := migrations[0]
		if migration.UpPath == "" || migration.DownPath == "" {
			return nil, fmt.Errorf(
				"%s/%s aggregate migration %d requires both up and down files",
				versionModeDirName,
				release,
				migration.Version,
			)
		}
		if migration.Version >= devVersionBase {
			return nil, fmt.Errorf(
				"version migration %d must be lower than %d",
				migration.Version,
				devVersionBase,
			)
		}

		mode := ensureMode(modes, release, modeVersion)
		mode.Aggregate = &migration
		versionMigrations = append(versionMigrations, migration)
	}

	sort.Slice(versionMigrations, func(i, j int) bool {
		return versionMigrations[i].Version < versionMigrations[j].Version
	})
	if err := validateSupersededFiles(versionMigrations); err != nil {
		return nil, err
	}
	return versionMigrations, nil
}

func loadMigrationDir(dir, release string, modeVersion uint64) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	seen := make(map[uint64]*migrationFile)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationFilePattern.FindStringSubmatch(entry.Name())
		if len(matches) != 4 {
			continue
		}
		fileVersion, err := strconv.ParseUint(matches[1], 10, 64)
		if err != nil {
			return nil, err
		}
		version := fileVersion
		if modeVersion > 0 {
			version, err = combineDevVersion(modeVersion, fileVersion)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", entry.Name(), err)
			}
		}
		name := matches[2]
		historyName := name
		if release != "" {
			historyName = release + "/" + name
		}
		direction := matches[3]

		item, ok := seen[version]
		if !ok {
			item = &migrationFile{
				Version:     version,
				FileVersion: fileVersion,
				Name:        historyName,
			}
			seen[version] = item
		} else if item.Name != historyName {
			return nil, fmt.Errorf(
				"duplicate migration version %d with different names: %s vs %s",
				version,
				item.Name,
				historyName,
			)
		}

		fullPath := filepath.Join(dir, entry.Name())
		switch direction {
		case "up":
			if item.UpPath != "" {
				return nil, fmt.Errorf("duplicate up migration for version %d", version)
			}
			item.UpPath = fullPath
		case "down":
			if item.DownPath != "" {
				return nil, fmt.Errorf("duplicate down migration for version %d", version)
			}
			item.DownPath = fullPath
		}
	}

	out := make([]migrationFile, 0, len(seen))
	for _, item := range seen {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := range out {
		if out[i].UpPath == "" {
			continue
		}
		body, err := os.ReadFile(out[i].UpPath)
		if err != nil {
			return nil, err
		}
		sources, err := parseSupersedesDirective(string(body))
		if err != nil {
			return nil, fmt.Errorf("migration %d: %w", out[i].Version, err)
		}
		for _, source := range sources {
			if source >= out[i].Version {
				return nil, fmt.Errorf("migration %d supersedes non-lower version %d", out[i].Version, source)
			}
		}
		out[i].Supersedes = sources
	}
	return out, nil
}

func parseReleaseVersion(release string) (uint64, error) {
	matches := releaseVersionPattern.FindStringSubmatch(release)
	if len(matches) != 2 {
		return 0, fmt.Errorf("invalid release version %q; expected v0_N", release)
	}
	version, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil || version == 0 {
		return 0, fmt.Errorf("invalid release version %q", release)
	}
	return version, nil
}

func ensureMode(modes map[uint64]*modeMigration, release string, modeVersion uint64) *modeMigration {
	if existing, ok := modes[modeVersion]; ok {
		return existing
	}
	mode := &modeMigration{Name: release, ModeVersion: modeVersion}
	modes[modeVersion] = mode
	return mode
}

func validateSupersededFiles(migrations []migrationFile) error {
	versions := make(map[uint64]struct{}, len(migrations))
	for _, migration := range migrations {
		versions[migration.Version] = struct{}{}
	}
	for _, migration := range migrations {
		for _, source := range migration.Supersedes {
			if _, ok := versions[source]; ok {
				return fmt.Errorf(
					"squash migration %d still has superseded migration file %d",
					migration.Version,
					source,
				)
			}
		}
	}
	return nil
}

func validateUniqueMigrationVersions(migrations []migrationFile) error {
	seen := make(map[uint64]string, len(migrations))
	for _, migration := range migrations {
		if previous, ok := seen[migration.Version]; ok {
			return fmt.Errorf(
				"duplicate migration history version %d: %s and %s",
				migration.Version,
				previous,
				migration.Name,
			)
		}
		seen[migration.Version] = migration.Name
	}
	return nil
}

func newCatalog(legacy []migrationFile, modes []modeMigration, all []migrationFile) migrationCatalog {
	return migrationCatalog{
		Legacy:            legacy,
		Modes:             modes,
		All:               all,
		VersionMigrations: legacy,
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
