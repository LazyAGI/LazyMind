package runtimeidentity

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMergeAliasesPreservesExistingExtensionFields(t *testing.T) {
	raw := []byte(`{"existing":true,"runtime_aliases":["Old Name"]}`)
	encoded, aliases, err := MergeAliases(raw, " New Name ", "Old Name")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Old Name", "New Name"}; !reflect.DeepEqual(aliases, want) {
		t.Fatalf("aliases = %#v, want %#v", aliases, want)
	}
	var ext map[string]any
	if err := json.Unmarshal(encoded, &ext); err != nil {
		t.Fatal(err)
	}
	if ext["existing"] != true {
		t.Fatalf("extension = %#v, want existing field preserved", ext)
	}
	decoded, err := Aliases(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, aliases) {
		t.Fatalf("decoded aliases = %#v, want %#v", decoded, aliases)
	}
}

func TestNormalizeBindingName(t *testing.T) {
	for input, want := range map[string]string{
		"  Manifest Skill  ":              "manifest-skill",
		"MANIFEST_skill":                  "manifest-skill",
		" External / Canonical_Skill ":    "external/canonical-skill",
		"external//canonical-skill":       "",
		"external / --canonical__skill--": "external/canonical-skill",
	} {
		if got := NormalizeBindingName(input); got != want {
			t.Errorf("NormalizeBindingName(%q) = %q, want %q", input, got, want)
		}
	}
}
