package workflow

import "testing"

func TestPreflightRelativeMarkdownDependencies(t *testing.T) {
	for _, tc := range []struct{ name, markdown, code, path string }{
		{"parent_connector", "[CONNECTORS.md](../../CONNECTORS.md)", "DEPENDENCY_OUTSIDE_PACKAGE", "../../CONNECTORS.md"},
		{"missing", "[data](data.csv)", "DEPENDENCY_RESOURCE_MISSING", "data.csv"},
		{"normalized_existing", "[data](references/../data.csv?raw=1#rows)", "", ""},
		{"space", "[data](<references/my data.csv>)", "DEPENDENCY_RESOURCE_MISSING", "references/my data.csv"},
		{"encoded_escape", "[data](%2e%2e/secret)", "DEPENDENCY_OUTSIDE_PACKAGE", "../secret"},
		{"reference_link", "[data][source]\n\n[source]: ../../CONNECTORS.md", "DEPENDENCY_OUTSIDE_PACKAGE", "../../CONNECTORS.md"},
		{"url", "[web](https://example.com/references/data.csv) [mail](mailto:user@example.com) [local](#intro)", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := []skillPackageFile{{Path: "SKILL.md", Content: "# Test\n" + tc.markdown}}
			if tc.name == "normalized_existing" {
				files = append(files, skillPackageFile{Path: "data.csv", Content: "a,b"})
			}
			checks := preflightSkillSnapshot(workflowSourceSkillSnapshot{Files: files})
			var found []skillConversionCheck
			for _, c := range checks {
				if c.Code == "DEPENDENCY_RESOURCE_MISSING" || c.Code == "DEPENDENCY_OUTSIDE_PACKAGE" {
					found = append(found, c)
				}
			}
			if tc.code == "" {
				if len(found) != 0 {
					t.Fatalf("unexpected dependency warning: %+v", found)
				}
				return
			}
			if len(found) != 1 || found[0].Code != tc.code || found[0].Path != tc.path || found[0].Severity != "warning" {
				t.Fatalf("wanted %s %s, got %+v", tc.code, tc.path, found)
			}
		})
	}
}
