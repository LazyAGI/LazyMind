import json
from concurrent.futures import ThreadPoolExecutor

from lazymind.chat.service.local_observation import LocalObservationWriter


def test_writer_appends_redacted_summary_and_full_records(tmp_path):
    writer = LocalObservationWriter(tmp_path, max_bytes=10_000)
    writer.write_summary({'run_id': 'r1', 'prompt': 'secret', 'metrics': {'model_ms': 3}})
    writer.write_full({'run_id': 'r1', 'output': 'secret output', 'api_key': 'secret key'})

    summary = json.loads((tmp_path / 'performance-summary.jsonl').read_text().strip())
    full = json.loads((tmp_path / 'performance-full.jsonl').read_text().strip())
    assert summary == {'run_id': 'r1', 'metrics': {'model_ms': 3}}
    assert full == {'run_id': 'r1'}


def test_writer_serializes_concurrent_jsonl_appends(tmp_path):
    writer = LocalObservationWriter(tmp_path)
    with ThreadPoolExecutor(max_workers=8) as pool:
        list(pool.map(lambda i: writer.write_summary({'run_id': str(i)}), range(40)))

    rows = [json.loads(line) for line in (tmp_path / 'performance-summary.jsonl').read_text().splitlines()]
    assert len(rows) == 40
    assert {row['run_id'] for row in rows} == {str(i) for i in range(40)}
