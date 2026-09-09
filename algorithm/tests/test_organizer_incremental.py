import asyncio
import json
import os
import subprocess
import sys
import time
import uuid

import pytest

from lazymind.chat.service.llm_task import LLMTaskRequest
from lazymind.chat.service.conversation_organizer import organize_step
from lazymind.chat.service import organizer_stream as supervisor


def request(**data):
    return LLMTaskRequest(task_type='conversation.organize_step', input={'data': {
        'protocol_version': 2, 'snapshot_id': 'run', 'snapshot_hash': 'hash',
        'cursor': 0, 'phase': 'batch', 'directory': [],
        'conversations': [{'id': 'c1', 'summary': '工作'}], **data,
    }}, options={'execution_issued_at': time.time()})


def test_incremental_decision_and_audit():
    def model(*args, **kwargs):
        return json.dumps({'candidate_operations': [{'op': 'create', 'id': 'cand_work', 'name': '工作', 'scope': '工作任务'}],
                           'assignments': [{'id': 'c1', 'group_id': 'cand_work'}]})
    result, _ = organize_step(request(), call=model)
    assert result['processed'] == 1
    assert result['assignments'] == [{'id': 'c1', 'group_id': 'cand_work'}]
    audited, _ = organize_step(request(phase='audit', scope='工作任务', identity=result['identity']),
                              call=lambda *args, **kwargs: '{"keep":["c1"],"reject":[]}')
    assert audited['accepted']
    with pytest.raises(Exception):
        organize_step(request(phase='audit', scope='工作任务', identity='wrong'), call=model)


def test_cancel_before_start_and_expired_requests():
    async def check():
        execution = str(uuid.uuid4())
        assert (await supervisor.cancel_execution(execution))['settled']
        with pytest.raises(Exception) as canceled:
            await supervisor.stream_execution(execution, request())
        assert canceled.value.status_code == 409
        expired = request(); expired.options['execution_issued_at'] = time.time() - 400
        with pytest.raises(Exception) as rejected:
            await supervisor.stream_execution(str(uuid.uuid4()), expired)
        assert rejected.value.status_code == 409
        supervisor._canceled[execution] = time.monotonic() - 1
        supervisor._prune()
        assert execution not in supervisor._canceled
    asyncio.run(check())


def test_real_worker_terminal_settlement():
    async def check():
        execution = str(uuid.uuid4())
        invalid = request(protocol_version=999)
        response = await supervisor.stream_execution(execution, invalid)
        process = supervisor._executions[execution].process
        events = [json.loads(line) async for line in response.body_iterator]
        assert process.returncode == 0
        assert events[-1]['type'] == 'result'
        assert events[-1]['result']['status'] == 'failed'
        assert execution not in supervisor._executions
        assert (await supervisor.cancel_execution(execution))['settled']
    asyncio.run(check())


def test_cancel_does_not_settle_a_surviving_process():
    class SurvivingProcess:
        def poll(self):
            return None

        def terminate(self):
            pass

        def kill(self):
            pass

        def wait(self, timeout):
            raise subprocess.TimeoutExpired('controlled survivor', timeout)

    async def check():
        execution_id = str(uuid.uuid4())
        execution = supervisor.Execution(SurvivingProcess(), None)
        supervisor._executions[execution_id] = execution
        try:
            assert not (await supervisor.cancel_execution(execution_id))['settled']
            assert supervisor._executions[execution_id] is execution
        finally:
            supervisor._executions.pop(execution_id)
    asyncio.run(check())


@pytest.mark.parametrize('disconnect', [False, True])
def test_abnormal_exit_and_disconnected_stream(monkeypatch, disconnect):
    popen = subprocess.Popen

    def launch(*args, **kwargs):
        return popen([sys.executable, '-c', 'import time; time.sleep(120)'], **kwargs)

    monkeypatch.setattr(supervisor.subprocess, 'Popen', launch)

    async def check():
        execution_id = str(uuid.uuid4())
        response = await supervisor.stream_execution(execution_id, request())
        process = supervisor._executions[execution_id].process
        if disconnect:
            pending = asyncio.create_task(anext(response.body_iterator))
            await asyncio.sleep(0.02)
            pending.cancel()
            with pytest.raises(asyncio.CancelledError):
                await pending
        else:
            process.kill()
            events = [json.loads(line) async for line in response.body_iterator]
            assert events[-1]['result']['error_code'] == 'worker_exited'
        assert process.poll() is not None
        assert execution_id not in supervisor._executions
        assert (await supervisor.cancel_execution(execution_id))['settled']
    asyncio.run(check())


def test_parent_pipe_eof_terminates_worker(tmp_path):
    # Hold execution in a controlled model task, while running the real worker entrypoint.
    from lazymind.chat.service import organizer_worker
    package = tmp_path / 'fixture_worker'
    package.mkdir(); (package / '__init__.py').write_text('')
    (package / 'worker.py').write_text(open(organizer_worker.__file__).read())
    (package / 'llm_task.py').write_text('''import time
class LLMTaskRequest:
    @staticmethod
    def model_validate_json(raw): return raw
def run_llm_task(request):
    time.sleep(120)
''')
    (package / 'conversation_organizer.py').write_text('from contextvars import ContextVar\n_STREAM_SINK = ContextVar("sink")\n')
    env = {**os.environ, 'PYTHONPATH': str(tmp_path)}
    proc = subprocess.Popen([sys.executable, '-m', 'fixture_worker.worker', str(os.getpid())],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
    try:
        proc.stdin.write(b'{}\n'); proc.stdin.flush()
        # EOF is produced by both parent death and closing the parent's sole write descriptor.
        proc.stdin.close()
        assert proc.wait(timeout=10) != 0
    finally:
        if proc.poll() is None: proc.kill(); proc.wait()
        proc.stdout.close(); proc.stderr.close()


def test_parent_crash_reaps_worker(tmp_path):
    import psutil
    from lazymind.chat.service import organizer_worker
    package = tmp_path / 'fixture_worker'
    package.mkdir(); (package / '__init__.py').write_text('')
    (package / 'worker.py').write_text(open(organizer_worker.__file__).read())
    (package / 'llm_task.py').write_text('''import time
class LLMTaskRequest:
    @staticmethod
    def model_validate_json(raw): return raw
def run_llm_task(request): time.sleep(120)
''')
    (package / 'conversation_organizer.py').write_text('from contextvars import ContextVar\n_STREAM_SINK = ContextVar("sink")\n')
    script = '''import subprocess,sys,os,time
worker=subprocess.Popen([sys.executable,'-m','fixture_worker.worker',str(os.getpid())],stdin=subprocess.PIPE,stdout=subprocess.DEVNULL)
worker.stdin.write(b'{}\\n');worker.stdin.flush()
print(worker.pid,flush=True)
time.sleep(120)
'''
    parent = subprocess.Popen([sys.executable, '-c', script], stdout=subprocess.PIPE,
                              env={**os.environ, 'PYTHONPATH': str(tmp_path)}, text=True)
    pid = int(parent.stdout.readline())
    try:
        parent.kill(); parent.wait(timeout=5)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            try:
                if psutil.Process(pid).status() == psutil.STATUS_ZOMBIE:
                    break
            except psutil.NoSuchProcess:
                break
            time.sleep(0.05)
        else:
            pytest.fail('worker survived parent crash')
    finally:
        if parent.poll() is None: parent.kill(); parent.wait()
        parent.stdout.close()
        try:
            child = psutil.Process(pid)
            if child.status() != psutil.STATUS_ZOMBIE: child.kill()
        except psutil.NoSuchProcess:
            pass
