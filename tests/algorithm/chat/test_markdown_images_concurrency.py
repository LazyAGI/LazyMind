import threading

from lazymind.chat.service.utils.citations import (
    annotate_citations,
    reset_citation_state,
)
from lazymind.chat.service.utils.markdown_images import build_image_url_map_from_config


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
