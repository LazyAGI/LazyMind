package currentmemory

import (
	"errors"
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
		{name: "invalid soul", path: SoulPath, content: "identity: invalid\n", wantErr: true},
		{name: "invalid profile", path: ProfilePath, content: "custom: true\n", wantErr: true},
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
