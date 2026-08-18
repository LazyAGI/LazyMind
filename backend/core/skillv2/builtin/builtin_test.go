package builtin

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestTemplateID prefixes the UID with the builtin prefix.
func TestTemplateID(t *testing.T) {
	got := TemplateID("abc123")
	want := "builtin:abc123"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTemplateID_TrimsWhitespace trims whitespace from the input UID.
func TestTemplateID_TrimsWhitespace(t *testing.T) {
	got := TemplateID("  uid-1  ")
	want := "builtin:uid-1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestIsTemplateID detects the builtin prefix.
func TestIsTemplateID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"builtin:abc", true},
		{"builtin:", true},
		{"normal-id", false},
		{"", false},
		{"BUILTIN:abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := IsTemplateID(tt.id); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseSkillMDMetadata extracts name and description from YAML frontmatter.
func TestParseSkillMDMetadata(t *testing.T) {
	content := "---\nname: My Skill\ndescription: A test skill\n---\n\n# Body content"
	name, desc := parseSkillMDMetadata(content)
	if name != "My Skill" || desc != "A test skill" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
}

// TestParseSkillMDMetadata_NoFrontmatter returns empty strings.
func TestParseSkillMDMetadata_NoFrontmatter(t *testing.T) {
	name, desc := parseSkillMDMetadata("# Just a heading")
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty", name, desc)
	}
}

// TestParseSkillMDMetadata_MissingClosing returns empty strings.
func TestParseSkillMDMetadata_MissingClosing(t *testing.T) {
	content := "---\nname: Test\n# no closing ---"
	name, desc := parseSkillMDMetadata(content)
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty", name, desc)
	}
}

// TestParseSkillMDMetadata_EmptyFrontmatter returns empty strings.
func TestParseSkillMDMetadata_EmptyFrontmatter(t *testing.T) {
	content := "---\n---\nbody"
	name, desc := parseSkillMDMetadata(content)
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty", name, desc)
	}
}

// TestParseSkillMDMetadata_WindowsLineEndings handles CRLF.
func TestParseSkillMDMetadata_WindowsLineEndings(t *testing.T) {
	content := "---\r\nname: Win Skill\r\ndescription: CRLF test\r\n---\r\n\r\nBody"
	name, desc := parseSkillMDMetadata(content)
	if name != "Win Skill" || desc != "CRLF test" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
}

// TestParseSkillMDMetadata_TrimsWhitespace trims name and description.
func TestParseSkillMDMetadata_TrimsWhitespace(t *testing.T) {
	content := "---\nname:   Padded   \ndescription:   Desc with spaces   \n---\nbody"
	name, desc := parseSkillMDMetadata(content)
	if name != "Padded" || desc != "Desc with spaces" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
}

func TestMarketResearcherPackage(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve builtin test source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", ".."))
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", filepath.Join(repositoryRoot, "skills"))

	const uid = "bsk_01K2B9M4RKT8A6C3F7H5N1P0Q2"
	pkg, found, err := PackageByUID(uid)
	if err != nil {
		t.Fatalf("load market researcher package: %v", err)
	}
	if !found {
		t.Fatal("market researcher manifest not found")
	}
	if pkg.Name != "market-researcher" || pkg.Category != "research" {
		t.Fatalf("unexpected package identity: name=%q category=%q", pkg.Name, pkg.Category)
	}
	if !strings.Contains(pkg.Description, "完整的市场与行业研究专家") {
		t.Fatalf("unexpected package description: %q", pkg.Description)
	}

	for _, requiredPath := range []string{
		"SKILL.md",
		"references/sector-overview.md",
		"references/competitive-analysis.md",
		"references/comps-analysis.md",
		"references/idea-generation.md",
		"references/data-and-source-routing.md",
		"references/online-research.md",
		"references/public-web-source-playbook.md",
		"references/visualization-guidelines.md",
		"references/report-contract.md",
		"references/quality-gates.md",
		"scripts/calculate-comps.py",
		"scripts/build-mermaid-chart.py",
	} {
		if _, exists := pkg.Files[requiredPath]; !exists {
			t.Fatalf("market researcher package missing %q", requiredPath)
		}
	}

	skillInstructions := string(pkg.Files["SKILL.md"])
	for _, requiredRule := range []string{
		"数值许可",
		"封闭数据模式",
		"审慎排序",
		"脚本使用 `run_script` 执行",
		`"--value", "A=8.0"`,
		"封闭数据硬分支",
		"允许数字表",
		"综合排序：证据不足",
		"文字方向与实际顺序一致",
		"上行/下行情景属于定性假设",
		"限制因果与趋势",
		"不能把少量指标包装成完整“经营质量”",
		"只能改变用户已提供的字段",
		"本样本内顺序同向",
		"封闭数据报告结构",
		"未获许可的情景数字",
		"联网真实性",
		"可视化真实性",
		"scripts/build-mermaid-chart.py",
		"定量图表不得使用文生图模型",
		"交付语言",
		"成品完整性",
		"存在有限工具但不足以完成本题",
		"OPEN_WEB",
		"URL_ONLY",
		"LIMITED",
		"联网检索调用集合为空",
	} {
		if !strings.Contains(skillInstructions, requiredRule) {
			t.Fatalf("market researcher instructions missing guardrail %q", requiredRule)
		}
	}

	dataGuide := string(pkg.Files["references/data-and-source-routing.md"])
	for _, requiredRule := range []string{"数值许可规则", "封闭数据模式", "不得一边添加假设"} {
		if !strings.Contains(dataGuide, requiredRule) {
			t.Fatalf("market researcher data guide missing guardrail %q", requiredRule)
		}
	}

	compsGuide := string(pkg.Files["references/comps-analysis.md"])
	for _, requiredRule := range []string{"不能据此自动列为第一候选", "不计算相关系数", "不自行填入百分比或倍数"} {
		if !strings.Contains(compsGuide, requiredRule) {
			t.Fatalf("market researcher comps guide missing guardrail %q", requiredRule)
		}
	}

	ideaGuide := string(pkg.Files["references/idea-generation.md"])
	if !strings.Contains(ideaGuide, "默认不生成综合分和总排名") {
		t.Fatal("market researcher idea generation must not force a default ranking")
	}

	onlineGuide := string(pkg.Files["references/online-research.md"])
	for _, requiredRule := range []string{"联网能力门", "OPEN_WEB", "URL_ONLY", "LIMITED", "联网工具调用集合必须为空", "get_WikipediaToolkit_methods", "WebSearchToolkit", "每次查询只表达一个搜索意图", "get_content", "搜索摘要只能用于发现线索", "联网失败的固定降级", "检索范围或主题相关性不足", "不得复制模板中的固定旧年份"} {
		if !strings.Contains(onlineGuide, requiredRule) {
			t.Fatalf("market researcher online research guide missing rule %q", requiredRule)
		}
	}

	publicWebGuide := string(pkg.Files["references/public-web-source-playbook.md"])
	for _, requiredRule := range []string{"官方定向查询", "搜索结果返回的原始 URL", "最小证据闭环", "公开网页搜索不能补齐实时行情"} {
		if !strings.Contains(publicWebGuide, requiredRule) {
			t.Fatalf("market researcher public web guide missing rule %q", requiredRule)
		}
	}

	visualGuide := string(pkg.Files["references/visualization-guidelines.md"])
	for _, requiredRule := range []string{"不得使用文生图模型绘制定量图表", "scripts/build-mermaid-chart.py", "图表数据与邻近表格逐项一致"} {
		if !strings.Contains(visualGuide, requiredRule) {
			t.Fatalf("market researcher visualization guide missing rule %q", requiredRule)
		}
	}

	script := string(pkg.Files["scripts/calculate-comps.py"])
	for _, requiredFlag := range []string{"--metric", "--value"} {
		if !strings.Contains(script, requiredFlag) {
			t.Fatalf("market researcher script missing safe input flag %q", requiredFlag)
		}
	}
	if strings.Contains(script, "closed_data_report_guardrails") {
		t.Fatal("market researcher calculation script must not contain report behavior instructions")
	}

	chartScript := string(pkg.Files["scripts/build-mermaid-chart.py"])
	for _, requiredFlag := range []string{"--chart", "--point", "--data-json", "xychart-beta", "quadrantChart"} {
		if !strings.Contains(chartScript, requiredFlag) {
			t.Fatalf("market researcher chart script missing capability %q", requiredFlag)
		}
	}
}
