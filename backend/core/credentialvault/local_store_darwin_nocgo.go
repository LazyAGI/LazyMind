//go:build darwin && !cgo

package credentialvault

import "context"

type unavailableLocalKeyStore struct{}

func newSystemLocalKeyStore(string) LocalKeyStore { return unavailableLocalKeyStore{} }

func (unavailableLocalKeyStore) Load(context.Context, AccountScope, LocalKeyKind) ([]byte, error) {
	return nil, ErrLocalSecureStoreUnavailable
}

func (unavailableLocalKeyStore) Save(context.Context, AccountScope, LocalKeyKind, []byte) error {
	return ErrLocalSecureStoreUnavailable
}

func (unavailableLocalKeyStore) Delete(context.Context, AccountScope, LocalKeyKind) error {
	return ErrLocalSecureStoreUnavailable
}
