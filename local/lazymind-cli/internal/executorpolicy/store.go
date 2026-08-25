package executorpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var providers = []string{"codex", "cursor", "workbuddy"}

type Status struct {
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
}

type Store struct {
	directory string
	mu        sync.Mutex
	changed   chan struct{}
}

func New(home string) (*Store, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil, errors.New("LazyMind home is required")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	return &Store{
		directory: filepath.Join(absolute, "executor-policy"),
		changed:   make(chan struct{}),
	}, nil
}

func (s *Store) Enabled(provider string) (bool, error) {
	path, err := s.path(provider)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func (s *Store) SetEnabled(provider string, enabled bool) (Status, error) {
	path, err := s.path(provider)
	if err != nil {
		return Status{}, err
	}
	if enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Status{}, err
		}
	} else {
		if err := os.MkdirAll(s.directory, 0o700); err != nil {
			return Status{}, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return Status{}, err
		}
		if err := file.Close(); err != nil {
			return Status{}, err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return Status{}, err
		}
	}
	s.notify()
	return Status{Provider: provider, Enabled: enabled}, nil
}

func (s *Store) Statuses() (map[string]Status, error) {
	statuses := make(map[string]Status, len(providers))
	for _, provider := range providers {
		enabled, err := s.Enabled(provider)
		if err != nil {
			return nil, err
		}
		statuses[provider] = Status{Provider: provider, Enabled: enabled}
	}
	return statuses, nil
}

func (s *Store) Changes() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func (s *Store) notify() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *Store) path(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, supported := range providers {
		if provider == supported {
			return filepath.Join(s.directory, provider+".disabled"), nil
		}
	}
	return "", errors.New("unsupported external Agent provider")
}
