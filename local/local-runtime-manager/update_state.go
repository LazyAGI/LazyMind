package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// UpdateStorageSnapshot records the paths selected by the runtime that actually
// started, rather than reconstructing them later from potentially changed env.
type UpdateStorageSnapshot struct {
	Version      int               `json:"version"`
	Databases    map[string]string `json:"databases"`
	SegmentStore string            `json:"segmentStore"`
}

func captureUpdateStorage(paths RuntimePaths, authURL string) *UpdateStorageSnapshot {
	files := map[string]string{
		"core": paths.CoreDBPath, "auth": updateAuthSQLitePath(authURL), "lazyllm": paths.LazyLLMDBPath,
		"segments": localSegmentStoreBackingPath(paths), "channel": paths.ChannelGatewayDBPath, "scan": paths.ScanDBPath,
		"core-state": filepath.Join(paths.CoreStateDir, "core_state.db"), "auth-state": filepath.Join(paths.AuthServiceStateDir, "auth_state.db"),
		"scan-state": filepath.Join(paths.ScanControlPlaneStateDir, "scan_state.db"),
	}
	if override := strings.TrimSpace(os.Getenv("LAZYMIND_STATE_SQLITE_PATH")); override != "" {
		// scanControlPlaneEnv always supplies its own explicit state path.
		for _, family := range []string{"core-state", "auth-state"} {
			files[family] = override
		}
	}
	return &UpdateStorageSnapshot{Version: 1, Databases: files, SegmentStore: localSegmentStoreType()}
}

func cloneUpdateStorage(source *UpdateStorageSnapshot) *UpdateStorageSnapshot {
	if source == nil {
		return nil
	}
	copy := *source
	copy.Databases = make(map[string]string, len(source.Databases))
	for key, value := range source.Databases {
		copy.Databases[key] = value
	}
	return &copy
}

func updateAuthSQLitePath(address string) string {
	driver, file, ok := strings.Cut(address, ":///")
	if !ok || (driver != "sqlite" && driver != "sqlite+pysqlite") {
		return ""
	}
	file, rawQuery, _ := strings.Cut(file, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	// Only these pysqlite DBAPI options leave the database filename unchanged.
	// URI mode and SQLAlchemy plugins can change its meaning; do not guess.
	for key, values := range query {
		if len(values) != 1 {
			return ""
		}
		switch key {
		case "timeout", "isolation_level", "detect_types", "check_same_thread", "cached_statements":
		default:
			return ""
		}
	}
	// SQLAlchemy does not URL-unescape its database component.
	file = filepath.FromSlash(file)
	if !filepath.IsAbs(file) || strings.ContainsAny(file, "#\x00") {
		return ""
	}
	return filepath.Clean(file)
}

func updateAuthUsesExternalDatabase(address string) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	dialect, _, _ := strings.Cut(u.Scheme, "+")
	query, err := url.ParseQuery(u.RawQuery)
	// PostgreSQL is the external authentication store supported by this runtime.
	// A plugin may rewrite the URL, so it is not evidence of external storage.
	return err == nil && dialect == "postgresql" && !query.Has("plugin") && !query.Has("plugins")
}

type updateSchemaEntry struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Table string `json:"table"`
	SQL   string `json:"sql"`
}

type updateHistoryEntry struct {
	Version uint64 `json:"version"`
	Name    string `json:"name"`
}

type updateDatabaseShape struct {
	Present       bool                 `json:"present"`
	ApplicationID int64                `json:"applicationId"`
	UserVersion   int64                `json:"userVersion"`
	Schema        []updateSchemaEntry  `json:"schema"`
	CoreVersion   uint64               `json:"coreVersion,omitempty"`
	CoreHistory   []updateHistoryEntry `json:"coreHistory,omitempty"`
}

func updateStateFailure(code string) error { return &desktopupdate.Failure{Code: code} }

func readUpdateStateBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, updateStateFailure("data_unknown")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 2*desktopupdate.MaxEnvelopeBytes+1))
	if err != nil || len(raw) > 2*desktopupdate.MaxEnvelopeBytes {
		return nil, updateStateFailure("data_unknown")
	}
	return raw, nil
}

func measureDesktopUpdateState(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) (desktopupdate.LocalState, error) {
	empty := desktopupdate.LocalState{}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	raw, err := readUpdateStateBytes(paths.StateFile)
	var persisted RuntimeState
	if err != nil || json.Unmarshal(raw, &persisted) != nil || persisted.Version != processComposeVersion {
		return empty, updateStateFailure("data_unknown")
	}
	if cfg.Profile != "desktop" || validateRequestedRuntimeOwner(cfg) != nil || activeRuntimeOwnershipError(persisted, cfg) != nil || !sameRuntimePath(persisted.RuntimeRoot, paths.RuntimeRoot) {
		return empty, updateStateFailure("runtime_owner_mismatch")
	}
	if persisted.Profile != "desktop" || persisted.RepoRoot == "" || persisted.ResourcesRoot == "" || persisted.Config.MaintenanceMode != "" || (persisted.OverallStatus != "ready" && persisted.OverallStatus != "stopped") {
		return empty, updateStateFailure("data_unknown")
	}
	storage := persisted.Config.UpdateStorage
	if storage == nil || storage.Version != 1 || len(storage.Databases) != 9 {
		return empty, updateStateFailure("data_unknown")
	}
	if !strings.EqualFold(storage.SegmentStore, "SQLiteStore") || persisted.Config.Algorithm.EnableEvo || persisted.Config.ModeProfile.VectorStore.Engine != "milvus-lite" {
		return empty, updateStateFailure("unsupported_storage")
	}
	authPath := updateAuthSQLitePath(persisted.Config.AuthService.DatabaseURL)
	if authPath == "" || !sameRuntimePath(authPath, storage.Databases["auth"]) {
		return empty, updateStateFailure("unsupported_storage")
	}
	shapes := map[string]updateDatabaseShape{}
	seen := map[string]bool{}
	for _, family := range []string{"core", "auth", "lazyllm", "segments", "channel", "scan", "core-state", "auth-state", "scan-state"} {
		file := storage.Databases[family]
		if !filepath.IsAbs(file) {
			return empty, updateStateFailure("data_unknown")
		}
		canonical, err := filepath.EvalSymlinks(file)
		optional := family == "core-state" || family == "auth-state" || ((family == "scan" || family == "scan-state") && persisted.Config.Algorithm.RAGDisabled)
		if os.IsNotExist(err) && optional {
			shapes[family] = updateDatabaseShape{}
			continue
		}
		if err != nil {
			return empty, updateStateFailure("data_unknown")
		}
		canonical = filepath.Clean(canonical)
		// Multiple owners sharing one SQLite path are not a supported layout.
		key := canonical
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return empty, updateStateFailure("unsupported_storage")
		}
		seen[key] = true
		shape, dirty, err := inspectUpdateSQLite(ctx, canonical, family == "core")
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		if err != nil {
			return empty, updateStateFailure("data_unknown")
		}
		if dirty {
			return desktopupdate.LocalState{Dirty: true, Components: map[string]string{}}, nil
		}
		shapes[family] = shape
	}
	components, err := measuredUpdateComponents(paths, persisted.Config.Algorithm.RAGDisabled)
	if err != nil {
		return empty, err
	}
	// This versioned encoding is a fingerprint, not a compatibility assertion.
	// Only signed policy edges explicitly accepting it may authorize an upgrade.
	identity := struct {
		Version      int                            `json:"version"`
		Databases    map[string]updateDatabaseShape `json:"databases"`
		Components   map[string]string              `json:"components"`
		RAGEnabled   bool                           `json:"ragEnabled"`
		VectorEngine string                         `json:"vectorEngine"`
	}{1, shapes, components, !persisted.Config.Algorithm.RAGDisabled, persisted.Config.ModeProfile.VectorStore.Engine}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return empty, updateStateFailure("data_unknown")
	}
	// Another start/stop may have changed the owner or selected stores while
	// measurement was in progress. Never publish that mixed runtime snapshot.
	after, err := readUpdateStateBytes(paths.StateFile)
	if err != nil || !bytes.Equal(raw, after) {
		return empty, updateStateFailure("data_unknown")
	}
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	digest := sha256.Sum256(encoded)
	return desktopupdate.LocalState{DataProfile: "sqlite-v1-" + hex.EncodeToString(digest[:]), Components: components}, nil
}

func inspectUpdateSQLite(ctx context.Context, file string, core bool) (updateDatabaseShape, bool, error) {
	result := updateDatabaseShape{Present: true, Schema: []updateSchemaEntry{}}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return result, false, updateStateFailure("data_unknown")
	}
	uriPath := filepath.ToSlash(file)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	address := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(500)"}}
	address.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", address.String())
	if err != nil {
		return result, false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, false, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&result.ApplicationID); err != nil {
		return result, false, err
	}
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&result.UserVersion); err != nil {
		return result, false, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT GLOB 'sqlite_*' ORDER BY type COLLATE BINARY,name COLLATE BINARY")
	if err != nil {
		return result, false, err
	}
	bytes := 0
	for rows.Next() {
		var entry updateSchemaEntry
		if err = rows.Scan(&entry.Type, &entry.Name, &entry.Table, &entry.SQL); err != nil {
			break
		}
		bytes += len(entry.SQL) + len(entry.Name) + len(entry.Table)
		if len(result.Schema) >= 4096 || bytes > desktopupdate.MaxEnvelopeBytes {
			err = updateStateFailure("data_unknown")
			break
		}
		result.Schema = append(result.Schema, entry)
	}
	rowsErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, false, err
	}
	if rowsErr != nil {
		return result, false, rowsErr
	}
	if !core {
		return result, false, nil
	}
	var count int
	var dirty bool
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		return result, false, updateStateFailure("data_unknown")
	}
	if err = tx.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&result.CoreVersion, &dirty); err != nil {
		return result, false, err
	}
	if dirty {
		return result, true, nil
	}
	rows, err = tx.QueryContext(ctx, "SELECT version,name FROM schema_migration_history ORDER BY version")
	if err != nil {
		return result, false, err
	}
	bytes = 0
	for rows.Next() {
		var entry updateHistoryEntry
		if err = rows.Scan(&entry.Version, &entry.Name); err != nil {
			break
		}
		bytes += len(entry.Name)
		if len(result.CoreHistory) >= 10000 || bytes > desktopupdate.MaxEnvelopeBytes || entry.Version == 0 || entry.Name == "" {
			err = updateStateFailure("data_unknown")
			break
		}
		result.CoreHistory = append(result.CoreHistory, entry)
	}
	rowsErr = rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, false, err
	}
	if rowsErr != nil {
		return result, false, rowsErr
	}
	if len(result.CoreHistory) == 0 || result.CoreHistory[len(result.CoreHistory)-1].Version != result.CoreVersion {
		return result, false, updateStateFailure("data_unknown")
	}
	return result, false, nil
}

func measuredUpdateComponents(paths RuntimePaths, ragDisabled bool) (map[string]string, error) {
	catalog, err := loadPythonComponentCatalogForUpdate(paths)
	if err != nil {
		return nil, updateStateFailure("component_state_unknown")
	}
	return measuredComponentsFromCatalog(paths, ragDisabled, catalog)
}

func measuredComponentsFromCatalog(paths RuntimePaths, ragDisabled bool, catalog *pythonComponentCatalog) (map[string]string, error) {
	result := map[string]string{}
	if catalog == nil {
		if !ragDisabled {
			return nil, updateStateFailure("component_state_unknown")
		}
		return result, nil
	}
	ids := make([]string, 0, len(catalog.Components))
	for id := range catalog.Components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if id != "rag" {
			return nil, updateStateFailure("component_state_unknown")
		}
		entry := catalog.Components[id]
		if entry.Platform != runtime.GOOS || entry.Arch != runtime.GOARCH || entry.PythonABI == "" {
			return nil, updateStateFailure("component_state_unknown")
		}
		root := filepath.Join(paths.RuntimeRoot, "deps", "python-components", id, entry.Revision)
		_, err := os.Stat(root)
		if os.IsNotExist(err) && ragDisabled {
			continue
		}
		if err != nil || installedPythonComponentPath(paths, entry) == "" {
			return nil, updateStateFailure("component_state_unknown")
		}
		result[id] = entry.Revision
	}
	return result, nil
}
