from __future__ import annotations

from dataclasses import asdict, dataclass, field
import json
import re
from typing import Any

from ..artifacts import ArtifactDraft
from ..checkpoints import CheckpointManager
from ..operations import OperationRunRef
from ..runtime import OperationRuntime
from ..store import EvoStore
from .capabilities import CapabilityRegistry
from .factory import IntentOperationFactory
from .harness import IntentHarness
from .models import AtomicIntent, IntentHarnessResult, IntentParser, IntentRequest, ValidationIssue
from .schema import validate_params


@dataclass(frozen=True)
class RequestLedger:
    messages: list[dict] = field(default_factory=list)
    handled_spans: list[dict] = field(default_factory=list)
    task_results: list[dict] = field(default_factory=list)

    def append(self, message_id: str, message: str) -> 'RequestLedger':
        return RequestLedger([*self.messages, {'message_id': message_id, 'message': message}], self.handled_spans, self.task_results)


@dataclass
class IncrementalIntentSession:
    store: EvoStore
    run_id: str
    runtime: OperationRuntime
    registry: CapabilityRegistry
    factory: IntentOperationFactory
    checkpoint_manager: CheckpointManager
    ledger: RequestLedger = field(default_factory=RequestLedger)
    running_task: OperationRunRef | None = None

    def submit(self, message_id: str, message: str, raw_task: str | dict, allowed_capabilities: list[str]) -> IntentHarnessResult:
        if self.running_task is not None:
            self.runtime.request_interrupt(self.running_task)
            self.runtime.settle_running(self.running_task)
            self.running_task = None
        self.ledger = self.ledger.append(message_id, message)
        checkpoint = self.checkpoint_manager.create_checkpoint(self.run_id, None, message, allowed_capabilities=allowed_capabilities)
        graph = self.store.artifact_graph(self.run_id)
        message_ref = graph.commit_artifact(ArtifactDraft(f'user_message_{message_id}', 'UserMessage', {'message_id': message_id, 'message': message}, 'user', role='external_input'))
        parser = LayeredIntentParser(raw_task)
        result = IntentHarness(self.store, self.run_id, checkpoint, parser, self.registry, self.factory).handle(
            IntentRequest(message_id, '\n'.join(item['message'] for item in self.ledger.messages), checkpoint.checkpoint_id, message_ref)
        )
        self.running_task = result.proposals[0].operation_ref if len(result.proposals) == 1 else None
        return result

    def complete(self) -> list:
        if self.running_task is None:
            return []
        results = self.runtime.dispatch(self.running_task)
        self.ledger = RequestLedger(
            self.ledger.messages,
            self.ledger.handled_spans,
            [*self.ledger.task_results, *[asdict(result) for result in results]],
        )
        self.running_task = None
        return results


class LayeredIntentParser(IntentParser):
    def __init__(self, raw: str | dict, binder: 'GraphParamBinder | None' = None):
        self.raw = raw
        self.binder = binder or GraphParamBinder()
        self.issues: list[ValidationIssue] = []
        self.action: str = ''

    def parse(self, request: IntentRequest, capabilities: list[dict]) -> list[AtomicIntent]:
        self.issues = []
        self.action = ''
        item = parse_next_task(self.raw)
        if not item:
            self.issues.append(ValidationIssue('invalid_next_task', '', 'clarify', "layered parser expects {'next_task': {...}}"))
            return []
        task_type = str(item.get('type') or 'execute_task')
        if task_type == 'stop':
            self.action = 'no_operations'
            return []
        if task_type == 'ask_clarification':
            self.issues.append(ValidationIssue('ask_clarification', _intent_id(item, 'ask_clarification'), 'clarify', _clarification_text(item)))
            return []
        if task_type == 'respond_only':
            return [_respond_intent(item)]
        if task_type != 'execute_task':
            self.issues.append(ValidationIssue('invalid_next_task_type', _intent_id(item, task_type), 'clarify', f'invalid next_task type: {task_type}'))
            return []
        capability_id = _capability_id(item)
        if capability_id == 'respond_to_user':
            return [_respond_intent(item)]
        capability = _capability(capabilities, capability_id)
        if capability is None:
            self.issues.append(ValidationIssue('unknown_capability', _intent_id(item, capability_id or 'unknown_capability'), 'clarify', f"unknown capability: {capability_id or '<missing>'}"))
            return []
        intent_id = str(item.get('intent_id') or item.get('task_id') or capability_id)
        system_fields = _system_fields(capability)
        self.issues.extend(_system_field_issues(intent_id, item, system_fields))
        if self.issues:
            return []
        self.issues.extend(_semantic_issues(intent_id, item, capability.get('semantic_schema') or {}, system_fields))
        return [_intent_from_task(request, item, capability, self.binder)]


class GraphParamBinder:
    def bind(self, request: IntentRequest, item: dict, capability: dict, semantic_params: dict | None = None) -> dict:
        target = {}
        params = {}
        consumed: set[str] = set()
        spans_text = _source_span_text(item)
        message_text = request.message
        for name, spec in (capability.get('system_param_contract') or {}).items():
            spec_dict = spec if isinstance(spec, dict) else {}
            source = spec_dict.get('source', spec)
            text = spans_text or message_text
            ordinal = _ordinal(text)
            case_id = _case_id(text, spec_dict.get('case_id_format', 'zero4'))
            if name in params or name in target:
                continue
            value = _template_value(spec_dict.get('template', ''), case_id, ordinal)
            if value:
                _bind_system_value(target, params, name, value)
            elif source == 'message_case_id' and case_id:
                _bind_system_value(target, params, name, case_id)
            elif source == 'message_case_index' and ordinal:
                _bind_system_value(target, params, name, ordinal - 1)
            elif source == 'source_span_mention' and spec_dict.get('type') == 'dataset_row_ordinal' and ordinal:
                _bind_system_value(target, params, name, ordinal - 1)
            elif source == 'source_span_mention' and case_id:
                _bind_system_value(target, params, name, case_id)
            elif source == 'message_operation_id':
                value = _operation_id(text)
                if value:
                    target['operation_run_id'] = value
                    params[name] = value
            elif source == 'message_artifact_ref':
                value = _artifact_ref(text)
                if value:
                    target['artifact_ref'] = value
                    _bind_system_value(target, params, name, value)
            elif source == 'message_artifact_id':
                value = _artifact_id(text, spec_dict.get('ids') or [])
                value = value or (case_id if spec_dict.get('case_id_format') else '')
                if value:
                    target['artifact_id'] = value
                    _bind_system_value(target, params, name, value)
            elif source == 'current_operation_id':
                value = str(spec_dict.get('value') or '')
                if value:
                    target['operation_run_id'] = value
                    params[name] = value
            elif source == 'constant':
                _bind_system_value(target, params, name, _patched_constant(spec_dict.get('value'), spec_dict, semantic_params or {}, consumed))
            elif source == 'run_id':
                _bind_system_value(target, params, name, spec_dict.get('value') or '')
                target['run_id'] = target.get('run_id') or params.get(name) or ''
        if target.get('artifact_ref') and not params.get(_artifact_param_name(capability)):
            param_name = _artifact_param_name(capability)
            if param_name:
                params[param_name] = target['artifact_ref']
        return {'target': target, 'params': params, 'consumed_semantic': consumed}


def layered_intent_prompt(message: str, capabilities: list[dict], completed_tasks: list[dict] | None = None) -> str:
    routing = [
        {
            key: cap.get(key, {} if key == 'semantic_schema' else None)
            for key in ('capability_id', 'title', 'description', 'intent_use_when', 'intent_avoid_when', 'task_type', 'semantic_schema')
            if key == 'semantic_schema' or cap.get(key)
        }
        for cap in capabilities
    ]
    return f"""
你是 LazyMind evo 的 next-task parser。只输出一个 JSON 对象，不要 markdown。

目标：
- 根据已完成任务，只选择当前最应该执行的一个剩余 task。
- 输出 capability_id、source_spans、semantic_params。
- source_spans 必须是最小原文片段，并包含绑定当前 task 目标所需的信息，例如第几条、case_id、artifact_ref 或 operation_run_id。
- semantic_params 只能来自所选 capability 的 semantic_schema，只表达用户语义输入。
- system_param_contract 是系统/runtime 绑定的 operation 参数，不能放进 semantic_params。
- system 参数由系统绑定；如果原文有“第几条”、case_id、artifact_ref 或 operation_run_id 线索，必须保留到 source_spans 并继续执行，不要因 case_id/ref 缺失而澄清。
- semantic_schema 中的字段只是允许提取的语义槽位；除非 schema.required 明确列出，否则不要因为用户没说某个字段就 ask_clarification，直接省略该字段。
- 如果所选 capability 的 semantic_schema 是空对象，semantic_params 必须输出空对象 {{}}。
- 不要输出 system params，例如 artifact_ref、input_refs、operation_id、case_id、*_ref、*_url、workdir。
- 条件依赖当前状态但已完成任务没有结果时，先选择状态查询 task，不要猜测条件分支。
- 已完成任务包含 result_summary 时，必须据此决定是否继续执行、回复或停止。

允许 capabilities:
{json.dumps(routing, ensure_ascii=False, indent=2, sort_keys=True)}

输出 schema:
{{"next_task":{{"type":"execute_task|ask_clarification|respond_only|stop","capability_id":"...","source_spans":[{{"text":"原文片段"}}],"semantic_params":{{}}}}}}

已完成任务:
{json.dumps(completed_tasks or [], ensure_ascii=False, indent=2, sort_keys=True)}

用户消息:
{message}
"""


def _intent_from_task(request: IntentRequest, item: dict, capability: dict, binder: GraphParamBinder) -> AtomicIntent:
    capability_id = str(capability['capability_id'])
    semantic_params = _semantic_params(item, _system_fields(capability))
    system = binder.bind(request, item, capability, semantic_params)
    params = {**{key: value for key, value in semantic_params.items() if key not in system['consumed_semantic']}, **system['params']}
    if capability_id.startswith('read_'):
        kind = 'query'
    elif capability_id == 'respond_to_user':
        kind = 'chat'
    elif capability_id.endswith('_operation') or capability_id in {'retry_operation', 'cancel_operation'}:
        kind = 'flow_control'
    else:
        kind = 'artifact_change'
    return AtomicIntent(
        intent_id=_intent_id(item, capability_id),
        kind=kind,
        action=capability_id,
        target={**system['target'], 'capability_id': capability_id},
        params=params,
        confidence=float(item.get('confidence', 0.9)),
        risk=str(capability.get('risk_level') or 'low'),
        depends_on=[],
    )


def _semantic_params(item: dict, system_fields: set[str]) -> dict:
    raw = item.get('semantic_params') or {}
    return {key: _value(value) for key, value in raw.items() if key not in system_fields}


def _semantic_issues(intent_id: str, item: dict, schema: dict, system_fields: set[str]) -> list[ValidationIssue]:
    params = _semantic_params(item, system_fields)
    allowed_schema = _semantic_schema(schema)
    allowed = set(allowed_schema.get('properties') or {})
    issues = [
        ValidationIssue('unknown_semantic_param', intent_id, 'clarify', f'unknown semantic param: {name}')
        for name in sorted(set(params) - allowed)
    ]
    issues.extend(validate_params(intent_id, params, allowed_schema))
    if params and not (item.get('source_spans') or all(isinstance(value, dict) and (value.get('source_span') or value.get('source')) for value in (item.get('semantic_params') or {}).values())):
        issues.append(ValidationIssue('missing_semantic_grounding', intent_id, 'clarify', 'semantic params must have source span grounding'))
    return issues


def _semantic_schema(schema: dict) -> dict:
    if schema.get('type') == 'object':
        return {**schema, 'additionalProperties': False}
    return {'type': 'object', 'properties': dict(schema), 'additionalProperties': False}


def _system_field_issues(intent_id: str, item: dict, system_fields: set[str]) -> list[ValidationIssue]:
    target = dict(item.get('target') or {})
    params = dict(item.get('params') or {})
    semantic = dict(item.get('semantic_params') or {})
    names = (set(target) | set(params) | set(semantic)) & system_fields
    return [
        ValidationIssue('llm_system_param', intent_id, 'clarify', f'LLM must not output system param: {name}')
        for name in sorted(names)
    ]


def _system_fields(capability: dict) -> set[str]:
    fields = set(capability.get('system_param_contract') or {})
    fields.update({
        'artifact_ref', 'artifact_id', 'case_id', 'case_index', 'input_refs', 'operation_id',
        'operation_run_id', 'run_id',
    })
    return fields


def parse_next_task(raw: str | dict) -> dict:
    try:
        return _next_task(_loads(raw))
    except json.JSONDecodeError:
        return {'type': 'ask_clarification', 'source_spans': [], 'semantic_params': {}}


def _next_task(data: Any) -> dict:
    if not isinstance(data, dict) or not isinstance(data.get('next_task'), dict):
        return {}
    return data['next_task']


def _loads(raw: str | dict) -> Any:
    if not isinstance(raw, str):
        return raw
    text = raw.strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass
    if '</think>' in text:
        tail = text.rsplit('</think>', 1)[1].strip()
        text = tail or text
    if text.startswith('```'):
        text = text.strip('`').removeprefix('json').strip()
    decoder = json.JSONDecoder()
    for match in reversed(list(re.finditer(r'{', text))):
        try:
            data, _end = decoder.raw_decode(text[match.start():])
        except json.JSONDecodeError:
            continue
        if isinstance(data, dict) and 'next_task' in data:
            return data
    return json.loads(text)


def _capability_id(item: dict) -> str:
    return str(item.get('capability_id') or '')


def _capability(capabilities: list[dict], capability_id: str) -> dict | None:
    return next((item for item in capabilities if item.get('capability_id') == capability_id), None)


def _intent_id(item: dict, capability_id: str) -> str:
    return str(item.get('intent_id') or item.get('task_id') or f'{capability_id}_next')


def _value(value: Any) -> Any:
    return value.get('value') if isinstance(value, dict) and 'value' in value else value


def _respond_intent(item: dict) -> AtomicIntent:
    answer = _value(item.get('answer') or (item.get('semantic_params') or {}).get('answer') or item.get('message') or _source_span_text(item))
    return AtomicIntent(_intent_id(item, 'respond_to_user'), 'chat', 'respond_to_user', {'capability_id': 'respond_to_user'}, {'answer': str(answer)}, confidence=float(item.get('confidence', 0.9)))


def _clarification_text(item: dict) -> str:
    return str(_value(item.get('question') or (item.get('semantic_params') or {}).get('question') or item.get('message') or '需要补充信息后才能继续。'))


def _case_id(text: str, case_id_format: str = 'zero4') -> str:
    match = re.search(r'case[_ -]?(\d{1,4})', text, re.I)
    if match:
        return _format_case_id(int(match.group(1)), case_id_format)
    match = re.search(r'第\s*([0-9一二三四五六七八九十百]+)\s*(?:条|个)?', text)
    return _format_case_id(_cn_int(match.group(1)), case_id_format) if match else ''


def _format_case_id(number: int, case_id_format: str) -> str:
    if case_id_format == 'zero4':
        return f'case_{number:04d}'
    if case_id_format == 'no_pad':
        return f'case_{number}'
    raise ValueError(f'unknown case_id_format: {case_id_format}')


def _ordinal(text: str) -> int:
    match = re.search(r'case[_ -]?(\d{1,4})', text, re.I)
    if match:
        return int(match.group(1))
    match = re.search(r'第\s*([0-9一二三四五六七八九十百]+)\s*(?:条|个)?', text)
    return _cn_int(match.group(1)) if match else 0


def _artifact_ref(text: str) -> str:
    match = re.search(r'[A-Za-z0-9_]+@v[0-9]+', text)
    return match.group(0) if match else ''


def _artifact_id(text: str, candidates: list[str]) -> str:
    text_lower = text.lower()
    return next((item for item in candidates if item.lower() in text_lower), '')


def _operation_id(text: str) -> str:
    match = re.search(r'\b(?:dataset|eval|candidate_eval|analysis|intent|repair|abtest)\.[A-Za-z0-9_.#-]+', text)
    return match.group(0).rstrip('.,;:!?。；：！？') if match else ''


def _artifact_param_name(capability: dict) -> str:
    props = (capability.get('params_schema') or {}).get('properties') or {}
    return next((name for name in props if name.endswith('_ref')), '')


def _bind_system_value(target: dict, params: dict, name: str, value: Any) -> None:
    if name in {'artifact_ref', 'artifact_id', 'input_refs', 'operation_id', 'operation_run_id', 'run_id'}:
        target[name] = value
    else:
        params[name] = value


def _source_span_text(item: dict) -> str:
    spans = item.get('source_spans') if isinstance(item.get('source_spans'), list) else []
    return ' '.join(str(span.get('text') or '') for span in spans if isinstance(span, dict))


def _template_value(template: str, case_id: str, ordinal: int) -> str:
    if not template or ('{case_id}' in template and not case_id) or ('{ordinal}' in template and not ordinal) or ('{case_index}' in template and not ordinal):
        return ''
    return template.format(case_id=case_id, ordinal=ordinal, case_index=ordinal - 1 if ordinal else '')


def _patched_constant(value: Any, spec: dict, semantic_params: dict, consumed: set[str]) -> Any:
    patch = spec.get('patch_semantic') or {}
    if not patch:
        return value
    copy = json.loads(json.dumps(value, ensure_ascii=False))
    for semantic_name, path in patch.items():
        if semantic_name in semantic_params:
            _set_path(copy, str(path), semantic_params[semantic_name])
            consumed.add(semantic_name)
    return copy


def _set_path(value: Any, path: str, item: Any) -> None:
    current = value
    parts = path.split('.')
    for index, part in enumerate(parts[:-1]):
        next_value = [] if parts[index + 1].isdigit() else {}
        if isinstance(current, list):
            current = _list_slot(current, int(part), next_value)
        else:
            current = current.setdefault(part, next_value)
    last = parts[-1]
    if isinstance(current, list):
        _list_slot(current, int(last), None)
        current[int(last)] = item
    else:
        current[last] = item


def _list_slot(items: list, index: int, default: Any) -> Any:
    while len(items) <= index:
        items.append(default)
    if items[index] is None:
        items[index] = default
    return items[index]


def _cn_int(text: str) -> int:
    if text.isdigit():
        return int(text)
    digits = {'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
    if text == '十':
        return 10
    if '十' in text:
        left, right = text.split('十', 1)
        return (digits.get(left, 1) * 10) + digits.get(right, 0)
    return digits.get(text, 0)
