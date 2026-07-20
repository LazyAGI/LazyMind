import importlib.util
import sys
import types
from pathlib import Path


def _load_tool_registry_module():
    fake_lazyllm = types.ModuleType('lazyllm')
    fake_lazyllm.globals = types.SimpleNamespace(get=lambda _name: {})

    fake_feishu = types.ModuleType('lazyllm.tools.fs.supplier.feishu')

    class FakeFeishuWikiFS:
        __public_apis__ = ['ls', 'search', 'find']

        def __init__(self, **kwargs):
            self.kwargs = kwargs

    fake_feishu.FeishuWikiFS = FakeFeishuWikiFS

    fake_notion = types.ModuleType('lazyllm.tools.fs.supplier.notion')

    class FakeNotionFS:
        __public_apis__ = ['ls', 'search', 'find']

        def __init__(self, **kwargs):
            self.kwargs = kwargs

    fake_notion.NotionFS = FakeNotionFS

    fake_search = types.ModuleType('lazyllm.tools.tools.search')
    for name in (
        'ArxivSearch',
        'BingSearch',
        'BochaSearch',
        'GoogleSearch',
        'SciverseSearch',
        'TavilySearch',
        'WikipediaSearch',
    ):
        fake_search.__dict__[name] = type(name, (), {'__init__': lambda self, **_kwargs: None})

    fake_tools = types.ModuleType('lazymind.chat.engine.tools')
    for name in ('KBToolGroup', 'ExternalDBToolGroup', 'LocalFSToolGroup', 'SystemQueryToolGroup', 'WriterToolGroup'):
        fake_tools.__dict__[name] = type(name, (), {})
    for name in (
        'calculator',
        'image_editor',
        'image_generator',
        'kb_tmp_search',
        'memory_editor',
        'read_memory',
        'skill_editor',
        'url_fetch',
        'vision_extractor',
        'vocab_learn',
    ):
        fake_tools.__dict__[name] = lambda *args, **kwargs: None

    fake_model_config = types.ModuleType('lazymind.model_config')
    fake_model_config.is_model_role_available = lambda _role: True

    modules = {
        'lazyllm': fake_lazyllm,
        'lazyllm.tools.fs.supplier.feishu': fake_feishu,
        'lazyllm.tools.fs.supplier.notion': fake_notion,
        'lazyllm.tools.tools.search': fake_search,
        'lazymind.chat.engine.tools': fake_tools,
        'lazymind.model_config': fake_model_config,
    }
    old_modules = {name: sys.modules.get(name) for name in modules}
    sys.modules.update(modules)
    try:
        path = Path('algorithm/lazymind/chat/service/component/tool_registry.py')
        module_name = '_tool_registry_under_test'
        spec = importlib.util.spec_from_file_location(module_name, path)
        module = importlib.util.module_from_spec(spec)
        assert spec.loader is not None
        sys.modules[module_name] = module
        spec.loader.exec_module(module)
        return module
    finally:
        sys.modules.pop('_tool_registry_under_test', None)
        for name, old_module in old_modules.items():
            if old_module is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = old_module


def test_feishu_group_uses_lazyllm_wiki_filesystem_directly():
    registry = _load_tool_registry_module()
    group = next(item for item in registry.DEFAULT_TOOLS if item.name == 'feishu')

    assert group.instance.__class__.__name__ == 'FakeFeishuWikiFS'
    assert group.instance.kwargs == {'space_id': 'dynamic', 'dynamic_auth': True}
    assert 'search' in group.instance.__public_apis__
    assert 'find' in group.instance.__public_apis__


def test_notion_group_uses_lazyllm_filesystem_directly():
    registry = _load_tool_registry_module()
    group = next(item for item in registry.DEFAULT_TOOLS if item.name == 'notion')

    assert group.instance.__class__.__name__ == 'FakeNotionFS'
    assert group.instance.kwargs == {'dynamic_auth': True}
    assert 'search' in group.instance.__public_apis__
    assert 'find' in group.instance.__public_apis__


def test_lazymind_does_not_register_a_duplicate_cloud_search_group():
    registry = _load_tool_registry_module()

    assert all(item.name != 'online_search' for item in registry.DEFAULT_TOOLS)
