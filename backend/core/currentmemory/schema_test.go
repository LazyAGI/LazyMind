package currentmemory

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const validReferenceDocument = `---
name: pref.response.technical_detail
summary: Explain tradeoffs for technical questions.
created_at: "2026-07-20T09:30:00+08:00"
updated_at: "2026-07-20T09:30:00+08:00"
source:
  kind: chat_explicit
  conversation_id: conversation-1
---
## Application Scenarios
Technical questions.

## Preference Details
Explain motivations and tradeoffs.

## Reason
The user requested it.
`

const legacySoulV1YAML = `identity:
  name: Legacy
  role: 助手
  description: 旧用户的助手
mission:
  primary_goal: 保留目标
  success_definition: 保留成功标准
interaction:
  relationship_mode: 协作者
  default_tone: 直接
  initiative_level: 主动
  challenge_level: 建设性
  decision_mode: 先建议再确认
epistemic:
  uncertainty_style: 明确说明
  verification_mode: 必要时核验
`

const legacyProfileV1YAML = `identity:
  preferred_name: Alice
  aliases: [A]
  pronouns: she/her
locale:
  languages: [中文, English]
  timezone: Asia/Shanghai
  region: 上海
professional:
  roles: [产品经理]
  organization: LazyMind
  industry: 人工智能
  expertise_domains: [Agent Memory]
accessibility:
  communication_needs: [无]
`

func TestValidateDocumentForPathMatchesCurrentMemorySchemas(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		path    string
		content string
		wantErr bool
	}{
		{name: "default soul", path: SoulPath, content: DefaultSoulYAML},
		{name: "default profile", path: ProfilePath, content: DefaultProfileYAML},
		{name: "default preference", path: PreferencePath, content: DefaultPreferenceYAML},
		{name: "valid reference", path: ReferencesPath + "/response.md", content: validReferenceDocument},
		{name: "generic memory file remains compatible", path: "memory/work/notes.txt", content: "free form"},
		{name: "dynamic soul mapping", path: SoulPath, content: "custom:\n  style: direct\n", wantErr: true},
		{name: "dynamic profile mapping", path: ProfilePath, content: "custom:\n  nickname: Neo\n", wantErr: true},
		{name: "invalid soul", path: SoulPath, content: "- invalid\n", wantErr: true},
		{name: "invalid profile", path: ProfilePath, content: "plain text\n", wantErr: true},
		{name: "invalid preference", path: PreferencePath, content: "preferences: wrong\n", wantErr: true},
		{name: "invalid reference slug", path: ReferencesPath + "/bad name.md", content: validReferenceDocument, wantErr: true},
		{name: "invalid reference content", path: ReferencesPath + "/response.md", content: "# missing frontmatter\n", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateDocumentForPath(testCase.path, []byte(testCase.content))
			if testCase.wantErr {
				if !errors.Is(err, ErrInvalidDocument) {
					t.Fatalf("ValidateDocumentForPath() error = %v, want ErrInvalidDocument", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateDocumentForPath() error = %v", err)
			}
		})
	}
}

func TestNormalizeSoulReconcilesLegacyDocument(t *testing.T) {
	document, stored, err := NormalizeSoul([]byte(legacySoulV1YAML))
	if err != nil {
		t.Fatal(err)
	}
	if memoryString(t, document, "identity.name") != "Legacy" ||
		memoryString(t, document, "interaction.default_tone") != "直接" ||
		memoryString(t, document, "interaction.default_relationship_mode") != "协作者" {
		t.Fatalf("Soul reconciliation result = %#v", document)
	}
	if string(stored) == legacySoulV1YAML ||
		!containsAll(
			string(stored),
			"schema_version: 2",
			"default_relationship_mode: 协作者",
			"default_tone: 直接",
		) {
		t.Fatalf("unexpected reconciled Soul:\n%s", stored)
	}
}

func memoryValue(t *testing.T, document MemoryDocument, path string) any {
	t.Helper()
	value, ok := nestedValue(document, path)
	if !ok {
		t.Fatalf("memory path %q not found in %#v", path, document)
	}
	return value
}

func memoryString(t *testing.T, document MemoryDocument, path string) string {
	t.Helper()
	value, ok := memoryValue(t, document, path).(string)
	if !ok {
		t.Fatalf("memory path %q is not a string", path)
	}
	return value
}

func memoryList(t *testing.T, document MemoryDocument, path string) []any {
	t.Helper()
	value, ok := memoryValue(t, document, path).([]any)
	if !ok {
		t.Fatalf("memory path %q is not a list", path)
	}
	return value
}

func TestNormalizeProfileReconcilesLegacyDocumentWithLatestTemplate(t *testing.T) {
	document, stored, err := NormalizeProfile([]byte(legacyProfileV1YAML))
	if err != nil {
		t.Fatal(err)
	}
	rawDocument, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var visible map[string]any
	if err := json.Unmarshal(rawDocument, &visible); err != nil {
		t.Fatal(err)
	}
	identity := visible["identity"].(map[string]any)
	locale := visible["locale"].(map[string]any)
	professional := visible["professional"].(map[string]any)
	if identity["preferred_name"] != "Alice" {
		t.Fatalf("same-path scalar was not preserved: %#v", identity)
	}
	aliases := identity["aliases"].([]any)
	if len(aliases) != 1 || aliases[0] != "A" {
		t.Fatalf("same-path list was not preserved: %#v", aliases)
	}
	if locale["residence"] != nil {
		t.Fatalf("renamed field must use the latest template default: %#v", locale)
	}
	if len(professional["occupations"].([]any)) != 0 ||
		len(professional["organizations"].([]any)) != 0 ||
		len(professional["industries"].([]any)) != 0 {
		t.Fatalf("renamed fields must use the latest template defaults: %#v", professional)
	}
	if !containsAll(
		string(stored),
		"schema_version: 2",
		"preferred_name: Alice",
		"residence: null",
		"occupations:",
		"organizations:",
		"industries:",
	) {
		t.Fatalf("unexpected migrated Profile:\n%s", stored)
	}
	for _, removed := range []string{
		"pronouns:",
		"timezone:",
		"region:",
		"roles:",
		"organization:",
		"industry:",
		"accessibility:",
	} {
		if containsAll(string(stored), removed) {
			t.Fatalf("migrated Profile retained %q:\n%s", removed, stored)
		}
	}
}

func TestTemplateReconciliationConvertsStringAndStringListValues(t *testing.T) {
	template := &MemoryTemplate{
		Kind:          "test",
		SchemaVersion: 3,
		Document: MemoryDocument{
			"values": MemoryDocument{
				"list_from_string": []any{},
				"list_from_blank":  []any{},
				"string_from_list": "",
				"nullable":         nil,
			},
		},
	}
	storedDefaults, err := template.render(template.Document)
	if err != nil {
		t.Fatal(err)
	}
	template.storedDefaults = storedDefaults

	document, stored := template.normalizeForRead([]byte(`
schema_version: 2
values:
  list_from_string: LazyMind
  list_from_blank: "  "
  string_from_list: [LazyMind, LazyMind]
  nullable: [北京, 上海]
extra: removed
`))
	if got := memoryList(t, document, "values.list_from_string"); len(got) != 1 || got[0] != "LazyMind" {
		t.Fatalf("str -> list[str] = %#v", got)
	}
	if got := memoryList(t, document, "values.list_from_blank"); len(got) != 0 {
		t.Fatalf("blank str -> list[str] = %#v", got)
	}
	if got := memoryString(t, document, "values.string_from_list"); got != "LazyMind， LazyMind" {
		t.Fatalf("list[str] -> str = %q", got)
	}
	if got := memoryString(t, document, "values.nullable"); got != "北京， 上海" {
		t.Fatalf("list[str] -> nullable string = %q", got)
	}
	if bytes.Contains(stored, []byte("extra:")) || !bytes.Contains(stored, []byte("schema_version: 3")) {
		t.Fatalf("reconciled storage =\n%s", stored)
	}
}

func TestApplyMemoryOperationsFreezesLeafContractsAcrossBatch(t *testing.T) {
	document := MemoryDocument{
		"custom": MemoryDocument{
			"nullable": nil,
			"text":     "before",
			"tags":     []any{"one"},
		},
	}
	setValue := "temporary"
	addValue := "two"
	result, err := applyMemoryOperations(
		document,
		[]CurrentMemoryOperation{
			{Op: "set", Path: "custom.nullable", Value: &setValue},
			{Op: "clear", Path: "custom.nullable"},
			{Op: "set", Path: "custom.text", Value: &setValue},
			{Op: "clear", Path: "custom.text"},
			{Op: "add", Path: "custom.tags", Value: &addValue},
			{Op: "remove", Path: "custom.tags", Value: &addValue},
		},
		"test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if value := memoryValue(t, result, "custom.nullable"); value != nil {
		t.Fatalf("nullable set+clear = %#v, want nil", value)
	}
	if value := memoryString(t, result, "custom.text"); value != "" {
		t.Fatalf("string set+clear = %q, want empty string", value)
	}
	if values := memoryList(t, result, "custom.tags"); len(values) != 1 || values[0] != "one" {
		t.Fatalf("list add+remove = %#v", values)
	}
}

func TestGetProfileReplacesInvalidStoredDocumentWithLatestTemplate(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	module := NewModule(db.DB)
	repository := NewRepository(db.DB)
	if err := repository.EnsureInitialized(t.Context(), "invalid-profile"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte("not: [valid"),
		[]byte("schema_version: future\nidentity: {}\n"),
		[]byte("schema_version: 99\nidentity: {}\n"),
		[]byte("schema_version: 2\nidentity: wrong\n"),
	} {
		if err := repository.UpdateFileContent(
			t.Context(),
			"invalid-profile",
			ProfilePath,
			invalid,
			time.Now().UTC(),
		); err != nil {
			t.Fatal(err)
		}
		result, err := module.GetProfile(t.Context(), "invalid-profile")
		if err != nil {
			t.Fatalf("GetProfile(%q): %v", invalid, err)
		}
		if memoryValue(t, result.Document, "identity.preferred_name") != nil {
			t.Fatalf("invalid profile was not reset: %#v", result.Document)
		}
		entry, err := repository.GetEntry(t.Context(), "invalid-profile", ProfilePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(entry.Content) != DefaultProfileYAML {
			t.Fatalf("invalid profile storage was not replaced:\n%s", entry.Content)
		}
	}
}

func TestGetProfileReturnsStorageErrorWhenTemplateReplacementFails(t *testing.T) {
	db := newCurrentMemoryTestDB(t)
	module := NewModule(db.DB)
	repository := NewRepository(db.DB)
	if err := repository.EnsureInitialized(t.Context(), "write-failure"); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateFileContent(
		t.Context(),
		"write-failure",
		ProfilePath,
		[]byte("invalid: ["),
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
CREATE TRIGGER reject_profile_template_update
BEFORE UPDATE ON memory_current_entries
WHEN OLD.user_id = 'write-failure' AND OLD.path = 'memory/users/profile.yaml'
BEGIN
  SELECT RAISE(FAIL, 'template replacement rejected');
END
`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := module.GetProfile(t.Context(), "write-failure"); err == nil ||
		!strings.Contains(err.Error(), "template replacement rejected") {
		t.Fatalf("GetProfile() error = %v", err)
	}
}

func containsAll(value string, expected ...string) bool {
	for _, item := range expected {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}
