from lazyllm.tools.rag.doc_node import DocNode
from lazyllm.tools.rag.store import document_store
from lazymind.parsing.service.embedding_input_compat import install


def test_empty_segments_do_not_reach_embedding_provider(monkeypatch):
    seen = []
    monkeypatch.setattr(document_store, 'parallel_do_embedding',
                        lambda embed, keys, nodes, groups: seen.extend(nodes) or nodes)
    install()
    valid = DocNode(text='real paragraph', group='line')
    empty = DocNode(text='', group='line')
    whitespace = DocNode(text=' \n ', group='line')
    assert document_store.parallel_do_embedding({}, [], [empty, valid, whitespace], {}) == [valid]
    assert seen == [valid]
    assert empty.text == ''


def test_real_embedding_batch_keeps_vector_alignment(monkeypatch):
    from lazyllm.tools.rag.utils import parallel_do_embedding
    monkeypatch.setattr(document_store, 'parallel_do_embedding', parallel_do_embedding)
    install()
    calls = []
    def provider(texts):
        assert all(text.strip() for text in texts)
        calls.append(texts)
        return [[float(i + 1)] for i in range(len(texts))]
    provider.batch_size = 8
    nodes = [DocNode(text=text, group='line') for text in ['first', '', ' \n ', 'second']]
    document_store.parallel_do_embedding({'embed_main': provider}, [], nodes, {'line': ['embed_main']})
    assert calls == [['first', 'second']]
    assert nodes[0].embedding['embed_main'] == [1.0]
    assert nodes[3].embedding['embed_main'] == [2.0]
    assert nodes[1].embedding == nodes[2].embedding == {}
    assert [node.text for node in nodes] == ['first', '', ' \n ', 'second']
