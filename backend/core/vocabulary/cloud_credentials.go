package vocabulary

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"lazymind/core/credentialvault"
	"sync"
)

var cloudKeys struct {
	sync.RWMutex
	manager *credentialvault.LocalKeyManager
}

func SetCloudCredentialKeyManager(manager *credentialvault.LocalKeyManager) func() {
	cloudKeys.Lock()
	old := cloudKeys.manager
	cloudKeys.manager = manager
	cloudKeys.Unlock()
	return func() { cloudKeys.Lock(); cloudKeys.manager = old; cloudKeys.Unlock() }
}
func cloudRootKey(ctx context.Context, owner string) ([]byte, error) {
	cloudKeys.RLock()
	manager := cloudKeys.manager
	cloudKeys.RUnlock()
	if manager == nil {
		return nil, errors.New("vocabulary cloud credentials unavailable")
	}
	key, err := manager.RootKey(ctx, credentialvault.AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: owner})
	if err != nil {
		return nil, errors.New("vocabulary cloud credentials unavailable")
	}
	return key, nil
}
func cloudCredentialAAD(c CloudConnection) credentialvault.LocalCredentialAAD {
	return credentialvault.LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: "vocabulary:" + c.Provider + ":" + c.ID, CredentialRevision: 1}
}
func encryptCloudToken(ctx context.Context, c CloudConnection, token string) (string, error) {
	key, err := cloudRootKey(ctx, c.OwnerID)
	if err != nil {
		return "", err
	}
	defer clear(key)
	value, err := credentialvault.EncryptLocalCredential(rand.Reader, key, cloudCredentialAAD(c), []byte(token))
	if err != nil {
		return "", errors.New("vocabulary cloud credentials unavailable")
	}
	raw, err := json.Marshal(value)
	return string(raw), err
}
func decryptCloudToken(ctx context.Context, c CloudConnection) (string, error) {
	key, err := cloudRootKey(ctx, c.OwnerID)
	if err != nil {
		return "", err
	}
	defer clear(key)
	var value credentialvault.LocalCredentialCiphertext
	if json.Unmarshal([]byte(c.TokenCipher), &value) != nil {
		return "", errors.New("vocabulary cloud credentials unavailable")
	}
	plain, err := credentialvault.DecryptLocalCredential(key, cloudCredentialAAD(c), value)
	if err != nil {
		return "", errors.New("vocabulary cloud credentials unavailable")
	}
	defer clear(plain)
	return string(plain), nil
}
