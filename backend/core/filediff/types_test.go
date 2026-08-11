package filediff

import (
	"testing"
)

// TestOptionsDefaultValues verifies zero-value defaults are reasonable.
func TestOptionsDefaultValues(t *testing.T) {
	opts := Options{}
	if opts.ContextLines != 0 {
		t.Fatal("ContextLines should default to 0")
	}
}

// TestFileDiffDefaultFields verifies zero-value defaults for FileDiff.
func TestFileDiffDefaultFields(t *testing.T) {
	fd := FileDiff{}
	if fd.Status != "" {
		t.Fatal("Status should default to empty")
	}
	if fd.Binary {
		t.Fatal("Binary should default to false")
	}
	if fd.Supported {
		t.Fatal("Supported should default to false")
	}
}

// TestDiffEntryLineDefaultFields verifies zero-value defaults.
func TestDiffEntryLineDefaultFields(t *testing.T) {
	line := DiffEntryLine{}
	if line.Type != "" {
		t.Fatal("Type should default to empty")
	}
	if line.OldLine != 0 {
		t.Fatal("OldLine should default to 0")
	}
}

// TestContentDefaultFields verifies zero-value defaults for Content.
func TestContentDefaultFields(t *testing.T) {
	c := Content{}
	if c.Path != "" {
		t.Fatal("Path should default to empty")
	}
	if c.Binary {
		t.Fatal("Binary should default to false")
	}
	if c.EditableText {
		t.Fatal("EditableText should default to false")
	}
}
