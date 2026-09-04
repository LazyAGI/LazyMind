package externalcontext

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeCodexUserMessageStripsTransportEnvelope(t *testing.T) {
	raw := `# Files mentioned by the user:

## screenshot.png: /missing/screenshot.png

# My request for Codex:

请修复图片展示
<image name="Image #1" path="/missing/screenshot.png">`

	text, inputs := normalizeCodexUserMessage("user-1", "history-1", raw, nil)
	if text != "请修复图片展示" {
		t.Fatalf("text = %q, want request body only", text)
	}
	if len(inputs) != 1 || inputs[0]["input_type"] != "text" || inputs[0]["text"] != text {
		t.Fatalf("inputs = %#v, want normalized text input", inputs)
	}
}

func TestNormalizeCodexUserMessageStripsResponseAnnotationsEnvelope(t *testing.T) {
	raw := `# Response annotations:
internal instructions
<response-annotations>[{"text":"旧回答"}]</response-annotations>

## My request:
只保留真实问题`
	text, inputs := normalizeCodexUserMessage("user-1", "history-1", raw, nil)
	if text != "只保留真实问题" || len(inputs) != 1 || inputs[0]["text"] != text {
		t.Fatalf("text=%q inputs=%#v", text, inputs)
	}
}

func TestNormalizeCodexUserMessageLeavesRegularTextUntouched(t *testing.T) {
	raw := "普通 Codex 用户消息"
	text, inputs := normalizeCodexUserMessage("user-1", "history-1", raw, nil)
	if text != raw || inputs != nil {
		t.Fatalf("text=%q inputs=%#v", text, inputs)
	}
}

func TestNormalizeCodexUserMessageStoresTransferredImage(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	raw := "## My request for Codex:\n查看截图\n<image path=\"/var/folders/example/clipboard.png\"></image>"
	text, inputs := normalizeCodexUserMessage("user-1", "history-image", raw, []NativeImage{{
		Name: "clipboard.png", Base64: base64.StdEncoding.EncodeToString([]byte("image-content")),
	}})
	if text != "查看截图" || len(inputs) != 2 {
		t.Fatalf("text=%q inputs=%#v", text, inputs)
	}
	uri, _ := inputs[1]["uri"].(string)
	if !strings.HasPrefix(uri, "/static-files/external-agent-attachments/") {
		t.Fatalf("image uri = %q", uri)
	}
}
