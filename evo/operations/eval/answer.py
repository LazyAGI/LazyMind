from __future__ import annotations

import os
import re
import math
from collections.abc import Mapping
from typing import Any
from uuid import uuid4

from evo.operations.route.chat_router import RouterChatRequest, call_router_chat

HEX = re.compile(r'^[0-9a-fA-F]+$')
DEFAULT_CASE_DEADLINE_SECONDS = 300.0
DEFAULT_FIRST_FRAME_TIMEOUT_SECONDS = 60.0


def answer_case(case: Mapping[str, Any], target_config: Mapping[str, Any]) -> dict[str, Any]:
    kb_id = case_kb_id(case, target_config)
    target = {
        'router_chat_url': str(target_config.get('router_chat_url') or ''),
        'router_admin_url': str(target_config.get('router_admin_url') or ''),
        'algorithm_id': str(target_config.get('algorithm_id') or ''),
        'kb_id': kb_id,
    }
    if not kb_id:
        return failed_rag_answer(case, {}, target, 'dataset_contract_error',
                                 'case routing metadata missing kb_id')
    if not _has_role(target_config.get('llm_config'), 'llm'):
        return failed_rag_answer(case, {}, target, 'chat_config_error',
                                 'eval.target_config.llm_config.llm missing; '
                                 'eval must be launched through core model-config injection')
    if not target['router_admin_url']:
        return failed_rag_answer(case, {}, target, 'chat_config_error',
                                 'eval.target_config.router_admin_url missing')
    if not target['algorithm_id']:
        return failed_rag_answer(case, {}, target, 'chat_config_error',
                                 'eval.target_config.algorithm_id missing')
    return call_chat_answer(case, target_config, kb_id)


def case_kb_id(case: Mapping[str, Any], target_config: Mapping[str, Any]) -> str:
    case_id = str(case.get('id') or '')
    by_case = target_config.get('case_metadata_by_id')
    metadata = case.get('case_metadata') if isinstance(case.get('case_metadata'), Mapping) else {}
    preparation = case.get('source_preparation') if isinstance(case.get('source_preparation'), Mapping) else {}
    case_source = preparation.get('case_source') if isinstance(preparation.get('case_source'), Mapping) else {}
    if isinstance(by_case, Mapping) and isinstance(by_case.get(case_id), Mapping):
        text = str(by_case[case_id].get('kb_id') or '').strip()
        if text:
            return text
    return str(metadata.get('kb_id') or case_source.get('kb_id') or target_config.get('kb_id') or '').strip()


def call_chat_answer(case: Mapping[str, Any], target_config: Mapping[str, Any], kb_id: str) -> dict[str, Any]:
    try:
        kb_ids = tuple(dict.fromkeys(item.strip() for item in str(kb_id or '').split(';') if item.strip()))
        session_id = str(target_config.get('session_id') or uuid4().hex).strip().lower()
        session_id = session_id if HEX.fullmatch(session_id) else uuid4().hex
        target = _target(target_config, kb_ids, session_id)
        if not kb_ids:
            return failed_rag_answer(case, {}, target, 'dataset_contract_error',
                                     'case routing metadata missing kb_id')

        result = call_router_chat(RouterChatRequest(
            router_chat_url=target['router_chat_url'],
            router_admin_url=target['router_admin_url'],
            algorithm_id=target['algorithm_id'],
            query=str(case.get('question') or ''),
            kb_ids=kb_ids,
            trace_id=session_id,
            conversation_id=target['conversation_id'],
            user_id=target['user_id'],
            llm_config=target_config.get('llm_config') if isinstance(target_config.get('llm_config'), Mapping) else None,
            connect_timeout_seconds=_number(target_config.get('connect_timeout_seconds'), 5.0),
            write_timeout_seconds=_number(target_config.get('write_timeout_seconds'), 60.0),
            pool_timeout_seconds=_number(target_config.get('pool_timeout_seconds'), 5.0),
            case_deadline_seconds=_number(
                target_config.get('case_deadline_seconds') or os.getenv('LAZYMIND_EVO_CHAT_CASE_DEADLINE_SECONDS'),
                DEFAULT_CASE_DEADLINE_SECONDS,
            ),
            first_frame_timeout_seconds=_number(
                target_config.get('first_frame_timeout_seconds')
                or os.getenv('LAZYMIND_EVO_CHAT_FIRST_FRAME_TIMEOUT_SECONDS'),
                DEFAULT_FIRST_FRAME_TIMEOUT_SECONDS,
            ),
        ))
    except (TypeError, ValueError) as exc:
        target = _raw_target(target_config, kb_id)
        return failed_rag_answer(case, {}, target, 'chat_config_error', str(exc))
    return _with_case(case, result)


def failed_rag_answer(
    case: Mapping[str, Any],
    stream: Mapping[str, Any],
    target: Mapping[str, Any],
    error_type: str,
    message: str,
) -> dict[str, Any]:
    return _answer_base(case, stream, target) | {
        'status': 'failed',
        'chat_error': {'type': error_type, 'message': message},
        'evidence_status': 'failed',
    }


def _with_case(case: Mapping[str, Any], result: Mapping[str, Any]) -> dict[str, Any]:
    stream = {'answer': result.get('answer') or '', 'frames': result.get('frames') or []}
    answer = _answer_base(case, stream, result.get('target') if isinstance(result.get('target'), Mapping) else {})
    answer.update(dict(result))
    answer['case_id'] = str(case.get('id') or answer.get('case_id') or '')
    answer['case'] = dict(case)
    answer['case_metadata'] = {'kb_id': answer.get('target', {}).get('kb_id', '')}
    answer['question'] = str(case.get('question') or '')
    answer['evidence_status'] = _evidence_status(answer)
    return answer


def _target(target_config: Mapping[str, Any], kb_ids: tuple[str, ...], session_id: str) -> dict[str, str]:
    target = {
        'router_chat_url': str(target_config.get('router_chat_url') or '').strip(),
        'router_admin_url': str(target_config.get('router_admin_url') or '').strip(),
        'algorithm_id': str(target_config.get('algorithm_id') or '').strip(),
        'kb_id': ';'.join(kb_ids),
        'trace_id': session_id,
        'conversation_id': str(target_config.get('conversation_id') or session_id).strip(),
        'user_id': str(target_config.get('user_id') or '0').strip() or '0',
    }
    target.update({
        key: str(target_config[key])
        for key in ('target_id', 'target_kind', 'target_label')
        if target_config.get(key)
    })
    if not target['router_admin_url']:
        raise ValueError('router_admin_url is required')
    if not target['algorithm_id']:
        raise ValueError('algorithm_id is required')
    return target


def _raw_target(target_config: Mapping[str, Any], kb_id: str) -> dict[str, str]:
    return {
        'router_chat_url': str(target_config.get('router_chat_url') or ''),
        'router_admin_url': str(target_config.get('router_admin_url') or ''),
        'algorithm_id': str(target_config.get('algorithm_id') or ''),
        'kb_id': str(kb_id or ''),
    }


def _answer_base(case: Mapping[str, Any], stream: Mapping[str, Any], target: Mapping[str, Any]) -> dict[str, Any]:
    return {
        'case_id': str(case.get('id') or ''),
        'case': dict(case),
        'case_metadata': {'kb_id': target.get('kb_id', '')},
        'question': str(case.get('question') or ''),
        'answer': str(stream.get('answer') or ''),
        'tool_errors': [],
        'contexts': [],
        'doc_ids': [],
        'chunk_ids': [],
        'trace_id': str(target.get('trace_id') or ''),
        'target': dict(target),
    }


def _evidence_status(answer: Mapping[str, Any]) -> str:
    if answer.get('status') != 'ok':
        return 'failed'
    return 'found' if answer.get('contexts') or answer.get('doc_ids') or answer.get('chunk_ids') else 'empty'


def _number(value: Any, default: float) -> float:
    result = float(default if value in (None, '') else value)
    if not math.isfinite(result) or result <= 0:
        raise ValueError('timeout values must be positive finite numbers')
    return result


def _has_role(value: object, role_name: str) -> bool:
    return isinstance(value, Mapping) and isinstance(value.get(role_name), Mapping) and bool(value[role_name])
