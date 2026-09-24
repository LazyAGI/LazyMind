from pathlib import Path
from tempfile import TemporaryDirectory

import pytest

from lazyllm.tools.writer.data_models import WritingContext
from lazyllm.tools.writer.tools import WriterContextTools
from lazyllm.tools.writer.utils import load_artifact_json
from lazymind.common.token_estimation import estimate_tokens
from lazymind.document_tools.selection import prepare_ir_requests
from lazymind.rewrite.base import BadRequestError
from lazymind.rewrite.context import input_budget, run_parallel_requests
from lazymind.rewrite.selection import (
    prepare_rewrite_requests, render_rewrite_request, resolve_markdown_selections,
)


def markdown_requests(source, selections, *, window=8192, context=None):
    targets = resolve_markdown_selections(source, selections)
    config = {'llm': {'max_input_tokens': window}}
    requests = prepare_rewrite_requests(source, targets, 'Improve clarity.',
                                        context=context, llm_config=config)
    assert all(estimate_tokens(render_rewrite_request(item)) <= input_budget(config)
               for item in requests)
    return requests


def test_short_document_uses_full_document_context():
    source = '# Report\n\nFirst paragraph.\n\nSecond paragraph.'
    request, = markdown_requests(source, [{'selected_text': 'First paragraph.'}])
    assert request['read_only_document'] == source
    assert 'read_only_context' not in request


def test_long_document_uses_local_neighbors():
    source = ('# Report\n\n## Alpha\n\nBefore.\n\nTarget.\n\nAfter.\n\n'
              + 'Unrelated appendix. ' * 12000)
    root = Path(__file__).resolve().parents[2] / 'tmp'
    root.mkdir(exist_ok=True)
    with TemporaryDirectory(prefix='rewrite-context-', dir=root) as directory:
        tools = WriterContextTools(artifact_store=directory)
        created = tools.create_writing_context(
            task={'query': 'Write a report.', 'task_type': 'write'}, document=source)
        context = load_artifact_json(created['artifact_path'], WritingContext).model_dump()
        request, = markdown_requests(source, [{'selected_text': 'Target.'}], context=context)

    assert 'read_only_document' not in request
    assert [item['content'] for item in request['read_only_context']['blocks']] == [
        'Before.', 'After.',
    ]
    # Exclude distant full blocks, but retain budgeted section/document excerpts.
    readonly = request['read_only_context']
    summary, = readonly['block_summaries']
    assert summary['content_ref']['heading_path'] == ['Report', 'Alpha']
    expected = next(item for item in context['block_summaries']
                    if item['content_ref'] == summary['content_ref'])
    assert summary['summary'] == expected['summary']
    assert summary['kind'] == 'excerpt'
    assert readonly['document_summary']['summary'] == context['document_summary']['summary']


def test_large_selection_is_grouped_without_losing_targets():
    texts = [f'Item {index}: ' + '正文内容。' * 150 for index in range(9)]
    source = '# Report\n\n' + '\n\n'.join(texts)
    requests = markdown_requests(
        source, [{'selected_text': f'Item {index}:'} for index in reversed(range(9))])
    assert len(requests) > 1
    assert [item['content'] for request in requests for item in request['paragraphs']] == texts


def test_single_oversized_target_fails_fast():
    source = 'Whole block: ' + '正文内容。' * 1000
    with pytest.raises(BadRequestError, match='exceeds the input token budget'):
        markdown_requests(source, [{'selected_text': 'Whole block:'}])


def test_ir_selection_is_grouped_without_unselected_sections():
    source = {'document_id': 'doc', 'title': 'Report', 'blocks': [
        {'node_id': 'chapter', 'type': 'heading', 'content': 'Chapter', 'children': [
            {'node_id': f'p{index}', 'type': 'paragraph',
             'content': f'Item {index}: ' + '正文内容。' * 150}
            for index in range(9)
        ]},
        {'node_id': 'appendix', 'type': 'heading', 'content': 'Appendix', 'children': [
            {'node_id': 'appendix-text', 'type': 'paragraph',
             'content': 'Unrelated appendix. ' * 3000},
        ]},
    ]}
    selections = [{'node_id': f'p{index}', 'selected_text': f'Item {index}:'}
                  for index in range(9)]
    _, targets, requests = prepare_ir_requests(
        source, 'Improve clarity.', selections, llm_config={'llm': {'max_input_tokens': 16384}})
    assert len(requests) > 1
    assert [item['ref'] for request in requests for item in request['paragraphs']] == [
        target.node_id for target in targets]
    assert all('Unrelated appendix.' not in str(request) for request in requests)


def test_parallel_failure_does_not_return_partial_results():
    def worker(item):
        if item == 'failed-group':
            raise RuntimeError('group failed')
        return item

    with pytest.raises(RuntimeError, match='group failed'):
        run_parallel_requests(['successful-group', 'failed-group'], worker)
