package workflow

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	workflowstore "lazymind/core/workflow/store"
)

// workflowCatalogSpec is the legacy catalog shape consumed by both the Workflow
// management page and the in-chat panel. The source of truth is now the pinned
// Workflow revision in Core, rather than the Python Chat process.
type workflowCatalogSpec map[string]any

func loadWorkflowCatalogSpec(r *http.Request, refOrID string, includeRawFiles bool) (workflowCatalogSpec, error) {
	db := store.DB()
	if db == nil {
		return nil, errors.New("store not initialized")
	}
	pkg, err := workflowstore.New(db).GetWorkflowPackage(r.Context(), store.UserID(r), refOrID, "")
	if err != nil {
		return nil, err
	}
	body := pkg.Files["workflow.yaml"]
	if len(body) == 0 {
		return nil, errors.New("workflow.yaml missing from revision")
	}
	var spec workflowCatalogSpec
	if err := yaml.Unmarshal(body, &spec); err != nil {
		return nil, err
	}
	if includeRawFiles {
		spec["workflow_yaml_raw"] = string(body)
		spec["state_yaml_raw"] = string(pkg.Files["scenario/state.yml"])
		spec["scenario_raw"] = string(pkg.Files["scenario/scenario.md"])
		spec["scripts_raw"] = workflowScriptsRaw(pkg.Files)
	}
	applyWorkflowCatalogLocale(spec, r.Header.Get("Accept-Language"))
	return spec, nil
}

func workflowScriptsRaw(files map[string][]byte) string {
	paths := make([]string, 0)
	for path := range files {
		if strings.HasPrefix(path, "scripts/") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	scripts := make(map[string]string, len(paths))
	for _, path := range paths {
		scripts[path] = string(files[path])
	}
	body, _ := json.Marshal(scripts)
	return string(body)
}

func applyWorkflowCatalogLocale(spec workflowCatalogSpec, acceptLanguage string) {
	locales := catalogStringMap(spec["i18n"])
	if len(locales) == 0 {
		return
	}
	language := strings.ToLower(strings.TrimSpace(strings.Split(acceptLanguage, ",")[0]))
	var selected map[string]any
	for key, value := range locales {
		keyLower := strings.ToLower(key)
		if language == keyLower || (len(language) >= 2 && strings.HasPrefix(keyLower, language[:2])) {
			selected = catalogStringMap(value)
			break
		}
	}
	if selected == nil {
		return
	}
	if name, ok := selected["name"].(string); ok && name != "" {
		spec["name"] = name
	}
	applyCatalogLabels(spec["steps"], selected["steps"])
	applyCatalogLabels(spec["slots"], selected["slots"])
	if ui := catalogStringMap(spec["ui"]); ui != nil {
		applyCatalogLabels(ui["tabs"], selected["tabs"])
	}
}

func applyCatalogLabels(itemsValue, translationsValue any) {
	items, _ := itemsValue.([]any)
	translations := catalogStringMap(translationsValue)
	for _, value := range items {
		item := catalogStringMap(value)
		id, _ := item["id"].(string)
		translated := catalogStringMap(translations[id])
		if label, ok := translated["label"].(string); ok && label != "" {
			item["label"] = label
		}
	}
}

func catalogStringMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case workflowCatalogSpec:
		return map[string]any(typed)
	default:
		return nil
	}
}

func writeWorkflowCatalogJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

// GetWorkflowInfo serves the exact immutable revision that the runtime catalog
// uses. This keeps panel layout and labels available even when Chat is offline.
func GetWorkflowInfo(w http.ResponseWriter, r *http.Request) {
	workflowID := strings.TrimSpace(common.PathVar(r, "workflow_id"))
	if workflowID == "" {
		common.ReplyErr(w, "workflow_id required", http.StatusBadRequest)
		return
	}
	spec, err := loadWorkflowCatalogSpec(r, workflowID, true)
	if err != nil {
		if errors.Is(err, workflowstore.ErrNotFound) {
			common.ReplyErr(w, "workflow not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeWorkflowCatalogJSON(w, spec)
}

// ListWorkflows serves the legacy built-in catalog used by the management UI.
// Published user Workflows are listed by the versioned Workflow facade and also
// have authoring rows in the draft list; including them here would duplicate
// those rows and incorrectly label them as built-in.
func ListWorkflows(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var resources []orm.WorkflowResource
	err := db.WithContext(r.Context()).
		Where("status = 'active' AND source_type = 'builtin' AND owner_user_id = ''").
		Order("plugin_ref ASC").Find(&resources).Error // workflow-naming: persistence
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	workflows := make([]workflowCatalogSpec, 0, len(resources))
	for _, resource := range resources {
		spec, loadErr := loadWorkflowCatalogSpec(r, resource.WorkflowRef, false)
		if loadErr != nil {
			common.ReplyErr(w, loadErr.Error(), http.StatusInternalServerError)
			return
		}
		workflows = append(workflows, spec)
	}
	writeWorkflowCatalogJSON(w, map[string]any{"workflows": workflows})
}
