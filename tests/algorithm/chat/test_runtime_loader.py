import importlib
import json
import os
import subprocess
import sys
import threading
import time

from pathlib import Path
from types import SimpleNamespace


_REPO_ROOT = Path(__file__).resolve().parents[3]


def _subprocess_env():
    env = os.environ.copy()
    env.update({
        'PYTHONPATH': os.pathsep.join((
            str(_REPO_ROOT / 'algorithm' / 'lazyllm'),
            str(_REPO_ROOT / 'algorithm'),
        )),
        'PYTHONDONTWRITEBYTECODE': '1',
        'LAZYLLM_INIT_DOC': 'False',
        'LAZYMIND_ENABLE_ROUTER': 'false',
        'LAZYMIND_BACKGROUND_JOBS_ENABLED': 'false',
        'LAZYMIND_WORKFLOWS_DIR': str(_REPO_ROOT / 'workflows'),
    })
    return env


def _run_probe(script: str, timeout: float = 35) -> dict:
    completed = subprocess.run(
        [sys.executable, '-c', script],
        cwd=_REPO_ROOT,
        env=_subprocess_env(),
        capture_output=True,
        text=True,
        timeout=timeout,
        check=True,
    )
    return json.loads(completed.stdout.strip().splitlines()[-1])


def _fresh_loader():
    from lazymind.chat import runtime_loader
    return importlib.reload(runtime_loader)


def test_chat_runtime_loader_is_single_flight(monkeypatch):
    loader = _fresh_loader()
    sentinel = SimpleNamespace(name='chat-service')
    calls = []
    real_import = loader.importlib.import_module

    def fake_import(name):
        if name != 'lazymind.chat.service.chat_service':
            return real_import(name)
        calls.append(name)
        time.sleep(0.03)
        return sentinel

    monkeypatch.setattr(loader.importlib, 'import_module', fake_import)
    results = []
    threads = [threading.Thread(target=lambda: results.append(loader.ensure_chat_runtime())) for _ in range(5)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert calls == ['lazymind.chat.service.chat_service']
    assert results == [sentinel] * 5
    assert loader.chat_runtime_status() == 'ready'


def test_rag_runtime_loader_is_single_flight(monkeypatch):
    loader = _fresh_loader()
    sentinel = SimpleNamespace(name='kb')
    calls = []

    def fake_import(name):
        calls.append(name)
        time.sleep(0.03)
        return sentinel

    monkeypatch.setattr(loader.importlib, 'import_module', fake_import)

    results = []
    threads = [threading.Thread(target=lambda: results.append(loader.ensure_rag_runtime())) for _ in range(5)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert calls == ['lazymind.chat.engine.tools.kb']
    assert results == [sentinel] * 5
    assert loader.rag_runtime_status() == 'ready'


def test_background_warmup_waits_before_loading(monkeypatch):
    loader = _fresh_loader()
    calls = []
    monkeypatch.setattr(loader, '_wait_for_kb_runtime', lambda: calls.append('dependencies'))
    monkeypatch.setattr(loader, 'ensure_chat_runtime', lambda: calls.append('chat'))
    monkeypatch.setattr(loader, 'ensure_rag_runtime', lambda: calls.append('rag'))

    loader._wait_and_warm()

    assert calls == ['dependencies', 'chat', 'rag']


def test_chat_service_starts_under_two_seconds_without_rag():
    result = _run_probe('''
import json
import sys
import time
from lazymind.chat.runtime_loader import ensure_chat_runtime
started = time.perf_counter()
ensure_chat_runtime()
print(json.dumps({
    "elapsed": time.perf_counter() - started,
    "rag_loaded": "lazyllm.tools.rag" in sys.modules,
    "runtime_docs_loaded": any(name.startswith("lazyllm.docs.tools") for name in sys.modules),
}))
''')

    assert result['elapsed'] < 2.0
    assert result['rag_loaded'] is False
    assert result['runtime_docs_loaded'] is False


def test_trace_maintenance_registers_its_config_without_runtime_docs():
    result = _run_probe('''
import json
import sys
from lazymind.chat.service.utils.trace_archive import start_local_trace_maintenance
start_local_trace_maintenance()
from lazyllm.configs import config
print(json.dumps({
    "trace_backend": config["trace_backend"],
    "runtime_docs_loaded": any(name.startswith("lazyllm.docs.tools") for name in sys.modules),
}))
''')

    assert result['trace_backend']
    assert result['runtime_docs_loaded'] is False


def test_background_warmup_loads_rag_within_thirty_seconds_without_request():
    result = _run_probe('''
import json
import time
import lazymind.chat.runtime_loader as loader
loader._wait_for_kb_runtime = lambda: None
started = time.perf_counter()
loader.start_background_chat_runtime_warmup()
deadline = started + 30
while loader.rag_runtime_status() not in {"ready", "failed"} and time.perf_counter() < deadline:
    time.sleep(0.01)
print(json.dumps({
    "elapsed": time.perf_counter() - started,
    "chat": loader.chat_runtime_status(),
    "rag": loader.rag_runtime_status(),
}))
''')

    assert result['elapsed'] < 30
    assert result['chat'] == 'ready'
    assert result['rag'] == 'ready'


def test_kb_request_triggers_rag_before_background_dependencies_are_ready():
    result = _run_probe('''
import json
import threading
import time
import lazymind.chat.runtime_loader as loader
from lazymind.chat.engine.tools.lazy_kb import KBToolkit
dependency_gate = threading.Event()
loader._wait_for_kb_runtime = lambda: dependency_gate.wait(30)
loader.start_background_chat_runtime_warmup()
real_ensure = loader.ensure_rag_runtime
def load_then_stop():
    real_ensure()
    raise RuntimeError("stop after loading")
loader.ensure_rag_runtime = load_then_stop
started = time.perf_counter()
try:
    KBToolkit().kb_search("trigger warmup")
except RuntimeError as exc:
    assert str(exc) == "stop after loading"
print(json.dumps({
    "elapsed": time.perf_counter() - started,
    "rag": loader.rag_runtime_status(),
    "dependencies_released": dependency_gate.is_set(),
}))
dependency_gate.set()
''')

    assert result['elapsed'] < 30
    assert result['rag'] == 'ready'
    assert result['dependencies_released'] is False


def test_temporary_attachment_retrieval_triggers_rag_immediately():
    result = _run_probe('''
import json
import threading
import time
import lazymind.chat.runtime_loader as loader
from lazymind.chat.engine.tools.lazy_kb import kb_tmp_search
dependency_gate = threading.Event()
loader._wait_for_kb_runtime = lambda: dependency_gate.wait(30)
loader.start_background_chat_runtime_warmup()
real_ensure = loader.ensure_rag_runtime
def load_then_stop():
    real_ensure()
    raise RuntimeError("stop after loading")
loader.ensure_rag_runtime = load_then_stop
started = time.perf_counter()
try:
    kb_tmp_search("search attachment")
except RuntimeError as exc:
    assert str(exc) == "stop after loading"
print(json.dumps({
    "elapsed": time.perf_counter() - started,
    "rag": loader.rag_runtime_status(),
    "dependencies_released": dependency_gate.is_set(),
}))
dependency_gate.set()
''')

    assert result['elapsed'] < 30
    assert result['rag'] == 'ready'
    assert result['dependencies_released'] is False
