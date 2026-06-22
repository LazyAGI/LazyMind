package filefilter

import (
	"testing"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestPolicyAllowsOnlyConfiguredExtensions(t *testing.T) {
	t.Parallel()

	policy := FromBinding(store.Binding{IncludeExtensions: store.JSON{"items": []any{"pdf", "docx"}}})
	if !policy.Allows(ObjectInfo{DisplayName: "guide.pdf", IsDocument: true, FileExtension: ".pdf"}) {
		t.Fatalf("pdf should be allowed")
	}
	if policy.Allows(ObjectInfo{DisplayName: "script.py", IsDocument: true, FileExtension: ".py"}) {
		t.Fatalf("py should be filtered")
	}
}

func TestPolicyFallsBackToSourceExtensions(t *testing.T) {
	t.Parallel()

	policy := FromSourceBinding(
		store.Source{IncludeExtensions: store.JSON{"items": []any{"pdf"}}},
		store.Binding{ProviderOptions: store.JSON{"include_extensions": []any{"py"}}},
	)
	if !policy.Allows(ObjectInfo{DisplayName: "guide.pdf", IsDocument: true, FileExtension: ".pdf"}) {
		t.Fatalf("source include extensions should be used when binding include is empty")
	}
	if policy.Allows(ObjectInfo{DisplayName: "script.py", IsDocument: true, FileExtension: ".py"}) {
		t.Fatalf("provider include extensions should not override source include extensions")
	}
}

func TestPolicyTreatsFeishuNativeDocumentAsMarkdown(t *testing.T) {
	t.Parallel()

	policy := FromBinding(store.Binding{IncludeExtensions: store.JSON{"items": []any{"md"}}})
	if !policy.Allows(ObjectInfo{
		DisplayName: "native-doc",
		IsDocument:  true,
		ProviderMeta: map[string]any{
			"kind":      "drive_file",
			"file_type": "docx",
		},
	}) {
		t.Fatalf("native feishu document should be treated as markdown")
	}
	if policy.Allows(ObjectInfo{
		DisplayName: "upload-without-extension",
		IsDocument:  true,
		ProviderMeta: map[string]any{
			"kind":      "drive_file",
			"file_type": "file",
		},
	}) {
		t.Fatalf("uploaded file without an extension should not be treated as markdown")
	}
}
