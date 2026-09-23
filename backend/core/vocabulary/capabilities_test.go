package vocabulary

import "testing"

func TestCapabilityRegistryKeepsEnglishCompatibleAndOtherCapabilitiesLocal(t *testing.T) {
	english, _, err := validateBookCapability("", nil)
	if err != nil || english.Key != "english_definition" || english.LocalOnly {
		t.Fatalf("default capability = %#v, err=%v", english, err)
	}
	classical, questions, err := validateBookCapability("classical_definition", nil)
	if err != nil || !classical.LocalOnly || len(questions) == 0 {
		t.Fatalf("classical capability = %#v, questions=%v, err=%v", classical, questions, err)
	}
	if _, _, err := validateBookCapability("classical_definition", []string{"cloze"}); err == nil {
		t.Fatal("expected incompatible question type to be rejected")
	}
}
