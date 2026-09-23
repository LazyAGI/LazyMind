from unittest.mock import Mock
import lazyllm
import pytest
from lazymind import vision_model as vm
from lazymind.model_config import inject_model_config

@pytest.fixture(autouse=True)
def no_model_calls(monkeypatch):
    factory = Mock(side_effect=AssertionError('Capability checks must not call models'))
    monkeypatch.setattr(lazyllm, 'AutoModel', factory)
    yield
    factory.assert_not_called()

def configure(monkeypatch, config=None, roles=('llm',)):
    monkeypatch.setattr(vm, 'is_model_role_available', lambda role: role in roles)
    monkeypatch.setattr(vm, '_llm_configuration', lambda: config or {})

def test_explicit_vlm_wins(monkeypatch):
    configure(monkeypatch, roles=('llm', 'vlm'))
    assert vm.select_vision_model_role() == 'vlm'

@pytest.mark.parametrize('config', [{'vision': True}, {'type': 'vlm'}])
def test_declared_visual_llm(monkeypatch, config):
    configure(monkeypatch, config)
    assert vm.select_vision_model_role() == 'llm'

@pytest.mark.parametrize('config', [{}, {'vision': False}, {'vision': 'true'}, {'model': 'gpt-4o'}, {'supports_vision': True}])
def test_undeclared_llm_never_probed_or_guessed(monkeypatch, config):
    configure(monkeypatch, config)
    with pytest.raises(vm.VisionModelUnavailable, match='未声明支持多模态'):
        vm.select_vision_model_role()

def test_no_models(monkeypatch):
    configure(monkeypatch, roles=())
    with pytest.raises(vm.VisionModelUnavailable, match='未配置视觉模型或主模型'):
        vm.select_vision_model_role()

def test_dynamic_metadata_is_request_scoped(monkeypatch):
    monkeypatch.setattr(vm, 'load_model_config', lambda: {'llm': {'source': 'dynamic'}})
    with lazyllm.new_session():
        inject_model_config({'llm': {'source': 'openai', 'model': 'private-alias', 'vision': True, 'api_key': 'test-key'}})
        assert vm._llm_configuration()['vision'] is True
        assert 'api_key' not in vm._llm_configuration()
    with lazyllm.new_session():
        inject_model_config({'llm': {'source': 'openai', 'model': 'text-only'}})
        assert 'vision' not in vm._llm_configuration()

@pytest.mark.parametrize('config', [{'vision': True}, {'type': 'vlm'}])
def test_visual_main_is_direct_even_when_separate_vlm_exists(monkeypatch, config):
    configure(monkeypatch, config, roles=('llm', 'vlm'))
    assert vm.main_model_supports_vision()


def test_separate_vlm_does_not_make_text_main_visual(monkeypatch):
    configure(monkeypatch, {'vision': False}, roles=('llm', 'vlm'))
    assert not vm.main_model_supports_vision()
