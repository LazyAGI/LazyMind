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

func TestPreflightMarkdownLinksPreserveOtherPaths(t *testing.T) {
	for _, tc := range []struct{ name, markdown, path string }{
		{"same_basename", "[data](data.csv) and assets/data.csv", "assets/data.csv"},
		{"longer_basename", "[data](data.csv) and assets/data.csv.json", "assets/data.csv.json"},
		{"before_link", "assets/data.csv.json and [data](data.csv)", "assets/data.csv.json"},
		{"image", "![data](data.csv) and assets/data.csv", "assets/data.csv"},
		{"reference_link", "[data][source] and assets/data.csv\n\n[source]: data.csv", "assets/data.csv"},
		{"inline_code", "[data](data.csv) and `read assets/data.csv`", "assets/data.csv"},
		{"code_block", "[data](data.csv)\n\n```text\nassets/data.csv\n```", "assets/data.csv"},
		{"html", "[data](data.csv)\n\n<div>\nassets/data.csv\n</div>", "assets/data.csv"},
	} {
		for _, existing := range []bool{false, true} {
			name := "missing/" + tc.name
			if existing {
				name = "existing/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				files := []skillPackageFile{
					{Path: "SKILL.md", Content: "# Test\n" + tc.markdown},
					{Path: "data.csv", Content: "a,b"},
				}
				if existing {
					files = append(files, skillPackageFile{Path: tc.path, Content: "a,b"})
				}
				var found []skillConversionCheck
				for _, check := range preflightSkillSnapshot(workflowSourceSkillSnapshot{Files: files}) {
					if check.Code == "DEPENDENCY_RESOURCE_MISSING" || check.Code == "DEPENDENCY_OUTSIDE_PACKAGE" {
						found = append(found, check)
					}
				}
				if existing {
					if len(found) != 0 {
						t.Fatalf("unexpected dependency warning: %+v", found)
					}
				} else if len(found) != 1 || found[0].Code != "DEPENDENCY_RESOURCE_MISSING" || found[0].Path != tc.path || found[0].Severity != "warning" {
					t.Fatalf("wanted missing dependency %s, got %+v", tc.path, found)
				}
			})
		}
	}
}
