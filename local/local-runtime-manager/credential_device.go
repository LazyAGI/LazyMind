package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/storagepath"
)

const cloudCredentialKeyEnvVar = "LAZYMIND_AUTH_CLOUD_SECRET_KEY"
const legacyCloudCredentialKey = "dev-ragscan-secret-key-change-me"
const modelCredentialKeyEnvVar = "LAZYMIND_MODEL_PROVIDER_SECRET_KEY"

var authDeviceCredentialTables = map[string][]string{
	"cloud_auth_connections": {"credential_ciphertext", "auth_state_ciphertext"},
	"mcp_oauth_grants":       {"ciphertext"},
	"mcp_oauth_states":       {"ciphertext"},
}

var coreDeviceCredentialTables = map[string][]string{
	"user_model_provider_groups": {"api_key_ciphertext"},
	"mcp_servers":                {"headers_json"},
}

type credentialDeviceIdentity struct {
	Version      int    `json:"version"`
	DeviceID     string `json:"deviceId"`
	DeviceSecret string `json:"deviceSecret"`
}

type credentialStore struct {
	path   string
	tables map[string][]string
}

func cloudCredentialKey() (string, error) {
	return cloudCredentialKeyForPaths(RuntimePaths{})
}

func cloudCredentialKeyForPaths(paths RuntimePaths) (string, error) {
	if configured := configuredCloudCredentialKey(); configured != "" {
		if err := validateExplicitCredentialScope(paths); err != nil {
			return "", err
		}
		return configured, nil
	}
	return deviceCredentialKeyForPaths("cloud-oauth", paths)
}

func configuredCloudCredentialKey() string {
	key := strings.TrimSpace(os.Getenv(cloudCredentialKeyEnvVar))
	if key == legacyCloudCredentialKey {
		return ""
	}
	return key
}

func configuredModelCredentialKey() string {
	return strings.TrimSpace(os.Getenv(modelCredentialKeyEnvVar))
}

func modelCredentialKey() (string, error) {
	return modelCredentialKeyForPaths(RuntimePaths{})
}

func modelCredentialKeyForPaths(paths RuntimePaths) (string, error) {
	if configured := configuredModelCredentialKey(); configured != "" {
		if err := validateExplicitCredentialScope(paths); err != nil {
			return "", err
		}
		return configured, nil
	}
	return deviceCredentialKeyForPaths("model-provider", paths)
}

func deviceCredentialKey(purpose string) (string, error) {
	return deviceCredentialKeyForPaths(purpose, RuntimePaths{})
}

func deviceCredentialKeyForPaths(purpose string, paths RuntimePaths) (string, error) {
	identity, err := credentialDeviceForPaths(paths)
	if err != nil {
		return "", err
	}
	secret, err := decodeCredentialDeviceSecret(identity)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "lazymind/desktop/%s/%s/v1", identity.DeviceID, purpose)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func credentialDeviceForPaths(paths RuntimePaths) (credentialDeviceIdentity, error) {
	root := windowsCredentialRoot(paths)
	if root == "" {
		path, err := checkedCredentialIdentityPath(paths)
		if err != nil {
			return credentialDeviceIdentity{}, err
		}
		return loadOrCreateCredentialDevice(path, os.Link)
	}
	physicalRoot, release, err := storagepath.HoldLocalLocation(root)
	if err != nil {
		return credentialDeviceIdentity{}, err
	}
	defer release()
	path, identityRoot, err := credentialIdentityLocation(paths)
	if err != nil {
		return credentialDeviceIdentity{}, err
	}
	if !strings.EqualFold(identityRoot, root) {
		return credentialDeviceIdentity{}, fmt.Errorf("credential data location changed during setup")
	}
	if err := storagepath.CheckLocalPath(root, path); err != nil {
		return credentialDeviceIdentity{}, err
	}
	// A valid identity needs no store inspection. Only protect the database
	// names while establishing a new device identity.
	identity, err := readCredentialDevice(filepath.Join(physicalRoot, "Desktop", filepath.Base(path)))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return identity, err
	}
	releaseStores, err := holdAndCheckCredentialStores(paths, root, physicalRoot, true)
	if err != nil {
		return credentialDeviceIdentity{}, err
	}
	defer releaseStores()
	if err := os.MkdirAll(physicalRoot, 0o700); err != nil {
		return credentialDeviceIdentity{}, err
	}
	verifiedRoot, releaseRoot, err := storagepath.HoldLocalLocation(root)
	if err != nil {
		return credentialDeviceIdentity{}, err
	}
	defer releaseRoot()
	if !strings.EqualFold(physicalRoot, verifiedRoot) {
		return credentialDeviceIdentity{}, fmt.Errorf("Local data location changed during credential setup")
	}
	if _, currentRoot, err := credentialIdentityLocation(paths); err != nil {
		return credentialDeviceIdentity{}, err
	} else if !strings.EqualFold(currentRoot, root) {
		return credentialDeviceIdentity{}, fmt.Errorf("credential data location changed during setup")
	}
	directory := filepath.Join(physicalRoot, "Desktop")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return credentialDeviceIdentity{}, err
	}
	physicalDirectory, releaseDirectory, err := storagepath.HoldLocalLocation(directory)
	if err != nil {
		return credentialDeviceIdentity{}, err
	}
	defer releaseDirectory()
	if !strings.EqualFold(physicalDirectory, directory) {
		return credentialDeviceIdentity{}, fmt.Errorf("Local identity directory redirects to another location")
	}
	physicalPath := filepath.Join(physicalDirectory, filepath.Base(path))
	if err := storagepath.CheckLocalPath(physicalRoot, physicalPath); err != nil {
		return credentialDeviceIdentity{}, err
	}
	return loadOrCreateCredentialDevice(physicalPath, storagepath.PublishLocalIdentity)
}

func holdAndCheckCredentialStores(paths RuntimePaths, root, physicalRoot string, reserveMissing bool) (func(), error) {
	stores, err := scopedCredentialStores(paths)
	if err != nil {
		return nil, err
	}
	storePaths := make([]string, 0, len(stores))
	for _, store := range stores {
		storePaths = append(storePaths, store.path)
	}
	legacyDesktop := runtime.GOOS == "windows" && paths.DesktopAppDir != "" && paths.registeredCredentialRoot == ""
	physicalStores, release, err := storagepath.HoldCredentialStores(root, physicalRoot, storePaths, reserveMissing, legacyDesktop)
	if err != nil {
		return nil, fmt.Errorf("inspect credentials before creating a device identity: %w", err)
	}
	for i, physical := range physicalStores {
		if physical != "" {
			if err := checkCredentialStore(physical, stores[i].tables, true); err != nil {
				release()
				return nil, err
			}
		}
	}
	return release, nil
}

// Prepare before starting Core's sqliteproxy server or Auth. Both services can
// persist to their stores, so first identity creation must happen before them.
func prepareScopedWindowsCredentials(paths RuntimePaths) error {
	if windowsCredentialRoot(paths) == "" {
		return nil
	}
	if configuredCloudCredentialKey() != "" && configuredModelCredentialKey() != "" && !desktopNeedsMCPIdentity(paths) {
		return validateExplicitCredentialScope(paths)
	}
	_, err := credentialDeviceForPaths(paths)
	return err
}

func ensureWindowsDesktopCredentialIdentity(cfg RuntimeConfig, paths RuntimePaths) error {
	root := windowsCredentialRoot(paths)
	if runtime.GOOS != "windows" || cfg.Profile != "desktop" || root == "" {
		return fmt.Errorf("ensuring the Desktop credential identity requires Windows Desktop storage")
	}
	if desktopRoot := strings.TrimSpace(os.Getenv("LAZYMIND_DESKTOP_DATA_ROOT")); desktopRoot != "" &&
		(!filepath.IsAbs(desktopRoot) || !strings.EqualFold(filepath.Clean(desktopRoot), root)) {
		return fmt.Errorf("Desktop credential data location conflicts with the runtime storage layout")
	}
	_, err := credentialDeviceForPaths(paths)
	return err
}

func desktopNeedsMCPIdentity(paths RuntimePaths) bool {
	// Electron derives this key separately from the model provider key.
	return paths.DesktopAppDir != "" && strings.TrimSpace(os.Getenv("LAZYMIND_MCP_SECRET_KEY")) == ""
}

func credentialDeviceIdentityPath() string {
	path, _, _ := credentialIdentityLocation(RuntimePaths{})
	return path
}

// Supplying a key bypasses derivation, not verification of the selected storage.
func validateExplicitCredentialScope(paths RuntimePaths) error {
	if paths.explicitWindowsLocalRoot == "" && windowsCredentialRoot(paths) == "" {
		return nil
	}
	_, _, err := credentialIdentityLocation(paths)
	return err
}

func windowsCredentialRoot(paths RuntimePaths) string {
	if paths.independentLocalCredentials {
		return paths.explicitWindowsLocalRoot
	}
	if runtime.GOOS == "windows" && paths.DesktopAppDir != "" && paths.registeredCredentialRoot == "" {
		return defaultRuntimePathLayout().DataRoot
	}
	return paths.registeredCredentialRoot
}

func credentialIdentityLocation(paths RuntimePaths) (string, string, error) {
	if localRoot := paths.explicitWindowsLocalRoot; localRoot != "" {
		installedRoot, independent, err := storagepath.ResolveExplicitLocal(localRoot, defaultRuntimePathLayout().DataRoot)
		if err != nil {
			return "", "", err
		}
		if (installedRoot != "" && !independent) || independent != paths.independentLocalCredentials {
			return "", "", fmt.Errorf("registered Local data location changed during credential setup")
		}
		if independent {
			return filepath.Join(localRoot, "Desktop", "credential-device.json"), localRoot, nil
		}
		return legacyCredentialIdentityLocation()
	}
	root, installed, err := storagepath.Resolve(paths.DesktopAppDir)
	if err != nil {
		return "", "", err
	}
	if paths.registeredCredentialRoot != "" && (!installed || !strings.EqualFold(root, paths.registeredCredentialRoot)) {
		return "", "", fmt.Errorf("registered Local data location changed during credential setup")
	}
	if installed {
		return filepath.Join(root, "Desktop", "credential-device.json"), root, nil
	}
	return legacyCredentialIdentityLocation()
}

func legacyCredentialIdentityLocation() (string, string, error) {
	return credentialDeviceIdentityPathForOS(runtime.GOOS, hostHomeDir(), os.Getenv("LOCALAPPDATA")),
		defaultRuntimePathLayout().DataRoot, nil
}

func actualAuthCredentialDB(paths RuntimePaths, defaultPath string) (string, error) {
	database := paths.authCredentialDatabaseURL
	if database == "" {
		return defaultPath, nil
	}
	path, ok := strings.CutPrefix(database, "sqlite:///")
	if !ok {
		path, ok = strings.CutPrefix(database, "sqlite+pysqlite:///")
	}
	// URI options, in-memory databases and relative filenames have different
	// resolution rules. A configured store must identify a definite disk file.
	path = filepath.FromSlash(path)
	if !ok || !filepath.IsAbs(path) || strings.ContainsAny(path, "?\x00") {
		return "", fmt.Errorf("inspect credentials: cannot verify the configured Auth database")
	}
	return filepath.Clean(path), nil
}

// checkedCredentialIdentityPath is also the preflight boundary used before
// Electron creates its shared identity. It never creates an identity or store.
func checkedCredentialIdentityPath(paths RuntimePaths) (string, error) {
	path, root, err := credentialIdentityLocation(paths)
	if err != nil {
		return "", err
	}
	if windowsCredentialRoot(paths) != "" {
		if err := storagepath.CheckLocalPath(root, path); err != nil {
			return "", err
		}
	}
	if _, err := readCredentialDevice(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if windowsCredentialRoot(paths) != "" {
		physicalRoot, releaseRoot, err := storagepath.HoldLocalLocation(root)
		if err != nil {
			return "", err
		}
		defer releaseRoot()
		releaseStores, err := holdAndCheckCredentialStores(paths, root, physicalRoot, false)
		if err != nil {
			return "", err
		}
		releaseStores()
		return path, nil
	}
	roots := []string{root, firstNonEmpty(paths.RuntimeRoot, root)}
	seenAuth, seenCore := map[string]bool{}, map[string]bool{}
	for index, dataRoot := range roots {
		sqliteRoot := filepath.Join(dataRoot, "data", "stores", "sqlite")
		if index == 1 && paths.RuntimeRoot != "" {
			sqliteRoot = envText(localSQLiteDirEnvVar, sqliteRoot)
		}
		authPath := filepath.Join(sqliteRoot, "auth", "authservice.db")
		corePath := filepath.Join(sqliteRoot, "core", "core.db")
		if index == 1 {
			authPath = firstNonEmpty(paths.AuthServiceDBPath, authPath)
			if paths.explicitWindowsLocalRoot != "" || paths.installedLocalDataRoot != "" {
				authPath, err = actualAuthCredentialDB(paths, authPath)
				if err != nil {
					return "", err
				}
			}
			corePath = firstNonEmpty(paths.CoreDBPath, corePath)
		}
		for _, store := range []struct {
			path   string
			tables map[string][]string
			seen   map[string]bool
		}{
			{authPath, authDeviceCredentialTables, seenAuth},
			{corePath, coreDeviceCredentialTables, seenCore},
		} {
			if store.seen[store.path] {
				continue
			}
			store.seen[store.path] = true
			if err := checkCredentialStore(store.path, store.tables, false); err != nil {
				return "", err
			}
		}
	}
	return path, nil
}

func scopedCredentialStores(paths RuntimePaths) ([]credentialStore, error) {
	var stores []credentialStore
	if configuredCloudCredentialKey() == "" {
		authPath, err := actualAuthCredentialDB(paths, paths.AuthServiceDBPath)
		if err != nil {
			return nil, err
		}
		stores = append(stores, credentialStore{authPath, authDeviceCredentialTables})
	}
	var tables map[string][]string
	if configuredModelCredentialKey() == "" {
		tables = coreDeviceCredentialTables
		if paths.DesktopAppDir != "" && !desktopNeedsMCPIdentity(paths) {
			tables = map[string][]string{"user_model_provider_groups": {"api_key_ciphertext"}}
		}
	} else if desktopNeedsMCPIdentity(paths) {
		tables = map[string][]string{"mcp_servers": {"headers_json"}}
	}
	if tables != nil {
		stores = append(stores, credentialStore{paths.CoreDBPath, tables})
	}
	return stores, nil
}

func checkCredentialStore(path string, tables map[string][]string, held bool) error {
	found, err := storedDeviceCredentials(path, tables, held)
	if err != nil {
		return fmt.Errorf("inspect credentials before creating a device identity: %w", err)
	}
	if found {
		return fmt.Errorf("credential device identity is missing while encrypted credentials remain; restore the original identity")
	}
	return nil
}

func storedDeviceCredentials(path string, tables map[string][]string, held bool) (bool, error) {
	var uri url.URL
	if held {
		// The held main file cannot be written or replaced and all journal
		// names are reserved. Keep the volume-GUID namespace verbatim.
		uri = url.URL{Scheme: "file", Opaque: url.PathEscape(path), RawQuery: "mode=ro&immutable=1"}
	} else {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		uriPath := filepath.ToSlash(path)
		if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
			uriPath = "/" + uriPath
		}
		uri = url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return false, err
	}
	defer db.Close()
	for table, fields := range tables {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return false, err
		}
		columns := map[string]bool{}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return false, err
			}
			columns[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		for _, field := range fields {
			if !columns[field] {
				continue
			}
			predicate := "TRIM(COALESCE(" + field + ", '')) <> ''"
			if table == "cloud_auth_connections" && (field == "credential_ciphertext" || field == "auth_state_ciphertext") {
				predicate += " AND " + field + " COLLATE BINARY NOT IN ('managed-reference-v1', 'cli-profile-reference-v1')"
			}
			if field == "headers_json" {
				predicate = "CASE WHEN json_valid(headers_json) THEN json_extract(headers_json, '$.enc') = 'aes-gcm' ELSE 0 END"
			}
			var found bool
			if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM " + table + " WHERE " + predicate + ")").Scan(&found); err != nil {
				return false, err
			}
			if found {
				return true, nil
			}
		}
	}
	return false, nil
}

func credentialDeviceIdentityPathForOS(goos, home, localAppData string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "lazymind-desktop", "credential-device.json")
	case "windows":
		root := strings.TrimSpace(localAppData)
		if root == "" {
			root = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(root, "LazyMind", "Desktop", "credential-device.json")
	default:
		return filepath.Join(home, ".config", "lazymind-desktop", "credential-device.json")
	}
}

func readCredentialDevice(path string) (credentialDeviceIdentity, error) {
	var identity credentialDeviceIdentity
	raw, err := os.ReadFile(path)
	if err != nil {
		return identity, fmt.Errorf("read credential device identity: %w", err)
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return identity, fmt.Errorf("invalid credential device identity")
	}
	if _, err := decodeCredentialDeviceSecret(identity); err != nil {
		return identity, err
	}
	return identity, nil
}

func decodeCredentialDeviceSecret(identity credentialDeviceIdentity) ([]byte, error) {
	secret, err := base64.RawURLEncoding.DecodeString(identity.DeviceSecret)
	if err != nil {
		secret, err = base64.URLEncoding.DecodeString(identity.DeviceSecret)
	}
	if identity.Version != 1 || strings.TrimSpace(identity.DeviceID) == "" || err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("invalid credential device identity")
	}
	return secret, nil
}

func loadOrCreateCredentialDevice(path string, publish func(string, string) error) (credentialDeviceIdentity, error) {
	identity, err := readCredentialDevice(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return identity, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return identity, fmt.Errorf("create credential device directory: %w", err)
	}
	var id [16]byte
	var secret [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return identity, fmt.Errorf("generate credential device identity: %w", err)
	}
	if _, err := rand.Read(secret[:]); err != nil {
		return identity, fmt.Errorf("generate credential device identity: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	identity = credentialDeviceIdentity{
		Version:      1,
		DeviceID:     fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]),
		DeviceSecret: base64.RawURLEncoding.EncodeToString(secret[:]),
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return identity, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".credential-device-*.tmp")
	if err != nil {
		return identity, fmt.Errorf("create credential device identity: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return identity, fmt.Errorf("write credential device identity: %w", writeErr)
	}
	if closeErr != nil {
		return identity, fmt.Errorf("close credential device identity: %w", closeErr)
	}
	// Publish a complete file without replacing an identity created by another process.
	if err := publish(file.Name(), path); err != nil && !os.IsExist(err) {
		return identity, fmt.Errorf("publish credential device identity: %w", err)
	}
	return readCredentialDevice(path)
}
