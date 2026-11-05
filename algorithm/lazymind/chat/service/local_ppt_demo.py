"""TEMPORARY LOCAL DEMO. Remove this module and the handle_chat hook after demo.

Opt-in fixture, owner-scoped clones only; no model/tool/network execution.
The fixture and files live outside Git. Original conversation is read-only.
"""
from __future__ import annotations

import asyncio
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
from datetime import datetime, timezone
from uuid import uuid4

from fastapi.responses import StreamingResponse

ROOT = Path(__file__).resolve().parents[4]
CONFIG = ROOT / 'workspace/ppt-demo/config.json'
ORDER = ['analyze_requirements', 'collect_materials', 'build_outline', 'plan_page_prompts', 'generate_ppt']
LABELS = ['需求与模板分析', '素材收集', '页面大纲', '逐页内容规划', '生成原生 PPTX']
MARKER = '[PPT Demo 回放]'


def now():
    return datetime.now(timezone.utc).isoformat()


def connect(path):
    db = sqlite3.connect(str(path), timeout=20)
    db.row_factory = sqlite3.Row
    return db


def rows(db, table, field, value):
    return [dict(r) for r in db.execute(f'SELECT * FROM {table} WHERE {field}=?', (value,))]


def clean(value):
    if isinstance(value, bytes):
        value = value.decode('utf-8')
    if isinstance(value, str):
        try:
            return json.dumps(clean(json.loads(value)), ensure_ascii=False)
        except (ValueError, TypeError):
            return value if 'data:application/' not in value else '[binary omitted]'
    if isinstance(value, dict):
        return {k: clean(v) for k, v in value.items() if 'base64' not in k.lower() and k.lower() != 'b64'}
    if isinstance(value, list):
        return [clean(v) for v in value]
    return value


def snapshot(db_path, session_id, final_path, directory):
    """Explicit setup helper, not called by normal Chat requests."""
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    with connect(db_path) as db:
        session = rows(db, 'plugin_sessions', 'id', session_id)[0]
        steps = [r for r in rows(db, 'plugin_session_steps', 'session_id', session_id)
                 if r['status'] == 'succeeded' and r['step_id'] in ORDER]
        if len(steps) != len(ORDER):
            raise ValueError('Expected one successful attempt for each demo step')
        tasks = [r for r in rows(db, 'sub_agent_tasks', 'conversation_id', session['conversation_id'])
                 if r['status'] == 'succeeded']
        data = {'session': session, 'steps': steps, 'tasks': tasks,
                'histories': sorted(rows(db, 'chat_histories', 'conversation_id', session['conversation_id']), key=lambda r: r['seq'])}
        for table in ('plugin_human_artifacts', 'plugin_slot_revisions', 'plugin_slot_order'):
            data[table] = rows(db, table, 'session_id', session_id)
        for table in ('sub_agent_artifacts', 'sub_agent_steps'):
            data[table] = [r for task in tasks for r in rows(db, table, 'task_id', task['id'])]
        first = next(r for r in data['plugin_slot_revisions'] if r['slot'] == 'native_pptx' and r['revision'] == 1)
        artifact = next(r for r in data['plugin_human_artifacts'] if r['id'] == first['human_artifact_id'])
        original = Path(json.loads(artifact['value'])['path'])
        data['pptx_filenames'] = {'original': original.name, 'final': Path(final_path).name}
        for name, source in [('original.pptx', original), ('final.pptx', Path(final_path))]:
            import zipfile
            with zipfile.ZipFile(source) as z:
                if z.testzip() or 'ppt/presentation.xml' not in z.namelist():
                    raise ValueError('Invalid PPTX fixture')
            shutil.copy2(source, directory / name)
    (directory / 'fixture.json').write_text(json.dumps(clean(data), ensure_ascii=False), encoding='utf-8')
    (directory / 'config.json').write_text(json.dumps({
        'enabled': True, 'db': str(db_path), 'owner': session['create_user_id'],
        'fixture': str(directory / 'fixture.json'), 'interval': 10,
    }), encoding='utf-8')


class Replay:
    def __init__(self, cfg, request):
        self.cfg, self.request = cfg, request
        self.data = json.loads(Path(cfg['fixture']).read_text())
        self.conv = request.conversation.conversation_id
        self.history = request.conversation.history_id
        self.sid = 'pptdemo-' + hashlib.sha256(self.conv.encode()).hexdigest()[:24]
        self.mapping = {self.data['session']['id']: self.sid,
                        self.data['session']['conversation_id']: self.conv}
        for table in ('steps', 'tasks', 'plugin_human_artifacts', 'plugin_slot_revisions',
                      'sub_agent_artifacts', 'sub_agent_steps'):
            for row in self.data[table]:
                self.mapping[row['id']] = str(uuid4())
        self.files = Path(cfg['db']).parents[3] / 'core/uploads/workflow-artifacts' / self.sid

    def remap(self, value):
        if isinstance(value, str):
            for old, new in self.mapping.items():
                value = value.replace(old, new)
            return value
        return value

    def insert(self, db, table, row, **overrides):
        row = {k: self.remap(v) for k, v in row.items()}
        row.update(overrides)
        # GORM JSON / RawMessage scan requires SQLite BLOB, not TEXT.
        types = {r['name']: r['type'].lower() for r in db.execute(f'PRAGMA table_info({table})')}
        for k, v in row.items():
            if 'json' in types.get(k, '') and isinstance(v, str):
                row[k] = v.encode()
        db.execute(f'INSERT INTO {table} ({",".join(row)}) VALUES ({",".join("?" for _ in row)})', list(row.values()))

    def existing(self, db):
        return db.execute('SELECT id FROM plugin_sessions WHERE id=? AND conversation_id=? AND create_user_id=?',
                          (self.sid, self.conv, self.cfg['owner'])).fetchone() is not None

    def start(self):
        with connect(self.cfg['db']) as db:
            owner = db.execute('SELECT create_user_id FROM conversations WHERE id=?', (self.conv,)).fetchone()
            if not owner or owner[0] != self.cfg['owner']:
                raise ValueError('Demo conversation owner mismatch')
            if db.execute('SELECT 1 FROM plugin_sessions WHERE conversation_id=?', (self.conv,)).fetchone():
                raise ValueError('Start the demo in a new chat; existing workflows are preserved')
            self.files.mkdir(parents=True, exist_ok=True)
            for name in ('original.pptx', 'final.pptx'):
                shutil.copy2(Path(self.cfg['fixture']).parent / name, self.files / name)
            self.insert(db, 'plugin_sessions', self.data['session'], id=self.sid,
                        trigger_history_id=self.history, status='active', current_step_id=ORDER[0],
                        state_version=1, created_at=now(), updated_at=now(), last_stopped_at=None,
                        origin_ref=self.conv)

    def file_value(self, final=False):
        key = 'final' if final else 'original'
        names = self.data.get('pptx_filenames', {})
        if not final and not names.get(key):
            first = next(r for r in self.data['plugin_slot_revisions'] if r['slot'] == 'native_pptx' and r['revision'] == 1)
            artifact = next(r for r in self.data['plugin_human_artifacts'] if r['id'] == first['human_artifact_id'])
            name = Path(json.loads(artifact['value'])['path']).name
        else:
            name = Path(names.get(key, 'presentation.pptx')).name
        # Separate revision directories preserve original names without collisions.
        p = self.files / ('v2' if final else 'v1') / name
        p.parent.mkdir(parents=True, exist_ok=True)
        if not p.exists():
            shutil.copy2(self.files / (key + '.pptx'), p)
        return json.dumps({'storage': 'managed_file', 'filename': name,
                           'name': name, 'path': str(p), 'size': p.stat().st_size,
                           'sha256': 'sha256:' + hashlib.sha256(p.read_bytes()).hexdigest()})

    def stage(self, step, running=False):
        with connect(self.cfg['db']) as db:
            source = next(r for r in self.data['steps'] if r['step_id'] == step)
            task = next(r for r in self.data['tasks'] if r['id'] == source['task_id'])
            if running:
                self.insert(db, 'sub_agent_tasks', task, trigger_history_id=self.history,
                            status='running', progress_pct=0, created_at=now(), updated_at=now(),
                            started_at=now(), finished_at=None, last_heartbeat=now())
                self.insert(db, 'plugin_session_steps', source, status='running', created_at=now(),
                            updated_at=now(), lease_owner='', lease_token='', lease_expires_at=None)
            else:
                for table in ('sub_agent_steps', 'sub_agent_artifacts'):
                    for r in self.data[table]:
                        if r['task_id'] != task['id']:
                            continue
                        # Saved execution records are display-only. clean() stripped binary payloads.
                        overrides = {'value': self.file_value()} if r.get('slot') == 'native_pptx' else {}
                        self.insert(db, table, r, **overrides)
                revisions = [r for r in self.data['plugin_slot_revisions'] if r['step_id'] == step
                             and (r['revision'] == 1 if r['slot'] == 'native_pptx' else r['selected'])]
                for rev in revisions:
                    human = next((r for r in self.data['plugin_human_artifacts'] if r['id'] == rev['human_artifact_id']), None)
                    if human:
                        overrides = {'value': self.file_value()} if rev['slot'] == 'native_pptx' else {}
                        self.insert(db, 'plugin_human_artifacts', human, created_at=now(), **overrides)
                    self.insert(db, 'plugin_slot_revisions', rev, selected=1, created_at=now())
                for order in self.data['plugin_slot_order']:
                    if order['slot_id'] in {r['slot_id'] for r in revisions}:
                        self.insert(db, 'plugin_slot_order', order, updated_at=now())
                db.execute("UPDATE sub_agent_tasks SET status='succeeded',progress_pct=100,started_at=?,finished_at=?,updated_at=? WHERE id=?",
                           (task['started_at'], task['finished_at'], task['updated_at'], self.mapping[task['id']]))
                db.execute("UPDATE plugin_session_steps SET status='succeeded',updated_at=? WHERE id=?", (now(), self.mapping[source['id']]))
            db.execute('UPDATE plugin_sessions SET current_step_id=?,state_version=state_version+1,updated_at=?,status=? WHERE id=?',
                       (step, now(), 'completed' if step == ORDER[-1] and not running else 'active', self.sid))

    def edit(self):
        with connect(self.cfg['db']) as db:
            rev = dict(db.execute("SELECT * FROM plugin_slot_revisions WHERE session_id=? AND slot='native_pptx' AND selected=1", (self.sid,)).fetchone())
            human_id = str(uuid4())
            self.insert(db, 'plugin_human_artifacts', {'id': human_id, 'session_id': self.sid, 'slot': 'native_pptx',
                        'content_type': 'file', 'value': self.file_value(True), 'caption': 'Demo 最终版本',
                        'created_at': now(), 'draft_version': 1})
            db.execute("UPDATE plugin_slot_revisions SET selected=0 WHERE session_id=? AND slot='native_pptx'", (self.sid,))
            self.insert(db, 'plugin_slot_revisions', rev, id=str(uuid4()), human_artifact_id=human_id,
                        artifact_seq=None, revision=rev['revision'] + 1, selected=1, created_at=now())
            editor = next(r for r in self.data['tasks'] if r['agent_type'] == 'pptx_edit')
            count = db.execute('SELECT count(*) FROM sub_agent_tasks WHERE conversation_id=?', (self.conv,)).fetchone()[0]
            editor_id = str(uuid4())
            self.insert(db, 'sub_agent_tasks', editor, id=editor_id, trigger_history_id=self.history,
                        seq_in_conversation=count + 1, last_heartbeat=now())
            for r in self.data['sub_agent_steps']:
                if r['task_id'] == editor['id']:
                    self.insert(db, 'sub_agent_steps', r, id=str(uuid4()), task_id=editor_id)
            db.execute('UPDATE plugin_sessions SET state_version=state_version+1,updated_at=? WHERE id=?', (now(), self.sid))

    def stop(self):
        with connect(self.cfg['db']) as db:
            if not self.existing(db):
                return
            db.execute("UPDATE plugin_session_steps SET status='interrupted' WHERE session_id=? AND status='running'", (self.sid,))
            db.execute("UPDATE sub_agent_tasks SET status='interrupted' WHERE conversation_id=? AND status='running'", (self.conv,))
            db.execute("UPDATE plugin_sessions SET status='paused' WHERE id=? AND status='active'", (self.sid,))


def try_demo(request):
    if not request.local_runtime or not CONFIG.is_file():
        return None
    cfg = json.loads(CONFIG.read_text())
    if not cfg.get('enabled') or request.conversation.user_id != cfg['owner']:
        return None
    if request.runtime.context_usage_preview or request.runtime.context_prompt_export:
        return None
    query = request.message.query
    if not request.conversation.conversation_id or not request.conversation.history_id:
        return None
    replay = Replay(cfg, request)
    with connect(cfg['db']) as db:
        existing = replay.existing(db)
    if not existing and not all(word in query for word in ('白云浮', 'CarlosShaoting', 'pptx')):
        return None
    from lazymind.chat.service.utils.streaming import log_and_emit_frame
    from lazymind.chat.runtime_events import runtime_event

    async def stream():
        def frame(text):
            return log_and_emit_frame({'text': text, 'think': None, 'sources': []}, 0, query, replay.conv)
        try:
            yield frame('<!-- local-ppt-demo -->')
            if existing:
                await asyncio.sleep(cfg['interval'])
                replay.edit()
                yield frame(replay.data['histories'][-1]['result'])
            else:
                replay.start()
                for step, label in zip(ORDER, LABELS):
                    replay.stage(step, running=True)
                    await asyncio.sleep(cfg['interval'])
                    replay.stage(step)
                    yield frame('')
                yield frame(replay.data['histories'][3]['result'])
            yield log_and_emit_frame({'text': None, 'think': None, 'sources': [],
                'runtime_event': runtime_event('run_finished', request.conversation.run_id or 'ppt-demo',
                    {'status': 'completed', 'reason': 'normal', 'model_invoked': False, 'partial_output': True})},
                0, query, replay.conv)
        except asyncio.CancelledError:
            replay.stop()
            raise
        except Exception as exc:
            replay.stop()
            yield frame(f'执行停止：{type(exc).__name__}: {exc}。')
            yield log_and_emit_frame({'text': None, 'sources': [], 'runtime_event': runtime_event(
                'run_finished', request.conversation.run_id or 'ppt-demo',
                {'status': 'failed', 'reason': 'runtime_failure', 'model_invoked': False})}, 0, query, replay.conv)
    return StreamingResponse(stream(), media_type='text/event-stream')
