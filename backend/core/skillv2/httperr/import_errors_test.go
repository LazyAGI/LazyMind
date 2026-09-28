package httperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"lazymind/core/common"
	"lazymind/core/skillv2/metadata"
)

func TestImportErrorCategories(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{errors.New("skill package must contain SKILL.md"), "skill_md_not_found"},
		{errors.New("skill package has ambiguous SKILL.md paths"), "skill_md_ambiguous"},
		{errors.New("invalid SKILL.md frontmatter: yaml: line 2: mapping values are not allowed in this context"), "frontmatter_yaml_invalid"},
		{errors.New("SKILL.md frontmatter closing separator is required"), "frontmatter_yaml_invalid"},
		{errors.New(`invalid SKILL.md frontmatter field "name": path segment cannot contain slash`), "invalid_skill_name"},
		{errors.New(`invalid SKILL.md frontmatter field "name": invalid path segment`), "invalid_skill_name"},
		{&metadata.LengthError{Field: "name", Max: metadata.MaxSkillNameLength}, "invalid_skill_name"},
		{&metadata.LengthError{Field: "description", Max: metadata.MaxSkillDescriptionLength}, "description_too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			ReplyError(rec, fmt.Errorf("import package: %w", tt.err))
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("status = %d", rec.Code)
			}
			var response common.APIResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			data, ok := response.Data.(map[string]any)
			if !ok || data["code"] != tt.code {
				t.Fatalf("data = %#v, want code %q", response.Data, tt.code)
			}
		})
	}
}
