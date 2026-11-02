package desktopupdate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type StartupLease struct {
	root, path string
	mu         sync.Mutex
}
type startupRecord struct {
	Version     int             `json:"version"`
	Scope       UpdateScope     `json:"scope"`
	OwnerDigest string          `json:"ownerDigest"`
	Role        string          `json:"role"`
	Actor       ProcessIdentity `json:"actor"`
}

func randomTransactionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Resolve existing symlink ancestors without creating missing data paths.
func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", failure("path_invalid", nil)
	}
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	parent, err = canonicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
func pathKey(path string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}
func within(parent, child string) bool {
	parent, child = pathKey(parent), pathKey(child)
	return parent == child || strings.HasPrefix(child, parent+string(filepath.Separator))
}
func overlaps(a, b string) bool { return within(a, b) || within(b, a) }

func normalizeScope(scope UpdateScope) (UpdateScope, error) {
	var result UpdateScope
	var err error
	if scope.Installation != "" {
		result.Installation, err = canonicalPath(scope.Installation)
		if err != nil || filepath.Dir(result.Installation) == result.Installation {
			return result, failure("scope_invalid", err)
		}
	}
	if (len(scope.DataRoots) == 0 && scope.Installation == "") || len(scope.DataRoots) > 128 {
		return result, failure("scope_invalid", nil)
	}
	seen := map[string]bool{}
	for _, p := range scope.DataRoots {
		resolved, err := canonicalPath(p)
		if err != nil || filepath.Dir(resolved) == resolved {
			return result, failure("scope_invalid", err)
		}
		if !seen[pathKey(resolved)] {
			result.DataRoots = append(result.DataRoots, resolved)
			seen[pathKey(resolved)] = true
		}
	}
	sort.Strings(result.DataRoots)
	return result, nil
}

func scopesOverlap(a, b UpdateScope) bool {
	if a.Installation != "" && b.Installation != "" && overlaps(a.Installation, b.Installation) {
		return true
	}
	for _, x := range a.DataRoots {
		for _, y := range b.DataRoots {
			if overlaps(x, y) {
				return true
			}
		}
	}
	return false
}

func acquireAdmission(ctx context.Context, root string) (*os.File, error) {
	if err := ensureDirectory(root); err != nil {
		return nil, failure("journal_write", err)
	}
	f, err := os.OpenFile(filepath.Join(root, "admission.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, failure("journal_lock", err)
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		if err := deadline.Err(); err != nil {
			_ = f.Close()
			return nil, failure("journal_busy", err)
		}
		locked, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, failure("journal_lock", err)
		}
		if locked {
			return f, nil
		}
		select {
		case <-deadline.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func releaseLock(f *os.File) {
	if f != nil {
		unlock(f)
		_ = f.Close()
	}
}

func readMetadata(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 2*MaxEnvelopeBytes {
		return failure("journal_corrupt", nil)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 2*MaxEnvelopeBytes+1))
	if err != nil || len(raw) > 2*MaxEnvelopeBytes {
		return failure("journal_corrupt", err)
	}
	return decodeStrict(raw, target)
}

func checkJournals(root string, scope UpdateScope) error {
	all, err := readJournals(root)
	if err != nil {
		return err
	}
	for _, j := range all {
		if !terminalStage(j.Stage) && scopesOverlap(j.ResolvedScope, scope) {
			return failure("update_in_progress", nil)
		}
	}
	return nil
}

func CheckStartup(ctx context.Context, root string, scope UpdateScope) error {
	root, err := canonicalPath(root)
	if err != nil {
		return err
	}
	scope, err = normalizeScope(scope)
	if err != nil {
		return err
	}
	if _, err = os.Stat(root); os.IsNotExist(err) {
		return ctx.Err()
	} else if err != nil {
		return err
	}
	lock, err := acquireAdmission(ctx, root)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	return checkJournals(root, scope)
}

func BeginStartup(ctx context.Context, root string, scope UpdateScope, owner, role string, actor ProcessIdentity) (*StartupLease, error) {
	root, err := canonicalPath(root)
	if err != nil {
		return nil, err
	}
	scope, err = normalizeScope(scope)
	if err != nil {
		return nil, err
	}
	if role != "writer" && role != "coordinator" {
		return nil, failure("registration_invalid", nil)
	}
	live, err := SameProcess(actor)
	if err != nil || !live {
		return nil, failure("process_identity", err)
	}
	lock, err := acquireAdmission(ctx, root)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lock)
	if UnsignedTestBuild {
		if err := CheckUnsignedCaller(scope.Installation, os.Getenv("LAZYMIND_UNSIGNED_EXPECTED_RELEASE"), os.Getenv("LAZYMIND_UNSIGNED_EXPECTED_VERSION")); err != nil {
			return nil, err
		}
	}
	if err = checkJournals(root, scope); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "startup", "v1")
	if err = ensureDirectory(dir); err != nil {
		return nil, err
	}
	id, err := randomTransactionID()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".json")
	raw, err := json.Marshal(startupRecord{1, scope, digestBytes([]byte(owner)), role, actor})
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(path, raw); err != nil {
		return nil, err
	}
	return &StartupLease{root: root, path: path}, nil
}

// Coordinator registration deliberately outlives the short guard command;
// its record is reclaimed only after the recorded parent identity is gone.
func (lease *StartupLease) Close() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.path == "" {
		return nil
	}
	lock, err := acquireAdmission(context.Background(), lease.root)
	if err != nil {
		return err
	}
	defer releaseLock(lock)
	if err = os.Remove(lease.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = syncDirectory(filepath.Dir(lease.path)); err != nil {
		return err
	}
	lease.path = ""
	return nil
}

func sourceRegistrations(root string, scope UpdateScope, owner string) ([]ProcessIdentity, []ProcessIdentity, error) {
	dir := filepath.Join(root, "startup", "v1")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var writers, coordinators []ProcessIdentity
	seen := map[string]map[ProcessIdentity]bool{"writer": {}, "coordinator": {}}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".trust-") && strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		var record startupRecord
		path := filepath.Join(dir, entry.Name())
		if !validID(strings.TrimSuffix(entry.Name(), ".json")) || readMetadata(path, &record) != nil || record.Version != 1 || (record.Role != "writer" && record.Role != "coordinator") {
			return nil, nil, failure("registration_corrupt", nil)
		}
		live, err := SameProcess(record.Actor)
		if err != nil {
			return nil, nil, err
		}
		if !live {
			if err = os.Remove(path); err != nil {
				return nil, nil, err
			}
			continue
		}
		if !scopesOverlap(record.Scope, scope) {
			continue
		}
		if record.OwnerDigest != digestBytes([]byte(owner)) {
			return nil, nil, failure("runtime_busy", nil)
		}
		if !seen[record.Role][record.Actor] {
			if record.Role == "writer" {
				writers = append(writers, record.Actor)
			} else {
				coordinators = append(coordinators, record.Actor)
			}
			seen[record.Role][record.Actor] = true
		}
	}
	return writers, coordinators, nil
}
