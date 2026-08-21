package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/skillv2/testutil"
	"lazymind/core/store"
)

func TestEnableDiagramDesignCopiesCompletePhaseFourPackage(t *testing.T) {
	builtinRoot, err := filepath.Abs("../../../../skills")
	if err != nil {
		t.Fatalf("resolve builtin skills root: %v", err)
	}
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", builtinRoot)
	t.Setenv("LAZYMIND_SKILL_OBJECT_ROOT", t.TempDir())
	db := testutil.NewTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	const uid = "bsk_01L0D6G4R4M8V2K7Q9C5X3H0EP"
	req := httptest.NewRequest(http.MethodPost, "/api/core/builtin-skills/"+uid+":enable", nil)
	req.Header.Set("X-User-Id", "user_diagram_design_test")
	req.Header.Set("X-User-Name", "Diagram Tester")
	req = mux.SetURLVars(req, map[string]string{"builtin_skill_uid": uid + ":enable"})
	rec := httptest.NewRecorder()

	EnableBuiltinSkill(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var skill testutil.SkillRow
	if err := db.Where(
		"owner_user_id = ? AND origin_builtin_skill_uid = ?",
		"user_diagram_design_test",
		uid,
	).Take(&skill).Error; err != nil {
		t.Fatalf("load installed diagram design skill: %v", err)
	}
	if skill.Category != "design" || skill.SkillName != "diagram-design" || !skill.IsEnabled {
		t.Fatalf("unexpected installed diagram design skill: %#v", skill)
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID == "" {
		t.Fatal("installed diagram design skill has no head revision")
	}

	var entries []testutil.SkillRevisionEntryRow
	if err := db.Where("revision_id = ?", *skill.HeadRevisionID).Find(&entries).Error; err != nil {
		t.Fatalf("load installed diagram design revision: %v", err)
	}
	paths := make(map[string]bool, len(entries))
	typeReferences := 0
	staticExamples := 0
	animatedExamples := 0
	for _, entry := range entries {
		if entry.EntryType != "file" {
			continue
		}
		paths[entry.Path] = true
		if strings.HasPrefix(entry.Path, "references/type-") && strings.HasSuffix(entry.Path, ".md") {
			typeReferences++
		}
		if strings.HasPrefix(entry.Path, "assets/example-") && strings.HasSuffix(entry.Path, ".html") {
			if strings.HasSuffix(entry.Path, "-animated.html") {
				animatedExamples++
			} else {
				staticExamples++
			}
		}
	}
	for _, requiredPath := range []string{
		"SKILL.md",
		"LICENSE",
		"references/style-guide.md",
		"references/import-mermaid.md",
		"references/import-drawio.md",
		"references/export-svg.md",
		"references/animation.md",
		"references/primitive-terminal.md",
		"references/primitive-sketchy.md",
		"assets/template.html",
		"assets/template-terminal.html",
		"assets/template-motion.html",
		"assets/example-import-mermaid.html",
		"assets/example-import-drawio.html",
		"assets/example-loop-terminal.html",
		"assets/example-architecture-sketchy.html",
		"assets/example-queue-animated.html",
		"assets/example-policy-trace-animated.html",
		"assets/example-paved-road-animated.html",
		"assets/example-quadrant-consultant.html",
		"assets/example-sequence-oauth.html",
		"assets/example-slopegraph.html",
		"assets/example-high-level-vertical.html",
		"scripts/apply_motion_controller.py",
		"scripts/self_check.py",
		"scripts/mermaid_extract.py",
		"scripts/drawio_extract.py",
		"scripts/export_svg.py",
		"scripts/verify-motion.py",
		"scripts/verify-geometry.py",
		"scripts/verify-treemap.py",
		"scripts/verify-slopegraph.py",
		"scripts/verify-dumbbell.py",
		"scripts/verify-sequence-oauth.py",
	} {
		if !paths[requiredPath] {
			t.Fatalf("installed diagram design skill is missing %q", requiredPath)
		}
	}
	if typeReferences != 38 || staticExamples != 52 || animatedExamples != 3 {
		t.Fatalf("installed package lost resources: type references=%d static examples=%d animated examples=%d", typeReferences, staticExamples, animatedExamples)
	}
}
