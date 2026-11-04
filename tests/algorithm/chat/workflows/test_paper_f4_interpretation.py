"""The integrated conclusion is delivered as Markdown without numeric prose gates."""
import json
from unittest.mock import Mock

from lazymind.document_tools.paper.analysis.interpretation import interpret
from lazymind.document_tools.paper.analysis.render import render


def test_executed_results_produce_one_unchanged_markdown_conclusion():
    metadata = dict(label='Mean score', metric='score', method='mean', scope='Observed sample',
                    coordinates={}, comparison='')
    analysis = dict(findings=[dict(value=0.5, calculation=json.dumps(metadata))],
                    targets=[], methods=['mean'], models=[], limitations=[])
    markdown = '## 综合结论\n\n2026 年样本均值为 0.5；本次模拟不能推断真实任务表现。'
    call = Mock(return_value=markdown)
    inputs = dict(data=dict(columns=['score'], column_types=['number'], units=[None], rows=[[0.5]]))
    text, record = interpret(inputs, analysis, dict(figures=[]), call)
    assert text == render(dict(summary=text)) == markdown
    assert record['format'] == 'markdown'
    sent = {}
    for message in call.call_args.args[0]['input']:
        if message['role'] == 'user':
            sent.update(json.loads(message['content'].split('INPUT_JSON:\n', 1)[1]))
    assert sent['analysis_results'][0]['value'] == 0.5
    assert sent['tables']['data']['rows'] == [[0.5]]
    assert 'schema' not in sent
    call.assert_called_once()
