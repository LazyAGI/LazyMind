from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from lazyllm.tools import tool_concurrency
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.memory import MemoryTools
from lazymind.common.memory import (
    EpisodeCreateInput,
    EpisodeSource,
    EpisodeType,
    MemoryPartialApplyError,
    MemoryStore,
    PreferenceItem,
    build_reference_path,
    get_episode_store,
    validate_reference_content,
)
from lazymind.common.memory.editors.preference import (
    build_preference_reference_content,
    preference_name_to_reference_name,
    reference_name_from_item,
    validate_preference_name,
)
from lazymind.common.memory.validation.common import parse_yaml_frontmatter
from lazymind.common.memory.validation.preference import render_preference_index

from .state import PreferenceStateSnapshot, load_preference_state


class StalePreferenceStateError(RuntimeError):
    pass


class BudgetExhaustedError(RuntimeError):
    pass


@dataclass
class ChangeBudget:
    maximum: int
    used: int = 0

    def require(self, count: int) -> None:
        if count < 0 or self.used + count > self.maximum:
            raise BudgetExhaustedError(
                f'Organizer change budget exhausted ({self.used}/{self.maximum}).'
            )

    def commit(self, count: int) -> None:
        self.used += max(0, count)


@dataclass
class PreferenceOrganizerGate:
    pass_number: int
    budget: ChangeBudget
    hard_min_items: int
    phase: str = 'analyze'
    plan_markdown: str = ''
    plan_hash: str = ''
    baseline_etag: str = ''
    current_etag: str = ''
    authorized_operations: list[dict[str, Any]] = field(default_factory=list)
    next_operation_index: int = 0
    operations: list[dict[str, Any]] = field(default_factory=list)
    terminal_outcome: str = ''
    terminal_error: str = ''

    def submit(self, markdown: str, snapshot: PreferenceStateSnapshot) -> dict[str, Any]:
        if self.phase != 'analyze':
            raise ToolExecutionError('A plan has already been submitted for this pass.')
        normalized = str(markdown or '').strip()
        if not normalized:
            raise ToolExecutionError('markdown must contain a complete organizer plan.')
        try:
            authorized = _parse_authorized_operations(
                normalized,
                snapshot,
                hard_min_items=self.hard_min_items,
                changes_remaining=self.budget.maximum - self.budget.used,
            )
        except (TypeError, ValueError) as exc:
            raise ToolExecutionError(str(exc)) from exc
        self.plan_markdown = normalized
        self.plan_hash = hashlib.sha256(normalized.encode('utf-8')).hexdigest()
        self.baseline_etag = snapshot.data.etag
        self.current_etag = snapshot.data.etag
        self.authorized_operations = authorized
        self.phase = 'apply'
        return {
            'status': 'accepted',
            'pass_number': self.pass_number,
            'plan_hash': self.plan_hash,
            'baseline_etag': self.baseline_etag,
            'authorized_operation_ids': [
                operation['operation_id'] for operation in authorized
            ],
            'write_tools_enabled': True,
        }

    def require_operation(
        self,
        action: str,
        operation_id: str,
    ) -> tuple[PreferenceStateSnapshot, dict[str, Any]]:
        if self.phase != 'apply' or not self.plan_markdown:
            raise ToolExecutionError('submit_preference_plan must succeed before any write.')
        if self.next_operation_index >= len(self.authorized_operations):
            raise ToolExecutionError(
                'The submitted Plan does not authorize another write operation.'
            )
        operation = self.authorized_operations[self.next_operation_index]
        if operation['operation_id'] != str(operation_id or '').strip():
            raise ToolExecutionError(
                'Write operations must follow the exact order in AUTHORIZED OPERATIONS; '
                f"expected {operation['operation_id']!r}."
            )
        if operation['action'] != action:
            raise ToolExecutionError(
                f"Operation {operation_id!r} is authorized for {operation['action']}, not {action}."
            )
        snapshot = load_preference_state()
        if snapshot.data.etag != self.current_etag:
            self.terminal_outcome = 'stale_state'
            self.terminal_error = 'Preference state changed after the organizer Gate.'
            raise StalePreferenceStateError(self.terminal_error)
        return snapshot, operation

    def record(self, action: str, names: list[str], changes: int) -> dict[str, Any]:
        snapshot = load_preference_state()
        self.current_etag = snapshot.data.etag
        self.budget.commit(changes)
        self.next_operation_index += 1
        receipt = {
            'action': action,
            'names': names,
            'changes': changes,
            'etag': self.current_etag,
        }
        self.operations.append(receipt)
        return receipt

    def fail(self, exc: Exception) -> ToolExecutionError:
        if isinstance(exc, StalePreferenceStateError):
            self.terminal_outcome = 'stale_state'
        elif isinstance(exc, BudgetExhaustedError):
            self.terminal_outcome = 'budget_exhausted'
        elif isinstance(exc, MemoryPartialApplyError):
            self.terminal_outcome = 'partial'
        else:
            self.terminal_outcome = self.terminal_outcome or 'failed'
        self.terminal_error = str(exc)
        return ToolExecutionError(str(exc))


class PreferenceOrganizerAnalyzeTools:
    __public_apis__ = [
        'read_preference_state',
        'read_memory_reference',
        'submit_preference_plan',
    ]

    def __init__(self, gate: PreferenceOrganizerGate):
        self.gate = gate

    def __lazy_source__(self) -> bool:
        return False

    @tool_concurrency(read_keys=('memory', 'memory/users/preference.yaml'))
    def read_preference_state(self) -> dict[str, Any]:
        """Read the complete Preference index and current item count."""
        snapshot = load_preference_state()
        return {
            'stored_items': snapshot.data.stored_items,
            'etag': snapshot.data.etag,
            'preferences': [item.__dict__ for item in snapshot.items],
        }

    def read_memory_reference(self, refs: str | list[str]) -> dict[str, Any]:
        """Read up to ten exact Preference refs when summaries are insufficient."""
        return MemoryTools().read_memory_reference(refs)

    def submit_preference_plan(self, markdown: str) -> dict[str, Any]:
        """Submit the complete Markdown Plan and enable the write tools for this pass."""
        return self.gate.submit(markdown, load_preference_state())


class PreferenceOrganizerApplyTools:
    __public_apis__ = [
        'merge_preferences',
        'move_preference_to_episode',
        'delete_preference',
    ]

    __key_source__ = lambda self: self.gate.phase == 'apply'  # noqa: E731

    def __init__(self, gate: PreferenceOrganizerGate):
        self.gate = gate

    def __lazy_source__(self) -> bool:
        return False

    @tool_concurrency(write_keys=[
        ('memory', 'memory/users/preference.yaml'),
        ('memory-reference-collection',),
    ])
    def merge_preferences(
        self,
        operation_id: str,
    ) -> dict[str, Any]:
        """Merge 2-10 same-scope Preferences into one new Preference.

        operation_id must be the next exact merge entry in AUTHORIZED OPERATIONS.
        All write arguments are loaded from that gated Plan entry.
        """
        try:
            snapshot, operation = self.gate.require_operation('merge', operation_id)
            normalized_sources = operation['source_names']
            normalized_name = operation['name']
            self.gate.budget.require(len(normalized_sources))
            if snapshot.data.stored_items - len(normalized_sources) + 1 < self.gate.hard_min_items:
                raise ValueError('merge would reduce Preference below the hard minimum.')
            _merge_preferences(
                snapshot,
                source_names=normalized_sources,
                name=normalized_name,
                summary=operation['summary'],
                scenario=operation['scenario'],
                details=operation['details'],
                reason=operation['reason'],
            )
            receipt = self.gate.record(
                'merge', [*normalized_sources, normalized_name], len(normalized_sources),
            )
            return {'status': 'applied', **receipt}
        except Exception as exc:
            raise self.gate.fail(exc) from exc

    @tool_concurrency(write_keys=[
        ('memory', 'memory/users/preference.yaml'),
        ('memory-reference-collection',),
        ('episode-memory',),
    ])
    def move_preference_to_episode(self, operation_id: str) -> dict[str, Any]:
        """Apply the next exact move_to_episode entry from the gated Plan."""
        try:
            snapshot, operation = self.gate.require_operation(
                'move_to_episode', operation_id,
            )
            normalized_name = operation['name']
            self.gate.budget.require(1)
            if snapshot.data.stored_items - 1 < self.gate.hard_min_items:
                raise ValueError('move would reduce Preference below the hard minimum.')
            episode_id = _move_preference_to_episode(
                snapshot, normalized_name, operation['episode_summary'],
            )
            receipt = self.gate.record('move_to_episode', [normalized_name], 1)
            return {'status': 'applied', 'episode_id': episode_id, **receipt}
        except Exception as exc:
            raise self.gate.fail(exc) from exc

    @tool_concurrency(write_keys=[
        ('memory', 'memory/users/preference.yaml'),
        ('memory-reference-collection',),
    ])
    def delete_preference(
        self,
        operation_id: str,
    ) -> dict[str, Any]:
        """Apply the next exact delete entry from the gated Plan."""
        try:
            snapshot, operation = self.gate.require_operation('delete', operation_id)
            normalized_name = operation['name']
            reason = operation['reason_code']
            replacement = operation['retained_or_replacement_name']
            names = [normalized_name, *([replacement] if replacement else [])]
            self.gate.budget.require(1)
            if snapshot.data.stored_items - 1 < self.gate.hard_min_items:
                raise ValueError('delete would reduce Preference below the hard minimum.')
            try:
                MemoryStore().remove_preference_with_reference(normalized_name)
                changes = 1
            except FileNotFoundError:
                changes = 0
            receipt = self.gate.record('delete', names, changes)
            return {'status': 'applied' if changes else 'idempotent', 'reason_code': reason, **receipt}
        except Exception as exc:
            raise self.gate.fail(exc) from exc


_AUTHORIZED_OPERATIONS_RE = re.compile(
    r'(?ims)^#{1,6}\s+AUTHORIZED OPERATIONS\s*$.*?```json\s*(\[.*?\])\s*```'
)
_OPERATION_ID_RE = re.compile(r'^[A-Za-z0-9._-]{1,64}$')


def _parse_authorized_operations(
    markdown: str,
    snapshot: PreferenceStateSnapshot,
    *,
    hard_min_items: int,
    changes_remaining: int,
) -> list[dict[str, Any]]:
    match = _AUTHORIZED_OPERATIONS_RE.search(markdown)
    if match is None:
        raise ValueError(
            'Plan must contain an AUTHORIZED OPERATIONS heading followed by one JSON list.'
        )
    try:
        raw_operations = json.loads(match.group(1))
    except json.JSONDecodeError as exc:
        raise ValueError(f'AUTHORIZED OPERATIONS is not valid JSON: {exc.msg}.') from exc
    if not isinstance(raw_operations, list):
        raise ValueError('AUTHORIZED OPERATIONS must be a JSON list.')

    known_names = {item.name for item in snapshot.items}
    count = snapshot.data.stored_items
    change_count = 0
    operation_ids: set[str] = set()
    authorized: list[dict[str, Any]] = []
    for raw in raw_operations:
        if not isinstance(raw, dict):
            raise ValueError('Every authorized operation must be a JSON object.')
        operation_id = str(raw.get('operation_id') or '').strip()
        action = str(raw.get('action') or '').strip()
        if not _OPERATION_ID_RE.fullmatch(operation_id) or operation_id in operation_ids:
            raise ValueError('Every operation_id must be unique and use 1-64 safe characters.')
        operation_ids.add(operation_id)
        if action == 'merge':
            allowed = {
                'operation_id', 'action', 'source_names', 'name', 'summary',
                'scenario', 'details', 'reason',
            }
            _require_exact_keys(raw, allowed)
            source_names = [
                validate_preference_name(value) for value in raw['source_names']
            ] if isinstance(raw.get('source_names'), list) else []
            if len(source_names) != len(set(source_names)) or not 2 <= len(source_names) <= 10:
                raise ValueError('merge source_names must contain 2-10 unique names.')
            if not set(source_names).issubset(known_names):
                raise ValueError('merge source_names must exist at Gate time.')
            name = validate_preference_name(raw.get('name'))
            if name in known_names or name in source_names:
                raise ValueError('merge name must be new at Gate time.')
            summary = _required_text(raw.get('summary'), 'summary')
            if len(summary) > 100:
                raise ValueError('merge summary must be at most 100 characters.')
            operation = {
                'operation_id': operation_id,
                'action': action,
                'source_names': source_names,
                'name': name,
                'summary': summary,
                **{
                    key: _required_text(raw.get(key), key)
                    for key in ('scenario', 'details', 'reason')
                },
            }
            known_names.difference_update(source_names)
            known_names.add(name)
            count -= len(source_names) - 1
            change_count += len(source_names)
        elif action == 'move_to_episode':
            _require_exact_keys(raw, {
                'operation_id', 'action', 'name', 'episode_summary',
            })
            name = validate_preference_name(raw.get('name'))
            if name not in known_names:
                raise ValueError('move_to_episode name must exist at Gate time.')
            episode_summary = _required_text(raw.get('episode_summary'), 'episode_summary')
            if len(episode_summary) > 200:
                raise ValueError('episode_summary must be at most 200 characters.')
            operation = {
                'operation_id': operation_id,
                'action': action,
                'name': name,
                'episode_summary': episode_summary,
            }
            known_names.remove(name)
            count -= 1
            change_count += 1
        elif action == 'delete':
            _require_exact_keys(raw, {
                'operation_id', 'action', 'name', 'reason_code',
                'retained_or_replacement_name',
            })
            name = validate_preference_name(raw.get('name'))
            if name not in known_names:
                raise ValueError('delete name must exist at Gate time.')
            reason_code = str(raw.get('reason_code') or '').strip()
            if reason_code not in {'duplicate', 'superseded', 'expired', 'invalid'}:
                raise ValueError('delete reason_code is not allowed.')
            replacement = str(raw.get('retained_or_replacement_name') or '').strip()
            if reason_code in {'duplicate', 'superseded'}:
                replacement = validate_preference_name(replacement)
                if replacement == name or replacement not in known_names:
                    raise ValueError('delete replacement must name a distinct retained Preference.')
            elif replacement:
                raise ValueError('delete replacement must be blank for expired or invalid items.')
            operation = {
                'operation_id': operation_id,
                'action': action,
                'name': name,
                'reason_code': reason_code,
                'retained_or_replacement_name': replacement,
            }
            known_names.remove(name)
            count -= 1
            change_count += 1
        else:
            raise ValueError(f'unsupported authorized action: {action!r}.')
        if count < hard_min_items:
            raise ValueError('Authorized Plan would reduce Preference below the hard minimum.')
        if change_count > changes_remaining:
            raise ValueError('Authorized Plan exceeds the remaining change budget.')
        authorized.append(operation)
    return authorized


def _require_exact_keys(raw: dict[str, Any], expected: set[str]) -> None:
    if set(raw) != expected:
        raise ValueError(
            f'authorized operation keys must be exactly {sorted(expected)}.'
        )


def _required_text(value: Any, field_name: str) -> str:
    normalized = str(value or '').strip()
    if not normalized:
        raise ValueError(f'{field_name} must not be blank.')
    return normalized


def _merge_preferences(
    snapshot: PreferenceStateSnapshot,
    *,
    source_names: list[str],
    name: str,
    summary: str,
    scenario: str,
    details: str,
    reason: str,
) -> None:
    items = list(snapshot.items)
    by_name = {item.name: item for item in items}
    missing = [source_name for source_name in source_names if source_name not in by_name]
    if missing:
        raise StalePreferenceStateError(f'merge sources no longer exist: {missing}')
    if name in by_name:
        raise ValueError(f'merge target {name!r} already exists.')
    source_items = [by_name[source_name] for source_name in source_names]
    earliest_index = min(items.index(item) for item in source_items)
    earliest = min(source_items, key=lambda item: item.created_at)
    source_references = {
        item.name: _read_valid_reference(item.ref)
        for item in source_items
    }
    frontmatter, _body = parse_yaml_frontmatter(source_references[earliest.name])
    source = frontmatter.get('source') if isinstance(frontmatter, dict) else {}
    source = source if isinstance(source, dict) else {}
    source_kind = str(source.get('kind') or '').strip()
    conversation_id = str(source.get('conversation_id') or '').strip()
    updated_at = datetime.now(timezone.utc).isoformat(timespec='seconds')
    reference_name = preference_name_to_reference_name(name)
    new_item = PreferenceItem(
        name=name,
        summary=str(summary or '').strip(),
        ref=f'references/{reference_name}.md',
        created_at=earliest.created_at,
        updated_at=updated_at,
    )
    reference_content = build_preference_reference_content(
        preference_name=name,
        summary=summary,
        scenario=scenario,
        details=details,
        reason=reason,
        created_at=earliest.created_at,
        updated_at=updated_at,
        source_kind=source_kind,
        conversation_id=conversation_id,
    )
    source_set = set(source_names)
    remaining = [item for item in items if item.name not in source_set]
    remaining.insert(earliest_index, new_item)
    content = render_preference_index(snapshot.content, remaining)
    store = MemoryStore()
    store.write(build_reference_path(reference_name), reference_content)
    try:
        store.write('memory/users/preference.yaml', content)
    except Exception:
        store.delete_reference(reference_name)
        raise
    failed_refs: list[str] = []
    for item in source_items:
        try:
            store.delete_reference(reference_name_from_item(item))
        except Exception:
            failed_refs.append(item.ref)
    if failed_refs:
        raise MemoryPartialApplyError(
            f'merge applied but source references could not be removed: {failed_refs}',
            operation='merge',
            applied=['new_reference', 'preference_index'],
            failed=['source_reference_cleanup'],
            item=new_item,
        )


def _move_preference_to_episode(
    snapshot: PreferenceStateSnapshot,
    name: str,
    episode_summary: str,
) -> str:
    item = next((candidate for candidate in snapshot.items if candidate.name == name), None)
    if item is None:
        raise StalePreferenceStateError(f'preference {name!r} no longer exists.')
    reference_content = _read_valid_reference(item.ref)
    frontmatter, _body = parse_yaml_frontmatter(reference_content)
    source = frontmatter.get('source') if isinstance(frontmatter, dict) else {}
    source = source if isinstance(source, dict) else {}
    occurred_at = datetime.fromisoformat(item.created_at.replace('Z', '+00:00'))
    create_result = get_episode_store().create(
        str(_agentic_value('user_id')),
        EpisodeCreateInput(
            occurred_at_ms=int(occurred_at.timestamp() * 1000),
            episode_type=EpisodeType.DECISION,
            summary=str(episode_summary or '').strip(),
            source=EpisodeSource(
                kind=str(source.get('kind') or '').strip(),
                conversation_id=str(source.get('conversation_id') or '').strip(),
            ),
        ),
    )
    try:
        MemoryStore().remove_preference_with_reference(name)
    except Exception as exc:
        raise MemoryPartialApplyError(
            'Episode was created but the source Preference could not be removed.',
            operation='move_to_episode',
            applied=['episode'],
            failed=['preference_cleanup'],
            item=item,
        ) from exc
    return create_result.id


def _read_valid_reference(ref: str) -> str:
    content = MemoryStore().read_reference(ref)
    error = validate_reference_content(content)
    if error:
        raise ValueError(f'Preference Reference {ref!r} is invalid: {error}')
    return content


def _agentic_value(key: str) -> Any:
    import lazyllm

    config = lazyllm.globals.get('agentic_config') or {}
    return config.get(key) if isinstance(config, dict) else None
