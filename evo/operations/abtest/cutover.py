import json
import os
import random
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any
from urllib.parse import quote

from ..analysis.utils import METRICS, bound_input_ref, typed_payload
from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import progress, validate_case_id
from ... import validate_id
from ...runtime import OperationContext, OperationOutput
from ... import normalize_http_origin

BOOTSTRAP_ITERATIONS = 2000
MIN_DECISIVE_CASES = 5


class CompareABTestOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        base_ref = bound_input_ref(ctx, ctx.params.get('baseline_eval_report_ref'), 'EvalReport')
        cand_ref = bound_input_ref(ctx, ctx.params.get('candidate_eval_report_ref'), 'EvalReport')
        base, cand = typed_payload(ctx, base_ref, 'EvalReport'), typed_payload(ctx, cand_ref, 'EvalReport')
        dataset_ref = str(base.get('eval_dataset_ref') or '')
        if not dataset_ref or dataset_ref != str(cand.get('eval_dataset_ref') or ''):
            raise ValueError('baseline and candidate EvalReport must use the same eval_dataset_ref')
        if ctx.params.get('case_ids') is not None:
            raise ValueError('case_ids is not supported; compare uses full EvalReport case set')
        primary = str(ctx.params.get('primary_metric') or 'answer_correctness')
        if primary not in METRICS:
            raise ValueError(f'unsupported primary_metric: {primary}')
        policy = {'primary_metric': primary, 'target_mean_delta': self._ratio(ctx.params, 'target_mean_delta', 0.02),
                  'goodcase_regression_ratio_limit': self._ratio(ctx.params, 'goodcase_regression_ratio_limit', 0.34),
                  'regression_epsilon': self._ratio(ctx.params, 'regression_epsilon', 0.02)}
        before, after = self._judge_rows(ctx, base), self._judge_rows(ctx, cand)
        if set(before) != set(after):
            raise ValueError(f'EvalReport case sets mismatch: {sorted(set(before) ^ set(after))}')
        if not before:
            raise ValueError('ABTest comparison case_ids cannot be empty')
        case_ids = sorted(before)
        rows = [self._delta(case_id, before[case_id], after[case_id], policy) for case_id in case_ids]
        b, a = (self._summary([rows_by_id[case_id] for case_id in case_ids]) for rows_by_id in (before, after))
        metrics = {'baseline': b, 'candidate': a, 'delta': {key: round(a[key] - b[key], 4) for key in b}}
        good = [row for row in rows if row['before']['quality_label'] == 'good']
        regressed = [row for row in good if row['outcome'] == 'regressed']
        ratio = round(len(regressed) / len(good), 4) if good else 0.0
        # An empty goodcase set means regressions are unobservable; the guard must not pass silently.
        passed = bool(good) and ratio <= policy['goodcase_regression_ratio_limit']
        guard = {'baseline_goodcase_count': len(good), 'regressed_count': len(regressed), 'regression_ratio': ratio,
                 'limit': policy['goodcase_regression_ratio_limit'], 'passed': passed,
                 'failure': '' if passed else ('no baseline goodcase to guard' if not good
                                               else 'goodcase regression ratio exceeded limit')}
        key = f"{policy['primary_metric']}_avg"
        delta, target = metrics['delta'][key], policy['target_mean_delta']
        deltas = [row['delta'][policy['primary_metric']] for row in rows]
        ci = self._bootstrap_ci(deltas)
        delta_reached, guard_ok = delta >= target, guard['passed']
        decisive = len(deltas) >= MIN_DECISIVE_CASES and (ci['lower'] > 0 or ci['upper'] < 0)
        if delta_reached and guard_ok and decisive:
            status = 'accept'
        elif delta_reached and guard_ok:
            status = 'inconclusive'
        else:
            status = 'reject'
        decision = {'status': status, 'primary_metric': key, 'primary_delta': delta,
                    'target_mean_delta': target, 'bootstrap_ci': ci,
                    'reasons': [f"primary metric delta {delta} {'>=' if delta_reached else '<'} target {target}",
                                f"goodcase guard {'passed' if guard_ok else 'failed'}: "
                                f"ratio {guard['regression_ratio']} limit {guard['limit']}",
                                f"bootstrap 95% CI [{ci['lower']}, {ci['upper']}] over {len(deltas)} cases "
                                f"{'excludes' if decisive else 'does not exclude'} zero"]}
        output_id = validate_id(str(ctx.params.get('output_id') or 'abtest_comparison'), 'output_id')
        payload = {'id': output_id, 'baseline_eval_report_ref': str(base_ref),
                   'candidate_eval_report_ref': str(cand_ref), 'eval_dataset_ref': str(ArtifactRef.parse(dataset_ref)),
                   'case_ids': case_ids, 'metrics': metrics, 'case_deltas': rows,
                   'goodcase_guard': guard, 'decision': decision,
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        report = {'id': f'{output_id}_report', 'abtest_comparison_id': output_id, 'markdown': self._markdown(payload),
                  'source_message_id': str(ctx.params.get('source_message_id') or '')}
        progress(ctx, 'abtest_compare', 'success', f"abtest decision: {decision['status']}", current_item=output_id,
                 detail={'decision_status': decision['status'], 'primary_delta': decision['primary_delta'],
                         'guard_passed': guard['passed']})
        return OperationOutput([
            ArtifactDraft(output_id, 'ABTestComparison', payload, ctx.operation_run_id, [base_ref, cand_ref]),
            ArtifactDraft(report['id'], 'ABTestReport', report, ctx.operation_run_id, [base_ref, cand_ref]),
        ])

    def _judge_rows(self, ctx, report) -> dict[str, dict[str, Any]]:
        rows = {}
        for raw_ref in report.get('judge_result_refs') or []:
            ref = ArtifactRef.parse(str(raw_ref))
            judge = typed_payload(ctx, ref, 'JudgeResult')
            case_id = validate_case_id(str(judge.get('case_id') or ''))

            if case_id in rows:
                raise ValueError(f'duplicate JudgeResult case_id: {case_id}')
            rows[case_id] = judge | {'judge_ref': str(ref), 'query': self._case_query(ctx, judge)}
        return rows

    def _delta(self, case_id, before, after, policy) -> dict[str, Any]:
        b, a = self._case_scores(before), self._case_scores(after)
        delta = {metric: round(a[metric] - b[metric], 4) for metric in METRICS}
        d, epsilon = delta[policy['primary_metric']], policy['regression_epsilon']
        return {'case_id': case_id, 'baseline_judge_ref': before['judge_ref'],
                'candidate_judge_ref': after['judge_ref'],
                'query': before.get('query') or after.get('query') or '',
                'before': b | self._case_delta_side(before),
                'after': a | self._case_delta_side(after), 'delta': delta,
                'outcome': 'improved' if d > epsilon else 'regressed' if d < -epsilon else 'unchanged'}

    def _case_query(self, ctx, row) -> str:
        try:
            case_ref = ArtifactRef.parse(str(row.get('case_ref') or ''))
            case = typed_payload(ctx, case_ref, 'DatasetCase')
            return str(case.get('question') or '').strip()
        except (TypeError, ValueError, KeyError):
            return ''

    def _case_delta_side(self, row) -> dict[str, Any]:
        return {'quality_label': row.get('quality_label', 'bad'),
                'trace_id': str(row.get('trace_id') or '').strip(),
                'reason': self._case_reason(row)}

    def _case_reason(self, row) -> str:
        reason = row.get('reason') or row.get('judge_reason') or ''
        if isinstance(reason, list): return '; '.join(str(item) for item in reason if str(item).strip())
        return str(reason or '')

    def _case_scores(self, row) -> dict[str, float]:
        out = {metric: self._number(row.get(metric), metric) for metric in METRICS}
        bad = [metric for metric, value in out.items() if not 0 <= value <= 1]
        if bad:
            raise ValueError(f'{bad[0]} out of range for {row.get("judge_ref")}: {row.get(bad[0])!r}')
        return out

    def _summary(self, rows) -> dict[str, float]:
        if any(not isinstance(row.get('is_correct'), bool) for row in rows):
            raise ValueError('is_correct missing from ABTest JudgeResult')
        scores = [self._case_scores(row) for row in rows]
        correct = [1.0 if row.get('is_correct') is True else 0.0 for row in rows]
        return {f'{m}_avg': round(sum(s[m] for s in scores) / len(scores), 4) if scores else 0.0
                for m in METRICS} | {'correct_rate': round(sum(correct) / len(correct), 4) if correct else 0.0}

    def _bootstrap_ci(self, deltas, iterations=BOOTSTRAP_ITERATIONS) -> dict[str, float]:
        if not deltas:
            return {'lower': 0.0, 'upper': 0.0}
        rng = random.Random(f'abtest:{len(deltas)}:{round(sum(deltas), 6)}')
        means = sorted(sum(rng.choice(deltas) for _ in deltas) / len(deltas) for _ in range(iterations))
        return {'lower': round(means[int(0.025 * iterations)], 4),
                'upper': round(means[min(iterations - 1, int(0.975 * iterations))], 4)}

    def _markdown(self, payload) -> str:
        decision, metrics, guard = payload['decision'], payload['metrics'], payload['goodcase_guard']
        ci = decision.get('bootstrap_ci') or {}
        lines = ['# ABTest 对比报告', '', f"- 决策: **{decision['status']}**",
                 f"- 主指标: {decision['primary_metric']} delta={decision['primary_delta']} "
                 f"(目标 {decision['target_mean_delta']}, 95% CI [{ci.get('lower')}, {ci.get('upper')}])",
                 f"- goodcase 守卫: {'通过' if guard['passed'] else '未通过'} "
                 f"(回归比例 {guard['regression_ratio']} / 上限 {guard['limit']})",
                 f"- 用例数: {len(payload['case_ids'])}", '', '## 指标对比', '',
                 '| 指标 | baseline | candidate | delta |', '| --- | --- | --- | --- |']
        for k in sorted(metrics['baseline']):
            lines.append(f"| {k} | {metrics['baseline'][k]} | {metrics['candidate'][k]} | {metrics['delta'][k]} |")
        lines += ['', '## 决策依据', ''] + [f'- {reason}' for reason in decision['reasons']]
        regressed = [row for row in payload['case_deltas'] if row['outcome'] == 'regressed']
        if regressed:
            lines += ['', '## 回归用例', ''] + [f"- {row['case_id']}: delta={row['delta']}" for row in regressed[:20]]
        return '\n'.join(lines)

    def _ratio(self, params, name, default) -> float:
        value = self._number(params.get(name, default), name)
        value = value / 100 if value > 1 else value
        if not 0 <= value <= 1:
            raise ValueError(f'{name} out of range: {value}')
        return value

    def _number(self, value, name) -> float:
        try:
            return round(float(value), 4)
        except (TypeError, ValueError) as exc:
            raise ValueError(f'{name} must be number: {value!r}') from exc


class CutoverCandidateAlgorithmOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        comparison_ref = self._ref(ctx, 'abtest_comparison_ref', 'ABTestComparison')
        workspace_ref = self._ref(ctx, 'candidate_workspace_ref', 'CandidateWorkspace')
        comparison, workspace = ctx.artifact_graph.get(comparison_ref), ctx.artifact_graph.get(workspace_ref)
        if (comparison.get('decision') or {}).get('status') != 'accept':
            raise ValueError('candidate cutover requires accepted ABTestComparison')
        router_admin_url = normalize_http_origin(str(ctx.params.get('router_admin_url') or ''), 'router_admin_url')
        algorithm_id = validate_id(str(ctx.params.get('algorithm_id')
                                       or f'evo_{ctx.run_id}_{int(time.time())}').replace('@', '_'), 'algorithm_id')
        path = Path(str(ctx.params.get('code_path') or workspace.get('workspace_ref') or '')).resolve()
        chat_path = path if (path / 'app.py').exists() else path / 'lazymind' / 'chat'
        if not chat_path.exists():
            raise ValueError(f'candidate chat code_path not found: {chat_path}')
        code_path = str(chat_path)
        health = request_json('GET', f'{router_admin_url}/health', timeout_s=10)
        if health.get('status') != 'ok':
            raise RuntimeError(f'router health check failed: {health}')
        progress(ctx, 'abtest_cutover', 'running', 'registering parser algorithm',
                 detail={'algorithm_id': algorithm_id, 'workspace_ref': workspace.get('workspace_ref')})
        root = Path(str(workspace.get('workspace_ref') or '')).resolve()
        if _has_parser_capability(root):
            _run_parser_command(root, 'register_parser_algorithm', algorithm_id)
            parser_registration = {'status': 'registered', 'algorithm_id': algorithm_id}
        else:
            # Candidate without parsing code keeps the default parser; only chat traffic is cut over.
            parser_registration = {'status': 'skipped', 'reason': 'candidate_has_no_parser_capability',
                                   'algorithm_id': algorithm_id}
        doc_source = ctx.params.get('document_server_url') or os.getenv('LAZYMIND_EVO_DOCUMENT_SERVER_URL')
        doc_url = str(doc_source or 'http://parsing:8000').split(',', 1)[0].rstrip('/')
        env = {'LAZYMIND_ALGO_ID': algorithm_id, 'LAZYMIND_AGENTIC_KB_NAME': algorithm_id,
               'LAZYMIND_DOCUMENT_SERVER_URL': f'{doc_url},{algorithm_id}'}
        for key in ('LAZYMIND_DOCUMENT_PROCESSOR_URL', 'LAZYMIND_MILVUS_URI', 'LAZYMIND_OPENSEARCH_URI',
                    'LAZYMIND_OPENSEARCH_USER', 'LAZYMIND_OPENSEARCH_PASSWORD', 'LAZYMIND_MODEL_CONFIG_PATH'):
            if value := os.getenv(key):
                env[key] = value
        env |= {k: str(v) for k, v in dict(ctx.params.get('config') or {}).items()}
        body = {'id': algorithm_id, 'name': algorithm_id, 'code_path': code_path,
                'instance_count': int(ctx.params.get('instance_count') or 1), 'config': env}
        progress(ctx, 'abtest_cutover', 'running', 'registering candidate algorithm',
                 detail={'router_admin_url': router_admin_url, 'algorithm_id': algorithm_id, 'code_path': code_path})
        # Strategy before cutover is saved so any failure (and later disable) restores it instead of wiping AB config.
        previous_strategy = _try_get_json(f'{router_admin_url}/inner/ab/strategy')
        registered: dict[str, Any] = {}
        target_weight = int(ctx.params.get('candidate_weight') or 100)
        canary_weight = max(1, min(target_weight, int(ctx.params.get('canary_weight') or 10)))
        stages: list[dict[str, Any]] = []
        try:
            registered = request_json('POST', f'{router_admin_url}/inner/algorithm/register', body)
            if not registered.get('ports'):
                raise RuntimeError(f'candidate algorithm registered without ports: {registered}')
            stages.append({'stage': 'register', 'status': 'passed', 'ports': registered.get('ports')})
            for stage, weight in (('canary', canary_weight), ('commit', target_weight)):
                bounded = max(0, min(100, weight))
                weights = {k: v for k, v in {'default': 100 - bounded, algorithm_id: bounded}.items() if v > 0}
                progress(ctx, 'abtest_cutover', 'running', f'switching chat traffic ({stage})',
                         detail={'router_admin_url': router_admin_url, 'weights': weights})
                strategy = request_json('PUT', f'{router_admin_url}/inner/ab/strategy', {'weights': weights})
                applied = _try_get_json(f'{router_admin_url}/inner/ab/strategy')
                if algorithm_id not in ((applied.get('strategy') or {}).get('weights') or weights):
                    raise RuntimeError(f'{stage} strategy not applied: {applied}')
                stages.append({'stage': stage, 'status': 'passed', 'weights': weights})
                if weight == target_weight:
                    break
        except Exception:
            _restore_strategy(router_admin_url, previous_strategy)
            if registered:
                try:
                    request_json('DELETE', f'{router_admin_url}/inner/algorithm/{quote(algorithm_id)}')
                except Exception:
                    pass
            drop_parser_algorithm(Path(str(workspace.get('workspace_ref') or '')), algorithm_id)
            raise
        payload = {'id': str(ctx.params.get('output_id') or 'candidate_algorithm_cutover'),
                   'algorithm_id': algorithm_id, 'router_admin_url': router_admin_url, 'code_path': code_path,
                   'workspace_ref': str(workspace.get('workspace_ref') or ''),
                   'parser_registration': parser_registration, 'abtest_comparison_ref': str(comparison_ref),
                   'candidate_workspace_ref': str(workspace_ref), 'register_response': registered,
                   'strategy': strategy, 'weights': weights, 'stages': stages,
                   'previous_strategy': previous_strategy, 'status': 'active'}
        progress(ctx, 'abtest_cutover', 'success', 'candidate algorithm cutover finished',
                 detail={'algorithm_id': algorithm_id, 'ports': registered.get('ports', [])})
        return OperationOutput([ArtifactDraft(payload['id'], 'CandidateAlgorithmCutover', payload,
                                              ctx.operation_run_id, [comparison_ref, workspace_ref])])

    def _ref(self, ctx, name, schema) -> ArtifactRef:
        ref = ArtifactRef.parse(str(ctx.params.get(name) or ''))
        if ctx.artifact_graph.schema_name(ref) != schema:
            raise ValueError(f'{name} must be {schema}: {ref}')
        return ref


def disable_candidate_algorithm(payload: dict[str, Any]) -> None:
    router_admin_url = ''
    for key in ('router_admin_url', 'router_url'):
        if value := str(payload.get(key) or '').strip():
            try:
                router_admin_url = normalize_http_origin(value, 'router_admin_url')
            except ValueError:
                router_admin_url = ''
            break
    algorithm_id = str(payload.get('algorithm_id') or '')
    if router_admin_url and algorithm_id and payload.get('status') == 'active':
        try:
            strategy = request_json('GET', f'{router_admin_url}/inner/ab/strategy')
            if algorithm_id in ((strategy.get('strategy') or {}).get('weights') or {}):
                # Restore the strategy captured before cutover; only delete when there was none.
                if not _restore_strategy(router_admin_url, payload.get('previous_strategy')):
                    request_json('DELETE', f'{router_admin_url}/inner/ab/strategy')
            request_json('DELETE', f'{router_admin_url}/inner/algorithm/{quote(algorithm_id)}')
        finally:
            drop_parser_algorithm(Path(str(payload.get('workspace_ref') or '')), algorithm_id)


def _restore_strategy(router_admin_url: str, previous: Any) -> bool:
    weights = ((previous or {}).get('strategy') or {}).get('weights') if isinstance(previous, dict) else {}
    if not weights:
        return False
    try:
        request_json('PUT', f'{router_admin_url}/inner/ab/strategy', {'weights': weights})
        return True
    except Exception:
        return False


def _try_get_json(url: str) -> dict[str, Any]:
    try:
        return request_json('GET', url)
    except Exception:
        return {}


def drop_parser_algorithm(root: Path, algorithm_id: str) -> None:
    if root.exists() and algorithm_id and _has_parser_capability(root.resolve()):
        _run_parser_command(root.resolve(), 'drop_parser_algorithm', algorithm_id)


def _has_parser_capability(root: Path) -> bool:
    return (root / 'lazymind' / 'parsing' / 'service' / 'build_document.py').exists()


def _run_parser_command(root: Path, func: str, algorithm_id: str) -> None:
    env = os.environ.copy()
    env['PYTHONPATH'] = os.pathsep.join([str(root), '/opt/lazyllm', env.get('PYTHONPATH', '')])
    code = ('from lazymind.parsing.service.build_document import {func}; '
            '{func}({algorithm_id})').format(func=func, algorithm_id=json.dumps(algorithm_id))
    run = subprocess.run([sys.executable, '-c', code], cwd=str(root), env=env, capture_output=True, text=True)
    if run.returncode:
        detail = (run.stderr or run.stdout or '').strip()[-2000:]
        raise RuntimeError(f'{func} failed for {algorithm_id}: {detail}')


def request_json(method: str, url: str, payload: dict[str, Any] | None = None, *, timeout_s: int = 180) -> dict:
    data = None if payload is None else json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(url, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=timeout_s) as response:
            raw = response.read().decode('utf-8')
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='replace')
        raise RuntimeError(f'{method} {url} failed: HTTP {exc.code} {detail}') from exc
    return json.loads(raw) if raw else {}
