"""Academic workflow integration; HTTP context inherits the current Chat user."""
import copy
import threading

from typing_extensions import NotRequired, TypedDict
from lazyllm.tools.agent import ToolExecutionError
from lazymind.chat.workflow.workflow_manager import _client


class PaperAnalysis(TypedDict):
    """Analysis authored by the model; identity and state come from the server."""

    paper_id: str
    seed_relation: NotRequired[str]
    direction_relation: NotRequired[str]
    key_contribution: NotRequired[str]

    @classmethod
    def __get_pydantic_core_schema__(cls, source, handler):
        schema = handler(source)
        schema.pop('ref', None)
        return schema


_INPUT_KEYS = {'user_instruction', 'candidate_directions', 'selected_directions', 'papers'}
_ANALYSIS_FIELDS = ('seed_relation', 'direction_relation', 'key_contribution')
_PUBLISH_LOCK = threading.RLock()


def _invalid(message):
    raise ToolExecutionError('ARTIFACT_ARGUMENT_INVALID: ' + message)


def _bound(material):
    from lazymind.chat.engine.subagent.context import require_context
    values = require_context().params.get('remote_inputs') or {}
    if material not in values:
        raise RuntimeError('Required bound Workflow input is missing: ' + material)
    return copy.deepcopy(values[material])


def _expansion_id():
    value = _bound('expansion_id')
    if not isinstance(value, str) or not value.strip():
        raise RuntimeError('Bound expansion_id must be a nonempty string')
    return value


def _resolve(paper_ids):
    from urllib.parse import quote
    import httpx
    try:
        return _call('POST', '/expansions/' + quote(_expansion_id(), safe='') + '/papers:resolve',
                     {'paper_ids': paper_ids})
    except httpx.HTTPStatusError as exc:
        if exc.response.status_code in {400, 422}:
            _invalid('papers: one or more identities are duplicated or not registered in this expansion')
        raise


def _call(method, path, payload=None):
    api = _client()
    response = api.transport.request(method, api.base_url + '/academic' + path,
                                     headers=api._headers(), json=payload, timeout=90)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)


def academic_expansion_context(expansion_id: str):
    """Read the current user's expansion and its complete prior rounds."""
    from urllib.parse import quote
    return _call('GET', '/expansions/' + quote(expansion_id, safe=''))


def academic_search_papers(expansion_id: str, query: str, max_results: int = 10):
    """Discover external arXiv papers and save pinned revisions to this expansion."""
    return _call('POST', '/papers:search', {'expansion_id': expansion_id, 'query': query, 'max_results': max_results})


def academic_read_paper(expansion_id: str, paper_id: str = '', cursor: str = ''):
    """Read a saved seed or external candidate within this collection's boundary."""
    payload = ({'expansion_id': expansion_id, 'cursor': cursor} if cursor
               else {'expansion_id': expansion_id, 'paper_id': paper_id})
    return _call('POST', '/paper-contents:read', payload)


def _result_body(result):
    import json
    import os
    from pathlib import Path
    if isinstance(result, dict) and result.get('type') == 'json' and isinstance(result.get('path'), str):
        path = Path(result['path']).resolve()
        root = Path(os.getenv('LAZYMIND_SUBAGENT_WORKSPACE', '/app/data/subagent')).resolve()
        if not path.is_relative_to(root) or not path.is_file():
            raise ValueError('result file must be a managed Workflow artifact')
        result = json.loads(path.read_text(encoding='utf-8'))
    return result


def academic_validate_result(result: dict):
    """Validate ReviewWorkflowInput before the final step can succeed."""
    result = _result_body(result)
    if (not isinstance(result, dict) or not isinstance(result.get('user_instruction'), str)
            or not result['user_instruction'].strip()):
        raise ValueError('user_instruction must preserve the original research requirements')
    if set(result) != _INPUT_KEYS:
        raise ValueError('result must contain only user_instruction, candidate_directions, selected_directions, papers')
    for field in ('candidate_directions', 'selected_directions'):
        if (not isinstance(result.get(field), list) or not result[field]
                or any(not isinstance(x, str) or not x for x in result[field])):
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
        authors = paper.get('authors')
        if 'authors' in paper and (not isinstance(authors, list) or any(not isinstance(x, str) for x in authors)):
            raise ValueError('authors must be a string array')
        if 'year' in paper and (not isinstance(paper['year'], int) or isinstance(paper['year'], bool)):
            raise ValueError('year must be an integer')
        for field in ('is_seed', 'is_important'):
            if not isinstance(item.get(field), bool):
                raise ValueError(field + ' must be boolean')
        for field in ('seed_relation', 'direction_relation', 'key_contribution'):
            if field in item and not isinstance(item[field], str):
                raise ValueError(field + ' must be a plain description string, never an object or array; '
                                 'rewrite this field and retry compose_result')
    return {'status': 'valid'}


def _canonical_input(result):
    result = _result_body(result)
    if not isinstance(result, dict) or not _INPUT_KEYS.issubset(result):
        _invalid('result: required direction, instruction, and complete papers fields are missing')
    body = {key: copy.deepcopy(result[key]) for key in _INPUT_KEYS}
    try:
        academic_validate_result(body)
    except ValueError as exc:
        _invalid(str(exc))
    ids = [entry['paper']['paper_id'] for entry in body['papers']]
    if len(ids) != len(set(ids)):
        _invalid('papers: duplicate paper_id')
    resolved = _resolve(ids)
    originals = {entry['paper']['paper_id']: entry for entry in body['papers']}
    body['papers'] = resolved['papers']
    for entry in body['papers']:
        original = originals[entry['paper']['paper_id']]
        for field in _ANALYSIS_FIELDS:
            if field in original:
                entry[field] = original[field]
    return body, resolved['contents']


def academic_validate_stage(result: dict):
    """Validate each early stage without another model call or publishing placeholders."""
    from lazymind.chat.engine.subagent.context import require_context
    result = _result_body(result)
    step = require_context().params.get('step_id')
    if step == 'explore_directions':
        if (not isinstance(result, dict) or not isinstance(result.get('user_instruction'), str)
                or not result['user_instruction'].strip()):
            _invalid('direction_research.user_instruction: nonempty string required')
        candidates, selected = result.get('candidate_directions'), result.get('selected_directions')
        if (not isinstance(candidates, list) or not candidates
                or any(not isinstance(x, str) or not x.strip() for x in candidates)
                or not isinstance(selected, list) or any(not isinstance(x, str) for x in selected)
                or not set(selected).issubset(candidates)):
            _invalid('direction_research: candidate/selected directions must be matching string arrays')
        pending = result.get('needs_direction_selection')
        if not isinstance(pending, bool) or (not pending and not selected):
            _invalid('direction_research: confirmed scope requires at least one selected direction')
        if not isinstance(result.get('directions'), list):
            _invalid('direction_research.directions: array required')
    else:
        body, _ = _canonical_input(result)
        context = academic_expansion_context(_expansion_id())
        seeds = {p['paper']['paper_id'] for p in (context['record']['result'].get('papers') or []) if p['is_seed']}
        if not seeds.issubset({p['paper']['paper_id'] for p in body['papers']}):
            _invalid('papers: all selected seed papers must be retained')
    return {'status': 'valid'}


def _publish_once(key, result, tool):
    from lazymind.chat.engine.subagent.context import require_context
    from lazymind.chat.engine.subagent.tools import _save_artifact
    with _PUBLISH_LOCK:
        ctx = require_context()
        # Only the current task's own receipts are eligible, including resumed tasks.
        rows = [*(ctx.db.load_artifacts(ctx.task_id, keys=[key]) if ctx.db else []),
                *ctx.local_artifacts(keys=[key])]
        for row in rows:
            value = row.get('value') or {}
            value = _result_body(value.get('data', value))
            if value != result:
                _invalid(key + ': this attempt already published a different result; retry the step to revise it')
            return {'status': 'ok', 'already_published': True, 'paper_count': len(result['papers'])}
        saved = _save_artifact(key, result, content_type='json', source_tool=tool, internal_publish=True)
        return {'status': 'ok', 'paper_count': len(result['papers']), 'artifact': saved}


def academic_submit_relations(analyses: list[PaperAnalysis]):
    """Submit the complete selected-paper analysis once. Do not copy paper metadata.

    Args:
        analyses: One entry for every paper in bound expanded_papers. Each entry has
            paper_id and optional plain strings seed_relation, direction_relation,
            key_contribution. Keep failed full-text papers; omit unsupported analysis.
    """
    from lazymind.chat.engine.subagent.context import require_context
    if require_context().params.get('step_id') != 'analyze_relations':
        raise RuntimeError('academic_submit_relations is only available in analyze_relations')
    result, contents = _canonical_input(_bound('expanded_papers'))
    if not isinstance(analyses, list):
        _invalid('analyses: expected an array of paper analysis objects')
    supplied = {}
    for index, analysis in enumerate(analyses):
        if not isinstance(analysis, dict) or not isinstance(analysis.get('paper_id'), str):
            _invalid(f'analyses[{index}].paper_id: required string')
        if set(analysis) - {'paper_id', *_ANALYSIS_FIELDS}:
            _invalid(f'analyses[{index}]: only paper_id and the three analysis strings are accepted')
        identity = analysis['paper_id']
        if identity in supplied:
            _invalid(f'analyses[{index}].paper_id: duplicate identity')
        for field in _ANALYSIS_FIELDS:
            if field in analysis and not isinstance(analysis[field], str):
                _invalid(f'analyses[{index}].{field}: expected a plain string')
        supplied[identity] = analysis
    expected = {p['paper']['paper_id'] for p in result['papers']}
    if set(supplied) != expected:
        _invalid('analyses.paper_id: must cover exactly the bound expanded_papers; missing or additional identities')
    statuses = {state['paper_id']: state['status'] for state in contents}
    for entry in result['papers']:
        identity = entry['paper']['paper_id']
        analysis = supplied[identity]
        if statuses.get(identity) == 'ready' and not any(analysis.get(f, '').strip() for f in _ANALYSIS_FIELDS):
            _invalid('analyses: available paper ' + identity + ' requires at least one analysis description')
        for field in _ANALYSIS_FIELDS:
            entry.pop(field, None)
            if analysis.get(field, '').strip():
                entry[field] = analysis[field]
    academic_validate_result(result)
    return _publish_once('paper_relations', result, 'academic_submit_relations')


def academic_publish_expansion_result():
    """Publish the already validated bound relation result; no model-authored payload."""
    from lazymind.chat.engine.subagent.context import require_context
    if require_context().params.get('step_id') != 'compose_result':
        raise RuntimeError('academic_publish_expansion_result is only available in compose_result')
    result, _ = _canonical_input(_bound('paper_relations'))
    academic_validate_result(result)
    return _publish_once('expansion_result', result, 'academic_publish_expansion_result')
