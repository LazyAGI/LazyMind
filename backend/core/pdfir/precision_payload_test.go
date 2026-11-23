package pdfir

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func precisionPayloadArchive(t *testing.T, items, layout string) ([]byte, Document) {
	t.Helper()
	var l map[string]any
	if e := json.Unmarshal([]byte(layout), &l); e != nil {
		t.Fatal(e)
	}
	l["_version_name"] = "3.4.4"
	l["_backend"] = "hybrid"
	data, _ := json.Marshal(l)
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	f, _ := z.Create("paper_content_list.json")
	f.Write([]byte(items))
	f, _ = z.Create("layout.json")
	f.Write(data)
	z.Close()
	base, e := AdaptZIP(raw.Bytes(), strings.Repeat("a", 64), "vlm")
	if e != nil {
		t.Fatal(e)
	}
	return raw.Bytes(), base
}

const payloadTextLayout = `{"pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[{"type":"text","lines":[{"spans":[{"type":"text","content":"Home.","bbox":[10,10,90,20]}]}]}],"para_blocks":[{"type":"text","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"text","content":"Home.","bbox":[10,10,90,20]}]}]}]},{"page_idx":1,"page_size":[100,100],"preproc_blocks":[{"type":"text","lines":[{"spans":[{"type":"text","content":"Next page secret.","bbox":[10,10,90,20]}]}]}],"para_blocks":[]}]}`
const payloadTableLayout = `{"pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[{"type":"table_body","bbox":[10,10,90,80],"lines":[{"spans":[{"type":"table","html":"<table><tr><td>home</td></tr></table>","bbox":[10,10,90,80]}]}]}],"para_blocks":[{"type":"table","bbox":[10,10,90,80],"blocks":[{"type":"table_body","bbox":[10,10,90,80],"lines":[{"spans":[{"type":"table","html":"<table><tr><td>home</td></tr></table>","bbox":[10,10,90,80]}]}]}]}]}]}`

func TestPrecisionRejectsIncompleteCanonicalPayload(t *testing.T) {
	cases := []struct{ name, items, layout string }{
		{"missing continuation", `[{"type":"text","text":"Home. Next page secret.","page_idx":0,"bbox":[100,100,900,200]}]`, payloadTextLayout},
		{"canonical extra table row", `[{"type":"table","table_body":"<table><tr><td>home</td></tr><tr><td>next page</td></tr></table>","page_idx":0,"bbox":[100,100,900,800]}]`, payloadTableLayout},
		{"canonical missing table row", `[{"type":"table","table_body":"<table></table>","page_idx":0,"bbox":[100,100,900,800]}]`, payloadTableLayout},
		{"unknown table footnote", `[{"type":"table","table_body":"<table><tr><td>home</td></tr></table>","table_footnote":["Unknown next page fact"],"page_idx":0,"bbox":[100,100,900,800]}]`, payloadTableLayout},
		{"unknown table caption", `[{"type":"table","table_body":"<table><tr><td>home</td></tr></table>","table_caption":["Table 1: Unknown next page fact"],"page_idx":0,"bbox":[100,100,900,800]}]`, payloadTableLayout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, base := precisionPayloadArchive(t, c.items, c.layout)
			if _, err := ProjectSourcePrecision(base, raw); err == nil {
				t.Fatal("incomplete canonical payload authenticated")
			}
		})
	}
}
func TestPrecisionRejectsMissingTextEvenWhenOtherCrossPageSpanExists(t *testing.T) {
	raw, base := precisionFixture(t, func(l map[string]any) {
		p := l["pdf_info"].([]any)[0].(map[string]any)
		para := p["para_blocks"].([]any)[0].(map[string]any)
		lines := para["lines"].([]any)
		spans := lines[1].(map[string]any)["spans"].([]any)
		lines[1].(map[string]any)["spans"] = spans[1:]
	})
	if _, err := ProjectSourcePrecision(base, raw); err == nil {
		t.Fatal("region replacement silently dropped canonical continuation")
	}
}
func TestPrecisionRejectsTamperedCanonicalTableProjection(t *testing.T) {
	raw, base := precisionPayloadArchive(t, `[{"type":"table","table_body":"<table><tr><td>home</td></tr></table>","page_idx":0,"bbox":[100,100,900,800]}]`, payloadTableLayout)
	base.Blocks[0].Table.Rows[0].Cells[0].Text = "foreign next page"
	if _, err := ProjectSourcePrecision(base, raw); err == nil {
		t.Fatal("actual consumed table rows were not bound to verified archive")
	}
}

func TestPrecisionRejectsUnboundFormulaAndImagePayloads(t *testing.T) {
	const formulaLayout = `{"pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[{"type":"interline_equation","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"interline_equation","content":"x=1","bbox":[10,10,90,20]}]}]}],"para_blocks":[{"type":"interline_equation","bbox":[10,10,90,20],"lines":[{"spans":[{"type":"interline_equation","content":"x=1","bbox":[10,10,90,20]}]}]}]}]}`
	const imageLayout = `{"pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[],"para_blocks":[{"type":"image","bbox":[10,10,90,20],"blocks":[]}]}]}`
	for _, c := range []struct{ name, items, layout string }{
		{"formula", `[{"type":"equation","text":"$$\nx=2\n$$","page_idx":0,"bbox":[100,100,900,200]}]`, formulaLayout},
		{"formula latex override", `[{"type":"equation","text":"$$\nx=1\n$$","latex":"foreign","page_idx":0,"bbox":[100,100,900,200]}]`, formulaLayout},
		{"image caption", `[{"type":"image","image_caption":["Figure 1: Unknown result"],"page_idx":0,"bbox":[100,100,900,200]}]`, imageLayout},
		{"image footnote", `[{"type":"image","image_footnote":["Unknown result"],"page_idx":0,"bbox":[100,100,900,200]}]`, imageLayout},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, base := precisionPayloadArchive(t, c.items, c.layout)
			if _, err := ProjectSourcePrecision(base, raw); err == nil {
				t.Fatal("unbound asset text authenticated")
			}
		})
	}
}

// Literal fixtures for the supported upstream render rules. These expected
// values are independent of the verifier and never generated with its renderer.
func TestPrecisionProducerRenderContract(t *testing.T) {
	for _, c := range []struct {
		name, para, want string
		cjk              bool
	}{
		{"western breaks", `{"type":"text","lines":[{"spans":[{"type":"text","content":"  Alpha  "}]},{"spans":[{"type":"text","content":"Beta"}]}]}`, "Alpha Beta", false},
		{"cjk breaks", `{"type":"text","lines":[{"spans":[{"type":"text","content":"第一行"}]},{"spans":[{"type":"text","content":"第二行"}]}]}`, "第一行第二行", true},
		{"cjk math", `{"type":"text","lines":[{"spans":[{"type":"text","content":"值"},{"type":"inline_equation","content":"x_i"}]},{"spans":[{"type":"text","content":"成立"}]}]}`, "值 $x_i$ 成立", true},
		{"fullwidth only alphanumeric", `{"type":"text","lines":[{"spans":[{"type":"text","content":"Ａｂ１２，*x*_y` + "`" + `~$"}]}]}`, "Ab12，\\*x\\*\\_y\\`\\~\\$", false},
		{"preserve existing escapes", `{"type":"text","lines":[{"spans":[{"type":"text","content":"\\* \\\\*"}]}]}`, `\* \\\*`, false},
		{"lowercase dehyphen", `{"type":"text","lines":[{"spans":[{"type":"text","content":"hyphen‐"}]},{"spans":[{"type":"text","content":"ａｔｅｄ"}]}]}`, "hyphenated", false},
		{"uppercase retain hyphen", `{"type":"text","lines":[{"spans":[{"type":"text","content":"mid-"}]},{"spans":[{"type":"text","content":"Upper"}]}]}`, "mid-Upper", false},
		{"final hyphen", `{"type":"text","lines":[{"spans":[{"type":"text","content":"word-"}]}]}`, "word-", false},
		{"ordinary dash", `{"type":"text","lines":[{"spans":[{"type":"text","content":"word—"}]},{"spans":[{"type":"text","content":"rest"}]}]}`, "word— rest", false},
		{"body prefix", `{"type":"text","lines":[{"spans":[{"type":"text","content":"## Title"}]}]}`, `\## Title`, false},
		{"title prefix", `{"type":"title","lines":[{"spans":[{"type":"text","content":"## Title"}]}]}`, "## Title", false},
		{"display math", `{"type":"interline_equation","lines":[{"spans":[{"type":"interline_equation","content":"x_{i}^2"}]}]}`, "$$\nx_{i}^2\n$$", false},
		{"duplicates preserved", `{"type":"text","lines":[{"spans":[{"type":"text","content":"same"},{"type":"text","content":"same"}]}]}`, "same same", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var para precisionBlock
			if e := json.Unmarshal([]byte(c.para), &para); e != nil {
				t.Fatal(e)
			}
			got, e := precisionRender(para, c.cjk)
			if e != nil || got != c.want {
				t.Fatalf("render = %q, %v; want %q", got, e, c.want)
			}
			if !precisionRenderMatches(c.want, para) || precisionRenderMatches(c.want+" unknown", para) {
				t.Fatal("whole-payload equality violated")
			}
		})
	}
}

func TestPrecisionProducerHTMLContract(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{`<table><tr><td>same</td></tr></table>`, `<table><tr><td>same</td></tr></table>`},
		{`<table><tr><td><eq>x_{i}&lt;y &amp; z</eq></td></tr></table>`, `<table><tr><td> $x_{i}<y & z$ </td></tr></table>`},
	} {
		got, e := precisionFormattedTable(c.raw)
		if e != nil || got != c.want {
			t.Fatalf("HTML = %q %v", got, e)
		}
	}
	if _, e := precisionFormattedTable(`<table><img src="unknown/file.png"></table>`); e == nil {
		t.Fatal("unbound media prefix accepted")
	}
}

func TestPrecisionRejectsUnknownProducerContract(t *testing.T) {
	for _, kind := range []string{"_backend", "_version_name"} {
		raw, base := precisionFixture(t, func(l map[string]any) { l[kind] = "unknown" })
		if _, e := ProjectSourcePrecision(base, raw); e == nil || !strings.Contains(e.Error(), "unsupported producer contract") {
			t.Fatalf("unknown producer accepted: %v", e)
		}
	}
}

// Caption blocks may be attached by the adapter's layout projection even when
// they are not listed as nested content-list caption fields.
func TestPrecisionRejectsUnknownTypedProjectedCaption(t *testing.T) {
	layout := `{"pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[],"para_blocks":[{"type":"image","bbox":[10,10,90,80],"blocks":[]},{"type":"image_caption","bbox":[10,2,90,8],"lines":[{"spans":[{"type":"unknown","content":"Figure 1: unverified","bbox":[10,2,90,8]}]}]}]}]}`
	raw, base := precisionPayloadArchive(t, `[{"type":"image","page_idx":0,"bbox":[100,100,900,800]}]`, layout)
	if len(base.Blocks[0].Image.Captions) != 1 {
		t.Fatal("fixture lacks actual consumed caption")
	}
	if _, e := ProjectSourcePrecision(base, raw); e == nil {
		t.Fatal("unknown typed layout caption authenticated")
	}
}
