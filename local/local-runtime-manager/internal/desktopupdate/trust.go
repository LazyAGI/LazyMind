package desktopupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type ledger struct {
	Version  int             `json:"version"`
	Sequence uint64          `json:"sequence"`
	Digest   string          `json:"payloadSHA256"`
	Envelope json.RawMessage `json:"envelope"`
}

func acceptPolicy(ctx context.Context, root string, c Config, sequence uint64, digest string, envelope []byte, requireExisting bool) error {
	if !filepath.IsAbs(root) {
		return failure("trust_location", nil)
	}
	partition := sha256.Sum256([]byte(c.TrustDomain + "\x00" + c.Audience + "\x00" + c.Channel))
	dir := filepath.Join(root, "trust", "v1", hex.EncodeToString(partition[:]))
	if err := ensureDirectory(dir); err != nil {
		return failure("trust_write", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "ledger.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return failure("trust_lock", err)
	}
	defer lock.Close()
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		locked, err := tryLock(lock)
		if err != nil {
			return failure("trust_lock", err)
		}
		if locked {
			break
		}
		select {
		case <-lockCtx.Done():
			return failure("trust_busy", lockCtx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer unlock(lock)
	if err := ctx.Err(); err != nil {
		return err
	}
	ledgerPath := filepath.Join(dir, "ledger.json")
	markerPath := filepath.Join(dir, "initialized")
	raw, err := os.ReadFile(ledgerPath)
	if err == nil {
		var previous ledger
		if len(raw) > 2*MaxEnvelopeBytes || decodeStrict(raw, &previous) != nil || previous.Version != 1 {
			return failure("trust_corrupt", nil)
		}
		policy, payload, err := decodeSigned(previous.Envelope, c)
		if err != nil && c.UpdaterProtocol == 2 {
			legacy := c
			legacy.UpdaterProtocol = 1
			policy, payload, err = decodeSigned(previous.Envelope, legacy)
		}
		hash := sha256.Sum256(payload)
		if err != nil || previous.Sequence != policy.Sequence || previous.Digest != hex.EncodeToString(hash[:]) {
			return failure("trust_corrupt", err)
		}
		if sequence < previous.Sequence {
			return failure("policy_replay", nil)
		}
		if sequence == previous.Sequence {
			if digest != previous.Digest {
				return failure("policy_equivocation", nil)
			}
			return nil
		}
	} else if errors.Is(err, os.ErrNotExist) {
		_, markerErr := os.Stat(markerPath)
		if requireExisting || markerErr == nil {
			return failure("trust_missing", nil)
		}
		if !errors.Is(markerErr, os.ErrNotExist) {
			return failure("trust_read", markerErr)
		}
	} else {
		return failure("trust_read", err)
	}
	// A durable marker prevents a missing ledger from looking like a new install.
	// If initial persistence fails, keep the marker and require explicit repair.
	if _, err := os.Stat(markerPath); errors.Is(err, os.ErrNotExist) {
		if err := atomicWrite(markerPath, []byte("trust/v1\n")); err != nil {
			return failure("trust_write", err)
		}
	} else if err != nil {
		return failure("trust_read", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(ledger{Version: 1, Sequence: sequence, Digest: digest, Envelope: append([]byte{}, envelope...)})
	if err != nil {
		return failure("trust_write", err)
	}
	if err := atomicWrite(ledgerPath, data); err != nil {
		return failure("trust_write", err)
	}
	return nil
}

func ensureDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return errors.New("trust path is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := ensureDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDirectory(parent)
}

func atomicWrite(destination string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(destination), ".trust-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return replaceFile(name, destination)
}

func ReadAcceptedEnvelope(c Config, root string) ([]byte, error) {
	partition := sha256.Sum256([]byte(c.TrustDomain + "\x00" + c.Audience + "\x00" + c.Channel))
	var stored ledger
	if err := readMetadata(filepath.Join(root, "trust", "v1", hex.EncodeToString(partition[:]), "ledger.json"), &stored); err != nil {
		return nil, failure("trust_missing", err)
	}
	if stored.Version != 1 {
		return nil, failure("trust_corrupt", nil)
	}
	return stored.Envelope, nil
}
