import asyncio
import json
import threading
import uuid
from concurrent.futures import TimeoutError as FutureTimeout
from types import SimpleNamespace
from unittest.mock import Mock

import lazyllm
import pytest

from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.agent_runtime import cancellation, executor as executor_mod
from lazymind.chat.workflow.workflow_manager import guard_workflow_agent_stream
from lazymind.document_tools import writing as writer


@pytest.fixture(autouse=True)
def session():
    sid = f'lifecycle-{uuid.uuid4().hex}'
    with lazyllm.globals._bind_sid(sid), lazyllm.locals._scope():
        yield
    lazyllm.globals._clear_sid(sid)


def test_request_cancel_targets_sid_without_changing_caller_context():
    caller = lazyllm.globals._sid, lazyllm.locals._sid
    target = f'target-{uuid.uuid4().hex}'
    cancellation.request_cancel(target)
    assert (lazyllm.globals._sid, lazyllm.locals._sid) == caller
    assert cancellation.make_cancel_stop_condition()(None) is False
    with lazyllm.globals._bind_sid(target):
        with pytest.raises(cancellation.UserCancelledError):
            cancellation.make_cancel_stop_condition()(None)
    assert (lazyllm.globals._sid, lazyllm.locals._sid) == caller


def test_workflow_generator_close_propagates_immediately():
    closed = []

    async def source():
        try:
            yield 'event', {'delta': 'ready'}
            await asyncio.Event().wait()
        finally:
            closed.append(True)

    async def scenario():
        inner = source()
        outer = guard_workflow_agent_stream(inner)
        await anext(outer)
        try:
            await outer.aclose()
            assert closed == [True]
        finally:
            await inner.aclose()

    asyncio.run(scenario())


def test_executor_joins_before_reset_and_marks_early_close_unsuccessful(monkeypatch):
    release, exited = threading.Event(), threading.Event()
    monitor, notice = Mock(), Mock()
    events, cancelled = [], []
    parent_state = lazyllm.locals['_lazyllm_agent']

    class Agent:
        _exact_repeat_monitor = monitor
        _runtime_notice_buffer = notice

        def __call__(self, _query):
            assert lazyllm.locals['_lazyllm_agent'] is parent_state
            lazyllm.FileSystemQueue().enqueue(json.dumps({'tag': 'text', 'delta': 'ready'}))
            try:
                assert release.wait(5)
                assert monitor.reset.call_count == 1
                assert notice.clear.call_count == 1
                cancellation.make_cancel_stop_condition()(None)
            except cancellation.UserCancelledError:
                cancelled.append(True)
                raise
            finally:
                exited.set()

    monkeypatch.setattr(executor_mod, 'telemetry_enabled', lambda: True)
    monkeypatch.setattr(executor_mod, 'append_event', lambda name, **data: events.append((name, data)))
    plan = AgentRunPlan(role=AgentRole.CHAT, tools=[],
                       prompt=PromptBuilder.for_role(AgentRole.CHAT).input('A', source='user').build(),
                       execution_options=AgentExecutionOptions())

    async def scenario():
        notified = asyncio.Event()

        def request(sid):
            cancellation.request_cancel(sid)
            notified.set()

        monkeypatch.setattr(executor_mod, 'request_cancel', request, raising=False)
        stream = AgentExecutor().stream_agent(Agent(), plan)
        outer = guard_workflow_agent_stream(stream)
        assert (await anext(outer))[0] == 'event'
        task = asyncio.create_task(outer.aclose())
        try:
            await asyncio.wait_for(notified.wait(), 1)
            assert not task.done()
            assert not exited.is_set()
            task.cancel('repeated close cancellation')
            await asyncio.sleep(0)
            assert not task.done()
        finally:
            release.set()
            try:
                await task
            finally:
                await stream.aclose()
        assert exited.is_set()
        assert cancelled == [True]
        assert monitor.reset.call_count == notice.clear.call_count == 2
        assert next(data for name, data in events if name == 'run_end')['ok'] is False

    asyncio.run(scenario())


def test_parallel_writer_waits_for_sections_and_stream_children(monkeypatch, tmp_path):
    from lazyllm.tools.writer.tools.stream_tools import DraftPreviewStream

    release, closing = threading.Event(), threading.Event()
    both_started = threading.Barrier(3)
    producer_exits = []
    source_pool = writer.ThreadPoolExecutor

    class Pool(source_pool):
        def shutdown(self, *args, **kwargs):
            closing.set()
            return super().shutdown(*args, **kwargs)

    class Drafting:
        def __init__(self, **kwargs):
            pass

        def stream_draft_section(self, **kwargs):
            state = lazyllm.locals['_lazyllm_agent']

            def work(sink):
                assert lazyllm.locals['_lazyllm_agent'] is state
                try:
                    sink({'delta': '## Section\n\nbody'})
                    both_started.wait(3)
                    assert release.wait(5)
                    sink({'delta': 'late'})
                    return 'body'
                finally:
                    producer_exits.append(True)

            return DraftPreviewStream(work, lambda payload: [payload['delta']],
                                      lambda result: ([], {}), idle_timeout=10)

    monkeypatch.setattr(writer, 'ThreadPoolExecutor', Pool)
    monkeypatch.setattr(writer, 'AutoModel', lambda **kwargs: object())
    monkeypatch.setattr(writer, 'SectionInstruction', SimpleNamespace(
        model_validate=lambda value: SimpleNamespace(section_title=value['section_title'])))
    monkeypatch.setattr(writer, 'WriterDraftingTools', Drafting)
    monkeypatch.setattr(writer, '_temp_root', lambda: tmp_path)
    monkeypatch.setattr(writer, '_write_input_artifact', lambda *args, **kwargs: '/input.json')

    def stop(_delta):
        both_started.wait(3)
        raise asyncio.CancelledError('stop sections')

    with source_pool(max_workers=1) as pool:
        future = pool.submit(writer.WriterWritingCapabilities().stream_draft_blocks_markdown,
                             writing_task_json='{}', writing_context_json='{}', on_delta=stop,
                             section_instructions_json=json.dumps({'instructions': [
                                 {'section_title': 'Section'}, {'section_title': 'Section 2'}]}))
        try:
            assert closing.wait(3)
            with pytest.raises(FutureTimeout):
                future.result(timeout=0.05)
        finally:
            release.set()
        with pytest.raises(asyncio.CancelledError, match='stop sections'):
            future.result(timeout=5)
        assert producer_exits == [True, True]
