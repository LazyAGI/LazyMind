"""Read paper JSON attachments without model transcription for F4 data analysis."""
from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path
import tempfile
from urllib.parse import urlsplit

import lazyllm

from .schema import ContractError, Object, parse

# Same source-file ceiling as the conversation text-resource reader.
MAX_SOURCE_BYTES = 100 * 1024 * 1024


class AnalysisEntryIntent(Object):
    standalone_analysis: bool


def describe_input_bindings(resources, bindings):
    """Explain paper references to the Agent without changing Runtime resources."""
    source = bindings.get('paper_input')
    if not isinstance(source, str) or not source.strip() or source.lstrip().startswith(('{', '[')):
        return resources
    if Path(urlsplit(source).path).suffix.lower() != '.json' or 'paper_input' not in resources:
        return resources
    return {**resources, 'paper_input': {
        **resources['paper_input'],
        'binding_kind': 'conversation_attachment_reference',
        'source_filename': Path(urlsplit(source).path).name,
        'metadata_scope': 'name, size and content_hash describe the reference text, not the original JSON',
        'validation_status': 'pending_step_execution',
        'instruction': ('The paper step reads and validates the complete original attachment. '
                        'This resource_id is a Workflow input ID, not a read_file_resource target. '
                        'When the requested step is ready, execute it; do not infer truncation from reference size.'),
    }}


def _json_files(paths):
    return list(dict.fromkeys(
        path for path in paths if isinstance(path, str) and Path(urlsplit(path).path).suffix.lower() == '.json'
    ))


def _select_attachment(config, request, reference=None):
    history = config.get('history_files_per_turn') or {}
    turns = sorted((key for key in history if str(key).isdigit()), key=int, reverse=True)
    groups = [history[key] for key in turns]
    groups.append(config.get('files') or [])
    groups = [_json_files(paths) for paths in groups]
    if reference is not None:
        # An explicit filename is a selector, never the contents of the input.
        # Search only this conversation and do not silently choose another upload.
        candidates = list(dict.fromkeys(
            path for paths in groups for path in paths
            if reference in (path, Path(urlsplit(path).path).name)
        ))
        if len(candidates) > 1:
            raise ContractError('AMBIGUOUS_INPUT', 'input.paper_input',
                                '指定的 JSON 附件存在多个同名版本，请明确选择一个文件')
        if not candidates:
            raise ContractError('MISSING_MATERIAL', 'input.paper_input',
                                'paper_input 只有文件引用，但当前会话中找不到对应的 JSON 附件；请检查文件名和上传状态')
        return candidates[0]
    named = [[path for path in paths if Path(urlsplit(path).path).name in request] for paths in groups]
    candidates = next((paths for paths in named if paths), []) or next((paths for paths in groups if paths), [])
    if len(candidates) > 1:
        names = '、'.join(Path(urlsplit(path).path).name for path in candidates)
        raise ContractError('AMBIGUOUS_INPUT', 'input.paper_input',
                            f'本轮有多个 JSON 附件（{names}），请明确指定文件名或绑定 paper_input')
    return candidates[0] if candidates else None


def _upload_requested(config, request):
    """The Host's request selects the source; a model-authored JSON copy does not."""
    if re.search(r'(?:不要|不用|忽略|不使用).{0,16}(?:上传|附件)|(?:ignore|do not|don.t).{0,24}(?:uploaded|attached)',
                 request, re.I):
        return False
    history = config.get('history_files_per_turn') or {}
    paths = _json_files([*(config.get('files') or []), *(p for group in history.values() for p in group or [])])
    return (any(Path(urlsplit(path).path).name in request for path in paths)
            or bool(re.search(r'上传|附件|\buploaded\b|\battached\b', request, re.I)))


def standalone_f4_request(request):
    from . import model
    from ..model_context import model_prompt

    prompt = '''Determine the scope of the complete user request. standalone_analysis is true when the requested
workflow ends with data analysis and figures, and false when analysis is part of a broader research or writing
workflow. Return JSON matching the schema.'''
    answer = model.call_model(model_prompt(
        prompt, facts={}, tables={}, shared=dict(schema=AnalysisEntryIntent.model_json_schema()),
        task=dict(request=request)))
    return parse(AnalysisEntryIntent, answer, 'input.analysis_entry')['standalone_analysis']


def workflow_input_bindings(bindings, request):
    """Import a catalog reference for standalone F4 JSON analysis requests."""
    if any(key.endswith('_pool') for key in bindings):
        return bindings
    config = lazyllm.globals.get('agentic_config') or {}
    if not _upload_requested(config, request) or not standalone_f4_request(request):
        return bindings
    source = _select_attachment(config, request)
    if source is None:
        raise ContractError('MISSING_MATERIAL', 'input.paper_input', '当前会话未找到所要求的 JSON 附件，请检查上传状态')
    # This exact path comes from the Host's catalog, not from the model.
    return {**bindings, 'paper_input': source}


def _unique_fields(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f'JSON 字段重复：{key}')
        value[key] = item
    return value


def _invalid_constant(value):
    raise ValueError(f'JSON 不允许 {value}')


def _bound_reference(path):
    """Keep real business objects; resolve filename-only scalar bindings."""
    try:
        with Path(path).open('rb') as stream:
            raw = stream.read(MAX_SOURCE_BYTES + 1)
    except OSError as exc:
        raise ContractError('MISSING_MATERIAL', 'input.paper_input', '无法读取已绑定的论文输入') from exc
    if len(raw) > MAX_SOURCE_BYTES:
        raise ContractError('INPUT_TOO_LARGE', 'input.paper_input', 'JSON 输入超过 100 MiB，请缩小输入')
    try:
        text = raw.decode('utf-8-sig').strip()
    except UnicodeError as exc:
        raise ContractError('INVALID_INPUT', 'input.paper_input', '论文输入必须采用 UTF-8 编码') from exc
    try:
        value = json.loads(text, object_pairs_hook=_unique_fields, parse_constant=_invalid_constant)
    except json.JSONDecodeError as exc:
        # Runtime imports text/json trigger strings verbatim, including filenames.
        if not text or Path(urlsplit(text).path).suffix.lower() != '.json':
            raise ContractError('INVALID_INPUT', 'input.paper_input',
                                '论文输入不是有效的 JSON 对象或 JSON 附件引用') from exc
        value = text
    except (ValueError, RecursionError) as exc:
        raise ContractError('INVALID_INPUT', 'input.paper_input', '论文输入含重复字段或无效 JSON 值') from exc
    # Preserve the same artifact-envelope support as the ordinary Runtime adapter.
    from ..workflow import _unwrap
    value = _unwrap(value)
    if isinstance(value, dict):
        return None
    if not isinstance(value, str) or not value.strip() or Path(urlsplit(value).path).suffix.lower() != '.json':
        raise ContractError('INVALID_INPUT', 'input.paper_input',
                            '论文输入必须是 JSON 对象，或当前会话中 JSON 附件的精确文件名')
    return value.strip()


def bind_attachment(bound: dict, workspace: str, request: str) -> dict:
    """Confirmed pools are pinned; an explicit upload request uses original attachment bytes.

    Candidates come only from the host's current conversation attachment catalog.
    Resolve through that catalog, read the complete bytes, validate the business input,
    then freeze a private input before the ordinary Runtime adapter consumes it.
    """
    if any(key.endswith('_pool') for key in bound):
        return bound
    config = lazyllm.globals.get('agentic_config') or {}
    if _upload_requested(config, request):
        source = _select_attachment(config, request)
        if source is None:
            raise ContractError('MISSING_MATERIAL', 'input.paper_input', '当前会话未找到所要求的 JSON 附件，请检查上传状态')
    else:
        reference = _bound_reference(bound['paper_input']) if 'paper_input' in bound else None
        if 'paper_input' in bound and reference is None:
            return bound
        source = (_select_attachment(config, request, reference=reference) if reference is not None
                  else _select_attachment(config, request))
    if source is None:
        return bound
    from lazymind.chat.engine.tools.file_resources.resolver import resolve_text_target
    from lazymind.chat.engine.tools.host_file_resolution import open_input_file

    filename = Path(urlsplit(source).path).name
    try:
        resolved = resolve_text_target(source, resources_only=True)
        if resolved.kind != 'attachment_text':
            raise ValueError('目标不是当前会话的 JSON 文本附件')
        with open_input_file(resolved.path) as stream:
            raw = stream.read(MAX_SOURCE_BYTES + 1)
    except (OSError, ValueError) as exc:
        raise ContractError('MISSING_MATERIAL', 'input.paper_input',
                            f'无法读取已上传的 JSON 附件 {filename}，请检查文件后重试') from exc
    if len(raw) > MAX_SOURCE_BYTES:
        raise ContractError('INPUT_TOO_LARGE', 'input.paper_input', 'JSON 附件超过 100 MiB，请缩小输入')
    try:
        value = json.loads(raw.decode('utf-8-sig'), object_pairs_hook=_unique_fields,
                           parse_constant=_invalid_constant)
    except (ValueError, RecursionError) as exc:
        raise ContractError('INVALID_INPUT', 'input.paper_input',
                            f'附件 {filename} 不是有效的 UTF-8 JSON：{exc}') from exc
    from . import trace
    trace.save('input-source.json', dict(kind='conversation_attachment', filename=filename,
                                         bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest()))
    # Validate only the shared business envelope; each algorithm validates its
    # own projected inputs after the ordinary material-pool initialization.
    from ..workflow import pool_from_business
    if isinstance(value, dict) and ('tables' in value or {'columns', 'rows'} <= value.keys()):
        value = {'topic': request.strip()[:120] or filename, 'data': value}
    pool_from_business(value)
    directory = Path(tempfile.mkdtemp(prefix='paper-input-', dir=workspace))
    target = directory / 'paper_input.json'
    target.write_text(json.dumps(value, ensure_ascii=False, allow_nan=False), encoding='utf-8')
    from ..review.schema import text_digest
    return {**bound, 'paper_input': str(target),
            '_imported_attachments': [text_digest(source.split('?', 1)[0])]}
