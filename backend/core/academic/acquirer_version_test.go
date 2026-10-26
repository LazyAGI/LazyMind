package academic

import (
	"strings"
	"testing"
)

func TestArxivFallbackPreservesExplicitVersion(t *testing.T) {
	for _, raw := range []string{"https://arxiv.org/pdf/2501.01234v3.pdf", "https://arxiv.org/abs/hep-th/9901001v2"} {
		urls := candidateOriginalURLs(FulltextCandidate{URL: raw, SourceProvider: "arxiv"})
		_, version := NormalizeArxivID(raw)
		for _, u := range urls {
			if !strings.Contains(u, version) {
				t.Fatalf("version %s lost in fallback %s", version, u)
			}
		}
	}
}
