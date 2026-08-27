package showcase

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common"
	skillbuiltin "lazymind/core/skillv2/builtin"
)

// ShowcaseCaseStep is one user-visible step in a case replay.
type ShowcaseCaseStep struct {
	Title       string `yaml:"title" json:"title"`
	Description string `yaml:"description" json:"description"`
}

// ShowcaseCaseMetric is a configured metric card in a product report preview.
type ShowcaseCaseMetric struct {
	Label  string `yaml:"label" json:"label"`
	Value  string `yaml:"value" json:"value"`
	Hint   string `yaml:"hint" json:"hint"`
	Accent bool   `yaml:"accent,omitempty" json:"accent,omitempty"`
}

// ShowcaseCaseResultItem is one configured item in a product report section.
type ShowcaseCaseResultItem struct {
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	Description string `yaml:"description" json:"description"`
}

// ShowcaseCaseResultSection configures an existing product report list component.
type ShowcaseCaseResultSection struct {
	Title  string                   `yaml:"title" json:"title"`
	Marker string                   `yaml:"marker" json:"marker"`
	Items  []ShowcaseCaseResultItem `yaml:"items" json:"items"`
}

// ShowcaseCaseProductReport contains text slots for the existing product report component.
type ShowcaseCaseProductReport struct {
	Metrics      []ShowcaseCaseMetric        `yaml:"metrics" json:"metrics"`
	Sections     []ShowcaseCaseResultSection `yaml:"sections" json:"sections"`
	Deliverables string                      `yaml:"deliverables" json:"deliverables"`
}

// ShowcaseCaseResult is the single result model consumed by Showcase components.
type ShowcaseCaseResult struct {
	Template      string                     `yaml:"template" json:"template"`
	Eyebrow       string                     `yaml:"eyebrow" json:"eyebrow"`
	Title         string                     `yaml:"title" json:"title"`
	Summary       string                     `yaml:"summary" json:"summary"`
	ImageAsset    string                     `yaml:"image_asset,omitempty" json:"image_asset,omitempty"`
	ImageURL      string                     `yaml:"-" json:"image_url,omitempty"`
	Highlights    []string                   `yaml:"highlights,omitempty" json:"highlights,omitempty"`
	ProductReport *ShowcaseCaseProductReport `yaml:"product_report,omitempty" json:"product_report,omitempty"`
}

// ShowcaseCaseTask is a selectable task shown on a case detail page.
type ShowcaseCaseTask struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	OutputLabel string             `json:"output_label"`
	PromptShort string             `json:"prompt_short"`
	Prompt      string             `json:"prompt"`
	Steps       []ShowcaseCaseStep `json:"steps"`
	Result      ShowcaseCaseResult `json:"result"`
}

// ShowcaseCase is the API view of one published Featured capability definition.
type ShowcaseCase struct {
	ID                string             `json:"id"`
	Type              string             `json:"type"`
	Provider          string             `json:"provider"`
	SourceURL         string             `json:"source_url"`
	Title             string             `json:"title"`
	Description       string             `json:"description"`
	Category          string             `json:"category"`
	Tags              []string           `json:"tags,omitempty"`
	OutputType        string             `json:"output_type"`
	OutputLabel       string             `json:"output_label"`
	ImageURL          string             `json:"image_url"`
	DetailTitle       string             `json:"detail_title"`
	DetailDescription string             `json:"detail_description"`
	AttachmentHint    string             `json:"attachment_hint,omitempty"`
	ResultSummary     string             `json:"result_summary"`
	Tasks             []ShowcaseCaseTask `json:"tasks"`
	BuiltinSkillUID   string             `json:"builtin_skill_uid,omitempty"`
	WorkflowRef       string             `json:"workflow_ref,omitempty"`
	Featured          bool               `json:"featured"`
	FeaturedOrder     int                `json:"featured_order"`
	Gallery           bool               `json:"gallery"`
	SearchAliases     []string           `json:"-"`
}

// ShowcaseCaseListResponse is the directory response used by the homepage and gallery.
type ShowcaseCaseListResponse struct {
	Cases      []ShowcaseCase `json:"cases"`
	Categories []string       `json:"categories"`
	Total      int            `json:"total"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ListCases(w http.ResponseWriter, r *http.Request) {
	locale := common.NormalizeLocale(r.Header.Get("Accept-Language"))
	common.SetLanguageResponseHeaders(w, locale)
	cases, err := availableCases(locale)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	filtered := make([]ShowcaseCase, 0, len(cases))
	for _, item := range cases {
		if !isAllCategory(category) && !matchesCaseCategory(item, category) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(caseSearchText(item)), query) {
			continue
		}
		filtered = append(filtered, item)
	}
	responseCategories := appendCaseCategories([]string{allCategory(locale)}, cases)
	writeJSON(w, http.StatusOK, ShowcaseCaseListResponse{Cases: filtered, Categories: responseCategories, Total: len(filtered)})
}

func GetCase(w http.ResponseWriter, r *http.Request) {
	locale := common.NormalizeLocale(r.Header.Get("Accept-Language"))
	common.SetLanguageResponseHeaders(w, locale)
	id := strings.TrimSpace(common.PathVar(r, "case_id"))
	cases, err := availableCases(locale)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, item := range cases {
		if item.ID == id {
			writeJSON(w, http.StatusOK, item)
			return
		}
	}
	common.ReplyErr(w, "not found", http.StatusNotFound)
}

func availableCases(locale string) ([]ShowcaseCase, error) {
	catalogPath := CatalogPath()
	if catalogPath == "" {
		return []ShowcaseCase{}, nil
	}
	catalog, err := LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	if err := validateFeaturedBindings(catalog, skillbuiltin.CatalogPath()); err != nil {
		return nil, err
	}
	return catalog.ShowcaseCases(locale), nil
}

func validateFeaturedBindings(catalog Catalog, builtinCatalogPath string) error {
	if builtinCatalogPath == "" {
		return definitionFailure("featured Skill catalog requires the builtin Skill catalog")
	}
	builtinCatalog, err := skillbuiltin.LoadCatalog(builtinCatalogPath)
	if err != nil {
		return definitionFailure("load builtin Skill catalog for featured bindings: %v", err)
	}
	byUID := make(map[string]skillbuiltin.CatalogSkill, len(builtinCatalog.Skills))
	for _, entry := range builtinCatalog.Skills {
		byUID[entry.UID] = entry
	}
	for _, definition := range catalog.Cases {
		if definition.Type == TypeWorkflow {
			continue
		}
		if definition.Skill == nil {
			return definitionFailure("featured Skill %s has no Skill binding", definition.ID)
		}
		entry, ok := byUID[definition.Skill.BuiltinSkillUID]
		if !ok {
			return definitionFailure("featured Skill %s references missing builtin Skill %s", definition.ID, definition.Skill.BuiltinSkillUID)
		}
		if entry.Version != definition.Skill.Version || entry.ArchiveSHA256 != definition.Skill.ArchiveSHA256 || entry.SourceURL != definition.Skill.SourceURL {
			return definitionFailure("featured Skill %s binding does not match the builtin Skill catalog", definition.ID)
		}
		if skillbuiltin.CatalogSkillMarketVisible(entry) {
			return definitionFailure("featured Skill %s must not be visible in the ordinary Skill marketplace", definition.ID)
		}
	}
	return nil
}

func allCategory(locale string) string {
	if locale == common.LocaleEnUS {
		return "All"
	}
	return "全部"
}

func isAllCategory(category string) bool {
	return category == "" || category == "全部" || category == "All"
}

func matchesCaseCategory(item ShowcaseCase, category string) bool {
	if item.Category == category {
		return true
	}
	for _, alias := range item.SearchAliases {
		if alias == category {
			return true
		}
	}
	return false
}

func caseSearchText(item ShowcaseCase) string {
	values := []string{item.Title, item.Description, item.DetailTitle, item.DetailDescription, item.Category}
	values = append(values, item.Tags...)
	values = append(values, item.SearchAliases...)
	for _, task := range item.Tasks {
		values = append(values, task.Title, task.Description, task.PromptShort, task.Result.Title, task.Result.Summary)
		values = append(values, task.Result.Highlights...)
	}
	return strings.Join(values, " ")
}

func appendCaseCategories(base []string, cases []ShowcaseCase) []string {
	out := append([]string(nil), base...)
	seen := make(map[string]struct{}, len(out))
	for _, category := range out {
		seen[category] = struct{}{}
	}
	for _, item := range cases {
		category := strings.TrimSpace(item.Category)
		if category == "" {
			continue
		}
		if _, exists := seen[category]; exists {
			continue
		}
		seen[category] = struct{}{}
		out = append(out, category)
	}
	return out
}
