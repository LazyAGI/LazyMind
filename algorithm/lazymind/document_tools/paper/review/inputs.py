"""Keep the reviewer focused on the submitted manuscript and its references/figures."""
from copy import deepcopy
import re

from ..publication import chart_uri, prose


def select_materials(inputs):
    for key in ('papers', 'charts'):
        if inputs.get(key) is not None and (
                not isinstance(inputs[key], list) or any(not isinstance(item, dict) for item in inputs[key])):
            raise ValueError(f'{key} 必须是对象列表')
    body = prose(inputs.get('manuscript') or '')
    result = {key: deepcopy(inputs[key]) for key in ('topic', 'request', 'manuscript') if key in inputs}
    papers = []
    for paper in inputs.get('papers') or []:
        identifier = paper.get('id')
        if identifier is not None and not isinstance(identifier, str):
            raise ValueError('paper.id 必须是字符串')
        linked = identifier and re.search(
            r'(?:#paper-ref-' + re.escape(identifier) + r'(?![\w.-])|\[(?:文献:)?'
            + re.escape(identifier) + r'\])', body)
        named = any(isinstance(paper.get(key), str) and paper[key].strip() and paper[key].strip() in body
                    for key in ('title', 'citation', 'doi', 'url'))
        if not linked and not named:
            continue
        item = {key: deepcopy(paper[key]) for key in (
            'id', 'title', 'citation', 'authors', 'year', 'doi', 'url', 'text', 'abstract',
            'content_type', 'truncated', 'fallback', 'more',
        ) if key in paper}
        if item.get('text'):
            item.pop('abstract', None)
        reading = paper.get('reading')
        reading = reading[-1] if isinstance(reading, list) and reading else reading
        if isinstance(reading, dict):
            item['reading'] = {key: deepcopy(reading[key]) for key in
                               ('content_type', 'truncated', 'fallback', 'more') if key in reading}
        papers.append(item)
    result['papers'] = papers
    result['charts'] = [
        {key: deepcopy(chart[key]) for key in ('id', 'title', 'caption', 'svg', 'media_uri') if key in chart}
        for chart in inputs.get('charts') or []
        if (isinstance(chart.get('svg'), str) and chart_uri(chart) in body)
        or (isinstance(chart.get('id'), str) and re.search(
            r'(?:#block-|id="block-|media-placeholder://)' + re.escape(chart['id']) + r'(?![\w-])', body))
    ]
    return result
