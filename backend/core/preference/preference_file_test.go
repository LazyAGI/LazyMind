package preference

import (
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestParsePreferenceFileMissingMetadataDefaultsEmpty(t *testing.T) {
	file, err := ParseFileContent("---\nagent_persona: helper\n---\n\n正文")
	if err != nil {
		t.Fatalf("ParseFileContent returned error: %v", err)
	}
	if file.AgentPersona != "helper" || file.PreferredName != "" || file.ResponseStyle != "" || file.Content != "正文" {
		t.Fatalf("unexpected parsed file: %#v", file)
	}
}

func TestParsePreferenceFileWithoutFrontmatterUsesWholeBody(t *testing.T) {
	file, err := ParseFileContent("legacy body")
	if err != nil {
		t.Fatalf("ParseFileContent returned error: %v", err)
	}
	if file.AgentPersona != "" || file.PreferredName != "" || file.ResponseStyle != "" || file.Content != "legacy body" {
		t.Fatalf("unexpected parsed legacy file: %#v", file)
	}
}

func TestParsePreferenceFileRejectsInvalidFrontmatter(t *testing.T) {
	if _, err := ParseFileContent("---\nagent_persona: [\n---\nbody"); err == nil {
		t.Fatal("expected invalid yaml error")
	}
	if _, err := ParseFileContent("---\nagent_persona: helper\nbody"); err == nil {
		t.Fatal("expected missing closing delimiter error")
	}
}

func TestPatchPreferenceFilePreservesUnknownFieldsAndPartialUpdates(t *testing.T) {
	current := "---\nunknown: keep\nagent_persona: old\npreferred_name: Pat\n---\n\nold body"
	responseStyle := "concise"
	next, file, err := PatchFileContent(current, PreferencePatch{ResponseStyle: &responseStyle})
	if err != nil {
		t.Fatalf("PatchFileContent returned error: %v", err)
	}
	if file.AgentPersona != "old" || file.PreferredName != "Pat" || file.ResponseStyle != "concise" || file.Content != "old body" {
		t.Fatalf("unexpected patched file: %#v", file)
	}
	if !strings.Contains(next, "unknown: keep") {
		t.Fatalf("unknown frontmatter field was dropped: %q", next)
	}
}

func TestPatchPreferenceFileEmptyStringClearsField(t *testing.T) {
	current := BuildInitialFileContent(orm.SystemUserPreference{
		Content:       "body",
		AgentPersona:  "agent",
		PreferredName: "Pat",
		ResponseStyle: "warm",
	})
	empty := ""
	next, file, err := PatchFileContent(current, PreferencePatch{PreferredName: &empty})
	if err != nil {
		t.Fatalf("PatchFileContent returned error: %v", err)
	}
	if file.PreferredName != "" {
		t.Fatalf("expected preferred_name cleared, got %#v", file)
	}
	if !strings.Contains(next, `preferred_name: ""`) {
		t.Fatalf("serialized file did not preserve explicit empty preferred_name: %q", next)
	}
}
