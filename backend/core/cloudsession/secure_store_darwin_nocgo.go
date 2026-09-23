//go:build darwin && !cgo

package cloudsession

import (
	"context"
	"errors"
)

type unavailableSecureTokenStore struct{}

func newSystemSecureTokenStore(string, string) SecureTokenStore { return unavailableSecureTokenStore{} }
func (unavailableSecureTokenStore) Load(context.Context) (RefreshToken, error) {
	return "", errors.New("macOS Keychain requires cgo")
}
func (unavailableSecureTokenStore) Save(context.Context, RefreshToken) error {
	return errors.New("macOS Keychain requires cgo")
}
func (unavailableSecureTokenStore) Delete(context.Context) error { return nil }
