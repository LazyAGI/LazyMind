package credentialvault

import (
	"crypto/sha256"
	"encoding/hex"
)

const localKeyStoreService = "com.lazymind.desktop.credential-vault"

func NewSystemLocalKeyStore() LocalKeyStore {
	return newSystemLocalKeyStore(localKeyStoreService)
}

func localKeyStoreAccount(scope AccountScope, kind LocalKeyKind) string {
	digest := sha256.Sum256([]byte(scope.CloudIssuer + "\x00" + scope.CloudAccountID + "\x00" + string(kind)))
	return string(kind) + ":" + hex.EncodeToString(digest[:16])
}
