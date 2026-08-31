package executorpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var providers = []string{"codex", "cursor", "workbuddy"}

type BridgeState string

const (
	BridgeReady                  BridgeState = "ready"
	BridgeAuthenticationRequired BridgeState = "authentication_required"
	BridgeUnavailable            BridgeState = "unavailable"
)

type Status struct {
	Provider          string      `json:"provider"`
	Enabled           bool        `json:"enabled"`
	Installed         bool        `json:"installed,omitempty"`
	Ready             bool        `json:"ready,omitempty"`
	UnavailableReason string      `json:"unavailable_reason,omitempty"`
	BridgeState       BridgeState `json:"bridge_state,omitempty"`
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
	store := &Store{
		directory: filepath.Join(absolute, "executor-policy"),
		changed:   make(chan struct{}),
	}
	for _, provider := range providers {
		if err := removeMarker(filepath.Join(store.directory, provider+".disabled")); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Enabled(provider string) (bool, error) {
	path, err := s.providerPath(provider)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path + ".enabled"); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func (s *Store) SetEnabled(provider string, enabled bool) (Status, error) {
	path, err := s.providerPath(provider)
	if err != nil {
		return Status{}, err
	}
	enabledPath := path + ".enabled"
	if err := removeMarker(path + ".disabled"); err != nil {
		return Status{}, err
	}
	if !enabled {
		if err := removeMarker(enabledPath); err != nil {
			return Status{}, err
		}
		s.notify()
		return Status{Provider: provider}, nil
	}
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return Status{}, err
	}
	file, err := os.OpenFile(enabledPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Status{}, err
	}
	if err := file.Close(); err != nil {
		return Status{}, err
	}
	if err := os.Chmod(enabledPath, 0o600); err != nil {
		return Status{}, err
	}
	s.notify()
	return Status{Provider: provider, Enabled: true}, nil
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

// Recheck wakes running Agent hosts without changing the user's execution policy.
func (s *Store) Recheck() {
	s.notify()
}

func (s *Store) notify() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *Store) providerPath(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, supported := range providers {
		if provider == supported {
			return filepath.Join(s.directory, provider), nil
		}
	}
	return "", errors.New("unsupported external Agent provider")
}

func removeMarker(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
