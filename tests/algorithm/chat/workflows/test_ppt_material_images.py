from __future__ import annotations

import importlib.util
import io
import json
from pathlib import Path
from unittest.mock import Mock

import yaml
import pytest


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[4]


def _load_ppt_tools():
    path = _repo_root() / 'workflows' / 'ppt-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_material_tools', path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_feishu_wiki_images_use_current_connection_and_feed_ppt_materials(monkeypatch, tmp_path):
    import lazyllm
    import requests
    from PIL import Image, ImageDraw
    from lazyllm.common.globals import new_session
    from lazymind.chat.engine.tool_auth import inject_tool_config

    tools = _load_ppt_tools()
    image = Image.new('RGB', (128, 128), 'white')
    ImageDraw.Draw(image).rectangle((10, 10, 90, 90), fill='blue')
    buffer = io.BytesIO()
    image.save(buffer, format='PNG')
    original = buffer.getvalue()
    requests_made = []

    def provider(_session, method, url, **options):
        assert method == 'GET' and options['headers']['Authorization'] == 'Bearer test-feishu-access'
        assert options['stream'] and options['allow_redirects'] is False
        assert url.startswith('https://open.feishu.cn/open-apis/')
        requests_made.append(url)
        response = requests.Response()
        response.status_code = 200
        if url.endswith('/wiki/v2/spaces/get_node'):
            payload = {'node': {'obj_type': 'docx', 'obj_token': 'DocExample'}}
        elif url.endswith('/docx/v1/documents/DocExample/blocks/DocExample/children'):
            assert options['params']['with_descendants'] == 'true'
            payload = {'items': [
                {'block_id': 'root', 'block_type': 1},
                {'block_id': 'block-1', 'block_type': 27, 'image': {'token': 'image-ok'}},
                {'block_id': 'block-2', 'block_type': 27, 'image': {'token': 'image-denied'}},
                {'block_id': 'block-3', 'block_type': 23, 'file': {'token': 'file-token'}},
            ]}
        elif url.endswith('/drive/v1/medias/image-ok/download'):
            response.raw = io.BytesIO(original)
            return response
        elif url.endswith('/drive/v1/medias/image-denied/download'):
            response.status_code = 403
            payload = {}
        else:
            pytest.fail(f'Unexpected provider request {url}')
        response.raw = io.BytesIO(json.dumps({'data': payload}).encode())
        return response

    monkeypatch.setattr(requests.Session, 'request', provider)
    monkeypatch.setattr(tools, '_material_root', lambda: tmp_path)
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_inspect_material_image', lambda *a: {'status': 'inspected', 'description': '图一'})
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _: [])
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **item: published.append(item) or {'ok': True})

    document = 'https://example.feishu.cn/wiki/WikiExample'
    with new_session('ppt-feishu-material-test'):
        lazyllm.globals.config['dynamic_fs_auth'] = {}
        with pytest.raises(tools.ToolExecutionError, match='Connect a Feishu account'):
            tools.ppt_download_feishu_images(document)
        assert not requests_made
        inject_tool_config({'feishu': 'test-feishu-access'})
        listed = tools.ppt_download_feishu_images(document + '?from=share#anchor')
        assert listed['document'] == document
        assert [file['image_token'] for file in listed['images']] == ['image-ok', 'image-denied']
        with pytest.raises(tools.ToolExecutionError, match='Select tokens from image blocks'):
            tools.ppt_download_feishu_images(document, ['file-token'])
        assert not any('/medias/' in url for url in requests_made)
        result = tools.ppt_download_feishu_images(document, ['image-ok', 'image-denied', 'image-ok'])
    assert result['count'] == 1
    assert 'HTTP 403' in result['errors'][0]['error']
    assert sum(url.endswith('/image-ok/download') for url in requests_made) == 1
    path = Path(result['images'][0]['local_path'])
    assert path.suffix == '.png' and path.read_bytes() == original
    assert not published

    inspected = tools.ppt_inspect_material_images(result['images'])
    registered = tools.ppt_register_material_images(inspected['images'])
    assert registered['count'] == registered['ui_published'] == 1
    assert published[0]['key'] == 'material_images' and published[0]['content_type'] == 'image'
    assert tools._load_material_manifest()['images'][0]['origin'] == document


@pytest.mark.parametrize('response_kind', ['image', 'html', 'forbidden'])
def test_remote_registration_downloads_and_validates_bytes_before_publishing(monkeypatch, tmp_path, response_kind):
    import requests
    from PIL import Image, ImageDraw

    tools = _load_ppt_tools()
    image = Image.new('RGB', (128, 128), 'white')
    ImageDraw.Draw(image).rectangle((10, 10, 90, 90), fill='blue')
    buffer = io.BytesIO()
    image.save(buffer, format='PNG')
    data = buffer.getvalue() if response_kind == 'image' else b'<html>Access denied</html>' * 10
    response = requests.Response()
    response.status_code = 403 if response_kind == 'forbidden' else 200
    response.raw = io.BytesIO(data)
    response.close = Mock(wraps=response.close)
    # Exercise a long, extension-mismatched signed URL in the path field, too.
    origin = 'https://example.org/photo.jpg?token=' + 'x' * 1000
    requested = []

    def get(url, **options):
        requested.append((url, options))
        return response

    monkeypatch.setattr(tools.requests, 'get', get)
    monkeypatch.setattr(tools, '_material_root', lambda: tmp_path)
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_inspect_material_image', lambda path, role: {
        'status': 'inspected', 'description': 'A blue figure',
    })
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [])
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **item: published.append(item) or {'ok': True})
    if response_kind != 'image':
        with pytest.raises(tools.ToolExecutionError):
            tools.ppt_register_material_images([{'path': origin}])
        assert published == [] and not tools._material_manifest_path().exists()
    else:
        result = tools.ppt_register_material_images([{'path': origin}])
        assert result['count'] == result['ui_published'] == 1
        path = Path(published[0]['value'])
        assert path.suffix == '.png' and path.read_bytes() == data
        assert tools._load_material_manifest()['images'][0]['origin'] == origin
    assert requested[0][0] == origin and requested[0][1]['stream'] is True
    response.close.assert_called_once()


def test_ppt_material_slot_uses_same_image_list_contract_as_image_workflow():
    ppt = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'workflow.yaml').read_text(
            encoding='utf-8'))
    image = yaml.safe_load(
        (_repo_root() / 'workflows' / 'image-workflow' / 'workflow.yaml').read_text(
            encoding='utf-8'))

    ppt_slot = next(slot for slot in ppt['slots'] if slot['id'] == 'material_images')
    image_slot = next(slot for slot in image['slots'] if slot['id'] == 'material_images')
    assert ppt_slot == {
        **image_slot,
        'label': 'Material Images',
        'user_visible': True,
    }
    assert all(slot['id'] != 'academic_brief' for slot in ppt['slots'])


def test_ppt_analysis_and_material_summary_share_markdown_widget():
    ppt = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'workflow.yaml').read_text(
            encoding='utf-8'))

    assert ppt['ui']['slots']['requirement_analysis'] == {
        'widgetType': 'text-markdown',
    }
    assert ppt['ui']['slots']['material_summary'] == {
        'widgetType': 'text-markdown',
    }


def test_ppt_analysis_routes_optional_collection_and_backgrounds():
    state = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'scenario' / 'state.yml').read_text(
            encoding='utf-8'))

    transitions = state['transitions']['analyze_requirements']
    assert {item['to'] for item in transitions} == {
        'collect_materials', 'plan_background_prompts', 'build_outline',
    }
    assert all(item.get('when') for item in transitions)
    routes = {item['to']: item['when'] for item in transitions}
    assert 'choose_style' not in state['steps']
    assert 'enabled' in routes['plan_background_prompts']
    assert 'disabled' in routes['build_outline']
    assert state['steps']['analyze_requirements']['route'] == 'choice'


def test_ppt_material_images_are_optional_and_search_failure_can_continue():
    state = yaml.safe_load(
        (_repo_root() / 'workflows' / 'ppt-workflow' / 'scenario' / 'state.yml').read_text(
            encoding='utf-8'))

    collect = state['steps']['collect_materials']
    assert {'cloud_files', 'ppt_download_feishu_images', 'ppt_register_material_images'} <= set(collect['tools'])
    manifest = yaml.safe_load((_repo_root() / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    declared_tools = {name for item in manifest['tool_scripts'] for name in item['functions']}
    assert 'ppt_download_feishu_images' in declared_tools
    outputs = {
        output['material']: output.get('required', True)
        for output in collect['outputs']
    }

    assert outputs == {
        'reflection_markdown': False,
        'material_summary': True,
        'material_images': False,
        'material_clarification': True,
    }


def test_register_material_images_publishes_one_previewable_image_per_new_file(
    monkeypatch, tmp_path,
):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_inspect_material_image', lambda path, role: {
        'status': 'inspected', 'description': 'A relevant figure', 'role': role,
    })
    old = {
        'path': str(tmp_path / 'old.png'),
        'caption': 'old',
        'alt': 'old',
        'source': 'kb',
    }
    staged_indices: list[int] = []
    saved: list[dict] = []

    def stage(item, index):
        staged_indices.append(index)
        return {
            'path': str(tmp_path / f'material_{index + 1:02d}.png'),
            'caption': item['caption'],
            'alt': item['caption'],
            'source': item['source'],
            'origin': item.get('url', ''),
        }

    monkeypatch.setattr(tools, '_load_material_manifest', lambda: {'images': [old]})
    monkeypatch.setattr(tools, '_stage_one_material_image', stage)
    monkeypatch.setattr(tools, '_write_material_manifest', lambda manifest: tmp_path / 'manifest.json')
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [0])
    monkeypatch.setattr(
        tools, '_save_artifact',
        lambda **kwargs: saved.append(kwargs) or {'success': True, 'result': {}},
    )

    result = tools.ppt_register_material_images([
        {'url': 'https://example.com/one.png', 'caption': '基础示意图', 'source': 'kb'},
        {'url': 'https://example.com/two.png', 'caption': '杆塔基础', 'source': 'web'},
    ])

    assert staged_indices == [1, 2]
    assert [item['content_type'] for item in saved] == ['image', 'image']
    assert [item['key'] for item in saved] == ['material_images', 'material_images']
    assert [item['caption'] for item in saved] == ['基础示意图', '杆塔基础']
    assert [item['sort_order'] for item in saved] == [None, None]
    assert result['ui_published'] == 2


def test_inspection_stages_candidates_and_only_registration_publishes_selection(monkeypatch, tmp_path):
    from PIL import Image, ImageDraw

    tools = _load_ppt_tools()
    material_root = tmp_path / 'materials'
    material_root.mkdir()
    monkeypatch.setattr(tools, '_material_root', lambda: material_root)
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(tools, 'get_model_role_runtime_identity', lambda role: 'configured-vision')
    candidates = {}
    for color in ('red', 'blue'):
        path = tmp_path / f'{color}.png'
        image = Image.new('RGB', (128, 128), 'white')
        ImageDraw.Draw(image).rectangle((16, 16, 96, 96), fill=color)
        image.save(path)
        candidates[f'https://example.com/{path.name}'] = path.read_bytes()
    monkeypatch.setattr(tools, '_download_image_bytes', candidates.__getitem__)
    observed = []

    def describe(system, instruction, paths):
        observed.append(Path(paths[0]).read_bytes())
        return f'Candidate figure {len(observed)}'

    monkeypatch.setattr(tools, '_agent_vlm_call', describe)
    saved = []
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [])
    monkeypatch.setattr(tools, '_save_artifact', lambda **item: saved.append(item) or {'success': True})

    inspected = tools.ppt_inspect_material_images([
        {'url': url, 'caption': Path(url).stem, 'source': 'web'} for url in candidates
    ])

    assert [item['visual_inspection']['description'] for item in inspected['images']] == [
        'Candidate figure 1', 'Candidate figure 2',
    ]
    assert not saved and not tools._material_manifest_path().exists()
    selected = inspected['images'][1]
    result = tools.ppt_register_material_images([selected])

    assert observed == list(candidates.values())
    assert result['ui_published'] == 1
    assert [(item['key'], item['caption'], item['value']) for item in saved] == [
        ('material_images', 'blue', selected['path']),
    ]
    [registered] = tools._load_material_manifest()['images']
    assert registered['origin'] == 'https://example.com/blue.png'
    assert registered['sha256'] == selected['sha256']

    stale = inspected['images'][0]
    Path(stale['path']).write_bytes(candidates['https://example.com/blue.png'])
    with pytest.raises(tools.ToolExecutionError, match='Prepared image changed'):
        tools.ppt_register_material_images([stale])
    assert len(saved) == 1 and len(observed) == 2


def test_replace_material_images_overwrites_cards_and_removes_stale_tail(
    monkeypatch, tmp_path,
):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: 'vlm')
    monkeypatch.setattr(tools, '_inspect_material_image', lambda path, role: {
        'status': 'inspected', 'description': 'A relevant figure', 'role': role,
    })
    saved: list[dict] = []
    deleted: list[int] = []

    def stage(item, index):
        return {
            'path': str(tmp_path / f'material_{index + 1:02d}.png'),
            'caption': item['caption'],
            'alt': item['caption'],
            'source': 'web',
            'origin': item['url'],
        }

    monkeypatch.setattr(tools, '_stage_one_material_image', stage)
    monkeypatch.setattr(tools, '_write_material_manifest', lambda manifest: tmp_path / 'manifest.json')
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda slot: [5, 6, 7, 8])
    monkeypatch.setattr(
        tools, '_delete_ui_slot_item',
        lambda slot, sort_order: deleted.append(sort_order) or {'ok': True},
    )
    monkeypatch.setattr(
        tools, '_save_artifact',
        lambda **kwargs: saved.append(kwargs) or {'success': True, 'result': {}},
    )

    result = tools.ppt_register_material_images([
        {'url': 'https://example.com/one.png', 'caption': 'one'},
        {'url': 'https://example.com/two.png', 'caption': 'two'},
    ], replace=True)

    assert deleted == [4, 3]
    assert [item['sort_order'] for item in saved] == [1, 2]
    assert result['ui_deleted'] == [{'ok': True}, {'ok': True}]


@pytest.mark.parametrize('role', ['llm', 'vlm'])
def test_visual_call_sends_actual_image_to_selected_role(monkeypatch, tmp_path, role):
    import lazyllm
    from unittest.mock import Mock
    tools = _load_ppt_tools()
    picture = tmp_path / 'figure.png'
    picture.write_bytes(b'image')
    model = Mock(return_value='<think>image analysis</think>Visible flow diagram')
    model.share.return_value = model
    factory = Mock(return_value=model)
    monkeypatch.setattr(lazyllm, 'AutoModel', factory)
    monkeypatch.setattr(tools, 'select_vision_model_role', lambda: role)
    assert tools._agent_vlm_call('Inspect', 'Explain the flow', [str(picture)]) == 'Visible flow diagram'
    factory.assert_called_once_with(model=role, type='vlm')
    formatter = model.share.call_args.kwargs['format']
    assert formatter({'content': 'Visible flow diagram', 'reasoning_content': 'image analysis'}) == 'Visible flow diagram'
    from lazyllm.components.formatter import decode_query_with_filepaths
    query = decode_query_with_filepaths(model.call_args.args[0])
    assert query['files'] == [str(picture.resolve())]
    assert query['query'] == 'Inspect\n\nExplain the flow'
    assert model.call_args.kwargs['llm_chat_history'] == []


@pytest.mark.parametrize('vision, expected_role', [(True, 'llm'), (False, '')])
def test_ppt_uses_existing_request_vision_flag_without_vlm_alias(monkeypatch, vision, expected_role):
    import lazyllm
    from lazymind import vision_model
    from lazymind.model_config import inject_model_config
    tools = _load_ppt_tools()
    monkeypatch.setattr(vision_model, 'load_model_config', lambda: {'llm': {'source': 'dynamic'}})
    monkeypatch.setattr(vision_model, 'is_model_role_available', lambda role: role == 'llm')
    with lazyllm.new_session():
        inject_model_config({'llm': {'source': 'openai', 'model': 'private-visual', 'vision': vision}})
        assert tools._material_vision_role() == expected_role
        # The original request remains LLM-only; no second role is synthesized.
        assert 'vlm' not in lazyllm.globals['config']['dynamic_model_configs']
