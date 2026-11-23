package pdfir

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	mergeHomePrefix = `<table><tr><td rowspan="2">Group</td><td>Heading</td></tr><tr><td>Detail</td></tr>`
	mergeHomeSeam   = `<tr><td>north&nbsp;</td><td>micro</td></tr>`
	mergeLaterSeam  = `<tr><td>east</td><td>scope</td></tr>`
	mergeLaterTail  = `<tr><td>independent</td><td>22</td></tr>`
	mergeJoinedSeam = `<tr><td>north east</td><td>microscope</td></tr>`
)

type cellMergeFixture struct {
	homeHTML, laterHTML, canonicalHTML string
	laterPage                          int
	alter                              func(home, later, rawHome, rawLater map[string]any)
	alterPages                         func([]map[string]any)
}

func newCellMergeFixture() cellMergeFixture {
	return cellMergeFixture{
		homeHTML:      mergeHomePrefix + mergeHomeSeam + "</table>",
		laterHTML:     "<table>" + mergeLaterSeam + mergeLaterTail + "</table>",
		canonicalHTML: mergeHomePrefix + mergeJoinedSeam + mergeLaterTail + "</table>",
		laterPage:     1,
	}
}

func (f cellMergeFixture) archive(t *testing.T) ([]byte, Document) {
	t.Helper()
	box := []int{10, 20, 90, 80}
	makeBody := func(body string) map[string]any {
		return map[string]any{"type": "table_body", "bbox": box, "lines": []any{
			map[string]any{"spans": []any{map[string]any{"type": "table", "bbox": box, "html": body}}},
		}}
	}
	wrap := func(body map[string]any) map[string]any {
		return map[string]any{"type": "table", "bbox": box, "blocks": []any{body}}
	}
	deleted := map[string]any{"type": "table_body", "bbox": box, "lines_deleted": true, "lines": []any{}}
	home, later := wrap(makeBody(f.canonicalHTML)), wrap(deleted)
	rawHome, rawLater := wrap(makeBody(f.homeHTML)), wrap(makeBody(f.laterHTML))
	for _, b := range []map[string]any{later, deleted, rawLater, cellMergeBody(rawLater)} {
		b["cell_merge"] = []int{1, 1}
	}
	if f.alter != nil {
		f.alter(home, later, rawHome, rawLater)
	}
	page := func(i int, pre, para map[string]any) map[string]any {
		return map[string]any{"page_idx": i, "page_size": []int{100, 100}, "preproc_blocks": []any{pre}, "para_blocks": []any{para}}
	}
	item := func(i int, body string) map[string]any {
		return map[string]any{"type": "table", "page_idx": i, "bbox": []int{100, 200, 900, 800}, "table_body": body}
	}
	pages := []map[string]any{page(0, rawHome, home), page(f.laterPage, rawLater, later)}
	if f.alterPages != nil {
		f.alterPages(pages)
	}
	layout, _ := json.Marshal(map[string]any{"pdf_info": pages})
	items, _ := json.Marshal([]any{item(0, f.canonicalHTML), item(f.laterPage, "")})
	return precisionPayloadArchive(t, string(items), string(layout))
}

func cellMergeBody(table map[string]any) map[string]any {
	return table["blocks"].([]any)[0].(map[string]any)
}

func TestPrecisionCellMergeReplaysCanonicalAndPreservesPhysicalRows(t *testing.T) {
	f := newCellMergeFixture()
	raw, base := f.archive(t)
	projected, err := ProjectSourcePrecision(base, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.Blocks, projected.Blocks) {
		t.Fatal("canonical evidence changed")
	}
	rs := projected.Precision.Regions
	if len(rs) != 2 || rs[0].Page != 0 || rs[1].Page != 1 || rs[0].ParentBlockID != base.Blocks[0].ID || rs[1].ParentBlockID != rs[0].ParentBlockID {
		t.Fatalf("physical association: %+v", rs)
	}
	for i, want := range []string{f.homeHTML, f.laterHTML} {
		rows, text, err := ParseSourceTable(want)
		if err != nil || rs[i].Table == nil || rs[i].Table.HTML != want || rs[i].Text != text || !reflect.DeepEqual(rs[i].Table.Rows, rows) {
			t.Fatalf("physical source rows lost on page %d: %+v", i, rs[i])
		}
	}
	encoded, _ := json.Marshal(rs[1].Table)
	var proof struct {
		CellMerge []int `json:"cell_merge"`
	}
	if err := json.Unmarshal(encoded, &proof); err != nil || !reflect.DeepEqual(proof.CellMerge, []int{1, 1}) || rs[1].Table.ContinuationBlockID != base.Blocks[1].ID || rs[1].Table.OmittedPrefixRows != 0 {
		t.Fatalf("missing continuation proof: %s", encoded)
	}
	if err := ValidateTableFragments(base.Blocks[0], rs, base.Blocks); err != nil {
		t.Fatal(err)
	}
}

func TestPrecisionCellMergeRejectsUnprovenSeams(t *testing.T) {
	tests := []struct {
		name, reason string
		alter        func(*cellMergeFixture)
	}{
		{"nonadjacent pages", "adjacent table", func(f *cellMergeFixture) { f.laterPage++ }},
		{"reversed layout pages", "adjacent table", func(f *cellMergeFixture) {
			f.alterPages = func(pages []map[string]any) {
				pages[0], pages[1] = pages[1], pages[0]
			}
		}},
		{"home is not last block", "adjacent table", func(f *cellMergeFixture) {
			f.alterPages = func(pages []map[string]any) {
				pages[0]["para_blocks"] = append(pages[0]["para_blocks"].([]any), map[string]any{"type": "text"})
			}
		}},
		{"continuation is not first block", "adjacent table", func(f *cellMergeFixture) {
			f.alterPages = func(pages []map[string]any) {
				pages[1]["para_blocks"] = append([]any{map[string]any{"type": "text"}}, pages[1]["para_blocks"].([]any)...)
			}
		}},
		{"ambiguous raw ancestor", "ambiguous physical table ancestor", func(f *cellMergeFixture) {
			f.alterPages = func(pages []map[string]any) {
				raw := pages[1]["preproc_blocks"].([]any)
				pages[1]["preproc_blocks"] = append(raw, map[string]any{"type": "table", "bbox": raw[0].(map[string]any)["bbox"]})
			}
		}},
		{"raw body box does not match", "cell merge fragment geometry", func(f *cellMergeFixture) {
			f.alter = func(_, _, _, rawLater map[string]any) {
				cellMergeBody(rawLater)["bbox"] = []int{11, 20, 90, 80}
			}
		}},
		{"nonempty placeholder", "table HTML differs", func(f *cellMergeFixture) {
			f.alter = func(_, later, _, rawLater map[string]any) {
				cellMergeBody(later)["lines"] = cellMergeBody(rawLater)["lines"]
			}
		}},
		{"preproc marker without para", "cell merge markers", func(f *cellMergeFixture) {
			f.alter = func(_, later, _, _ map[string]any) {
				delete(later, "cell_merge")
				delete(cellMergeBody(later), "cell_merge")
			}
		}},
		{"parent and body disagree", "cell merge markers", func(f *cellMergeFixture) {
			f.alter = func(_, later, _, _ map[string]any) { delete(cellMergeBody(later), "cell_merge") }
		}},
		{"preproc and para disagree", "cell merge markers", func(f *cellMergeFixture) {
			f.alter = func(_, _, _, rawLater map[string]any) { rawLater["cell_merge"] = []int{0, 0} }
		}},
		{"mixed vector", "cell merge markers", changeCellMergeVector([]int{1, 0})},
		{"nonboolean vector", "cell merge markers", changeCellMergeVector([]int{1, 2})},
		{"short vector", "visual columns", changeCellMergeVector([]int{1})},
		{"long vector", "visual columns", changeCellMergeVector([]int{1, 1, 1})},
		{"over-limit vector", "cell merge markers", changeCellMergeVector(append([]int{1}, make([]int, 100)...))},
		{"span crosses seam", "incomplete table HTML", func(f *cellMergeFixture) {
			f.homeHTML = strings.Replace(f.homeHTML, `rowspan="2"`, `rowspan="4"`, 1)
		}},
		{"inherited seam slot", "unit cells", func(f *cellMergeFixture) {
			f.homeHTML = strings.Replace(f.homeHTML, `rowspan="2"`, `rowspan="3"`, 1)
			f.homeHTML = strings.Replace(f.homeHTML, "<td>north&nbsp;</td>", "", 1)
		}},
		{"seam colspan", "unit cells", func(f *cellMergeFixture) {
			f.laterHTML = strings.Replace(f.laterHTML, mergeLaterSeam, `<tr><td colspan="2">eastscope</td></tr>`, 1)
		}},
		{"seam markup", "plain-text seam", func(f *cellMergeFixture) {
			f.laterHTML = strings.Replace(f.laterHTML, ">east<", "><b>east</b><", 1)
		}},
		{"canonical seam markup", "plain-text seam", func(f *cellMergeFixture) {
			f.canonicalHTML = strings.Replace(f.canonicalHTML, ">north east<", "><b>north east</b><", 1)
		}},
		{"repeat header", "repeated header", func(f *cellMergeFixture) {
			f.homeHTML = strings.Replace(f.homeHTML, ">Group<", ">east<", 1)
			f.homeHTML = strings.Replace(f.homeHTML, ">Heading<", ">scope<", 1)
		}},
		{"drop second later row", "canonical cell merge", func(f *cellMergeFixture) {
			f.canonicalHTML = strings.Replace(f.canonicalHTML, mergeLaterTail, "", 1)
		}},
		{"unaccounted later cell content", "canonical cell merge", func(f *cellMergeFixture) {
			f.laterHTML = strings.Replace(f.laterHTML, ">east<", ">southeast<", 1)
		}},
		{"missing producer space", "canonical cell merge", func(f *cellMergeFixture) {
			f.canonicalHTML = strings.Replace(f.canonicalHTML, "north east", "northeast", 1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCellMergeFixture()
			tt.alter(&f)
			raw, base := f.archive(t)
			if _, err := ProjectSourcePrecision(base, raw); err == nil || !strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("want %q rejection, got %v", tt.reason, err)
			}
		})
	}
}

func changeCellMergeVector(vector []int) func(*cellMergeFixture) {
	return func(f *cellMergeFixture) {
		f.alter = func(_, later, _, rawLater map[string]any) {
			for _, b := range []map[string]any{later, cellMergeBody(later), rawLater, cellMergeBody(rawLater)} {
				b["cell_merge"] = vector
			}
		}
	}
}
