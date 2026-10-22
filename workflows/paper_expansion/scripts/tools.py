"""Academic workflow integration; HTTP context inherits the current Chat user."""
from lazymind.chat.workflow.workflow_manager import _client


def _call(method, path, payload=None):
    api = _client()
    response = api.transport.request(method, api.base_url + '/academic' + path,
                                     headers=api._headers(), json=payload, timeout=30)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)


def academic_expansion_context(expansion_id: str):
    """Read the current user's expansion and its complete prior rounds."""
    from urllib.parse import quote
    return _call('GET', '/expansions/' + quote(expansion_id, safe=''))


def academic_demo_papers():
    """Get clearly labelled Mock discovery using real registered paper identities."""
    return _call('GET', '/demo-papers')


def academic_read_paper(paper_id: str, cursor: str = ''):
    """Read actual full text without importing; processing/error states are authoritative."""
    return _call('POST', '/paper-contents:read', {'cursor': cursor} if cursor else {'paper_id': paper_id})


def academic_validate_result(result: dict):
    """Validate ReviewWorkflowInput before the final step can succeed."""
    import json
    import os
    from pathlib import Path
    if isinstance(result, dict) and result.get('type') == 'json' and isinstance(result.get('path'), str):
        path = Path(result['path']).resolve()
        root = Path(os.getenv('LAZYMIND_SUBAGENT_WORKSPACE', '/app/data/subagent')).resolve()
        if not path.is_relative_to(root) or not path.is_file():
            raise ValueError('result file must be a managed Workflow artifact')
        result = json.loads(path.read_text(encoding='utf-8'))
    if not isinstance(result, dict) or not isinstance(result.get('user_instruction'), str) or not result['user_instruction'].strip():
        raise ValueError('user_instruction must preserve the original research requirements')
    for field in ('candidate_directions', 'selected_directions'):
        if not isinstance(result.get(field), list) or not result[field] or any(not isinstance(x, str) or not x for x in result[field]):
            raise ValueError(field + ' must be a nonempty string array')
    if not set(result['selected_directions']).issubset(result['candidate_directions']):
        raise ValueError('selected_directions must use candidate strings verbatim')
    if not isinstance(result.get('papers'), list):
        raise ValueError('papers must be the complete paper list')
    for item in result['papers']:
        if not isinstance(item, dict) or not isinstance(item.get('paper'), dict):
            raise ValueError('each paper must use the WorkflowPaper shape')
        paper = item['paper']
        if not isinstance(paper.get('paper_id'), str) or not paper['paper_id']:
            raise ValueError('paper_id must come from the server')
        for field in ('title', 'venue', 'doi', 'arxiv_id', 'paper_url', 'pdf_url'):
            if field in paper and not isinstance(paper[field], str):
                raise ValueError('paper.' + field + ' must be a string')
        if paper.get('abstract') is not None and not isinstance(paper['abstract'], str):
            raise ValueError('abstract must be the original string or null')
        if 'authors' in paper and (not isinstance(paper['authors'], list) or any(not isinstance(x, str) for x in paper['authors'])):
            raise ValueError('authors must be a string array')
        if 'year' in paper and (not isinstance(paper['year'], int) or isinstance(paper['year'], bool)):
            raise ValueError('year must be an integer')
        for field in ('is_seed', 'is_important'):
            if not isinstance(item.get(field), bool):
                raise ValueError(field + ' must be boolean')
        for field in ('seed_relation', 'direction_relation', 'key_contribution'):
            if field in item and not isinstance(item[field], str):
                raise ValueError(field + ' must be a plain description string, never an object or array; rewrite this field and retry compose_result')
    return {'status': 'valid'}
