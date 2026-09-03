from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path
from xml.etree import ElementTree as ET


REPOSITORY_ROOT = Path(__file__).parents[3]
SKILL_ROOT = REPOSITORY_ROOT / "skills" / "design" / "diagram-design"
SELF_CHECK = SKILL_ROOT / "scripts" / "self_check.py"
MERMAID_EXTRACT = SKILL_ROOT / "scripts" / "mermaid_extract.py"
DRAWIO_EXTRACT = SKILL_ROOT / "scripts" / "drawio_extract.py"
EXPORT_SVG = SKILL_ROOT / "scripts" / "export_svg.py"
APPLY_MOTION_CONTROLLER = SKILL_ROOT / "scripts" / "apply_motion_controller.py"
VERIFY_GEOMETRY = SKILL_ROOT / "scripts" / "verify-geometry.py"
VERIFY_MOTION = SKILL_ROOT / "scripts" / "verify-motion.py"
VERIFY_TREEMAP = SKILL_ROOT / "scripts" / "verify-treemap.py"
VERIFY_SLOPEGRAPH = SKILL_ROOT / "scripts" / "verify-slopegraph.py"
VERIFY_DUMBBELL = SKILL_ROOT / "scripts" / "verify-dumbbell.py"
VERIFY_SEQUENCE_OAUTH = SKILL_ROOT / "scripts" / "verify-sequence-oauth.py"


def run_python(script: Path, *args: str | Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, "-X", "utf8", str(script), *map(str, args)],
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )


def run_self_check(*paths: Path) -> subprocess.CompletedProcess[str]:
    return run_python(SELF_CHECK, *paths)


def valid_static_diagram(title: str = "中文系统架构") -> str:
    return f"""<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>{title}</title></head>
<body>
<svg viewBox="0 0 960 600" role="img" aria-labelledby="diagram-title diagram-desc">
  <title id="diagram-title">{title}</title>
  <desc id="diagram-desc">展示入口、服务与数据存储之间关系的静态图。</desc>
  <rect width="960" height="600" fill="#f5f5f5"/>
</svg>
</body>
</html>
"""


def test_phase_four_resources_are_complete_and_all_root_paths_exist() -> None:
    skill_text = (SKILL_ROOT / "SKILL.md").read_text(encoding="utf-8")
    resource_paths = set(
        re.findall(
            r"(?:references|assets|scripts)/[a-z0-9_-]+(?:/[a-z0-9_.-]+)*\.(?:md|html|py)",
            skill_text,
        )
    )

    assert resource_paths
    for relative_path in sorted(resource_paths):
        assert (SKILL_ROOT / relative_path).is_file(), relative_path

    type_references = sorted((SKILL_ROOT / "references").glob("type-*.md"))
    examples = sorted((SKILL_ROOT / "assets").glob("example-*.html"))
    animated_examples = [path for path in examples if path.name.endswith("-animated.html")]
    static_examples = [path for path in examples if path not in animated_examples]
    assert len(type_references) == 38
    assert len(static_examples) == 52
    assert len(animated_examples) == 3
    routing_section = skill_text.split("## 38 类图形路由", 1)[1].split("## 上游 Reference", 1)[0]
    assert len(
        re.findall(
            r"\| `references/type-[^`]+\.md` \| `assets/example-[^`]+\.html` \|",
            routing_section,
        )
    ) == 38
    assert {path.name for path in (SKILL_ROOT / "scripts").glob("*.py")} == {
        "apply_motion_controller.py",
        "self_check.py",
        "mermaid_extract.py",
        "drawio_extract.py",
        "export_svg.py",
        "verify-motion.py",
        "verify-geometry.py",
        "verify-treemap.py",
        "verify-slopegraph.py",
        "verify-dumbbell.py",
        "verify-sequence-oauth.py",
    }


def test_phase_four_instructions_match_lazymind_delivery_contract() -> None:
    skill_text = (SKILL_ROOT / "SKILL.md").read_text(encoding="utf-8")

    for required in (
        "write_file",
        "scripts/self_check.py",
        'args=["<write_file 返回的绝对 path>"]',
        "save_chat_artifact",
        'content_type="file"',
        "parameters error",
        "第四阶段不支持",
        "references/import-mermaid.md",
        "references/import-drawio.md",
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
        "find_user_attachment",
        "fidelity ledger",
        "PNG 不在第四阶段范围内",
        "中文字体规则",
    ):
        assert required in skill_text

    assert "https://github.com/cathrynlavery/diagram-design" in skill_text
    assert "Copyright (c) 2025 Cathryn Lavery" in (SKILL_ROOT / "LICENSE").read_text(encoding="utf-8")


def test_templates_have_chinese_font_fallbacks_and_no_scripts() -> None:
    for name in ("template.html", "template-dark.html", "template-full.html"):
        template = (SKILL_ROOT / "assets" / name).read_text(encoding="utf-8")
        assert "PingFang SC" in template
        assert "Microsoft YaHei" in template
        assert "Noto Sans CJK SC" in template
        assert "<script" not in template.casefold()

    terminal = (SKILL_ROOT / "assets" / "template-terminal.html").read_text(
        encoding="utf-8"
    )
    assert "Noto Sans Mono CJK SC" in terminal
    assert "Microsoft YaHei" in terminal
    assert "<script" not in terminal.casefold()

    motion = (SKILL_ROOT / "assets" / "template-motion.html").read_text(
        encoding="utf-8"
    )
    assert "PingFang SC" in motion
    assert "Noto Sans Mono CJK SC" in motion
    assert motion.casefold().count("<script") == 1
    assert "<script data-diagram-controls>" in motion


def test_self_check_accepts_static_chinese_html(tmp_path: Path) -> None:
    diagram = tmp_path / "diagram.html"
    diagram.write_text(valid_static_diagram(), encoding="utf-8")

    completed = run_self_check(diagram)

    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert f"OK {diagram}" in completed.stdout


def test_self_check_rejects_noncanonical_scripts_invalid_motion_and_remote_assets(
    tmp_path: Path,
) -> None:
    unsafe = tmp_path / "unsafe.html"
    unsafe.write_text(
        valid_static_diagram().replace(
            "</body>",
            '<div data-motion-root data-motion-mode="step"></div>'
            '<img src="https://example.com/remote.png">'
            '<script>alert("x")</script></body>',
        ),
        encoding="utf-8",
    )

    completed = run_self_check(unsafe)

    assert completed.returncode == 1
    assert "must carry only the canonical data-diagram-controls attribute" in completed.stdout
    assert "data-step-count must be an ASCII decimal integer" in completed.stdout
    assert "controlled mode needs one in-root control group" in completed.stdout
    assert "remote reference on <img>" in completed.stdout


def test_all_bundled_examples_pass_self_check() -> None:
    examples = sorted((SKILL_ROOT / "assets").glob("example-*.html"))

    completed = run_self_check(*examples)

    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert completed.stdout.count("OK ") == 55


def test_bundled_motion_assets_pass_motion_contract() -> None:
    motion_assets = [SKILL_ROOT / "assets" / "template-motion.html"]
    motion_assets.extend(sorted((SKILL_ROOT / "assets").glob("example-*-animated.html")))

    completed = run_python(VERIFY_MOTION, *motion_assets)

    assert completed.returncode == 0, completed.stdout + completed.stderr
    assert completed.stdout.count("OK ") == 4


def test_motion_contract_rejects_modified_controller(tmp_path: Path) -> None:
    source = (SKILL_ROOT / "assets" / "template-motion.html").read_text(
        encoding="utf-8"
    )
    modified = tmp_path / "modified-controller.html"
    modified.write_text(
        source.replace(
            "const params = new URLSearchParams(location.search);",
            "const params = new URLSearchParams(location.search); void 0;",
            1,
        ),
        encoding="utf-8",
    )

    self_checked = run_self_check(modified)
    motion_checked = run_python(VERIFY_MOTION, modified)

    assert self_checked.returncode == 1
    assert "must exactly match the controller" in self_checked.stdout
    assert motion_checked.returncode == 1
    assert "must exactly match the controller" in motion_checked.stdout


def test_motion_controller_installer_repairs_modified_controller(
    tmp_path: Path,
) -> None:
    source = (SKILL_ROOT / "assets" / "template-motion.html").read_text(
        encoding="utf-8"
    )
    target = tmp_path / "modified-controller.html"
    target.write_text(
        source.replace(
            "const params = new URLSearchParams(location.search);",
            "const params = new URLSearchParams(location.search); void 0;",
            1,
        ),
        encoding="utf-8",
    )

    installed = run_python(APPLY_MOTION_CONTROLLER, target)
    self_checked = run_self_check(target)
    motion_checked = run_python(VERIFY_MOTION, target)

    assert installed.returncode == 0, installed.stdout + installed.stderr
    assert json.loads(installed.stdout)["action"] == "replaced"
    assert self_checked.returncode == 0, self_checked.stdout + self_checked.stderr
    assert motion_checked.returncode == 0, motion_checked.stdout + motion_checked.stderr


def test_motion_controller_installer_inserts_controller_into_scriptless_html(
    tmp_path: Path,
) -> None:
    source = (SKILL_ROOT / "assets" / "template-motion.html").read_text(
        encoding="utf-8"
    )
    script = re.search(
        r"<script\b[^>]*>.*?</script\s*>", source, re.IGNORECASE | re.DOTALL
    )
    assert script is not None
    target = tmp_path / "scriptless-motion.html"
    target.write_text(source[: script.start()] + source[script.end() :], encoding="utf-8")

    installed = run_python(APPLY_MOTION_CONTROLLER, target)
    self_checked = run_self_check(target)
    motion_checked = run_python(VERIFY_MOTION, target)

    assert installed.returncode == 0, installed.stdout + installed.stderr
    payload = json.loads(installed.stdout)
    assert payload["action"] == "inserted"
    assert payload["path"] == str(target.resolve())
    assert self_checked.returncode == 0, self_checked.stdout + self_checked.stderr
    assert motion_checked.returncode == 0, motion_checked.stdout + motion_checked.stderr


def test_motion_controller_installer_rejects_extra_script_without_mutation(
    tmp_path: Path,
) -> None:
    source = (SKILL_ROOT / "assets" / "template-motion.html").read_text(
        encoding="utf-8"
    )
    target = tmp_path / "unsafe-motion.html"
    unsafe = source.replace("</body>", "<script>alert('no')</script>\n</body>", 1)
    target.write_text(unsafe, encoding="utf-8")

    installed = run_python(APPLY_MOTION_CONTROLLER, target)

    assert installed.returncode == 2
    assert "must not contain multiple scripts" in installed.stderr
    assert target.read_text(encoding="utf-8") == unsafe


def test_terminal_sketchy_and_special_examples_have_expected_grammar() -> None:
    terminal = (SKILL_ROOT / "assets" / "example-loop-terminal.html").read_text(
        encoding="utf-8"
    )
    assert "--accent: #ff5a36" in terminal
    assert "titlebar" in terminal
    assert "Noto Sans Mono CJK SC" in terminal
    assert "<script" not in terminal.casefold()

    sketchy = (
        SKILL_ROOT / "assets" / "example-architecture-sketchy.html"
    ).read_text(encoding="utf-8")
    assert "<feTurbulence" in sketchy
    assert "<feDisplacementMap" in sketchy
    geometry_group = re.search(
        r'<g filter="url\(#sketchy-architecture-filter\)">(.*?)</g>',
        sketchy,
        re.DOTALL,
    )
    assert geometry_group is not None
    assert "<text" not in geometry_group.group(1).casefold()

    for name in (
        "example-quadrant-consultant.html",
        "example-sequence-oauth.html",
        "example-sequence-oauth-dark.html",
        "example-sequence-oauth-full.html",
        "example-slopegraph.html",
        "example-slopegraph-dark.html",
        "example-slopegraph-full.html",
        "example-high-level-vertical.html",
        "example-high-level-vertical-dark.html",
        "example-high-level-vertical-full.html",
    ):
        assert (SKILL_ROOT / "assets" / name).is_file(), name


def test_bundled_special_examples_pass_type_specific_validators() -> None:
    checks = (
        run_python(
            VERIFY_GEOMETRY,
            SKILL_ROOT / "assets" / "example-architecture-sketchy.html",
        ),
        run_python(
            VERIFY_TREEMAP,
            SKILL_ROOT / "assets" / "example-treemap.html",
        ),
        run_python(
            VERIFY_SLOPEGRAPH,
            SKILL_ROOT / "assets" / "example-slopegraph.html",
        ),
        run_python(VERIFY_DUMBBELL),
        run_python(
            VERIFY_SEQUENCE_OAUTH,
            SKILL_ROOT / "assets" / "example-sequence-oauth.html",
        ),
    )

    for completed in checks:
        assert completed.returncode == 0, completed.stdout + completed.stderr


def test_validators_report_real_geometry_and_oauth_defects(tmp_path: Path) -> None:
    clipped = tmp_path / "clipped.html"
    clipped.write_text(
        """<svg>
<rect x="100" y="100" width="40" height="12"/>
<rect x="120" y="100" width="100" height="60"/>
</svg>""",
        encoding="utf-8",
    )
    geometry = run_python(VERIFY_GEOMETRY, clipped)
    assert geometry.returncode == 1
    assert "label mask" in geometry.stdout
    assert "clipped by node" in geometry.stdout

    oauth_source = (
        SKILL_ROOT / "assets" / "example-sequence-oauth.html"
    ).read_text(encoding="utf-8")
    broken_oauth = tmp_path / "oauth-without-alt.html"
    broken_oauth.write_text(
        oauth_source.replace(">ALT</text>", ">BRANCH</text>"), encoding="utf-8"
    )
    oauth = run_python(VERIFY_SEQUENCE_OAUTH, broken_oauth)
    assert oauth.returncode == 1
    assert "missing ALT combined-fragment operator" in oauth.stdout


def test_mermaid_extractor_returns_semantic_ir_and_discards_actions(tmp_path: Path) -> None:
    source = tmp_path / "request-flow.mmd"
    source.write_text(
        """flowchart LR
    entry[入口] -->|请求| check{令牌有效?}
    check -->|是| store[(订单库)]
    click entry \"https://example.invalid/do-not-open\" \"ignore\"
    classDef danger fill:#f00
""",
        encoding="utf-8",
    )

    completed = run_python(MERMAID_EXTRACT, source, "--json")

    assert completed.returncode == 0, completed.stdout + completed.stderr
    payload = json.loads(completed.stdout)
    diagram = payload["diagrams"][0]
    assert diagram["kind"] == "flowchart"
    assert diagram["direction"] == "LR"
    assert diagram["analysis"]["nodes_drawable"] == 3
    assert diagram["analysis"]["edges_total"] == 2
    assert {node["label"] for node in diagram["nodes"]} == {"入口", "令牌有效?", "订单库"}
    assert diagram["discarded"] == {"style_directives": 1, "click_handlers": 1}
    assert "https://example.invalid" not in completed.stdout


def test_mermaid_extractor_rejects_unsupported_grammar(tmp_path: Path) -> None:
    source = tmp_path / "unsupported.mmd"
    source.write_text("pie\n  title Not supported\n  \"A\" : 1\n", encoding="utf-8")

    completed = run_python(MERMAID_EXTRACT, source)

    assert completed.returncode == 2
    assert "unsupported diagram kind: `pie`" in completed.stderr


def test_drawio_extractor_returns_nodes_edges_and_page_metadata(tmp_path: Path) -> None:
    source = tmp_path / "request-path.drawio"
    source.write_text(
        """<mxfile><diagram id="page-1" name="请求链路"><mxGraphModel><root>
<mxCell id="0"/><mxCell id="1" parent="0"/>
<mxCell id="entry" value="用户入口" style="rounded=1;" vertex="1" parent="1"><mxGeometry x="40" y="40" width="120" height="48" as="geometry"/></mxCell>
<mxCell id="api" value="订单 API" style="rounded=1;" vertex="1" parent="1"><mxGeometry x="240" y="40" width="120" height="48" as="geometry"/></mxCell>
<mxCell id="edge" value="提交" edge="1" source="entry" target="api" parent="1"><mxGeometry relative="1" as="geometry"/></mxCell>
</root></mxGraphModel></diagram></mxfile>""",
        encoding="utf-8",
    )

    completed = run_python(DRAWIO_EXTRACT, source, "--json")

    assert completed.returncode == 0, completed.stdout + completed.stderr
    payload = json.loads(completed.stdout)
    page = payload["pages"][0]
    assert payload["pages_total"] == 1
    assert page["name"] == "请求链路"
    assert page["analysis"]["nodes_drawable"] == 2
    assert page["analysis"]["edges_total"] == 1
    assert {node["label"] for node in page["nodes"]} == {"用户入口", "订单 API"}
    assert page["edges"][0]["label"] == "提交"


def test_svg_export_is_standalone_accessible_and_non_destructive(tmp_path: Path) -> None:
    source = tmp_path / "diagram.html"
    source_text = valid_static_diagram()
    source.write_text(source_text, encoding="utf-8")
    output = tmp_path / "diagram-vector.svg"

    checked = run_self_check(source)
    exported = run_python(EXPORT_SVG, source, "--out", output)

    assert checked.returncode == 0, checked.stdout + checked.stderr
    assert exported.returncode == 0, exported.stdout + exported.stderr
    result = json.loads(exported.stdout)
    assert result["status"] == "ok"
    assert result["output"] == str(output.resolve())
    assert result["bytes"] == output.stat().st_size
    assert b"\r\n" not in output.read_bytes()
    assert source.read_text(encoding="utf-8") == source_text

    root = ET.parse(output).getroot()
    namespace = "{http://www.w3.org/2000/svg}"
    assert root.tag == namespace + "svg"
    assert root.attrib["viewBox"] == "0 0 960 600"
    assert root.find(namespace + "title") is not None
    assert root.find(namespace + "desc") is not None
    assert "fonts.googleapis.com/css2" in output.read_text(encoding="utf-8")

    refused = run_python(EXPORT_SVG, source, "--out", output)
    assert refused.returncode == 2
    assert "already exists" in refused.stderr

    overwritten = run_python(EXPORT_SVG, source, "--out", output, "--overwrite")
    assert overwritten.returncode == 0, overwritten.stdout + overwritten.stderr
    assert json.loads(overwritten.stdout)["bytes"] == output.stat().st_size
    assert b"\r\n" not in output.read_bytes()


def test_svg_export_turns_motion_html_into_complete_static_frame(tmp_path: Path) -> None:
    source = tmp_path / "motion.html"
    source.write_text(
        (SKILL_ROOT / "assets" / "template-motion.html").read_text(encoding="utf-8"),
        encoding="utf-8",
    )
    output = tmp_path / "motion.svg"

    self_checked = run_self_check(source)
    motion_checked = run_python(VERIFY_MOTION, source)
    exported = run_python(EXPORT_SVG, source, "--out", output)

    assert self_checked.returncode == 0, self_checked.stdout + self_checked.stderr
    assert motion_checked.returncode == 0, motion_checked.stdout + motion_checked.stderr
    assert exported.returncode == 0, exported.stdout + exported.stderr
    svg = output.read_text(encoding="utf-8")
    assert "[data-motion-decorative]{display:none!important;}" in svg
    assert "[data-motion-item]{opacity:1!important;transform:none!important;" in svg
    assert "<script" not in svg.casefold()
    ET.parse(output)


def test_svg_export_rejects_executable_svg(tmp_path: Path) -> None:
    source = tmp_path / "unsafe.html"
    source.write_text(
        valid_static_diagram().replace(
            "  <rect", '  <script>alert("no")</script>\n  <rect', 1
        ),
        encoding="utf-8",
    )

    completed = run_python(EXPORT_SVG, source, "--out", tmp_path / "unsafe.svg")

    assert completed.returncode == 2
    assert "contains a script tag" in completed.stderr
