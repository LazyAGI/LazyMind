package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestScriptsApprovedForPublishRequiresMatchingAuditHash(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:script_publish?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	source := "def run(value):\n    return value\n"
	sum := sha256.Sum256([]byte(source))
	hash := hex.EncodeToString(sum[:])
	analysis := orm.WorkflowGenerationAnalysis{ID: "a1", DraftID: "d1", ScriptReportJSON: `{"scripts/run.py":{"classification":"importable_tool","sha256":"` + hash + `"}}`}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{ID: "d1", SourceAnalysisID: "a1", ScriptsContent: `{"scripts/run.py":"def run(value):\n    return value\n"}`}
	if !scriptsApprovedForPublish(db, draft) {
		t.Fatal("matching audited script should be publishable")
	}
	draft.ScriptsContent = `{"scripts/run.py":"def run(value):\n    return value + 1\n"}`
	if scriptsApprovedForPublish(db, draft) {
		t.Fatal("modified script must invalidate audit")
	}
}

func TestGeneratedScriptAuditAllowsOnlyUnchangedPythonScripts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:generated_script_audit?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	source := "def run(value):\n    return value\n"
	reportJSON, err := scriptAuditReportJSON("{}", map[string]string{"run.py": source, "notes.js": "export default 1;\n"})
	if err != nil {
		t.Fatal(err)
	}
	analysis := orm.WorkflowGenerationAnalysis{ID: "a-generated", DraftID: "d-generated", ScriptReportJSON: reportJSON}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{ID: "d-generated", SourceAnalysisID: analysis.ID, ScriptsContent: `{"run.py":"def run(value):\n    return value\n"}`}
	if !scriptsApprovedForPublish(db, draft) {
		t.Fatalf("unchanged generated Python script should be publishable; report=%s", reportJSON)
	}
	draft.ScriptsContent = `{"run.py":"def run(value):\n    return value + 1\n"}`
	if scriptsApprovedForPublish(db, draft) {
		t.Fatal("edited generated script must invalidate the script audit")
	}
	draft.ScriptsContent = `{"notes.js":"export default 1;\n"}`
	if scriptsApprovedForPublish(db, draft) {
		t.Fatal("non-Python generated scripts must not be auto-approved")
	}
}

func TestFrameworkToolsAvailableForPublishRequiresAuditedAvailability(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:framework_tool_publish?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{ID: "d2", SourceAnalysisID: "a2"}
	analysis := orm.WorkflowGenerationAnalysis{
		ID:                    "a2",
		DraftID:               draft.ID,
		ToolMappingReportJSON: `{"search":{"action":"replace","framework_tool":"web_search","available":true}}`,
	}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	if !frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("an audited available framework replacement should be publishable")
	}
	if err := db.Model(&analysis).Update("tool_mapping_report_json", `{"capability:web_search":{"action":"require","framework_tool":"web_search","available":true},"parse_query":"llm_text_analysis","search_skills":"http_request"}`).Error; err != nil {
		t.Fatal(err)
	}
	if !frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("mixed step mappings and available capability requirements should be publishable")
	}
	if err := db.Model(&analysis).Update("tool_mapping_report_json", `{"search":{"action":"replace","framework_tool":"web_search","available":false}}`).Error; err != nil {
		t.Fatal(err)
	}
	if frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("an unavailable framework replacement must block publishing")
	}
	if err := db.Model(&analysis).Update("tool_mapping_report_json", `{"search":{"action":"replace","framework_tool":"web_search"}}`).Error; err != nil {
		t.Fatal(err)
	}
	if frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("a replacement without an availability audit must fail closed")
	}
}

func TestAuthoringDiagnosticsRequireDeclaredSkillCapabilities(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:skill_capability_publish?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	analysis := orm.WorkflowGenerationAnalysis{
		ID:                    "analysis-cap",
		DraftID:               "draft-cap",
		ToolMappingReportJSON: `{"capability:web_search":{"action":"require","required":true,"workflow_capability":"web_search","framework_tool":"web_search","available":true,"label":"网页搜索"}}`,
	}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{
		ID:                  "draft-cap",
		SourceAnalysisID:    analysis.ID,
		WorkflowYAMLContent: "id: demo\nname: Demo\nslots:\n  - id: input\n    external: true\n  - id: result\nsteps:\n  - id: search\n",
		StateYAMLContent:    "steps:\n  search:\n    inputs: [{slot: input, required: true}]\n    outputs: [result]\ntransitions:\n  __start__: [{to: search}]\n  search: [{to: __end__}]\n",
		ScenarioContent:     "# Scenario\n\n### search\n\nSearch the web and produce a result.\n",
		ScriptsContent:      "{}",
	}
	if diagnostics := authoringDiagnosticsForDraft(db, draft); diagnostics.Valid {
		t.Fatalf("missing required capability must block publish: %#v", diagnostics.Diagnostics)
	}
	mappings := requiredCapabilityMappingsForDraft(db, draft.ID, draft.SourceAnalysisID)
	draft.WorkflowYAMLContent, draft.StateYAMLContent, _ = injectSkillCapabilitiesIntoWorkflow(draft.WorkflowYAMLContent, draft.StateYAMLContent, mappings)
	if diagnostics := authoringDiagnosticsForDraft(db, draft); !diagnostics.Valid {
		t.Fatalf("declared required capability should publish: %#v", diagnostics.Diagnostics)
	}
}

func TestSyncSkillCapabilitiesBeforePublishRestoresEditorDroppedCapabilities(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:skill_capability_publish_sync?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowDraft{}, &orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	analysis := orm.WorkflowGenerationAnalysis{
		ID:                    "analysis-sync",
		DraftID:               "draft-sync",
		ToolMappingReportJSON: `{"capability:http_request":{"action":"require","required":true,"workflow_capability":"http_request","available":true,"label":"公开 API 访问"},"capability:credentialed_http_request":{"action":"require","required":true,"workflow_capability":"credentialed_http_request","available":true,"label":"凭证型 API 访问","configurable":true,"config_warning":true,"credential_kind":"api_key"}}`,
	}
	draft := orm.WorkflowDraft{
		ID:                    "draft-sync",
		CreatedBy:             "user-sync",
		SourceType:            "skill",
		SourceSkillRevisionID: "builtin:sync",
		SourceSkillTreeHash:   "tree-sync",
		SourceAnalysisID:      analysis.ID,
		WorkflowYAMLContent:   "id: sync\nname: Sync\nslots:\n  - id: result\nsteps:\n  - id: fetch\n",
		StateYAMLContent:      "steps:\n  - id: fetch\n    outputs: [result]\n    tools: [url_fetch]\ntransitions:\n  __start__: [{to: fetch}]\n  fetch: [{to: __end__}]\n",
		ScenarioContent:       "# Scenario\n\n### fetch\n\nFetch data.\n",
		ScriptsContent:        "{}",
		Version:               1,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	if err := syncSkillCapabilitiesBeforePublish(t.Context(), db, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Version != 2 {
		t.Fatalf("draft version = %d, want 2", draft.Version)
	}
	if !strings.Contains(draft.StateYAMLContent, "credentialed_http_request") || !strings.Contains(draft.StateYAMLContent, "url_fetch") || !strings.Contains(draft.WorkflowYAMLContent, "clarification_fields") {
		t.Fatalf("capabilities were not restored\nworkflow:\n%s\nstate:\n%s", draft.WorkflowYAMLContent, draft.StateYAMLContent)
	}
	if diagnostics := authoringDiagnosticsForDraft(db, draft); !diagnostics.Valid {
		t.Fatalf("restored capabilities should pass publish diagnostics: %#v", diagnostics.Diagnostics)
	}
}

func TestAuthoringDiagnosticsWarnForCredentialedCapabilities(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:skill_capability_credentials?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	analysis := orm.WorkflowGenerationAnalysis{
		ID:                    "analysis-credential",
		DraftID:               "draft-credential",
		ToolMappingReportJSON: `{"capability:credentialed_http_request":{"action":"require","required":true,"workflow_capability":"credentialed_http_request","available":true,"label":"凭证型 API 访问","configurable":true,"config_warning":true,"credential_kind":"api_key"}}`,
	}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{
		ID:                  "draft-credential",
		SourceAnalysisID:    analysis.ID,
		WorkflowYAMLContent: "id: credential\nname: Credential\nruntime:\n  clarification_fields:\n    - id: authenticated_api_access_credential\n      question: Please enter the API key.\n      type: text\nslots:\n  - id: result\nsteps:\n  - id: fetch\n    capabilities: [credentialed_http_request]\n",
		StateYAMLContent:    "steps:\n  fetch:\n    outputs: [result]\ntransitions:\n  __start__: [{to: fetch}]\n  fetch: [{to: __end__}]\n",
		ScenarioContent:     "# Scenario\n\n### fetch\n\nFetch data from a credentialed API.\n",
		ScriptsContent:      "{}",
	}
	diagnostics := authoringDiagnosticsForDraft(db, draft)
	if !diagnostics.Valid {
		t.Fatalf("credential warning must not block publish: %#v", diagnostics.Diagnostics)
	}
	foundWarning := false
	for _, diagnostic := range diagnostics.Diagnostics {
		if diagnostic.Code == "WORKFLOW_CREDENTIAL_CONFIGURATION_RECOMMENDED" && diagnostic.Severity == "warning" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("missing credential warning: %#v", diagnostics.Diagnostics)
	}
}

func TestAuthoringDiagnosticsAcceptsMappedWorkflowTools(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:skill_capability_tools?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	analysis := orm.WorkflowGenerationAnalysis{
		ID:                    "analysis-tools",
		DraftID:               "draft-tools",
		ToolMappingReportJSON: `{"capability:http_request":{"action":"require","required":true,"workflow_capability":"http_request","workflow_tools":["url_fetch"],"available":true,"label":"公开 API 访问"}}`,
	}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{
		ID:                  "draft-tools",
		SourceAnalysisID:    analysis.ID,
		WorkflowYAMLContent: "id: tools\nname: Tools\nslots:\n  - id: result\nsteps:\n  - id: fetch\n",
		StateYAMLContent:    "steps:\n  fetch:\n    outputs: [result]\n    tools: [url_fetch]\ntransitions:\n  __start__: [{to: fetch}]\n  fetch: [{to: __end__}]\n",
		ScenarioContent:     "# Scenario\n\n### fetch\n\nFetch public API data.\n",
		ScriptsContent:      "{}",
	}
	if diagnostics := authoringDiagnosticsForDraft(db, draft); !diagnostics.Valid {
		t.Fatalf("mapped workflow tools should satisfy capability diagnostics: %#v", diagnostics.Diagnostics)
	}
}

func TestAuthoringDiagnosticsAllowsAdminToPublishUnauditedScripts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:script_publish_admin?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	draft := orm.WorkflowDraft{
		ID:                  "draft-admin-script",
		WorkflowYAMLContent: "id: admin_script\nname: Admin Script\nslots:\n  - id: input\n    external: true\n  - id: result\nsteps:\n  - id: run\n",
		StateYAMLContent:    "steps:\n  run:\n    inputs: [{slot: input, required: true}]\n    outputs: [result]\ntransitions:\n  __start__: [{to: run}]\n  run: [{to: __end__}]\n",
		ScenarioContent:     "# Scenario\n\n### run\n\nRun the helper script.\n",
		ScriptsContent:      `{"scripts/run.py":"def run(value):\n    return value\n"}`,
	}
	nonAdmin := authoringDiagnosticsForDraft(db, draft)
	if nonAdmin.Valid {
		t.Fatalf("non-admin diagnostics should block unaudited scripts: %#v", nonAdmin.Diagnostics)
	}
	adminReq := httptest.NewRequest("POST", "/workflow-drafts/draft-admin-script:publish", nil)
	adminReq.Header.Set("X-User-Role", "system-admin")
	admin := authoringDiagnosticsForRequest(db, draft, adminReq)
	if !admin.Valid {
		t.Fatalf("admin diagnostics should allow unaudited scripts: %#v", admin.Diagnostics)
	}
	var foundWarning bool
	for _, diagnostic := range admin.Diagnostics {
		if diagnostic.Code == "SCRIPT_ADMIN_APPROVED" && diagnostic.Severity == "warning" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("admin diagnostics should include a script publish warning: %#v", admin.Diagnostics)
	}
}
