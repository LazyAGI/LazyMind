//go:build darwin

package credentialvault

import "golang.org/x/sys/unix"

// Darwin supports the same atomic no-replace contract for portable store tests;
// NewSystemLocalKeyStore continues to use Keychain on this platform.
func publishFileKey(dir int, temp, name string) error {
	return unix.RenameatxNp(dir, temp, dir, name, unix.RENAME_EXCL)
}
