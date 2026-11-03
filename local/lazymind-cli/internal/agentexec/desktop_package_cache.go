package agentexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

const desktopPackageQueryTimeout = 8 * time.Second

var errPackageQueryBackoff = errors.New("desktop package discovery temporarily unavailable; retry shortly")

type desktopPackageCacheEntry struct {
	CheckedAt time.Time       `json:"checked_at"`
	Body      json.RawMessage `json:"body,omitempty"`
	Failed    bool            `json:"failed,omitempty"`
}

// A disk cache is required: Desktop polling launches a fresh CLI process, while
// browser clients use the long-lived Assistant Bridge. An in-memory cache alone
// would still start PowerShell on every Desktop poll.
type desktopPackageCache struct {
	directory string
	now       func() time.Time
}

func desktopPackageCacheDirectory() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "LazyMind", "Cache", "desktop-packages-v1"), nil
}

func normalizedPackageNames(names []string) []string {
	seen := map[string]bool{}
	for _, name := range names {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			seen[name] = true
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func packageCacheKey(names []string) string {
	sum := sha256.Sum256([]byte(strings.Join(names, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (c desktopPackageCache) lock(ctx context.Context) (func(), error) {
	if err := os.MkdirAll(c.directory, 0700); err != nil {
		return nil, err
	}
	return localfile.LockContext(ctx, filepath.Join(c.directory, "query.lock"))
}

func (c desktopPackageCache) query(ctx context.Context, names []string, query func(context.Context, []string) ([]byte, error)) ([]byte, error) {
	names = normalizedPackageNames(names)
	if len(names) == 0 {
		return nil, nil
	}
	unlock, err := c.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(c.directory, packageCacheKey(names)+".json")
	if body, err := readPackageCache(path); err == nil {
		var entry desktopPackageCacheEntry
		if json.Unmarshal(body, &entry) == nil {
			var packages []windowsAppPackage
			valid := json.Unmarshal(entry.Body, &packages) == nil
			ttl := 10 * time.Second
			if valid && len(packages) > 0 && !entry.Failed {
				ttl = 60 * time.Second
			}
			age := c.now().Sub(entry.CheckedAt)
			if age >= 0 && age < ttl {
				if entry.Failed {
					return nil, errPackageQueryBackoff
				}
				if valid {
					return append([]byte(nil), entry.Body...), nil
				}
			}
		}
	}
	// Reserve a short backoff before launching. If the process is killed or the
	// cache cannot be written, subsequent polls must not create a process storm.
	entry := desktopPackageCacheEntry{CheckedAt: c.now(), Failed: true}
	if err := writePackageCache(path, entry); err != nil {
		return nil, err
	}
	body, queryErr := query(ctx, names)
	if queryErr == nil {
		queryErr = ctx.Err()
	}
	var packages []windowsAppPackage
	if queryErr == nil {
		queryErr = json.Unmarshal([]byte(strings.TrimPrefix(string(body), "\ufeff")), &packages)
	}
	entry.CheckedAt = c.now()
	if queryErr == nil {
		entry.Body, queryErr = json.Marshal(packages)
		entry.Failed = queryErr != nil
	}
	if err := writePackageCache(path, entry); err != nil {
		return nil, err
	}
	if queryErr != nil {
		return nil, queryErr
	}
	return append([]byte(nil), entry.Body...), nil
}

func readPackageCache(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

func writePackageCache(path string, entry desktopPackageCacheEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return localfile.Write(path, body, 0600)
}

func (c desktopPackageCache) invalidate(ctx context.Context) error {
	unlock, err := c.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	paths, err := filepath.Glob(filepath.Join(c.directory, "*.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// InvalidateDesktopApplications is used only for explicit refresh, reconnect or
// binding changes. Periodic polling and window focus keep the shared cache.
func InvalidateDesktopApplications(ctx context.Context) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	directory, err := desktopPackageCacheDirectory()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, desktopPackageQueryTimeout)
	defer cancel()
	return (desktopPackageCache{directory: directory, now: time.Now}).invalidate(ctx)
}
