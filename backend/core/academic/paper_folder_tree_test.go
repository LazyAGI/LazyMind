package academic

import (
	"fmt"
	"lazymind/core/paperlibrary"
	"testing"
)

func TestPaperFolderHierarchyValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		folders []paperlibrary.Folder
		valid   bool
	}{
		{"legacy", []paperlibrary.Folder{{ID: "a", Name: "Topic"}}, true},
		{"nested", []paperlibrary.Folder{{ID: "a", Name: "Topic"}, {ID: "b", Name: "Topic", ParentID: paperlibrary.Pointer("a")}}, true},
		{"missing parent", []paperlibrary.Folder{{ID: "a", ParentID: paperlibrary.Pointer("missing")}}, false},
		{"self parent", []paperlibrary.Folder{{ID: "a", ParentID: paperlibrary.Pointer("a")}}, false},
		{"cycle", []paperlibrary.Folder{{ID: "a", ParentID: paperlibrary.Pointer("b")}, {ID: "b", ParentID: paperlibrary.Pointer("a")}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := paperlibrary.ValidateFolderTree(tc.folders)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
func TestPaperFolderHierarchyDepth(t *testing.T) {
	for _, depth := range []int{32, 33} {
		folders := []paperlibrary.Folder{}
		for i := 0; i < depth; i++ {
			f := paperlibrary.Folder{ID: fmt.Sprint(i), Name: "Topic"}
			if i > 0 {
				f.ParentID = paperlibrary.Pointer(fmt.Sprint(i - 1))
			}
			folders = append(folders, f)
		}
		if err := paperlibrary.ValidateFolderTree(folders); (err == nil) != (depth <= 32) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
}
