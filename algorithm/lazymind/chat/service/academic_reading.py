"""Shared Core protocol for scoped academic tools."""
import httpx

from lazyllm.tools.agent import ToolExecutionError


def academic_request(client, method, path, *, payload=None, params=None, timeout=90):
    options = {'params': params} if params is not None else {'json': payload}
    response = client.transport.request(
        method, client.base_url + '/academic' + path,
        headers=client._headers(), timeout=timeout, **options)
    try:
        response.raise_for_status()
    except httpx.HTTPStatusError as exc:
        if response.status_code not in {400, 422}:
            raise
        error = response.json()
        raise ToolExecutionError(f"{error['code']}: {error['message']}") from exc
    value = response.json()
    return value.get('data', value)


def paper_read_payload(paper_id='', cursor='', *, view='content', keywords=None,
                       page_start=0, page_end=0, positions=None, reference_keys=None,
                       max_results=0, content_limit=5, reference_limit=20):
    if view not in ('content', 'references'):
        raise ToolExecutionError('view must be content or references')
    if cursor:
        conflicts = [name for name, value in (
            ('paper_id', paper_id), ('keywords', keywords), ('page_start', page_start),
            ('page_end', page_end), ('positions', positions), ('reference_keys', reference_keys),
            ('max_results', max_results)) if value]
        if conflicts:
            raise ToolExecutionError('Continue reading with only its cursor; omit these conflicting fields: '
                                     + ', '.join(conflicts))
        return {'cursor': cursor}
    if not paper_id:
        raise ToolExecutionError('paper_id is required without a cursor')
    if view == 'references' and (page_start or page_end or positions):
        raise ToolExecutionError('Reference view does not accept content page selectors')
    if view == 'content' and reference_keys:
        raise ToolExecutionError('Content view does not accept reference selectors')
    maximum = 20 if view == 'references' else 5
    if max_results < 0 or max_results > maximum:
        raise ToolExecutionError(f'max_results must be 1..{maximum}, or omitted for the default')
    limit = reference_limit if view == 'references' else content_limit
    payload = {'paper_id': paper_id, 'view': view, 'keywords': keywords or [],
               'max_results': min(max_results or 5, limit)}
    if view == 'references':
        payload['reference_keys'] = reference_keys or []
    else:
        payload.update(page_start=page_start, page_end=page_end)
        if positions:
            payload['positions'] = positions
    return payload


def reading_observation(value):
    """Keep original text and anchors intact while omitting repeated bibliographic detail."""
    if value.get('status') == 'stage_timeout':
        return value

    def identity(paper):
        return {key: paper[key] for key in (
            'paper_id', 'title', 'doi', 'arxiv_id', 'year', 'published_at', 'source_version_date',
            'source_provider', 'source_version', 'source_ref') if key in paper}

    material = {**value, 'paper': identity(value['paper'])}
    if 'references' in value:
        material['references'] = [
            {**row, 'paper': identity(row['paper'])} if row.get('paper') else row
            for row in value['references']]
    return material
