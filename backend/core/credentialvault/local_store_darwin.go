//go:build darwin && cgo

package credentialvault

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef lm_vault_string(const char *value) {
  return CFStringCreateWithBytes(kCFAllocatorDefault, (const UInt8 *)value,
      (CFIndex)strlen(value), kCFStringEncodingUTF8, false);
}

static CFDictionaryRef lm_vault_query(const char *service, const char *account, bool returnData) {
  CFStringRef serviceValue = lm_vault_string(service);
  CFStringRef accountValue = lm_vault_string(account);
  if (serviceValue == NULL || accountValue == NULL) {
    if (serviceValue != NULL) CFRelease(serviceValue);
    if (accountValue != NULL) CFRelease(accountValue);
    return NULL;
  }
  const void *keys[] = {kSecClass, kSecAttrService, kSecAttrAccount,
      kSecUseAuthenticationUI, kSecReturnData, kSecMatchLimit};
  const void *values[] = {kSecClassGenericPassword, serviceValue, accountValue,
      kSecUseAuthenticationUIFail, kCFBooleanTrue, kSecMatchLimitOne};
  CFIndex count = returnData ? 6 : 4;
  CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, keys, values, count,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  CFRelease(serviceValue);
  CFRelease(accountValue);
  return query;
}

static OSStatus lm_vault_load(const char *service, const char *account, UInt32 *length, void **data) {
  CFDictionaryRef query = lm_vault_query(service, account, true);
  if (query == NULL) return errSecAllocate;
  CFTypeRef result = NULL;
  OSStatus status = SecItemCopyMatching(query, &result);
  CFRelease(query);
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

static OSStatus lm_vault_save(const char *service, const char *account, UInt32 length, const void *data) {
  CFDataRef value = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)data, (CFIndex)length);
  if (value == NULL) return errSecAllocate;
  CFDictionaryRef query = lm_vault_query(service, account, false);
  if (query == NULL) {
    CFRelease(value);
    return errSecAllocate;
  }
  const void *updateKeys[] = {kSecValueData};
  const void *updateValues[] = {value};
  CFDictionaryRef update = CFDictionaryCreate(kCFAllocatorDefault, updateKeys, updateValues, 1,
      &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
  OSStatus status = update == NULL ? errSecAllocate : SecItemUpdate(query, update);
  if (update != NULL) CFRelease(update);
  CFRelease(query);
  if (status == errSecItemNotFound) {
    CFStringRef serviceValue = lm_vault_string(service);
    CFStringRef accountValue = lm_vault_string(account);
    if (serviceValue == NULL || accountValue == NULL) {
      if (serviceValue != NULL) CFRelease(serviceValue);
      if (accountValue != NULL) CFRelease(accountValue);
      CFRelease(value);
      return errSecAllocate;
    }
    const void *keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData, kSecUseAuthenticationUI};
    const void *values[] = {kSecClassGenericPassword, serviceValue, accountValue, value, kSecUseAuthenticationUIFail};
    CFDictionaryRef item = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 5,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    status = item == NULL ? errSecAllocate : SecItemAdd(item, NULL);
    if (item != NULL) CFRelease(item);
    CFRelease(serviceValue);
    CFRelease(accountValue);
  }
  CFRelease(value);
  return status;
}

static OSStatus lm_vault_delete(const char *service, const char *account) {
  CFDictionaryRef query = lm_vault_query(service, account, false);
  if (query == NULL) return errSecAllocate;
  OSStatus status = SecItemDelete(query);
  CFRelease(query);
  return status;
}
*/
import "C"

import (
	"context"
	"unsafe"
)

type darwinLocalKeyStore struct{ service string }

func newSystemLocalKeyStore(service string) LocalKeyStore {
	return &darwinLocalKeyStore{service: service}
}

func (store *darwinLocalKeyStore) Load(ctx context.Context, scope AccountScope, kind LocalKeyKind) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service := C.CString(store.service)
	account := C.CString(localKeyStoreAccount(scope, kind))
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	var length C.UInt32
	var data unsafe.Pointer
	status := C.lm_vault_load(service, account, &length, &data)
	if status == C.errSecItemNotFound {
		return nil, ErrLocalKeyNotFound
	}
	if status != C.errSecSuccess || data == nil || length == 0 {
		return nil, ErrLocalSecureStoreUnavailable
	}
	defer C.free(data)
	return C.GoBytes(data, C.int(length)), nil
}

func (store *darwinLocalKeyStore) Save(ctx context.Context, scope AccountScope, kind LocalKeyKind, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(value) == 0 || len(value) > 4096 {
		return ErrLocalSecureStoreUnavailable
	}
	service := C.CString(store.service)
	account := C.CString(localKeyStoreAccount(scope, kind))
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	if C.lm_vault_save(service, account, C.UInt32(len(value)), unsafe.Pointer(&value[0])) != C.errSecSuccess {
		return ErrLocalSecureStoreUnavailable
	}
	return nil
}

func (store *darwinLocalKeyStore) Delete(ctx context.Context, scope AccountScope, kind LocalKeyKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service := C.CString(store.service)
	account := C.CString(localKeyStoreAccount(scope, kind))
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	status := C.lm_vault_delete(service, account)
	if status != C.errSecSuccess && status != C.errSecItemNotFound {
		return ErrLocalSecureStoreUnavailable
	}
	return nil
}
