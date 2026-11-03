package arxivsource

import "testing"

func TestParseCommonIdentifiers(t *testing.T) {
	tests := []struct{ input, base, version string }{
		{"2404.06654", "2404.06654", ""},
		{" arXiv: 2404.06654v3 ", "2404.06654", "v3"},
		{"ARXIV : 2404.06654V3", "2404.06654", "v3"},
		{"https://arxiv.org/pdf/2404.06654", "2404.06654", ""},
		{"https://arxiv.org/abs/2404.06654v3", "2404.06654", "v3"},
		{"http://www.arxiv.org/pdf/2404.06654v3.pdf?download=1#page=2", "2404.06654", "v3"},
		{"https://export.arxiv.org/html/2404.06654v3", "2404.06654", "v3"},
		{"arxiv.org/format/2404.06654v3", "2404.06654", "v3"},
		{"www.arxiv.org/src/2404.06654v3", "2404.06654", "v3"},
		{"//export.arxiv.org/e-print/2404.06654v3", "2404.06654", "v3"},
		{"[RULER](https://arxiv.org/pdf/2404.06654v3.pdf)", "2404.06654", "v3"},
		{"[RULER](https://arxiv.org/abs/2404.06654v3 \"Paper\")", "2404.06654", "v3"},
		{"<https://arxiv.org/abs/2404.06654v3>", "2404.06654", "v3"},
		{"2404.06654v3.pdf", "2404.06654", "v3"},
		{"0704.0001v2", "0704.0001", "v2"},
		{"1412.7878", "1412.7878", ""},
		{"1501.00001", "1501.00001", ""},
		{"arXiv:hep-th/9901001v2", "hep-th/9901001", "v2"},
		{"https://arxiv.org/pdf/cond-mat/0601001v1.pdf", "cond-mat/0601001", "v1"},
		{"math.CA/0611800v2", "math/0611800", "v2"},
		{"https://arxiv.org/abs/cs.SE/0601001", "cs/0601001", ""},
		{"10.48550/arXiv.2404.06654", "2404.06654", ""},
		{"https://doi.org/10.48550/arXiv.2404.06654v3", "2404.06654", "v3"},
		{"doi: 10.48550/arXiv.2404.06654v3", "2404.06654", "v3"},
		{"doi.org/10.48550/arXiv.hep-th/9901001v2", "hep-th/9901001", "v2"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.BaseID != tt.base || got.Version != tt.version {
				t.Fatalf("Parse = %#v; want %s %s", got, tt.base, tt.version)
			}
			if got.ID() != tt.base+tt.version {
				t.Fatalf("ID = %q", got.ID())
			}
			if got.AbsURL() != "https://arxiv.org/abs/"+tt.base+tt.version {
				t.Fatalf("AbsURL = %q", got.AbsURL())
			}
			if got.PDFURL() != "https://arxiv.org/pdf/"+tt.base+tt.version {
				t.Fatalf("PDFURL = %q", got.PDFURL())
			}
		})
	}
}

func TestParseRejectsMalformedAndUntrustedInputs(t *testing.T) {
	for _, input := range []string{
		"", "See arXiv:2404.06654", "2404.06654 extra", "2404.06654v0", "2404.06654v01",
		"2400.06654", "2413.06654", "2404.00000", "2404.6654", "1412.07878", "0703.0001",
		"hep-th/9001001", "hep-th/0704001", "hep-th/9901000", "hep-th/9913001", "math.CAA/0611800",
		"https://example.org/pdf/2404.06654", "https://arxiv.org.evil.test/pdf/2404.06654",
		"https://arxiv.org@evil.test/pdf/2404.06654", "https://user@arxiv.org/pdf/2404.06654",
		"ftp://arxiv.org/pdf/2404.06654", "https://arxiv.org:1234/abs/2404.06654", "http://127.0.0.1/abs/2404.06654",
		"https://arxiv.org/search/2404.06654", "https://arxiv.org/pdf/2404.06654/more", "https://arxiv.org/abs/../2404.06654",
		"[https://arxiv.org/abs/2404.06654](https://evil.test/paper)", "https://doi.org/10.1000/2404.06654",
		"https://doi.org.evil.test/10.48550/arXiv.2404.06654", "2404.06654?x=1", "2404.06654\n2404.06655",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatal("expected invalid input error")
			} else {
				requireCode(t, err, "invalid_input")
			}
		})
	}
}

func TestParseCopiedCategoryLabelsAndUnicodeSpaces(t *testing.T) {
	for _, input := range []string{
		"arXiv:2404.06654v3 [cs.CL]", "2404.06654v3 [cs.CL]",
		"arXiv:\u00a02404.06654v3", "arXiv\u00a0:\u00a02404.06654v3\u00a0[cs.CL]",
		"2404.06654v3 [math.GT]", "2404.06654v3 [physics.optics]",
		"2404.06654v3 [cond-mat.supr-con]", "2404.06654v3 [hep-th]",
		"2404.06654v3 [quant-ph]", "2404.06654v3 [stat.ML]",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := Parse(input)
			if err != nil || got.BaseID != "2404.06654" || got.Version != "v3" {
				t.Fatalf("copied label must retain exact identifier: %#v, %v", got, err)
			}
		})
	}
	got, err := Parse("arXiv:math.CA/0611800v2 [math.CA]")
	if err != nil || got.ID() != "math/0611800v2" {
		t.Fatalf("legacy label: %#v, %v", got, err)
	}
}

func TestParseOfficialURLsWithTrailingSlash(t *testing.T) {
	for _, input := range []string{
		"https://arxiv.org/abs/2404.06654v3/",
		"https://arxiv.org/pdf/2404.06654v3.pdf/?download=1#page=2",
		"www.arxiv.org/html/2404.06654v3/", "https://export.arxiv.org/src/2404.06654v3/",
	} {
		got, err := Parse(input)
		if err != nil || got.ID() != "2404.06654v3" {
			t.Errorf("Parse(%q) = %#v, %v", input, got, err)
		}
	}
}

func TestParseRejectsProseOrInvalidCategorySuffixes(t *testing.T) {
	for _, input := range []string{
		"arXiv:2404.06654v3 [anything]", "2404.06654v3 [cs.ZZ]",
		"2404.06654v3 [cs.CL extra]", "2404.06654v3 [cs.CL,cs.LG]",
		"2404.06654v3 [cs.CL] see paper", "see 2404.06654v3 [cs.CL]",
		"2404.06654v3 [cs.CL][cs.LG]", "2404.06654v3 [https://evil.test]",
		"https://evil.test/pdf/2404.06654v3/ [cs.CL]", "https://arxiv.org.evil.test/pdf/2404.06654v3/",
		"https://arxiv.org/abs/2404.06654v3//", "2404.06654v3/", "arXiv:\u200b2404.06654v3",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatal("invalid copied text accepted")
			}
		})
	}
}
