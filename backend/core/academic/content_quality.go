package academic

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"strings"
	"unicode"
)

type contentQuality struct {
	Acquisition contentAcquisition `json:"acquisition"`
	Downloaded  bool               `json:"downloaded"`
	Parsed      bool               `json:"parsed"`
	Identity    string             `json:"identity"`
	Material    string             `json:"material"`
	Text        string             `json:"text"`
	Eligible    bool               `json:"eligible"`
	Reason      string             `json:"reason,omitempty"`
}

func checkPaperQuality(paper PaperRef, pages []ContentPage, kind, hash string) contentQuality {
	q := contentQuality{Downloaded: hash != "", Parsed: len(pages) > 0, Identity: "unconfirmed", Material: "unknown", Text: "suspect"}
	var text strings.Builder
	for _, page := range pages {
		text.WriteString(page.Text)
		text.WriteByte('\n')
	}
	body := text.String()
	runes := []rune(body)
	head := runes
	if len(head) > 10000 {
		head = head[:10000]
	}
	opening := strings.ToLower(string(head))
	prefix := []rune(opening)
	if len(prefix) > 600 {
		prefix = prefix[:600]
	}
	nonspace, readable, replacement := 0, 0, 0
	for _, r := range runes {
		if !unicode.IsSpace(r) {
			nonspace++
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			readable++
		}
		if r == '\ufffd' || (unicode.IsControl(r) && !unicode.IsSpace(r)) {
			replacement++
		}
	}
	if readable < 100 || nonspace == 0 || float64(readable)/float64(nonspace) < 0.35 || float64(replacement)/float64(nonspace) > 0.02 {
		q.Reason = "解析文字过少或存在明显乱码，需重试或核对原文"
		return q
	}
	q.Text = "usable"
	for _, line := range strings.Split(string(prefix), "\n") {
		title := NormalizeTitle(line)
		for _, marker := range []string{"supplementary material", "supplemental material", "supporting information", "training slides", "lecture slides", "course materials", "workshop tutorial"} {
			if strings.HasPrefix(title, marker) {
				q.Material = "other"
				q.Reason = "材料可能是补充文件或幻灯片，不能作为论文正文"
				return q
			}
		}
	}

	front := []rune(opening)
	if len(front) > 2500 {
		front = front[:2500]
	}
	doi, arxiv, _ := extractStrongIDs(string(front))
	expectedArxiv, _ := NormalizeArxivID(paper.ArxivID)
	expectedDOI := NormalizeDOI(paper.DOI)
	// PDF line wrapping may split a DOI after its journal prefix. Such a
	// partial identifier cannot prove either a match or a conflict.
	doiConflict := expectedDOI != "" && doi != "" && doi != expectedDOI && !strings.HasPrefix(expectedDOI, doi)
	if expectedArxiv != "" && arxiv != "" && arxiv != expectedArxiv || doiConflict && (arxiv == "" || arxiv != expectedArxiv) {
		q.Identity = "mismatch"
		q.Reason = "正文标识与目标论文冲突，不能作为该论文正文"
		return q
	}
	// Match the document opening, not identifiers found in its bibliography.
	title := NormalizeTitle(paper.Title)
	normalized := NormalizeTitle(string(front))
	// Keep title order: a bag of common topic words in an unrelated abstract
	// cannot verify paper identity. Joining whitespace tolerates PDF line wraps.
	matched := len([]rune(title)) >= 8 && strings.Contains(strings.ReplaceAll(normalized, " ", ""), strings.ReplaceAll(title, " ", ""))
	identifierOpening := strings.ReplaceAll(string(front), " ", "")
	if expectedDOI != "" && strings.Contains(identifierOpening, expectedDOI) {
		matched = true
	}
	if expectedArxiv != "" && strings.Contains(identifierOpening, expectedArxiv) {
		matched = true
	}
	if !matched {
		q.Reason = "正文开头与目标论文题录尚未匹配，需核对论文身份"
		return q
	}
	q.Identity = "matched"
	q.Material = "main"
	q.Eligible = true
	return q
}
func usablePaperContent(content orm.AcademicPaperContent) bool {
	var q contentQuality
	return content.Status == "ready" && content.ContentHash != "" && len(content.PagesJSON) > 2 && json.Unmarshal(content.QualityJSON, &q) == nil && q.Eligible && q.Identity == "matched" && q.Material == "main" && q.Text == "usable"
}
