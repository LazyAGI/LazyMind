//go:build linux || darwin

package credentialvault

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// fileLocalKeyStore is an explicit server deployment option, never a native
// desktop credential-store fallback. Its directory must be backed up together
// with the encrypted database. Keys are immutable until explicitly deleted.
type fileLocalKeyStore struct{ directory string }

func newFileLocalKeyStore(directory string) LocalKeyStore {
	return &fileLocalKeyStore{directory: directory}
}

// Walk with directory descriptors and O_NOFOLLOW, including ancestors. No
// pathname-based check followed by an open can race a substituted symlink.
func (s *fileLocalKeyStore) openDirectory() (int, error) {
	if !filepath.IsAbs(s.directory) || filepath.Clean(s.directory) == "/" {
		return -1, ErrLocalSecureStoreUnavailable
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, ErrLocalSecureStoreUnavailable
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(s.directory), "/"), "/")
	for _, part := range parts {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err == unix.ENOENT {
			if err = unix.Mkdirat(fd, part, 0700); err == nil || err == unix.EEXIST {
				// Persist the new directory entry before publishing any key below
				// it. Also sync when a concurrent creator won Mkdirat.
				if err = unix.Fsync(fd); err == nil {
					next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
				}
			}
		}
		unix.Close(fd)
		if err != nil {
			return -1, ErrLocalSecureStoreUnavailable
		}
		fd = next
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&0777 != 0700 || st.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return -1, ErrLocalSecureStoreUnavailable
	}
	return fd, nil
}
func fileKeyName(scope AccountScope, kind LocalKeyKind) string {
	digest := sha256.Sum256([]byte(localKeyStoreAccount(scope, kind)))
	return hex.EncodeToString(digest[:]) + ".key"
}
func readFileKey(dir int, name string) ([]byte, error) {
	fd, e := unix.Openat(dir, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if e == unix.ENOENT {
		return nil, ErrLocalKeyNotFound
	}
	if e != nil {
		return nil, ErrLocalSecureStoreUnavailable
	}
	f := os.NewFile(uintptr(fd), "credential-key")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return nil, ErrLocalSecureStoreUnavailable
	}
	value, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(value) == 0 || len(value) > 4096 {
		return nil, ErrLocalSecureStoreUnavailable
	}
	return value, nil
}
func (s *fileLocalKeyStore) Load(ctx context.Context, scope AccountScope, kind LocalKeyKind) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !validAccountScope(scope) {
		return nil, ErrLocalSecureStoreUnavailable
	}
	dir, e := s.openDirectory()
	if e != nil {
		return nil, e
	}
	defer unix.Close(dir)
	return readFileKey(dir, fileKeyName(scope, kind))
}
func (s *fileLocalKeyStore) Save(ctx context.Context, scope AccountScope, kind LocalKeyKind, value []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !validAccountScope(scope) || len(value) == 0 || len(value) > 4096 {
		return ErrLocalSecureStoreUnavailable
	}
	dir, e := s.openDirectory()
	if e != nil {
		return e
	}
	defer unix.Close(dir)
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return ErrLocalSecureStoreUnavailable
	}
	temp := ".key-" + hex.EncodeToString(random[:])
	name := fileKeyName(scope, kind)
	fd, e := unix.Openat(dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return ErrLocalSecureStoreUnavailable
	}
	defer unix.Unlinkat(dir, temp, 0)
	f := os.NewFile(uintptr(fd), "credential-key")
	_, e = f.Write(value)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return ErrLocalSecureStoreUnavailable
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	// An exclusive rename publishes flushed bytes only if no key exists. Never overwrite
	// another process's encryption root after a concurrent Load reported missing.
	e = publishFileKey(dir, temp, name)
	if e == unix.EEXIST {
		existing, err := readFileKey(dir, name)
		if err == nil && bytes.Equal(existing, value) {
			return nil
		}
		return ErrLocalSecureStoreUnavailable
	}
	if e != nil {
		return ErrLocalSecureStoreUnavailable
	}
	if unix.Fsync(dir) != nil {
		return ErrLocalSecureStoreUnavailable
	}
	return nil
}
func (s *fileLocalKeyStore) Delete(ctx context.Context, scope AccountScope, kind LocalKeyKind) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !validAccountScope(scope) {
		return ErrLocalSecureStoreUnavailable
	}
	dir, e := s.openDirectory()
	if e != nil {
		return e
	}
	defer unix.Close(dir)
	name := fileKeyName(scope, kind)
	if _, e = readFileKey(dir, name); e == ErrLocalKeyNotFound {
		return nil
	} else if e != nil {
		return e
	}
	if e = unix.Unlinkat(dir, name, 0); e != nil && e != unix.ENOENT {
		return ErrLocalSecureStoreUnavailable
	}
	if unix.Fsync(dir) != nil {
		return ErrLocalSecureStoreUnavailable
	}
	return nil
}
