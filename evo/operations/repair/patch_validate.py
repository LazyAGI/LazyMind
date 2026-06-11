from __future__ import annotations

import re
from pathlib import Path
from typing import Any

from .schemas import validate_repair_artifact

TOPK_RE = re.compile(r'\b(top[_-]?k|limit|max[_-]?(?:chunks|contexts|results)|threshold)\b', re.I)
INT_RE = re.compile(r'\b\d+\b')
CASE_RE = re.compile(r'\bcase[_-]?\d+\b', re.I)
DOC_CHUNK_RE = re.compile(r'\b(?:doc|chunk)[_-]?[0-9a-f]{1,64}\b', re.I)
ID_FIELD_RE = re.compile(r'\b(case_id|doc_id|chunk_id)\b.{0,80}[\'"]([0-9a-zA-Z_-]{3,80})[\'"]', re.I)
BARE_ZERO4_RE = re.compile(r'[\'"](\d{4})[\'"]')
JUDGE_RE = re.compile(r'\b(judge|answer_correctness|faithfulness|quality_label|is_correct)\b', re.I)
EXCEPTION_SWALLOW_RE = re.compile(
    r'\b(except\s+Exception|except:|try:|pass|return\s+(?:None|True|False|\[\]|\{\}|0(?:\.0)?))\b', re.I
)
PROMPT_RE = re.compile(r'\b(prompt|system_prompt|instruction|grading|judge)\b', re.I)
RANKING_RE = re.compile(r'\b(score|rank|rerank|sort|similarity|threshold|weight)\b', re.I)


def assess_patch_correctness(attempt: int, patch: dict[str, Any], evaluation: dict[str, Any],
                             worker_report: dict[str, Any] | None = None) -> dict[str, Any]:
    shape = patch_shape(patch)
    risks = _overfitting_risks(patch, shape)
    invariants = _behavior_invariants(patch, shape, worker_report or {})
    payload = {
        'id': f'patch_correctness_assessment_attempt_{attempt}',
        'attempt': attempt,
        'code_patch_candidate_ref': f"{patch.get('id')}@v1" if patch.get('id') else '',
        'repair_evaluation_ref': f"{evaluation.get('id')}@v1" if evaluation.get('id') else '',
        'patch_shape': shape,
        'overfitting_risks': risks,
        'behavior_invariants': invariants,
        'heldout_eval': _heldout_eval(evaluation),
        'metamorphic_checks': [],
        'verdict': _verdict(patch, evaluation, risks, invariants),
    }
    validate_repair_artifact('PatchCorrectnessAssessment', payload)
    return payload


def patch_shape(patch: dict[str, Any]) -> dict[str, Any]:
    diff = str(patch.get('diff') or '')
    added, deleted = _added_lines(diff), _deleted_lines(diff)
    code_added = [text for text in added if _code_line(text)]
    code_deleted = [text for text in deleted if _code_line(text)]
    files = list(patch.get('files_changed') or [])
    test_files = [path for path in files if _is_test_path(str(path))]
    flags = (['test_files_touched'] if test_files else [])
    large = len(files) > 3 or len(added) + len(deleted) > 160
    if large: flags.append('large_patch')
    return {
        'line_added': len(added), 'line_deleted': len(deleted),
        'code_line_added': len(code_added), 'code_line_deleted': len(code_deleted),
        'files_changed': len(files), 'new_files': len(list(patch.get('files_created') or [])),
        'test_files_touched': bool(test_files), 'test_files': test_files,
        'large_patch': large,
        'comment_only_patch': bool(diff.strip()) and not (code_added or code_deleted),
        'unrelated_files': [],
        'risk_flags': flags,
    }


def _overfitting_risks(patch: dict[str, Any], shape: dict[str, Any]) -> list[dict[str, Any]]:
    risks: list[dict[str, Any]] = []
    diff = str(patch.get('diff') or '')
    if shape['test_files_touched']:
        risks.append({'kind': 'test_only_change', 'severity': 'high',
                      'evidence': ', '.join(shape.get('test_files') or [])})
    if shape['comment_only_patch']:
        risks.append({'kind': 'comment_only_patch', 'severity': 'high',
                      'evidence': 'diff contains no production code line changes'})
    hardcoded = _hardcoded_identifiers(diff)
    if hardcoded:
        risks.append({'kind': 'case_hardcode', 'severity': 'high', 'evidence': ', '.join(hardcoded[:8])})
    if _looks_like_broad_topk_increase(diff):
        risks.append({'kind': 'unbounded_topk_increase', 'severity': 'medium',
                      'evidence': 'numeric retrieval/top-k/limit value increased without a nearby guard'})
    if _looks_like_judge_bypass(diff):
        risks.append({'kind': 'judge_bypass', 'severity': 'high',
                      'evidence': 'diff changes judge/scoring semantics or hardcoded correctness labels'})
    if _looks_like_exception_swallow(diff):
        risks.append({'kind': 'exception_swallow', 'severity': 'high',
                      'evidence': 'diff adds broad exception swallowing or neutral fallback return'})
    if _looks_like_broad_prompt_rewrite(diff):
        risks.append({'kind': 'broad_prompt_rewrite', 'severity': 'medium',
                      'evidence': 'large prompt/judge instruction rewrite detected'})
    if _looks_like_ranking_semantics_break(diff):
        risks.append({'kind': 'ranking_semantics_break', 'severity': 'medium',
                      'evidence': 'ranking/sorting/threshold semantics changed broadly'})
    if shape['large_patch']:
        risks.append({'kind': 'large_patch', 'severity': 'medium',
                      'evidence': f"{shape['files_changed']} files, "
                                  f"{shape['line_added'] + shape['line_deleted']} changed lines"})
    return risks


def _behavior_invariants(patch: dict[str, Any], shape: dict[str, Any],
                         worker_report: dict[str, Any]) -> list[dict[str, Any]]:
    status = str(worker_report.get('protocol_status') or '')
    risk_kinds = set(shape.get('risk_flags') or [])
    risk_kinds.update(risk.get('kind') for risk in _overfitting_risks(patch, shape))
    return [
        {'name': 'does_not_touch_tests', 'passed': not shape['test_files_touched']},
        {'name': 'not_comment_only_patch', 'passed': not shape['comment_only_patch']},
        {'name': 'patch_shape_within_default_budget', 'passed': not shape['large_patch']},
        {'name': 'does_not_globally_increase_context_without_guard',
         'passed': 'unbounded_topk_increase' not in risk_kinds},
        {'name': 'does_not_bypass_judge_or_scoring', 'passed': 'judge_bypass' not in risk_kinds},
        {'name': 'does_not_swallow_exceptions_broadly', 'passed': 'exception_swallow' not in risk_kinds},
        {'name': 'keeps_ranking_semantics_local', 'passed': 'ranking_semantics_break' not in risk_kinds},
        {'name': 'worker_report_protocol_valid', 'passed': status in {'valid', 'not_run', ''}},
        {'name': 'patch_scope_passed', 'passed': (patch.get('scope_check') or {}).get('status') == 'passed'},
    ]


def _verdict(patch: dict[str, Any], evaluation: dict[str, Any], risks: list[dict[str, Any]],
             invariants: list[dict[str, Any]]) -> str:
    if any(risk.get('severity') == 'high' for risk in risks): return 'reject'
    if (patch.get('scope_check') or {}).get('status') != 'passed': return 'needs_more_validation'
    if any(risk.get('kind') in {'unbounded_topk_increase', 'large_patch', 'broad_prompt_rewrite',
                                'ranking_semantics_break'} for risk in risks):
        return 'needs_more_validation'
    if any(not item.get('passed') for item in invariants): return 'needs_more_validation'
    heldout = _heldout_eval(evaluation)
    if heldout.get('enabled') and heldout.get('passed') is not True: return 'needs_more_validation'
    return 'acceptable' if evaluation.get('status') == 'passed' else 'needs_more_validation'


def _heldout_eval(evaluation: dict[str, Any]) -> dict[str, Any]:
    if 'heldout_eval' not in evaluation:
        return {'enabled': False, 'sibling_badcase_ids': [], 'stratified_goodcase_ids': [],
                'passed': True, 'summary': {'reason': 'not_provided'}}
    heldout = evaluation.get('heldout_eval') if isinstance(evaluation.get('heldout_eval'), dict) else {}
    enabled_raw, passed_raw = heldout.get('enabled'), heldout.get('passed')
    malformed = not isinstance(enabled_raw, bool) or not isinstance(passed_raw, bool)
    enabled = True if malformed else enabled_raw
    return {
        'enabled': enabled,
        'sibling_badcase_ids': list(heldout.get('sibling_badcase_ids') or []),
        'stratified_goodcase_ids': list(heldout.get('stratified_goodcase_ids') or []),
        'passed': False if malformed else (passed_raw is True if enabled else True),
        'summary': heldout.get('summary') if isinstance(heldout.get('summary'), dict) else {},
    }


def _looks_like_broad_topk_increase(diff: str) -> bool:
    removed, added = [], []
    for line in diff.splitlines():
        if line.startswith('---') or line.startswith('+++'): continue
        target = added if line.startswith('+') else removed if line.startswith('-') else None
        if target is None or not TOPK_RE.search(line): continue
        target.extend(int(item) for item in INT_RE.findall(line))
    return bool(removed and added and max(added) > max(removed))


def _hardcoded_identifiers(diff: str) -> list[str]:
    hits = set(CASE_RE.findall(diff) + DOC_CHUNK_RE.findall(diff))
    hits.update(match.group(2) for match in ID_FIELD_RE.finditer(diff))
    hits.update(match.group(1) for match in BARE_ZERO4_RE.finditer(diff))
    return sorted(hits)


def _looks_like_judge_bypass(diff: str) -> bool:
    return any(JUDGE_RE.search(line) and re.search(r'\b(1\.0|true|good|correct)\b', line, re.I)
               for line in _added_lines(diff))


def _looks_like_exception_swallow(diff: str) -> bool:
    joined = '\n'.join(_added_lines(diff))
    return bool(EXCEPTION_SWALLOW_RE.search(joined) and re.search(r'\b(except\s+Exception|except:)\b', joined))


def _looks_like_broad_prompt_rewrite(diff: str) -> bool:
    added = [line for line in _added_lines(diff) if PROMPT_RE.search(line)]
    deleted = [line for line in _deleted_lines(diff) if PROMPT_RE.search(line)]
    return len(added) + len(deleted) >= 12


def _looks_like_ranking_semantics_break(diff: str) -> bool:
    added, deleted = _added_lines(diff), _deleted_lines(diff)
    if not any(RANKING_RE.search(line) for line in added + deleted): return False
    text = '\n'.join(added + deleted)
    return bool(re.search(r'\breverse\s*=|sorted\(|sort\(|score\s*[+\-*/]=|threshold\s*=|weight\s*=', text))


def _added_lines(diff: str) -> list[str]:
    return [line[1:] for line in diff.splitlines() if line.startswith('+') and not line.startswith('+++')]


def _deleted_lines(diff: str) -> list[str]:
    return [line[1:] for line in diff.splitlines() if line.startswith('-') and not line.startswith('---')]


def _is_test_path(path: str) -> bool:
    parts = set(Path(path).parts)
    name = Path(path).name
    return 'tests' in parts or name.startswith('test_') or name.endswith('_test.py')


def _code_line(text: str) -> bool:
    stripped = text.strip()
    return bool(stripped and not stripped.startswith('#') and not stripped.startswith(('"""', "'''")))
