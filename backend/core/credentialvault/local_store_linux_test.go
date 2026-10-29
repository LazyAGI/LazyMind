//go:build linux

package credentialvault

import (
	"context"
	"errors"
	"testing"
)

func TestLinuxKeyStoreRequiresExplicitOptIn(t *testing.T) {
	scope := AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: "u"}
	ctx := context.Background()
	t.Setenv("LAZYMIND_CREDENTIAL_KEY_DIR", "")
	if _, e := NewSystemLocalKeyStore().Load(ctx, scope, LocalRootKey); !errors.Is(e, ErrLocalSecureStoreUnavailable) {
		t.Fatal("default must fail closed", e)
	}
	t.Setenv("LAZYMIND_CREDENTIAL_KEY_DIR", fileStoreTestDir(t))
	if e := NewSystemLocalKeyStore().Save(ctx, scope, LocalRootKey, []byte("key")); e != nil {
		t.Fatal(e)
	}
	got, e := NewSystemLocalKeyStore().Load(ctx, scope, LocalRootKey)
	if e != nil || string(got) != "key" {
		t.Fatal("opt-in did not persist", e)
	}
	t.Setenv("LAZYMIND_CREDENTIAL_KEY_DIR", "relative/keys")
	if e := NewSystemLocalKeyStore().Save(ctx, scope, LocalRootKey, []byte("key")); !errors.Is(e, ErrLocalSecureStoreUnavailable) {
		t.Fatal("relative path allowed", e)
	}
}
