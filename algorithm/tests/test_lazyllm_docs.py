import importlib.util
from pathlib import Path
from types import SimpleNamespace


_MODULE_PATH = Path(__file__).parents[1] / 'lazymind' / 'lazyllm_docs.py'
_SPEC = importlib.util.spec_from_file_location('lazymind_lazyllm_docs_test', _MODULE_PATH)
_MODULE = importlib.util.module_from_spec(_SPEC)
assert _SPEC.loader is not None
_SPEC.loader.exec_module(_MODULE)


class _Config(dict):
    pass


def _fake_lazyllm(doc=None):
    def add_doc():
        pass

    add_doc.__doc__ = doc
    return SimpleNamespace(add_doc=add_doc, config=_Config(init_doc=False))


def test_ensure_lazyllm_docs_skips_prebuilt_docs(monkeypatch):
    lazyllm = _fake_lazyllm('Add document for lazyllm functions')
    imported = []
    monkeypatch.setattr(_MODULE.importlib, 'import_module', imported.append)

    assert _MODULE.ensure_lazyllm_docs(lazyllm) is False
    assert imported == []


def test_ensure_lazyllm_docs_initializes_missing_docs_and_restores_config(monkeypatch):
    lazyllm = _fake_lazyllm()
    imported = []
    dependency_check = SimpleNamespace(cache_clear=lambda: imported.append('cache_clear'))

    def fake_import(name):
        assert lazyllm.config['init_doc'] is True
        imported.append(name)
        if name == 'lazyllm.thirdparty':
            return SimpleNamespace(check_dependency_by_group=dependency_check)
        return SimpleNamespace()

    monkeypatch.setattr(_MODULE.importlib, 'import_module', fake_import)

    assert _MODULE.ensure_lazyllm_docs(lazyllm) is True
    assert lazyllm.config['init_doc'] is False
    assert imported == [
        'lazyllm.thirdparty',
        'cache_clear',
        *[f'lazyllm.docs.{name}' for name in _MODULE._DOC_MODULES],
    ]
