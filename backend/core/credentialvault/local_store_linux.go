//go:build linux

package credentialvault

import (
	"golang.org/x/sys/unix"
	"os"
	"strings"
)

// LAZYMIND_CREDENTIAL_KEY_DIR explicitly opts a server into filesystem custody
// of encryption keys. Without it Linux continues to fail closed.
func newSystemLocalKeyStore(string) LocalKeyStore {
	if directory := strings.TrimSpace(os.Getenv("LAZYMIND_CREDENTIAL_KEY_DIR")); directory != "" {
		return newFileLocalKeyStore(directory)
	}
	return unavailableLocalKeyStore{}
}

func publishFileKey(dir int, temp, name string) error {
	return unix.Renameat2(dir, temp, dir, name, unix.RENAME_NOREPLACE)
}
