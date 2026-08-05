package doc

import "testing"

func TestNormalizeParsingLLMConfigAddsCanonicalSTTType(t *testing.T) {
	input := map[string]any{
		"stt": map[string]any{
			"source": "siliconflow",
			"model":  "TeleAI/TeleSpeechASR",
		},
	}

	got := normalizeParsingLLMConfig(input)
	roleConfig, ok := got["speech_to_text"].(map[string]any)
	if !ok {
		t.Fatalf("speech_to_text config has type %T", got["speech_to_text"])
	}
	if gotType := roleConfig["type"]; gotType != "stt" {
		t.Fatalf("speech_to_text type = %v, want stt", gotType)
	}
}
