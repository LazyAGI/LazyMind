"""
Additional tests for kb tool helpers (filters, files, no-parent node).

The chat.tools.kb module imports chat.pipelines.get_ppl_search which triggers a
circular import via vocab.evolution.  We break the cycle with the same
lightweight stub approach used in test_pipeline_builders_extra.py.
"""
import sys
import types


def _stub_vocab_and_chat_pipelines():
    """Stub out modules that cause circular imports at collection time.

    vocab.evolution is NOT stubbed here because the circular import
    (vocab.evolution → chat.pipelines) has been resolved with lazy
    imports inside the class constructors.  Stubbing vocab.evolution would
    leave an empty module object in sys.modules and break any later test that
    imports real symbols from it (e.g. ActionPlanningModule).
    """
    # vocab stubs — only inject if not already loaded
    vocab_pkg = types.ModuleType('vocab')
    sys.modules.setdefault('vocab', vocab_pkg)

    if 'vocab.vocab_manager' not in sys.modules:
        vm_stub = types.ModuleType('vocab.vocab_manager')
        vm_stub.get_vocab_manager = lambda user_id: (lambda q: q)
        sys.modules['vocab.vocab_manager'] = vm_stub

    if 'vocab.db' not in sys.modules:
        db_stub = types.ModuleType('vocab.db')
        sys.modules['vocab.db'] = db_stub


_stub_vocab_and_chat_pipelines()

from chat.tools import kb  # noqa: E402  (must come after stubs)

DEFAULT_AGENTIC_CONFIG = {
    'kb_id': 'ds_9e96150bb1ceeec7d96055638072b8a9',
}


# ---------------------------------------------------------------------------
# kb_search — explicit filters merged with kb_id
# ---------------------------------------------------------------------------

def test_kb_search_merges_explicit_filters_with_kb_id(monkeypatch):
    captured_payload = {}

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        def fake_search(payload):
            captured_payload.update(payload)
            return []
        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        kb.kb_search('query', filters={'file_name': 'report.pdf'})
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured_payload['filters']['file_name'] == 'report.pdf'
    assert captured_payload['filters']['kb_id'] == DEFAULT_AGENTIC_CONFIG['kb_id']


def test_kb_search_uses_files_from_agentic_config(monkeypatch):
    captured_payload = {}

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        def fake_search(payload):
            captured_payload.update(payload)
            return []
        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    config_with_files = dict(DEFAULT_AGENTIC_CONFIG, files=['file-a', 'file-b'])
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = config_with_files
    try:
        kb.kb_search('query')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured_payload['files'] == ['file-a', 'file-b']


def test_kb_search_passes_image_files_from_agentic_config(monkeypatch):
    captured_payload = {}

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        def fake_search(payload):
            captured_payload.update(payload)
            return []
        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    config_with_images = dict(DEFAULT_AGENTIC_CONFIG, image_files=['image-a.png'])
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = config_with_images
    try:
        kb.kb_search('query')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured_payload['image_files'] == ['image-a.png']


def test_kb_search_forwards_user_id_from_agentic_config(monkeypatch):
    captured_payload = {}

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        def fake_search(payload):
            captured_payload.update(payload)
            return []
        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    config_with_user = dict(DEFAULT_AGENTIC_CONFIG, user_id='user-007')
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = config_with_user
    try:
        kb.kb_search('query')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured_payload['user_id'] == 'user-007'


def test_kb_search_user_id_defaults_to_empty_when_absent(monkeypatch):
    captured_payload = {}

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        def fake_search(payload):
            captured_payload.update(payload)
            return []
        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        kb.kb_search('query')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured_payload['user_id'] == ''


def test_kb_search_explicit_empty_files_overrides_config_files(monkeypatch):
    captured_payload = {}

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        def fake_search(payload):
            captured_payload.update(payload)
            return []
        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    config_with_files = dict(DEFAULT_AGENTIC_CONFIG, files=['file-a'])
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = config_with_files
    try:
        kb.kb_search('query', files=[])
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured_payload['files'] == []


# ---------------------------------------------------------------------------
# kb_get_parent_node — node without parent returns empty items
# ---------------------------------------------------------------------------

def test_kb_get_parent_node_returns_empty_when_no_parent(monkeypatch):
    class FakeNode:
        def __init__(self):
            self.uid = 'root-node'
            self.number = 1
            self.group = 'block'
            self._parent = None
            self.text = 'root text'
            self.metadata = {}
            self.global_metadata = {'docid': 'doc-1', 'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id']}

    class FakeDocument:
        def get_nodes(self, uids=None, doc_ids=None, group=None, kb_id=None, numbers=None):
            if uids == ['root-node']:
                return [FakeNode()]
            return []

    monkeypatch.setattr(kb.lazyllm.tools.rag, 'Document', lambda **kwargs: FakeDocument())
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        result = kb.kb_get_parent_node('root-node')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert result['success'] is True
    assert result['tool'] == 'kb_get_parent_node'
    assert result['result']['node_id'] == 'root-node'
    assert result['result']['parent_id'] is None
    assert result['result']['total'] == 0
    assert result['result']['items'] == []
