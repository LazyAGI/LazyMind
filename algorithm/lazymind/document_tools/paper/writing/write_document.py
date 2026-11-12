"""Draft a new manuscript using public Writer section planning and streaming."""
from ...toolkits import WriterCreateToolkit
from ..publication import validate_chart_references as _validate_chart_references
from ..publication import normalize_source_citations
from ..runtime_events import current_events, report_progress
from ..shared_facts import bind_shared_facts
import json

from .common import (
    _CHART_OWNERS,
    _bind_chart_references,
    _heading_signature,
    _json,
    _materialize_chart_insertions,
    _prepare_sections,
    _section_callbacks,
    _trace_call,
    _validate_manuscript,
    prepare_writer_context,
    validate_inputs as validate_writing_inputs,
    validate_result as _validate_result,
)

from .write_outline import prepare_outline
from .requirements import validate_required_sections


validate_result = _validate_result


def validate_inputs(inputs):
    validate_writing_inputs(inputs)
    if not inputs['outline']:
        from ..contracts import MissingMaterials
        raise MissingMaterials('请先提供或生成并确认论文大纲，再继续写作。')


def _write_draft(inputs, *, toolkit=None, on_delta=None):
    """Internal orchestration; callbacks are not part of the business input."""
    toolkit = toolkit if toolkit is not None else WriterCreateToolkit()
    report_progress(10, '正在准备写作材料')
    prepared = _trace_call('prepare', prepare_writer_context, inputs, toolkit=toolkit)
    task = prepared['writing_task']
    # Charts already supply captions and explicit finding associations. Do not ask Writer to
    # search for or generate media; the publisher resolves media placeholders to saved SVG resources.
    task['constraints']['visual_policy'] = {'allow_visuals': False}
    context_json = _json(prepared['writing_context'])
    outline = prepare_outline(prepared, toolkit)
    task_json = _json(task)
    signature = _heading_signature(outline)
    # 章节规划与正文生成沿用公共 Writer，补充论文约束。
    report_progress(25, '正在规划章节')
    planned = json.loads(_trace_call(
        'section_plan', toolkit.generate_section_instructions,
        writing_task_json=task_json, outline_json=_CHART_OWNERS.sub('', outline),
        writing_context_json=context_json,
    ))
    events = current_events()
    plan = _prepare_sections(planned['section_instructions'], outline, task['constraints'])
    report_progress(27, '正在整理全篇共享事实与章节论证任务')
    _trace_call('shared_facts', bind_shared_facts, plan, prepared)
    _bind_chart_references(plan, prepared, outline)
    context_json = _json(prepared['writing_context'])
    # Capture bound methods explicitly: parallel workers need not inherit ContextVars.
    callbacks = _section_callbacks(events)
    report_progress(30, '正在生成正文')
    sections = json.loads(_trace_call(
        'draft_sections', toolkit.stream_draft_blocks_markdown,
        writing_task_json=task_json, section_instructions_json=_json(plan), writing_context_json=context_json,
        on_delta=on_delta or (events.delta if events else lambda delta: None), **callbacks))
    if not isinstance(sections, list) or not sections or any(not isinstance(s, str) or not s.strip() for s in sections):
        raise ValueError('Writer returned no complete draft sections.')
    if events:
        events.finish_preview()
    # 组装完整稿件。
    report_progress(83, '正在组装全文')
    result = json.loads(_trace_call('assemble', toolkit.generate_draft_document_markdown,
                                    draft_sections_json=_json(sections), writing_context_json=context_json,
                                    outline_json=_CHART_OWNERS.sub('', outline), title=signature[0][1],
                                    ))
    body = result['draft_document']
    body = _materialize_chart_insertions(body, prepared['charts'])
    body = normalize_source_citations(body, prepared['reference_candidates'],
                                      material_source_ids=prepared.get('material_source_ids', []))
    from .recovery import repair_reader_provenance
    body = repair_reader_provenance([body], prepared.get('material_identifiers', []))[0]
    objects = _validate_chart_references(body, prepared['charts'])
    if {match[1] for match in objects} != {chart['id'] for chart in prepared['charts']}:
        raise ValueError('Draft must insert every planned chart exactly once.')
    if 'paper-charts:' in body:
        raise ValueError('Internal chart ownership leaked into manuscript.')
    # Publish a structurally valid draft for F6 to review; semantic criticism must not
    # prevent the manuscript from reaching the review/revision stages.
    validate_required_sections(body, task['constraints'])
    _validate_manuscript(body, signature, prepared['charts'], task['constraints'], toolkit, context_json,
                         candidates=prepared['reference_candidates'], check_quality=False)
    return {'manuscript': body, 'is_demo': False}


def run(inputs):
    from ..analysis import trace as diagnostics
    if diagnostics.enabled():
        return _write_draft(inputs)
    from lazymind.chat.engine.subagent.context import get_context
    context = get_context()
    if context is None:
        return _write_draft(inputs)
    with diagnostics.capture(context.workspace_path, context.task_id):
        return _write_draft(inputs)
