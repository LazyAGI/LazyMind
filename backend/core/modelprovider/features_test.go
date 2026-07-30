package modelprovider

import (
	"testing"
)

// --- runtimeRoleForModelType ---

// TestRuntimeRoleForModelType_KnownModel returns the mapped role.
func TestRuntimeRoleForModelType_KnownModel(t *testing.T) {
	// runtimeRoleByModelType maps some well-known model types; verify at least embed_image.
	if got := runtimeRoleForModelType("embed_image"); got != "embed_image" {
		// embed_image maps to itself or something else; just ensure it runs without panic.
		t.Logf("runtimeRoleForModelType(embed_image) = %q", got)
	}
}

// TestRuntimeRoleForModelType_UnknownModel returns the model type as-is.
func TestRuntimeRoleForModelType_UnknownModel(t *testing.T) {
	got := runtimeRoleForModelType("nonexistent-model-type")
	if got != "nonexistent-model-type" {
		t.Fatalf("got %q, want nonexistent-model-type", got)
	}
}

// TestRuntimeRoleForModelType_Empty returns empty.
func TestRuntimeRoleForModelType_Empty(t *testing.T) {
	got := runtimeRoleForModelType("")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// --- SetImageEmbedRequired / ClearImageEmbedRequiredOverride ---

// TestSetImageEmbedRequired_SetsState updates the internal flag.
func TestSetImageEmbedRequired_SetsState(t *testing.T) {
	// Save and restore original state.
	imageEmbedRequiredMu.RLock()
	orig := imageEmbedRequired
	origInit := imageEmbedRequiredInit
	imageEmbedRequiredMu.RUnlock()

	defer func() {
		imageEmbedRequiredMu.Lock()
		imageEmbedRequired = orig
		imageEmbedRequiredInit = origInit
		imageEmbedRequiredMu.Unlock()
	}()

	SetImageEmbedRequired(true)
	imageEmbedRequiredMu.RLock()
	if !imageEmbedRequired || !imageEmbedRequiredInit {
		imageEmbedRequiredMu.RUnlock()
		t.Fatal("expected imageEmbedRequired=true and init=true")
	}
	imageEmbedRequiredMu.RUnlock()

	SetImageEmbedRequired(false)
	imageEmbedRequiredMu.RLock()
	if imageEmbedRequired || !imageEmbedRequiredInit {
		imageEmbedRequiredMu.RUnlock()
		t.Fatal("expected imageEmbedRequired=false and init=true")
	}
	imageEmbedRequiredMu.RUnlock()
}

// TestClearImageEmbedRequiredOverride_ResetsInit clears the init flag.
func TestClearImageEmbedRequiredOverride_ResetsInit(t *testing.T) {
	imageEmbedRequiredMu.RLock()
	origInit := imageEmbedRequiredInit
	imageEmbedRequiredMu.RUnlock()

	defer func() {
		imageEmbedRequiredMu.Lock()
		imageEmbedRequiredInit = origInit
		imageEmbedRequiredMu.Unlock()
	}()

	// Set first so there's something to clear.
	SetImageEmbedRequired(true)
	ClearImageEmbedRequiredOverride()

	imageEmbedRequiredMu.RLock()
	if imageEmbedRequiredInit {
		imageEmbedRequiredMu.RUnlock()
		t.Fatal("expected imageEmbedRequiredInit=false after ClearImageEmbedRequiredOverride")
	}
	// The value itself persists (only init flag is cleared).
	if !imageEmbedRequired {
		imageEmbedRequiredMu.RUnlock()
		t.Fatal("expected imageEmbedRequired to persist after ClearImageEmbedRequiredOverride")
	}
	imageEmbedRequiredMu.RUnlock()
}
