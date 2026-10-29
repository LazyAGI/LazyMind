package pdfir

import (
	"strings"
	"testing"
)

func TestImageCaptionsRemainSafeAndLinkExplicitMentions(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"image","page_idx":0,"bbox":[100,100,900,500],"image_caption":["Figure 1. <b>Architecture</b><script>bad()</script>"],"image_footnote":["A note"]},{"type":"text","page_idx":0,"text":"Figure 1 illustrates the method."}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	b := d.Blocks[0]
	if b.Image == nil || b.Image.Number != "1" || len(b.Image.RelatedBlocks) != 1 || b.Image.RelatedBlocks[0].BlockID != d.Blocks[1].ID || strings.Contains(b.Image.Captions[0], "bad") {
		t.Fatalf("bad image projection: %+v", b.Image)
	}
	if b.ID != Digest("s:" + Revision + ":vlm:0")[:24] {
		t.Fatal("source ID changed")
	}
}
func TestImageProjectionDoesNotAssignMultipleFigureNumbers(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"image","page_idx":0,"image_caption":["Figure 1. First","Figure 2. Second"]},{"type":"text","page_idx":0,"text":"Figure 1 and Figure 2."}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	if d.Blocks[0].Image.Number != "" || len(d.Blocks[0].Image.Captions) != 0 || len(d.Blocks[0].Image.RelatedBlocks) != 0 {
		t.Fatal("ambiguous figure labels were assigned")
	}
}
