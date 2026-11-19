"""Prepare source-bound paper facts independently of Writer's deterministic plan."""
from collections import Counter
import copy
import json
import re

from lazyllm import AutoModel
from lazyllm.tools.writer.tools.base import _WriterJsonFormatter
from pydantic import BaseModel, ConfigDict, Field, ValidationError, field_validator

from .model_response import normalize_response
from .prompts.write_document_prompt import SECTION_CONTENT_INSTRUCTIONS, SHARED_FACTS_PROMPT


LOCAL_CATEGORIES = {'method', 'literature', 'background'}
GLOBAL_CATEGORIES = {'research_question', 'contribution', 'terminology', 'status', 'limitation', 'conflict', 'number'}


class SourceUse(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    source_id: str = Field(min_length=1)
    categories: list[str] = Field(min_length=1)

    @field_validator('categories')
    @classmethod
    def normalize_categories(cls, values):
        categories = list(dict.fromkeys(value.strip().lower() for value in values))
        if not all(categories):
            raise ValueError('Source categories must be nonblank labels.')
        return categories


class SectionUse(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    instruction_id: str = Field(min_length=1)
    argument_role: str = Field(min_length=1, pattern=r'\S')
    fact_ids: list[str]


class FactPlan(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    sources: list[SourceUse]
    sections: list[SectionUse]


def _strings(value, path=''):
    if isinstance(value, str):
        yield path, value
    elif isinstance(value, dict):
        for key, item in value.items():
            yield from _strings(item, f'{path}.{key}' if path else key)
    elif isinstance(value, list):
        for index, item in enumerate(value):
            yield from _strings(item, f'{path}[{index}]')


def source_paragraphs(resources):
    """The model selects complete source paragraphs, never a meaning-changing substring."""
    spans = []
    for resource in resources:
        kind = resource['meta']['material_kind']
        value = json.loads(resource['inline_text'])
        if kind == 'analysis':
            strings = [('summary', value.get('summary', ''))]
        elif kind == 'paper':
            strings = [(field, text) for key in ('text', 'abstract', 'notes', 'user_annotations')
                       for field, text in _strings(value.get(key), key)]
        elif kind in {'question', 'evidence', 'writing_materials'}:
            strings = list(_strings(value))
        else:
            continue
        for path, text in strings:
            offset = 0
            for paragraph in re.split(r'\n\s*\n', text):
                start = text.find(paragraph, offset)
                offset = start + len(paragraph)
                content = paragraph.strip()
                if not content:
                    continue
                start += len(paragraph) - len(paragraph.lstrip())
                spans.append(dict(id=f'source:{len(spans) + 1}', content=content,
                                  source_id=resource['resource_id'], source_path=path,
                                  start=start, end=start + len(content)))
    return spans


def _fact(identifier, category, value, source_id, source_path='', **provenance):
    return dict(fact_id=identifier, key=category,
                value=json.dumps(dict(content=value, source_id=source_id, source_path=source_path, **provenance),
                                 ensure_ascii=False), source=[source_id], locked=True)


def _analysis_facts(resources):
    analysis = next((json.loads(item['inline_text']) for item in resources
                     if item['meta']['material_kind'] == 'analysis'), {})
    facts = {}
    for index, finding in enumerate(analysis.get('findings', [])):
        identifier = 'analysis:finding:' + finding['id']
        facts[identifier] = _fact(identifier, 'number', copy.deepcopy(finding),
                                  'write_document:analysis', f'findings[{index}]')
    for key in ('targets', 'methods', 'models', 'diagnostics', 'limitations', 'data_sources'):
        if analysis.get(key):
            identifier = 'analysis:' + key
            facts[identifier] = _fact(identifier, 'limitation' if key == 'limitations' else 'method',
                                      copy.deepcopy(analysis[key]), 'write_document:analysis', key)
    return facts


def _validate_selection(value, spans, facts, plan):
    selection = FactPlan.model_validate(value)
    used = [item.source_id for item in selection.sources]
    expected = {span['id'] for span in spans}
    if len(used) != len(set(used)) or set(used) != expected:
        raise ValueError('Classify every supplied source paragraph exactly once; do not omit status or conflicts.')
    _validate_section_uses(selection.sections, expected | set(facts), plan)
    return selection


def _validate_section_uses(sections, available, plan):
    if [item.instruction_id for item in sections] != [item['instruction_id'] for item in plan['instructions']]:
        raise ValueError('Section assignments must match every real Writer instruction ID in order.')
    for section in sections:
        if len(section.fact_ids) != len(set(section.fact_ids)) or not set(section.fact_ids) <= available:
            raise ValueError('Section fact_ids must be distinct known source/analysis IDs.')


def _retain_valid_selection(value, spans, facts, plan):
    """Retry damaged classifications without regenerating hundreds of valid entries."""
    if not isinstance(value, dict) or not isinstance(value.get('sources'), list):
        return [], []
    expected = {span['id'] for span in spans}
    try:
        sections = [SectionUse.model_validate(item) for item in value['sections']]
        _validate_section_uses(sections, expected | set(facts), plan)
    except (ValueError, TypeError, KeyError):
        return [], []
    sources = []
    for item in value['sources']:
        try:
            source = SourceUse.model_validate(item)
        except (ValueError, TypeError):
            continue
        if source.source_id in expected:
            sources.append(source)
    counts = Counter(item.source_id for item in sources)
    return [item.model_dump() for item in sources if counts[item.source_id] == 1], [
        item.model_dump() for item in sections]


def _select_facts(spans, facts, plan, prepared):
    fields = ('instruction_id', 'section_title', 'section_goal', 'required_points', 'expected_blocks')
    payload = dict(schema=FactPlan.model_json_schema(), source_paragraphs=spans,
                   analysis_facts=list(facts.values()),
                   task=prepared['writing_task'], sections=[
                       {key: item.get(key) for key in fields}
                       for item in plan['instructions']])
    model = AutoModel(model='llm')
    error = ''
    retained_sources, retained_sections = [], []
    for attempt in range(2):
        response = model(SHARED_FACTS_PROMPT + '\n' + json.dumps(dict(payload, correction=error), ensure_ascii=False))
        value = None
        try:
            body = normalize_response(response).body
            value = _WriterJsonFormatter()(body) if isinstance(body, str) else body
            if retained_sources and isinstance(value, dict) and isinstance(value.get('sources'), list):
                incoming = {item.get('source_id') for item in value['sources'] if isinstance(item, dict)
                            and isinstance(item.get('source_id'), str)}
                value['sources'] = ([item for item in retained_sources if item['source_id'] not in incoming]
                                    + value['sources'])
                if not value.get('sections'):
                    value['sections'] = retained_sections
            return _validate_selection(value, spans, facts, plan)
        except (ValueError, TypeError, KeyError) as exc:
            if isinstance(exc, ValidationError):
                counts = Counter(item['msg'] for item in exc.errors(include_input=False, include_url=False))
                error = '; '.join(f'{message} ({count} errors)' for message, count in counts.items())
            else:
                error = str(exc)
            if attempt:
                raise ValueError('Shared paper fact preparation failed: ' + error) from exc
            retained_sources, retained_sections = _retain_valid_selection(value, spans, facts, plan)
            if retained_sources:
                retained_ids = {item['source_id'] for item in retained_sources}
                payload['source_paragraphs'] = [item for item in spans if item['id'] not in retained_ids]
                payload['sections'] = []
                error += ('\nValid classifications and section assignments are retained by the host. '
                          'Return classifications ONLY for the supplied source_paragraphs, with sections=[].')


def bind_shared_facts(plan, prepared, *, selection=None):
    """Enrich the real deterministic Writer plan without requiring invented plan metadata.

    Source paragraphs retain negation and scope; source-bound does not mean verified.
    Numeric facts come directly from F4, including denominators, units and uncertainty.
    """
    resources = prepared.get('resources', [])
    spans = source_paragraphs(resources)
    facts = _analysis_facts(resources)
    if not spans and not facts:
        return
    selected = (_select_facts(spans, facts, plan, prepared) if selection is None
                else _validate_selection(selection, spans, facts, plan))
    by_id = {span['id']: span for span in spans}
    global_ids = {key for key, fact in facts.items() if fact['key'] in GLOBAL_CATEGORIES}
    assigned_ids = {key for section in selected.sections for key in section.fact_ids}
    coverage, categories = [], set()
    for item in selected.sources:
        span = by_id[item.source_id]
        category = item.categories[0]
        facts[span['id']] = _fact(
            span['id'], category, span['content'], span['source_id'], span['source_path'],
            start=span['start'], end=span['end'], categories=item.categories)
        # Unrecognized labels or missing section assignments must not hide source evidence.
        if (set(item.categories) - LOCAL_CATEGORIES
                or ('background' not in item.categories and span['id'] not in assigned_ids)):
            global_ids.add(span['id'])
        categories.update(item.categories)
        coverage.append(dict(id=span['id'], source_id=span['source_id'], source_path=span['source_path'],
                             start=span['start'], end=span['end'], categories=item.categories))
    roles = []
    for instruction, assignment in zip(plan['instructions'], selected.sections):
        instruction['meta'].update(argument_role=assignment.argument_role, paper_fact_ids=assignment.fact_ids)
        instruction['fact_constraints'] = [SECTION_CONTENT_INSTRUCTIONS] + [
            facts[key]['key'] + ': ' + facts[key]['value'] for key in assignment.fact_ids]
        instruction['required_points'].append('This section\'s argument task: ' + assignment.argument_role)
        roles.append(dict(section=instruction['content_ref'], argument_role=assignment.argument_role))
    categories.update(fact['key'] for fact in facts.values())
    context = copy.deepcopy(prepared['writing_context'])
    context['facts'] = [fact for key, fact in facts.items() if key in global_ids]
    for resource in resources:
        if resource['meta']['material_kind'] == 'paper':
            paper = json.loads(resource['inline_text'])
            fields = ('id', 'title', 'content_type', 'truncated', 'reading')
            reading_coverage = {key: paper[key] for key in fields if key in paper}
            context['facts'].append(_fact(resource['resource_id'] + ':coverage', 'paper_reading_coverage',
                                          reading_coverage, resource['resource_id']))
    for key, source_id in (('reference_candidates', 'write_document:reference_candidates'),
                           ('chart_directory', 'write_document:charts')):
        if prepared.get(key):
            value = prepared[key]
            if key == 'chart_directory':
                value = [{field: item for field, item in chart.items() if field != 'findings'} for chart in value]
            context['facts'].append(_fact('paper:' + key, key, value, source_id))
    context.setdefault('meta', {}).update(
        paper_fact_gaps=sorted(GLOBAL_CATEGORIES - {'number', 'conflict'} - categories),
        paper_section_roles=roles, paper_source_coverage=coverage,
        paper_fact_policy='Complete source paragraphs, not independently verified. Missing categories are unknown; '
                          'missing status does not mean no experiment. Preserve conflicting sources and negation.')
    prepared['writing_context'] = context
    prepared['shared_facts'] = list(facts.values())
    from .analysis import trace as diagnostics
    diagnostics.save('shared-facts-bound-plan.json', plan)
    diagnostics.save('shared-facts-context.json', context)


class RelationProposal(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    fact_id: str = Field(min_length=1)
    quote: str = Field(min_length=1, max_length=2000)
    replacement: str = Field(min_length=1, max_length=2000)
    reason: str = Field(min_length=1)


class RelationIssue(RelationProposal):
    source_quote: str = Field(min_length=1)


class RelationResponse(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    issues: list[RelationProposal]
    pending: list[str] = Field(default_factory=list)


class RelationReview(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    issues: list[RelationIssue]
    pending: list[str] = Field(default_factory=list)


def _relation_sources(prepared):
    sources = {}
    for fact in prepared.get('shared_facts', []):
        if fact['key'] == 'background':
            continue
        value = json.loads(fact['value'])
        content = value['content']
        sources[fact['fact_id']] = dict(
            fact_id=fact['fact_id'], source_id=value['source_id'], source_path=value['source_path'],
            categories=value.get('categories', [fact['key']]),
            text=content if isinstance(content, str) else json.dumps(content, ensure_ascii=False, sort_keys=True))
    return sources


def _review_relations(body, sources, previous=None):
    from .prompts.write_document_prompt import FACT_RELATION_REVIEW_PROMPT
    payload = dict(schema=RelationResponse.model_json_schema(), source_facts=list(sources.values()),
                   manuscript=body, previous_corrections=previous or [])
    response = AutoModel(model='llm')(FACT_RELATION_REVIEW_PROMPT + '\n' + json.dumps(payload, ensure_ascii=False))
    from .analysis import trace as diagnostics
    phase = 'recheck' if previous else 'initial'
    diagnostics.save('shared-fact-relation-model-' + phase + '.json', dict(response=str(response)))
    body = normalize_response(response).body
    proposed = RelationResponse.model_validate(_WriterJsonFormatter()(body) if isinstance(body, str) else body)
    issues, pending = [], list(proposed.pending)
    for issue in proposed.issues:
        source = sources.get(issue.fact_id)
        if source is None:
            pending.append(f'未应用核对建议（{issue.fact_id}）：来源标识不存在，无法核实该建议。')
            continue
        # The host binds the complete immutable source. A model need not copy a
        # long paragraph, and cannot replace its negation/status with an excerpt.
        issues.append(RelationIssue(**issue.model_dump(), source_quote=source['text']))
    return RelationReview(issues=issues, pending=pending)


def _relation_edits(body, review, sources):
    edits = []
    tokens = re.compile(r'[A-Za-z][A-Za-z0-9_.-]*|\d[\d,]*(?:\.\d+)?')
    for issue in review.issues:
        source = sources.get(issue.fact_id)
        if source is None or issue.source_quote != source['text']:
            raise ValueError('共享事实关系检查引用了不存在的来源或来源摘录。')
        if body.count(issue.quote) != 1 or '\n\n' in issue.quote:
            raise ValueError('关系修正必须定位唯一的已有段落片段，不能整篇重写。')
        if issue.quote == issue.replacement:
            raise ValueError('关系修正没有修改原文。')
        original = set(tokens.findall(issue.quote))
        inserted = set(tokens.findall(issue.replacement)) - original
        source_tokens = set(tokens.findall(issue.source_quote))
        if 'conflict' in source['categories']:
            conflicting = {token for token in source_tokens if any(char.isdigit() for char in token)}
            if not conflicting <= set(tokens.findall(issue.replacement)):
                raise ValueError('不能从冲突来源中只选择一个数值作为已证实结论。')
        if any(any(char.isdigit() for char in token) and token not in source_tokens for token in inserted):
            raise ValueError('关系修正引入了来源摘录未支持的新数字或标识。')
        start = body.index(issue.quote)
        edits.append((start, start + len(issue.quote), issue.replacement))
    edits.sort()
    if any(left[1] > right[0] for left, right in zip(edits, edits[1:])):
        raise ValueError('共享事实关系修正存在重叠，未修改稿件。')
    return edits


def _ground_relation_review(body, review, sources):
    """Never apply an unverifiable model proposal or mistake it for a draft defect."""
    accepted, rejected, spans = [], [], []
    for issue in review.issues:
        try:
            edits = _relation_edits(body, RelationReview(issues=[issue]), sources)
        except ValueError as exc:
            rejected.append(dict(proposal=issue.model_dump(), reason=str(exc)))
        else:
            accepted.append(issue)
            spans.append(edits[0][:2])
    overlaps = {index for index, span in enumerate(spans) for other, candidate in enumerate(spans)
                if index != other and span[0] < candidate[1] and candidate[0] < span[1]}
    for index in sorted(overlaps):
        rejected.append(dict(proposal=accepted[index].model_dump(), reason='与其他核对建议定位重叠，未应用。'))
    accepted = [issue for index, issue in enumerate(accepted) if index not in overlaps]
    pending = list(review.pending) + [
        f"未应用核对建议（{item['proposal']['fact_id']}）：{item['reason']}" for item in rejected]
    return RelationReview(issues=accepted, pending=pending), rejected


def _show_pending(pending):
    if pending:
        from .runtime_events import report_progress
        report_progress(86, '初稿仍有来源待确认；以下为核对建议与覆盖限制，尚未核实：\n'
                        + '\n'.join('- ' + item for item in pending))


def check_fact_relations(body, prepared):
    """Source-grounded model review with one bounded correction, not deterministic fact verification."""
    from .analysis import trace as diagnostics
    sources = _relation_sources(prepared)
    if not sources:
        return body
    proposed = _review_relations(body, sources)
    initial, rejected = _ground_relation_review(body, proposed, sources)
    report = dict(kind='source_grounded_model_review', independently_verified=False,
                  initial=proposed.model_dump(), corrections=[], pending=initial.pending,
                  rejected_proposals=rejected)
    diagnostics.save('shared-fact-relations-initial.json', report)
    edits = _relation_edits(body, initial, sources)
    if not edits:
        diagnostics.save('shared-fact-relations-final.json', report)
        _show_pending(report['pending'])
        return body
    revised = body
    for start, end, replacement in reversed(edits):
        revised = revised[:start] + replacement + revised[end:]
    report['corrections'] = [issue.model_dump() for issue in initial.issues]
    proposed_final = _review_relations(revised, sources, report['corrections'])
    final, rejected_final = _ground_relation_review(revised, proposed_final, sources)
    report['rejected_proposals'].extend(rejected_final)
    report.update(final=proposed_final.model_dump(), pending=list(dict.fromkeys(initial.pending + final.pending)))
    diagnostics.save('shared-fact-relations-final.json', report)
    _show_pending(report['pending'])
    if final.issues:
        raise ValueError('一次局部更正后仍有来源关系冲突，已保留核对记录；请检查材料和对应段落。')
    return revised
