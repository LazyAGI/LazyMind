package codex

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestRemoveLegacyControlBlockRestoresOriginalLine(t *testing.T) {
	original := "chatgpt_base_url = \"https://example.test/backend-api\"\n"
	body := "model = \"gpt\"\n" + legacyControlBegin + "\n" +
		legacyOriginalLine + base64.RawStdEncoding.EncodeToString([]byte(original)) + "\n" +
		"# upstream-chatgpt-base-url: ignored\n" +
		"chatgpt_base_url = \"http://127.0.0.1:19091/backend-api\"\n" + legacyControlEnd + "\n"

	restored, changed, err := removeLegacyControlBlock([]byte(body))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if string(restored) != "model = \"gpt\"\n"+original {
		t.Fatalf("restored config:\n%s", restored)
	}
}

func TestRemoveLegacyControlBlockWithEmptyOriginalRemovesOnlyBlock(t *testing.T) {
	body := legacyControlBegin + "\n" + legacyOriginalLine + "\nchatgpt_base_url = \"http://127.0.0.1:19091/backend-api\"\n" + legacyControlEnd + "\nmodel = \"gpt\"\n"
	restored, changed, err := removeLegacyControlBlock([]byte(body))
	if err != nil || !changed || string(restored) != "model = \"gpt\"\n" {
		t.Fatalf("changed=%v err=%v restored=%q", changed, err, restored)
	}
}

func TestRemoveLegacyControlBlockRejectsIncompleteMarker(t *testing.T) {
	_, _, err := removeLegacyControlBlock([]byte(legacyControlBegin + "\n"))
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err=%v", err)
	}
}
