package paperguide

import (
	"encoding/json"
	"os"
	"testing"

	"lazymind/core/pdfir"
)

func fixture(t *testing.T) (pdfir.Document, SourceIdentity) {
	t.Helper()
	raw, err := os.ReadFile("testdata/source.json")
	if err != nil {
		t.Fatal(err)
	}
	var d pdfir.Document
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d, SourceIdentity{SourceSHA256: d.SourceSHA256, DatasetID: "ds", DocumentID: "doc", OwnerID: "owner"}
}

func TestSourceSnapshotIdentityAndGeometry(t *testing.T) {
	d, identity := fixture(t)
	s, err := BuildSourceSnapshot(d, identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sections) != 2 || len(s.Assets) != 3 || s.Evidence[0].Locator.Kind != "page" || s.Evidence[0].Locator.Page != 0 || len(s.Evidence[0].Locator.BBox) != 0 {
		t.Fatalf("unexpected sources: %+v", s)
	}
	if s.Assets[0].RelatedBlocks[0].BlockID != "cap" {
		t.Fatal("lost caption relation")
	}
	foreign := identity
	foreign.OwnerID = "someone-else"
	other, err := BuildSourceSnapshot(d, foreign)
	if err != nil || s.Evidence[0].ID == other.Evidence[0].ID {
		t.Fatal("scope did not isolate IDs")
	}
	d.Blocks[0].Text += " changed"
	other, err = BuildSourceSnapshot(d, identity)
	if err != nil || s.IRDigest == other.IRDigest || s.Evidence[0].ID == other.Evidence[0].ID {
		t.Fatal("IR change did not invalidate IDs")
	}
	identity.SourceSHA256 = pdfir.Digest("wrong")
	if _, err := BuildSourceSnapshot(d, identity); err == nil {
		t.Fatal("wrong file hash accepted")
	}
	identity.SourceSHA256 = d.SourceSHA256
	d.Blocks[0].Page = 10
	if _, err := BuildSourceSnapshot(d, identity); err == nil {
		t.Fatal("invalid page accepted")
	}
}

func TestTextMatchingUsesRuneOffsetsAndPreservesMeaning(t *testing.T) {
	text := "Every sample was re-\nweighed at −2 °C. Repeat. Repeat."
	start, end, err := MatchTextSpan(text, "Every sample was re- weighed at −2 °C.")
	if err != nil || string([]rune(text)[start:end]) != "Every sample was re-\nweighed at −2 °C." {
		t.Fatalf("offsets %d:%d (%v)", start, end, err)
	}
	for _, quote := range []string{"Repeat.", "Every sample was reweighed at 2 °C.", "Some sample was re- weighed at −2 °C."} {
		if _, _, err := MatchTextSpan(text, quote); err == nil {
			t.Fatalf("accepted %q", quote)
		}
	}
}

func TestAbstractOnlyAndMathematicalMinus(t *testing.T) {
	d, identity := fixture(t)
	d.Blocks = []pdfir.Block{{ID: "title", Type: "title", Text: "A paper about measurement", Page: 0},
		{ID: "abstract", Type: "text", Text: "Abstract: A single synthetic observation.", Page: 0}}
	s, err := BuildSourceSnapshot(d, identity)
	if err != nil || !s.Coverage.AbstractOnly || s.Coverage.Body {
		t.Fatalf("abstract-only coverage: %+v (%v)", s.Coverage, err)
	}
	if _, _, err := MatchTextSpan("Formula x-\ny, for all samples.", "Formula xy, for all samples."); err == nil {
		t.Fatal("line break normalization removed a mathematical minus")
	}
}

func TestHistoricalResolversUseCanonicalSource(t *testing.T) {
	d, identity := fixture(t)
	d.Blocks[5].BBox = []float64{0, 0, 1, 1}
	s, err := buildSourceSnapshot(d, identity, "paper-guide-source-v4")
	if err != nil {
		t.Fatal(err)
	}
	span, err := ResolveTextLocator(s, s.Evidence[1].ID, "re- weighed at −2 °C.")
	if err != nil || span.Kind != "text_span" || span.Start != 17 || span.End != len([]rune(d.Blocks[1].Text)) {
		t.Fatalf("incorrect span: %+v, %v", span, err)
	}
	page, err := ResolveTextLocator(s, s.Evidence[0].ID, "Repeat.")
	if err != nil || page.Kind != "page" || page.Page != 0 {
		t.Fatalf("expected honest page fallback: %+v (%v)", page, err)
	}
	if _, err := ResolveTextLocator(s, s.Evidence[0].ID, "some other paper"); err == nil {
		t.Fatal("fabricated quote accepted")
	}
	cells, err := ResolveTableLocator(s, s.Evidence[5].ID, []GuideCell{{Row: 1, Column: 1}})
	if err != nil || len(cells.Headers) != 1 || cells.Headers[0] != "Temperature (°C)" || len(cells.Cells) != 1 {
		t.Fatalf("merged header was not associated with both data columns: %+v (%v)", cells, err)
	}
	if _, err := ResolveTableLocator(s, s.Evidence[5].ID, []GuideCell{{Row: 0, Column: 1}}); err == nil {
		t.Fatal("accepted nonexistent cell origin")
	}
}

func TestPreserveAmbiguousHyphensAndExplicitSoftHyphen(t *testing.T) {
	for _, pair := range [][2]string{{"Compare mu-\nsigma.", "Compare musigma."}, {"re-\nweighed", "reweighed"}} {
		if _, _, err := MatchTextSpan(pair[0], pair[1]); err == nil {
			t.Fatalf("fabricated match %q", pair[1])
		}
	}
	s := "−2 °C: re\u00ad\nweighed."
	start, end, err := MatchTextSpan(s, "reweighed")
	if err != nil || string([]rune(s)[start:end]) != "re\u00ad\nweighed" {
		t.Fatalf("soft hyphen source span %d:%d: %v", start, end, err)
	}
}

func TestLegacySnapshotRemainsReadableAndCatalogIdentityChanges(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-snapshot-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var legacy GuideSourceSnapshot
	if err = json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	if err = ValidateSnapshot(legacy, legacy.Identity, legacy.IRDigest); err != nil {
		t.Fatalf("legacy history unreadable: %v", err)
	}
	d, id := fixture(t)
	current, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if current.IRDigest != legacy.IRDigest || current.Evidence[0].ID == legacy.Evidence[0].ID {
		t.Fatal("catalog version failed to isolate identity or changed original IR")
	}
	var fields map[string]any
	raw, _ = json.Marshal(current)
	json.Unmarshal(raw, &fields)
	if fields["catalog_version"] == nil {
		t.Fatal("snapshot has no catalog version")
	}
	fields["catalog_version"] = "unknown"
	raw, _ = json.Marshal(fields)
	json.Unmarshal(raw, &current)
	if ValidateSnapshot(current, id, current.IRDigest) == nil {
		t.Fatal("unknown catalog accepted")
	}
}
