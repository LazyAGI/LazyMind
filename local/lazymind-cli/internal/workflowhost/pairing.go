// Package workflowhost owns local plugin pairing, not Workflow business state.
package workflowhost

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

const DSHProvider = "deepseek-harness"

type Pairing struct {
	Provider     string    `json:"provider"`
	ConnectorID  string    `json:"connector_id"`
	Token        string    `json:"token"`
	AccountScope string    `json:"account_scope"`
	Profile      string    `json:"profile"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

var connectorPattern = regexp.MustCompile(`^(dsh|host)-[a-f0-9]{32}$`)
var providerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func pairingPath(home, id string) (string, error) {
	if !connectorPattern.MatchString(id) {
		return "", errors.New("invalid paired connector identifier")
	}
	return filepath.Join(home, "workflow-hosts", id+".json"), nil
}

// Ensure preserves the existing DSH connector identity and credential.
func Ensure(home, profile, accountScope string) (Pairing, error) {
	return EnsureForProvider(home, DSHProvider, profile, accountScope)
}

func EnsureForProvider(home, provider, profile, accountScope string) (Pairing, error) {
	if !providerPattern.MatchString(provider) {
		return Pairing{}, errors.New("invalid workflow host provider")
	}
	if accountScope == "" {
		return Pairing{}, errors.New("log in before pairing a workflow host")
	}
	absolute, err := filepath.Abs(profile)
	if err != nil {
		return Pairing{}, err
	}
	identity := accountScope + "\x00" + absolute
	prefix := "dsh-"
	if provider != DSHProvider {
		identity = accountScope + "\x00" + provider + "\x00" + absolute
		prefix = "host-"
	}
	sum := sha256.Sum256([]byte(identity))
	id := prefix + hex.EncodeToString(sum[:16])
	path, err := pairingPath(home, id)
	if err != nil {
		return Pairing{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Pairing{}, err
	}
	unlock, err := localfile.Lock(path + ".lock")
	if err != nil {
		return Pairing{}, err
	}
	defer unlock()
	pair := Pairing{Provider: provider, ConnectorID: id, AccountScope: accountScope, Profile: absolute, Enabled: true, CreatedAt: time.Now().UTC()}
	body, err := os.ReadFile(path)
	if err == nil {
		pair = Pairing{}
		if err := json.Unmarshal(body, &pair); err != nil {
			return Pairing{}, errors.New("invalid workflow host pairing; repair this connection explicitly")
		}
		if !normalizeProvider(&pair) || pair.Provider != provider || pair.ConnectorID != id || pair.AccountScope != accountScope || pair.Profile != absolute || len(pair.Token) != 64 {
			return Pairing{}, errors.New("workflow host pairing does not match this account and profile")
		}
		pair.Enabled = true
	} else if errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return Pairing{}, err
		}
		pair.Token = hex.EncodeToString(secret)
	} else {
		return Pairing{}, err
	}
	return pair, save(path, pair)
}

func Verify(home, id, token, accountScope string) (Pairing, error) {
	path, err := pairingPath(home, id)
	if err != nil {
		return Pairing{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Pairing{}, errors.New("workflow host is not paired")
	}
	var pair Pairing
	if json.Unmarshal(body, &pair) != nil || !normalizeProvider(&pair) || pair.ConnectorID != id || !pair.Enabled || pair.AccountScope != accountScope || token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(pair.Token)) != 1 {
		return Pairing{}, errors.New("workflow host pairing is invalid; reconnect for the current account")
	}
	return pair, nil
}

// Configured is a read-only installation check; it never re-enables a disconnected peer.
func Configured(home, id, accountScope string) bool {
	path, err := pairingPath(home, id)
	if err != nil {
		return false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pair Pairing
	return json.Unmarshal(body, &pair) == nil && normalizeProvider(&pair) && pair.ConnectorID == id && pair.Enabled && pair.AccountScope == accountScope && len(pair.Token) == 64
}

// Only pre-existing dsh identities may omit provider. Never infer a new host's
// provider from an untrusted bind request or silently interpret it as DSH.
func normalizeProvider(pair *Pairing) bool {
	if strings.HasPrefix(pair.ConnectorID, "dsh-") {
		if pair.Provider == "" {
			pair.Provider = DSHProvider
		}
		return pair.Provider == DSHProvider
	}
	return strings.HasPrefix(pair.ConnectorID, "host-") && providerPattern.MatchString(pair.Provider) && pair.Provider != DSHProvider
}

func Disable(home, id string) error {
	path, err := pairingPath(home, id)
	if err != nil {
		return err
	}
	unlock, err := localfile.Lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var pair Pairing
	if err := json.Unmarshal(body, &pair); err != nil {
		return err
	}
	pair.Enabled = false
	return save(path, pair)
}

func save(path string, pair Pairing) error {
	body, err := json.Marshal(pair)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pairing-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return localfile.Replace(f.Name(), path)
}
