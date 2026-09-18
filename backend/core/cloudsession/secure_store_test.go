package cloudsession

import (
	"context"
	"errors"
	"testing"
)

func TestSystemSecureTokenAccountIsStableAndIssuerScoped(t *testing.T) {
	first := systemSecureTokenAccount(" https://cloud.example ")
	if first != systemSecureTokenAccount("https://cloud.example") {
		t.Fatal("equivalent Cloud origins produced different credential accounts")
	}
	if first == systemSecureTokenAccount("https://other.example") {
		t.Fatal("different Cloud origins shared a credential account")
	}
	if first == "" || first == "https://cloud.example" {
		t.Fatalf("unsafe credential account %q", first)
	}
}

func TestMemorySecureTokenStoreNeverPersistsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	first := NewMemorySecureTokenStore()
	if err := first.Save(ctx, "fixture-refresh"); err != nil {
		t.Fatal(err)
	}
	if token, err := first.Load(ctx); err != nil || token != "fixture-refresh" {
		t.Fatalf("load token=%q err=%v", token, err)
	}
	second := NewMemorySecureTokenStore()
	if _, err := second.Load(ctx); !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("new process-like store restored memory token: %v", err)
	}
	if err := first.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Load(ctx); !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("deleted memory token remained available: %v", err)
	}
}
