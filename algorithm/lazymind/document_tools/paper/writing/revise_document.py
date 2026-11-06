"""Scoped paper revision using the public Writer revision toolkit."""
from ..prompts.write_document_prompt import REVIEW_REVISION_INSTRUCTIONS, REVISION_SCOPE_INSTRUCTIONS
from lazyllm.tools.writer.data_models.revision import MarkdownModifyPlan, StringReplaceSet
from ...toolkits import WriterCreateToolkit, WriterRevisionToolkit
from ..publication import validate_chart_references as _validate_chart_references
from ..publication import split_references, repair_generated_citation_links
from .. import contracts
import copy
import re
import uuid
from ..runtime_events import current_events, report_progress
import json
from lazyllm import AutoModel
from lazyllm.tools.writer.tools.base import _WriterJsonFormatter
from lazyllm.tools.writer.utils import locate_markdown_paragraph
from lazyllm.tools.writer.utils.serialization import markdown_section_range
from .recovery import repair_locators, repair_replacements, repair_reader_provenance
from .matching import resolve_locator

from .common import (  # noqa: F401 - validate_* are public algorithm entries
    _heading_signature,
    _markdown_prose,
    _json,
    _materialize_chart_insertions,
    _validate_manuscript,
    _validate_reference_markers,
    prepare_writer_context,
    validate_inputs,
    validate_result,
)


class RevisionInputRequired(contracts.MissingMaterials):
    """Revision scope needs a user answer, rather than an execution retry."""


def _requested_issue_count(request):
    number = r'(\d+|[一二三四五六七八九十两]+)'
    qualifier = r'(?:最重要的?|最关键的?|重要的?|关键的?|前)?'
    match = re.search(
        r'(?:只|仅)(?:针对|处理|落实|修改|修订|解决|采用|选出|挑选|改)?\s*' + qualifier + r'\s*' + number
        + r'\s*(?:条|个|项)\s*' + qualifier
        + r'(?:审稿意见|问题|意见|建议|(?=[，,。；;.!！?？\s]*$))', request,
    ) or re.search(r'(?:前|最重要的?|最关键的?)\s*' + number + r'\s*(?:条|个|项)(?:审稿意见|问题|意见|建议)?', request)
    if match:
        value = match[1]
        if value.isdecimal():
            return int(value)
        digits = dict(zip('一二三四五六七八九两', [1, 2, 3, 4, 5, 6, 7, 8, 9, 2]))
        if '十' in value:
            left, right = value.split('十', 1)
            return digits.get(left, 1) * 10 + digits.get(right, 0)
        return digits.get(value)
    match = re.search(r'\b(?:only\s+(?:the\s+)?|top\s+|first\s+)(?:most important\s+)?(\d+)\s+'
                      r'(?:(?:most\s+)?important\s+)?(?:issues|comments|suggestions)\b', request, re.IGNORECASE)
    return int(match[1]) if match else None


def _selection_has_count(result, count, issue_ids):
    if not isinstance(result, dict) or result.get('intent') != 'review':
        return False
    if result.get('selection') == 'all':
        return set(result) == {'intent', 'selection'} and len(issue_ids) == count
    ids = result.get('issue_ids')
    return (set(result) == {'intent', 'selection', 'issue_ids'} and result.get('selection') == 'subset'
            and isinstance(ids, list) and len(ids) == count
            and all(isinstance(key, str) and key in issue_ids for key in ids) and len(set(ids)) == count)


def resolve_review_issues(request, review, *, workflow_context=None):
    events = current_events()
    identifier = uuid.uuid4().hex
    if events:
        events.begin_stage(identifier, '正在确定修订范围', 7)
    try:
        selected = _resolve_review_issues(request, review, workflow_context)
    except RevisionInputRequired:
        if events:
            events.end_stage(identifier)
        raise
    except Exception as error:
        if events:
            events.end_stage(identifier, error)
        raise
    if events:
        events.end_stage(identifier)
        issues = [issue for issue in (review or {}).get('issues', []) if issue['id'] in selected]
        executable = sum(issue.get('action_type') == 'text_revision' for issue in issues)
        events.progress(8, f'已选择 {executable} 条可执行意见、{len(issues) - executable} 条待补事项'
                        if selected else '按本轮指定要求直接修改稿件')
    return selected


def _resolve_review_issues(request, review, workflow_context):
    """Resolve review use, deriving internal issue IDs from the current report.

    Version compatibility is checked by the material contract; it is not consent
    to use a historical review. No saved or model-generated selection is reused.
    """
    if not request.strip():
        raise RevisionInputRequired('请明确本轮修改要求，或说明按当前审稿意见修订。')
    if review is None:
        return []
    issue_ids = [issue['id'] for issue in review['issues']]
    count = _requested_issue_count(request)
    if count is not None and not 1 <= count <= len(issue_ids):
        raise RevisionInputRequired(f'当前有 {len(issue_ids)} 条审稿意见，请确认本轮要处理的条数。')
    context = workflow_context or {}
    current_review = (context.get('current_step') == 'revise_document'
                      and context.get('previous_completed_step') == 'peer_review'
                      and context.get('review_matches_manuscript') is True)
    confirmation = re.sub(r'[\s，,。.!！]', '', request)
    if current_review and re.fullmatch(r'(?:请)?(?:继续|开始)?(?:修订|修改)(?:论文|稿件|正文)?(?:吧)?|继续', confirmation):
        result = {'intent': 'review', 'selection': 'all'}
    else:
        raw = str(AutoModel(model='llm')(REVISION_SCOPE_INSTRUCTIONS + _json({
            'request': request, 'issues': review['issues'], 'workflow_context': context,
        }))).strip()
        result = _WriterJsonFormatter()(raw)
    if count is not None and not _selection_has_count(result, count, issue_ids):
        # Correct only issue selection, before any manuscript planning or edits.
        raw = str(AutoModel(model='llm')(REVISION_SCOPE_INSTRUCTIONS + _json({
            'request': request, 'issues': review['issues'], 'workflow_context': context,
            'required_count': count, 'previous_selection': result,
            'correction': 'Select exactly required_count distinct current issue IDs by the requested priority. '
                          'Do not select all when it exceeds that count. Return unclear if selection is not possible.',
        }))).strip()
        result = _WriterJsonFormatter()(raw)
        if not _selection_has_count(result, count, issue_ids):
            raise RevisionInputRequired(f'本轮要求只处理 {count} 条意见，尚未取得符合要求的选择；稿件未修改。')
    if not isinstance(result, dict):
        raise ValueError('Invalid revision intent response')
    intent = result.get('intent')
    if intent in {'direct', 'unclear'} and set(result) == {'intent'}:
        if intent == 'direct':
            return []
        raise RevisionInputRequired('请确认是否按当前审稿意见修订，或说明只修改哪些内容、采用哪些意见编号。')
    if intent != 'review' or result.get('selection') not in {'all', 'subset'}:
        raise ValueError('Invalid revision intent response')
    if workflow_context is not None and context.get('review_matches_manuscript') is not True:
        raise RevisionInputRequired('当前审稿与稿件版本不匹配，请先确认使用哪份稿件和审稿报告；本轮未修改正文。')
    if result['selection'] == 'all':
        if set(result) != {'intent', 'selection'}:
            raise ValueError('Invalid revision intent response')
        if not issue_ids:
            raise RevisionInputRequired('当前审稿没有待处理意见，无需按审稿修订；如需其他修改，请说明具体要求。')
        return issue_ids
    if set(result) != {'intent', 'selection', 'issue_ids'}:
        raise ValueError('Invalid revision intent response')
    selected = result['issue_ids']
    if (not isinstance(selected, list) or not selected
            or any(not isinstance(key, str) or key not in issue_ids for key in selected)):
        raise RevisionInputRequired('请选择当前审稿报告中有效的意见编号，选择不能为空。')
    return [key for key in issue_ids if key in selected]


def _operation_ranges(source, plan):
    """Resolve source-coordinate ranges per operation, including move destinations."""
    operations = []
    for item in plan.instructions:
        ranges = []
        scopes = [(item.content_ref, item.target_scope, item.locator_text)]
        if item.modify_type == 'move':
            scopes.append((item.destination_ref, item.destination_scope, item.destination_locator_text))
        for ref, scope, locator in scopes:
            if ref is None or ref.node_id or ref.placeholder_id:
                raise ValueError('Paper edits require Markdown heading references.')
            start, end = ((0, len(source)) if ref.document_root else markdown_section_range(
                source, ref.heading_path, ref.occurrence,
            ))
            if scope == 'section':
                ranges.append((start, end))
                continue
            text = (locate_markdown_paragraph(
                source, locator or '', heading_path=ref.heading_path, occurrence=ref.occurrence,
            ) if scope == 'paragraph' else locator)
            if not text or source[start:end].count(text) != 1:
                raise ValueError('Paper edit locator must match exactly once in its scope.')
            offset = start + source[start:end].index(text)
            ranges.append((offset, offset + len(text)))
        operations.append(ranges)
    return operations


def _within_range(start, end, allowed):
    return any(left <= start and end <= right for left, right in allowed)


def _bounded_replacements(source, allowed, generated):
    """Reject ambiguous/out-of-plan edits before the shared atomic application.

    Bounds refer to the original manuscript, so sort independent replacements from
    bottom to top: changing a heading cannot invalidate a later child reference.
    """
    replacements = StringReplaceSet.model_validate(generated)
    if not replacements.replacements:
        raise ValueError('Writer returned no manuscript edits.')
    located = []
    matched_ranges = []
    for replacement in replacements.replacements:
        ref = replacement.content_ref
        if ref is None or ref.document_root:
            start, end = 0, len(source)
        else:
            if ref.node_id or ref.placeholder_id:
                raise ValueError('Paper replacements require Markdown heading references.')
            start, end = markdown_section_range(source, ref.heading_path, ref.occurrence)
        if source[start:end].count(replacement.old_string) != 1:
            raise ValueError('Replacement text must match exactly once in its original scope.')
        start += source[start:end].index(replacement.old_string)
        end = start + len(replacement.old_string)
        matched_ranges.append((start, end))
        # Writer may include unchanged surrounding text to make a replacement
        # unique. Check what actually changes, not that matching context.
        old, new = replacement.old_string, replacement.new_string
        prefix = 0
        while prefix < min(len(old), len(new)) and old[prefix] == new[prefix]:
            prefix += 1
        old_end, new_end = len(old), len(new)
        while old_end > prefix and new_end > prefix and old[old_end - 1] == new[new_end - 1]:
            old_end -= 1
            new_end -= 1
        changed_start, changed_end = start + prefix, start + old_end
        if not _within_range(changed_start, changed_end, allowed):
            raise ValueError('Writer replacement exceeds the planned modification scope.')
        located.append((changed_start, changed_end, replacement))
    located.sort(key=lambda item: item[0])
    # The shared applier still replaces the full strings, so overlapping matching
    # contexts remain unsafe even when their changed characters are disjoint.
    matched_ranges.sort()
    if any(left[1] > right[0] for left, right in zip(matched_ranges, matched_ranges[1:])):
        raise ValueError('Writer returned overlapping manuscript edits.')
    replacements.replacements = [item[2] for item in reversed(located)]
    return replacements.model_dump(), located


def _validate_review_plan(plan, issues):
    """Keep operation issue IDs inside the selected set; partial coverage is allowed."""
    selected = {issue['id'] for issue in issues}
    for item in plan.instructions:
        ids = item.meta.get('issue_ids')
        if (not isinstance(ids, list) or not ids
                or any(not isinstance(key, str) or key not in selected for key in ids)):
            raise ValueError('Review plan operations must reference selected issue_ids.')


def _validated_ranges(source, plan, pending=None):
    plan = plan.model_copy(deep=True)
    for operation in plan.instructions:
        if operation.modify_type != 'update' or operation.target_scope not in {'fragment', 'paragraph'}:
            continue
        ref = operation.content_ref
        if ref is None or ref.node_id or ref.placeholder_id:
            continue
        try:
            start, end = (0, len(source)) if ref.document_root else markdown_section_range(
                source, ref.heading_path, ref.occurrence,
            )
            operation.locator_text, operation.target_scope = resolve_locator(
                source[start:end], operation.locator_text, operation.target_scope,
            )
        except ValueError:
            pass  # Keep semantic mismatches for the existing single correction attempt.
    failures = {}
    for index, operation in enumerate(plan.instructions):
        try:
            _operation_ranges(source, plan.model_copy(update={'instructions': [operation]}))
        except ValueError as error:
            failures[index] = str(error)
    if failures:
        report_progress(38, '正在纠正失败的原文定位')
        try:
            plan = repair_locators(source, plan, failures)
        except Exception as error:
            if pending is None:
                raise
            pending.append('部分原文定位未能纠正：' + str(error))
    if pending is None:
        return plan, _operation_ranges(source, plan), bool(failures)
    accepted, ranges = [], []
    for operation in plan.instructions:
        try:
            bounds = _operation_ranges(source, plan.model_copy(update={'instructions': [operation]}))[0]
        except ValueError as error:
            pending.append(f"{operation.meta.get('issue_ids', [])} 定位未完成：{error}")
        else:
            accepted.append(operation)
            ranges.append(bounds)
    return plan.model_copy(update={'instructions': accepted}), ranges, bool(failures)


def _validated_changes(source, plan, allowed, generated, *, allow_repair, pending=None,
                       candidates=(), material_identifiers=()):
    if pending is not None:
        accepted = []
        for raw in generated.get('replacements', []):
            try:
                StringReplaceSet.model_validate({'replacements': [raw]})
            except ValueError as error:
                pending.append('修改补丁格式未通过，已保留原文：' + str(error))
            else:
                accepted.append(raw)
        generated = {'replacements': accepted}
    replacements = StringReplaceSet.model_validate(generated)
    failures = {}
    for index, replacement in enumerate(replacements.replacements):
        try:
            _bounded_replacements(source, allowed, {'replacements': [replacement.model_dump()]})
        except ValueError as error:
            failures[index] = str(error)
    if failures and allow_repair:
        report_progress(60, '正在纠正失败的修改补丁')
        try:
            generated = repair_replacements(source, plan, allowed, replacements, failures)
        except Exception as error:
            if pending is None:
                raise
            pending.append('部分修改补丁未能纠正：' + str(error))
    accepted = []
    for raw in generated.get('replacements', []):
        try:
            _bounded_replacements(source, allowed, {'replacements': accepted + [raw]})
        except ValueError as error:
            if pending is None:
                raise
            pending.append('修改补丁未应用，已保留原文：' + str(error))
        else:
            accepted.append(raw)
    if not accepted and pending is not None:
        return {'replacements': []}, []
    replacements = StringReplaceSet.model_validate({'replacements': accepted})
    texts = repair_reader_provenance([item.new_string for item in replacements.replacements], material_identifiers)
    for replacement, text in zip(replacements.replacements, texts):
        replacement.new_string = repair_generated_citation_links(text, candidates)
    return _bounded_replacements(source, allowed, replacements.model_dump())


def _modify_manuscript(prepared: dict, constraints, toolkit, *, editor=None):
    """Apply request edits or review-bound edits with the same public toolkit."""
    source, references = split_references(prepared['manuscript'])
    pending = []

    def finish(body):
        body += references
        if pending:
            report_progress(85, f'已保留可执行的修改，另有 {len(pending)} 项待处理')
        events = current_events()
        if events:
            events.completed_preview(body)
        return {'manuscript': body, 'is_demo': False, '_pending_edits': pending}
    # Do not expand pre-existing insertion directives outside the approved edit.
    _validate_chart_references(source, prepared['charts'])
    _heading_signature(source)
    task = copy.deepcopy(prepared['writing_task'])
    reviewing = task['meta'].get('review_driven', False)
    context_json = _json(prepared['writing_context'])
    issues = prepared['selected_issues'] if reviewing else []
    if reviewing:
        if not issues:
            raise ValueError('Review revision requires selected issues; empty is not all.')
        if not any(issue['action_type'] == 'text_revision' for issue in issues):
            pending.extend(f"{issue['id']}：仍需补充材料或实验，保留为待补事项" for issue in issues)
            return finish(source)
        task['query'] += REVIEW_REVISION_INSTRUCTIONS + _json(issues)
    editor = editor if editor is not None else WriterRevisionToolkit()
    try:
        report_progress(20, '正在定位修改范围')
        located = editor.locate_revision_target(
            writing_task_json=_json(task), writer_document_json=source,
            writing_context_json=context_json,
        )
        report_progress(35, '正在规划稿件修改')
        plan = MarkdownModifyPlan.model_validate(json.loads(editor.generate_modify_plan(
            writing_task_json=_json(task), writer_document_json=source,
            locate_result_json=located, writing_context_json=context_json,
        )))
    except Exception as error:
        pending.append('本轮修改计划未能生成，已保留原稿：' + str(error))
        return finish(source)
    accepted = []
    for operation in plan.instructions:
        try:
            if reviewing:
                _validate_review_plan(plan.model_copy(update={'instructions': [operation]}), issues)
            if operation.visual_instruction:
                raise ValueError('论文文字修订不生成视觉素材')
        except ValueError as error:
            pending.append(f"{operation.meta.get('issue_ids', [])} 修改计划未执行：{error}")
        else:
            accepted.append(operation)
    plan = plan.model_copy(update={'instructions': accepted})
    covered = {key for item in plan.instructions for key in item.meta.get('issue_ids', [])}
    pending.extend(f"{issue['id']}：本轮未生成可执行修改，保留原文"
                   for issue in issues if issue['action_type'] == 'text_revision' and issue['id'] not in covered)
    plan, operation_ranges, repaired = _validated_ranges(source, plan, pending)
    if not plan.title_instruction and not plan.instructions:
        if not pending:
            pending.append('本轮没有可执行的文本修改，已保留原稿')
        return finish(source)
    allowed = [bounds for ranges in operation_ranges for bounds in ranges]
    if plan.title_instruction:
        title = next(re.finditer(r'^#[ \t]+[^\r\n]+', _markdown_prose(source), re.MULTILINE))
        allowed.append(title.span())
    report_progress(50, '正在生成修改内容')
    try:
        generated = json.loads(editor.generate_string_replace_set(
            markdown_document=source, modify_plan_json=plan.model_dump_json(), writing_context_json=context_json))
        _, located_changes = _validated_changes(
            source, plan, allowed, generated, allow_repair=not repaired, pending=pending,
            candidates=prepared['reference_candidates'],
            material_identifiers=prepared.get('material_identifiers', []))
    except Exception as error:
        pending.append('修改内容未能生成，已保留原稿：' + str(error))
        return finish(source)
    if plan.title_instruction and not any(
            start <= title.end() and end >= title.start() for start, end, _ in located_changes):
        pending.append('本轮未生成有效的标题修改，保留原标题')
    if reviewing:
        for issue in issues:
            if issue['action_type'] != 'text_revision' or issue['id'] not in covered:
                continue
            bounds = [bounds for item, ranges in zip(plan.instructions, operation_ranges)
                      if issue['id'] in item.meta.get('issue_ids', []) for bounds in ranges]
            if not any(_within_range(start, end, bounds) for start, end, _ in located_changes):
                pending.append(f"{issue['id']}：本轮未产生有效修改，保留原文")
    report_progress(80, '正在逐项应用可执行的修改')
    body = source
    for start, end, replacement in reversed(located_changes):
        raw = replacement.model_dump()
        try:
            if plan.title_instruction and start <= title.end() and end >= title.start():
                if (not title.start() <= start <= end <= title.end()
                        or replacement.new_string.count('\n') != replacement.old_string.count('\n')
                        or replacement.new_string.count('\r') != replacement.old_string.count('\r')):
                    raise ValueError('A paper title change must stay within its original heading line.')
            # Each patch retains the original scope/source checks; one failed patch cannot roll back others.
            _validate_reference_markers(raw['new_string'], prepared['reference_candidates'], raw['old_string'])
            _validate_chart_references(raw['new_string'], prepared['charts'])
            applied = json.loads(editor.apply_string_replace(
                markdown_document=body, string_replace_set_json=_json({'replacements': [raw]}),
                writing_context_json=context_json))
            if not applied['string_replace_result']['success']:
                raise ValueError('Writer 未能应用此项修改')
            candidate = _materialize_chart_insertions(applied['revised_document'], prepared['charts'])
            _validate_manuscript(candidate + references, None, prepared['charts'], constraints, toolkit, context_json,
                                 candidates=prepared['reference_candidates'], original=body + references,
                                 check_quality=False)
        except Exception as error:
            pending.append('修改未应用，已保留对应原文：' + str(error))
        else:
            body = candidate
    if body == source and not pending:
        pending.append('本轮未产生有效文字修改，已保留原稿')
    return finish(body)


def run(inputs):
    """Revise a manuscript from the current request or selected current review issues."""
    report_progress(10, '正在准备修订材料')
    toolkit = WriterCreateToolkit()
    prepared = prepare_writer_context(inputs, toolkit=toolkit)
    prepared['writing_task']['constraints']['visual_policy'] = {'allow_visuals': False}
    output = _modify_manuscript(prepared, inputs['constraints'], toolkit)
    pending = output.pop('_pending_edits', [])
    from ..publication import publish_citations, resolve_chart_sources
    from .. import peer_review
    from ..review.schema import ReviewResult, ReviewIssue
    # Check the same canonical Markdown that publication will deliver.
    body = publish_citations(
        output['manuscript'], inputs.get('papers') or [],
        paper_language=inputs['constraints']['paper_language'])
    body = resolve_chart_sources(body, prepared['charts'])
    review_inputs = {key: copy.deepcopy(inputs.get(key)) for key in contracts.FIELDS['peer_review']}
    review_inputs.update(topic=inputs['topic'], manuscript=body,
                         request='核对修订后稿件的五类事实，特别检查改动是否引入新错误；不得把文字修改等同于事实已验证。')
    report_progress(88, '正在核对修订后的事实')
    try:
        fresh = peer_review.run(review_inputs)
    except Exception as error:
        pending.append('修订稿已保留；修订后复核未完成：' + str(error))
        fresh = peer_review.build_output(ReviewResult(
            summary='本轮已交付可执行的修订；其余事项仍待处理，未声称通过复核。',
            issues=[], limitations=pending), review_inputs)
    reviewed = ReviewResult.model_validate({
        **{key: fresh['review_data'][key] for key in ReviewResult.model_fields if key != 'issues'},
        'issues': [{key: issue[key] for key in ReviewIssue.model_fields}
                   for issue in fresh['review_data']['issues']],
    })
    reviewed.limitations.extend('本轮未完成：' + item for item in pending)
    # Missing experiments/materials cannot be supplied by editing prose. Keep them
    # visible even if their old quotation disappeared from the revised manuscript.
    previous = inputs.get('review_data') or {}
    for issue in previous.get('issues', []):
        if issue['action_type'] == 'text_revision':
            continue
        if any(item.problem == issue['problem'] for item in reviewed.issues):
            continue
        pending = {key: issue[key] for key in ReviewIssue.model_fields}
        if pending['quote'] and pending['quote'] not in body:
            pending['quote'] = None
        pending['problem'] = f"原 {issue['id']} 待补事项，尚未独立核实：" + pending['problem']
        reviewed.issues.append(ReviewIssue.model_validate(pending))
    fresh = peer_review.build_output(reviewed, review_inputs, fact_checks=fresh['review_data']['fact_checks'])
    return {**fresh, 'manuscript': body}


def retain_manuscript(inputs):
    """Deliver the existing manuscript when an optional workflow revision cannot be applied."""
    from ..publication import publish_citations, resolve_chart_sources, figure_catalog
    from ..review.schema import SavedReview, text_digest

    body = inputs.get('manuscript')
    contracts.text(body, 'manuscript')
    body = publish_citations(body, inputs.get('papers') or [],
                             paper_language=(inputs.get('constraints') or {}).get('paper_language'))
    body = resolve_chart_sources(body, figure_catalog(inputs.get('charts') or [], inputs.get('media_assets')))
    review, data = inputs.get('review'), copy.deepcopy(inputs.get('review_data'))
    if (not review or not data or data.get('manuscript_sha256') != text_digest(body)
            or data.get('review_sha256') != text_digest(review)):
        review = '# 稿件记录\n\n沿用当前稿件。\n'
        data = SavedReview(summary='沿用当前稿件。', issues=[], limitations=['本轮沿用稿件，新增核对待执行。'],
                           manuscript_sha256=text_digest(body), review_sha256=text_digest(review)).model_dump()
    return dict(manuscript=body, review=review, review_data=data, is_demo=False)
