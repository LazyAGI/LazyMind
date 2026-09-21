//go:build !darwin && !windows

package cloudsession

import (
	"context"
	"errors"
)

type unavailableSecureTokenStore struct{}

func newSystemSecureTokenStore(string, string) SecureTokenStore { return unavailableSecureTokenStore{} }
func (unavailableSecureTokenStore) Load(context.Context) (RefreshToken, error) {
	return "", errors.New("system credential storage is unsupported on this platform")
}
func (unavailableSecureTokenStore) Save(context.Context, RefreshToken) error {
	return errors.New("system credential storage is unsupported on this platform")
}
func (unavailableSecureTokenStore) Delete(context.Context) error { return nil }
