from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import jsonpatch
from jsonpointer import JsonPointer, JsonPointerException
from jsonschema import Draft202012Validator
from pydantic import AnyHttpUrl, TypeAdapter, ValidationError

from evo.artifact_runtime.kernel import ArtifactRef

from .schemas import ConfigValidationIssue, PlannedAction

HTTP_URL = TypeAdapter(AnyHttpUrl)
SPEC = {
    'run_config': (['thread_id', 'mode', 'num_case', 'inputs', 'llm_config'],
                   {'thread_id', 'mode', 'title', 'num_case', 'inputs', 'llm_config'}),
    'source_config': ([], {'kb_id', 'csv_data', 'target_case_count', 'min_case_count'}),
    'target_config': (['router_chat_url', 'router_admin_url', 'algorithm_id', 'llm_config'],
                      {'router_chat_url', 'router_admin_url', 'algorithm_id', 'llm_config',
                       'case_deadline_seconds', 'first_frame_timeout_seconds',
                       'connect_timeout_seconds', 'write_timeout_seconds', 'pool_timeout_seconds'}),
    'candidate_config': (['router_chat_url', 'router_admin_url', 'llm_config'],
                         {'router_chat_url', 'router_admin_url', 'algorithm_id', 'llm_config',
                          'case_deadline_seconds', 'first_frame_timeout_seconds',
                          'connect_timeout_seconds', 'write_timeout_seconds', 'pool_timeout_seconds'}),
    'eval_policy': (['judge_llm_config'], {'judge_llm_config'}),
    'repair_policy': (['llm_config'], {'llm_config', 'thread_id', 'workspace_namespace'}),
}


class ConfigValidationError(ValueError):
    def __init__(self, issues: list[ConfigValidationIssue]) -> None:
        self.issues = issues
        super().__init__('; '.join(issue.message for issue in issues))


def validate_config_patch(thread_id: str, action: PlannedAction, ref: ArtifactRef,
                          current: object) -> tuple[ArtifactRef, str, Any]:
    issues = _pointer_issues(action.pointer)
    patched = current
    if not issues:
        try:
            patched = jsonpatch.apply_patch(
                current, [{'op': 'replace', 'path': action.pointer, 'value': action.value}], in_place=False,
            )
        except (jsonpatch.JsonPatchException, JsonPointerException):
            issues.append(_issue(action.pointer, 'unknown_field', f'path does not exist: {action.pointer}'))
    issues += _schema_issues(action.target, patched) + _semantic_issues(thread_id, action.target, patched)
    if issues:
        raise ConfigValidationError(issues)
    return ref, action.pointer, action.value


def _pointer_issues(pointer: str) -> list[ConfigValidationIssue]:
    try:
        parts = JsonPointer(pointer).parts
    except JsonPointerException as exc:
        return [_issue(pointer, 'invalid_type', f'invalid JSON pointer: {exc}')]
    if not parts:
        return [_issue(pointer, 'immutable_field', 'root config replacement is not allowed')]
    if parts[-1] == '-':
        return [_issue(pointer, 'invalid_type', 'array append is not supported')]
    return []


def _schema_issues(target: str, value: object) -> list[ConfigValidationIssue]:
    required, fields = SPEC[target]
    schema = {
        'type': 'object',
        'required': required,
        'additionalProperties': False,
        'properties': {key: {} for key in fields},
    }
    return [_issue(JsonPointer.from_parts(error.absolute_path).path,
                   'missing_required' if error.validator == 'required' else 'unknown_field',
                   error.message)
            for error in Draft202012Validator(schema).iter_errors(value)]


def _semantic_issues(thread_id: str, target: str, value: object) -> list[ConfigValidationIssue]:
    if not isinstance(value, Mapping):
        return []
    issues: list[ConfigValidationIssue] = []
    if target == 'run_config' and value.get('thread_id') != thread_id:
        issues.append(_issue('/thread_id', 'immutable_field', 'run_config.thread_id is immutable'))
    if target == 'run_config':
        if not isinstance(value.get('num_case'), int) or value.get('num_case') < 1:
            issues.append(_issue('/num_case', 'out_of_range', 'run_config.num_case must be a positive integer'))
        if not isinstance(value.get('inputs'), Mapping):
            issues.append(_issue('/inputs', 'invalid_type', 'run_config.inputs must be an object'))
        if not isinstance(value.get('llm_config'), Mapping):
            issues.append(_issue('/llm_config', 'invalid_type', 'run_config.llm_config must be an object'))
    if target == 'source_config':
        if not value.get('kb_id') and not value.get('csv_data'):
            issues.append(_issue('/', 'missing_required', 'source_config requires kb_id or csv_data'))
        if value.get('csv_data'):
            count = value.get('min_case_count')
            if not isinstance(count, int) or count < 100:
                code = 'out_of_range' if isinstance(count, int) else 'invalid_type'
                issues.append(_issue('/min_case_count', code, 'CSV source min_case_count must be integer >= 100'))
    if target in {'target_config', 'candidate_config'}:
        issues += (
            _url_issue('/router_chat_url', value.get('router_chat_url'))
            + _url_issue('/router_admin_url', value.get('router_admin_url'))
            + _role_issue('/llm_config/llm', value.get('llm_config'))
        )
        algorithm_id = str(value.get('algorithm_id') or '').strip()
        if target == 'target_config' and not algorithm_id:
            issues.append(_issue('/algorithm_id', 'missing_required', '/algorithm_id is required'))
        if target == 'candidate_config' and algorithm_id and not algorithm_id.startswith('evo_'):
            issues.append(_issue('/algorithm_id', 'invalid_value',
                                 'candidate_config.algorithm_id must start with evo_'))
        for key in (
            'case_deadline_seconds',
            'first_frame_timeout_seconds',
            'connect_timeout_seconds',
            'write_timeout_seconds',
            'pool_timeout_seconds',
        ):
            if key in value and (not isinstance(value.get(key), (int, float)) or value.get(key) <= 0):
                issues.append(_issue(f'/{key}', 'out_of_range', f'{key} must be positive'))
    if target == 'eval_policy':
        issues += _role_issue('/judge_llm_config/evo_llm', value.get('judge_llm_config'))
    if target == 'repair_policy':
        issues += _role_issue('/llm_config/evo_llm', value.get('llm_config'))
        if str(value.get('workspace_namespace') or thread_id) != thread_id:
            issues.append(_issue('/workspace_namespace', 'cross_thread_reference',
                                 'workspace_namespace must stay within the thread'))
    return issues


def _url_issue(path: str, value: object) -> list[ConfigValidationIssue]:
    try:
        HTTP_URL.validate_python(value)
        return []
    except ValidationError:
        return [_issue(path, 'invalid_url', f'{path} must be an http(s) URL')]


def _role_issue(path: str, value: object) -> list[ConfigValidationIssue]:
    role = path.rsplit('/', 1)[-1]
    return [] if isinstance(value, Mapping) and isinstance(value.get(role), Mapping) else [
        _issue(path, 'missing_required', f'{path} is required')
    ]


def _issue(path: str, code: str, message: str) -> ConfigValidationIssue:
    return ConfigValidationIssue(path=path or '/', code=code, message=message)
