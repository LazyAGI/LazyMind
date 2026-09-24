//go:build darwin && cgo

package cloudsession

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef lm_cfstring(const char *value) {
  return CFStringCreateWithBytes(
      kCFAllocatorDefault, (const UInt8 *)value, (CFIndex)strlen(value),
      kCFStringEncodingUTF8, false);
}

static OSStatus lm_open_keychain(const char *path, SecKeychainRef *keychain) {
  if (path == NULL || path[0] == '\0') return errSecParam;
  return SecKeychainOpen(path, keychain);
}

static CFDictionaryRef lm_keychain_query(
    const char *service, const char *account, bool returnData, SecKeychainRef keychain) {
  CFStringRef serviceValue = lm_cfstring(service);
  CFStringRef accountValue = lm_cfstring(account);
  if (serviceValue == NULL || accountValue == NULL) {
    if (serviceValue != NULL) CFRelease(serviceValue);
    if (accountValue != NULL) CFRelease(accountValue);
    return NULL;
  }
  const void *keychainValues[] = {keychain};
  CFArrayRef searchList = CFArrayCreate(
      kCFAllocatorDefault, keychainValues, 1, &kCFTypeArrayCallBacks);
  if (searchList == NULL) {
    CFRelease(serviceValue);
    CFRelease(accountValue);
    return NULL;
  }
  const void *keys[7] = {
      kSecClass, kSecAttrService, kSecAttrAccount, kSecMatchSearchList,
      kSecUseAuthenticationUI, kSecReturnData, kSecMatchLimit};
  const void *values[7] = {
      kSecClassGenericPassword, serviceValue, accountValue, searchList,
      kSecUseAuthenticationUIFail, kCFBooleanTrue, kSecMatchLimitOne};
  CFIndex count = returnData ? 7 : 5;
  CFDictionaryRef query = CFDictionaryCreate(
      kCFAllocatorDefault, keys, values, count,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  CFRelease(serviceValue);
  CFRelease(accountValue);
  CFRelease(searchList);
  return query;
}

static OSStatus lm_keychain_load(
    const char *service, const char *account, const char *path, UInt32 *length, void **data) {
  SecKeychainRef keychain = NULL;
  OSStatus status = lm_open_keychain(path, &keychain);
  if (status != errSecSuccess) return status;
  CFDictionaryRef query = lm_keychain_query(service, account, true, keychain);
  if (query == NULL) {
    CFRelease(keychain);
    return errSecAllocate;
  }
  CFTypeRef result = NULL;
  status = SecItemCopyMatching(query, &result);
  CFRelease(query);
  CFRelease(keychain);
  if (status != errSecSuccess) return status;
  if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
    if (result != NULL) CFRelease(result);
    return errSecDecode;
  }
  CFDataRef value = (CFDataRef)result;
  CFIndex valueLength = CFDataGetLength(value);
  if (valueLength <= 0 || valueLength > UINT32_MAX) {
    CFRelease(value);
    return errSecDecode;
  }
  void *copy = malloc((size_t)valueLength);
  if (copy == NULL) {
    CFRelease(value);
    return errSecAllocate;
  }
  memcpy(copy, CFDataGetBytePtr(value), (size_t)valueLength);
  CFRelease(value);
  *length = (UInt32)valueLength;
  *data = copy;
  return errSecSuccess;
}

static OSStatus lm_keychain_save(
    const char *service, const char *account, const char *path, UInt32 length, const void *data) {
  SecKeychainRef keychain = NULL;
  OSStatus status = lm_open_keychain(path, &keychain);
  if (status != errSecSuccess) return status;
  CFDataRef token = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)data, (CFIndex)length);
  if (token == NULL) {
    CFRelease(keychain);
    return errSecAllocate;
  }
  CFDictionaryRef query = lm_keychain_query(service, account, false, keychain);
  if (query == NULL) {
    CFRelease(keychain);
    CFRelease(token);
    return errSecAllocate;
  }
  const void *updateKeys[] = {kSecValueData};
  const void *updateValues[] = {token};
  CFDictionaryRef update = CFDictionaryCreate(
      kCFAllocatorDefault, updateKeys, updateValues, 1,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  if (update == NULL) {
    CFRelease(query);
    CFRelease(token);
    CFRelease(keychain);
    return errSecAllocate;
  }
  status = SecItemUpdate(query, update);
  CFRelease(update);
  CFRelease(query);
  if (status == errSecItemNotFound) {
    CFStringRef serviceValue = lm_cfstring(service);
    CFStringRef accountValue = lm_cfstring(account);
    if (serviceValue == NULL || accountValue == NULL) {
      if (serviceValue != NULL) CFRelease(serviceValue);
      if (accountValue != NULL) CFRelease(accountValue);
      CFRelease(token);
      CFRelease(keychain);
      return errSecAllocate;
    }
    const void *keys[] = {
        kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData,
        kSecUseKeychain, kSecUseAuthenticationUI};
    const void *values[] = {
        kSecClassGenericPassword, serviceValue, accountValue, token,
        keychain, kSecUseAuthenticationUIFail};
    CFDictionaryRef item = CFDictionaryCreate(
        kCFAllocatorDefault, keys, values, 6,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (item == NULL) {
      CFRelease(serviceValue);
      CFRelease(accountValue);
      CFRelease(token);
      CFRelease(keychain);
      return errSecAllocate;
    }
    status = SecItemAdd(item, NULL);
    CFRelease(item);
    CFRelease(serviceValue);
    CFRelease(accountValue);
  }
  CFRelease(token);
  CFRelease(keychain);
  return status;
}

static OSStatus lm_keychain_delete(const char *service, const char *account, const char *path) {
  SecKeychainRef keychain = NULL;
  OSStatus status = lm_open_keychain(path, &keychain);
  if (status != errSecSuccess) return status;
  CFDictionaryRef query = lm_keychain_query(service, account, false, keychain);
  if (query == NULL) {
    CFRelease(keychain);
    return errSecAllocate;
  }
  status = SecItemDelete(query);
  CFRelease(query);
  CFRelease(keychain);
  return status;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

type darwinSecureTokenStore struct {
	service      string
	account      string
	keychainPath string
}

func newSystemSecureTokenStore(service, account string) SecureTokenStore {
	return &darwinSecureTokenStore{service: service, account: account, keychainPath: darwinLoginKeychainPath()}
}

func darwinLoginKeychainPath() string {
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOST_HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Keychains", "login.keychain-db")
}

func (s *darwinSecureTokenStore) Load(ctx context.Context) (RefreshToken, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	service, account := C.CString(s.service), C.CString(s.account)
	path := C.CString(s.keychainPath)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	defer C.free(unsafe.Pointer(path))
	var length C.UInt32
	var data unsafe.Pointer
	status := C.lm_keychain_load(service, account, path, &length, &data)
	if status == C.errSecItemNotFound {
		return "", ErrNoRefreshToken
	}
	if status != C.errSecSuccess {
		return "", fmt.Errorf("load Cloud refresh token from Keychain: status=%d", int32(status))
	}
	defer C.free(data)
	if length == 0 || data == nil {
		return "", ErrNoRefreshToken
	}
	return RefreshToken(C.GoBytes(data, C.int(length))), nil
}

func (s *darwinSecureTokenStore) Save(ctx context.Context, token RefreshToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body := []byte(token)
	if len(body) == 0 {
		return errors.New("Cloud refresh token is empty")
	}
	service, account := C.CString(s.service), C.CString(s.account)
	path := C.CString(s.keychainPath)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	defer C.free(unsafe.Pointer(path))
	status := C.lm_keychain_save(service, account, path, C.UInt32(len(body)), unsafe.Pointer(&body[0]))
	if status != C.errSecSuccess {
		return fmt.Errorf("save Cloud refresh token to Keychain: status=%d", int32(status))
	}
	return nil
}

func (s *darwinSecureTokenStore) Delete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service, account := C.CString(s.service), C.CString(s.account)
	path := C.CString(s.keychainPath)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	defer C.free(unsafe.Pointer(path))
	status := C.lm_keychain_delete(service, account, path)
	if status == C.errSecItemNotFound {
		return nil
	}
	if status != C.errSecSuccess {
		return fmt.Errorf("delete Cloud refresh token from Keychain: status=%d", int32(status))
	}
	return nil
}
