import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest
from lazyllm.tools.agent import ToolExecutionError
from PIL import Image

MODULE_PATH = Path(__file__).resolve().parents[3] / 'workflows' / 'image-workflow-v2' / 'scripts' / 'tools.py'
SPEC = importlib.util.spec_from_file_location('image_workflow_v2_tools', MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
image_workflow_support = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(image_workflow_support)


@pytest.mark.parametrize(
    ('user_text', 'route', 'delivery_mode'),
    [
        ('生成一张春日山谷插画，不要文字。', 'ordinary', 'not_applicable'),
        ('生成一张春日山谷插画，不制作表情包，也不编辑现有图片。', 'ordinary', 'not_applicable'),
        ('最终交付普通图片，不要制作表情包，也不要编辑现有图片。', 'ordinary', 'not_applicable'),
        ('Create one landscape image, not a meme or sticker.', 'ordinary', 'not_applicable'),
        ('把我上传的图片中的杯子改成花瓶。', 'edit', 'not_applicable'),
        ('把上传的猫咪做成静态表情包。', 'meme', 'static'),
        ('用这张图制作动态 GIF 表情包。', 'meme', 'dynamic'),
        ('做一套聊天贴纸。', 'meme', 'unspecified'),
    ],
)
def test_route_classifier_publishes_exact_final_deliverable(
    monkeypatch, user_text: str, route: str, delivery_mode: str,
) -> None:
    from lazymind.chat.engine.subagent import context as context_module
    from lazymind.chat.engine.subagent import tools as subagent_tools

    calls = []
    monkeypatch.setattr(
        context_module,
        'require_context',
        lambda: SimpleNamespace(params={'user_input': user_text}),
    )
    monkeypatch.setattr(
        subagent_tools,
        '_save_artifact',
        lambda *args, **kwargs: calls.append((args, kwargs)) or {'status': 'ok'},
    )

    result = image_workflow_support.classify_image_request()

    assert result['route_plan']['route'] == route
    assert result['route_plan']['delivery_mode'] == delivery_mode
    assert calls[0][0][0] == 'route_plan'
    assert calls[0][0][1] == result['route_plan']
    assert calls[0][1]['internal_publish'] is True


def test_ordinary_preparer_preserves_the_immutable_request(monkeypatch) -> None:
    from lazymind.chat.engine.subagent import context as context_module
    from lazymind.chat.engine.subagent import tools as subagent_tools

    user_text = '生成一张16:9春日山谷插画，远处雪山，近处溪流与野花，不要文字。'
    calls = []
    monkeypatch.setattr(
        context_module,
        'require_context',
        lambda: SimpleNamespace(params={'user_input': user_text}),
    )
    monkeypatch.setattr(
        subagent_tools,
        '_save_artifact',
        lambda *args, **kwargs: calls.append((args, kwargs)) or {'status': 'ok'},
    )

    result = image_workflow_support.prepare_ordinary_request()

    assert user_text in result['ordinary_prompt']
    assert [call[0][0] for call in calls] == ['material_summary', 'ordinary_prompt']
    assert all(call[1]['internal_publish'] is True for call in calls)


def test_anchor_sheet_resolves_remote_refs_before_opening(monkeypatch, tmp_path) -> None:
    local_paths = []
    for index in range(4):
        path = tmp_path / f'anchor-{index}.png'
        Image.new('RGB', (32, 32), (index * 30, 0, 0)).save(path)
        local_paths.append(str(path))

    remote_refs = [f'https://images.example.test/anchor-{index}.png' for index in range(4)]
    resolved_refs = []

    def resolve(refs):
        resolved_refs.extend(refs)
        return local_paths

    monkeypatch.setattr(image_workflow_support, '_resolve_source_image_paths', resolve)
    monkeypatch.setattr(image_workflow_support, '_upload_root', lambda: str(tmp_path / 'uploads'))
    monkeypatch.setattr(
        image_workflow_support,
        'static_file_url_from_any',
        lambda path: f'/static-files/{Path(path).name}',
    )

    result = image_workflow_support.compose_character_anchor_sheet(remote_refs, [
        'Open eyes / closed mouth',
        'Open eyes / open mouth',
        'Closed eyes / closed mouth',
        'Closed eyes / open mouth',
    ])

    assert resolved_refs == remote_refs
    assert result['source_images'] == remote_refs
    assert result['labels'] == [
        '睁眼 · 闭嘴', '睁眼 · 张嘴', '闭眼 · 闭嘴', '闭眼 · 张嘴',
    ]
    assert Path(result['local_path']).is_file()


def test_anchor_sheet_rejects_reused_output_reference() -> None:
    duplicate = 'https://images.example.test/same-anchor.png'

    with pytest.raises(ToolExecutionError, match='four distinct tool outputs'):
        image_workflow_support.compose_character_anchor_sheet([duplicate] * 4)



def _approved_brief(captions: list[str]) -> dict:
    return {
        'delivery_mode': 'dynamic',
        'caption_mode': 'caption',
        'states': [
            {'state_id': f'state-{index}', 'caption': caption}
            for index, caption in enumerate(captions, start=1)
        ],
        'caption_source': 'provided',
        'caption_review_status': 'approved',
        'character_source': 'uploaded source image',
        'requested_count': len(captions),
        'planned_count': len(captions),
        'count_status': 'compliant',
        'allowed_count': 5,
        'requires_count_decision': False,
        'brief_status': 'approved',
        'cost_profile': 'standard',
    }


def test_meme_brief_rejects_silent_state_reduction() -> None:
    brief = _approved_brief(['求求你啦', '好不好嘛'])
    request = '请生成4个动态表情，分别是“求求你啦”、“好不好嘛”、“你最好啦”、“拜托拜托”。'

    with pytest.raises(ToolExecutionError, match='explicitly asks for 4 states'):
        image_workflow_support.validate_meme_brief_data(brief, request)


def test_meme_brief_preserves_explicit_caption_order() -> None:
    captions = ['求求你啦', '好不好嘛', '你最好啦', '拜托拜托']
    request = '请生成4个动态表情，分别是“求求你啦”、“好不好嘛”、“你最好啦”、“拜托拜托”。'

    result = image_workflow_support.validate_meme_brief_data(
        _approved_brief(captions), request,
    )

    assert result['valid'] is True
    assert result['explicit_request_count'] == 4
    assert result['state_count'] == 4
    assert result['captions_preserved'] is True


def test_meme_brief_ignores_numbered_state_labels_and_tool_json() -> None:
    request = (
        '真实生成 2 张全新的静态表情包。状态 1：字幕逐字为“开心啦”，动作是点头。'
        '状态 2：字幕逐字为“惊讶啦”，动作是递出热饮。'
        '参数必须严格等于：{"input_bindings":{"source_image":"cat.jpg"}}。'
    )
    brief = _approved_brief(['开心啦', '惊讶啦'])
    brief['delivery_mode'] = 'static'
    brief['allowed_count'] = 12

    result = image_workflow_support.validate_meme_brief_data(brief, request)

    assert result['valid'] is True
    assert result['explicit_request_count'] == 2
    assert result['captions_preserved'] is True


def test_meme_brief_accepts_declared_empty_cost_profile() -> None:
    brief = _approved_brief(['开心啦'])
    brief['cost_profile'] = {}

    result = image_workflow_support.validate_meme_brief_data(
        brief, '生成1个动态表情包，文案是“开心啦”。',
    )

    assert result['valid'] is True


def test_meme_brief_rejects_reordered_explicit_captions() -> None:
    request = '覆盖“开心啦”、“疑惑啦”、“惊讶啦”、“害羞啦”、“生气啦”这5个场景。'
    brief = _approved_brief(['疑惑啦', '开心啦', '惊讶啦', '害羞啦', '生气啦'])

    with pytest.raises(ToolExecutionError, match='preserve the launch request verbatim and in order'):
        image_workflow_support.validate_meme_brief_data(brief, request)


def test_meme_brief_publisher_uses_immutable_context_request(monkeypatch) -> None:
    from lazymind.chat.engine.subagent import context as context_module
    from lazymind.chat.engine.subagent import tools as subagent_tools

    request = '请生成4个动态表情，分别是“求求你啦”、“好不好嘛”、“你最好啦”、“拜托拜托”。'
    brief = _approved_brief(['求求你啦', '好不好嘛', '你最好啦', '拜托拜托'])
    calls = []
    monkeypatch.setattr(
        context_module,
        'require_context',
        lambda: SimpleNamespace(params={'user_input': request}),
    )
    monkeypatch.setattr(
        subagent_tools,
        '_save_artifact',
        lambda *args, **kwargs: calls.append((args, kwargs)) or {'status': 'ok'},
    )

    result = image_workflow_support.save_validated_meme_brief(brief)

    assert result['validation']['explicit_request_count'] == 4
    assert calls[0][0][:2] == ('meme_brief_data', brief)
    assert calls[0][1]['internal_publish'] is True
