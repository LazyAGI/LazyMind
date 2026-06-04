import importlib
import runpy
import signal
import sys


class _FakeDocumentProcessor:
    instances = []

    def __init__(self, **kwargs):
        self.kwargs = kwargs
        self.started = False
        self.waited = False
        self.stopped = False
        _FakeDocumentProcessor.instances.append(self)

    def start(self):
        self.started = True

    def wait(self):
        self.waited = True

    def stop(self):
        self.stopped = True


def _fresh_import_server(monkeypatch):
    from lazyllm.tools.rag import parsing_service
    import lazymind.processor.service.db

    _FakeDocumentProcessor.instances = []
    monkeypatch.setattr(parsing_service, 'DocumentProcessor', _FakeDocumentProcessor)
    monkeypatch.setattr(lazymind.processor.service.db, 'require_shared_db_config', lambda service_name: {'service': service_name})
    sys.modules.pop('lazymind.processor.service.server', None)
    return importlib.import_module('lazymind.processor.service.server')


def test_server_constructs_document_processor_from_env(monkeypatch):
    monkeypatch.setenv('LAZYMIND_DOCUMENT_PROCESSOR_PORT', '8123')

    module = _fresh_import_server(monkeypatch)

    assert module.db_config == {'service': 'DocumentProcessor'}
    assert module.doc_processor is _FakeDocumentProcessor.instances[0]
    assert module.doc_processor.kwargs == {
        'port': 8123,
        'db_config': {'service': 'DocumentProcessor'},
        'num_workers': 0,
    }


def test_server_signal_handler_sets_shutdown_and_stops_processor(monkeypatch):
    module = _fresh_import_server(monkeypatch)

    module._on_signal(None, None)

    assert module._shutdown_event.is_set()
    assert module.doc_processor.stopped is True


def test_server_signal_handler_ignores_stop_errors(monkeypatch):
    module = _fresh_import_server(monkeypatch)

    class BrokenProcessor:
        def stop(self):
            raise RuntimeError('stop failed')

    module.doc_processor = BrokenProcessor()

    module._on_signal(None, None)

    assert module._shutdown_event.is_set()


def test_server_main_starts_waits_and_registers_signals(monkeypatch):
    from lazyllm.tools.rag import parsing_service
    import lazymind.processor.service.db
    import threading

    _FakeDocumentProcessor.instances = []
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

    monkeypatch.setattr(parsing_service, 'DocumentProcessor', _FakeDocumentProcessor)
    monkeypatch.setattr(lazymind.processor.service.db, 'require_shared_db_config', lambda service_name: {'service': service_name})
    monkeypatch.setattr(threading, 'Event', FakeEvent)
    monkeypatch.setattr(signal, 'signal', lambda sig, handler: signal_calls.append((sig, handler.__name__)))
    monkeypatch.setenv('LAZYMIND_DOCUMENT_PROCESSOR_PORT', '8125')
    sys.modules.pop('lazymind.processor.service.server', None)

    runpy.run_module('lazymind.processor.service.server', run_name='__main__')

    instance = _FakeDocumentProcessor.instances[0]
    assert instance.started is True
    assert instance.waited is True
    assert (signal.SIGTERM, '_on_signal') in signal_calls
    assert (signal.SIGINT, '_on_signal') in signal_calls


def test_server_main_handles_keyboard_interrupt_from_wait(monkeypatch):
    from lazyllm.tools.rag import parsing_service
    import lazymind.processor.service.db
    import threading

    signal_calls = []

    class InterruptingDocumentProcessor(_FakeDocumentProcessor):
        def wait(self):
            self.waited = True
            raise KeyboardInterrupt

    class FakeEvent:
        def set(self):
            return None

        def wait(self):
            return None

    monkeypatch.setattr(parsing_service, 'DocumentProcessor', InterruptingDocumentProcessor)
    monkeypatch.setattr(lazymind.processor.service.db, 'require_shared_db_config', lambda service_name: {'service': service_name})
    monkeypatch.setattr(threading, 'Event', FakeEvent)
    monkeypatch.setattr(signal, 'signal', lambda sig, handler: signal_calls.append((sig, handler.__name__)))
    sys.modules.pop('lazymind.processor.service.server', None)

    runpy.run_module('lazymind.processor.service.server', run_name='__main__')

    assert signal_calls
