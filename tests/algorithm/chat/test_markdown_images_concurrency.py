import threading
import pickle
import weakref
import gc
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace
import codecs

from lazymind.chat.service.utils.citations import (
    annotate_citations,
    reset_citation_state,
    citation_state_lock,
)
from lazymind.chat.service.utils.markdown_images import build_image_url_map_from_config


def test_initialized_citation_state_remains_serializable():
    state = {}
    reset_citation_state(state)
    build_image_url_map_from_config(state)
    lock = citation_state_lock(state)
    restored = pickle.loads(pickle.dumps({'citation_state': state}))
    assert restored['citation_state']['_image_url_registry'] == {}
    assert citation_state_lock(state) is lock


def test_remote_call_serializes_initialized_citation_state(monkeypatch):
    import lazyllm
    from lazyllm.module.servermodule import ServerModule
    from lazyllm.common import globals as global_state

    state = {}
    reset_citation_state(state)
    build_image_url_map_from_config(state)
    monkeypatch.setitem(lazyllm.globals._data, 'agentic_config', {'citation_state': state})
    response = {'total': 1, 'data': [{'text': 'known document'}]}

    def post(url, *, json, headers):
        assert url == 'http://localhost:12345/_call'
        assert headers['Global-Parameters'] == global_state.pickled_data
        return SimpleNamespace(status_code=200, content=codecs.encode(pickle.dumps(response), 'base64'))

    monkeypatch.setattr('lazyllm.module.servermodule.requests.post', post)
    result = ServerModule._call(SimpleNamespace(_url='http://localhost:12345/document'), 'retrieve', 'known')
    assert result == response


def test_citation_locks_are_shared_while_in_use_and_reclaimed_afterwards():
    state = {}
    with ThreadPoolExecutor(max_workers=8) as pool:
        locks = list(pool.map(lambda _: citation_state_lock(state), range(32)))
    assert all(lock is locks[0] for lock in locks)
    assert citation_state_lock({}) is not locks[0]
    reference = weakref.ref(locks[0])
    del locks
    gc.collect()
    assert reference() is None
    with citation_state_lock(state):
        with citation_state_lock(state):
            pickle.dumps(state)


def test_build_image_url_map_tolerates_concurrent_citation_updates():
    state = {}
    reset_citation_state(state)

    registry = state['_image_url_registry']
    for index in range(5000):
        registry[f'initial-{index}'] = f'/static-files/initial-{index}.png'

    errors = []
    stop = threading.Event()

    def reader():
        try:
            for _ in range(100):
                build_image_url_map_from_config(state)
        except Exception as exc:  # pragma: no cover - assertion below reports the type.
            errors.append(exc)
        finally:
            stop.set()

    def writer():
        index = 0
        while not stop.is_set() and index < 10000:
            annotate_citations({
                'uid': f'node-{index}',
                'docid': f'doc-{index}',
                'text': f'/static-files/generated-{index}.png',
                'metadata': {},
            }, state)
            index += 1

    threads = [threading.Thread(target=reader), threading.Thread(target=writer)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert errors == []
