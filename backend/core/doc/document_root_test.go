package doc

import "testing"

func TestRootNodePlainTextUnwrapsEncodedNodes(t *testing.T) {
	raw := `[
  "{\"content\":\"body\\n\\nREFERENCES\",\"metadata\":{\"page\":9}}",
  "{\"content\":\"- Ainslie, J. A paper, 2023.\\n- Beltagy, I. Longformer, 2020.\",\"metadata\":{\"type\":\"list\"}}"
]`
	want := "body\n\nREFERENCES\n\n- Ainslie, J. A paper, 2023.\n- Beltagy, I. Longformer, 2020."
	if got := rootNodePlainText(raw); got != want {
		t.Fatalf("rootNodePlainText() = %q, want %q", got, want)
	}
}

func TestRootNodePlainTextPreservesPlainText(t *testing.T) {
	raw := "already plain text"
	if got := rootNodePlainText(raw); got != raw {
		t.Fatalf("rootNodePlainText() = %q, want original", got)
	}
}
