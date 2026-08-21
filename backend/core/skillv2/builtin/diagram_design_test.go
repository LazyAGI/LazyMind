package builtin

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiagramDesignPackage(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve builtin test source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", ".."))
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", filepath.Join(repositoryRoot, "skills"))

	const uid = "bsk_01L0D6G4R4M8V2K7Q9C5X3H0EP"
	pkg, found, err := PackageByUID(uid)
	if err != nil {
		t.Fatalf("load diagram design package: %v", err)
	}
	if !found {
		t.Fatal("diagram design manifest not found")
	}
	if pkg.Name != "diagram-design" || pkg.Category != "design" {
		t.Fatalf("unexpected package identity: name=%q category=%q", pkg.Name, pkg.Category)
	}
	if !strings.Contains(pkg.Description, "独立 SVG") || !strings.Contains(pkg.Description, "第四阶段") || !strings.Contains(pkg.Description, "受控动画") {
		t.Fatalf("unexpected package description: %q", pkg.Description)
	}

	for _, requiredPath := range []string{
		"SKILL.md",
		"LICENSE",
		"references/style-guide.md",
		"references/semantic-patterns.md",
		"references/primitive-annotation.md",
		"references/primitive-icons.md",
		"assets/template.html",
		"assets/template-dark.html",
		"assets/template-full.html",
		"assets/icons.html",
		"assets/example-import-mermaid.html",
		"assets/example-import-drawio.html",
		"references/import-mermaid.md",
		"references/import-drawio.md",
		"references/output-spec.md",
		"references/export-svg.md",
		"references/animation.md",
		"references/primitive-terminal.md",
		"references/primitive-sketchy.md",
		"assets/template-terminal.html",
		"assets/template-motion.html",
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
		if _, exists := pkg.Files[requiredPath]; !exists {
			t.Fatalf("diagram design package missing %q", requiredPath)
		}
	}

	typeReferences := 0
	staticExamples := 0
	animatedExamples := 0
	allowedScripts := map[string]bool{
		"scripts/apply_motion_controller.py": true,
		"scripts/self_check.py":              true,
		"scripts/mermaid_extract.py":         true,
		"scripts/drawio_extract.py":          true,
		"scripts/export_svg.py":              true,
		"scripts/verify-motion.py":           true,
		"scripts/verify-geometry.py":         true,
		"scripts/verify-treemap.py":          true,
		"scripts/verify-slopegraph.py":       true,
		"scripts/verify-dumbbell.py":         true,
		"scripts/verify-sequence-oauth.py":   true,
	}
	for filePath := range pkg.Files {
		if strings.HasPrefix(filePath, "references/type-") && strings.HasSuffix(filePath, ".md") {
			typeReferences++
		}
		if strings.HasPrefix(filePath, "assets/example-") && strings.HasSuffix(filePath, ".html") {
			if strings.HasSuffix(filePath, "-animated.html") {
				animatedExamples++
			} else {
				staticExamples++
			}
		}
		if strings.HasPrefix(filePath, "scripts/") && !allowedScripts[filePath] {
			t.Fatalf("phase-four package contains unsupported script %q", filePath)
		}
	}
	if typeReferences != 38 || staticExamples != 52 || animatedExamples != 3 {
		t.Fatalf("unexpected phase-four coverage: type references=%d static examples=%d animated examples=%d", typeReferences, staticExamples, animatedExamples)
	}

	instructions := string(pkg.Files["SKILL.md"])
	for _, requiredRule := range []string{
		"LazyMind 强制工作流",
		"38 类图形路由",
		"references/style-guide.md",
		"scripts/self_check.py",
		"args=[\"<write_file 返回的绝对 path>\"]",
		"save_chat_artifact",
		"第四阶段不支持",
		"scripts/mermaid_extract.py",
		"scripts/drawio_extract.py",
		"scripts/export_svg.py",
		"references/animation.md",
		"assets/template-motion.html",
		"scripts/apply_motion_controller.py",
		"scripts/verify-motion.py",
		"references/primitive-terminal.md",
		"references/primitive-sketchy.md",
		"scripts/verify-geometry.py",
		"scripts/verify-treemap.py",
		"scripts/verify-slopegraph.py",
		"scripts/verify-dumbbell.py",
		"scripts/verify-sequence-oauth.py",
		"fidelity ledger",
		"find_user_attachment",
		"PNG 不在第四阶段范围内",
		"中文字体规则",
		"cathrynlavery/diagram-design",
	} {
		if !strings.Contains(instructions, requiredRule) {
			t.Fatalf("diagram design instructions missing rule %q", requiredRule)
		}
	}
}
