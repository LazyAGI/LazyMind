"""Generate an editable outline with public Writer streaming."""
from ..prompts.write_document_prompt import CHART_OUTLINE_INSTRUCTIONS
from ...toolkits import WriterCreateToolkit
import copy
import json
import re

from lazyllm.tools.writer.utils.serialization import (
    MARKDOWN_OUTLINE_INSTRUCTION_RE,
    parse_document_markdown,
    parse_markdown_outline_instructions,
)

from ..runtime_events import current_events, report_progress
from .requirements import validate_required_sections

from .common import (
    _heading_signature,
    _json,
    _markdown_prose,
    _outline_chart_owners,
    _strip_outline_budget,
    _trace_call,
    prepare_writer_context,
    validate_inputs as validate_writing_inputs,
)


def _normalize_outline_budgets(outline, constraints):
    """Publish real paper budgets, never label Writer's relative weights as length."""
    instructions = parse_markdown_outline_instructions(outline)
    if not instructions:
        return outline
    budgets = {}
    target = constraints.get('word_target')
    if target is not None:
        document = parse_document_markdown(outline, document_id='paper-outline-budget')

        def allocate(blocks, total):
            headings = [block for block in blocks if block.type == 'heading']
            weights = [block.target_chars or 1 for block in headings]
            total_weight = sum(weights)
            assigned, cumulative = 0, 0
            for block, weight in zip(headings, weights):
                cumulative += weight
                end = total * cumulative // total_weight
                budget = end - assigned
                assigned = end
                budgets[block.node_id] = budget
                allocate(block.children, budget)

        allocate(document.blocks, target)
    updated = copy.deepcopy(instructions)
    for node_id, payload in updated.items():
        description = payload.get('outline_description')
        if not isinstance(description, str):
            continue
        description = _strip_outline_budget(description, payload.get('target_chars'))
        if node_id in budgets:
            budget = budgets[node_id]
            # A tiny requested total may leave a heading without a positive budget.
            # Keep the sidecar valid and omit its length hint in that case.
            payload['target_chars'] = budget or None
            if description and budget:
                hint = f' (about {budget} words)' if constraints.get('paper_language') == 'en' \
                    else f'（约{budget}字）'
                ending = description[-1:] if description.endswith(('。', '.', '!', '?', '！', '？')) else ''
                body = description[:-1] if ending else description
                description = body + hint + ending
        payload['outline_description'] = description
    # Edit only validated sidecar JSON. Preserve headings, anchors, chart ownership,
    # research prose and fenced examples byte for byte.
    matches = re.finditer(MARKDOWN_OUTLINE_INSTRUCTION_RE.pattern, _markdown_prose(outline), re.MULTILINE)
    for match in reversed(list(matches)):
        try:
            payload = json.loads(match[1])
        except json.JSONDecodeError:
            continue
        if isinstance(payload, dict) and isinstance(payload.get('node_id'), str) \
                and payload == instructions.get(payload['node_id']):
            replacement = updated[payload['node_id']]
            if replacement != payload:
                outline = outline[:match.start(1)] + _json(replacement) + outline[match.end(1):]
    return outline


def prepare_outline(prepared, toolkit, *, on_delta=None):
    """Reuse Writer outline planning for both the approval step and legacy drafts."""
    task = prepared['writing_task']
    context_json = _json(prepared['writing_context'])
    outline = prepared['outline']
    original_signature = _heading_signature(outline) if outline else None
    previous = _outline_chart_owners(outline or '', prepared['charts'], require_complete=False)
    needs_ownership = len(previous) != len(prepared['charts'])
    if not outline or needs_ownership:
        report_progress(15, '正在生成大纲和图表归属' if prepared['charts'] else '正在生成大纲')
        outline_task = copy.deepcopy(task)
        if not outline:
            from ..prompts.write_document_prompt import PAPER_OUTLINE_INSTRUCTIONS
            outline_task['query'] = outline_task.get('query', '') + PAPER_OUTLINE_INSTRUCTIONS
        if prepared['charts']:
            outline_task['query'] += '\n' + CHART_OUTLINE_INSTRUCTIONS + _json(prepared['chart_directory'])
        if outline:
            outline_task['query'] += (
                '\nAdd only missing chart ownership markers. Preserve every existing image assignment. '
                'Preserve the following outline headings, hierarchy and order verbatim:\n' + outline)
        generate = toolkit.stream_outline if on_delta is not None else toolkit.generate_outline
        kwargs = {'on_delta': on_delta} if on_delta is not None else {}
        outline = _trace_call('outline', generate,
                              writing_task_json=_json(outline_task), writing_context_json=context_json, **kwargs,
                              )
        if original_signature and _heading_signature(outline) != original_signature:
            raise ValueError('Chart ownership planning changed the supplied outline structure.')
    if prepared['charts']:
        for attempt in range(2):
            try:
                owners = _outline_chart_owners(outline, prepared['charts'])
                if any(owners.get(key) != value for key, value in previous.items()):
                    raise ValueError('Image planning moved an existing assignment.')
                break
            except ValueError as error:
                if attempt:
                    raise
                report_progress(65, '正在完善大纲图表归属')
                repair_task = copy.deepcopy(task)
                repair_task['query'] += (
                    '\nCorrect only chart ownership markers in the supplied outline. Preserve its headings, '
                    'hierarchy, order and section instructions. Assign every catalog ID exactly once and retain '
                    'existing confirmed owners.\n' + CHART_OUTLINE_INSTRUCTIONS + _json(prepared['chart_directory'])
                    + '\nConfirmed owners: ' + _json(previous) + '\nValidation feedback: ' + str(error)
                    + '\nOutline to correct:\n' + outline)
                corrected = _trace_call('outline', toolkit.generate_outline,
                                        writing_task_json=_json(repair_task), writing_context_json=context_json)
                if _heading_signature(corrected) != _heading_signature(outline):
                    raise ValueError('Chart ownership correction changed the outline structure.')
                outline = corrected
    _heading_signature(outline)
    validate_required_sections(outline, task.get('constraints', {}))
    return _normalize_outline_budgets(outline, task.get('constraints', {}))


def validate_inputs(inputs):
    validate_writing_inputs(inputs)
    if inputs['manuscript']:
        raise ValueError('Use revise_document to edit an existing manuscript.')


def run(inputs):
    toolkit = WriterCreateToolkit()
    prepared = prepare_writer_context(inputs, toolkit=toolkit)
    prepared['writing_task']['constraints']['visual_policy'] = {'allow_visuals': False}
    events = current_events()

    def on_delta(text):
        if text.strip():
            report_progress(35, '正在输出大纲内容')
        if events is not None:
            events.delta(text)

    outline = prepare_outline(prepared, toolkit, on_delta=on_delta)
    report_progress(85, '大纲已生成，正在校验并保存')
    if events is not None:
        events.completed_preview(outline)
    return {'outline': outline, 'is_demo': False}
