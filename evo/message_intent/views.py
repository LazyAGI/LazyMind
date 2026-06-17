from __future__ import annotations

from collections.abc import Callable
from typing import Any

from evo.artifact_runtime import ArtifactKey

MAX_EXCERPT_CHARS = 1200
MAX_FACT_ITEMS = 20
MAX_FACT_CHARS = 360


class ArtifactViewService:
    def __init__(self, artifact_reader: Callable[[str], dict | None]) -> None:
        self._artifact_reader = artifact_reader

    def view(self, artifact_id: str) -> dict[str, Any]:
        row = self._artifact_reader(artifact_id)
        if row is None:
            return {
                'source_ref': artifact_id,
                'schema': '',
                'facts': {'exists': False},
                'evidence': [],
                'excerpt': '',
                'max_chars': MAX_EXCERPT_CHARS,
                'untrusted': True,
            }
        data = row.get('data')
        schema = str(row.get('schema') or '')
        ref = str(row.get('ref') or artifact_id)
        if artifact_id.startswith('eval.summary'):
            facts = self._eval_summary_facts(data)
        elif artifact_id.startswith('analysis.summary'):
            facts = self._analysis_summary_facts(data)
        elif artifact_id.startswith('abtest.comparison'):
            facts = self._abtest_facts(data)
        elif '[' in artifact_id or row.get('partition'):
            facts = self._case_facts(data)
        else:
            facts = self._generic_facts(data)
        return {
            'source_ref': ref,
            'schema': schema,
            'facts': facts,
            'evidence': self._evidence(data),
            'excerpt': _clip(_safe_jsonish(data), MAX_EXCERPT_CHARS),
            'max_chars': MAX_EXCERPT_CHARS,
            'untrusted': True,
        }

    @staticmethod
    def _eval_summary_facts(data: Any) -> dict[str, Any]:
        if not isinstance(data, dict):
            return {'exists': True}
        failed = data.get('failed_cases') or data.get('failures') or []
        if not isinstance(failed, list):
            failed = []
        return {
            'exists': True,
            'total': data.get('total') or data.get('size') or 0,
            'failed_count': len(failed),
            'failed_cases': [str(item.get('case_id') or item.get('id') or item) for item in failed[:MAX_FACT_ITEMS]],
            'status': str(data.get('status') or ''),
        }

    @staticmethod
    def _analysis_summary_facts(data: Any) -> dict[str, Any]:
        if not isinstance(data, dict):
            return {'exists': True}
        case_ids = data.get('case_ids') or []
        if not isinstance(case_ids, list):
            case_ids = []
        return {
            'exists': True,
            'total': data.get('total') or len(case_ids),
            'case_ids': [str(item) for item in case_ids[:MAX_FACT_ITEMS]],
            'repairable_cases': _compact_fact(data.get('repairable_cases') or []),
            'status': str(data.get('status') or ''),
        }

    @staticmethod
    def _abtest_facts(data: Any) -> dict[str, Any]:
        if not isinstance(data, dict):
            return {'exists': True}
        return {
            'exists': True,
            'status': str(data.get('status') or ''),
            'decision': str(data.get('decision') or data.get('winner') or ''),
            'delta': _compact_fact(data.get('delta') or data.get('metrics_delta') or {}),
        }

    @staticmethod
    def _case_facts(data: Any) -> dict[str, Any]:
        if not isinstance(data, dict):
            return {'exists': True}
        return {
            'exists': True,
            'case_id': str(data.get('case_id') or data.get('id') or ''),
            'question': _clip(str(data.get('question') or ''), 240),
            'difficulty': str(data.get('difficulty') or ''),
            'failure_type': str(data.get('failure_type') or ''),
            'status': str(data.get('status') or ''),
        }

    @staticmethod
    def _generic_facts(data: Any) -> dict[str, Any]:
        if isinstance(data, dict):
            return {
                'exists': True, 'keys': sorted(
                    str(key) for key in data.keys())[
                    :30], 'status': str(
                    data.get('status') or '')}
        if isinstance(data, list):
            return {'exists': True, 'items': len(data)}
        return {'exists': True, 'type': type(data).__name__}

    @staticmethod
    def _evidence(data: Any) -> list[dict[str, str]]:
        if not isinstance(data, dict):
            return []
        out = []
        for key in ('question', 'answer', 'summary', 'reason', 'failure_type'):
            value = data.get(key)
            if isinstance(value, str) and value.strip():
                out.append({'field': key, 'excerpt': _clip(value, 360)})
        return out


def artifact_id_for_key(key: ArtifactKey) -> str:
    return key.artifact_id if not key.partition else f'{key.artifact_id}[{key.partition}]'


def _safe_jsonish(value: Any) -> str:
    import json

    return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)


def _compact_fact(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            str(key): _compact_fact(item) for key,
            item in list(
                sorted(
                    value.items(),
                    key=lambda item: str(
                        item[0])))[
                :MAX_FACT_ITEMS]}
    if isinstance(value, list):
        return [_compact_fact(item) for item in value[:MAX_FACT_ITEMS]]
    if isinstance(value, str):
        return _clip(value, MAX_FACT_CHARS)
    if isinstance(value, (int, float, bool)) or value is None:
        return value
    return _clip(str(value), MAX_FACT_CHARS)


def _clip(value: str, limit: int) -> str:
    text = str(value or '')
    return text if len(text) <= limit else text[: max(0, limit - 3)] + '...'
