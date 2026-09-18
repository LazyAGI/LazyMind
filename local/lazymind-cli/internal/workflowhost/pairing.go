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
	"time"

	"lazymind/agentconnector/internal/localfile"
)

type Pairing struct {
	ConnectorID  string    `json:"connector_id"`
	Token        string    `json:"token"`
	AccountScope string    `json:"account_scope"`
	Profile      string    `json:"profile"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

var connectorPattern = regexp.MustCompile(`^dsh-[a-f0-9]{32}$`)

func pairingPath(home, id string) (string, error) {
	if !connectorPattern.MatchString(id) {
		return "", errors.New("invalid paired connector identifier")
	}
	return filepath.Join(home, "workflow-hosts", id+".json"), nil
}

func Ensure(home, profile, accountScope string) (Pairing, error) {
	if accountScope == "" {
		return Pairing{}, errors.New("log in before pairing a workflow host")
	}
	absolute, err := filepath.Abs(profile)
	if err != nil {
		return Pairing{}, err
	}
	sum := sha256.Sum256([]byte(accountScope + "\x00" + absolute))
	id := "dsh-" + hex.EncodeToString(sum[:16])
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
	pair := Pairing{ConnectorID: id, AccountScope: accountScope, Profile: absolute, Enabled: true, CreatedAt: time.Now().UTC()}
	body, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(body, &pair); err != nil {
			return Pairing{}, errors.New("invalid workflow host pairing; repair this connection explicitly")
		}
		if pair.AccountScope != accountScope || pair.Profile != absolute || len(pair.Token) != 64 {
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
	if json.Unmarshal(body, &pair) != nil || pair.ConnectorID != id || !pair.Enabled || pair.AccountScope != accountScope || token == "" ||
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
	return json.Unmarshal(body, &pair) == nil && pair.ConnectorID == id && pair.Enabled && pair.AccountScope == accountScope && len(pair.Token) == 64
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
