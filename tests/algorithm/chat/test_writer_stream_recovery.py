import json
import time

from types import SimpleNamespace

import pytest

from lazymind.document_tools import writing as writer


def _instruction(title='研究方法'):
    return SimpleNamespace(section_title=title)


def test_markdown_section_uses_relative_heading_levels_and_ignores_fenced_code():
    result = writer._normalize_streamed_markdown_section(
        '## 研究方法\n\n### 研究方法\n\n#### 研究设计\n\n##### 数据来源\n\n'
        '```markdown\n# 示例标题\n```',
        _instruction(),
    )

    assert result.count('## 研究方法') == 1
    assert '### 研究设计' in result
    assert '#### 数据来源' in result
    assert '```markdown\n# 示例标题\n```' in result


def test_heading_validation_failure_is_recovered_and_checkpointed(monkeypatch, tmp_path):
    calls = []

    class FakeInstruction:
        @classmethod
        def model_validate(cls, value):
            return SimpleNamespace(section_title=value['section_title'])

    class FakeStream:
        result_called = False

        def __enter__(self):
            return self

        def __exit__(self, *args):
            return None

        def __iter__(self):
            yield '## 研究方法\n\n'
            yield '### 研究方法\n\n'
            yield '#### 数据来源\n\n正文'
            # DraftPreviewStream finalizes while the iterator is being exhausted,
            # before callers can reach result(). Match that real lifecycle here.
            raise ValueError(writer._MARKDOWN_DRAFT_ROOT_ERROR)

        def result(self):
            FakeStream.result_called = True
            raise AssertionError('result() must not be reached after iterator finalization fails')

    class FakeDrafting:
        def __init__(self, **kwargs):
            pass

        def stream_draft_section(self, **kwargs):
            calls.append(kwargs)
            return FakeStream()

    monkeypatch.setattr(writer, 'AutoModel', lambda **kwargs: object())
    monkeypatch.setattr(writer, 'SectionInstruction', FakeInstruction)
    monkeypatch.setattr(writer, 'WriterDraftingTools', FakeDrafting)
    monkeypatch.setattr(writer, '_temp_root', lambda: tmp_path / 'temporary')
    monkeypatch.setattr(writer, '_write_input_artifact', lambda *args, **kwargs: '/input.json')
    (tmp_path / 'temporary').mkdir()
    checkpoint_dir = tmp_path / 'checkpoints'
    instructions = json.dumps({'instructions': [{'section_title': '研究方法',
                                                'required_points': ['说明来源'], 'expected_blocks': ['说明来源']}]})
    toolkit = writer.WriterWritingCapabilities()

    first = json.loads(toolkit.stream_draft_blocks_markdown(
        writing_task_json='{"query":"Write","task_type":"write"}',
        section_instructions_json=instructions,
        writing_context_json='{"context_id":"ctx"}',
        on_delta=lambda value: None,
        checkpoint_dir=str(checkpoint_dir),
    ))
    second = json.loads(toolkit.stream_draft_blocks_markdown(
        writing_task_json=json.dumps({"query": "Write", "task_type": "write", "task_id": "new",
                                     "meta": {"source": "retry"}, "constraints": {"max_chars": None}}),
        section_instructions_json=instructions,
        writing_context_json=json.dumps({'context_id': 'regenerated', 'doc_id': 'new-storage-id',
                                         'meta': {'source': 'refreshed', 'timestamp': 'later'}}),
        on_delta=lambda value: None,
        checkpoint_dir=str(checkpoint_dir),
    ))

    assert len(calls) == 1
    assert not FakeStream.result_called
    assert first == second
    assert first[0].startswith('## 研究方法')
    assert '### 数据来源' in first[0]

    changes = [
        {'facts': [{'fact_id': 'f', 'key': 'result', 'value': 'New evidence', 'source': ['source']}]},
        {'style_profile': {'tone': 'formal'}},
        {'document_summary': {'summary': 'New manuscript'}},
        {'block_summaries': [{'content_ref': {'heading_path': ['Title']}, 'summary': 'Changed block'}]},
    ]
    for expected_calls, effective_change in enumerate(changes, start=2):
        json.loads(toolkit.stream_draft_blocks_markdown(
            writing_task_json='{"query":"Write","task_type":"write"}', section_instructions_json=instructions,
            writing_context_json=json.dumps({'context_id': 'regenerated', **effective_change}),
            on_delta=lambda value: None, checkpoint_dir=str(checkpoint_dir),
        ))
        assert len(calls) == expected_calls

    json.loads(toolkit.stream_draft_blocks_markdown(
        writing_task_json='{"query":"Different requirement","task_type":"write"}',
        section_instructions_json=instructions, writing_context_json='{"context_id":"ctx"}',
        on_delta=lambda value: None, checkpoint_dir=str(checkpoint_dir),
    ))
    assert len(calls) == 6


@pytest.mark.parametrize('failure', [False, True])
def test_whole_document_timing_preserves_stream_result_and_errors(monkeypatch, failure):
    records, deltas = [], []

    class Stream:
        timings = {'started_at_unix_ms': time.time() * 1000 - 100,
                   'call_started_ms': 5, 'first_response_ms': 25,
                   'first_visible_ms': 40, 'last_event_ms': 85,
                   'observed_elapsed_ms': 100,
                   'generation_finished_ms': None if failure else 90}

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def __iter__(self):
            yield '# Test'
            if failure:
                raise ValueError('generation failed')

        def result(self):
            return {'content': 'result'}

    monkeypatch.setattr(writer, 'AutoModel', lambda **kw: object())
    monkeypatch.setattr(writer, 'WriterDraftingTools', lambda **kw: SimpleNamespace(
        stream_whole_document=lambda **args: Stream()))
    monkeypatch.setattr(writer, '_primary_data', lambda value: value['content'])
    monkeypatch.setattr(writer, 'LOG', SimpleNamespace(info=records.append))
    if failure:
        with pytest.raises(ValueError, match='generation failed'):
            writer.stream_whole_document('task', 'context', artifact_store='unused', on_delta=deltas.append)
    else:
        assert writer.stream_whole_document(
            'task', 'context', artifact_store='unused', on_delta=deltas.append) == 'result'
    assert deltas == ['# Test']
    assert len(records) == 1
    record = json.loads(records[0].removeprefix('[WriterTiming] '))
    assert record['status'] == ('error' if failure else 'completed')
    assert record['queue_wait_ms'] == 5
    assert record['response_wait_ms'] == 20
    assert record['visible_wait_ms'] == 35
    assert record['response_to_generation_end_ms'] == (None if failure else 65)
    assert record['last_event_to_generation_end_ms'] == (None if failure else 5)
    assert record['after_generation_ms'] == (None if failure else 10)
    assert '# Test' not in records[0]


def test_whole_document_setup_failure_records_missing_milestones(monkeypatch):
    records = []
    def fail(**kwargs):
        raise RuntimeError('setup failed')
    monkeypatch.setattr(writer, 'AutoModel', fail)
    monkeypatch.setattr(writer, 'LOG', SimpleNamespace(info=records.append))
    with pytest.raises(RuntimeError, match='setup failed'):
        writer.stream_whole_document('task', 'context', artifact_store='unused')
    record = json.loads(records[0].removeprefix('[WriterTiming] '))
    assert record['status'] == 'error'
    assert record['response_wait_ms'] is None
    assert record['response_to_generation_end_ms'] is None
    assert record['after_generation_ms'] is None


@pytest.mark.parametrize('failure', [False, True])
def test_writer_artifact_timing_preserves_batch_preflight(monkeypatch, tmp_path, failure):
    from lazymind.chat.engine.subagent import tools, context
    from lazymind.chat.engine.agent_runtime import telemetry
    from lazymind.chat.engine.subagent.context import SubAgentContext

    records, events = [], []
    ctx = SubAgentContext(
        task_id='timing-task', conversation_id='timing-conversation',
        agent_type='workflow_step', objective='',
        params={'workflow_id': 'writer-workflow', 'revision_id': 'baseline'},
        workspace_path=str(tmp_path), input_slots=[],
        output_slots=['draft_document', 'section_instructions'],
        db=SimpleNamespace(next_artifact_seq=lambda *args: 1), emit=events.append,
    )
    monkeypatch.setattr(context, 'get_context', lambda: ctx)
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_record_artifact_coord', lambda *args, **kwargs: None)
    monkeypatch.setattr(telemetry.lazyllm, 'LOG', SimpleNamespace(info=records.append))
    body = tmp_path / 'body.md'
    body.write_text('# Private body\nContent')
    entries = [
        {'key': 'draft_document', 'content_type': 'file', 'value': str(body)},
        {'key': 'section_instructions', 'content_type': 'file',
         'value': str(tmp_path / 'missing.json') if failure else str(body)},
    ]
    if failure:
        with pytest.raises(Exception):
            tools.save_artifacts(entries)
        assert events == []  # A later invalid file still prevents all publication.
    else:
        assert tools.save_artifacts(entries)['saved_count'] == 2
        assert [event['slot'] for event in events] == ['draft_document', 'section_instructions']
    timings = [json.loads(line.removeprefix('[WriterStageTiming] ')) for line in records]
    ends = [row for row in timings if row['event'] == 'end']
    assert len(ends) == (2 if failure else 4)
    assert ends[-1]['status'] == ('error' if failure else 'completed')
    assert all(row['elapsed_ms'] >= 0 for row in ends)
    assert all(row['workflow_revision'] == 'baseline' for row in ends)
    assert 'Private body' not in '\n'.join(records)
    assert str(tmp_path) not in '\n'.join(records)


def test_writer_stage_timing_preserves_errors_when_logging_fails(monkeypatch):
    from lazymind.chat.engine.agent_runtime import telemetry
    from lazymind.chat.engine.subagent import context

    monkeypatch.setattr(context, 'get_context', lambda: SimpleNamespace(
        task_id='task', conversation_id='conversation', params={'workflow_id': 'writer-workflow'}))
    def broken_log(*args):
        raise RuntimeError('logging failed')
    monkeypatch.setattr(telemetry.lazyllm, 'LOG', SimpleNamespace(info=broken_log))
    with telemetry.writer_stage_timing('visual_media'):
        pass
    with pytest.raises(ValueError, match='original failure'):
        with telemetry.writer_stage_timing('visual_media'):
            raise ValueError('original failure')


@pytest.mark.parametrize('wall_jump', [-3600, 3600])
def test_whole_document_tail_uses_stream_clock_despite_wall_jump(monkeypatch, wall_jump):
    from lazyllm.tools.writer.tools import stream_tools

    clock = {'monotonic': 10.0, 'wall': 100000.0}
    monkeypatch.setattr(stream_tools, 'time', SimpleNamespace(
        monotonic=lambda: clock['monotonic'], time=lambda: clock['wall']))
    records = []

    def call(sink):
        clock['monotonic'] = 10.05
        sink({'tag': 'text', 'delta': '# Test\n\nBody'})
        clock['monotonic'] = 10.1
        return '# Test\n\nBody'

    def finalize(body):
        clock['wall'] += wall_jump
        clock['monotonic'] = 10.15
        return {'body': body}

    monkeypatch.setattr(writer, 'AutoModel', lambda **kw: object())
    monkeypatch.setattr(writer, 'WriterDraftingTools', lambda **kw: SimpleNamespace(
        stream_whole_document=lambda **args: stream_tools.DraftMarkdownStream(
            call=call, finalize=finalize, prefix='', idle_timeout=60)))
    monkeypatch.setattr(writer, '_primary_data', lambda value: value['body'])
    monkeypatch.setattr(writer, 'LOG', SimpleNamespace(info=records.append))
    assert writer.stream_whole_document('task', 'context', artifact_store='unused') == '# Test\n\nBody'
    record = json.loads(records[0].removeprefix('[WriterTiming] '))
    assert record['generation_finished_ms'] == 100
    assert record['observed_elapsed_ms'] == 150
    assert record['after_generation_ms'] == 50
