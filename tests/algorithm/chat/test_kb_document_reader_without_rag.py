"""Document fulltext reads use Core authorization without optional retrieval imports."""
from unittest.mock import Mock

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools import lazy_kb
from lazymind.chat.engine.tools.infra import core_api_client


def test_fulltext_read_does_not_load_retrieval_runtime(monkeypatch):
    monkeypatch.setattr(lazy_kb, '_toolkit', Mock(side_effect=AssertionError('RAG must not load')))
    read = Mock(return_value={'content': {'text': 'Paper methods and results'}})
    monkeypatch.setattr(core_api_client, 'get_core_api', read)

    result = lazy_kb.KBToolkit().read_document('library/a', 'paper/b')

    assert result['content']['text'] == 'Paper methods and results'
    read.assert_called_once_with('/datasets/library%2Fa/documents/paper%2Fb:read')


@pytest.mark.parametrize('dataset,document', [('', 'paper'), ('library', ''), ('  ', 'paper')])
def test_fulltext_read_requires_both_identifiers(monkeypatch, dataset, document):
    read = Mock()
    monkeypatch.setattr(core_api_client, 'get_core_api', read)
    with pytest.raises(ToolExecutionError, match='required'):
        lazy_kb.KBToolkit().read_document(dataset, document)
    read.assert_not_called()


def test_fulltext_read_preserves_inherited_scope(monkeypatch):
    read = Mock(return_value={'content': {'text': 'paper'}})
    monkeypatch.setattr(core_api_client, 'get_core_api', read)
    toolkit = lazy_kb.KBToolkit(kb_scope=['allowed'])
    with pytest.raises(ToolExecutionError, match='scope'):
        toolkit.read_document('other', 'paper')
    read.assert_not_called()
    assert toolkit.read_document('allowed', 'paper')['content']['text'] == 'paper'


@pytest.mark.parametrize('rag_enabled', [False, True])
def test_chat_registry_exposes_fulltext_without_retrieval_component(monkeypatch, rag_enabled):
    import lazyllm
    from lazyllm.tools.agent import ToolManager
    from lazymind.common import optional_components
    from lazymind.chat.service.component import tool_registry

    monkeypatch.setattr(optional_components, 'component_enabled', lambda name: rag_enabled)
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'selected-library'}}
    configs = tool_registry.filter_tools(tool_registry.DEFAULT_TOOLS, available_tools=['kb'])
    assert len(configs) == 1
    manager = ToolManager([configs[0].tool])
    names = {tool['function']['name'] for tool in manager.tools_description}
    assert 'KBToolkit_read_document' in names
    assert ('KBToolkit_kb_search' in names) is rag_enabled
    if not rag_enabled:
        assert names == {'KBToolkit_read_document'}
        read = Mock(return_value={'content': {'text': 'full paper'}})
        monkeypatch.setattr(core_api_client, 'get_core_api', read)
        result = manager([{'function': {'name': 'KBToolkit_read_document', 'arguments': {
            'knowledge_base_id': 'selected-library', 'document_id': 'doc',
        }}}])
        assert 'full paper' in str(result)
        read.assert_called_once()


def test_fulltext_read_rejects_empty_inherited_scope(monkeypatch):
    read = Mock()
    monkeypatch.setattr(core_api_client, 'get_core_api', read)
    with pytest.raises(ToolExecutionError, match='scope'):
        lazy_kb.KBToolkit(kb_scope=[]).read_document('library', 'paper')
    read.assert_not_called()
