"""Runtime extension for PPT workflow attempts, including pinned legacy revisions."""
from __future__ import annotations

import json
from typing import Any

from lazymind.chat.engine.subagent.extensions import RuntimeExtension


_FONT_UNAVAILABLE_PREFIX = '请求的字体不可用：'


def _json_object(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, (str, bytes, bytearray)):
        try:
            decoded = json.loads(value)
        except ValueError:
            return {}
        if isinstance(decoded, dict):
            return decoded
    return {}


def _record_tool_step(step: dict[str, Any], state: dict[str, Any]) -> None:
    """Track failed render targets without treating their published placeholders as success."""
    pending = state.setdefault('pending', {})
    failures = state.setdefault('failures', {})
    for call in step.get('tool_calls') or []:
        if not isinstance(call, dict):
            continue
        name = call.get('name')
        if name == 'ppt_run_stage':
            args = _json_object(call.get('args'))
            stage = str(args.get('stage') or '').strip().lower()
            if stage == 'page-html':
                target = (name, stage, str(args.get('page') or ''))
            elif stage == 'batch-page-html':
                target = (name, stage, str(args.get('start_page') or 0), str(args.get('end_page') or 0))
            else:
                continue
        elif name in ('ppt_generate_pages', 'ppt_plan_page_prompts'):
            target = (name,)
        else:
            continue
        pending[str(call.get('id') or '')] = target

    for result in step.get('tool_results') or []:
        if not isinstance(result, dict):
            continue
        name = result.get('name')
        if name not in ('ppt_generate_pages', 'ppt_run_stage', 'ppt_plan_page_prompts', 'ppt_replace_deck_fonts'):
            continue
        payload = _json_object(result.get('result', result.get('content')))
        if name == 'ppt_replace_deck_fonts':
            reason = payload.get('error') or (payload.get('value') if payload.get('ok') is False else None)
            if reason:
                reason = str(reason)
                if reason.startswith(_FONT_UNAVAILABLE_PREFIX):
                    reason += '。请安装或导入相应字体，或选择当前可用的字体。'
                state['font_edit_failure'] = reason[:2000]
            elif payload.get('ok') is True:
                edit = _json_object(payload.get('value'))
                missing_font = state.get('font_edit_failure', '').startswith(_FONT_UNAVAILABLE_PREFIX)
                if edit.get('published_count') and (edit.get('target_font') or not missing_font):
                    state.pop('font_edit_failure', None)
            continue
        call_id = str(result.get('tool_call_id') or result.get('id') or '')
        target = pending.pop(call_id, None)
        if target is None and name in ('ppt_generate_pages', 'ppt_plan_page_prompts'):
            target = (name,)
        if target is None or target[0] != name:
            continue
        if name == 'ppt_plan_page_prompts':
            # Raised tool errors are serialized as {error: ...}; successful
            # planning returns counts without a status or ok flag.
            value = _json_object(payload.get('value')) if payload.get('ok') is True else payload
            reason = value.get('error') or (payload.get('value') if payload.get('ok') is False else None)
            if reason:
                failures[target] = f'{name}: {reason}'[:2000]
            elif isinstance(value.get('page_count'), int) and value['page_count'] > 0:
                failures.pop(target, None)
            continue
        if payload.get('ok') is False:
            reason = payload.get('value') or payload.get('error') or 'generation failed'
        else:
            if payload.get('ok') is True:
                payload = _json_object(payload.get('value'))
            if payload.get('status') == 'ok' and not payload.get('failed'):
                failures.pop(target, None)
                continue
            if payload.get('status') not in ('failed', 'partial', 'skipped') and not payload.get('failed'):
                continue
            reason = payload.get('error') or payload.get('failed_detail') or payload.get('reason') or payload
        failures[target] = f'{name}: {reason}'[:2000]


def _completion_failure(state: dict[str, Any]) -> tuple[str, str] | None:
    if font_failure := state.get('font_edit_failure'):
        return 'ppt_font_edit_failed', font_failure
    failures = state.get('failures')
    if failures:
        return 'ppt_generation_incomplete', '\n'.join(failures.values())
    return None


def runtime_extension(params: dict[str, Any]) -> RuntimeExtension:
    checks_generation = params.get('step_id') in ('generate_ppt', 'plan_page_prompts')
    return RuntimeExtension(
        tool_call_limits={'ppt_generate_background_images': 1},
        on_tool_step=_record_tool_step if checks_generation else None,
        completion_failure=_completion_failure if checks_generation else None,
    )
