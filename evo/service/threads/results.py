from __future__ import annotations
import json
from pathlib import Path
from fastapi import APIRouter, HTTPException
from evo.service.core import store as _store
from evo.service.threads.workspace import ThreadWorkspace


def build_results_router(*, base_dir: Path, store: _store.FsStateStore) -> APIRouter:
    router = APIRouter(prefix='/v1/evo/threads/{thread_id}/results', tags=['thread-results'])

    @router.get('/datasets')
    def datasets(thread_id: str) -> list[dict]:
        ws = _ws(base_dir, thread_id)
        ids = _dataset_ids(base_dir, ws, store, thread_id)
        return [{'dataset_id': i,
                 'path': str(p := Path(base_dir) / 'datasets' / i / 'eval_data.json'),
                 'exists': p.is_file(),
                 'case_count': len((_json(p) or {}).get('cases') or []),
                 'kb_id': (_json(p) or {}).get('kb_id')} for i in ids]

    @router.get('/eval-reports')
    def eval_reports(thread_id: str) -> list[dict]:
        ws = _ws(base_dir, thread_id)
        out = []
        for row in _store.list_flow_tasks_by_thread(store, 'eval', thread_id):
            eval_id = ((row.get('payload') or {}).get('eval_id') or '').strip()
            if eval_id and (path := ws.eval_path(eval_id)).is_file():
                out.append(_eval_report(path, row))
        if not out:
            for eval_id in ws.load_artifacts().get('eval_ids') or []:
                if (path := ws.eval_path(eval_id)).is_file():
                    out.append(_eval_report(path))
        return out

    @router.get('/analysis-reports')
    def analysis_reports(thread_id: str) -> list[dict]:
        ws = _ws(base_dir, thread_id)
        out = []
        for row in _store.list_flow_tasks_by_thread(store, 'run', thread_id):
            if not (rid := (row.get('payload') or {}).get('report_id')):
                continue
            jp = _first(ws.dir / 'outputs' / 'reports' / f'{rid}.json', Path(base_dir)
                        / 'work' / 'reports' / f'{rid}.json', Path(base_dir) / 'reports' / f'{rid}.json')
            mp = _first(ws.dir / 'outputs' / 'reports' / f'{rid}.md', Path(base_dir)
                        / 'work' / 'reports' / f'{rid}.md', Path(base_dir) / 'reports' / f'{rid}.md')
            data = _json(jp)
            out.append({'run_id': row['id'], 'report_id': rid, 'json_path': str(jp),
                       'md_path': str(mp), 'json': data, 'markdown': _text(mp), '_empty': _empty_analysis(data)})
        if any(not item['_empty'] for item in out):
            out = [item for item in out if not item['_empty']]
        for item in out:
            item.pop('_empty', None)
        return out

    @router.get('/diffs')
    def diffs(thread_id: str) -> list[dict]:
        ws = _ws(base_dir, thread_id)
        rows = sorted(_store.list_flow_tasks_by_thread(store, 'apply', thread_id),
                      key=lambda r: r.get('created_at') or 0, reverse=True)
        out = []
        for idx, row in enumerate(rows):
            preview = _preview(base_dir, ws, row['id'])
            data = _json(preview) or {}
            result = ((row.get('payload') or {}).get('result') or {})
            out.append({'apply_id': row['id'],
                        'status': row.get('status'),
                        'created_at': row.get('created_at'),
                        'updated_at': row.get('updated_at'),
                        'terminal_at': row.get('terminal_at'),
                        'final_commit': row.get('final_commit') or result.get('final_commit'),
                        'is_latest': idx == 0,
                        'preview_path': str(preview) if preview.is_file() else None,
                        'preview': data or None,
                        'files': _files(data)})
        return out

    @router.get('/abtests')
    def abtests(thread_id: str) -> list[dict]:
        ws = _ws(base_dir, thread_id)
        return [
            {
                'abtest_id': i,
                'summary': _json(
                    d
                    / 'summary.json'),
                'decision': _json(
                    d
                    / 'decision.json'),
                'markdown': _text(
                    d
                    / 'summary.md')} for i in ws.load_artifacts().get('abtest_ids') or [] for d in [
                ws.dir
                / 'abtests'
                / i]]
    return router


def _ws(base_dir: Path, thread_id: str) -> ThreadWorkspace:
    ws = ThreadWorkspace(base_dir, thread_id, create=False)
    if not ws.thread_meta_path.exists():
        raise HTTPException(404, f'thread {thread_id} not found')
    return ws


def _json(path: Path) -> dict | None:
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError):
        return None


def _text(path: Path) -> str | None:
    try:
        return path.read_text(encoding='utf-8')
    except OSError:
        return None


def _eval_report(path: Path, row: dict | None = None) -> dict:
    data = _json(path) or {}
    summary = _case_details_summary(data.get('case_details') or [])
    return {
        'eval_id': path.stem,
        'task_id': (row or {}).get('id'),
        'path': str(path),
        'report_id': data.get('report_id'),
        'total_cases': data.get('total_cases') or summary['total_count'],
        'metrics': summary['averages'],
        'case_details_summary': summary,
    }


def _case_details_summary(cases: list[dict]) -> dict:
    buckets: dict[int, list[dict]] = {}
    for case in cases:
        buckets.setdefault(int(case.get('question_type') or 1), []).append(case)
    return {
        'total_count': len(cases),
        'averages': _averages(cases),
        'question_types': [
            {
                'question_type': key,
                'count': len(items),
                'averages': _averages(items),
            }
            for key, items in sorted(buckets.items())
        ],
    }


def _averages(cases: list[dict]) -> dict[str, float]:
    metrics = ('answer_correctness', 'faithfulness', 'context_recall', 'doc_recall')
    return {
        key: round(sum(float(case.get(key) or 0) for case in cases) / len(cases), 4) if cases else 0.0
        for key in metrics
    }


def _empty_analysis(data: dict | None) -> bool:
    if not data:
        return True
    meta = data.get('metadata') or {}
    return (
        int(meta.get('total_cases') or 0) == 0
        and not data.get('actions')
        and not data.get('hypotheses')
        and not data.get('findings')
    )


def _first(*paths: Path) -> Path:
    return next((p for p in paths if p.is_file()), paths[0])


def _dataset_ids(base_dir: Path, ws: ThreadWorkspace, store: _store.FsStateStore, thread_id: str) -> list[str]:
    ids: list[str] = []

    def add(dataset_id: str | None) -> None:
        path = Path(base_dir) / 'datasets' / str(dataset_id) / 'eval_data.json' if dataset_id else None
        if dataset_id and dataset_id not in ids and path and path.is_file():
            ids.append(dataset_id)

    for dataset_id in ws.load_artifacts().get('dataset_ids') or []:
        add(str(dataset_id))
    for row in _store.list_flow_tasks_by_thread(store, 'dataset_gen', thread_id):
        add((row.get('payload') or {}).get('eval_name'))
    for row in _store.list_flow_tasks_by_thread(store, 'eval', thread_id):
        add((row.get('payload') or {}).get('dataset_id'))
    return ids


def _preview(base_dir: Path, ws: ThreadWorkspace, apply_id: str) -> Path:
    rels = [Path('applies') / apply_id / 'preview' / apply_id / 'index.json',
            Path('applies') / apply_id / 'preview' / 'index.json']
    return _first(*(p for r in rels for p in (ws.dir / 'outputs' / r, Path(base_dir) / 'work' / r)))


def _files(preview: dict) -> list[dict]:
    out = []
    for item in preview.get('files') or []:
        if isinstance(item, dict):
            path = Path(str(item.get('diff_path') or ''))
            out.append({'path': item.get('path'),
                        'change_kind': item.get('change_kind'),
                        'additions': item.get('additions'),
                        'deletions': item.get('deletions'),
                        'diff_path': str(path) if str(path) else None,
                        'filename': path.name or None,
                        'content': _text(path) if path.is_file() else None})
    return out
