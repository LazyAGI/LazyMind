//go:build windows

package credentialvault

import (
	"context"
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	vaultWindowsCredentialTypeGeneric         = 1
	vaultWindowsCredentialPersistLocalMachine = 2
)

var (
	vaultWindowsAdvapi32   = windows.NewLazySystemDLL("advapi32.dll")
	vaultWindowsCredRead   = vaultWindowsAdvapi32.NewProc("CredReadW")
	vaultWindowsCredWrite  = vaultWindowsAdvapi32.NewProc("CredWriteW")
	vaultWindowsCredDelete = vaultWindowsAdvapi32.NewProc("CredDeleteW")
	vaultWindowsCredFree   = vaultWindowsAdvapi32.NewProc("CredFree")
)

type vaultWindowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsLocalKeyStore struct{ service string }

func newSystemLocalKeyStore(service string) LocalKeyStore {
	return &windowsLocalKeyStore{service: service}
}

func (store *windowsLocalKeyStore) target(scope AccountScope, kind LocalKeyKind) (*uint16, error) {
	return windows.UTF16PtrFromString(store.service + "/" + localKeyStoreAccount(scope, kind))
}

func (store *windowsLocalKeyStore) Load(ctx context.Context, scope AccountScope, kind LocalKeyKind) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := store.target(scope, kind)
	if err != nil {
		return nil, ErrLocalSecureStoreUnavailable
	}
	var credential *vaultWindowsCredential
	result, _, callErr := vaultWindowsCredRead.Call(
		uintptr(unsafe.Pointer(target)), vaultWindowsCredentialTypeGeneric, 0, uintptr(unsafe.Pointer(&credential)),
	)
	if result == 0 {
		if errors.Is(callErr, syscall.ERROR_NOT_FOUND) {
			return nil, ErrLocalKeyNotFound
		}
		return nil, ErrLocalSecureStoreUnavailable
	}
	defer vaultWindowsCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlob == nil || credential.CredentialBlobSize == 0 || credential.CredentialBlobSize > 4096 {
		return nil, ErrLocalSecureStoreUnavailable
	}
	return append([]byte(nil), unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)...), nil
}

func (store *windowsLocalKeyStore) Save(ctx context.Context, scope AccountScope, kind LocalKeyKind, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(value) == 0 || len(value) > 4096 {
		return ErrLocalSecureStoreUnavailable
	}
	target, err := store.target(scope, kind)
	if err != nil {
		return ErrLocalSecureStoreUnavailable
	}
	username, err := windows.UTF16PtrFromString("LazyMind Desktop Credential Vault")
	if err != nil {
		return ErrLocalSecureStoreUnavailable
	}
	credential := vaultWindowsCredential{
		Type: vaultWindowsCredentialTypeGeneric, TargetName: target,
		CredentialBlobSize: uint32(len(value)), CredentialBlob: &value[0],
		Persist: vaultWindowsCredentialPersistLocalMachine, UserName: username,
	}
	result, _, _ := vaultWindowsCredWrite.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return ErrLocalSecureStoreUnavailable
	}
	return nil
}

func (store *windowsLocalKeyStore) Delete(ctx context.Context, scope AccountScope, kind LocalKeyKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := store.target(scope, kind)
	if err != nil {
		return ErrLocalSecureStoreUnavailable
	}
	result, _, callErr := vaultWindowsCredDelete.Call(uintptr(unsafe.Pointer(target)), vaultWindowsCredentialTypeGeneric, 0)
	if result == 0 && !errors.Is(callErr, syscall.ERROR_NOT_FOUND) {
		return ErrLocalSecureStoreUnavailable
	}
	return nil
}
