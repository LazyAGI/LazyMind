import importlib
import runpy
import signal
import sys


class _FakeDocumentProcessorWorker:
    instances = []

    def __init__(
        self,
        db_config=None,
        num_workers=1,
        port=8001,
        task_poller=None,
        lease_duration=30.0,
        lease_renew_interval=5.0,
        high_priority_task_types=None,
        high_priority_only=False,
        poll_mode='queue',
        callback_task_statuses=None,
        callback_task_types=None,
        launcher=None,
        **kwargs,
    ):
        self.kwargs = {
            k: v for k, v in {
                'db_config': db_config,
                'num_workers': num_workers,
                'port': port,
                'lease_duration': lease_duration,
                'lease_renew_interval': lease_renew_interval,
                'high_priority_task_types': high_priority_task_types,
                'high_priority_only': high_priority_only,
                'poll_mode': poll_mode,
            }.items()
        }
        self.started = False
        self.waited = False
        self.stopped = False
        _FakeDocumentProcessorWorker.instances.append(self)

    def start(self):
        self.started = True

    def wait(self):
        self.waited = True

    def stop(self):
        self.stopped = True


def _fresh_import_worker(monkeypatch):
    from lazyllm.tools.rag import parsing_service
    import lazymind.processor.service.db

    _FakeDocumentProcessorWorker.instances = []
    monkeypatch.setattr(lazymind.processor.service.db, 'require_shared_db_config', lambda service_name: {'service': service_name})
    # Patch parsing_service AFTER capturing the real class so supported_params still works.
    # We replace it with _FakeDocumentProcessorWorker which has the same explicit signature.
    monkeypatch.setattr(parsing_service, 'DocumentProcessorWorker', _FakeDocumentProcessorWorker)
    sys.modules.pop('lazymind.processor.service.worker', None)
    return importlib.import_module('lazymind.processor.service.worker')


def test_worker_constructs_document_processor_worker_from_env(monkeypatch):
    from lazymind.config import config as _cfg

    monkeypatch.setitem(_cfg._impl, 'document_worker_port', 8124)
    monkeypatch.setitem(_cfg._impl, 'document_worker_num_workers', 3)
    monkeypatch.setitem(_cfg._impl, 'document_worker_lease_duration', '12.5')
    monkeypatch.setitem(_cfg._impl, 'document_worker_lease_renew_interval', '2.5')
    monkeypatch.setitem(_cfg._impl, 'document_worker_high_priority_task_types', 'parse, index')
    monkeypatch.setitem(_cfg._impl, 'document_worker_high_priority_only', True)
    monkeypatch.setitem(_cfg._impl, 'document_worker_poll_mode', 'queue')

    module = _fresh_import_worker(monkeypatch)

    assert module.db_config == {'service': 'DocumentProcessorWorker'}
    assert module.doc_processor_worker is _FakeDocumentProcessorWorker.instances[-1]
    assert module.doc_processor_worker.kwargs == {
        'port': 8124,
        'db_config': {'service': 'DocumentProcessorWorker'},
        'num_workers': 3,
        'lease_duration': 12.5,
        'lease_renew_interval': 2.5,
        'high_priority_task_types': ['parse', 'index'],
        'high_priority_only': True,
        'poll_mode': 'queue',
    }


def test_worker_signal_handler_sets_shutdown_and_stops_worker(monkeypatch):
    module = _fresh_import_worker(monkeypatch)

    module._on_signal(None, None)

    assert module._shutdown_event.is_set()
    assert module.doc_processor_worker.stopped is True


def test_worker_signal_handler_ignores_stop_errors(monkeypatch):
    module = _fresh_import_worker(monkeypatch)

    class BrokenWorker:
        def stop(self):
            raise RuntimeError('stop failed')

    module.doc_processor_worker = BrokenWorker()

    module._on_signal(None, None)

    assert module._shutdown_event.is_set()


def test_worker_main_starts_waits_and_registers_signals(monkeypatch):
    from lazyllm.tools.rag import parsing_service
    import lazymind.processor.service.db
    import threading

    _FakeDocumentProcessorWorker.instances = []
    signal_calls = []

    class FakeEvent:
        def __init__(self):
            self.waited = False

        def set(self):
            return None

        def is_set(self):
            return False

        def wait(self):
            self.waited = True
            return None

    monkeypatch.setattr(parsing_service, 'DocumentProcessorWorker', _FakeDocumentProcessorWorker)
    monkeypatch.setattr(lazymind.processor.service.db, 'require_shared_db_config', lambda service_name: {'service': service_name})
    monkeypatch.setattr(threading, 'Event', FakeEvent)
    monkeypatch.setattr(signal, 'signal', lambda sig, handler: signal_calls.append((sig, handler.__name__)))
    monkeypatch.setenv('LAZYMIND_DOCUMENT_WORKER_PORT', '8126')
    sys.modules.pop('lazymind.processor.service.worker', None)

    runpy.run_module('lazymind.processor.service.worker', run_name='__main__')

    instance = _FakeDocumentProcessorWorker.instances[0]
    assert instance.started is True
    assert instance.waited is True
    assert (signal.SIGTERM, '_on_signal') in signal_calls
    assert (signal.SIGINT, '_on_signal') in signal_calls


def test_worker_main_handles_keyboard_interrupt_from_wait(monkeypatch):
    from lazyllm.tools.rag import parsing_service
    import lazymind.processor.service.db
    import threading

    signal_calls = []

    class InterruptingDocumentProcessorWorker(_FakeDocumentProcessorWorker):
        def wait(self):
            self.waited = True
            raise KeyboardInterrupt

    class FakeEvent:
        def set(self):
            return None

        def wait(self):
            return None

    monkeypatch.setattr(parsing_service, 'DocumentProcessorWorker', InterruptingDocumentProcessorWorker)
    monkeypatch.setattr(lazymind.processor.service.db, 'require_shared_db_config', lambda service_name: {'service': service_name})
    monkeypatch.setattr(threading, 'Event', FakeEvent)
    monkeypatch.setattr(signal, 'signal', lambda sig, handler: signal_calls.append((sig, handler.__name__)))
    sys.modules.pop('lazymind.processor.service.worker', None)

    runpy.run_module('lazymind.processor.service.worker', run_name='__main__')

    assert signal_calls
