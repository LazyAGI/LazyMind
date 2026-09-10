import asyncio
import json
import os
import subprocess
import sys
import time
import uuid

import pytest

from lazymind.conversation.model_client import ConversationCallError
from lazymind.conversation.conversation_grouping.schemas import GroupingRequest
from lazymind.conversation.conversation_grouping.grouping import organize_step
from lazymind.conversation.conversation_grouping import execution as supervisor


def request(**data):
    return GroupingRequest(input={
        'snapshot_id': 'run', 'snapshot_hash': 'hash',
        'cursor': 0, 'phase': 'batch', 'directory': [],
        'conversations': [{'id': 'c1', 'summary': '工作'}], **data,
    }, llm_config={'llm': {'source': 'openai', 'model': 'test', 'skip_auth': True}},
        options={'execution_issued_at': time.time()})


def test_incremental_decision_and_audit():
    def model(*args, **kwargs):
        return json.dumps({'candidate_operations': [{'op': 'create', 'id': 'new_1', 'name': '工作', 'scope': '工作任务'}],
                           'assignments': [{'id': 'c1', 'group_id': 'new_1'}]})
    result, _ = organize_step(request(), call=model)
    assert result['processed'] == 1
    assert result['assignments'] == [{'id': 'c1', 'group_id': 'new_1'}]
    audited, _ = organize_step(request(phase='audit', scope='工作任务', identity=result['identity']),
                               call=lambda *args, **kwargs: '{"keep":["c1"],"reject":[]}')
    assert audited['accepted']
    with pytest.raises(ConversationCallError):
        organize_step(request(phase='audit', scope='工作任务', identity='wrong'), call=model)


def test_cancel_before_start_and_expired_requests():
    async def check():
        execution = str(uuid.uuid4())
        assert (await supervisor.cancel_execution(execution))['settled']
        with pytest.raises(supervisor.ExecutionConflict):
            await supervisor.stream_execution(execution, request())
        expired = request()
        expired.options['execution_issued_at'] = time.time() - 400
        with pytest.raises(supervisor.ExecutionConflict):
            await supervisor.stream_execution(str(uuid.uuid4()), expired)
        supervisor._canceled[execution] = time.monotonic() - 1
        supervisor._prune()
        assert execution not in supervisor._canceled
    asyncio.run(check())


def test_real_worker_terminal_settlement():
    async def check():
        execution = str(uuid.uuid4())
        invalid = request(conversations=[{'id': ''}])
        response = await supervisor.stream_execution(execution, invalid)
        process = supervisor._executions[execution].process
        events = [json.loads(line) async for line in response]
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
            pending = asyncio.create_task(anext(response))
            await asyncio.sleep(0.02)
            pending.cancel()
            with pytest.raises(asyncio.CancelledError):
                await pending
        else:
            process.kill()
            events = [json.loads(line) async for line in response]
            assert events[-1]['result']['error_code'] == 'worker_exited'
        assert process.poll() is not None
        assert execution_id not in supervisor._executions
        assert (await supervisor.cancel_execution(execution_id))['settled']
    asyncio.run(check())


def test_parent_pipe_eof_terminates_worker(tmp_path):
    # Hold execution in a controlled model task, while running the real worker entrypoint.
    from lazymind.conversation.conversation_grouping import worker as organizer_worker
    package = tmp_path / 'fixture_worker'
    package.mkdir()
    (package / '__init__.py').write_text('')
    (package / 'worker.py').write_text(open(organizer_worker.__file__).read())
    (package / 'schemas.py').write_text('''import time
class GroupingRequest:
    @staticmethod
    def model_validate_json(raw): return raw
def run_grouping(request):
    time.sleep(120)
''')
    (package / 'grouping.py').write_text(
        'from contextvars import ContextVar\nfrom .schemas import run_grouping\n_STREAM_SINK = ContextVar("sink")\n')
    env = {**os.environ, 'PYTHONPATH': str(tmp_path)}
    proc = subprocess.Popen([sys.executable, '-m', 'fixture_worker.worker', str(os.getpid())],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
    try:
        proc.stdin.write(b'{}\n')
        proc.stdin.flush()
        # EOF is produced by both parent death and closing the parent's sole write descriptor.
        proc.stdin.close()
        assert proc.wait(timeout=10) != 0
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait()
        proc.stdout.close()
        proc.stderr.close()


def test_parent_crash_reaps_worker(tmp_path):
    import psutil
    from lazymind.conversation.conversation_grouping import worker as organizer_worker
    package = tmp_path / 'fixture_worker'
    package.mkdir()
    (package / '__init__.py').write_text('')
    (package / 'worker.py').write_text(open(organizer_worker.__file__).read())
    (package / 'schemas.py').write_text('''import time
class GroupingRequest:
    @staticmethod
    def model_validate_json(raw): return raw
def run_grouping(request): time.sleep(120)
''')
    (package / 'grouping.py').write_text(
        'from contextvars import ContextVar\nfrom .schemas import run_grouping\n_STREAM_SINK = ContextVar("sink")\n')
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
        parent.kill()
        parent.wait(timeout=5)
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
        if parent.poll() is None:
            parent.kill()
            parent.wait()
        parent.stdout.close()
        try:
            child = psutil.Process(pid)
            if child.status() != psutil.STATUS_ZOMBIE: child.kill()
        except psutil.NoSuchProcess:
            pass


def test_compact_directory_multishard_repair_and_final_creation():
    cards = [{'id': f'internal-uuid-{i}', 'short_id': f'g{i + 1}',
              'name': f'组{i}', 'scope': '' if i == 0 else '收录范围',
              'kind': 'candidate' if i % 2 else 'existing', 'count': 123,
              'version': 7, 'alias': '', 'examples': [{'id': 'secret-example', 'summary': 'secret-summary'}]}
             for i in range(101)]
    payloads = []

    def model(req, prompt, **kwargs):
        payload = json.loads(prompt.split('输入：\n', 1)[1])
        payloads.append(payload)
        assert 'internal-uuid' not in prompt and 'secret-example' not in prompt and 'secret-summary' not in prompt
        for key in ('existing_groups', 'candidate_groups'):
            assert all(set(card) == {'id', 'name', 'scope'} for card in payload[key])
        operations = []
        target = 'free'
        if len(payloads) == 1:
            target = 'g99999'  # Invalid output must repair, not enter the reduction prompt.
        elif payload['mode'] == 'directory_scan':
            assert len(payload['existing_groups']) + len(payload['candidate_groups']) <= 50
            if any(card['id'] == 'g1' for card in payload['existing_groups']):
                assert payload['existing_groups'][0]['scope'] == ''
                target = 'g1'
        else:
            if '本次是最终归并' in payload['instruction']:
                operations = [{'op': 'create', 'id': 'new_1', 'name': '新场景', 'scope': '新场景任务'}]
                target = 'new_1'
            else:
                target = 'g1'
        return json.dumps({'candidate_operations': operations, 'assignments': [{'id': 'c1', 'group_id': target}]})

    result, _ = organize_step(request(directory=cards), call=model)
    assert len(payloads) == 6  # One rejected output, three scans, two reductions.
    assert result['assignments'][0]['group_id'] == 'new_1'
    assert result['operations'][0]['id'] == 'new_1'


def test_compact_directory_candidate_operations_and_audit_evidence():
    cards = [{'id': 'internal-a', 'short_id': 'g1', 'kind': 'candidate', 'name': '邮件', 'scope': '阅读邮件'},
             {'id': 'internal-b', 'short_id': 'g2', 'kind': 'candidate', 'name': '发邮件', 'scope': '发送邮件'}]
    operations = [{'op': 'merge', 'source_ids': ['g1', 'g2'], 'target_id': 'g1', 'name': '邮件处理', 'scope': '收发邮件'}]
    result, _ = organize_step(request(directory=cards), call=lambda *a, **k: json.dumps({
        'candidate_operations': operations, 'assignments': [{'id': 'c1', 'group_id': 'g1'}]}))
    assert result['operations'] == operations

    def audit(req, prompt, **kwargs):
        payload = json.loads(prompt.split('输入：\n', 1)[1])
        assert payload['items'] == [{'id': 'c1', 'title': '', 'summary': '工作'}]
        assert 'existing_groups' not in payload and 'candidate_groups' not in payload
        return '{"keep":["c1"],"reject":[]}'

    assert organize_step(request(phase='audit', scope='工作'), call=audit)[0]['accepted']
