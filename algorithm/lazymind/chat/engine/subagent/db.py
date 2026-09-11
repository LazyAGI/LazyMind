"""In-memory execution state and Core API queries for LazyMind SubAgents."""
from __future__ import annotations

import json
from typing import Any, Dict, List, Optional
from urllib.parse import quote


class MemorySubAgentStore:
    """Per-execution state for remote hosts that must not connect to Core DB."""

    def __init__(
        self,
        task: Dict[str, Any],
        steps: Optional[List[Dict[str, Any]]] = None,
        artifacts: Optional[List[Dict[str, Any]]] = None,
    ) -> None:
        self._task = dict(task)
        self._steps = [dict(step) for step in (steps or [])]
        self._artifacts = [dict(artifact) for artifact in (artifacts or [])]

    def load_task(self, task_id: str) -> Optional[Dict[str, Any]]:
        return dict(self._task) if str(self._task.get('id')) == task_id else None

    def append_step(self, task_id: str, seq: int, role: str, content: Dict[str, Any]) -> None:
        self._steps.append({'seq': seq, 'role': role, 'content': dict(content)})

    def load_steps(self, task_id: str) -> List[Dict[str, Any]]:
        return [dict(step) for step in sorted(self._steps, key=lambda value: value['seq'])]

    def max_step_seq(self, task_id: str) -> int:
        return max((int(step['seq']) for step in self._steps), default=-1)

    def next_artifact_seq(self, task_id: str, key: str) -> int:
        return max(
            (int(artifact.get('seq') or 0) for artifact in self._artifacts
             if artifact.get('slot') == key),
            default=0,
        ) + 1

    def load_artifacts(self, task_id: str, keys: Optional[List[str]] = None) -> List[Dict[str, Any]]:
        keyset = set(keys or [])
        return [
            dict(artifact)
            for artifact in self._artifacts
            if not keyset or artifact.get('slot') in keyset
        ]

    def dispose(self) -> None:
        pass


class TaskQueryDB:
    """Read ordinary task state through Core, the sole owner of core.db."""

    @staticmethod
    def _get(path: str, params: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        from lazymind.chat.engine.tools.infra.core_api_client import get_core_api
        return get_core_api(path, params=params)

    def get_task_status(self, task_id: str) -> Optional[Dict[str, Any]]:
        try:
            row = self._get(f'/internal/subagent/tasks/{quote(task_id, safe="")}', None)
            if not row:
                return None
            row['id'] = row.get('id') or row.get('task_id')
            row['progress_pct'] = row.get('progress_pct', row.get('progress', 0))
            return row
        except Exception:
            return None

    def list_tasks_by_conversation(self, conversation_id: str) -> List[Dict[str, Any]]:
        try:
            encoded = quote(conversation_id, safe='')
            data = self._get(f'/internal/subagent/conversations/{encoded}/tasks', None)
            rows = data.get('tasks') or []
            return [dict(row, id=row.get('id') or row.get('task_id')) for row in rows]
        except Exception:
            return []

    def load_artifacts_for_tasks(self, task_ids: List[str]) -> List[Dict[str, Any]]:
        """Read visible artifacts for ordinary LazyMind tasks."""
        if not task_ids:
            return []
        try:
            data = self._get('/internal/subagent/artifacts', {'task_id': task_ids[:100]})
            return [dict(row) for row in (data.get('artifacts') or [])]
        except Exception:
            return []

    def format_task_artifacts(self, task_ids: List[str]) -> List[str]:
        """Render ordinary task artifacts for the parent ChatAgent context."""
        return [json.dumps(row, ensure_ascii=False, default=str)
                for row in self.load_artifacts_for_tasks(task_ids)]

    def build_chat_agent_task_context(self, conversation_id: str) -> str:
        tasks = self.list_tasks_by_conversation(conversation_id)
        visible = [task for task in tasks if task.get('status') in {
            'running', 'pending', 'succeeded', 'failed', 'interrupted',
        }]
        if not visible:
            return ''
        status_labels = {'succeeded': 'done', 'failed': 'failed',
                         'interrupted': 'interrupted', 'pending': 'pending',
                         'running': 'running'}
        lines = [
            f'Task {index}. {task.get("title") or task.get("id")} '
            f'[{status_labels.get(str(task.get("status")), task.get("status"))}]'
            + (f': {task.get("summary")}' if task.get('summary') else '')
            for index, task in enumerate(visible, 1)
        ]
        task_ids = [str(task.get('id') or task.get('task_id') or '') for task in visible]
        lines.extend(self.format_task_artifacts([task_id for task_id in task_ids if task_id]))
        return '## LazyMind Tasks\n' + '\n'.join(lines)
