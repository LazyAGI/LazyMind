package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestValidateGeneratedWorkflowSkeletonRejectsPlaceholderOutput(t *testing.T) {
	err := validateGeneratedWorkflowSkeleton(`
id: ""
name: ""
slots:
  - id: research_topic
steps: []
`)
	if err == nil {
		t.Fatal("expected placeholder skeleton to be rejected")
	}
}

func TestValidateGeneratedWorkflowSkeletonAcceptsConcreteOutput(t *testing.T) {
	err := validateGeneratedWorkflowSkeleton(`
id: deep_research
name: Deep Research
slots:
  - id: research_topic
    type: text
    external: true
  - id: final_report
    type: text
steps:
  - id: synthesize_report
    label: Synthesize report
`)
	if err != nil {
		t.Fatalf("expected concrete skeleton to pass: %v", err)
	}
}

func TestValidateGenerateResumePointRequiresPriorArtifacts(t *testing.T) {
	if err := validateGenerateResumePoint(orm.WorkflowDraft{}, generatePhaseDesignBrief); err != nil {
		t.Fatalf("design_brief start should always be allowed: %v", err)
	}
	if err := validateGenerateResumePoint(orm.WorkflowDraft{}, generatePhaseSkeleton); err == nil {
		t.Fatal("skeleton start should require design brief")
	}
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: user_input
    external: true
  - id: result
steps:
  - id: summarize
`
	stateYAML := `
transitions:
  __start__: [{to: summarize}]
  summarize: [{to: __end__}]
steps:
  summarize:
    inputs: [{slot: user_input, required: true}]
    outputs: [result]
`
	draft := orm.WorkflowDraft{
		DesignBriefContent:  "brief",
		WorkflowYAMLContent: workflowYAML,
		StateYAMLContent:    stateYAML,
	}
	if err := validateGenerateResumePoint(draft, generatePhaseSkeleton); err != nil {
		t.Fatalf("skeleton start should accept design brief: %v", err)
	}
	if err := validateGenerateResumePoint(draft, generatePhaseStateMachine); err != nil {
		t.Fatalf("state_machine start should accept workflow yaml: %v", err)
	}
	if err := validateGenerateResumePoint(draft, generatePhaseScenarioScripts); err != nil {
		t.Fatalf("scenario_scripts start should accept valid workflow+state: %v; diagnostics=%#v", err, diagnoseWorkflowWithProfile(workflowYAML, stateYAML, "", "{}", graphengine.ProfileGenerationPhase))
	}
}

func TestValidateGenerateResumePointAllowsScenarioResumeWithOnlyUIErrors(t *testing.T) {
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: user_input
    external: true
  - id: result
    exposed: true
steps:
  - id: summarize
ui:
  tabs:
    - key: result
      label: Result
      contents:
        - slot: result
`
	stateYAML := `
transitions:
  __start__:
    - to: summarize
  summarize:
    - to: __end__
steps:
  summarize:
    inputs:
      - slot: user_input
        required: true
    outputs:
      - result
`
	draft := orm.WorkflowDraft{
		WorkflowYAMLContent: workflowYAML,
		StateYAMLContent:    stateYAML,
	}
	diagnostics := diagnoseWorkflowWithProfile(workflowYAML, stateYAML, "", "{}", graphengine.ProfileGenerationPhase)
	if !hasDiagnosticErrorsForTarget(diagnostics, "ui") {
		t.Fatalf("fixture should have UI errors: %#v", diagnostics)
	}
	if hasDiagnosticErrorsForTarget(diagnostics, "statemachine") {
		t.Fatalf("fixture should not have state machine errors: %#v", diagnostics)
	}
	if err := validateGenerateResumePoint(draft, generatePhaseScenarioScripts); err != nil {
		t.Fatalf("scenario_scripts resume should not be blocked by UI-only errors: %v", err)
	}
}

func TestValidateGeneratedScenarioContentRejectsPlaceholders(t *testing.T) {
	stateYAML := `
steps:
  summarize:
    outputs:
      - result
transitions:
  __start__:
    - to: summarize
  summarize:
    - to: __end__
`
	scenarioMD := `
# Scenario

### summarize (summarize)

（暂无描述）
`
	if err := validateGeneratedScenarioContent(scenarioMD, stateYAML); err == nil {
		t.Fatal("expected placeholder scenario content to be rejected")
	}
}

func TestValidateGeneratedScenarioContentAcceptsSubstantiveSections(t *testing.T) {
	stateYAML := `
steps:
  summarize:
    outputs:
      - result
transitions:
  __start__:
    - to: summarize
  summarize:
    - to: __end__
`
	scenarioMD := `
# Scenario

### summarize (summarize)

The summarize step reads the user input and upstream source material, identifies
the important findings, and produces the final result material for the user.
`
	if err := validateGeneratedScenarioContent(scenarioMD, stateYAML); err != nil {
		t.Fatalf("expected substantive scenario content to pass: %v", err)
	}
}

func TestReusableSkillScriptsKeepsOnlyOriginalScriptFiles(t *testing.T) {
	pkg := map[string]any{
		"files": []any{
			map[string]any{"path": "scripts/tools/check.py", "content": "def check():\n    return True\n"},
			map[string]any{"path": "scripts/helpers/format.py", "content": "def format_value(v):\n    return str(v)\n"},
			map[string]any{"path": "SKILL.md", "content": "# Skill"},
		},
	}
	report := map[string]any{
		"scripts/tools/check.py": map[string]any{"classification": "importable_tool"},
		"scripts/helpers/format.py": map[string]any{
			"classification": "wrappable_command",
		},
		"synthesis_checklist": "function runSynthesisCheck() { return true; }",
	}
	scripts := reusableSkillScripts(pkg, report)
	if scripts["scripts/tools/check.py"] == "" {
		t.Fatalf("expected original script path to be preserved: %#v", scripts)
	}
	if scripts["scripts/helpers/format.py"] == "" {
		t.Fatalf("expected nested script path to be preserved: %#v", scripts)
	}
	if _, ok := scripts["scripts/generated/synthesis_checklist.js"]; ok {
		t.Fatalf("inline analysis code should not be stored as an original script: %#v", scripts)
	}
}

func TestDetectSkillCapabilityRequirementsFindsGenericExternalNeeds(t *testing.T) {
	requirements := detectSkillCapabilityRequirements(`
# Find Skill

Use SkillHub to search skill packages for the user. If the user asks for poster
assets, generate images from text prompts and inspect uploaded images.
`)
	got := map[string]bool{}
	for _, req := range requirements {
		got[req.WorkflowCapability] = true
	}
	for _, want := range []string{"http_request", "text2image", "vlm"} {
		if !got[want] {
			t.Fatalf("missing capability %q in %#v", want, requirements)
		}
	}
	if got["web_search"] {
		t.Fatalf("SkillHub lookup should map to http_request, not web_search: %#v", requirements)
	}
}

func TestDetectSkillCapabilityRequirementsFromSnapshotIgnoresReferenceTaxonomy(t *testing.T) {
	requirements := detectSkillCapabilityRequirementsFromSnapshot(workflowSourceSkillSnapshot{Files: []skillPackageFile{
		{Path: "SKILL.md", Content: `
# Find SkillHub Skill

Use SkillHub to search skill packages through https://api.skillhub.cn/api/skills.
Open category references only to map the user's search intent.
`},
		{Path: "references/design-media.md", Content: `
| key | name | english |
| --- | --- | --- |
| design-image-gen | 图片生成 | Image Generation |
| design-image-edit | 图片编辑 | Image Editing |
`},
	}})
	got := map[string]bool{}
	for _, req := range requirements {
		got[req.WorkflowCapability] = true
	}
	if !got["http_request"] {
		t.Fatalf("missing http_request in %#v", requirements)
	}
	if got["web_search"] {
		t.Fatalf("explicit SkillHub API access should not require search-engine config: %#v", requirements)
	}
	for _, unwanted := range []string{"text2image", "image_editing"} {
		if _, ok := got[unwanted]; ok {
			t.Fatalf("reference taxonomy should not require %q: %#v", unwanted, requirements)
		}
	}
}

func TestDetectSkillCapabilityRequirementsDetectsCredentialedAPI(t *testing.T) {
	requirements := detectSkillCapabilityRequirements(`
# CRM Sync Skill

Call https://api.example.com/v1/customers with an API key in the Authorization header.
`)
	got := map[string]skillCapabilityRequirement{}
	for _, req := range requirements {
		got[req.WorkflowCapability] = req
	}
	if !got["http_request"].Supported {
		t.Fatalf("missing http_request in %#v", requirements)
	}
	credential := got["credentialed_http_request"]
	if !credential.Configurable || !credential.ConfigWarning || credential.CredentialKind != "api_key" {
		t.Fatalf("missing configurable credential requirement in %#v", requirements)
	}
	for _, unwanted := range []string{"text2image", "image_editing"} {
		if _, ok := got[unwanted]; ok {
			t.Fatalf("reference taxonomy should not require %q: %#v", unwanted, requirements)
		}
	}
}

func TestDetectSkillCapabilityRequirementsIgnoresCredentialTaxonomyFields(t *testing.T) {
	requirements := detectSkillCapabilityRequirements(`
# Public SkillHub API

Call https://api.skillhub.cn/api/skills. This endpoint is public and 无需鉴权.
The response labels include requires_api_key so users can filter skills that need their own key.
`)
	got := map[string]bool{}
	for _, req := range requirements {
		got[req.WorkflowCapability] = true
	}
	if !got["http_request"] {
		t.Fatalf("missing http_request in %#v", requirements)
	}
	if got["credentialed_http_request"] {
		t.Fatalf("requires_api_key taxonomy field should not require credentials: %#v", requirements)
	}
}

func TestDetectSkillCapabilityRequirementsFromSnapshotReadsExecutableReferences(t *testing.T) {
	requirements := detectSkillCapabilityRequirementsFromSnapshot(workflowSourceSkillSnapshot{Files: []skillPackageFile{
		{Path: "SKILL.md", Content: `
# Poster Skill

Follow references/render.md for the rendering implementation.
`},
		{Path: "references/render.md", Content: `
调用文生图工具生成图片，并在需要时执行图片编辑工具修图。
`},
	}})
	got := map[string]bool{}
	for _, req := range requirements {
		got[req.WorkflowCapability] = true
	}
	for _, want := range []string{"text2image", "image_editing"} {
		if !got[want] {
			t.Fatalf("missing capability %q in %#v", want, requirements)
		}
	}
}

func TestInjectSkillCapabilitiesIntoWorkflowDeclaresRequiredCapabilities(t *testing.T) {
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: query
    type: text
    external: true
  - id: result
    type: text
steps:
  - id: search
    label: Search
`
	stateYAML := `
start_route: all
steps:
  search:
    inputs: [{slot: query, required: true}]
    outputs: [result]
transitions:
  __start__: [{to: search}]
  search: [{to: __end__}]
`
	mappings := mergeDetectedCapabilityMappings(nil, []skillCapabilityRequirement{{
		ID: "web_search", Label: "网页搜索", WorkflowCapability: "web_search", FrameworkTool: "web_search", Supported: true, Required: true,
	}})
	nextWorkflow, nextState, injected := injectSkillCapabilitiesIntoWorkflow(workflowYAML, stateYAML, mappings)
	if len(injected) != 1 || injected[0] != "web_search" {
		t.Fatalf("injected = %#v", injected)
	}
	compiled := graphengine.Compile(nextWorkflow, nextState, "", graphengine.ProfilePublish)
	if !compiled.Valid {
		t.Fatalf("compiled invalid: %#v", compiled.Diagnostics)
	}
	if !stringSliceContains(compiled.Graph.Nodes["search"].Capabilities, "web_search") {
		t.Fatalf("node capabilities missing web_search: %#v", compiled.Graph.Nodes["search"].Capabilities)
	}
	if !stringSliceContains(compiled.Graph.Nodes["search"].LegacyTools, "web_search") {
		t.Fatalf("node tools missing web_search: %#v", compiled.Graph.Nodes["search"].LegacyTools)
	}
}

func TestInjectSkillCapabilitiesIntoWorkflowAddsCredentialClarification(t *testing.T) {
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: query
    type: text
    external: true
  - id: result
    type: text
steps:
  - id: fetch
    label: Fetch
`
	stateYAML := `
steps:
  fetch:
    inputs: [{slot: query, required: true}]
    outputs: [result]
transitions:
  __start__: [{to: fetch}]
  fetch: [{to: __end__}]
`
	mappings := mergeDetectedCapabilityMappings(nil, []skillCapabilityRequirement{{
		ID: "authenticated_api_access", Label: "凭证型 API 访问", WorkflowCapability: "credentialed_http_request", Supported: true, Required: true, Configurable: true, ConfigWarning: true, CredentialKind: "api_key",
	}})
	nextWorkflow, nextState, injected := injectSkillCapabilitiesIntoWorkflow(workflowYAML, stateYAML, mappings)
	if len(injected) != 1 || injected[0] != "credentialed_http_request" {
		t.Fatalf("injected = %#v", injected)
	}
	if !strings.Contains(nextWorkflow, "clarification_fields") || !strings.Contains(nextWorkflow, "authenticated_api_access_credential") {
		t.Fatalf("credential clarification field missing:\n%s", nextWorkflow)
	}
	compiled := graphengine.Compile(nextWorkflow, nextState, "", graphengine.ProfilePublish)
	if !compiled.Valid {
		t.Fatalf("compiled invalid: %#v", compiled.Diagnostics)
	}
	if !stringSliceContains(compiled.Graph.Nodes["fetch"].LegacyTools, "url_fetch") {
		t.Fatalf("credentialed API access should map to url_fetch tool: %#v", compiled.Graph.Nodes["fetch"].LegacyTools)
	}
}

func TestInjectSkillCapabilitiesIntoWorkflowUsesStepToolMappings(t *testing.T) {
	workflowYAML := `
id: demo
name: Demo
slots:
  - id: query
    external: true
  - id: raw
  - id: answer
steps:
  - id: fetch
  - id: summarize
`
	stateYAML := `
steps:
  fetch:
    inputs: [{slot: query, required: true}]
    outputs: [raw]
  summarize:
    inputs: [{slot: raw, required: true}]
    outputs: [answer]
transitions:
  __start__: [{to: fetch}]
  fetch: [{to: summarize}]
  summarize: [{to: __end__}]
`
	mappings := mergeDetectedCapabilityMappings(map[string]any{
		"fetch": map[string]any{
			"workflow_capability": "http_request",
		},
	}, []skillCapabilityRequirement{{
		ID: "public_api_access", Label: "公开 API 访问", WorkflowCapability: "http_request", WorkflowTools: []string{"url_fetch"}, Supported: true, Required: true,
	}})
	nextWorkflow, nextState, _ := injectSkillCapabilitiesIntoWorkflow(workflowYAML, stateYAML, mappings)
	compiled := graphengine.Compile(nextWorkflow, nextState, "", graphengine.ProfilePublish)
	if !compiled.Valid {
		t.Fatalf("compiled invalid: %#v", compiled.Diagnostics)
	}
	if !stringSliceContains(compiled.Graph.Nodes["fetch"].LegacyTools, "url_fetch") {
		t.Fatalf("fetch node tools missing url_fetch: %#v", compiled.Graph.Nodes["fetch"].LegacyTools)
	}
	if stringSliceContains(compiled.Graph.Nodes["summarize"].LegacyTools, "url_fetch") {
		t.Fatalf("summarize node should not inherit step-specific fetch tool: %#v", compiled.Graph.Nodes["summarize"].LegacyTools)
	}
}

func TestReconcileDetectedCapabilityMappingsPrunesStaleCapabilityEntries(t *testing.T) {
	mappings := reconcileDetectedCapabilityMappings(map[string]any{
		"capability:image_editing": map[string]any{
			"action":              "require",
			"required":            true,
			"workflow_capability": "image_editing",
			"source":              "deterministic_skill_capability_scan",
		},
		"capability:web_search": map[string]any{
			"action":              "require",
			"required":            true,
			"workflow_capability": "web_search",
			"source":              "deterministic_skill_capability_scan",
		},
		"llm-text2image": map[string]any{
			"action":              "require",
			"required":            true,
			"workflow_capability": "text2image",
		},
		"script:search": map[string]any{
			"action":         "replace",
			"framework_tool": "web_search",
			"available":      true,
		},
	}, []skillCapabilityRequirement{{
		ID: "web_search", Label: "网页搜索", WorkflowCapability: "web_search", FrameworkTool: "web_search", Supported: true, Required: true,
	}})
	if _, ok := mappings["capability:image_editing"]; ok {
		t.Fatalf("stale image_editing capability was not pruned: %#v", mappings)
	}
	if _, ok := mappings["llm-text2image"]; ok {
		t.Fatalf("stale text2image capability was not pruned: %#v", mappings)
	}
	if _, ok := mappings["capability:web_search"]; !ok {
		t.Fatalf("web_search capability was pruned: %#v", mappings)
	}
	if _, ok := mappings["script:search"]; !ok {
		t.Fatalf("non-capability mapping should be preserved: %#v", mappings)
	}
}

func TestSaveGeneratedDraftUpdatesDoesNotWriteCanceledJob(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{
		ID:             "22222222-2222-4222-8222-222222222222",
		Name:           "Canceled Draft",
		CreatedBy:      "user-1",
		GenerateStatus: generateStatusBriefDone,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	jobRow := orm.AsyncJob{
		ID:           "job-save-canceled",
		JobType:      workflowDraftGenerateJobType,
		Status:       string(asyncjob.StatusCanceled),
		ResourceType: "workflow_draft",
		ResourceID:   draft.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&jobRow).Error; err != nil {
		t.Fatal(err)
	}

	err := saveGeneratedDraftUpdates(context.Background(), db.DB, draft.ID, asyncjob.Job{ID: jobRow.ID}, map[string]any{
		"generate_status": generateStatusDone,
		"updated_at":      time.Now().UTC(),
	})
	if !errors.Is(err, errWorkflowDraftGenerationCanceled) {
		t.Fatalf("save err=%v, want canceled", err)
	}
	var updated orm.WorkflowDraft
	if err := db.Where("id=?", draft.ID).First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.GenerateStatus != generateStatusBriefDone {
		t.Fatalf("draft status changed after canceled save: %q", updated.GenerateStatus)
	}
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
