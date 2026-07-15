import signal
import threading
import inspect

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
doc_processor_worker = DocumentProcessorWorker(
    **{key: value for key, value in worker_kwargs.items() if key in supported_params}
)

_shutdown_event = threading.Event()


def _on_signal(signum, frame):
    _shutdown_event.set()
    try:
        doc_processor_worker.stop()
    except Exception:
        pass


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, _on_signal)
    signal.signal(signal.SIGINT, _on_signal)
    doc_processor_worker.start()
    try:
        doc_processor_worker.wait()
    except KeyboardInterrupt:
        pass
    # Keep process alive; wait() may return immediately with some launcher configs
    _shutdown_event.wait()
