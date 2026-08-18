import ast
import json
import subprocess
import sys
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).parents[3]
SKILL_ROOT = REPOSITORY_ROOT / "skills" / "research" / "market-researcher"
COMPS_SCRIPT = SKILL_ROOT / "scripts" / "calculate-comps.py"
CHART_SCRIPT = SKILL_ROOT / "scripts" / "build-mermaid-chart.py"
SEARCH_BASE = REPOSITORY_ROOT / "algorithm" / "lazyllm" / "lazyllm" / "tools" / "tools" / "search" / "base.py"


def run_skill_script(path: Path, *args: str) -> tuple[subprocess.CompletedProcess[str], dict]:
    completed = subprocess.run(
        [sys.executable, str(path), *args],
        check=False,
        capture_output=True,
        text=True,
    )
    return completed, json.loads(completed.stdout)


def test_calculate_comps_keeps_known_baseline() -> None:
    completed, payload = run_skill_script(
        COMPS_SCRIPT,
        "--metric",
        "EV/Sales",
        "--value",
        "A=8.0",
        "--value",
        "B=5.0",
        "--value",
        "C=2.0",
    )

    assert completed.returncode == 0
    assert payload["status"] == "ok"
    statistics = payload["result"]["metrics"]["EV/Sales"]["statistics"]
    assert statistics == {"median": 5.0, "mean": 5.0, "minimum": 2.0, "maximum": 8.0}


def test_build_xy_chart_preserves_input_values_and_order() -> None:
    data = json.dumps(
        [{"period": "2022", "market_size": 100}, {"period": "2023", "market_size": 120}],
        ensure_ascii=False,
    )
    completed, payload = run_skill_script(
        CHART_SCRIPT,
        "--chart",
        "xy-line",
        "--title",
        "市场规模趋势",
        "--x-key",
        "period",
        "--y-key",
        "market_size",
        "--x-axis-label",
        "年份",
        "--y-axis-label",
        "亿元",
        "--data-json",
        data,
    )

    assert completed.returncode == 0
    assert payload["status"] == "ok"
    result = payload["result"]
    assert result["data_table"] == [
        {"period": "2022", "market_size": 100.0},
        {"period": "2023", "market_size": 120.0},
    ]
    assert 'x-axis "年份" ["2022", "2023"]' in result["mermaid"]
    assert "line [100, 120]" in result["mermaid"]


def test_build_xy_chart_accepts_safe_repeatable_points() -> None:
    completed, payload = run_skill_script(
        CHART_SCRIPT,
        "--chart",
        "xy-line",
        "--title",
        "市场规模趋势",
        "--x-axis-label",
        "年份",
        "--y-axis-label",
        "亿元",
        "--point",
        "2022=100",
        "--point",
        "2023=120",
    )

    assert completed.returncode == 0
    assert payload["status"] == "ok"
    assert payload["result"]["data_table"] == [
        {"x": "2022", "y": 100.0},
        {"x": "2023", "y": 120.0},
    ]
    assert 'x-axis "年份" ["2022", "2023"]' in payload["result"]["mermaid"]


def test_build_pie_chart_rejects_negative_values() -> None:
    data = json.dumps([{"label": "A", "value": 60}, {"label": "B", "value": -10}])
    completed, payload = run_skill_script(
        CHART_SCRIPT,
        "--chart",
        "pie",
        "--title",
        "样本构成",
        "--data-json",
        data,
    )

    assert completed.returncode == 2
    assert payload["status"] == "error"
    assert "negative" in payload["error"]


def test_build_quadrant_requires_prevalidated_normalized_coordinates() -> None:
    data = json.dumps([{"label": "A", "x": 1.2, "y": 0.8}])
    completed, payload = run_skill_script(
        CHART_SCRIPT,
        "--chart",
        "quadrant",
        "--title",
        "竞争定位",
        "--data-json",
        data,
    )

    assert completed.returncode == 2
    assert payload["status"] == "error"
    assert "between 0 and 1" in payload["error"]


def test_market_researcher_routes_online_and_visual_workflows() -> None:
    skill_text = (SKILL_ROOT / "SKILL.md").read_text(encoding="utf-8")
    online_text = (SKILL_ROOT / "references" / "online-research.md").read_text(encoding="utf-8")
    public_web_text = (
        SKILL_ROOT / "references" / "public-web-source-playbook.md"
    ).read_text(encoding="utf-8")
    visual_text = (SKILL_ROOT / "references" / "visualization-guidelines.md").read_text(encoding="utf-8")
    quality_text = (SKILL_ROOT / "references" / "quality-gates.md").read_text(encoding="utf-8")

    assert "references/online-research.md" in skill_text
    assert "references/public-web-source-playbook.md" in skill_text
    assert "references/visualization-guidelines.md" in skill_text
    assert "scripts/build-mermaid-chart.py" in skill_text
    assert '"--point", "2022=100"' in visual_text
    assert "## LazyMind 联网硬门" in skill_text
    assert "在任何联网、搜索、Toolkit 展开或网页抓取调用之前" in skill_text
    assert "`OPEN_WEB`" in skill_text
    assert "`URL_ONLY`" in skill_text
    assert "`LIMITED`" in skill_text
    assert "联网检索调用集合为空" in skill_text
    assert "get_WikipediaToolkit_methods" in skill_text
    assert "不得调用 Wikipedia 搜索" in skill_text
    assert "不得猜测或拼接任何 URL" in skill_text
    assert "禁止使用 `httpbin.org`" in skill_text
    assert "任何具体四位年份都视为失败" in skill_text
    assert "触发硬门后，直接按以下中性骨架结束" in skill_text
    assert "当前会话未提供可用联网检索工具" in online_text
    assert "检索范围或主题相关性不足" in online_text
    assert "最多执行 1 次诊断查询" in online_text
    assert "停止整个工具" in online_text
    assert "不得抓取搜索引擎首页、Wikipedia 首页" in online_text
    assert "不得凭模型记忆新增具体公司、产品、政策、技术趋势或市场事件" in online_text
    assert "发送前扫描所有四位年份" in online_text
    assert "不得凭模型记忆猜测 Statista" in online_text
    assert "不得使用 `httpbin.org`" in online_text
    assert "不得复制模板中的固定旧年份" in online_text
    assert "不得先完整总结一遍" in online_text
    assert "WebSearchToolkit" in online_text
    assert "盘点阶段只允许读取 Skill/Reference" in online_text
    assert "后续联网工具调用集合必须为空" in online_text
    assert "不得调用或展开 `get_WikipediaToolkit_methods`" in online_text
    assert "该结果就是停止条件" in online_text
    assert "每次查询只表达一个搜索意图" in online_text
    assert "get_content" in online_text
    assert "get_contents" in online_text
    assert "正文路径最多切换一次" in online_text
    assert "公开网页搜索不能补齐实时行情" in public_web_text
    assert "搜索结果中的标题、URL 和摘要只用于筛选" in public_web_text
    assert "搜索结果返回的原始 URL" in public_web_text
    assert "不要无限改写近义查询" in public_web_text
    assert "不得使用文生图模型绘制定量图表" in visual_text
    assert "联网证据门" in quality_text
    assert "可视化门" in quality_text
    assert "最终输出如下" in quality_text


def test_market_researcher_mermaid_contract_has_safe_direct_path_and_retry_stop() -> None:
    skill_text = (SKILL_ROOT / "SKILL.md").read_text(encoding="utf-8")
    visual_text = (
        SKILL_ROOT / "references" / "visualization-guidelines.md"
    ).read_text(encoding="utf-8")
    quality_text = (SKILL_ROOT / "references" / "quality-gates.md").read_text(encoding="utf-8")
    report_text = (SKILL_ROOT / "references" / "report-contract.md").read_text(encoding="utf-8")

    assert "只读取 `references/visualization-guidelines.md`" in skill_text
    assert "受控 Mermaid 直出" in skill_text
    assert "此路径不要调用 `run_script`" in skill_text
    assert "同一脚本、同一目的最多调用一次" in skill_text
    assert "`parameters error` 或参数类型错误是停止条件" in skill_text

    assert "数据点不超过 12 个" in visual_text
    assert "xychart-beta" in visual_text
    assert "pie showData" in visual_text
    assert '"args":["--chart","xy-line"' in visual_text
    assert '"args":"[\\"--chart\\",\\"xy-line\\"]"' in visual_text
    assert "同一图表任务最多调用一次 `run_script`" in visual_text
    assert "增加 `allow_unsafe`" in visual_text
    assert "脚本失败后不重试" in visual_text
    assert "先输出同源 Markdown 表格" in visual_text

    assert "受控直出只用于简单单序列趋势/柱状图或构成图" in quality_text
    assert "`parameters error` 后没有改名、改路径、调用 `--help`" in quality_text
    assert "简单单序列趋势/柱状图或构成图可按可视化规范的固定模板受控直出" in report_text
    assert "同一图表脚本最多调用一次" in report_text

    assert "涉及定量数据时，优先通过 `run_script`" not in visual_text
    assert "脚本失败或 Mermaid 不适合时" not in visual_text


def test_market_researcher_search_contract_matches_lazyllm_public_apis() -> None:
    tree = ast.parse(SEARCH_BASE.read_text(encoding="utf-8"))
    public_apis: list[str] | None = None

    for node in tree.body:
        if not isinstance(node, ast.ClassDef) or node.name != "SearchBase":
            continue
        for statement in node.body:
            if not isinstance(statement, ast.Assign):
                continue
            if any(isinstance(target, ast.Name) and target.id == "__public_apis__" for target in statement.targets):
                public_apis = ast.literal_eval(statement.value)
                break

    assert public_apis is not None
    assert {"search", "get_content", "get_contents"}.issubset(public_apis)

    online_text = (SKILL_ROOT / "references" / "online-research.md").read_text(encoding="utf-8")
    for method in public_apis:
        assert f"`{method}`" in online_text
