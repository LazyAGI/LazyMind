from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from .schemas import (WORKER_PROTOCOL_STATUSES, WORKER_REPORT_REQUIRED_FIELDS,
                      symbol_within_primary, worker_report_protocol_shape_errors)

_REPORT_LIST_FIELDS = ('confirmed_locations', 'rejected_locations', 'files_changed',
                       'touched_symbols', 'local_validation')
_REPORT_STR_FIELDS = ('hypothesis_checked', 'stop_reason', 'remaining_uncertainty')


def build_worker_report(attempt: int, instruction: dict[str, Any], trace: dict[str, Any], files: list[str],
                        status: str = 'valid', phase: str = 'patch') -> dict[str, Any]:
    mode = str(instruction.get('mode') or 'patch_once')
    if mode != 'no_patch':
        parsed = _parse_worker_report(trace)
        if parsed:
            return _normalized_worker_report(parsed, attempt, mode, _norm_files(files), phase, trace, instruction)
    fallback_status = status if mode == 'no_patch' else 'missing'
    if _trace_protocol_violation(mode, trace, _norm_files(files)):
        fallback_status = 'invalid'
    return {
        'id': f'opencode_{phase}_worker_report_attempt_{attempt}', 'attempt': attempt, 'mode': mode,
        'protocol_status': fallback_status,
        'hypothesis_checked': ((instruction.get('diagnosis_summary') or {}).get('primary_hypothesis') or ''),
        'confirmed_locations': [], 'rejected_locations': [],
        'edit_intent': {'target_symbol': '', 'intended_behavior_change': '', 'risk_acknowledgement': []},
        'files_changed': _norm_files(files), 'touched_symbols': [], 'local_validation': [],
        'stop_reason': _fallback_stop_reason(mode, fallback_status, trace), 'remaining_uncertainty': '',
    }


def _parse_worker_report(trace: dict[str, Any]) -> dict[str, Any] | None:
    for key in ('stdout', 'text_summary'):
        path = str((trace.get('raw_paths') or {}).get(key) or '')
        if not path:
            continue
        try:
            text = Path(path).read_text(encoding='utf-8')
        except OSError:
            continue
        report = _extract_worker_report_json(text)
        if report:
            return _coerce_report_fields(report)
    return None


def _flatten_entries(items: list[Any], *dict_keys: str) -> list[str]:
    """LLMs emit dict entries or comma-joined strings where a flat string list is expected."""
    out: list[str] = []
    for item in items:
        if isinstance(item, dict):
            value = next((str(item[key]) for key in dict_keys if item.get(key)), '')
            if value.strip():
                out.append(value.strip())
            continue
        out.extend(part.strip() for part in str(item).split(',') if part.strip())
    return out


def _coerce_report_fields(report: dict[str, Any]) -> dict[str, Any]:
    """Normalize LLM type drift (scalar vs list, dict vs string entries) before protocol validation."""
    coerced = dict(report)
    for key in _REPORT_LIST_FIELDS:
        value = coerced.get(key)
        if not isinstance(value, list):
            coerced[key] = [value] if isinstance(value, (str, dict)) and value else []
    coerced['files_changed'] = _flatten_entries(coerced['files_changed'], 'path')
    coerced['touched_symbols'] = _flatten_entries(coerced['touched_symbols'], 'symbol', 'name')
    for key in _REPORT_STR_FIELDS:
        value = coerced.get(key)
        if not isinstance(value, str):
            coerced[key] = '' if value is None else str(value)
    edit = coerced.get('edit_intent')
    if isinstance(edit, dict):
        risk = edit.get('risk_acknowledgement')
        if not isinstance(risk, list):
            edit = dict(edit)
            edit['risk_acknowledgement'] = [risk] if isinstance(risk, str) and risk else []
            coerced['edit_intent'] = edit
    return coerced


def _extract_worker_report_json(text: str) -> dict[str, Any] | None:
    decoder = json.JSONDecoder()
    for index, char in enumerate(text):
        if char != '{':
            continue
        try:
            obj, _ = decoder.raw_decode(text[index:])
        except json.JSONDecodeError:
            continue
        if isinstance(obj, dict) and any(key in obj for key in WORKER_REPORT_REQUIRED_FIELDS):
            return obj
    return None


def _worker_protocol_status(report: dict[str, Any], attempt: int, expected_mode: str, files: list[str],
                            trace: dict[str, Any], instruction: dict[str, Any]) -> str:
    status = str(report.get('protocol_status') or '')
    if status not in WORKER_PROTOCOL_STATUSES:
        return 'invalid'
    if status == 'not_run' and expected_mode != 'no_patch':
        return 'invalid'
    if 'attempt' in report:
        try:
            if int(report.get('attempt')) != int(attempt):
                return 'invalid'
        except (TypeError, ValueError):
            return 'invalid'
    if str(report.get('mode') or '') != expected_mode:
        return 'invalid'
    if _trace_protocol_violation(expected_mode, trace, files):
        return 'invalid'
    if status == 'valid' and worker_report_protocol_shape_errors(report, expected_mode):
        return 'invalid'
    if status == 'valid' and expected_mode == 'explore_only':
        if _norm_files(report.get('files_changed')) != files:
            return 'invalid'
    if status == 'valid' and expected_mode == 'patch_once':
        if _norm_files(report.get('files_changed')) != files:
            return 'invalid'
        if not _patch_report_matches_instruction(report, instruction, files):
            return 'invalid'
    return status


def _normalized_worker_report(report: dict[str, Any], attempt: int, expected_mode: str, files: list[str],
                              phase: str, trace: dict[str, Any], instruction: dict[str, Any]) -> dict[str, Any]:
    status = _worker_protocol_status(report, attempt, expected_mode, files, trace, instruction)
    edit = report.get('edit_intent') if isinstance(report.get('edit_intent'), dict) else {}
    return {
        'id': f'opencode_{phase}_worker_report_attempt_{attempt}', 'attempt': attempt, 'mode': expected_mode,
        'protocol_status': status,
        'raw_id': str(report.get('id') or ''), 'raw_attempt': report.get('attempt', ''),
        'raw_mode': str(report.get('mode') or ''), 'raw_protocol_status': str(report.get('protocol_status') or ''),
        'hypothesis_checked': str(report.get('hypothesis_checked') or ''),
        'confirmed_locations': _as_list(report.get('confirmed_locations')),
        'rejected_locations': _as_list(report.get('rejected_locations')),
        'edit_intent': {
            'target_symbol': str(edit.get('target_symbol') or ''),
            'intended_behavior_change': str(edit.get('intended_behavior_change') or ''),
            'risk_acknowledgement': _as_list(edit.get('risk_acknowledgement')),
        },
        'files_changed': (_norm_files(report['files_changed'])
                          if isinstance(report.get('files_changed'), list) else files),
        'touched_symbols': _as_list(report.get('touched_symbols')),
        'local_validation': _as_list(report.get('local_validation')),
        'stop_reason': _stop_reason(report, expected_mode, status),
        'remaining_uncertainty': _remaining_uncertainty(report, expected_mode, status, files, trace),
    }


def _as_list(value: Any) -> list[Any]:
    return value if isinstance(value, list) else []


def _trace_protocol_violation(mode: str, trace: dict[str, Any], files: list[str]) -> bool:
    if _prohibited_tools(trace):
        return True
    if mode in {'explore_only', 'no_patch'}:
        return bool(files or _norm_files(trace.get('files_modified')) or _ui_edit_events(trace))
    return False


def _patch_report_matches_instruction(report: dict[str, Any], instruction: dict[str, Any], files: list[str]) -> bool:
    primary_ref = str(instruction.get('primary_source_observation_ref') or '')
    primary = next((item for item in instruction.get('start_points') or []
                    if isinstance(item, dict) and item.get('source_observation_ref') == primary_ref), {})
    if not primary:
        return False
    primary_path, primary_symbol = str(primary.get('path') or ''), str(primary.get('symbol_hint') or '')
    edit = report.get('edit_intent') if isinstance(report.get('edit_intent'), dict) else {}
    candidates = {_symbol_token(str(edit.get('target_symbol') or ''))} | {
        _symbol_token(str(item)) for item in report.get('touched_symbols') or [] if isinstance(item, str)
    }
    if not any(symbol_within_primary(symbol, primary_symbol) for symbol in candidates if symbol):
        return False
    return primary_path in files


def _symbol_token(value: str) -> str:
    return value.split('(', 1)[0].strip().rsplit(':', 1)[-1].strip()


def _prohibited_tools(trace: dict[str, Any]) -> list[str]:
    blocked = {'question', 'task', 'todowrite', 'plan_enter', 'plan_exit'}
    tools = {str(item.get('kind') or '') for item in trace.get('ui_events') or [] if isinstance(item, dict)}
    tools |= set(_raw_event_tools(trace))
    return sorted(blocked & tools)


def _raw_event_tools(trace: dict[str, Any]) -> list[str]:
    path = str((trace.get('raw_paths') or {}).get('events_jsonl') or '')
    if not path:
        return []
    try:
        lines = Path(path).read_text(encoding='utf-8').splitlines()
    except OSError:
        return []
    out = []
    for line in lines:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        out.extend(_event_tools(event))
    return out


def _event_tools(value: Any) -> list[str]:
    if isinstance(value, dict):
        tools = []
        for key, child in value.items():
            if key in {'tool', 'type'} and isinstance(child, str):
                tools.append(child)
            else:
                tools.extend(_event_tools(child))
        return tools
    if isinstance(value, list):
        return [item for child in value for item in _event_tools(child)]
    return []


def _ui_edit_events(trace: dict[str, Any]) -> bool:
    return any(isinstance(item, dict) and item.get('kind') in {'edit_file', 'patch'}
               for item in trace.get('ui_events') or [])


def _norm_files(value: Any) -> list[str]:
    return sorted({
        str(item).split('/candidate/', 1)[-1].lstrip('/')
        for item in (value if isinstance(value, list) else []) if str(item).strip()
    })


def _fallback_stop_reason(mode: str, status: str, trace: dict[str, Any]) -> str:
    if status == 'invalid':
        return 'worker_protocol_violation'
    if mode == 'no_patch':
        return 'no_safe_patch'
    return 'error' if trace.get('last_error') else 'worker_report_missing'


def _stop_reason(report: dict[str, Any], mode: str, status: str) -> str:
    if status == 'invalid':
        return 'worker_protocol_violation'
    fallback = 'no_safe_patch' if mode == 'no_patch' else 'completed_one_minimal_diff'
    return str(report.get('stop_reason') or fallback)


def _remaining_uncertainty(report: dict[str, Any], mode: str, status: str, files: list[str],
                           trace: dict[str, Any]) -> str:
    if status != 'invalid':
        return str(report.get('remaining_uncertainty') or '')
    reasons = []
    if _prohibited_tools(trace):
        reasons.append(f"blocked tool used: {', '.join(_prohibited_tools(trace))}")
    if mode in {'explore_only', 'no_patch'}:
        if files or _norm_files(trace.get('files_modified')) or _ui_edit_events(trace):
            reasons.append(f'{mode} produced file edits')
    if mode == 'patch_once' and _norm_files(report.get('files_changed')) != files:
        reasons.append('worker files_changed does not match git diff')
    return '; '.join(reasons) or str(report.get('remaining_uncertainty') or 'worker report protocol invalid')
