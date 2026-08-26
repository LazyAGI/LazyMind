from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import jsonpatch
from jsonpointer import JsonPointer, JsonPointerException
from pydantic import AnyHttpUrl, TypeAdapter, ValidationError

from evo.artifact_runtime import ArtifactRef
from evo.repair_model import EvoModelConfigError, resolve_evo_model

from .schemas import ConfigPatchAction, ConfigValidationIssue


HTTP_URL = TypeAdapter(AnyHttpUrl)


class ConfigValidationError(ValueError):
    def __init__(self, issues: list[ConfigValidationIssue]) -> None:
        self.issues = issues
        super().__init__('; '.join(issue.message for issue in issues))


def validate_config_patch(thread_id: str, action: ConfigPatchAction,
                          ref: ArtifactRef, current: object
                          ) -> tuple[ArtifactRef, object]:
    issues = _pointer_issues(action.pointer)
    patched = current
    if not issues:
        try:
            patched = jsonpatch.apply_patch(
                current,
                [{'op': 'add', 'path': action.pointer, 'value': action.value}],
                in_place=False,
            )
        except (jsonpatch.JsonPatchException, JsonPointerException):
            issues.append(_issue(
                action.pointer,
                'unknown_field',
                f'path cannot be changed: {action.pointer}',
            ))
    issues.extend(_semantic_issues(thread_id, action.target, patched))
    if issues:
        raise ConfigValidationError(issues)
    return ref, patched


def patch_value(current: object, pointer: str, value: Any) -> object:
    issues = _pointer_issues(pointer)
    if issues:
        raise ConfigValidationError(issues)
    try:
        return jsonpatch.apply_patch(
            current,
            [{'op': 'add', 'path': pointer, 'value': value}],
            in_place=False,
        )
    except (jsonpatch.JsonPatchException, JsonPointerException) as exc:
        raise ConfigValidationError([
            _issue(pointer, 'unknown_field', f'path cannot be changed: {pointer}'),
        ]) from exc


def _pointer_issues(pointer: str) -> list[ConfigValidationIssue]:
    try:
        parts = JsonPointer(pointer).parts
    except JsonPointerException as exc:
        return [_issue(pointer, 'invalid_type', f'invalid JSON pointer: {exc}')]
    if not parts:
        return [_issue(pointer, 'immutable_field', 'root replacement must use replace')]
    if parts[-1] == '-':
        return [_issue(pointer, 'invalid_type', 'array append is not supported')]
    return []


def _semantic_issues(thread_id: str, target: str,
                     value: object
                     ) -> list[ConfigValidationIssue]:
    if not isinstance(value, Mapping):
        return [_issue('/', 'invalid_type', f'{target} must be an object')]
    issues: list[ConfigValidationIssue] = []
    if target == 'run_config':
        issues.extend(_run_config_issues(thread_id, value))
    elif target == 'source_config':
        issues.extend(_source_config_issues(value))
    elif target in {'target_config', 'candidate_config'}:
        issues.extend(_service_config_issues(target, value))
    elif target == 'eval_policy':
        if 'judge_llm_config' in value:
            issues.append(_issue(
                '/judge_llm_config', 'unknown_field',
                'eval_policy must use run_config.llm_config',
            ))
    elif target == 'repair_policy':
        for name in ('llm_config', 'mode', 'thread_id'):
            if name in value:
                issues.append(_issue(
                    f'/{name}', 'unknown_field',
                    f'repair_policy does not support {name}',
                ))
        namespace = value.get('workspace_namespace')
        if namespace is not None and str(namespace) != thread_id:
            issues.append(_issue(
                '/workspace_namespace',
                'invalid_value',
                'workspace_namespace must stay within the current thread',
            ))
    return issues


def _run_config_issues(thread_id: str,
                       value: Mapping[str, object]
                       ) -> list[ConfigValidationIssue]:
    issues: list[ConfigValidationIssue] = []
    configured_thread = value.get('thread_id')
    if configured_thread is not None and configured_thread != thread_id:
        issues.append(_issue('/thread_id', 'immutable_field', 'run_config.thread_id is immutable'))
    num_case = value.get('num_case')
    if num_case is not None and (
        not isinstance(num_case, int) or isinstance(num_case, bool) or num_case < 1
    ):
        issues.append(_issue('/num_case', 'out_of_range', 'num_case must be a positive integer'))
    for name in ('inputs', 'llm_config'):
        if name in value and not isinstance(value[name], Mapping):
            issues.append(_issue(f'/{name}', 'invalid_type', f'{name} must be an object'))
    llm_config = value.get('llm_config')
    if isinstance(llm_config, Mapping):
        for role in ('llm', 'evo_llm', 'embed_main'):
            issues.extend(_role_issues(f'/llm_config/{role}', llm_config))
        if isinstance(llm_config.get('evo_llm'), Mapping):
            try:
                resolve_evo_model(llm_config['evo_llm'])
            except EvoModelConfigError as exc:
                issues.append(_issue('/llm_config/evo_llm', 'invalid_value', exc.reason))
    return issues


def _source_config_issues(value: Mapping[str, object]) -> list[ConfigValidationIssue]:
    issues: list[ConfigValidationIssue] = []
    if not any(value.get(name) for name in ('kb_id', 'csv_data', 'csv_path', 'eval_dataset_path')):
        issues.append(_issue(
            '/',
            'missing_required',
            'source_config requires a knowledge-base or dataset source',
        ))
    for name in ('target_case_count', 'min_case_count'):
        count = value.get(name)
        if count is not None and (
            not isinstance(count, int) or isinstance(count, bool) or count < 1
        ):
            issues.append(_issue(f'/{name}', 'out_of_range', f'{name} must be a positive integer'))
    return issues


def _service_config_issues(target: str,
                           value: Mapping[str, object]
                           ) -> list[ConfigValidationIssue]:
    issues: list[ConfigValidationIssue] = []
    for name in ('router_chat_url', 'router_admin_url'):
        if name in value:
            issues.extend(_url_issues(f'/{name}', value[name]))
    if 'llm_config' in value:
        issues.append(_issue(
            '/llm_config', 'unknown_field',
            f'{target} must use run_config.llm_config',
        ))
    algorithm_id = str(value.get('algorithm_id') or '').strip()
    if target == 'candidate_config' and algorithm_id and not algorithm_id.startswith('evo_'):
        issues.append(_issue(
            '/algorithm_id',
            'invalid_value',
            'candidate algorithm_id must start with evo_',
        ))
    for name in (
        'case_deadline_seconds', 'first_frame_timeout_seconds',
        'connect_timeout_seconds', 'write_timeout_seconds', 'pool_timeout_seconds',
    ):
        amount = value.get(name)
        if amount is not None and (
            not isinstance(amount, (int, float)) or isinstance(amount, bool) or amount <= 0
        ):
            issues.append(_issue(f'/{name}', 'out_of_range', f'{name} must be positive'))
    return issues


def _url_issues(path: str, value: object) -> list[ConfigValidationIssue]:
    try:
        HTTP_URL.validate_python(value)
        return []
    except ValidationError:
        return [_issue(path, 'invalid_url', f'{path} must be an http(s) URL')]


def _role_issues(path: str, value: object) -> list[ConfigValidationIssue]:
    role = path.rsplit('/', 1)[-1]
    if isinstance(value, Mapping) and isinstance(value.get(role), Mapping):
        return []
    return [_issue(path, 'missing_required', f'{path} is required')]


def _issue(path: str, code: str, message: str) -> ConfigValidationIssue:
    return ConfigValidationIssue(path=path or '/', code=code, message=message)


__all__ = [
    'ConfigValidationError', 'patch_value', 'validate_config_patch',
]
