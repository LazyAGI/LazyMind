package academic

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestNormalizeDOI(t *testing.T) {
	got := NormalizeDOI("https://doi.org/10.1000/ABC.123).")
	if got != "10.1000/abc.123" {
		t.Fatalf("NormalizeDOI() = %q", got)
	}
}

func TestNormalizeArxivID(t *testing.T) {
	base, version := NormalizeArxivID("https://arxiv.org/pdf/2501.01234v3.pdf")
	if base != "2501.01234" || version != "v3" {
		t.Fatalf("NormalizeArxivID() = %q, %q", base, version)
	}
}

func TestExtractStrongIDsDoesNotTreatDOISuffixAsArxiv(t *testing.T) {
	doi, arxiv, _ := extractStrongIDs("doi: 10.1145/3620665.3640366. URL https://doi.org/10.1145/3620665.3640366")
	if doi != "10.1145/3620665.3640366" || arxiv != "" {
		t.Fatalf("extractStrongIDs() = doi %q, arxiv %q", doi, arxiv)
	}
}

func TestExtractStrongIDsRepairsPDFSpacingInArxivURL(t *testing.T) {
	_, arxiv, _ := extractStrongIDs("URL https://arxiv.org/abs/2306. 14898.")
	if arxiv != "2306.14898" {
		t.Fatalf("arxiv = %q", arxiv)
	}
}

func TestExtractStrongIDsRepairsSpacesAroundArxivPath(t *testing.T) {
	_, arxiv, _ := extractStrongIDs("URL https://arxiv.org/ abs/2209.15001.")
	if arxiv != "2209.15001" {
		t.Fatalf("arxiv = %q", arxiv)
	}
}

func TestExtractStrongIDsFromSpacedArxivDOI(t *testing.T) {
	_, arxiv, _ := extractStrongIDs("doi: 10.48550/ARXIV.2309.03882. https://doi.org/10.48550/arXiv. 2309.03882")
	if arxiv != "2309.03882" {
		t.Fatalf("arxiv = %q", arxiv)
	}
}

func TestExtractReferenceURLRepairsPDFSpacing(t *testing.T) {
	got := extractReferenceURL("gpt-fast. URL https://github.com/ pytorch-labs/gpt-fast.")
	if got != "https://github.com/pytorch-labs/gpt-fast" {
		t.Fatalf("extractReferenceURL() = %q", got)
	}
}

func TestExtractReferenceURLRepairsMissingColon(t *testing.T) {
	got := extractReferenceURL("torchtune. URL https//github.com/pytorch/torchtune.")
	if got != "https://github.com/pytorch/torchtune" {
		t.Fatalf("extractReferenceURL() = %q", got)
	}
}

func TestReferenceIdentityUsesNormalizedRawText(t *testing.T) {
	left := NormalizeTitle("Dao, T.  FlashAttention-2: Faster Attention, 2023.")
	right := NormalizeTitle("Dao, T. FlashAttention-2: Faster Attention, 2023.")
	if left != right {
		t.Fatalf("normalized identities differ: %q != %q", left, right)
	}
}

func TestSplitReferences(t *testing.T) {
	section := "[1] A. Author. First Paper. 2024. doi:10.1234/Example.1\n[2] B. Author. Second Paper. arXiv:2501.01234v2."
	items := splitReferences(section)
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(items), items)
	}
	if items[0].DOI != "10.1234/example.1" || items[0].Year != 2024 {
		t.Fatalf("first = %#v", items[0])
	}
	if items[1].Arxiv != "2501.01234" {
		t.Fatalf("second = %#v", items[1])
	}
}

func TestSplitUnnumberedReferencesFromPdfTextLines(t *testing.T) {
	section := "Beltagy, I., Peters, M. E., and Cohan, A. Longformer:\nThe long-document transformer, 2020. URL https://arxiv.org/abs/2004.05150.\nChen, T., Moreau, T., and Jiang, Z. TVM: an automated\nend-to-end compiler, 2018."
	items := splitReferences(section)
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(items), items)
	}
	if items[0].Arxiv != "2004.05150" || items[0].Year != 2020 {
		t.Fatalf("first = %#v", items[0])
	}
	if items[1].Year != 2018 {
		t.Fatalf("second = %#v", items[1])
	}
}

func TestSplitMinerURootListReferences(t *testing.T) {
	section := "- Ainslie, J. GQA: Training generalized multi-query transformer models, 2023. URL https://arxiv.org/abs/2305.13245.\n- Beltagy, I., Peters, M. E., and Cohan, A. Longformer, 2020. URL https://arxiv.org/abs/2004.05150."
	items := splitReferences(section)
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(items), items)
	}
	if items[0].Arxiv != "2305.13245" || items[1].Arxiv != "2004.05150" {
		t.Fatalf("unexpected arxiv ids: %#v", items)
	}
}

func TestSplitReferencesMergesWrappedAuthorFragment(t *testing.T) {
	section := "A. Author, B. Author,\n\net al. A useful paper. arXiv preprint arXiv:2309.11998, 2023."
	items := splitReferences(section)
	if len(items) != 1 || items[0].Arxiv != "2309.11998" || items[0].Year != 2023 {
		t.Fatalf("items = %#v", items)
	}
}

func TestBibliographySectionStopsAtAppendix(t *testing.T) {
	text := "body\nReferences\n[1] citation\nAppendix A\nnot a citation"
	if got := bibliographySection(text); got != "[1] citation" {
		t.Fatalf("section = %q", got)
	}
}

func TestBibliographySectionStopsAtInlineAppendix(t *testing.T) {
	text := "body\nREFERENCES\n- Wu, M. A useful paper, 2024. A APPENDIX A.1 Details that are not a citation."
	if got := bibliographySection(text); got != "- Wu, M. A useful paper, 2024." {
		t.Fatalf("section = %q", got)
	}
}

func TestValidateRemoteURLRejectsPrivateAddress(t *testing.T) {
	if _, err := validateRemoteURL(context.Background(), "http://127.0.0.1/paper.pdf"); err == nil {
		t.Fatal("expected loopback URL to be rejected")
	}
}

func TestTokenCoverage(t *testing.T) {
	if got := tokenCoverage("A. Author. Attention Is All You Need. 2017.", "Attention Is All You Need"); got != 1 {
		t.Fatalf("coverage = %v", got)
	}
}

func TestCitationTitle(t *testing.T) {
	citation := "Tri Dao, Daniel Y. Fu, Stefano Ermon, Atri Rudra, and Christopher Ré. 2022. FlashAttention: Fast and Memory-Efficient Exact Attention with IO-Awareness. In NeurIPS."
	if got := citationTitle(citation); got != "FlashAttention: Fast and Memory-Efficient Exact Attention with IO-Awareness" {
		t.Fatalf("citationTitle() = %q", got)
	}
}

func TestCitationTitleWhenYearIsLast(t *testing.T) {
	citation := "J. Ainslie, J. Lee-Thorp, and M. de Jong. GQA: Training generalized multi-query transformer models from multi-head checkpoints. arXiv preprint arXiv:2305.13245, 2023."
	if got := citationTitle(citation); got != "GQA: Training generalized multi-query transformer models from multi-head checkpoints" {
		t.Fatalf("citationTitle() = %q", got)
	}
}

func TestCitationTitleWhenYearPrecedesURL(t *testing.T) {
	citation := "Rabe, M. N. and Staats, C. Self-attention does not need o(n2) memory, 2022. URL https://arxiv.org/ abs/2112.05682."
	if got := citationTitle(citation); got != "Self-attention does not need o(n2) memory" {
		t.Fatalf("citationTitle() = %q", got)
	}
}

func TestCitationTitleBeforeVenueAndYear(t *testing.T) {
	citation := "Vaswani, A., Shazeer, N., Parmar, N., and Polosukhin, I. Attention is all you need. Advances in Neural Information Processing Systems, 2017."
	if got := citationTitle(citation); got != "Attention is all you need" {
		t.Fatalf("citationTitle() = %q", got)
	}
}

func TestCitationTitleRepairsPDFWordBreaks(t *testing.T) {
	citation := "Dao, T., Fu, D. Y., Ermon, S., Rudra, A., and Re, C. Flashat- ́ tention: Fast and memory-efficient exact attention with IO-awareness. In NeurIPS, 2022."
	if got := citationTitle(citation); got != "Flashattention: Fast and memory-efficient exact attention with IO-awareness" {
		t.Fatalf("citationTitle() = %q", got)
	}
}

func TestCitationTitleIgnoresPublisherTextAfterYear(t *testing.T) {
	citation := "Chen, T., Moreau, T., Jiang, Z., Zheng, L., Yan, E., Cowan, M., Shen, H., Wang, L., Hu, Y., Ceze, L., Guestrin, C., and Krishnamurthy, A. TVM: an automated end-to-end optimizing compiler for deep learning. In Proceedings of OSDI, USA, 2018. USENIX Association. ISBN 9781931971478."
	if got := citationTitle(citation); got != "TVM: an automated end-to-end optimizing compiler for deep learning" {
		t.Fatalf("citationTitle() = %q", got)
	}
}

func TestSplitNumberedReferencesKeepsDashedPageContinuation(t *testing.T) {
	section := "- [6] Mark Chen, Jerry Tworek, Heewoo Jun, Qiming Yuan, Henrique Ponde de Oliveira Pinto, Jared Kaplan, Harri Edwards, Yuri Burda, Nicholas\n- Joseph, Greg Brockman, et al. 2021. Evaluating large language models trained on code. arXiv preprint arXiv:2107.03374 (2021).\n- [7] Tianqi Chen, Bing Xu, Chiyuan Zhang, and Carlos Guestrin. 2016. Training deep nets with sublinear memory cost."
	items := splitReferences(section)
	if len(items) != 2 {
		t.Fatalf("splitReferences() returned %d items, want 2: %#v", len(items), items)
	}
	if items[0].Key != "6" || !strings.Contains(items[0].Raw, "Joseph, Greg Brockman") {
		t.Fatalf("first reference was truncated across page boundary: %#v", items[0])
	}
	if items[1].Key != "7" {
		t.Fatalf("second reference key = %q, want 7", items[1].Key)
	}
}

func TestAcquirePreviouslyFailedArxivPDFs(t *testing.T) {
	if os.Getenv("RUN_ACADEMIC_NETWORK_TESTS") != "1" {
		t.Skip("set RUN_ACADEMIC_NETWORK_TESTS=1 to exercise real arXiv downloads")
	}
	ids := []string{
		"2405.05751",
		"2407.08608",
		"2410.01359",
		"2004.05150",
		"2108.12409",
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			path, size, err := acquirePDF(context.Background(), FulltextCandidate{
				URL:            "https://arxiv.org/pdf/" + id + ".pdf",
				SourceProvider: "arxiv",
				ExpectedMIME:   "application/pdf",
			})
			if err != nil {
				t.Fatalf("download failed: %v", err)
			}
			defer os.Remove(path)
			if size < 1024 {
				t.Fatalf("downloaded PDF is too small: %d", size)
			}
		})
	}
}

func TestResolveMetadataOnlyReferencesWithArxiv(t *testing.T) {
	if os.Getenv("RUN_ACADEMIC_NETWORK_TESTS") != "1" {
		t.Skip("set RUN_ACADEMIC_NETWORK_TESTS=1 to exercise real arXiv resolution")
	}
	tests := []struct {
		citation string
		wantID   string
	}{
		{"Tri Dao, Daniel Y. Fu, Stefano Ermon, Atri Rudra, and Christopher Ré. 2022. FlashAttention: Fast and Memory-Efficient Exact Attention with IO-Awareness. In NeurIPS.", "2205.14135"},
		{"Elias Frantar, Saleh Ashkboos, Torsten Hoefler, and Dan Alistarh. 2022. GPTQ: accurate post-training quantization for generative pre-trained transformers. arXiv preprint.", "2210.17323"},
		{"Yaniv Leviathan, Matan Kalman, and Yossi Matias. 2022. Fast inference from transformers via speculative decoding. arXiv preprint.", "2211.17192"},
		{"Chen, T., Moreau, T., Jiang, Z., Zheng, L., Yan, E., Cowan, M., Shen, H., Wang, L., Hu, Y., Ceze, L., Guestrin, C., and Krishnamurthy, A. TVM: an automated end-to-end optimizing compiler for deep learning. In OSDI, 2018.", "1802.04799"},
		{"Raffel, C., Shazeer, N., Roberts, A., Lee, K., Narang, S., Matena, M., Zhou, Y., Li, W., and Liu, P. J. Exploring the limits of transfer learning with a unified text-to-text transformer, 2020.", "1910.10683"},
		{"Vaswani, A., Shazeer, N., Parmar, N., Uszkoreit, J., Jones, L., Gomez, A. N., Kaiser, L., and Polosukhin, I. Attention is all you need. Advances in Neural Information Processing Systems, 2017.", "1706.03762"},
	}
	for _, test := range tests {
		work, ok := resolveWithArxiv(context.Background(), test.citation)
		if !ok || work.ArxivID != test.wantID {
			t.Errorf("resolveWithArxiv(%q) = %#v, %v; want %s", test.citation, work, ok, test.wantID)
		}
	}
}

func TestResolveMetadataOnlyReferencesWithOpenAlex(t *testing.T) {
	if os.Getenv("RUN_ACADEMIC_NETWORK_TESTS") != "1" {
		t.Skip("set RUN_ACADEMIC_NETWORK_TESTS=1 to exercise real OpenAlex resolution")
	}
	work, ok := resolveWithOpenAlex(context.Background(),
		"Tri Dao, Daniel Y. Fu, Stefano Ermon, Atri Rudra, and Christopher Ré. 2022. FlashAttention: Fast and Memory-Efficient Exact Attention with IO-Awareness. In NeurIPS.")
	if !ok || NormalizeTitle(work.Title) != NormalizeTitle("FlashAttention: Fast and Memory-Efficient Exact Attention with IO-Awareness") || work.ArxivID != "2205.14135" {
		t.Fatalf("resolveWithOpenAlex() = %#v, %v", work, ok)
	}
}
