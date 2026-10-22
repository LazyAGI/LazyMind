import asyncio
import hashlib
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi import HTTPException
from lazymind.chat.api import academic_routes as ar


def test_pdf_bridge_validates_version_and_preserves_pages(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import ingest
    monkeypatch.setattr(ar, 'require_internal_token', lambda token: token)
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path))
    path = tmp_path / 'academic' / 'paper.pdf'
    path.parent.mkdir()
    path.write_bytes(b'%PDF-fixture')
    monkeypatch.setattr(ingest, 'parse_pdf_pages', lambda path: [(2, 'body B'), (1, 'body A')])
    payload = ar.PDFRequest(file_ref='paper.pdf', content_hash=hashlib.sha256(path.read_bytes()).hexdigest())
    result = asyncio.run(ar.parse_pdf(payload, 'token'))
    assert result == {'pages': [{'page': 1, 'text': 'body A'}, {'page': 2, 'text': 'body B'}]}
    path.write_bytes(b'%PDF-changed')
    with pytest.raises(HTTPException) as exc:
        asyncio.run(ar.parse_pdf(payload, 'token'))
    assert exc.value.status_code == 422


def test_pdf_bridge_rejects_unregistered_path(tmp_path, monkeypatch):
    monkeypatch.setattr(ar, 'require_internal_token', lambda token: token)
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path))
    outside = tmp_path / 'outside.pdf'
    outside.write_bytes(b'%PDF')
    with pytest.raises(HTTPException) as exc:
        asyncio.run(ar.parse_pdf(ar.PDFRequest(file_ref='../outside.pdf', content_hash='0' * 64), 'token'))
    assert exc.value.status_code == 422


def test_expansion_result_contract_rejects_nested_relation_metadata(tmp_path, monkeypatch):
    import copy
    import importlib.util
    import json
    from pathlib import Path
    import pytest

    path = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('paper_expansion_contract', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    result = {'user_instruction': 'original requirements', 'candidate_directions': ['tools'],
              'selected_directions': ['tools'], 'papers': [{'paper': {'paper_id': 'real-id', 'abstract': None},
              'is_seed': True, 'is_important': False, 'direction_relation': 'related to tools'}]}
    assert module.academic_validate_result(result)['status'] == 'valid'
    invalid = copy.deepcopy(result)
    invalid['papers'][0]['direction_relation'] = [{'direction': 'tools'}]
    with pytest.raises(ValueError, match='plain description string'):
        module.academic_validate_result(invalid)
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(tmp_path))
    result['papers'][0]['direction_relation'] = 'full description ' * 4000
    carrier = tmp_path / 'large.txt'
    carrier.write_text(json.dumps(result), encoding='utf-8')
    assert module.academic_validate_result({'type': 'json', 'path': str(carrier)})['status'] == 'valid'


@pytest.mark.parametrize('package,slot', [('paper_workflow_mock', 'mock_feedback'),
                                        ('paper_ppt_with_reflection_mock', 'ppt_request')])
def test_mock_publisher_preserves_authoritative_handoff(monkeypatch, package, slot):
    import importlib.util
    import json
    from lazymind.chat.engine.subagent import tools

    path = Path(__file__).resolve().parents[3] / 'workflows' / package / 'scripts/tools.py'
    spec = importlib.util.spec_from_file_location('academic_mock_publisher', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    frozen = {'mock': True, 'kind': 'ppt', 'input': {'papers': [{'paper': {'paper_id': 'original-paper'}}]},
              'papers': [{'paper_id': 'original-paper'}], 'reflection_markdown': 'saved reflection body',
              'feedback': 'Mock, no real PPT file'}
    def request(method, url, **kwargs):
        data = {'handoff_id': 'owned-handoff'} if method == 'GET' else frozen
        return SimpleNamespace(raise_for_status=lambda: None, json=lambda: data)
    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(base_url='http://core',
                        transport=SimpleNamespace(request=request), _headers=lambda: {}))
    monkeypatch.setattr(module, 'require_context', lambda: SimpleNamespace(conversation_id='owned-chat', output_slots=[slot]))
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda key, value, **kwargs:
                        published.append((key, value, kwargs)) or {'status': 'ok'})
    receipt = module.academic_mock_submit()
    assert receipt['artifact']['status'] == 'ok' and receipt['paper_count'] == 1
    assert len(published) == 1 and published[0][0] == slot
    value = published[0][1]
    saved = json.loads(value.split('```json\n')[1].split('\n```')[0]) if slot == 'mock_feedback' else value
    assert saved == frozen
