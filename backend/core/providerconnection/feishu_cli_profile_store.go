package providerconnection

// Profile permissions are fixed at 0700 for directories and 0600 for files.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	ErrCLIProfileNotFound       = errors.New("PROFILE_NOT_FOUND")
	ErrCLIProfileOwnerMismatch  = errors.New("PROFILE_OWNER_MISMATCH")
	ErrCLIProfileTenantMismatch = errors.New("PROFILE_TENANT_MISMATCH")
)

var feishuCLIProfileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type FeishuCLIProfile struct {
	Reference    string
	RootDir      string
	ConfigDir    string
	StateDir     string
	DownloadsDir string
	LocalUserID  string
	ConnectionID string
}

type FeishuCLIProfileIdentity struct {
	LocalUserID  string    `json:"local_user_id"`
	ConnectionID string    `json:"connection_id"`
	TenantKey    string    `json:"tenant_key"`
	OpenID       string    `json:"open_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type FeishuCLIProfileStore struct {
	root string
}

func NewFeishuCLIProfileStore(root string) (*FeishuCLIProfileStore, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return nil, ErrCLIProfileNotFound
	}
	if err := secureMkdirAll(root); err != nil {
		return nil, err
	}
	return &FeishuCLIProfileStore{root: root}, nil
}

func (store *FeishuCLIProfileStore) Ensure(_ context.Context, localUserID, connectionID string) (FeishuCLIProfile, error) {
	profile, err := store.profile(localUserID, connectionID)
	if err != nil {
		return FeishuCLIProfile{}, err
	}
	for _, path := range []string{profile.RootDir, profile.ConfigDir, profile.StateDir, profile.DownloadsDir} {
		if err := secureMkdirAll(path); err != nil {
			return FeishuCLIProfile{}, err
		}
	}
	return profile, nil
}

func (store *FeishuCLIProfileStore) Load(_ context.Context, localUserID, connectionID string) (FeishuCLIProfile, error) {
	profile, err := store.profile(localUserID, connectionID)
	if err != nil {
		return FeishuCLIProfile{}, err
	}
	for _, path := range []string{profile.RootDir, profile.ConfigDir, profile.StateDir, profile.DownloadsDir} {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return FeishuCLIProfile{}, ErrCLIProfileNotFound
		}
	}
	return profile, nil
}

func (store *FeishuCLIProfileStore) BindIdentity(ctx context.Context, profile FeishuCLIProfile, tenantKey, openID string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.validateProfile(profile); err != nil {
		return err
	}
	tenantKey = strings.TrimSpace(tenantKey)
	openID = strings.TrimSpace(openID)
	if tenantKey == "" || openID == "" || len(tenantKey) > 512 || len(openID) > 512 {
		return ErrCLIProfileTenantMismatch
	}
	current, err := store.Identity(ctx, profile)
	if err != nil && !errors.Is(err, ErrCLIProfileNotFound) {
		return err
	}
	if err == nil && (current.LocalUserID != profile.LocalUserID || current.ConnectionID != profile.ConnectionID) {
		return ErrCLIProfileOwnerMismatch
	}
	if err == nil && current.TenantKey != "" && (current.TenantKey != tenantKey || current.OpenID != openID) {
		return ErrCLIProfileTenantMismatch
	}
	return writeSecureJSON(filepath.Join(profile.StateDir, "identity.json"), FeishuCLIProfileIdentity{
		LocalUserID: profile.LocalUserID, ConnectionID: profile.ConnectionID,
		TenantKey: tenantKey, OpenID: openID, UpdatedAt: now.UTC(),
	})
}

func (store *FeishuCLIProfileStore) Identity(ctx context.Context, profile FeishuCLIProfile) (FeishuCLIProfileIdentity, error) {
	if err := ctx.Err(); err != nil {
		return FeishuCLIProfileIdentity{}, err
	}
	if err := store.validateProfile(profile); err != nil {
		return FeishuCLIProfileIdentity{}, err
	}
	file, err := os.Open(filepath.Join(profile.StateDir, "identity.json"))
	if err != nil {
		return FeishuCLIProfileIdentity{}, ErrCLIProfileNotFound
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var identity FeishuCLIProfileIdentity
	if err := decoder.Decode(&identity); err != nil {
		return FeishuCLIProfileIdentity{}, ErrCLIProfileNotFound
	}
	if identity.LocalUserID != profile.LocalUserID || identity.ConnectionID != profile.ConnectionID {
		return FeishuCLIProfileIdentity{}, ErrCLIProfileOwnerMismatch
	}
	return identity, nil
}

func (store *FeishuCLIProfileStore) WriteState(ctx context.Context, profile FeishuCLIProfile, name string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.validateProfile(profile); err != nil {
		return err
	}
	if !feishuCLIProfileIDPattern.MatchString(name) {
		return ErrCLIProfileNotFound
	}
	return writeSecureJSON(filepath.Join(profile.StateDir, name+".json"), value)
}

func (store *FeishuCLIProfileStore) ReadState(ctx context.Context, profile FeishuCLIProfile, name string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.validateProfile(profile); err != nil || !feishuCLIProfileIDPattern.MatchString(name) || value == nil {
		return ErrCLIProfileNotFound
	}
	file, err := os.Open(filepath.Join(profile.StateDir, name+".json"))
	if err != nil {
		return ErrCLIProfileNotFound
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return ErrCLIProfileNotFound
	}
	return nil
}

func (store *FeishuCLIProfileStore) FindState(ctx context.Context, localUserID, name string, value any) (FeishuCLIProfile, error) {
	if err := ctx.Err(); err != nil {
		return FeishuCLIProfile{}, err
	}
	if store == nil || !feishuCLIProfileIDPattern.MatchString(localUserID) || !feishuCLIProfileIDPattern.MatchString(name) || value == nil {
		return FeishuCLIProfile{}, ErrCLIProfileNotFound
	}
	ownerRoot := filepath.Join(store.root, localUserID)
	entries, err := os.ReadDir(ownerRoot)
	if err != nil || len(entries) > 512 {
		return FeishuCLIProfile{}, ErrCLIProfileNotFound
	}
	for _, entry := range entries {
		if !entry.IsDir() || !feishuCLIProfileIDPattern.MatchString(entry.Name()) {
			continue
		}
		profile, profileErr := store.Load(ctx, localUserID, entry.Name())
		if profileErr != nil {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(profile.StateDir, name+".json")); statErr != nil {
			continue
		}
		if readErr := store.ReadState(ctx, profile, name, value); readErr != nil {
			return FeishuCLIProfile{}, readErr
		}
		return profile, nil
	}
	return FeishuCLIProfile{}, ErrCLIProfileNotFound
}

func (store *FeishuCLIProfileStore) profile(localUserID, connectionID string) (FeishuCLIProfile, error) {
	if store == nil || !feishuCLIProfileIDPattern.MatchString(localUserID) || !feishuCLIProfileIDPattern.MatchString(connectionID) {
		return FeishuCLIProfile{}, ErrCLIProfileNotFound
	}
	root := filepath.Join(store.root, localUserID, connectionID)
	cleanRoot := filepath.Clean(root)
	prefix := store.root + string(filepath.Separator)
	if !strings.HasPrefix(cleanRoot, prefix) {
		return FeishuCLIProfile{}, ErrCLIProfileNotFound
	}
	return FeishuCLIProfile{
		Reference: localUserID + "/" + connectionID, RootDir: cleanRoot,
		ConfigDir: filepath.Join(cleanRoot, "config"), StateDir: filepath.Join(cleanRoot, "state"),
		DownloadsDir: filepath.Join(cleanRoot, "downloads"), LocalUserID: localUserID, ConnectionID: connectionID,
	}, nil
}

func (store *FeishuCLIProfileStore) validateProfile(profile FeishuCLIProfile) error {
	expected, err := store.profile(profile.LocalUserID, profile.ConnectionID)
	if err != nil {
		return err
	}
	if profile.Reference != expected.Reference || filepath.Clean(profile.RootDir) != expected.RootDir ||
		filepath.Clean(profile.ConfigDir) != expected.ConfigDir || filepath.Clean(profile.StateDir) != expected.StateDir ||
		filepath.Clean(profile.DownloadsDir) != expected.DownloadsDir {
		return ErrCLIProfileOwnerMismatch
	}
	return nil
}

func secureMkdirAll(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ErrCLIProfileNotFound
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func writeSecureJSON(path string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w", ErrCLIProfileNotFound)
	}
	return nil
}
