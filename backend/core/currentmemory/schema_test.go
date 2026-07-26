package currentmemory

import (
	"errors"
	"strings"
	"testing"
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

func TestNormalizeSoulMigratesV1ToV2(t *testing.T) {
	document, stored, err := NormalizeSoul([]byte(legacySoulV1YAML))
	if err != nil {
		t.Fatal(err)
	}
	if document.Interaction.DefaultRelationshipMode != "协作者" ||
		document.Interaction.DefaultInitiativeLevel != "主动" ||
		document.Interaction.DefaultChallengeLevel != "建设性" ||
		document.Interaction.DefaultDecisionMode != "先建议再确认" {
		t.Fatalf("Soul v1 values were not preserved: %#v", document.Interaction)
	}
	if string(stored) == legacySoulV1YAML ||
		!containsAll(
			string(stored),
			"schema_version: 2",
			"default_relationship_mode: 协作者",
			"default_initiative_level: 主动",
			"default_challenge_level: 建设性",
			"default_decision_mode: 先建议再确认",
		) {
		t.Fatalf("unexpected migrated Soul:\n%s", stored)
	}
}

func TestNormalizeProfileMigratesV1ToV2(t *testing.T) {
	document, stored, err := NormalizeProfile([]byte(legacyProfileV1YAML))
	if err != nil {
		t.Fatal(err)
	}
	if document.Locale.Residence == nil || *document.Locale.Residence != "上海" {
		t.Fatalf("Profile residence was not migrated: %#v", document.Locale)
	}
	if len(document.Professional.Occupations) != 1 ||
		document.Professional.Occupations[0] != "产品经理" ||
		len(document.Professional.Organizations) != 1 ||
		document.Professional.Organizations[0] != "LazyMind" ||
		len(document.Professional.Industries) != 1 ||
		document.Professional.Industries[0] != "人工智能" {
		t.Fatalf("Profile professional data was not migrated: %#v", document.Professional)
	}
	if !containsAll(
		string(stored),
		"schema_version: 2",
		"residence: 上海",
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

func containsAll(value string, expected ...string) bool {
	for _, item := range expected {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}
