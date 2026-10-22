import json
import os
from pathlib import Path
import subprocess
import sys


def test_doc_server_exports_routes_from_a_fresh_interpreter(tmp_path):
    """A warm pytest process can conceal the DocServer/processor import cycle."""
    root = Path(__file__).resolve().parents[3]
    output = tmp_path / 'doc-server-openapi.json'
    result = subprocess.run(
        [
            sys.executable,
            str(root / 'backend/core/doc/doc_server.py'),
            '--export-openapi',
            str(output),
        ],
        env={**os.environ, 'LAZYLLM_INIT_DOC': 'False'},
        capture_output=True,
        text=True,
        timeout=30,
    )

    assert result.returncode == 0, result.stderr
    spec = json.loads(output.read_text(encoding='utf-8'))
    assert '/v1/health' in spec['paths']
    assert '/v1/ready' in spec['paths']


def test_doc_server_relay_type_loads_without_launcher_preloads(tmp_path):
    """Relay children unpickle the service class without running its launcher."""
    payload = tmp_path / 'service-type.pickle'
    producer = subprocess.run(
        [sys.executable, '-c', '''
from pathlib import Path
import cloudpickle
import sys
from lazyllm.tools.rag.parsing_service import DocumentProcessor
from lazyllm.tools.rag.doc_service import DocServer
Path(sys.argv[1]).write_bytes(cloudpickle.dumps(DocServer._Impl))
''', str(payload)],
        env={**os.environ, 'LAZYLLM_INIT_DOC': 'False'},
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert producer.returncode == 0, producer.stderr

    consumer = subprocess.run(
        [sys.executable, '-c', '''
from pathlib import Path
import cloudpickle
import sys
service_type = cloudpickle.loads(Path(sys.argv[1]).read_bytes())
assert service_type.__qualname__ == 'DocServer._Impl'
''', str(payload)],
        env={**os.environ, 'LAZYLLM_INIT_DOC': 'False'},
        capture_output=True,
        text=True,
        timeout=30,
    )
    assert consumer.returncode == 0, consumer.stderr
