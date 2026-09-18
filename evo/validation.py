"""Trusted offline capability validation. Input credentials arrive on stdin only.

The optional workflow inputs must refer to an isolated validation router and corpus:
the real five-stage flow may publish its candidate to that router. Protocol probes
alone NEVER produce an admission record. No browser endpoint accepts these reports.
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import sys
import tempfile
from pathlib import Path
from typing import Any

from evo import artifacts as A
from evo.artifact_runtime import ArtifactKey
from evo.message_intent.planner import plan_next_turn
from evo.operations.dataset.generation import generate_case, prepare_case
from evo.operations.eval.judge import judge_case
from evo.operations.repair.opencode import run_opencode_streaming
from evo.repair_model import opencode_settings, resolve_evo_model
from evo.service.core import EvoService


VERSION = 'evo-opencode-v1'
REQUIRED_CHECKS = ('planning', 'dataset_generation', 'evaluation', 'code_edit', 'workflow')
SOURCE = 'The fictional Aurora test library opens at 09:00 and closes at 17:00 every day.'


def _planning(config: dict[str, Any]) -> None:
    result = plan_next_turn({
        'user_message': '只查询当前任务进度，不修改任何内容。',
        'flow_snapshot': {'status': 'running', 'current_stage': 'dataset'},
        'intent_catalog': {'stages': list(A.STEPS)},
    }, config)
    action = result.next_action
    if action is None or action.kind != 'query' or action.query != 'progress':
        raise ValueError('planning_contract')


def _dataset(config: dict[str, Any]) -> dict[str, Any]:
    snapshot = {'dataset_id': 'capability-fixture', 'source_units': [{
        'source_id': 'capability-fixture', 'doc_id': 'fixture-doc', 'doc_ref': 'fixture-doc',
        'filename': 'library.txt', 'chunk_id': 'fixture-chunk',
        'source_unit_ref': 'fixture-chunk', 'content': SOURCE,
    }]}
    run = {'llm_config': config, 'num_case': 1, 'question_type': 'single_hop', 'difficulty': 'easy'}
    return generate_case(run, snapshot, prepare_case(run, snapshot, 'case_0001'))


def _evaluate(config: dict[str, Any], case: dict[str, Any]) -> None:
    result = judge_case(case, {'case_id': case['id'], 'status': 'ok', 'answer': case['answer'],
                              'sources': [], 'trace_id': ''}, {}, config)
    if result.get('failure_type') in {'judge_contract_error', 'dataset_contract_error', 'infra_failure'}:
        raise ValueError('evaluation_contract')
    if result.get('is_correct') is not True:
        raise ValueError('reference_answer_rejected')


def _code_edit(config: dict[str, Any], root: Path) -> None:
    workspace = root / 'code-probe'
    workspace.mkdir(mode=0o700)
    target = workspace / 'probe.txt'
    target.write_text('before\n', encoding='utf-8')
    marker = os.urandom(16).hex()
    result = run_opencode_streaming(
        workdir=str(workspace), artifact_dir=root / 'code-evidence', timeout_s=120,
        config=opencode_settings(config['evo_llm']),
        prompt=json.dumps({'task': 'Read probe.txt, then use edit/write to replace its contents.',
                           'required_contents': marker + '\n',
                           'constraints': ['Only change probe.txt.', 'Do not run shell commands.']}),
    )
    if result.returncode != 0 or target.is_symlink() or target.read_text().strip() != marker:
        raise ValueError('code_edit_contract')


async def _workflow(config: dict[str, Any], inputs: dict[str, Any], root: Path) -> dict[str, Any]:
    service = await EvoService.open(root / 'workflow', max_concurrency=2)
    thread_id = ''
    try:
        created = await service.create_thread({'mode': 'auto', 'title': 'Capability validation',
                                               'inputs': inputs, 'llm_config': config})
        thread_id = created['thread_id']
        await service.start(thread_id, {})
        async with asyncio.timeout(900):
            while True:
                try:
                    snapshot = await service.flow.wait_until_boundary(thread_id, timeout=60)
                except TimeoutError:
                    continue
                if snapshot.status in {'failed', 'cancelled', 'completed'}:
                    break
                await asyncio.sleep(0.05)
        if snapshot.status != 'completed' or any(stage.status != 'completed' for stage in snapshot.stages):
            raise ValueError('workflow_incomplete')
        roots = {}
        for stage, artifact_id in A.ROOTS.items():
            head = await service.flow.head(thread_id, ArtifactKey.scalar(artifact_id))
            if head is None:
                raise ValueError('workflow_artifact_missing')
            value = await service.flow.read(thread_id, head.ref)
            roots[stage] = hashlib.sha256(json.dumps(value, sort_keys=True, default=str).encode()).hexdigest()
        attempts = await service.flow.attempts(thread_id)
        # A swallowed judge/transport failure must not turn a completed DAG into evidence.
        for attempt in attempts:
            for key in attempt.output_keys:
                if key.artifact_id not in {A.EVAL_JUDGE_RESULT, A.ABTEST_CANDIDATE_JUDGE_RESULT}:
                    continue
                head = await service.flow.head(thread_id, key)
                value = await service.flow.read(thread_id, head.ref) if head else {}
                if value.get('failure_type') in {'judge_contract_error', 'dataset_contract_error', 'infra_failure'}:
                    raise ValueError('workflow_evaluation_failed')
        return {'thread_id': thread_id, 'stages': list(A.STEPS), 'artifact_sha256': roots,
                'attempt_count': len(attempts)}
    finally:
        if thread_id:
            snapshot = await service.flow.snapshot(thread_id)
            if snapshot.status not in {'completed', 'cancelled'} or snapshot.runtime.active_attempts:
                await service.cancel(thread_id, {})
        await service.close()


def validate(packet: dict[str, Any], root: Path) -> dict[str, Any]:
    config = packet['llm_config']
    report: dict[str, Any] = {'model_ref': packet['model_ref'], 'nonce': packet['nonce'],
                              'validation_version': VERSION, 'passed': False,
                              'checks': {}, 'failures': {}}
    resolve_evo_model(config.get('evo_llm'))
    case = None
    for name in REQUIRED_CHECKS:
        try:
            if name == 'planning':
                _planning(config)
            elif name == 'dataset_generation':
                case = _dataset(config)
            elif name == 'evaluation':
                if case is None:
                    raise ValueError('dataset_generation_required')
                _evaluate(config, case)
            elif name == 'code_edit':
                _code_edit(config, root)
            else:
                inputs = packet.get('workflow_inputs')
                if not inputs:
                    raise ValueError('isolated_workflow_inputs_required')
                report['workflow'] = asyncio.run(_workflow(config, inputs, root))
            report['checks'][name] = True
        except Exception as exc:
            # Do not serialize provider exceptions, model outputs, URLs or credentials.
            report['checks'][name] = False
            safe_codes = {'dataset_generation_required', 'isolated_workflow_inputs_required',
                          'planning_contract', 'evaluation_contract', 'reference_answer_rejected',
                          'code_edit_contract', 'workflow_incomplete', 'workflow_artifact_missing',
                          'workflow_evaluation_failed'}
            report['failures'][name] = (str(exc) if type(exc) is ValueError and str(exc) in safe_codes
                                        else 'dependency_unavailable' if isinstance(exc, ImportError)
                                        else 'validation_failed')
    report['passed'] = all(report['checks'].get(name) is True for name in REQUIRED_CHECKS)
    return report


def main() -> None:
    os.umask(0o077)
    # Keep stdout a strict report channel, including child-process/library logs.
    with os.fdopen(os.dup(sys.stdout.fileno()), 'w') as output:
        os.dup2(sys.stderr.fileno(), sys.stdout.fileno())
        try:
            packet = json.loads(sys.stdin.buffer.read(2 * 1024 * 1024))
            with tempfile.TemporaryDirectory(prefix='evo-validation-') as directory:
                os.environ['LAZYMIND_EVO_BASE_DIR'] = directory
                report = validate(packet, Path(directory))
            output.write(json.dumps(report) + '\n')
        except Exception:
            output.write(json.dumps({'passed': False, 'error': 'validation_failed'}) + '\n')


if __name__ == '__main__':
    main()
