//go:build darwin && cgo

package auth

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef lte_query(const char *service, const char *account) {
	CFStringRef serviceRef = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef accountRef = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	if (serviceRef == NULL || accountRef == NULL) {
		if (serviceRef != NULL) CFRelease(serviceRef);
		if (accountRef != NULL) CFRelease(accountRef);
		return NULL;
	}
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (query == NULL) {
		CFRelease(serviceRef);
		CFRelease(accountRef);
		return NULL;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, serviceRef);
	CFDictionarySetValue(query, kSecAttrAccount, accountRef);
	CFRelease(serviceRef);
	CFRelease(accountRef);
	return query;
}

static OSStatus lte_keychain_copy(const char *service, const char *account, void **outData, size_t *outLength) {
	CFMutableDictionaryRef query = lte_query(service, account);
	if (query == NULL) return errSecParam;
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status != errSecSuccess) return status;
	if (result == NULL) return errSecItemNotFound;
	CFDataRef data = (CFDataRef)result;
	CFIndex length = CFDataGetLength(data);
	if (length <= 0) {
		CFRelease(result);
		return errSecItemNotFound;
	}
	void *buffer = malloc((size_t)length);
	if (buffer == NULL) {
		CFRelease(result);
		return errSecAllocate;
	}
	CFDataGetBytes(data, CFRangeMake(0, length), (UInt8 *)buffer);
	CFRelease(result);
	*outData = buffer;
	*outLength = (size_t)length;
	return errSecSuccess;
}

static OSStatus lte_keychain_store(const char *service, const char *account, const void *data, size_t length) {
	CFMutableDictionaryRef query = lte_query(service, account);
	if (query == NULL) return errSecParam;
	CFDataRef payload = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)data, (CFIndex)length);
	if (payload == NULL) {
		CFRelease(query);
		return errSecAllocate;
	}
	CFMutableDictionaryRef attributes = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	if (attributes == NULL) {
		CFRelease(payload);
		CFRelease(query);
		return errSecAllocate;
	}
	CFDictionarySetValue(attributes, kSecValueData, payload);
	CFDictionarySetValue(attributes, kSecAttrAccessible, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);
	OSStatus status = SecItemUpdate(query, attributes);
	if (status == errSecItemNotFound) {
		CFDictionarySetValue(query, kSecValueData, payload);
		CFDictionarySetValue(query, kSecAttrAccessible, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);
		status = SecItemAdd(query, NULL);
	}
	CFRelease(attributes);
	CFRelease(payload);
	CFRelease(query);
	return status;
}

static OSStatus lte_keychain_delete(const char *service, const char *account) {
	CFMutableDictionaryRef query = lte_query(service, account);
	if (query == NULL) return errSecParam;
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"
)

// keychainStore stores each record as a generic-password item under the
// shared service, with accessibility AfterFirstUnlockThisDeviceOnly.  The
// credential and the device transaction are always separate accounts, and
// every write replaces the whole record so no partial refresh is possible.
type keychainStore struct{}

// NewStore returns the macOS Keychain-backed credential store.  There is no
// plaintext fallback: an unavailable Keychain surfaces as an error.
func NewStore() (Store, error) {
	return &keychainStore{}, nil
}

func (s *keychainStore) LoadCredential() (*Credential, error) {
	raw, err := loadRecord(CredentialAccount)
	if err != nil {
		return nil, err
	}
	var credential Credential
	if err := json.Unmarshal(raw, &credential); err != nil || credential.AccessToken == "" {
		return nil, errors.New("stored credential is unreadable")
	}
	return &credential, nil
}

func (s *keychainStore) SaveCredential(credential Credential) error {
	if credential.AccessToken == "" {
		return errors.New("refusing to store an empty credential")
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return errors.New("encode credential")
	}
	return storeRecord(CredentialAccount, raw)
}

func (s *keychainStore) DeleteCredential() error {
	return deleteRecord(CredentialAccount)
}

func (s *keychainStore) LoadTransaction() (*DeviceTransaction, error) {
	raw, err := loadRecord(TransactionAccount)
	if err != nil {
		return nil, err
	}
	var transaction DeviceTransaction
	if err := json.Unmarshal(raw, &transaction); err != nil || transaction.DeviceCode == "" {
		return nil, errors.New("stored device transaction is unreadable")
	}
	return &transaction, nil
}

func (s *keychainStore) SaveTransaction(transaction DeviceTransaction) error {
	if transaction.DeviceCode == "" {
		return errors.New("refusing to store an empty device transaction")
	}
	raw, err := json.Marshal(transaction)
	if err != nil {
		return errors.New("encode device transaction")
	}
	return storeRecord(TransactionAccount, raw)
}

func (s *keychainStore) DeleteTransaction() error {
	return deleteRecord(TransactionAccount)
}

func loadRecord(account string) ([]byte, error) {
	service := C.CString(KeychainService)
	defer C.free(unsafe.Pointer(service))
	accountRef := C.CString(account)
	defer C.free(unsafe.Pointer(accountRef))

	var data unsafe.Pointer
	var length C.size_t
	status := C.lte_keychain_copy(service, accountRef, &data, &length)
	if status == C.errSecItemNotFound {
		return nil, notFoundError(account)
	}
	if status != C.errSecSuccess {
		return nil, fmt.Errorf("keychain read failed (status %d)", int(status))
	}
	if data == nil {
		return nil, notFoundError(account)
	}
	defer C.free(data)
	return C.GoBytes(data, C.int(length)), nil
}

func storeRecord(account string, payload []byte) error {
	service := C.CString(KeychainService)
	defer C.free(unsafe.Pointer(service))
	accountRef := C.CString(account)
	defer C.free(unsafe.Pointer(accountRef))

	var data unsafe.Pointer
	if len(payload) > 0 {
		data = C.CBytes(payload)
		defer C.free(data)
	}
	status := C.lte_keychain_store(service, accountRef, data, C.size_t(len(payload)))
	if status != C.errSecSuccess {
		return fmt.Errorf("keychain write failed (status %d)", int(status))
	}
	return nil
}

func deleteRecord(account string) error {
	service := C.CString(KeychainService)
	defer C.free(unsafe.Pointer(service))
	accountRef := C.CString(account)
	defer C.free(unsafe.Pointer(accountRef))

	status := C.lte_keychain_delete(service, accountRef)
	if status == C.errSecSuccess || status == C.errSecItemNotFound {
		return nil
	}
	return fmt.Errorf("keychain delete failed (status %d)", int(status))
}

func notFoundError(account string) error {
	if account == TransactionAccount {
		return ErrNoTransaction
	}
	return ErrNoCredential
}
