from types import SimpleNamespace

import pytest

from lazymind.chat.service import external_tools


def echo_tool(text: str) -> dict:
    """Echo text for a registry dispatch test.

    Args:
        text (str): Text to echo.
    """
    return {'text': text, 'local_path': '/private/test'}


@pytest.fixture
def registry(monkeypatch):
    config = SimpleNamespace(name='new_registered_tool', tool=echo_tool, description='Echo')
    monkeypatch.setattr(external_tools, 'DEFAULT_TOOLS', [config])
    monkeypatch.setattr(external_tools, 'tool_is_active', lambda _: True)
    return config


def test_new_registry_tool_is_discovered_and_executed(registry):
    catalog = external_tools.run_external_tools({})['items']
    assert catalog[0]['name'] == 'new_registered_tool'
    assert catalog[0]['available']
    assert 'text' in catalog[0]['input_schema']['properties']
    result = external_tools.run_external_tools({
        'tool_name': registry.name, 'arguments': {'text': 'hello'},
    }, execute=True)
    assert result['result'] == {'text': 'hello'}


def test_internal_visibility_does_not_revoke_external_tools(registry):
    payload = {'disabled_tools': [registry.name], 'tool_name': registry.name, 'arguments': {'text': 'hello'}}
    assert external_tools.run_external_tools(payload)['items'][0]['available']
    assert external_tools.run_external_tools(payload, execute=True)['result'] == {'text': 'hello'}


def test_context_tool_cannot_be_enabled_by_caller(registry):
    registry.name = 'local_fs'
    assert 'filesystem' in external_tools.run_external_tools({})['items'][0]['reason']
    with pytest.raises(ValueError):
        external_tools.run_external_tools({'tool_name': 'local_fs'}, execute=True)


def test_unavailable_and_unknown_tools_fail_closed(registry, monkeypatch):
    monkeypatch.setattr(external_tools, 'tool_is_active', lambda _: False)
    assert not external_tools.run_external_tools({})['items'][0]['available']
    for name in (registry.name, 'unknown'):
        with pytest.raises(ValueError):
            external_tools.run_external_tools({'tool_name': name}, execute=True)


def test_required_arguments_are_validated(registry):
    with pytest.raises(ValueError):
        external_tools.run_external_tools({'tool_name': registry.name, 'arguments': {}}, execute=True)


def test_credentials_are_request_scoped(registry, monkeypatch):
    import lazyllm
    observed = []

    def active(_):
        observed.append(dict(lazyllm.globals.config['dynamic_tool_auth'] or {}))
        return True

    monkeypatch.setattr(external_tools, 'tool_is_active', active)
    external_tools.run_external_tools({'tool_config': {'tavily': 'test-only-secret'}})
    external_tools.run_external_tools({})
    assert observed[0]
    assert not observed[1]


def test_media_capability_discovery_requires_selected_model(registry, monkeypatch):
    registry.name = 'video_generator'
    registry.model_role = 'video_generator'
    # A static server model must not substitute for the authenticated user's choice.
    item = external_tools.run_external_tools({})['items'][0]
    assert not item['available']
    assert item['model_role'] == 'video_generator'
    assert 'Settings' in item['reason']
    item = external_tools.run_external_tools({
        'llm_config': {'text2video': {'source': 'openai', 'model': 'configured-video', 'api_key': 'test'}},
    })['items'][0]
    assert item['available']


def test_targeted_catalog_does_not_resolve_other_tools(registry, monkeypatch):
    other = SimpleNamespace(name='unrelated', tool=echo_tool, description='Other')
    monkeypatch.setattr(external_tools, 'DEFAULT_TOOLS', [registry, other])
    touched = []
    monkeypatch.setattr(external_tools, 'tool_is_active', lambda config: touched.append(config.name) or True)
    assert len(external_tools.run_external_tools({'tool_name': registry.name})['items']) == 1
    assert touched == [registry.name]


def test_media_inputs_are_scoped_and_removed_after_execution():
    import base64
    import io
    from pathlib import Path
    from PIL import Image

    content = io.BytesIO()
    Image.new('RGB', (2, 2)).save(content, format='PNG')
    uri = 'data:image/png;base64,' + base64.b64encode(content.getvalue()).decode()
    with external_tools._media_arguments('image_editor', {'urls': [uri, uri], 'prompt': 'edit'}) as args:
        assert args['urls'][0] == args['urls'][1]
        path = Path(args['urls'][0])
        assert path.read_bytes() == content.getvalue()
    assert not path.exists()
    with external_tools._media_arguments('video_generator', {'prompt': 'text only'}) as args:
        assert args == {'prompt': 'text only'}
    for unsafe in ('/etc/passwd', 'https://example.com/image.png', 'file:///etc/passwd', 'data:image/png;base64,bad'):
        with pytest.raises(ValueError):
            with external_tools._media_arguments('video_generator', {'first_frame_url': unsafe}):
                pass


def test_media_directory_is_removed_on_tool_failure():
    import base64
    import io
    from pathlib import Path
    from PIL import Image

    content = io.BytesIO()
    Image.new('RGB', (2, 2)).save(content, format='PNG')
    uri = 'data:image/png;base64,' + base64.b64encode(content.getvalue()).decode()
    with pytest.raises(RuntimeError):
        with external_tools._media_arguments('image_editor', {'urls': [uri]}) as args:
            path = Path(args['urls'][0])
            raise RuntimeError('provider failed')
    assert not path.exists()


def test_configured_video_tool_executes_with_isolated_image_bytes(registry, monkeypatch):
    import base64
    import io
    from pathlib import Path
    from typing import Optional
    from PIL import Image

    observed = []

    def video(prompt: str, first_frame_url: Optional[str] = None) -> dict:
        """Generate a test video.

        Args:
            prompt (str): Requested video.
            first_frame_url (Optional[str]): Authorized first frame.
        """
        if first_frame_url:
            path = Path(first_frame_url)
            observed.append(path)
            assert path.read_bytes() == content.getvalue()
        return {'video_url': '/static-files/ai_generated/test.mp4'}

    registry.name = 'video_generator'
    registry.model_role = 'video_generator'
    registry.tool = video
    content = io.BytesIO()
    Image.new('RGB', (2, 2)).save(content, format='PNG')
    uri = 'data:image/png;base64,' + base64.b64encode(content.getvalue()).decode()
    payload = {'tool_name': registry.name, 'arguments': {'prompt': 'animate', 'first_frame_url': uri}}
    with pytest.raises(ValueError):
        external_tools.run_external_tools(payload, execute=True)
    payload['llm_config'] = {'text2video': {'source': 'openai', 'model': 'configured-video', 'api_key': 'test'}}
    result = external_tools.run_external_tools(payload, execute=True)
    assert result['result']['video_url'].endswith('/test.mp4')
    assert len(observed) == 1 and not observed[0].exists()
    payload['arguments'] = {'prompt': 'text only'}
    assert external_tools.run_external_tools(payload, execute=True)['result'] == result['result']


@pytest.mark.parametrize('name,selection,input_key,runner', [
    ('image_editor', 'image_editing', 'urls', 'run_image_model'),
    ('video_generator', 'text2video', 'first_frame_url', 'run_video_model'),
])
def test_registered_media_tools_use_only_materialized_authorized_inputs(monkeypatch, name, selection, input_key, runner):
    import base64
    import io
    from pathlib import Path
    from PIL import Image
    from lazymind.chat.engine.tools import multimodal

    config = next(item for item in external_tools.DEFAULT_TOOLS if item.name == name)
    monkeypatch.setattr(external_tools, 'DEFAULT_TOOLS', [config])
    content = io.BytesIO()
    Image.new('RGB', (2, 2)).save(content, format='PNG')
    uri = 'data:image/png;base64,' + base64.b64encode(content.getvalue()).decode()
    observed = []

    def provider_boundary(role, prompt, *, files, **kwargs):
        assert role == name and prompt == 'change'
        assert len(files) == 1
        path = Path(files[0])
        assert path.read_bytes() == content.getvalue()
        observed.append(path)
        return {'media_url': 'https://example.com/generated-output'}

    # Keep the production registry, callable, file resolution and ToolManager
    # assembly real; only the media provider boundary is replaced.
    monkeypatch.setattr(multimodal, runner, provider_boundary)
    result = external_tools.run_external_tools({
        'tool_name': name,
        'llm_config': {selection: {'source': 'openai', 'model': 'configured-media', 'api_key': 'test'}},
        'arguments': {'prompt': 'change', input_key: [uri] if input_key == 'urls' else uri},
    }, execute=True)
    assert result['result'] == {'media_url': 'https://example.com/generated-output'}
    assert len(observed) == 1 and not observed[0].exists()


@pytest.mark.parametrize('vision_role', ['vlm', 'llm'])
def test_external_vision_uses_configured_model_and_returns_only_description(monkeypatch, vision_role):
    import base64
    import io
    from pathlib import Path

    from PIL import Image
    from lazyllm.components.formatter import decode_query_with_filepaths
    from lazymind.chat.engine import attachment_reader

    config = next(item for item in external_tools.DEFAULT_TOOLS if item.name == 'multimodal')
    monkeypatch.setattr(external_tools, 'DEFAULT_TOOLS', [config])
    content = io.BytesIO()
    Image.new('RGB', (2, 2), 'red').save(content, format='PNG')
    image = content.getvalue()
    observed = []

    def provider_boundary(model, type):
        assert (model, type) == (vision_role, 'vlm')

        def describe(encoded_query, **kwargs):
            query = decode_query_with_filepaths(encoded_query)
            assert query['query'] == 'Describe the visible colors'
            path = Path(query['files'][0])
            assert path.read_bytes() == image
            observed.append(path)
            return 'A red square.'

        return describe

    monkeypatch.setattr(attachment_reader, 'AutoModel', provider_boundary)
    request = {'tool_name': 'multimodal'}
    assert not external_tools.run_external_tools(request)['items'][0]['available']
    vision_model = {'source': 'openai', 'model': 'configured-vision', 'api_key': 'test', 'vision': True}
    request['llm_config'] = {vision_role: {**vision_model, 'features': {'vision': False}}}
    assert not external_tools.run_external_tools(request)['items'][0]['available']
    request['llm_config'] = {vision_role: vision_model}
    catalog = external_tools.run_external_tools(request)['items'][0]
    assert catalog['available']
    assert 'resource:<id>' in catalog['input_schema']['properties']['url']['description']

    with pytest.raises(ValueError, match='authorized image bytes'):
        external_tools.run_external_tools({**request, 'arguments': {'url': '/private/image.png'}}, execute=True)
    result = external_tools.run_external_tools({**request, 'arguments': {
        'url': 'data:image/png;base64,' + base64.b64encode(image).decode(),
        'instruction': 'Describe the visible colors',
    }}, execute=True)

    assert result['result'] == {'description': 'A red square.'}
    assert len(observed) == 1 and not observed[0].exists()
