import json
from pathlib import Path

from lazymind.chat.api import workflow_routes


def _write_local_bundle(root: Path, monkeypatch) -> None:
    (root / 'node_modules' / 'pptxgenjs').mkdir(parents=True)
    (root / 'node_modules' / 'playwright').mkdir(parents=True)
    (root / 'browsers' / 'chromium_headless_shell-1208' / 'chrome-headless-shell-linux64').mkdir(parents=True)
    (root / 'html_to_pptx.mjs').write_text('', encoding='utf-8')
    (
        root
        / 'browsers'
        / 'chromium_headless_shell-1208'
        / 'chrome-headless-shell-linux64'
        / 'chrome-headless-shell'
    ).write_text('', encoding='utf-8')
    platform_name, arch = workflow_routes._local_platform_target()
    (root / 'bundle-manifest.json').write_text(
        json.dumps({'platform': platform_name, 'arch': arch}),
        encoding='utf-8',
    )
    node = root / 'node'
    node.write_text('', encoding='utf-8')
    monkeypatch.setenv('LAZYMIND_PPT_EXPORT_CLI', str(root / 'html_to_pptx.mjs'))
    monkeypatch.setenv('LAZYMIND_PPT_EXPORT_NODE', str(node))
    monkeypatch.setenv('PLAYWRIGHT_BROWSERS_PATH', str(root / 'browsers'))


def test_local_editable_ppt_ignores_feature_flag_until_bundle_exists(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'local')
    monkeypatch.setenv('LAZYMIND_OUTPUT_EDITABLE_PPT', 'true')
    monkeypatch.setenv('LAZYMIND_PPT_EXPORT_CLI', str(tmp_path / 'html_to_pptx.mjs'))

    assert workflow_routes.editable_pptx_enabled() is False


def test_local_editable_ppt_enables_after_bundle_is_detected(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'local')
    monkeypatch.setenv('LAZYMIND_OUTPUT_EDITABLE_PPT', 'false')
    _write_local_bundle(tmp_path, monkeypatch)

    assert workflow_routes.editable_pptx_enabled() is True


def test_container_editable_ppt_still_uses_deploy_flag(monkeypatch):
    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'container')
    monkeypatch.setenv('LAZYMIND_OUTPUT_EDITABLE_PPT', 'true')

    assert workflow_routes.editable_pptx_enabled() is True


def test_generated_markdown_uses_shared_heading_normalization():
    source = '# 2026 报告\n## 1. 素材来源\n正文 2026 年\n```md\n## 2. 示例\n```\n### 二、结论\n'
    assert workflow_routes.normalize_generated_document(workflow_routes.GeneratedMarkdownRequest(text=source)) == {
        'text': '# 2026 报告\n## 素材来源\n正文 2026 年\n```md\n## 2. 示例\n```\n### 结论\n',
    }
