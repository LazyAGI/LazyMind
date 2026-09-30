from __future__ import annotations

import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest
from lazyllm.tools.agent import ToolExecutionError
from PIL import Image

MODULE_PATH = Path(__file__).resolve().parents[3] / 'workflows' / 'image-workflow-v2' / 'scripts' / 'baoyu.py'
SPEC = importlib.util.spec_from_file_location('image_workflow_v2_baoyu', MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)


def test_supported_aspect_ratios_use_explicit_seedream_sizes():
    assert module._size_for_aspect_ratio('1:1') == '2048x2048'
    assert module._size_for_aspect_ratio('16:9') == '2688x1512'
    assert module._size_for_aspect_ratio('9:16') == '1512x2688'
    with pytest.raises(ToolExecutionError, match='Unsupported aspect_ratio'):
        module._size_for_aspect_ratio('5:4')


def test_editor_preserves_source_ratio_when_no_ratio_is_requested(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
):
    source = tmp_path / 'source.png'
    Image.new('RGB', (1600, 900), 'white').save(source)
    captured = {}

    monkeypatch.setattr(
        module,
        '_resolve_source_image_paths',
        lambda urls: [str(source)],
    )

    def fake_run(prompt, *, files, image_size):
        captured.update(prompt=prompt, files=files, image_size=image_size)
        return {'local_path': 'edited.png'}

    monkeypatch.setattr(module, '_run_seedream', fake_run)

    result = module.baoyu_image_editor('Change only the hat.', [str(source)])

    assert result == {'local_path': 'edited.png'}
    assert captured == {
        'prompt': 'Change only the hat.',
        'files': [str(source)],
        'image_size': '2728x1536',
    }


def test_editor_requires_one_authoritative_source():
    with pytest.raises(ToolExecutionError, match='exactly one authoritative'):
        module.baoyu_image_editor('edit', [])
    with pytest.raises(ToolExecutionError, match='exactly one authoritative'):
        module.baoyu_image_editor('edit', ['a.png', 'b.png'])


def test_seedream_model_is_pinned_and_uses_session_ark_key(
    monkeypatch: pytest.MonkeyPatch,
):
    captured = {}
    sentinel = object()

    monkeypatch.setattr(
        module,
        'effective_env_value',
        lambda name: 'session-key' if name == 'ARK_API_KEY' else '',
    )

    def fake_online_module(**kwargs):
        captured.update(kwargs)
        return sentinel

    monkeypatch.setattr(module.lazyllm, 'OnlineMultiModalModule', fake_online_module)

    assert module._seedream_model(editing=True) is sentinel
    assert captured == {
        'source': 'doubao',
        'model': 'doubao-seedream-5-0-260128',
        'type': 'image_editing',
        'api_key': 'session-key',
    }


def test_seedream_key_failure_is_structured(monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setattr(module, 'effective_env_value', lambda name: '')
    monkeypatch.setattr(module.lazyllm, 'config', {'doubao_api_key': ''})

    with pytest.raises(ToolExecutionError) as raised:
        module._seedream_api_key()

    assert raised.value.missing_env == ['ARK_API_KEY']


def test_seedream_result_records_adapter_provenance(monkeypatch: pytest.MonkeyPatch):
    sentinel = object()
    captured = {}
    monkeypatch.setattr(module, '_seedream_model', lambda *, editing: sentinel)

    def fake_run(model, prompt, **kwargs):
        captured.update(model=model, prompt=prompt, **kwargs)
        return {'local_path': 'generated.png', **kwargs['metadata']}

    monkeypatch.setattr(module, 'run_image_model_instance', fake_run)

    result = module._run_seedream('A fox', files=None, image_size='2048x2048')

    assert captured['model'] is sentinel
    assert captured['files'] is None
    assert captured['batch_size'] == 1
    assert captured['model_call_options'] == {
        'size': '2048x2048',
        'watermark': False,
        'guidance_scale': None,
    }
    assert result['adapter'] == 'baoyu-image-gen'
    assert result['provider'] == 'seedream'
    assert result['model'] == 'doubao-seedream-5-0-260128'
    assert result['attempts'] == 1


@pytest.mark.parametrize(
    ('launch_request', 'expected'),
    [
        ('生成 16:9 横版插画', '16:9'),
        ('make a portrait poster', '9:16'),
        ('生成横向风景', '16:9'),
        ('正方形头像', '1:1'),
        ('no ratio given', '1:1'),
    ],
)
def test_requested_aspect_ratio_is_deterministic(launch_request: str, expected: str):
    assert module._requested_aspect_ratio(launch_request) == expected


def test_generate_ordinary_image_only_publishes_real_provider_file(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
):
    generated = tmp_path / 'generated.png'
    generated.write_bytes(b'png')
    saved = []
    monkeypatch.setattr(module, '_artifact_text', lambda key: 'A spring valley')
    monkeypatch.setattr(module, '_artifact_image_refs', lambda *keys: [])
    monkeypatch.setattr(module, '_immutable_request', lambda: '16:9 横版')
    monkeypatch.setattr(
        module,
        'baoyu_image_generator',
        lambda prompt, aspect_ratio, urls: {
            'images': [{'local_path': str(generated), 'image_url': '/static-files/x'}],
            'provider': 'seedream',
            'model': 'seedream-5',
            'image_size': '2688x1512',
        },
    )
    fake_tools = SimpleNamespace(
        _save_artifact=lambda *args, **kwargs: saved.append((args, kwargs)) or {'status': 'ok'}
    )
    monkeypatch.setitem(__import__('sys').modules, 'lazymind.chat.engine.subagent.tools', fake_tools)

    result = module.generate_ordinary_image()

    assert result['saved_count'] == 1
    assert saved[0][0] == ('ordinary_output', {'path': str(generated)})
    assert saved[0][1]['internal_publish'] is True
    assert saved[0][1]['publisher_list_index'] == 0


def test_generate_ordinary_image_cannot_publish_after_provider_failure(
    monkeypatch: pytest.MonkeyPatch,
):
    monkeypatch.setattr(module, '_artifact_text', lambda key: 'prompt')
    monkeypatch.setattr(module, '_artifact_image_refs', lambda *keys: [])
    monkeypatch.setattr(module, '_immutable_request', lambda: '1:1')
    monkeypatch.setattr(
        module,
        'baoyu_image_generator',
        lambda *args, **kwargs: (_ for _ in ()).throw(ToolExecutionError('provider failed')),
    )

    with pytest.raises(ToolExecutionError, match='provider failed'):
        module.generate_ordinary_image()


def test_generate_edit_candidate_uses_contract_and_real_source(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
):
    generated = tmp_path / 'edited.png'
    generated.write_bytes(b'png')
    captured = {}
    saved = []
    monkeypatch.setattr(module, '_artifact_json', lambda key: {
        'requested_edit': 'Make the hat blue',
        'edit_scope': 'Hat only',
        'preserve': 'Identity and background',
        'do_not': 'Change the face',
    })
    monkeypatch.setattr(
        module,
        '_artifact_image_refs',
        lambda *keys: ['C:/source.png'],
    )
    monkeypatch.setattr(module, '_immutable_request', lambda: '把帽子改成蓝色')

    def fake_editor(prompt, urls, aspect_ratio):
        captured.update(prompt=prompt, urls=urls, aspect_ratio=aspect_ratio)
        return {
            'images': [{'local_path': str(generated)}],
            'provider': 'seedream',
            'model': 'seedream-5',
            'image_size': '2048x2048',
        }

    monkeypatch.setattr(module, 'baoyu_image_editor', fake_editor)
    fake_tools = SimpleNamespace(
        _save_artifact=lambda *args, **kwargs: saved.append((args, kwargs)) or {'status': 'ok'}
    )
    monkeypatch.setitem(__import__('sys').modules, 'lazymind.chat.engine.subagent.tools', fake_tools)

    result = module.generate_edit_candidate()

    assert result['saved_count'] == 1
    assert captured['urls'] == ['C:/source.png']
    assert captured['aspect_ratio'] is None
    assert captured['prompt'].splitlines() == [
        'Requested edit: Make the hat blue',
        'Edit scope: Hat only',
        'Preserve: Identity and background',
        'Do not: Change the face',
    ]
    assert saved[0][0] == ('edit_candidate', {'path': str(generated)})
    assert saved[0][1]['internal_publish'] is True
    assert 'publisher_list_index' not in saved[0][1]
