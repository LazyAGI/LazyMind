"""RC regression tests. API doubles here are not Runtime or model E2E."""
import base64
from copy import deepcopy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from demo import create
from gates import GateError, Guard, Trace, digest
from lifecycle import compatible, ensure, package_files
from task import Task


class Catalog:
    def __init__(self, enabled=True, present=True):
        self.items = [{'workflow_id':'test-rc', 'workflow_ref':'user:owner:test-rc', 'enabled':enabled}] if present else []
        self.files = {k:base64.b64encode(v.encode()).decode() for k,v in package_files('test-rc').items()}
        self.writes = []
    def call(self, method, path, body=None):
        if method == 'GET' and path == '/chat/settings/workflows': return {'workflows':self.items}
        if method == 'GET':return {'files':self.files,'revision_id':'revision-1','revision_no':1}
        self.writes.append((method,path,body));return {}
    def publish(self, files):
        self.writes.append(('publish',files));self.items=[{'workflow_id':'test-rc','workflow_ref':'user:owner:test-rc','enabled':False}]
        return {'workflow_ref':'user:owner:test-rc'}


class LifecycleTests(unittest.TestCase):
    def test_first_install_then_reuse_does_not_publish_twice(self):
        runtime=Catalog(present=False)
        self.assertTrue(ensure(runtime,'test-rc',True)['created'])
        self.assertFalse(ensure(runtime,'test-rc',True)['created'])
        self.assertEqual(sum(x[0]=='publish' for x in runtime.writes),1)
    def test_disabled_workflow_is_reused(self):
        runtime=Catalog(enabled=False)
        ensure(runtime,'test-rc',True)
        self.assertFalse(any(x[0]=='publish' for x in runtime.writes))
    def test_incompatible_files_fail_without_writes(self):
        runtime=Catalog()
        runtime.files['scenario/state.yml']=base64.b64encode(b'initial: bad').decode()
        with self.assertRaisesRegex(GateError,'WORKFLOW_INCOMPATIBLE'):ensure(runtime,'test-rc',True)
        self.assertEqual(runtime.writes,[])
    def test_publish_error_not_swallowed(self):
        runtime=Catalog(present=False)
        with patch.object(runtime,'publish',side_effect=GateError('RUNTIME_REJECTED')):
            with self.assertRaisesRegex(GateError,'RUNTIME_REJECTED'):ensure(runtime,'test-rc',True)
        self.assertEqual(runtime.writes,[])
    def test_ambiguous_catalog_rejected(self):
        runtime=Catalog();runtime.items*=2
        with self.assertRaisesRegex(GateError,'WORKFLOW_AMBIGUOUS'):ensure(runtime,'test-rc',True)
    def test_extra_execution_files_rejected(self):
        runtime=Catalog();runtime.files['scripts/bypass.py']=base64.b64encode(b'pass').decode()
        self.assertFalse(compatible(package_files('test-rc'),runtime.files))
    def test_normalized_yaml_ui_only_is_compatible(self):
        import yaml
        runtime=Catalog();doc=yaml.safe_load(base64.b64decode(runtime.files['workflow.yaml']))
        doc['ui']={'tabs':[]};runtime.files['workflow.yaml']=base64.b64encode(yaml.safe_dump(doc).encode()).decode()
        self.assertTrue(compatible(package_files('test-rc'),runtime.files))
        doc['runtime']={'bypass':'yes'};runtime.files['workflow.yaml']=base64.b64encode(yaml.safe_dump(doc).encode()).decode()
        self.assertFalse(compatible(package_files('test-rc'),runtime.files))


class DeclaredTaskTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.root=Path(self.tmp.name)/'demo';create(self.root)
        self.task=Task(self.root/'task.json',self.root/'worktree');self.repo=self.task.repo
        self.trace=Trace(self.root/'state/trace.jsonl')
    def tearDown(self):self.tmp.cleanup()
    def test_non_slug_requirements_are_derived_from_manifest(self):
        self.assertEqual(self.task.coverage,['quoted','headers','ragged','empty'])
        self.assertEqual(self.task.validate_binding()['task_id'],'csv-records')
    def test_approved_brief_cannot_change_before_validation(self):
        g=Guard(self.repo,self.root/'state',self.task.data['expected_base'],self.trace,
                task_id='csv-records',binding_check=self.task.validate_binding)
        g.brief.write_text('approved');g.expected_brief_hash=digest(b'approved');g.set_todo('in_progress')
        g.brief.write_text('different')
        with self.assertRaisesRegex(GateError,'BRIEF_CHANGED'):g.validate()
        self.assertEqual(g.launch_count,0)
    def test_protected_file_mutation_rejected(self):
        (self.repo/'test_csv_records.py').write_text('pass')
        with self.assertRaisesRegex(GateError,'PROTECTED_INPUT_CHANGED'):self.task.validate_binding()
    def test_manifest_mutation_rejected(self):
        self.task.path.write_text('{}')
        with self.assertRaisesRegex(GateError,'TASK_MANIFEST_CHANGED'):self.task.validate_binding()
    def test_primary_checkout_rejected(self):
        with self.assertRaisesRegex(GateError,'LINKED_WORKTREE_REQUIRED'):Task(self.root/'task.json',self.root/'repository')
    def test_plan_task_id_is_bound(self):
        # A real extractor substitute used only to test local validation, not E2E.
        with tempfile.TemporaryDirectory() as t:
            upstream=Path(t);script=upstream/'skills/subagent-driven-development/scripts/task-brief'
            script.parent.mkdir(parents=True);script.write_text('printf "### Task 1: wrong\\nquoted headers ragged empty\\n" > "$3"\n')
            with self.assertRaisesRegex(GateError,'PLAN_TASK_UNBOUND'):
                self.task.prepare_brief(upstream,self.root/'state/brief.md',self.trace)
    def test_symlink_in_allowed_path_rejected(self):
        (self.repo/'csv_records.py').unlink();(self.repo/'csv_records.py').symlink_to('test_csv_records.py')
        with self.assertRaisesRegex(GateError,'TASK_SYMLINK_UNSUPPORTED'):self.task.validate_binding()
    def test_coverage_cannot_be_unbound_from_checks(self):
        data=deepcopy(self.task.data);data['coverage'][0]['checks']=[];self.task.path.write_text(json.dumps(data))
        with self.assertRaisesRegex(GateError,'COVERAGE_CHECK_UNBOUND'):Task(self.task.path,self.repo)
    def test_actual_wrong_head_blocks_callback(self):
        g=Guard(self.repo,self.root/'state',self.task.data['expected_base'],self.trace,
                task_id='csv-records',binding_check=self.task.validate_binding)
        g.brief.write_text('task');g.set_todo('in_progress');g.validate()
        subprocess.run(['git','-C',str(self.repo),'commit','--allow-empty','-qm','wrong base'],check=True)
        with self.assertRaisesRegex(GateError,'BASE_MISMATCH'):g.dispatch(lambda _:self.fail('launched'))
    def test_non_text_coverage_still_fails_for_user_tasks(self):
        (self.repo/'csv_records.py').write_text('def read_records(text): return []\n');(self.repo/'.eval').mkdir()
        (self.repo/'.eval/coverage.json').write_text(json.dumps([{'id':i,'status':'DONE','evidence':None} for i in self.task.coverage]))
        with self.assertRaisesRegex(GateError,'COVERAGE_INCOMPLETE'):self.task.complete(self.trace)
    def test_claimed_coverage_does_not_hide_failing_behavior(self):
        (self.repo/'csv_records.py').write_text('def read_records(text): return []\n');(self.repo/'.eval').mkdir()
        (self.repo/'.eval/coverage.json').write_text(json.dumps([{'id':i,'status':'DONE','evidence':'claim'} for i in self.task.coverage]))
        with self.assertRaisesRegex(GateError,'ACCEPTANCE_FAILED'):self.task.complete(self.trace)
    def test_undeclared_file_modification_fails(self):
        (self.repo/'extra.py').write_text('pass')
        with self.assertRaisesRegex(GateError,'SCOPE_CHANGED'):self.task.validate_scope()

class RCProcessAndInputTests(unittest.TestCase):
    def test_remote_plaintext_auth_and_embedded_credentials_rejected(self):
        from cli import url_base
        for url in ['http://example.org', 'https://user:secret@example.org', 'https://example.org?token=x']:
            with self.subTest(url=url), self.assertRaises(GateError): url_base(url)
        self.assertEqual(url_base('http://127.0.0.1:5024'), 'http://127.0.0.1:5024/api/core')
    def test_private_token_permissions_enforced(self):
        from cli import credentials
        with tempfile.TemporaryDirectory() as t:
            p=Path(t)/'token.json';p.write_text('{"token":"test-placeholder"}');p.chmod(0o644)
            with self.assertRaisesRegex(GateError,'TOKEN_PERMISSIONS'):credentials({'token_file':str(p)})
            p.chmod(0o600);self.assertEqual(credentials({'token_file':str(p)}),'test-placeholder')
    def test_owned_child_is_terminated_on_timeout(self):
        from gates import codex_child
        import signal
        with tempfile.TemporaryDirectory() as t:
            root=Path(t);repo=root/'repo';state=root/'state';repo.mkdir();state.mkdir()
            with patch('gates.subprocess.Popen') as spawn, patch('gates.os.killpg') as kill:
                process=spawn.return_value;process.pid=12345
                process.communicate.side_effect=subprocess.TimeoutExpired('unit-double',600)
                with self.assertRaisesRegex(GateError,'CHILD_TIMEOUT'):
                    codex_child(repo,state,'unit test','model','codex')
                kill.assert_called_once_with(12345,signal.SIGKILL)
                process.wait.assert_called_once()
                self.assertTrue(spawn.call_args.kwargs['start_new_session'])
    def test_no_automatic_replay_after_dispatch(self):
        from project import execute
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as t:
            root=Path(t);repo=root/'repo';repo.mkdir();state=root/'state';state.mkdir()
            raw=b'{}';workflow={'workflow_ref':'r','revision_id':'v','revision_no':1,'created':False}
            binding={'task':digest(raw),'repo':str(repo),'config_model':'model','reasoning':'medium',
                     'workflow':{k:v for k,v in workflow.items() if k!='created'}}
            (state/'session.json').write_text(json.dumps({'session_id':'session','binding':binding}))
            from unittest.mock import Mock
            runtime=Mock();runtime.trace=Trace(state/'trace.jsonl')
            runtime.control.return_value={'projection':{'nodes':{k:{'execution':v} for k,v in
                [('prepare','succeeded'),('validate_dispatch','succeeded'),('dispatch','failed'),
                 ('collect','none'),('validate_completion','none'),('record','none')]}}}
            task=SimpleNamespace(repo=repo,raw=raw,data={'expected_base':'a'*40,'task_id':'task'},validate_binding=lambda: {})
            with self.assertRaisesRegex(GateError,'UNSAFE_REPLAY_REFUSED'):
                execute(runtime,task,{'model':'model','reasoning':'medium'},state,workflow,resume=True)
            runtime.call.assert_not_called()

class RCRecoveryTests(unittest.TestCase):
    def test_external_executor_does_not_issue_ui_control(self):
        from project import execute
        from types import SimpleNamespace
        from unittest.mock import Mock
        with tempfile.TemporaryDirectory() as t:
            root=Path(t);repo=root/'repo';repo.mkdir();state=root/'state';state.mkdir();raw=b'{}'
            workflow={'workflow_ref':'r','revision_id':'v','revision_no':1,'created':False}
            binding={'task':digest(raw),'repo':str(repo),'config_model':'model','reasoning':'medium','workflow':{k:v for k,v in workflow.items() if k!='created'}}
            (state/'session.json').write_text(json.dumps({'session_id':'session','binding':binding}))
            runtime=Mock();runtime.trace=Trace(state/'trace.jsonl');runtime.trace.emit('prepared')
            runtime.control.return_value={'control':{'active_execution_ids':[]},'session':{'slots':[{'slot_id':'preparation','selected':True,'artifact_value':{'brief_sha256':'abc'}}]},'projection':{'nodes':{k:{'execution':v} for k,v in [('prepare','succeeded'),('validate_dispatch','failed'),('dispatch','none'),('collect','none'),('validate_completion','none'),('record','none')]}}}
            task=SimpleNamespace(repo=repo,raw=raw,data={'expected_base':'a'*40,'task_id':'task'},validate_binding=lambda: {})
            with self.assertRaisesRegex(GateError,'UI_RETRY_REQUIRED'):
                execute(runtime,task,{'model':'model','reasoning':'medium'},state,workflow,resume=True)
            runtime.call.assert_not_called()
    def test_unicode_changed_file_matches_exact_scope(self):
        with tempfile.TemporaryDirectory() as t:
            root=Path(t)/'demo';create(root);repo=root/'worktree'
            data=json.loads((root/'task.json').read_text());data['allowed_files']=['标签.py'];(root/'task.json').write_text(json.dumps(data))
            task=Task(root/'task.json',repo);(repo/'标签.py').write_text('pass\n')
            self.assertEqual(task.validate_scope(),['标签.py'])


if __name__=='__main__':unittest.main()
