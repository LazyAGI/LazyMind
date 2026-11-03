package doc

import (
	"encoding/json"
	"errors"
	"lazymind/core/algo"
	"lazymind/core/pdfir"
	"math"
	"strings"
)

const paperReadingMaxBytes = 1 << 20
const paperReadingPromptVersion = algo.VisualExplanationTaskVersion

type paperVisualExplanation struct {
	SourceSHA256 string            `json:"source_sha256"`
	Summary      string            `json:"summary"`
	Observations []formulaStep     `json:"observations"`
	Evidence     []formulaEvidence `json:"evidence"`
	Model        string            `json:"model"`
	Warnings     []string          `json:"warnings"`
}

func paperBlockText(b pdfir.Block) string {
	if strings.TrimSpace(b.Text) != "" {
		return b.Text
	}
	if b.Latex != "" {
		return b.Latex
	}
	var pieces []string
	if b.Table != nil {
		pieces = append(pieces, b.Table.Captions...)
		for _, row := range b.Table.Rows {
			var cells []string
			for _, cell := range row.Cells {
				cells = append(cells, cell.Text)
			}
			pieces = append(pieces, strings.Join(cells, " | "))
		}
		pieces = append(pieces, b.Table.Footnotes...)
	}
	if b.Image != nil {
		pieces = append(pieces, b.Image.Captions...)
		pieces = append(pieces, b.Image.Footnotes...)
	}
	return strings.Join(pieces, "\n")
}
func paperEvidence(b pdfir.Block, limit int) formulaEvidence {
	text := []rune(paperBlockText(b))
	if len(text) > limit {
		text = text[:limit]
	}
	e := formulaEvidence{ID: b.ID, Text: string(text), Page: b.Page}
	valid := len(b.BBox) == 4
	for _, v := range b.BBox {
		valid = valid && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
	}
	if valid && b.BBox[0] < b.BBox[2] && b.BBox[1] < b.BBox[3] {
		e.BBox = append([]float64(nil), b.BBox...)
	}
	return e
}

func paperVisualContext(d pdfir.Document, id string) (pdfir.Block, []formulaEvidence, error) {
	index := -1
	byID := map[string]pdfir.Block{}
	for i, b := range d.Blocks {
		byID[b.ID] = b
		if b.ID == id && (b.Type == "image" || b.Type == "table") {
			index = i
		}
	}
	if index < 0 {
		return pdfir.Block{}, nil, errors.New("Resource not found")
	}
	target := d.Blocks[index]
	chosen := []pdfir.Block{target}
	var refs []pdfir.TableRelation
	if target.Table != nil {
		refs = target.Table.RelatedBlocks
	}
	if target.Image != nil {
		refs = target.Image.RelatedBlocks
	}
	for _, ref := range refs {
		if b, ok := byID[ref.BlockID]; ok {
			chosen = append(chosen, b)
		}
	}
	for delta := 1; delta <= 3; delta++ {
		for _, i := range []int{index - delta, index + delta} {
			if i >= 0 && i < len(d.Blocks) && d.Blocks[i].Page == target.Page && d.Blocks[i].Type != "image" && d.Blocks[i].Type != "table" {
				chosen = append(chosen, d.Blocks[i])
			}
		}
	}
	ev := []formulaEvidence{}
	seen := map[string]bool{}
	budget := 24000
	for _, b := range chosen {
		if seen[b.ID] || b.ID == "" || b.Page < 0 || strings.TrimSpace(paperBlockText(b)) == "" {
			continue
		}
		seen[b.ID] = true
		if len(ev) >= 32 || budget <= 0 {
			break
		}
		e := paperEvidence(b, min(6000, budget))
		budget -= len([]rune(e.Text))
		ev = append(ev, e)
	}
	return target, ev, nil
}
func paperCleanIDs(ids []string, allowed map[string]bool) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if allowed[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}
func paperUnavailable(language string) string {
	if language == "zh" {
		return "所提供的原文摘录中缺少可靠依据，暂无法解释此项。"
	}
	return "Reliable evidence is unavailable in the supplied source excerpts for this item."
}
func paperDecode(raw string, v any) error {
	if len(raw) > paperReadingMaxBytes {
		return errors.New("explanation too large")
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```json") {
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "```json"), "```"))
	}
	if json.Unmarshal([]byte(raw), v) != nil {
		return errors.New("invalid explanation")
	}
	return nil
}
func validatePaperVisual(raw string, ev []formulaEvidence, language string) (paperVisualExplanation, error) {
	var result paperVisualExplanation
	if err := paperDecode(raw, &result); err != nil {
		return result, err
	}
	if strings.TrimSpace(result.Summary) == "" || len(result.Summary) > 16000 || len(result.Observations) > 24 {
		return paperVisualExplanation{}, errors.New("invalid explanation")
	}
	ids := map[string]bool{}
	for _, e := range ev {
		ids[e.ID] = true
	}
	for i := range result.Observations {
		o := &result.Observations[i]
		if strings.TrimSpace(o.Text) == "" || len(o.Text) > 12000 {
			return paperVisualExplanation{}, errors.New("invalid explanation")
		}
		o.EvidenceIDs = paperCleanIDs(o.EvidenceIDs, ids)
		o.Inferred = true
		if len(o.EvidenceIDs) == 0 {
			o.Text = paperUnavailable(language)
		}
	}
	if result.Observations == nil {
		result.Observations = []formulaStep{}
	}
	result.Evidence = ev
	result.Model = ""
	result.Warnings = paperReadingWarnings(language, true)
	return result, nil
}
func paperReadingWarnings(language string, visual bool) []string {
	if language == "zh" {
		out := []string{"AI 生成的解释可能有误，所有解读均标为推断。请核对原文；引用只表示相关上下文，不代表结论已被验证。", "上下文经过长度限制与跨页取样，可能省略部分细节；缺少证据不代表论文没有讨论此项。"}
		if visual {
			out = append(out, "这是基于提取的图注、表格单元格和相关正文的语境解释；模型没有查看图片像素，不能验证颜色、曲线或图中视觉细节。")
		}
		return out
	}
	out := []string{"AI-generated interpretations may be incorrect and are marked inferred. Verify the original evidence; citations identify context, not proof.", "Context is bounded and sampled across pages. Missing evidence does not mean the paper omits the topic."}
	if visual {
		out = append(out, "Contextual explanation from extracted captions, table cells and related text only. No image pixels were supplied; visual details, colors and curves cannot be verified.")
	}
	return out
}
