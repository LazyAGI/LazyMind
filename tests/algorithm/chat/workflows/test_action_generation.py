import importlib.util
import json
from pathlib import Path

import pytest
from lazymind.chat.workflow.action_generation import GenerationRequired, generate, generation_scope


def test_replay_is_pinned_and_context_is_reset():
    with generation_scope([]):
        with pytest.raises(GenerationRequired) as pending:
            generate('shared rules', 'selected text')
    request = pending.value.request
    assert generate('shared rules', 'selected text') is None
    response = {'request_hash': request['request_hash'], 'text': 'answer'}
    with generation_scope([response]):
        assert generate('shared rules', 'selected text') == 'answer'
        with pytest.raises(GenerationRequired) as second:
            generate('second request', 'context')
        assert second.value.request['index'] == 1
    with generation_scope([response]), pytest.raises(ValueError):
        generate('changed rules', 'selected text')
    assert generate('native', 'request') is None


@pytest.mark.parametrize('instruction,plan,expected', [
    ('让副标题更精简', {'op': 'replace_text', 'value': 'Clear subtitle'}, '>Clear subtitle</h2>'),
    ('把副标题往下挪一点', {'op': 'set_style', 'styles': {'margin-top': '8px'}}, 'style="margin-top: 8px">Original'),
])
def test_real_ppt_planner_uses_external_generation_and_validates(tmp_path, monkeypatch, instruction, plan, expected):
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('external_action_ppt_test', path)
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    monkeypatch.setattr(tools, 'available_font_families', lambda: ['Arial'])
    html = '<!doctype html><html><head><title>Title</title></head><body><h1 data-el="title">Keep</h1><h2 data-el="subtitle">Original</h2></body></html>'
    args = dict(artifact=html, instruction=instruction, selection={'type': 'ppt_html', 'el': 'subtitle', 'page': 1}, artifact_store=str(tmp_path), slot='preview_html')
    with generation_scope([]), pytest.raises(GenerationRequired) as pending:
        tools.ppt_preview_selection_edit(**args)
    request = pending.value.request
    assert json.loads(request['user_prompt'])['selected_element']['text'] == 'Original'
    with generation_scope([{'request_hash': request['request_hash'], 'text': json.dumps(plan)}]):
        preview = tools.ppt_preview_selection_edit(**args)
    assert expected in preview['candidate_html']
    assert '>Keep</h1>' in preview['candidate_html']
    committed = tools.ppt_apply_selection_edit(preview['commit']['token'], artifact=html, artifact_store=str(tmp_path), slot='preview_html')
    assert committed['artifact']['value'] == preview['candidate_html']
    assert committed['native_companion'] is None
    with generation_scope([{'request_hash': request['request_hash'], 'text': '{"op":"arbitrary_code","value":"bad"}'}]), pytest.raises(ValueError):
        tools.ppt_preview_selection_edit(**args)


def test_native_companion_preserves_charts_and_rejects_ambiguous_selection(tmp_path):
    import zipfile
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('external_action_native_test', path)
    tools = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(tools)
    source, output = tmp_path / 'source.pptx', tmp_path / 'candidate.pptx'
    xml = '<a:p xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:r><a:t>Original</a:t></a:r></a:p>'
    with zipfile.ZipFile(source, 'w') as deck:
        deck.writestr('ppt/slides/slide1.xml', xml)
        deck.writestr('ppt/charts/chart1.xml', '<chart>unchanged</chart>')
    tools._patch_native_selection(source, output, 1, 'Original', 'Clear title', [{'op': 'replace_text'}])
    with zipfile.ZipFile(output) as deck:
        assert b'Clear title' in deck.read('ppt/slides/slide1.xml')
        assert deck.read('ppt/charts/chart1.xml') == b'<chart>unchanged</chart>'
    with pytest.raises(ValueError):
        tools._patch_native_selection(source, output, 1, 'Missing', 'Changed', [{'op': 'replace_text'}])
    with pytest.raises(ValueError):
        tools._patch_native_selection(source, output, 1, 'Original', 'Changed', [{'op': 'set_style'}])
