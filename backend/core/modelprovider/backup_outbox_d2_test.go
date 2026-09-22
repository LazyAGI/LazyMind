package modelprovider

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readD2ModelProviderSource(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate model-provider D2 source test")
	}
	payload, err := os.ReadFile(filepath.Join(filepath.Dir(file), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestProviderCredentialMutationsAdvanceRevisionAndEnqueueReferenceOnlyBackup(t *testing.T) {
	source := readD2ModelProviderSource(t, "group.go")
	for _, marker := range []string{
		"credential_revision",
		"enqueueCredentialBackup",
		"credentialvault.BackupUpsert",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("Provider credential mutation flow omitted %q", marker)
		}
	}
	if strings.Contains(source, "enqueueCredentialBackup(apiKey") || strings.Contains(source, "enqueueCredentialBackup(req.APIKey") {
		t.Error("Provider mutation passes plaintext API Key to the backup outbox")
	}
}

func TestProviderListResponseDoesNotExposeStoredAPIKeyToRenderer(t *testing.T) {
	source := readD2ModelProviderSource(t, "group.go")
	listStart := strings.Index(source, "type groupListItem struct")
	if listStart < 0 {
		t.Fatal("cannot locate Provider group list DTO")
	}
	listEnd := strings.Index(source[listStart:], "type groupListResponse struct")
	if listEnd < 0 {
		t.Fatal("cannot locate Provider group list DTO")
	}
	block := source[listStart : listStart+listEnd]
	if strings.Contains(block, `json:"api_key"`) {
		t.Error("Provider group list still exposes the stored API Key to the Desktop renderer")
	}
	if !strings.Contains(block, `json:"has_api_key"`) {
		t.Error("Provider group list omitted a safe has_api_key replacement")
	}
}
