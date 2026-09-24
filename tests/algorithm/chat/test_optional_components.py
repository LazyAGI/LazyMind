import importlib
import pytest

from lazymind.common.optional_components import ComponentRequiredError, require_component


def test_disabled_rag_does_not_import_or_wait_for_kb(monkeypatch):
    from lazymind.chat import runtime_loader
    loader = importlib.reload(runtime_loader)
    monkeypatch.setenv('LAZYMIND_RAG_ENABLED', '0')
    calls = []
    monkeypatch.setattr(loader, 'ensure_chat_runtime', lambda: calls.append('chat'))
    monkeypatch.setattr(loader, '_wait_for_kb_runtime', lambda: pytest.fail('waited for absent RAG'))
    monkeypatch.setattr(loader.importlib, 'import_module', lambda _: pytest.fail('imported absent RAG'))
    loader._wait_and_warm()
    assert calls == ['chat']
    assert loader.rag_runtime_status() == 'not_installed'
    with pytest.raises(ComponentRequiredError, match='python-rag-dependency'):
        loader.ensure_rag_runtime()


def test_missing_rag_has_actionable_message(monkeypatch):
    monkeypatch.setenv('LAZYMIND_RAG_ENABLED', '0')
    with pytest.raises(ComponentRequiredError) as exc:
        require_component('rag')
    assert exc.value.component == 'rag'
    assert exc.value.code == 'CAPABILITY_REQUIRED'
    assert 'python-rag-dependency' in str(exc.value)
    monkeypatch.delenv('LAZYMIND_RAG_ENABLED')
    require_component('rag')  # source/cloud compatibility


def test_knowledge_search_missing_rag_is_not_generic_import_error(monkeypatch):
    from lazymind.chat.service import knowledge_search_service as svc
    monkeypatch.setenv('LAZYMIND_RAG_ENABLED', '0')
    with pytest.raises(svc.KnowledgeSearchError) as exc:
        svc.search('user', 'query', ['kb'], 1)
    assert exc.value.code == 'CAPABILITY_REQUIRED'
