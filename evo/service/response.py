from __future__ import annotations

import json
from dataclasses import asdict
from typing import Any

from ..artifacts import ArtifactDraft
from ..runtime import AdapterCall
from .flow import EvoFlowService, FlowMessageResult


class ModelResponseSynthesizer:
    MAX_PROMPT_CHARS = 120_000
    MAX_VALUE_CHARS = 8_000

    def synthesize(self, service: EvoFlowService, *, thread_id: str, message_id: str, user_message: str,
                   result: FlowMessageResult, flow_status: dict[str, Any] | None = None) -> str:
        payload = {
            'thread_id': thread_id,
            'message_id': message_id,
            'user_message': user_message,
            'flow_status': flow_status or {},
            'intent_result': _result_payload(service, result),
            'recent_events': _recent_events(service, limit=20),
        }
        evidence = json.dumps(_compact(payload, self.MAX_VALUE_CHARS), ensure_ascii=False, default=str)
        if len(evidence) > self.MAX_PROMPT_CHARS:
            evidence = evidence[:self.MAX_PROMPT_CHARS] + '\n...<truncated>'
        prompt = (
            '你是 LazyMind self-evolution 的执行解释器。基于输入 JSON 中的 intent、operation 输出、'
            '运行状态和事件，生成给用户的自然语言回复。不要编造未出现在证据里的事实；如果需要澄清，'
            '说明缺少什么以及为什么缺少。只能把 intent_result.raw.issues 里的条目当作当前消息的校验问题；'
            'recent_events 仅作为背景证据，不要把历史消息的问题说成本次问题。只输出回复正文。\n\n'
            f'{evidence}'
        )
        answer = AdapterCall('llm.message_response', lambda req: service.llm(req['prompt'], stream=False)).run(
            _SyntheticContext(service, message_id), {'prompt': prompt}, phase='message_response', item_ref=message_id
        ).response
        text = _strip_reasoning(str(answer or '').strip())
        service.artifacts.commit_artifact(ArtifactDraft(
            f'intent_answer_{message_id}', 'IntentAnswer',
            {'query_intent_id': message_id, 'target_refs': list(result.operation_refs), 'answer': text,
             'evidence': payload},
            f'message.response.{message_id}',
        ))
        return text


class _SyntheticContext:
    def __init__(self, service: EvoFlowService, message_id: str):
        self.run_id = service.run_id
        self.operation_run_id = f'message.response.{message_id}'
        self.call_recorder = service.runtime._call_recorder(self.operation_run_id)

    def check_interrupt(self) -> None:
        return None

    def interrupt_requested(self) -> bool:
        return False


def _result_payload(service: EvoFlowService, result: FlowMessageResult) -> dict[str, Any]:
    outputs = []
    for item in result.results:
        refs = []
        for ref in item.output_refs:
            try:
                refs.append({'ref': str(ref), 'payload': service.artifacts.get(ref)})
            except Exception as exc:
                refs.append({'ref': str(ref), 'error': str(exc)})
        outputs.append({'operation_run_id': item.operation_run_id, 'status': item.status, 'outputs': refs})
    return {**asdict(result), 'outputs': outputs}


def _recent_events(service: EvoFlowService, *, limit: int) -> list[dict[str, Any]]:
    events = service.store.read_events(service.run_id)[-limit:]
    return [asdict(event) for event in events]


def _compact(value: Any, max_chars: int) -> Any:
    if isinstance(value, str):
        return value if len(value) <= max_chars else value[:max_chars] + '...<truncated>'
    if isinstance(value, list):
        return [_compact(item, max_chars) for item in value[:50]]
    if isinstance(value, dict):
        return {str(key): _compact(item, max_chars) for key, item in list(value.items())[:80]}
    return value


def _strip_reasoning(text: str) -> str:
    if '</think>' in text: return text.rsplit('</think>', 1)[1].strip()
    if text.startswith('<think>'): return ''
    return text
