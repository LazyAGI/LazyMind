import signal
import threading
import inspect
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import lazyllm
from lazymind.model_config import inject_model_config

lazyllm.inject_model_config = inject_model_config

from lazyllm.tools.rag.parsing_service import DocumentProcessorWorker  # noqa: E402
from lazymind.config import config as _cfg  # noqa: E402
from lazymind.processor.service.db import require_shared_db_config  # noqa: E402


def _parse_high_priority_task_types(raw: str | None) -> list[str] | None:
    if raw is None or not raw.strip():
        return None
    return [item.strip() for item in raw.split(',') if item.strip()]


maintenance = os.getenv('LAZYMIND_MAINTENANCE_MODE') == 'installer-warmup'
db_config = require_shared_db_config('DocumentProcessorWorker')
worker_kwargs = {
    'port': _cfg['document_worker_port'],
    'db_config': db_config,
    'num_workers': _cfg['document_worker_num_workers'],
    'lease_duration': float(_cfg['document_worker_lease_duration']),
    'lease_renew_interval': float(_cfg['document_worker_lease_renew_interval']),
    'high_priority_task_types': _parse_high_priority_task_types(_cfg['document_worker_high_priority_task_types']),
    'high_priority_only': _cfg['document_worker_high_priority_only'],
    'poll_mode': _cfg['document_worker_poll_mode'],
}
supported_params = set(inspect.signature(DocumentProcessorWorker).parameters)
doc_processor_worker = None if maintenance else DocumentProcessorWorker(
    **{key: value for key, value in worker_kwargs.items() if key in supported_params}
)

_shutdown_event = threading.Event()


def _on_signal(signum, frame):
    _shutdown_event.set()
    try:
        if doc_processor_worker is not None:
            doc_processor_worker.stop()
    except Exception:
        pass


class _MaintenanceHealthHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/health':
            self.send_error(404)
            return
        payload = json.dumps({'status': 'ok', 'maintenance': True}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, format, *args):
        return


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, _on_signal)
    signal.signal(signal.SIGINT, _on_signal)
    server = None
    if maintenance:
        server = ThreadingHTTPServer(('127.0.0.1', int(_cfg['document_worker_port'])), _MaintenanceHealthHandler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
    else:
        doc_processor_worker.start()
    try:
        if maintenance:
            _shutdown_event.wait()
        else:
            doc_processor_worker.wait()
    except KeyboardInterrupt:
        pass
    finally:
        if server is not None:
            server.shutdown()
            server.server_close()
        _shutdown_event.set()
