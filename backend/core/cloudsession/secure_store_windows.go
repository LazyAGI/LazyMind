//go:build windows

package cloudsession

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsCredentialTypeGeneric         = 1
	windowsCredentialPersistLocalMachine = 2
	windowsCredentialMaxBlobSize         = 5 * 512
)

var (
	windowsAdvapi32   = windows.NewLazySystemDLL("advapi32.dll")
	windowsCredRead   = windowsAdvapi32.NewProc("CredReadW")
	windowsCredWrite  = windowsAdvapi32.NewProc("CredWriteW")
	windowsCredDelete = windowsAdvapi32.NewProc("CredDeleteW")
	windowsCredFree   = windowsAdvapi32.NewProc("CredFree")
)

type windowsCredential struct {
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

type windowsSecureTokenStore struct {
	target string
}

func newSystemSecureTokenStore(service, account string) SecureTokenStore {
	return &windowsSecureTokenStore{target: service + "/" + account}
}

func (s *windowsSecureTokenStore) Load(ctx context.Context) (RefreshToken, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target, err := windows.UTF16PtrFromString(s.target)
	if err != nil {
		return "", err
	}
	var credential *windowsCredential
	result, _, callErr := windowsCredRead.Call(
		uintptr(unsafe.Pointer(target)), windowsCredentialTypeGeneric, 0, uintptr(unsafe.Pointer(&credential)),
	)
	if result == 0 {
		if errors.Is(callErr, syscall.ERROR_NOT_FOUND) {
			return "", ErrNoRefreshToken
		}
		return "", fmt.Errorf("load Cloud refresh token from Credential Manager: %w", callErr)
	}
	defer windowsCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlob == nil || credential.CredentialBlobSize == 0 {
		return "", ErrNoRefreshToken
	}
	body := unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)
	return RefreshToken(append([]byte(nil), body...)), nil
}

func (s *windowsSecureTokenStore) Save(ctx context.Context, token RefreshToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body := []byte(token)
	if len(body) == 0 || len(body) > windowsCredentialMaxBlobSize {
		return errors.New("Cloud refresh token size is invalid")
	}
	target, err := windows.UTF16PtrFromString(s.target)
	if err != nil {
		return err
	}
	username, err := windows.UTF16PtrFromString("LazyMind Desktop")
	if err != nil {
		return err
	}
	credential := windowsCredential{
		Type: windowsCredentialTypeGeneric, TargetName: target,
		CredentialBlobSize: uint32(len(body)), CredentialBlob: &body[0],
		Persist: windowsCredentialPersistLocalMachine, UserName: username,
	}
	result, _, callErr := windowsCredWrite.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return fmt.Errorf("save Cloud refresh token to Credential Manager: %w", callErr)
	}
	return nil
}

func (s *windowsSecureTokenStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(s.target)
	if err != nil {
		return err
	}
	result, _, callErr := windowsCredDelete.Call(uintptr(unsafe.Pointer(target)), windowsCredentialTypeGeneric, 0)
	if result == 0 && !errors.Is(callErr, syscall.ERROR_NOT_FOUND) {
		return fmt.Errorf("delete Cloud refresh token from Credential Manager: %w", callErr)
	}
	return nil
}
