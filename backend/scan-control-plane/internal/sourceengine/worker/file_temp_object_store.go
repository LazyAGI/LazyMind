package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const scanTempScheme = "scan-temp"

var (
	errInvalidTempURI   = errors.New("invalid scan temp uri")
	errInvalidTempToken = errors.New("invalid scan temp token")
	errTempNotFound     = errors.New("scan temp object not found")
)

type FileTempObjectStore struct {
	baseDir string
	clock   func() time.Time

	mu     sync.Mutex
	tokens map[string]struct{}
}

func NewFileTempObjectStore(baseDir string) *FileTempObjectStore {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = filepath.Join(os.TempDir(), "scan-control-plane", "sourceengine")
	}
	return &FileTempObjectStore{
		baseDir: baseDir,
		clock:   time.Now,
		tokens:  map[string]struct{}{},
	}
}

func (s *FileTempObjectStore) Put(ctx context.Context, input TempObjectInput) (TempObject, error) {
	if err := ctx.Err(); err != nil {
		return TempObject{}, err
	}
	if input.Reader == nil {
		return TempObject{}, errors.New("temp object reader is required")
	}
	baseDir, err := s.ensureBaseDir()
	if err != nil {
		return TempObject{}, err
	}
	for attempt := 0; attempt < 10; attempt++ {
		token, err := newTempToken()
		if err != nil {
			return TempObject{}, err
		}
		path, err := pathForTempToken(baseDir, token)
		if err != nil {
			return TempObject{}, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return TempObject{}, err
		}
		size, err := io.Copy(file, contextReader{ctx: ctx, reader: input.Reader})
		closeErr := file.Close()
		if err != nil {
			_ = os.Remove(path)
			return TempObject{}, err
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return TempObject{}, closeErr
		}
		s.remember(token)
		createdAt := s.now().UTC()
		return TempObject{
			URI:          scanTempURI(token),
			CleanupToken: token,
			SizeBytes:    size,
			CreatedAt:    createdAt,
		}, nil
	}
	return TempObject{}, errors.New("could not allocate temp object token")
}

func (s *FileTempObjectStore) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token, err := tokenFromScanTempURI(uri)
	if err != nil {
		return nil, err
	}
	if !s.known(token) {
		return nil, errTempNotFound
	}
	baseDir, err := s.resolveBaseDir()
	if err != nil {
		return nil, err
	}
	path, err := pathForTempToken(baseDir, token)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errTempNotFound
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errTempNotFound
	}
	return os.Open(path)
}

func (s *FileTempObjectStore) Cleanup(ctx context.Context, cleanupToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := tokenFromCleanupToken(cleanupToken)
	if err != nil {
		return err
	}
	if !s.known(token) {
		return nil
	}
	baseDir, err := s.resolveBaseDir()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.forget(token)
			return nil
		}
		return err
	}
	path, err := pathForTempToken(baseDir, token)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.forget(token)
	return nil
}

func (s *FileTempObjectStore) CleanupExpired(ctx context.Context, ttl time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if ttl <= 0 {
		return 0, errors.New("temp ttl must be positive")
	}
	baseDir, err := s.resolveBaseDir()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	expiredBefore := s.now().UTC().Add(-ttl)
	cleaned := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return cleaned, err
		}
		name := entry.Name()
		if validateTempToken(name) != nil {
			continue
		}
		path, err := pathForTempToken(baseDir, name)
		if err != nil {
			return cleaned, err
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return cleaned, err
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.ModTime().After(expiredBefore) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return cleaned, err
		}
		s.forget(name)
		cleaned++
	}
	return cleaned, nil
}

func (s *FileTempObjectStore) ensureBaseDir() (string, error) {
	if s == nil {
		return "", errors.New("temp object store is nil")
	}
	baseDir, err := filepath.Abs(s.baseDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(baseDir)
}

func (s *FileTempObjectStore) resolveBaseDir() (string, error) {
	if s == nil {
		return "", errors.New("temp object store is nil")
	}
	baseDir, err := filepath.Abs(s.baseDir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(baseDir)
}

func (s *FileTempObjectStore) remember(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokens == nil {
		s.tokens = map[string]struct{}{}
	}
	s.tokens[token] = struct{}{}
}

func (s *FileTempObjectStore) known(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tokens[token]
	return ok
}

func (s *FileTempObjectStore) forget(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

func (s *FileTempObjectStore) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func newTempToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func scanTempURI(token string) string {
	return (&url.URL{Scheme: scanTempScheme, Host: token}).String()
}

func tokenFromScanTempURI(rawURI string) (string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidTempURI, err)
	}
	if parsed.Scheme != scanTempScheme || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", errInvalidTempURI
	}
	if err := validateTempToken(parsed.Host); err != nil {
		return "", err
	}
	return parsed.Host, nil
}

func tokenFromCleanupToken(cleanupToken string) (string, error) {
	if strings.HasPrefix(cleanupToken, scanTempScheme+"://") {
		return tokenFromScanTempURI(cleanupToken)
	}
	if err := validateTempToken(cleanupToken); err != nil {
		return "", err
	}
	return cleanupToken, nil
}

func validateTempToken(token string) error {
	if len(token) != 32 {
		return errInvalidTempToken
	}
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return errInvalidTempToken
		}
	}
	return nil
}

func pathForTempToken(baseDir, token string) (string, error) {
	if err := validateTempToken(token); err != nil {
		return "", err
	}
	path := filepath.Join(baseDir, token)
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errInvalidTempToken
	}
	return path, nil
}
