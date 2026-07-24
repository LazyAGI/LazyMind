import pytest

from lazymind.chat.engine.tools.internal.context_expansion import (
    ContextExpansionComponent,
    _estimate_tokens,
    _get_doc_id,
    _get_node_type,
    _node_sort_key,
)


class DummyNode:
    def __init__(
        self,
        uid,
        text='text',
        score=0.0,
        metadata=None,
        global_metadata=None,
    ):
        self.uid = uid
        self.text = text
        self.relevance_score = score
        self.metadata = metadata or {}
        self.global_metadata = global_metadata or {}


def test_context_expansion_returns_empty_input_unchanged():
    component = ContextExpansionComponent(document=object())

    assert component([]) == []


def test_context_expansion_skips_seed_without_doc_id():
    seed = DummyNode('seed', global_metadata={})

    class FakeDocument:
        def get_window_nodes(self, node, span, merge):
            raise AssertionError('document should not be queried without docid')

    component = ContextExpansionComponent(FakeDocument())

    assert component([seed]) == [seed]


def test_context_expansion_fetches_neighbors_with_budget_and_doc_filter():
    seed = DummyNode('seed', text='seed', score=0.8, metadata={'index': 2}, global_metadata={'docid': 'doc-1'})
    left = DummyNode('left', text='abcd', metadata={'index': 1}, global_metadata={'docid': 'doc-1'})
    right = DummyNode('right', text='abcd', metadata={'index': 3}, global_metadata={'docid': 'doc-1'})
    other_doc = DummyNode('other', text='abcd', metadata={'index': 4}, global_metadata={'docid': 'doc-2'})

    class FakeDocument:
        def __init__(self):
            self.calls = []

        def get_window_nodes(self, node, span, merge):
            self.calls.append((node.uid, span, merge))
            return [right, seed, other_doc, left]

    document = FakeDocument()
    component = ContextExpansionComponent(document, token_budget=2, score_decay=0.5, max_new_nodes_per_seed=3)

    result = component([seed])

    assert document.calls == [('seed', (-1, 1), False)]
    assert {node.uid for node in result} == {'seed', 'left', 'right'}
    assert left.relevance_score == pytest.approx(0.4)
    assert right.relevance_score == pytest.approx(0.4)


def test_context_expansion_retries_rpc_failure_and_accepts_single_window_node(monkeypatch):
    monkeypatch.setattr('lazymind.chat.engine.tools.internal.context_expansion.time.sleep', lambda _: None)
    seed = DummyNode('seed', score=1.0, metadata={'index': 1}, global_metadata={'docid': 'doc-1'})
    neighbor = DummyNode('neighbor', metadata={'index': 2}, global_metadata={'docid': 'doc-1'})

    class FakeDocument:
        def __init__(self):
            self.calls = 0

        def get_window_nodes(self, node, span, merge):
            self.calls += 1
            if self.calls == 1:
                raise RuntimeError('temporary failure')
            return neighbor

    document = FakeDocument()
    component = ContextExpansionComponent(document, score_decay=0.5)

    result = component([seed])

    assert document.calls == 2
    assert [node.uid for node in result] == ['seed', 'neighbor']
    assert neighbor.relevance_score == pytest.approx(0.5)


def test_context_expansion_returns_seed_when_rpc_retries_exhausted(monkeypatch):
    monkeypatch.setattr('lazymind.chat.engine.tools.internal.context_expansion.time.sleep', lambda _: None)
    seed = DummyNode('seed', global_metadata={'docid': 'doc-1'})

    class FakeDocument:
        def get_window_nodes(self, node, span, merge):
            raise RuntimeError('always failing')

    component = ContextExpansionComponent(FakeDocument())

    assert component([seed]) == [seed]


def test_context_expansion_table_seed_uses_larger_span_and_ignores_budget():
    seed = DummyNode(
        'table',
        text='table',
        score=0.7,
        metadata={'type': 'table', 'index': 2},
        global_metadata={'docid': 'doc-1'},
    )
    neighbors = [
        DummyNode(str(i), text='x' * 100, metadata={'index': i}, global_metadata={'docid': 'doc-1'})
        for i in range(4)
    ]

    class FakeDocument:
        def __init__(self):
            self.span = None

        def get_window_nodes(self, node, span, merge):
            self.span = span
            return [seed] + neighbors

    document = FakeDocument()
    component = ContextExpansionComponent(document, token_budget=1, max_new_nodes_per_seed=1)

    result = component([seed])

    assert document.span == (-2, 2)
    assert len(result) == 5


def test_context_expansion_limits_seeds_and_skips_neighbors_over_budget():
    first = DummyNode('first', text='seed', score=0.9, metadata={'index': 1}, global_metadata={'docid': 'doc-1'})
    second = DummyNode('second', text='seed', score=0.8, metadata={'index': 2}, global_metadata={'docid': 'doc-1'})
    large_neighbor = DummyNode('large', text='x' * 100, metadata={'index': 3}, global_metadata={'docid': 'doc-1'})

    class FakeDocument:
        def __init__(self):
            self.queried = []

        def get_window_nodes(self, node, span, merge):
            self.queried.append(node.uid)
            return [large_neighbor]

    document = FakeDocument()
    component = ContextExpansionComponent(document, token_budget=1, max_seeds=1)

    result = component([second, first])

    assert document.queried == ['first']
    assert [node.uid for node in result] == ['first', 'second']


def test_context_expansion_helpers_are_defensive():
    node = DummyNode(
        'n2',
        metadata={'node_type': 'paragraph', 'index': 5},
        global_metadata={'docid': 'doc-9'},
    )

    assert _get_doc_id(node) == 'doc-9'
    assert _get_node_type(node) == 'paragraph'
    assert _node_sort_key(node) == (5, 'n2')
    assert _estimate_tokens('abcdefgh') == 2
    assert _estimate_tokens('') == 1

    class TypeOnlyNode:
        metadata = None
        type = 'table'

    assert _get_node_type(TypeOnlyNode()) == 'table'
