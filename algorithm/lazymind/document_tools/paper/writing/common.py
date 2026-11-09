"""Paper writing and v2-to-Writer preparation, without workflow or pool access.

Shared material preparation and validation for the three writing entries.
"""
import copy
from collections import Counter
import json
import re
import logging
import time
from threading import Lock

from lazyllm import tracing

from lazyllm.tools.writer.data_models.planning import SectionInstructionList
from lazyllm.tools.writer.data_models.quality import ReviewReport
from lazyllm.tools.writer.utils.serialization import (
    get_markdown_outline_targets,
    markdown_section_range,
    parse_document_markdown,
    parse_markdown_sections,
    parse_markdown_outline_instructions,
)

from .. import contracts
from ..publication import (
    figure_catalog,
    normalize_source_citations,
    protect_reference_region,
    split_references,
    validate_citations,
    validate_unmanaged_additions,
    markdown_prose as _markdown_prose,
    validate_chart_references as _validate_chart_references,
)
from ..runtime_events import report_progress

from ...toolkits import WriterCreateToolkit
from .requirements import resolve_constraints


from ..prompts.write_document_prompt import (
    WRITING_INSTRUCTIONS,
    MATERIAL_USAGE_INSTRUCTIONS,
    CHART_USAGE_INSTRUCTIONS,
    REFERENCE_USAGE_INSTRUCTIONS,
    SECTION_CONTENT_INSTRUCTIONS,
    WRITING_TASK_HEADER,
    REVISE_MODE_INSTRUCTIONS,
    DRAFT_MODE_INSTRUCTIONS,
    SECTION_NO_TARGET_INSTRUCTIONS,
    SECTION_BUDGET_INSTRUCTIONS,
    DIRECT_REVISION_INSTRUCTIONS,
    CHART_REFERENCE_GUIDANCE,
)


# ======= 写作阶段埋点与章节回调 =======

LOG = logging.getLogger(__name__)


_CHART_OWNERS = re.compile(r'<!-- paper-charts:\s*(\[[^\n]*?\])\s*-->')


def _safe(function, *args, **kwargs):
    try:
        return function(*args, **kwargs)
    except Exception:
        LOG.warning('F5 trace recording failed', exc_info=True)


def _record(record):
    LOG.info('[PaperWritingTiming] %s', json.dumps(record, ensure_ascii=False))


def _trace_call(phase, function, *args, **kwargs):
    """Measure business stages, leaving nested Writer LLM spans intact."""
    def stage():
        pass
    stage.__name__ = 'paper.write_document.' + phase
    context = _safe(tracing.get_trace_context)
    record = {'phase': phase, 'trace_id': getattr(context, 'trace_id', None)}
    # These are adapter inputs, not claimed to be final model prompt/token sizes.
    for key, value in kwargs.items():
        if key.endswith('_json') and isinstance(value, str):
            record[key + '_chars'] = len(value)
    if phase == 'prepare' and args:
        for key in ('papers', 'evidence', 'analysis', 'charts', 'question'):
            value = args[0].get(key)
            record[key + '_json_chars'] = len(json.dumps(value, ensure_ascii=False))
            if isinstance(value, list):
                record[key + '_count'] = len(value)
            if key == 'analysis' and isinstance(value, dict):
                record['finding_count'] = len(value.get('findings', []))
    span = _safe(tracing.start_span, span_kind='callable', target=stage, args=(), kwargs={})
    started = time.monotonic()
    _safe(_record, {**record, 'status': 'started'})
    try:
        result = function(*args, **kwargs)
        record['status'] = 'completed'
        if isinstance(result, str):
            record['output_chars'] = len(result)
            if phase == 'consistency':
                record['quality_passed'] = _safe(
                    lambda: json.loads(result)['review_report']['result']['is_passed'])
        return result
    except BaseException as error:
        record.update(status='error', error_type=type(error).__name__)
        _safe(tracing.set_span_error, span, error)
        raise
    finally:
        record['elapsed_ms'] = round((time.monotonic() - started) * 1000)
        _safe(tracing.set_span_attributes, span, {
            'paper.writing.' + key: value for key, value in record.items() if value is not None
        })
        _safe(tracing.finish_span, span)
        _safe(_record, record)


def _section_callbacks(events):
    """Capture correlation before workers run; forward original callbacks unchanged."""
    context = _safe(tracing.get_trace_context)
    trace_id = getattr(context, 'trace_id', None)
    started, seen, lock = time.monotonic(), set(), Lock()
    output_chars = {}

    def progress(event):
        state = {key: event.get(key) for key in (
            'section_index', 'section_total', 'section_state', 'section_attempt')}
        signature = tuple(state.values())
        with lock:
            fresh = signature not in seen
            seen.add(signature)
            index = state['section_index']
            if 'section_output_chars' in event:
                output_chars[index] = event['section_output_chars']
            state['last_reported_output_chars'] = output_chars.get(index)
        if fresh:
            _safe(_record, dict(phase='section_progress', trace_id=trace_id, **state,
                                elapsed_ms=round((time.monotonic() - started) * 1000)))
        if events:
            events.section_progress(event)

    def restart(prefix):
        _safe(_record, dict(phase='preview_restart', trace_id=trace_id, prefix_chars=len(prefix)))
        if events:
            events.restart(prefix)

    callbacks = {'on_progress': progress}
    # Presence of this callback permits Writer retries after body output. Preserve that policy.
    if events:
        callbacks['on_preview_restart'] = restart
    return callbacks


def _json(value):
    return json.dumps(value, ensure_ascii=False)


def _chart_caption(title):
    return re.sub(r'([\\\[\]])', r'\\\1', title).replace('\n', ' ').replace('\r', ' ')


def _chart_directory(chart, findings):
    """Project contract-validated chart associations from a shared finding index."""
    if 'media_uri' in chart:
        return {key: chart[key] for key in ('id', 'title', 'caption')}
    if any(ref not in findings for ref in chart['finding_ids']):
        raise ValueError('chart.finding_ids 包含未知的分析结论')
    return {**{key: copy.deepcopy(chart[key]) for key in ('id', 'title', 'columns', 'caption', 'finding_ids')},
            'findings': [copy.deepcopy(findings[ref]) for ref in chart['finding_ids']]}


def _reference_candidates(papers):
    candidates = []
    for paper in papers:
        entry = paper.get('citation')
        if entry is None:
            continue
        candidates.append({'id': paper['id'], 'citation': entry})
    return candidates


def _validate_reference_markers(body, candidates, original=None):
    """Check reserved citation markers; preserve existing unsupported citations."""
    def markers(text):
        return Counter(re.findall(r'\[文献[^\]\n]*(?:\]|(?=\n|$))', _markdown_prose(text)))
    if normalize_source_citations(body, candidates) != body:
        raise ValueError('成稿来源引用必须使用 [文献:ID]，不能遗留综述的来源标记或列表')
    validate_unmanaged_additions(original, body)
    if original is not None:
        protect_reference_region(original, body)
    if '#paper-ref-' in body or 'paper-references-' in body:
        validate_citations(body, candidates)
    existing = markers(original or '')
    allowed = {item['id'] for item in candidates}
    for marker, count in markers(body).items():
        match = re.fullmatch(r'\[文献:([\w.-]+)\]', marker)
        if match and match[1] in allowed:
            continue
        if count > existing[marker]:
            raise ValueError('Invalid or unavailable reference marker: ' + marker)


def _outline_chart_owners(outline, charts, *, require_complete=True):
    catalog = {chart['id'] for chart in charts}
    owners = {}
    for level, path, occurrence, body in parse_markdown_sections(_markdown_prose(outline)):
        for match in _CHART_OWNERS.finditer(body):
            ids = json.loads(match[1])
            if level == 1 or not isinstance(ids, list) or any(not isinstance(key, str) for key in ids):
                raise ValueError('Chart ownership requires a body heading and a list of chart IDs.')
            for key in ids:
                if key not in catalog:
                    raise ValueError('Outline assigns an unknown chart.')
                if key in owners:
                    raise ValueError('Outline assigns a chart more than once.')
                owners[key] = (path, occurrence)
    if require_complete and owners.keys() != catalog:
        raise ValueError('Outline must assign every supplied chart exactly once.')
    return owners


def _bind_chart_references(plan, prepared: dict, outline):
    """Translate outline ownership into Writer's existing cross-reference protocol."""
    if not prepared['charts']:
        return
    instructions = plan['instructions']
    placements = _outline_chart_owners(outline, prepared['charts'])
    owners = {}
    for chart in prepared['charts']:
        path, occurrence = placements[chart['id']]
        target_start, target_end = markdown_section_range(outline, path, occurrence)
        matches = []
        for i, item in enumerate(instructions):
            section = item['content_ref']
            start, end = markdown_section_range(
                outline, section['heading_path'], section.get('occurrence', 1),
            )
            if start <= target_start and target_end <= end:
                matches.append(i)
        if len(matches) != 1:
            raise ValueError('Chart placement does not match a planned section.')
        owners[chart['id']] = (matches[0], chart, path)
    for index, item in enumerate(instructions):
        meta = item['meta']
        meta['cross_reference_targets'] = list(dict.fromkeys(
            [*meta.get('cross_reference_targets', []), *owners]))
        references = meta.setdefault('cross_references', [])
        for target, (owner, chart, heading_path) in owners.items():
            references.append({
                'target': target, 'kind': 'image', 'required': owner == index,
                'must_create': owner == index, 'caption': _chart_caption(chart['title']),
                'guidance': (CHART_REFERENCE_GUIDANCE.format(title=chart['title'])
                             + ' Figure caption (preserve supplied meaning): ' + chart['caption']
                             + ' Place the chart in section: ' + ' / '.join(heading_path)),
            })


def _materialize_chart_insertions(body, charts):
    """Expand bounded revision insertions without invoking media generation."""
    catalog = {chart['id']: chart for chart in charts}
    prose = _markdown_prose(body).split('\n')
    lines = body.split('\n')
    for index, line in enumerate(prose):
        if '[图表:' not in line:
            continue
        match = re.fullmatch(r'\s*\[图表:([^\]]+)\]\s*', line)
        if match is None:
            raise ValueError('Chart insertion must be a standalone chart ID.')
        if match[1] not in catalog:
            raise ValueError('Writer manuscript references an unknown chart.')
        chart = catalog[match[1]]
        target = chart['id']
        caption = _chart_caption(chart['title'])
        lines[index] = f'<a id="block-{target}"></a>\n![{caption}](media-placeholder://{target})'
    return '\n'.join(lines)


def prepare_writer_inputs(inputs: dict, *, toolkit=None) -> dict:
    """Adapt normalized v2 materials using the shared, model-free Writer builders.

    This internal bundle is not the public run output or a new business contract.
    Resource roles are already known, so no model-based resource classification is
    needed. Full material text is kept as key facts for the context builder.
    """
    toolkit = toolkit if toolkit is not None else WriterCreateToolkit()
    source = copy.deepcopy(inputs)
    constraints = source['constraints']
    if source['mode'] == 'draft':
        constraints = resolve_constraints(constraints, source['request'])
        source['constraints'] = constraints
    selected_issues = []
    review = None
    if source.get('review_data') is not None:
        review_data = source['review_data']
        issues = {item['id']: item for item in review_data['issues']}
        selected_issues = [issues[key] for key in source['selected_issue_ids']]
        # Only selected recommendations enter the revision context. The full report's
        # summary may contain instructions about unselected issues.
        review = {'issues': selected_issues, 'limitations': review_data['limitations']}
    # Build the material rules before context creation: replacement generation
    # receives context and plan, but does not receive the writing task directly.
    query = '\n\n'.join((
        WRITING_INSTRUCTIONS, MATERIAL_USAGE_INSTRUCTIONS,
        CHART_USAGE_INSTRUCTIONS, REFERENCE_USAGE_INSTRUCTIONS,
    )) + WRITING_TASK_HEADER + _json({
        'topic': source['topic'], 'request': source['request'],
        'constraints': constraints, 'mode': source['mode'],
        'selected_issue_ids': source['selected_issue_ids'],
    })
    if review is not None:
        query += REVISE_MODE_INSTRUCTIONS
    elif source['mode'] == 'revise':
        query += DIRECT_REVISION_INSTRUCTIONS
    else:
        query += DRAFT_MODE_INSTRUCTIONS
    task = json.loads(toolkit.build_writing_task(query=query))
    # Explicit structured constraints take precedence over incidental numbers in
    # user prose. English words must not be passed to Writer as Chinese chars.
    task['constraints'] = copy.deepcopy(constraints)
    target = constraints['word_target']
    if target is not None and constraints['paper_language'] == 'zh-CN':
        task['constraints'].update(target_chars=target, max_chars=int(target * 1.1))
    # Business mode stays upstream-owned; Writer distinguishes creation from editing.
    task['task_type'] = {
        'draft': 'write',
        'revise': 'revise',
    }[source['mode']]
    task['scope'] = 'document'
    task['output'] = {'representation': 'markdown'}
    task['meta'] = {'source': 'write_document', 'mode': source['mode'], 'review_driven': review is not None}
    resources = []

    def material(key, title, value):
        if value is None or value == []:
            return
        resources.append({
            'resource_id': 'write_document:' + key, 'resource_type': 'text',
            'title': title, 'inline_text': _json(value),
            'meta': {'role': 'background', 'material_kind': key.split(':', 1)[0]},
        })

    for paper in source['papers']:
        material('paper:' + paper['id'], paper['title'], paper)
    candidates = _reference_candidates(source['papers'])
    material('reference_candidates',
             'Citation candidate catalog (only these sources may receive new citations)', candidates)
    material('question', 'Research question, hypotheses and validation plan', source['question'])
    material('evidence', 'Literature evidence and limitations', source['evidence'])
    material('analysis', 'Upstream analysis results (not recalculated in this step)', source['analysis'])
    findings = {item['id']: item for item in (source['analysis'] or {}).get('findings', [])}
    charts = figure_catalog(source['charts'], source.get('media_assets'))
    chart_directory = [_chart_directory(chart, findings) for chart in charts]
    material('writing_materials', 'Uploaded writing materials (source facts, not inferred analysis)',
             source.get('writing_materials'))
    material('charts', 'Chart reference catalog', chart_directory)
    material('outline', 'Existing outline', source['outline'])
    material('manuscript', 'Current manuscript body',
             split_references(source['manuscript'])[0] if source['manuscript'] else source['manuscript'])
    material('review', 'Current review issues for this revision', review)
    resources = json.loads(toolkit.build_resources(input_resources_json=_json(resources)))
    profiles = [{
        'resource_id': resource['resource_id'], 'resource_role': 'background',
        'raw_content': resource['inline_text'], 'key_facts': [resource['inline_text']],
        'meta': copy.deepcopy(resource['meta']),
    } for resource in resources]
    material_identifiers = {resource['resource_id'] for resource in resources}
    material_identifiers.update(paper['id'] for paper in source['papers'] if paper['id'].startswith('upload-'))
    for paper in source['papers']:
        if paper.get('review_role') == 'background' and not paper.get('citation'):
            material_identifiers.add('[文献:' + paper['id'] + ']')
    material_identifiers.update(item['id'] for item in (source['analysis'] or {}).get('data_sources', []))
    return {
        'writing_task': task, 'resources': resources, 'resource_profiles': profiles,
        'outline': source['outline'], 'manuscript': source['manuscript'],
        'charts': charts, 'chart_directory': chart_directory, 'selected_issues': selected_issues,
        'reference_candidates': candidates,
        'material_source_ids': [paper['id'] for paper in source['papers']
                                if paper.get('attachment_key') and not paper.get('citation')],
        'material_identifiers': sorted(material_identifiers, key=len, reverse=True),
    }


def prepare_writer_context(inputs: dict, *, toolkit=None) -> dict:
    """Prepare a Writer-consumable task/context, without invoking a model.

    The shared context builder owns its internal temporary artifacts. This helper
    does not save public artifacts, access a material pool or resolve input paths.
    """
    toolkit = toolkit if toolkit is not None else WriterCreateToolkit()
    prepared = prepare_writer_inputs(inputs, toolkit=toolkit)
    context = json.loads(toolkit.create_writing_context(
        writing_task_json=_json(prepared['writing_task']),
        resource_profiles_json=_json(prepared['resource_profiles']),
    ))
    locked = {resource['resource_id'] for resource in prepared['resources']
              if resource['meta']['material_kind'] in {'paper', 'evidence', 'analysis'}}
    for fact in context['facts']:
        if fact['key'] in locked:
            fact['locked'] = True
    prepared['writing_context'] = context
    return prepared


def _heading_signature(markdown):
    """Use Writer's fence-aware parser; add only the paper hierarchy constraint."""
    get_markdown_outline_targets(markdown)
    signature = [(level, path[-1]) for level, path, _, _ in parse_markdown_sections(markdown)]
    previous = 0
    for level, title in signature:
        if not title or level > previous + 1:
            raise ValueError('Paper headings must not skip levels or have empty titles.')
        previous = level
    return signature


def _strip_outline_budget(description, budget):
    """Strip only the generated suffix matching this heading's own metadata."""
    if not isinstance(description, str) or type(budget) is not int or budget <= 0:
        return description
    suffix = rf'(?:（约{budget}字）| \(about {budget} (?:characters|words)\))(?=[。.!?！？]?$)'
    return re.sub(suffix, '', description).strip()


def _outline_budget_descriptions(outline):
    """Remove Writer budget suffixes only from their matching outline descriptions."""
    replacements = {}
    for payload in parse_markdown_outline_instructions(outline).values():
        description = payload.get('outline_description')
        cleaned = _strip_outline_budget(description, payload.get('target_chars'))
        if cleaned != description:
            replacements[description] = cleaned
    return replacements


def _prepare_sections(plan, outline, constraints):
    """Retain Writer planning and apply language-aware paper budgets."""
    plan = SectionInstructionList.model_validate(plan).model_dump(exclude_none=True)
    instructions = plan['instructions']
    _, targets = get_markdown_outline_targets(outline)
    expected = [(path, occurrence) for _, path, occurrence, _ in targets]
    actual = [(item['content_ref']['heading_path'], item['content_ref']['occurrence'])
              for item in instructions]
    if actual != expected:
        raise ValueError('Writer section plan does not match the supplied outline.')
    target = constraints['word_target']
    unit = 'words' if constraints['paper_language'] == 'en' else 'chinese_characters'
    # Prefer confirmed outline budgets over a fresh section planner's weights.
    # English targets remain word counts; the final audit uses the academic counter.
    weights = [max(1, int(item['meta'].get('target_chars') or 1)) for item in instructions]
    outline_document = parse_document_markdown(outline, document_id='paper-section-budgets')
    outline_weights = [block.target_chars for block in outline_document.blocks
                       if block.type == 'heading' and block.numbering.get('level') == 1]
    if len(outline_weights) == len(instructions) and all(outline_weights):
        weights = outline_weights
    descriptions = _outline_budget_descriptions(outline)
    assigned = 0
    for index, item in enumerate(instructions):
        # Do not regex-strip numbers from research facts or user instructions.
        # Only replace complete descriptions tied to Writer's outline metadata.
        item['section_goal'] = descriptions.get(item['section_goal'], item['section_goal'])
        for field in ('required_points', 'expected_blocks'):
            item[field] = [descriptions.get(value, value) for value in item[field]]
        meta = item['meta']
        meta.pop('target_chars', None)
        meta.pop('max_chars', None)
        meta.pop('word_target', None)
        meta.pop('count_unit', None)
        for heading in item.get('heading_structure') or []:
            heading.pop('target_chars', None)
        if target is not None:
            cumulative = target * sum(weights[:index + 1]) // sum(weights)
            budget = cumulative - assigned
            assigned = cumulative
            meta.update(word_target=budget, count_unit=unit)
            item['required_points'].append(
                SECTION_BUDGET_INSTRUCTIONS.format(budget=budget, unit=unit, target=constraints['word_target'])
            )
            if unit == 'chinese_characters' and budget > 0:
                meta.update(target_chars=budget, max_chars=int(budget * 1.1))
        else:
            item['required_points'].append(SECTION_NO_TARGET_INSTRUCTIONS)
        item['fact_constraints'].append(SECTION_CONTENT_INSTRUCTIONS)
    return plan


def _matches_outline(actual, signature):
    planned = [heading for heading in actual if heading in signature]
    additions = [heading for heading in actual if heading not in signature]
    return planned == signature and all(level > 2 for level, _ in additions)


def _validate_manuscript_structure(body, *, signature=None, charts=None,
                                   candidates=None, original=None):
    """Shared mechanical checks for generation and the API result boundary."""
    actual = _heading_signature(body)
    if signature is not None and not _matches_outline(actual, signature):
        raise ValueError('Writer manuscript headings do not match the outline.')
    if charts is not None:
        _validate_chart_references(body, charts)
    if candidates is not None:
        _validate_reference_markers(body, candidates, original)
    return actual


def _validate_manuscript(body, signature, charts, constraints, toolkit, context_json, *,
                         candidates, original=None, check_quality=True):
    """Validate generated Markdown before the optional model quality check."""
    report_progress(87, '正在校验稿件结构与引用')
    _trace_call('validate_structure', _validate_manuscript_structure, body,
                signature=signature, charts=charts, candidates=candidates, original=original)
    if not check_quality:
        return
    report_progress(90, '正在检查全文一致性')
    quality = json.loads(_trace_call('consistency', toolkit.check_consistency,
                                     draft_document_json=body, writing_context_json=context_json,
                                     ))
    report = ReviewReport.model_validate(quality['review_report'])
    if not report.result.is_passed:
        raise ValueError('Writer consistency check failed: ' + report.result.summary)
    report_progress(95, '全文一致性检查通过')


def validate_inputs(inputs):
    """Writing-specific prerequisites after the shared material contract."""
    if inputs['outline'] and not inputs['manuscript']:
        _heading_signature(inputs['outline'])
    if inputs['manuscript']:
        _heading_signature(inputs['manuscript'])
    selected = set(inputs['selected_issue_ids'])
    if inputs['mode'] == 'draft':
        if inputs['manuscript']:
            raise ValueError('Use revise_document to edit an existing manuscript.')
        if inputs['review'] is not None or inputs.get('review_data') is not None or selected:
            raise ValueError('draft does not accept review materials or selected IDs')
    elif inputs.get('review_data') is not None:
        if not selected or not selected <= {i['id'] for i in inputs['review_data']['issues']}:
            raise ValueError('selected_issue_ids must be nonempty and match the current review')
    elif not inputs['request'].strip():
        raise contracts.MissingMaterials('请明确本轮需要修改的内容。')


def validate_result(value, inputs):
    """Check writing output independently of storage and public field validation."""
    for key in ('outline', 'manuscript'):
        if value.get(key) is not None and len(value[key]) > 2_000_000:
            raise ValueError(f'{key} 超过长度限制')
    signature = _validate_manuscript_structure(
        value['manuscript'],
        charts=figure_catalog(inputs['charts'], inputs.get('media_assets')) if inputs else None,
        candidates=_reference_candidates(inputs['papers']) if inputs else None,
        original=inputs['manuscript'] if inputs else None,
    )
    if inputs and inputs['mode'] == 'revise':
        return
    if inputs and (not inputs['outline'] or not _matches_outline(signature, _heading_signature(inputs['outline']))):
        raise ValueError('确认的大纲与稿件不一致')
