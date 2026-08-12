from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any, Literal
from uuid import uuid4

import lazyllm
from json_repair import repair_json
from lazyllm import AutoModel, LOG
from pydantic import BaseModel, Field

from lazymind.model_config import inject_model_config


TaskMode = Literal['llm', 'agent']


class LLMTaskFile(BaseModel):
    path: str
    content: str = ''
    content_type: str = 'text/plain'


class LLMTaskInput(BaseModel):
    text: str = ''
    data: dict[str, Any] = Field(default_factory=dict)
    messages: list[dict[str, Any]] = Field(default_factory=list)
    files: list[LLMTaskFile] = Field(default_factory=list)


class LLMTaskSkillRef(BaseModel):
    name: str = ''
    source: str = 'builtin'
    version: str = 'latest'
    required: bool = True
    content: str = ''


class LLMTaskToolRef(BaseModel):
    name: str = ''
    source: str = 'builtin'
    mode: str = 'expanded'
    required: bool = False
    description: str = ''
    config: dict[str, Any] = Field(default_factory=dict)


class LLMTaskRequest(BaseModel):
    mode: TaskMode = 'llm'
    task_type: str = 'general'
    instruction: str = ''
    input: LLMTaskInput = Field(default_factory=LLMTaskInput)
    skills: list[str | LLMTaskSkillRef] = Field(default_factory=list)
    tools: list[str | LLMTaskToolRef] = Field(default_factory=list)
    response_format: dict[str, Any] = Field(default_factory=dict)
    llm_config: dict[str, Any] = Field(default_factory=dict)
    tool_config: dict[str, Any] = Field(default_factory=dict)
    options: dict[str, Any] = Field(default_factory=dict)


class LLMTaskResult(BaseModel):
    status: Literal['succeeded', 'failed']
    task_id: str
    output: dict[str, Any] = Field(default_factory=dict)
    text: str = ''
    files: list[LLMTaskFile] = Field(default_factory=list)
    tool_call_turns: int = 0
    usage: dict[str, Any] = Field(default_factory=dict)
    error: str | None = None


class LLMTaskError(RuntimeError):
    pass


_WORKFLOW_TASKS = {
    'workflow.analyze_skill',
    'workflow.design_brief',
    'workflow.generate_skeleton',
    'workflow.generate_state_machine',
    'workflow.generate_scenario_scripts',
    'workflow.repair',
    'workflow.polish_info',
}


def run_llm_task(request: LLMTaskRequest) -> LLMTaskResult:
    task_id = str(uuid4())
    lazyllm.globals._init_sid(sid=f'llm_task_{task_id}')
    lazyllm.locals._init_sid(sid=f'llm_task_{task_id}')
    inject_model_config(request.llm_config)
    try:
        output, text, files = _run_task(request)
        return LLMTaskResult(
            status='succeeded',
            task_id=task_id,
            output=output,
            text=text,
            files=files,
            usage={'input_chars': len(_prompt_for_log(request)), 'output_chars': len(text)},
        )
    except Exception as exc:
        LOG.exception(f'[LLMTask] failed task_type={request.task_type}: {exc}')
        return LLMTaskResult(status='failed', task_id=task_id, error=str(exc))


def _run_task(request: LLMTaskRequest) -> tuple[dict[str, Any], str, list[LLMTaskFile]]:
    if request.task_type in _WORKFLOW_TASKS:
        return _run_workflow_task(request)
    raw = _call_json_or_text(request, _generic_prompt(request))
    if isinstance(raw, dict):
        return raw, json.dumps(raw, ensure_ascii=False), []
    return {}, str(raw), []


def _run_workflow_task(request: LLMTaskRequest) -> tuple[dict[str, Any], str, list[LLMTaskFile]]:
    prompt = _workflow_prompt(request)
    output = _call_json(request, prompt)
    if request.task_type == 'workflow.repair':
        output = _apply_workflow_edits(request, output)
    text = json.dumps(output, ensure_ascii=False)
    files = _output_files(output)
    return output, text, files


def _call_json_or_text(request: LLMTaskRequest, prompt: str) -> Any:
    wants_json = (request.response_format or {}).get('type') == 'json_object'
    if wants_json:
        return _call_json(request, prompt)
    model = AutoModel(model='llm')
    return model(
        prompt,
        stream_output=True,
        temperature=request.options.get('temperature', 0),
        timeout=int(request.options.get('timeout_seconds', 600)),
    )


def _call_json(request: LLMTaskRequest, prompt: str) -> dict[str, Any]:
    model = AutoModel(model='llm')
    last_raw: Any = None
    last_error: Exception | None = None
    max_retries = max(1, int(request.options.get('max_retries', 2)))
    attempt_prompt = prompt
    for attempt in range(max_retries):
        try:
            raw = model(
                attempt_prompt,
                response_format={'type': 'json_object'},
                stream_output=True,
                temperature=request.options.get('temperature', 0),
                timeout=int(request.options.get('timeout_seconds', 600)),
            )
            last_raw = raw
            parsed = _json_object(raw)
            if parsed:
                return parsed
            raise ValueError('empty JSON object')
        except Exception as exc:
            last_error = exc
            attempt_prompt = (
                f'{prompt}\n\nThe previous response was invalid JSON for the required schema. '
                f'Return only one JSON object. Error: {type(exc).__name__}: {exc}'
            )
            LOG.warning(f'[LLMTask] JSON attempt {attempt + 1} failed: {exc}')
    snippet = re.sub(r'\s+', ' ', str(last_raw or '')).strip()[:500]
    raise LLMTaskError(f'model returned invalid JSON: {last_error}; response={snippet}')


def _json_object(raw: Any) -> dict[str, Any]:
    if isinstance(raw, BaseModel):
        return raw.model_dump()
    if isinstance(raw, dict):
        return raw
    parsed = _parse_json(str(raw))
    if not isinstance(parsed, dict):
        raise ValueError(f'expected JSON object, got {type(parsed).__name__}')
    return parsed


def _parse_json(text: str) -> Any:
    text = re.sub(r'<think>.*?</think>', '', text, flags=re.S).strip()
    fenced = re.search(r'```(?:json)?\s*(\{.*\})\s*```', text, re.S)
    if fenced:
        text = fenced.group(1)
    else:
        start = text.find('{')
        end = text.rfind('}')
        if start >= 0 and end > start:
            text = text[start:end + 1]
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return repair_json(text, return_objects=True)


def _generic_prompt(request: LLMTaskRequest) -> str:
    return '\n\n'.join([
        'You are a platform AI task runner. Follow the caller instruction exactly.',
        _skill_context(request),
        _tool_context(request),
        f'Task type: {request.task_type}',
        f'Instruction:\n{request.instruction}',
        _input_context(request),
        'Return the requested result. If JSON is requested, return only one JSON object.',
    ]).strip()


def _workflow_prompt(request: LLMTaskRequest) -> str:
    task = request.task_type
    base = [
        'You generate and repair LazyMind Workflow artifacts.',
        'Return only one JSON object. Do not wrap it in markdown.',
        'Workflow artifacts must be valid YAML strings for the LazyMind graph compiler.',
        'Use workflow.yaml for slots, steps, and UI. Use scenario/state.yml for graph transitions and step prompts.',
        'Every non-external slot must be produced exactly once. Each workflow step must have a matching state step.',
        'state.yml must include transitions.__start__ and eventually reach __end__.',
        'Allowed slot types: text, image, file, json. Use English snake_case ids.',
        'The workflow.yaml step field "command" is descriptive legacy data and is NOT executable. Never emit it.',
        'A packaged Python helper is executable only when workflow.yaml declares it in tool_scripts and the matching '
        'state step declares the callable name in tools. Never instruct an agent to run scripts/path.py unless that '
        'script and callable are included in the generated package.',
        'Do not claim to produce DOCX, PNG, PDF, or another concrete file format unless an actually available tool '
        'creates that format. If execution support is unavailable, redesign the output and labels as an honest '
        'LLM/built-in-capability fallback (for example Markdown instead of DOCX).',
        'For a multi-step pipeline, expose each step output in a corresponding UI tab; set tab.step_id to the step id.',
        _skill_context(request),
        _tool_context(request),
        _input_context(request),
    ]
    schemas = {
        'workflow.analyze_skill': (
            'Return schema: {"verdict":"generatable|needs_confirmation|rejected",'
            '"verdict_code":"string","message":"string","candidates":[{"id":"unique non-empty string"}],'
            '"coverage":{},"tool_mappings":{},"scripts":{}}. Analyze whether the skill can be converted to a Workflow.'
        ),
        'workflow.design_brief': (
            'Return schema: {"design_brief":"markdown"}. '
            'The brief must specify slots, steps, transitions, UI, and scripts.'
        ),
        'workflow.generate_skeleton': (
            'Return schema: {"workflow_yaml":"string"}. Generate complete workflow.yaml only.'
        ),
        'workflow.generate_state_machine': (
            'Return schema: {"state_yaml":"string","workflow_yaml":"string","warnings":["string"]}. '
            'workflow_yaml may be empty if no change is needed.'
        ),
        'workflow.generate_scenario_scripts': (
            'Return schema: {"scenario_md":"string","scripts":{"path":"content"},"warnings":["string"]}. '
            'scenario_md must be complete human-readable documentation. For every step in scenario/state.yml, '
            'include a markdown section with the step id, its purpose, required inputs, produced outputs, and '
            'what the runtime agent should do. Do not use placeholders such as 暂无描述, TODO, TBD, '
            'no description, or empty sections. scripts may be empty. Only create safe Python helper scripts '
            'when necessary.'
        ),
        'workflow.repair': (
            'Return schema: {"workflow_yaml":"string","state_yaml":"string","scenario_md":"string",'
            '"scripts":{"path":"content"},"remaining_warnings":["string"],'
            '"edits":[{"file":"workflow.yaml|scenario/state.yml|scenario/scenario.md|scripts/name.py",'
            '"old":"exact old text","new":"replacement text"}]}. '
            'Prefer edits for small local repairs. Also include the final full content for every repaired core file.'
        ),
        'workflow.polish_info': (
            'Return schema: any subset of {"description":"string","when_to_use":"string",'
            '"overview":"string","notes":"string"}.'
        ),
    }
    base.append(schemas.get(task, 'Return a JSON object.'))
    if request.instruction:
        base.append(f'Caller instruction:\n{request.instruction}')
    return '\n\n'.join(part for part in base if part).strip()


def _input_context(request: LLMTaskRequest) -> str:
    payload = {
        'text': request.input.text,
        'data': request.input.data,
        'messages': request.input.messages,
        'files': [file.model_dump() for file in request.input.files],
    }
    return 'Input:\n' + json.dumps(payload, ensure_ascii=False, indent=2)


def _skill_context(request: LLMTaskRequest) -> str:
    chunks = []
    for item in request.skills:
        ref = _normalize_skill(item)
        content = ref.content or _load_builtin_skill(ref.name)
        if content:
            chunks.append(f'## Skill: {ref.name}\n{content}')
        elif ref.required:
            raise LLMTaskError(f'required skill not found: {ref.name}')
    return 'Loaded skills:\n' + '\n\n'.join(chunks) if chunks else ''


def _tool_context(request: LLMTaskRequest) -> str:
    refs = [_normalize_tool(item) for item in request.tools]
    if not refs:
        return ''
    docs = []
    for ref in refs:
        if ref.name == 'str_replace':
            docs.append(
                '- str_replace: propose an edit with file, old, and new. '
                'The old text must match exactly and uniquely.'
            )
        else:
            desc = ref.description or 'No callable runtime is exposed; describe intended output in JSON.'
            docs.append(f'- {ref.name}: {desc}')
    return 'Available task tools:\n' + '\n'.join(docs)


def _normalize_skill(item: str | LLMTaskSkillRef) -> LLMTaskSkillRef:
    if isinstance(item, str):
        return LLMTaskSkillRef(name=item)
    return item


def _normalize_tool(item: str | LLMTaskToolRef) -> LLMTaskToolRef:
    if isinstance(item, str):
        return LLMTaskToolRef(name=item)
    return item


def _load_builtin_skill(name: str) -> str:
    if not name:
        return ''
    root = Path(__file__).resolve().parents[2] / 'skills'
    path = root / name / 'SKILL.md'
    if path.is_file():
        return path.read_text(encoding='utf-8')
    return ''


def _apply_workflow_edits(request: LLMTaskRequest, output: dict[str, Any]) -> dict[str, Any]:
    files = {file.path: file.content for file in request.input.files}
    changed: dict[str, str] = {}
    for raw in output.get('edits') or []:
        if not isinstance(raw, dict):
            continue
        path = str(raw.get('file') or raw.get('path') or '')
        old = str(raw.get('old') or '')
        new = str(raw.get('new') or '')
        if not path or old == '':
            continue
        current = changed.get(path, files.get(path, ''))
        if current.count(old) != 1:
            continue
        changed[path] = current.replace(old, new, 1)
    field_to_path = {
        'workflow_yaml': 'workflow.yaml',
        'state_yaml': 'scenario/state.yml',
        'scenario_md': 'scenario/scenario.md',
    }
    for field, path in field_to_path.items():
        if path in changed and not str(output.get(field) or '').strip():
            output[field] = changed[path]
    scripts = output.get('scripts')
    if not isinstance(scripts, dict):
        scripts = {}
    for path, content in changed.items():
        if path.startswith('scripts/'):
            scripts[path] = content
    output['scripts'] = {str(k): str(v) for k, v in scripts.items()}
    return output


def _output_files(output: dict[str, Any]) -> list[LLMTaskFile]:
    files = []
    mapping = {
        'workflow_yaml': 'workflow.yaml',
        'state_yaml': 'scenario/state.yml',
        'scenario_md': 'scenario/scenario.md',
    }
    for field, path in mapping.items():
        value = output.get(field)
        if isinstance(value, str) and value.strip():
            files.append(LLMTaskFile(path=path, content=value, content_type='text/yaml'))
    scripts = output.get('scripts')
    if isinstance(scripts, dict):
        for path, content in scripts.items():
            if isinstance(content, str):
                files.append(LLMTaskFile(path=str(path), content=content, content_type='text/x-python'))
    return files


def _prompt_for_log(request: LLMTaskRequest) -> str:
    try:
        return _generic_prompt(request)
    except Exception:
        return request.instruction
