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
		{PDFParse, "结构化 PDF 解析", []string{}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "future_document"}, "document", "parser_attempt", "mineru-vlm-ir-v1"},
		{ChatPrepare, "聊天文档准备", []string{}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "chat", "future_document"}, "document", "document_parse", "reader-artifact-v1"},
		{Formula, "公式研读", []string{PDFParse}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "future_document"}, "block", "model_request", "formula-explanation-v7-low-latency"},
		{Figure, "图片研读", []string{PDFParse}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "future_document"}, "block", "model_request", "paper-reading-v2-grounded-findings"},
		{Table, "表格研读", []string{PDFParse}, []string{OnDemand, Automatic, Disabled}, []string{"explicit", "future_document"}, "block", "model_request", "paper-reading-v2-grounded-findings"},
		{"overview", "论文速览", []string{PDFParse}, []string{}, []string{}, "document", "unavailable", "unavailable"},
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
