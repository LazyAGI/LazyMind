//go:build darwin && cgo && integration

package cloudsession

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestDarwinSystemSecureStoreRoundTrip(t *testing.T) {
	store := NewSystemSecureTokenStore("https://keychain-integration.example.invalid")
	ctx := context.Background()
	_ = store.Delete(ctx)
	t.Cleanup(func() { _ = store.Delete(context.Background()) })
	if err := store.Save(ctx, "fixture-refresh-token"); err != nil {
		t.Fatalf("save fixture token: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("load fixture token: %v", err)
	}
	if loaded != "fixture-refresh-token" {
		t.Fatal("Keychain round trip changed the fixture token")
	}
}

func TestDarwinConfiguredIssuerSecureStoreRoundTripWhenEmpty(t *testing.T) {
	issuer := os.Getenv("LAZYMIND_TEST_EMPTY_KEYCHAIN_ISSUER")
	if issuer == "" {
		t.Fatal("configured issuer prerequisite is not set")
	}
	store := NewSystemSecureTokenStore(issuer)
	ctx := context.Background()
	loaded, err := store.Load(ctx)
	if err == nil && loaded != "" {
		t.Skip("configured issuer already has a stored session")
	}
	if err != nil && !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("load configured issuer: %v", err)
	}
	if err := store.Save(ctx, "fixture-refresh-token"); err != nil {
		t.Fatalf("save configured issuer fixture: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background()) })
	if loaded, err := store.Load(ctx); err != nil || loaded != "fixture-refresh-token" {
		t.Fatalf("configured issuer round trip failed: %v", err)
	}
}
