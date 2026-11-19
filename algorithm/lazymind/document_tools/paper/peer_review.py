"""Read-only academic review; storage and workflow routing belong to the caller."""
import json
import hashlib
import re
import time
import uuid
from contextlib import contextmanager
from threading import Event, Thread
from copy import deepcopy

from lazyllm import AutoModel, LOG
from lazyllm.tools.writer.tools.base import WriterToolBase
from pydantic import ValidationError

from .model_response import normalize_response
from .contracts import validate_input
from .fact_checks import (CATEGORY_LABELS as FACT_LABELS, STATUS_LABELS, NUMERIC, OPERATIONS,
                          _pointer, _quantity_token, check_facts,
                          literature_coverage, literature_input, reconcile_precision_advice, review_issues)
from .prompts.peer_review_prompt import (JSON_STRUCTURE_PROMPT, LITERATURE_PROMPT, REVIEW_PROMPT, REPAIR_PROMPT)
from .review.schema import (FactCheck, FactClaim, LiteratureReadings, RepairedIssue, ReviewIssue,
                            ReviewRepair, ReviewResult, SavedReview, text_digest)

SEVERITY_LABELS = {'major': '重要问题', 'minor': '一般问题', 'suggestion': '建议'}
CATEGORY_LABELS = {'method': '方法', 'evidence': '证据', 'analysis': '分析',
                   'structure': '结构', 'language': '语言', 'citation': '引用'}
ACTION_LABELS = {'text_revision': '可直接修改文字', 'needs_material': '需要作者补充材料',
                 'needs_experiment': '需要补充实验，不能用生成文字代替'}
MAX_REPAIR_CHARS = 24000


def log_event(event, **metadata):
    # Use the service logger: standard-library INFO records are not emitted by
    # the chat container's logging configuration. Never pass payloads here.
    LOG.info('paper_review ' + event + ' ' + json.dumps(metadata, ensure_ascii=False))


@contextmanager
def stage(name, trace_id, *, heartbeat=False, progress=None, **metadata):
    started = time.monotonic()
    from .runtime_events import current_events
    events = current_events()
    labels = {'input_validation': (10, '正在读取并校验稿件'),
              'model_response': (35, '正在生成审稿意见'), 'json_parse': (70, '正在解析审稿结果'),
              'schema_and_quote_validation': (80, '正在核验意见与原文引用'),
              'targeted_repair': (85, '正在修正审稿意见'), 'report_rendering': (90, '正在整理审稿报告')}
    stage_id = uuid.uuid4().hex if events is not None and name in labels else None
    if stage_id:
        percent, title = labels[name]
        events.begin_stage(stage_id, title, percent)
    log_event('stage_start', trace_id=trace_id, stage=name, **metadata)
    done = Event()

    def waiting():
        while not done.wait(30):
            log_event('stage_waiting', trace_id=trace_id, stage=name,
                      elapsed_s=round(time.monotonic() - started, 3),
                      **(progress() if progress else {}))

    worker = Thread(target=waiting, daemon=True) if heartbeat else None
    if worker:
        worker.start()
    try:
        yield
    except Exception as error:
        if stage_id:
            events.end_stage(stage_id, error)
        causes, seen, cause = [], set(), error
        while cause is not None and id(cause) not in seen and len(causes) < 8:
            seen.add(id(cause))
            causes.append(type(cause).__name__)
            cause = cause.__cause__ or cause.__context__
        log_event('stage_failed', trace_id=trace_id, stage=name,
                  elapsed_s=round(time.monotonic() - started, 3),
                  error_type=type(error).__name__, cause_types=causes)
        raise
    else:
        if stage_id:
            events.end_stage(stage_id)
        log_event('stage_completed', trace_id=trace_id, stage=name,
                  elapsed_s=round(time.monotonic() - started, 3))
    finally:
        done.set()
        if worker:
            worker.join()


def review_scope(result: ReviewResult, inputs: dict) -> tuple[list[str], list[str]]:
    supplied = ['稿件']
    limitations = list(result.limitations)
    coverage_labels = {'full_text': '文献全文', 'abstract': '文献摘要',
                       'excerpt': '文献文本（节选或覆盖不明）', 'metadata': '文献题录'}
    for index, paper in enumerate(inputs.get('papers') or [], 1):
        coverage = literature_coverage(paper)
        supplied.append(coverage_labels[coverage])
        label = paper.get('id') or paper.get('title') or str(index)
        if coverage == 'abstract':
            limitations.append(f'文献 {label} 仅提供摘要；支持范围限于摘要，不能声称全文已核实。')
        elif coverage == 'excerpt':
            limitations.append(f'文献 {label} 为节选、截断或全文覆盖不明的文本；不能声明全文支持已核实。')
        elif coverage == 'metadata':
            limitations.append(f'文献 {label} 仅有题录，未提供可核读的正文或摘要，无法核验具体主张支持。')
    limitations.append('仅审查成稿及所提供的相关文献和图表；未联网检索、查重或复现实验，不构成期刊录用判断。')
    limitations.append('事实核对仅覆盖列出的原文与来源；主审模型自报不等于核实。独立全文核读会标明方法与范围，仍不构成确定性证明。'
                       '论证与语言建议不阻断交付；未核实事项仍需保留，不能标记为已解决。')
    return list(dict.fromkeys(supplied)), list(dict.fromkeys(limitations))


def render_review(result: ReviewResult, inputs: dict, coverage=None, issues=None) -> str:
    """Render the structured result for the existing Markdown editor."""
    coverage = check_facts(result, inputs) if coverage is None else coverage
    if issues is None:
        audit = []
        issues = review_issues(result, coverage, audit=audit)
        result = result.model_copy(update={'limitations': result.limitations + audit})
    lines = [f'# 模拟审稿 · {inputs["topic"]}', '', '## 总体评价', result.summary, '', '## 事实核对']
    active = [group for group in coverage if group.status != 'not_applicable' or group.checks]
    if active:
        lines.extend(['状态仅覆盖列出的成稿内部一致性与文献支持核对，不代表已复现实验。', '',
                      '| 类型 | 状态 | 证据位置与范围 |', '| --- | --- | --- |'])
    else:
        lines.append('本轮未单列可独立核对的事实项。')
    for group in active:
        locations = '、'.join(group.evidence_locations) or group.reason
        lines.append(f'| {FACT_LABELS[group.category]} | {STATUS_LABELS[group.status]} | {locations} |')
    for group in coverage:
        for check in group.checks:
            lines.extend(['', f'### {FACT_LABELS[group.category]} · {STATUS_LABELS[check.status]}',
                          f'位置：{check.location}', '', '原文：',
                          *['> ' + line for line in check.quote.splitlines()], '', check.calculation, '',
                          '证据位置：' + '、'.join(check.evidence_locations), '后续动作：' + check.suggestion])
    lines.extend(['', '## 逐条意见'])
    for disposition, label in (('fact', '事实问题'), ('unverified', '待核实事项'), ('advice', '论证与语言建议（不阻断交付）')):
        selected = [(index, issue) for index, (issue, kind) in enumerate(issues, 1) if kind == disposition]
        if not selected:
            continue
        lines.extend(['', '### ' + label])
        for index, issue in selected:
            lines.extend([
                '', f'#### R{index}｜{SEVERITY_LABELS[issue.severity]}｜{CATEGORY_LABELS[issue.category]}',
                f'位置：{issue.location}', '',
            ])
            if issue.quote is not None:
                lines.extend(['原文：', *['> ' + line for line in issue.quote.splitlines()], ''])
            else:
                lines.extend(['原文：不适用（全文或缺失内容建议）', ''])
            lines.extend([f'问题与依据：{issue.problem}', '', f'修改建议：{issue.suggestion}', '',
                          f'处理方式：{ACTION_LABELS[issue.action_type]}'])
    if not issues:
        lines.extend(['', '本次检查未识别出明确问题；这不代表论文已达到发表标准。'])

    return '\n'.join(lines) + '\n'


class ReviewQuoteError(ValueError):
    """The model's excerpt could not be located, not a missing upload."""


def locate_quotes(result: ReviewResult, manuscript: str) -> ReviewResult:
    result = result.model_copy(deep=True)
    invalid = []
    for index, issue in enumerate(result.issues, 1):
        if issue.quote is None or issue.quote in manuscript:
            continue
        # Only whitespace runs may differ. Never fuzzy-match changed words or numbers.
        pattern = r'\s+'.join(re.escape(part) for part in issue.quote.split())
        match = re.search(pattern, manuscript)
        if match:
            issue.quote = match.group(0)
        else:
            invalid.append(f'第 {index} 条（{issue.location}）：{issue.quote[:160]}')
    if invalid:
        raise ReviewQuoteError('论文已收到，但模型生成的以下摘录在正文中找不到：\n'
                               + '\n'.join(invalid) + '\n这是审稿摘录校验失败，不是缺少论文文件。')
    return result


def build_output(result: ReviewResult, inputs: dict, *, fact_checks=None, readings=None) -> dict:
    """One model result supplies both formats, with matching issue IDs and source digests."""
    result = locate_quotes(result, inputs['manuscript'])
    if fact_checks is not None:
        # Internal re-render of the same fresh review; source/quote anchors are checked again below.
        readings = []
        for raw in fact_checks:
            check = FactCheck.model_validate(raw)
            for item in check.checks:
                if item.reading is not None:
                    index = item.reading.claim_index
                    if not 0 <= index < len(result.fact_claims) or result.fact_claims[index].quote != item.quote:
                        raise ValueError('文献核读结果与当前事实项不匹配，不能复用。')
                    readings.append(item.reading)
    coverage = check_facts(result, inputs, readings)
    result = reconcile_precision_advice(result, coverage)
    audit = []
    issues = review_issues(result, coverage, audit=audit)
    result = result.model_copy(update={'limitations': result.limitations + audit})
    review = render_review(result, inputs, coverage, issues)
    data = SavedReview.model_validate({
        **result.model_dump(),
        'limitations': review_scope(result, inputs)[1],
        'issues': [dict(issue.model_dump(), id=f'R{index}', disposition=disposition)
                   for index, (issue, disposition) in enumerate(issues, 1)],
        'fact_checks': [group.model_dump() for group in coverage],
        'manuscript_sha256': text_digest(inputs['manuscript']), 'review_sha256': text_digest(review),
    })
    return {'review': review, 'review_data': data.model_dump(), 'is_demo': False}


def fact_claim_errors(claim, inputs=None):
    """Generation-time contract checks, separate from SavedReview's compatible reader."""
    errors, snippets = {}, []
    if claim.operation not in OPERATIONS[claim.category]:
        errors.update(category='类别与运算不匹配。', operation='类别与运算不匹配。')
    arity = {'value': 1, 'denominator': 1, 'difference': 2, 'ratio': 2, 'percentage': 2,
             'percentage_points': 2, 'relative_change': 2, 'direction': 2, 'figure_value': 2}
    if claim.operation in arity and len(claim.sources) != arity[claim.operation]:
        errors.update(operation='运算与来源数量不匹配。', sources='需要明确的来源数量。')
    numeric = claim.operation in NUMERIC | {'figure_value'}
    if numeric:
        try:
            _quantity_token(claim.asserted_text)
        except ValueError as error:
            errors['asserted_text'] = str(error)
    for index, source in enumerate(claim.sources):
        if numeric and source.value_text is not None:
            try:
                _quantity_token(source.value_text)
            except ValueError as error:
                errors[f'sources.{index}.value_text'] = str(error)
        # A canonical scalar label is already the exact supplied source, not a new passage.
        if inputs is not None and claim.operation == 'status' and source.path.startswith('/result_meta/'):
            try:
                actual = _pointer(inputs, source.path)
            except ValueError:
                continue
            if isinstance(actual, str) and len(actual) <= 100 and source.quote != actual:
                errors[f'sources.{index}.quote'] = '来源标量标签与摘录格式不一致；必须复制实际字段值。'
                snippets.append({'path': source.path, 'text': actual})
    return errors, snippets


def repair_targets(candidate: dict, manuscript: str, inputs=None) -> dict:
    """Collect invalid fields/issues without losing the valid part of the review."""
    if not isinstance(candidate, dict) or not isinstance(candidate.get('issues'), list):
        raise ValueError('审稿结果不是完整 JSON 对象或缺少意见列表，已停止，未自动重新审稿。')
    targets = {}
    try:
        ReviewResult.model_validate(candidate)
    except ValidationError as error:
        for item in error.errors(include_input=False, include_url=False):
            loc = item['loc']
            path = f'issues.{loc[1]}' if loc[0] == 'issues' else loc[0]
            if path not in ('summary', 'limitations', 'fact_claims') and not path.startswith('issues.'):
                raise ValueError('审稿结果含未知顶层字段，已停止，未自动重新审稿。') from error
            value = candidate['issues'][loc[1]] if loc[0] == 'issues' else candidate.get(path)
            target = targets.setdefault(path, {'value': value, 'errors': []})
            target['errors'].append({'field': list(loc), 'message': item['msg']})
    for index, issue in enumerate(candidate['issues']):
        path = f'issues.{index}'
        if path in targets:
            continue
        try:
            locate_quotes(ReviewResult(summary='检查摘录', issues=[ReviewIssue.model_validate(issue)],
                                       limitations=[]), manuscript)
        except ReviewQuoteError as error:
            targets[path] = {'value': issue, 'errors': [str(error)]}
    if isinstance(candidate.get('fact_claims'), list):
        invalid = []
        for index, raw in enumerate(candidate['fact_claims']):
            try:
                claim = FactClaim.model_validate(raw)
            except ValidationError as error:
                errors = {'.'.join(map(str, item['loc'])): item['msg']
                          for item in error.errors(include_input=False, include_url=False)}
                snippets = []
            else:
                errors, snippets = fact_claim_errors(claim, inputs)
            if errors:
                invalid.append({'index': index, 'value': raw, 'errors': errors, 'source_snippets': snippets})
        if invalid:
            targets['fact_claims'] = {'value': [item['value'] for item in invalid],
                                      'indices': [item['index'] for item in invalid],
                                      'errors': [item['errors'] for item in invalid],
                                      'source_snippets': [item['source_snippets'] for item in invalid]}
    return targets


def quarantine_invalid_facts(candidate):
    candidate = deepcopy(candidate)
    facts = []
    for index, raw in enumerate(candidate.get('fact_claims', [])):
        try:
            FactClaim.model_validate(raw)
        except ValidationError as error:
            note = {'index': index, 'proposal': raw, 'reason': str(error)}
            candidate.setdefault('limitations', []).append('待核验事实提案（未进入事实修订）：' + json.dumps(
                note, ensure_ascii=False))
        else:
            facts.append(raw)
    candidate['fact_claims'] = facts
    return candidate


def apply_repair(candidate: dict, targets: dict, repair: ReviewRepair, manuscript: str,
                 allowed_lines: set[int] | None = None) -> ReviewResult:
    if set(repair.replacements) != set(targets):
        raise ValueError('审稿局部修复必须且只能返回指定字段，不能改动有效意见。')
    merged = deepcopy(candidate)
    lines = manuscript.splitlines(keepends=True)
    for path, value in repair.replacements.items():
        if path.startswith('issues.'):
            original = targets[path].get('value', {})
            original = original if isinstance(original, dict) else {}
            line_quote = isinstance(value, dict) and isinstance(value.get('quote'), dict)
            issue = (RepairedIssue if line_quote else ReviewIssue).model_validate(value)
            value = issue.model_dump()
            if issue.quote is None and original.get('quote') is not None:
                raise ValueError('不能将需要核验的原文引用改为 null。')
            if line_quote:
                start, end = issue.quote.start_line, issue.quote.end_line
                if not 1 <= start <= end <= len(lines):
                    raise ValueError('审稿修复的原文行号越界或倒置，不能发布意见。')
                if allowed_lines is not None and not set(range(start, end + 1)) <= allowed_lines:
                    raise ValueError('审稿修复引用了未提供的原文行或跨越不连续片段。')
                value['quote'] = ''.join(lines[start - 1:end]).rstrip('\r\n')
            elif value['quote'] != original.get('quote'):
                raise ValueError('格式修复不能改写原文引用；引用修复必须返回原文行号。')
            if 'errors' in targets[path]:
                invalid_fields = {error['field'][2] for error in targets[path]['errors']
                                  if isinstance(error, dict) and len(error.get('field', [])) > 2}
                for field in ReviewIssue.model_fields.keys() - {'quote'} - invalid_fields:
                    if field in original and value[field] != original[field]:
                        raise ValueError('局部修复不能改变该条意见中已经有效的字段。')
            merged['issues'][int(path.split('.')[1])] = value
        else:
            if path == 'fact_claims' and 'indices' in targets[path]:
                target = targets[path]
                if not isinstance(value, list) or len(value) != len(target['indices']):
                    raise ValueError('事实格式修复必须且只能返回无效项，不能增删。')
                for offset, (index, raw) in enumerate(zip(target['indices'], value)):
                    old = candidate[path][index]
                    if not isinstance(old, dict):
                        continue
                    changed = set(target['errors'][offset])
                    for field in FactClaim.model_fields.keys() - {'sources'}:
                        if field not in changed and isinstance(raw, dict) and raw.get(field) != old.get(field):
                            raise ValueError('不能修改事实项中已有效的字段。')
                    if isinstance(raw, dict) and isinstance(raw.get('sources'), list):
                        snippets = target.get('source_snippets', [[]] * len(value))[offset]
                        if 'sources' in changed:
                            for source in raw['sources']:
                                if not any(all(source.get(key, {} if key == 'where' else None)
                                               == prior.get(key, {} if key == 'where' else None)
                                               for key in ('path', 'quote', 'column', 'where'))
                                           for prior in old['sources']):
                                    raise ValueError('格式修复不能另选来源或改变分组。')
                        if 'sources' not in changed and len(raw['sources']) != len(old['sources']):
                            raise ValueError('不能改变有效来源数量。')
                        for position, source in enumerate(raw['sources']):
                            if position >= len(old['sources']):
                                if source not in old['sources']:
                                    raise ValueError('格式修复不能另选来源或补入证据。')
                                continue
                            before = old['sources'][position]
                            for key in ('path', 'quote', 'value_text', 'column', 'where'):
                                if 'sources' not in changed and f'sources.{position}.{key}' not in changed:
                                    if source.get(key, {} if key == 'where' else None) != before.get(
                                            key, {} if key == 'where' else None):
                                        raise ValueError('格式修复不能改动有效来源字段。')
                                elif key == 'quote' and f'sources.{position}.quote' in changed:
                                    if not any(source.get('path') == item['path'] and source.get('quote') == item['text']
                                               for item in snippets):
                                        raise ValueError('标签格式修复只能复制实际来源字段值。')
                                elif key == 'path' and source.get(key) not in {s['path'] for s in old['sources']}:
                                    raise ValueError('格式修复不能另选来源路径。')
                    try:
                        replacement = FactClaim.model_validate(raw)
                        remaining, _ = fact_claim_errors(replacement)
                        if remaining:
                            raise ValueError('事实项仍违反生成契约。')
                    except (ValueError, TypeError):
                        merged.setdefault('limitations', []).append(f'第 {index + 1} 个事实项格式修复未完成，保持待核实。')
                        continue
                    merged[path][index] = replacement.model_dump()
                continue
            if path == 'fact_claims' and isinstance(candidate.get(path), list):
                if not isinstance(value, list) or len(value) != len(candidate[path]):
                    raise ValueError('局部修复不能删除或增加事实核对项。')
                for index, original in enumerate(candidate[path]):
                    try:
                        FactClaim.model_validate(original)
                    except ValidationError:
                        continue
                    if value[index] != original:
                        raise ValueError('局部修复不能改变已经有效的事实核对项。')
            merged[path] = value
    if any('indices' in target for target in targets.values()):
        merged = quarantine_invalid_facts(merged)
    return ReviewResult.model_validate(merged)


def _needs_source(target, manuscript):
    issue = target.get('value')
    if not isinstance(issue, dict):
        return False
    quote = issue.get('quote')
    if 'quote' not in issue:
        return True
    if quote is None:
        return False
    if not isinstance(quote, str) or not quote.strip():
        return True
    pattern = r'\s+'.join(re.escape(part) for part in quote.split())
    return quote not in manuscript and re.search(pattern, manuscript) is None


def repair_request(inputs: dict, targets: dict) -> tuple[str, set[int]]:
    """Format-only repairs need no manuscript; quote repairs get selected original lines."""
    from .review.context import source_lines

    context = {key: inputs[key] for key in ('topic', 'request') if key in inputs}
    sources = [target for path, target in targets.items() if path.startswith('issues.')
               and _needs_source(target, inputs['manuscript'])]
    payload = {'inputs': context, 'targets': targets,
               'quote_mode': 'source_lines' if sources else 'preserve',
               'issue_schema': (RepairedIssue if sources else ReviewIssue).model_json_schema()}
    if 'fact_claims' in targets:
        payload['fact_claim_schema'] = ReviewResult.model_json_schema()['properties']['fact_claims']
        payload['fact_claim_definitions'] = ReviewResult.model_json_schema().get('$defs', {})

    def render():
        return REPAIR_PROMPT + '\n\nInput JSON:\n' + json.dumps(payload, ensure_ascii=False)

    allowed = set()
    if sources:
        budget = min(8000, MAX_REPAIR_CHARS - len(render()) - 32)
        selections = []
        for target in sources:
            text, indices = source_lines(inputs['manuscript'], target['value'], budget // len(sources))
            selections.append(text)
            allowed.update(indices)
        context['manuscript_lines'] = '\n'.join(selections)
    prompt = render()
    if len(prompt) > MAX_REPAIR_CHARS:
        raise ValueError('审稿局部修复内容超过限制，已停止；请缩小审查范围后重试。')
    return prompt, allowed


def repair_prompt(inputs: dict, targets: dict) -> str:
    return repair_request(inputs, targets)[0]


def repair_result(reviewer, inputs, candidate, targets, trace_id):
    """Repair each target once, preserving valid opinions and disclosing unverified ones."""
    candidate, targets = deepcopy(candidate), deepcopy(targets)
    contract_target = targets.get('fact_claims', {})
    if 'indices' in contract_target:
        try:
            prompt, allowed = repair_request(inputs, {'fact_claims': contract_target})
            answer = ReviewRepair.model_validate(call_once(reviewer, prompt, ReviewRepair, trace_id))
            trial = {'summary': '校验事实格式修复', 'issues': [], 'limitations': [],
                     'fact_claims': candidate['fact_claims']}
            checked = apply_repair(trial, {'fact_claims': contract_target}, answer, inputs['manuscript'])
            candidate['fact_claims'] = [item.model_dump() for item in checked.fact_claims]
            candidate.setdefault('limitations', []).extend(checked.limitations)
        except Exception as error:
            log_event('fact_contract_repair_unavailable', trace_id=trace_id, error_type=type(error).__name__)
            candidate.setdefault('limitations', []).append('事实项的一次格式修复未完成；原项保留为待核实，不代表材料缺失。')
        targets.pop('fact_claims')
        candidate = quarantine_invalid_facts(candidate)
    replacements, unresolved = {}, {}
    for path, target in targets.items():
        batch = {path: target}
        try:
            prompt, allowed = repair_request(inputs, batch)
        except ValueError as error:
            if not path.startswith('issues.'):
                raise
            unresolved[path] = str(error)
            continue
        answer = ReviewRepair.model_validate(call_once(reviewer, prompt, ReviewRepair, trace_id))
        # Check each replacement without requiring other, unrepaired fields to validate yet.
        if set(answer.replacements) != {path}:
            raise ValueError('审稿局部修复必须且只能返回指定字段，不能改动有效意见。')
        if path.startswith('issues.'):
            trial = {'summary': '校验局部修复', 'issues': [target['value']], 'limitations': []}
            try:
                checked = apply_repair(trial, {'issues.0': target}, ReviewRepair(
                    replacements={'issues.0': answer.replacements[path]}), inputs['manuscript'], allowed)
                locate_quotes(checked, inputs['manuscript'])
            except ValueError as error:
                unresolved[path] = str(error)
                continue
            replacements[path] = checked.issues[0].model_dump()
        else:
            if path == 'fact_claims':
                trial = {'summary': '校验事实项格式修复', 'issues': [], 'limitations': [],
                         'fact_claims': target['value']}
                apply_repair(trial, batch, answer, inputs['manuscript'])
            replacements[path] = answer.replacements[path]
    merged = deepcopy(candidate)
    for path, value in replacements.items():
        if path.startswith('issues.'):
            merged['issues'][int(path.split('.')[1])] = value
        else:
            merged[path] = value
    notes = []
    for path, reason in unresolved.items():
        issue = targets[path]['value']
        notes.append('待核验意见（未进入可自动修订意见）：' + json.dumps(
            {'original_issue': issue, 'reason': reason}, ensure_ascii=False))
    merged['issues'] = [issue for index, issue in enumerate(merged['issues'])
                        if f'issues.{index}' not in unresolved]
    result = ReviewResult.model_validate(merged)
    result.limitations.extend(notes)
    log_event('repair_result', trace_id=trace_id, repaired=len(replacements), unverified=len(unresolved))
    return locate_quotes(result, inputs['manuscript'])


def _unique_fields(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('审稿 JSON 含重复字段，不能确定应保留的内容。')
        result[key] = value
    return result


def parse_response(response):
    """Never use structural JSON guessing: one damaged issue must not become two."""
    response = normalize_response(response).body
    if not isinstance(response, str):
        return response
    text = WriterToolBase._strip_leading_think_blocks(response).strip()
    if text.startswith('```') and text.endswith('```'):
        text = re.sub(r'^```(?:json)?\s*\n', '', text, count=1).removesuffix('```').strip()
    try:
        return json.loads(text, object_pairs_hook=_unique_fields)
    except json.JSONDecodeError as error:
        if error.msg == 'Extra data' and text.startswith('{'):
            value, end = json.JSONDecoder(object_pairs_hook=_unique_fields).raw_decode(text)
            if isinstance(value, dict) and not re.search(r'[{}\[\]\":,<>]', text[end:]):
                return value
        # Repair only a repeated field tag: "category":"analysis","category":"location":...
        # Tokenizing complete strings keeps quoted manuscript content untouched.
        tokens = list(re.finditer(r'"(?:\\.|[^"\\])*"|[{}\[\]:,]|[^\s{}\[\]:,"]+', text))
        values = [token.group() for token in tokens]
        fields = {json.dumps(key) for key in ReviewIssue.model_fields}
        removals = []
        for i in range(4, len(tokens) - 3):
            if (values[i] in fields and values[i] == values[i - 4]
                    and values[i - 3] == ':' and values[i - 2].startswith('"') and values[i - 1] == ','
                    and values[i + 1] == ':' and values[i + 2] in fields
                    and values[i + 2] != values[i] and values[i + 3] == ':'):
                removals.append((tokens[i].start(), tokens[i + 2].start()))
        for start, end in reversed(removals):
            text = text[:start] + text[end:]
        return json.loads(text, object_pairs_hook=_unique_fields)


def _json_scalars(text):
    """Only JSON punctuation may change in a structural repair; content and order cannot."""
    pattern = r'"(?:\\.|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][-+]?\d+)?|true|false|null|[{}\[\]:,]|\s+'
    values, end = [], 0
    for match in re.finditer(pattern, text):
        if match.start() != end:
            raise ValueError('损坏响应含不可恢复的非 JSON 内容。')
        token, end = match.group(), match.end()
        if token.strip() and token not in '{}[]:,':
            scalar = json.loads(token)
            values.append((type(scalar).__name__, scalar))
    if end != len(text):
        raise ValueError('损坏响应含不完整 JSON 字符串，不能猜测补全。')
    return values


def call_once(reviewer: WriterToolBase, prompt: str, schema, trace_id=None, *, allow_structure_repair=True):
    # Writer's convenience call retries the whole task; F6 owns one targeted repair instead.
    trace_id = trace_id or uuid.uuid4().hex
    started, received = time.monotonic(), False
    counts = {'text_chars': 0, 'think_chars': 0, 'deltas': 0}
    last_delta = None
    from .runtime_events import StructuredPreview

    def render_preview(value):
        # Partial review objects remain previews; strict quote/schema checks follow.
        parts = ['# 模拟审稿', '', '> 生成中的预览，尚未完成意见与原文校验。']
        if isinstance(value.get('summary'), str):
            parts.extend(['', '## 总体评价', value['summary']])
        for issue in value.get('issues', []) if isinstance(value.get('issues'), list) else []:
            if isinstance(issue, dict):
                for key, label in (('problem', '问题'), ('suggestion', '修改建议')):
                    if isinstance(issue.get(key), str):
                        parts.extend(['', f'### {label}', issue[key]])
        return '\n'.join(parts) if len(parts) > 3 else ''

    preview = StructuredPreview(render_preview) if schema is ReviewResult else None

    def progress():
        return {**counts, 'idle_s': round(time.monotonic() - last_delta, 3) if last_delta else None}

    def on_event(event):
        nonlocal received, last_delta
        if not isinstance(event, dict):
            return
        if preview is not None:
            preview(event)
        if event.get('tag') == 'runtime_event':
            runtime = event.get('runtime_event', {})
            data = runtime.get('data', {})
            fields = {key: data[key] for key in (
                'model_call_id', 'retry_index', 'max_attempts', 'delay_ms',
                'attempt_count', 'kind', 'finish', 'has_semantic_output', 'duration_ms',
            ) if key in data and isinstance(data[key], (str, int, float, bool))}
            log_event('model_runtime', trace_id=trace_id, runtime_type=runtime.get('type'), **fields)
        elif event.get('tag') in {'text', 'think'} and isinstance(event.get('delta'), str) and event['delta']:
            counts[event['tag'] + '_chars'] += len(event['delta'])
            counts['deltas'] += 1
            last_delta = time.monotonic()
            if not received:
                received = True
                log_event('model_first_delta', trace_id=trace_id, schema=schema.__name__,
                          delta_type=event['tag'], elapsed_s=round(time.monotonic() - started, 3))

    # Receive provider deltas without publishing unfinished JSON or reasoning.
    # This uses Writer's streaming transport; F6 still owns strict JSON parsing.
    model = reviewer._build_structured_llm(
        reviewer._structured_output_prompt(schema), stream_output={'_stream_sink': on_event}, apply_formatter=False)
    model_name = getattr(model, '_model_name', None)
    with stage('model_response', trace_id, heartbeat=True, progress=progress, schema=schema.__name__,
               model_class=type(model).__name__,
               model_name=model_name if isinstance(model_name, str) else None,
               prompt_chars=len(prompt), streaming=True):
        response = normalize_response(model(prompt)).body
        if preview is not None:
            preview.flush()
    log_event('model_response', trace_id=trace_id, schema=schema.__name__,
              response_chars=len(response) if isinstance(response, str) else 0, **progress())
    if isinstance(response, str):
        cleaned = WriterToolBase._strip_leading_think_blocks(response)
        log_event('response_normalization', trace_id=trace_id, raw_chars=len(response),
                  content_chars=len(cleaned), removed_chars=len(response) - len(cleaned))
    try:
        with stage('json_parse', trace_id, schema=schema.__name__):
            return parse_response(response)
    except ValueError as error:
        # Keep malformed-response diagnostics useful without logging manuscript,
        # prompt, reasoning, or model credentials.
        raw = response if isinstance(response, str) else ''
        info = {'trace_id': trace_id, 'schema': schema.__name__, 'response_type': type(response).__name__,
                'response_chars': len(raw), 'sha256': hashlib.sha256(raw.encode()).hexdigest(),
                'fenced': raw.lstrip().startswith('```'),
                'thinking_tag': bool(re.search(r'<think(?:ing)?\b', raw, re.I)),
                'ends_with_object': raw.rstrip().endswith('}'),
                'error_type': type(error).__name__}
        if isinstance(error, json.JSONDecodeError):
            info.update(message=error.msg, line=error.lineno, column=error.colno, position=error.pos,
                        parsed_chars=len(error.doc), at_end=error.pos >= len(error.doc.rstrip()))
        LOG.warning('paper_review parse_failure ' + json.dumps(info, ensure_ascii=False))
        if (allow_structure_repair and schema is ReviewResult and isinstance(error, json.JSONDecodeError)
                and error.msg == 'Extra data' and len(raw) <= MAX_REPAIR_CHARS):
            content = WriterToolBase._strip_leading_think_blocks(raw).strip()
            try:
                scalars = _json_scalars(content)
                repaired = call_once(reviewer, JSON_STRUCTURE_PROMPT + '\n' + content, schema, trace_id,
                                     allow_structure_repair=False)
                if _json_scalars(json.dumps(repaired, ensure_ascii=False)) != scalars:
                    raise ValueError('JSON 结构修复改变了原始字段或内容，不能采纳。')
            except ValueError:
                log_event('structure_repair_rejected', trace_id=trace_id, original_sha256=info['sha256'])
            else:
                log_event('structure_repair_accepted', trace_id=trace_id, original_sha256=info['sha256'])
                return repaired
        raise ValueError('审稿返回无法解析的 JSON，已停止，未自动重新审稿。') from error


def read_literature(reviewer, result, inputs, trace_id):
    payload = literature_input(result, inputs)
    if not payload['claims']:
        return []
    prompt = LITERATURE_PROMPT + '\n\nInput JSON:\n' + json.dumps(payload, ensure_ascii=False)
    if len(prompt) > 120000:
        result.limitations.append('所提供全文超出本次独立核读上下文范围，文献支持保持证据不足；未截断后冒充全文核实。')
        return []
    try:
        answer = LiteratureReadings.model_validate(call_once(reviewer, prompt, LiteratureReadings, trace_id))
        expected = {claim['claim_index'] for claim in payload['claims']}
        returned = [reading.claim_index for reading in answer.readings]
        if len(returned) != len(set(returned)) or set(returned) != expected:
            raise ValueError('独立文献核读结果编号不完整或重复。')
        return answer.readings
    except Exception as error:
        log_event('literature_reading_unavailable', trace_id=trace_id, error_type=type(error).__name__)
        result.limitations.append('独立全文语义核读未完成，相关支持主张保持证据不足；不能按已定位摘录宣称核实。')
        return []


def run(inputs: dict) -> dict:
    """Business inputs -> Markdown review, structured review_data, is_demo."""
    trace_id = uuid.uuid4().hex
    with stage('review', trace_id):
        with stage('input_validation', trace_id):
            inputs = validate_input('peer_review', inputs)
        log_event('input_ready', trace_id=trace_id, manuscript_chars=len(inputs['manuscript']),
                  manuscript_sha256=text_digest(inputs['manuscript']),
                  papers=len(inputs['papers']), charts=len(inputs['charts']))
        reviewer = WriterToolBase(llm=AutoModel(model='llm'))
        prompt = REVIEW_PROMPT + '\n\nInput JSON:\n' + json.dumps(inputs, ensure_ascii=False)
        candidate = call_once(reviewer, prompt, ReviewResult, trace_id)
        with stage('schema_and_quote_validation', trace_id):
            targets = repair_targets(candidate, inputs['manuscript'], inputs)
        log_event('validation_result', trace_id=trace_id, issues=len(candidate['issues']),
                  repair_targets=len(targets))
        if targets:
            with stage('targeted_repair', trace_id):
                result = repair_result(reviewer, inputs, candidate, targets, trace_id)
        else:
            result = ReviewResult.model_validate(candidate)
        readings = read_literature(reviewer, result, inputs, trace_id)
        with stage('report_rendering', trace_id):
            return build_output(result, inputs, readings=readings)
