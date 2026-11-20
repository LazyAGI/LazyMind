package readingpolicy

type Capability struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Dependencies   []string `json:"dependencies"`
	SupportedModes []string `json:"supported_modes"`
	Triggers       []string `json:"triggers"`
	Granularity    string   `json:"granularity"`
	CostUnit       string   `json:"cost_unit"`
	ResultVersion  string   `json:"result_version"`
}

// Registry is the server-owned list. Unknown future capabilities are never
// automatically enabled by existing presets.
func Registry() []Capability {
	return []Capability{
		{PaperGuide, "整篇论文导读", []string{PDFParse}, []string{OnDemand, Disabled}, []string{"explicit"}, "document", "model_request", "paper-guide-v1"},
		{PDFParse, "结构化 PDF 解析", []string{}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "future_document"}, "document", "paper", "mineru-vlm-ir-v1"},
		{AIStudy, "AI 论文精读", []string{PDFParse}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "future_document"}, "document", "paper", "paper-reading-v2-grounded-findings"},
	}
}
func configurableKeys() []string {
	keys := []string{}
	for _, c := range Registry() {
		if len(c.SupportedModes) > 0 {
			keys = append(keys, c.ID)
		}
	}
	return keys
}
