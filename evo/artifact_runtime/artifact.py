from __future__ import annotations

from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from types import MappingProxyType

from .errors import DefinitionError
from .utils import _positive_int, _string, _text


@dataclass(frozen=True, order=True)
class ArtifactKey:
    artifact_id: str
    partition_key: str = ''

    def __post_init__(self) -> None:
        _text(self.artifact_id, 'artifact_id')
        _string(self.partition_key, 'partition_key')

        if self.partition_key and not self.partition_key.strip():
            raise DefinitionError('partition_key must be non-empty when set')

    @classmethod
    def scalar(cls, artifact_id: str) -> ArtifactKey:
        return cls(artifact_id)

    @classmethod
    def partition(cls, artifact_id: str, partition_key: str) -> ArtifactKey:
        _text(partition_key, 'partition_key')
        return cls(artifact_id, partition_key)


@dataclass(frozen=True, order=True)
class ArtifactRef:
    key: ArtifactKey
    version: int

    def __post_init__(self) -> None:
        if not isinstance(self.key, ArtifactKey):
            raise TypeError('key must be ArtifactKey')
        _positive_int(self.version, 'version')


@dataclass(frozen=True)
class ArtifactRecord:
    ref: ArtifactRef
    producer: str
    input_refs: tuple[ArtifactRef, ...] = ()

    def __post_init__(self) -> None:
        if not isinstance(self.ref, ArtifactRef):
            raise TypeError('ref must be ArtifactRef')

        _text(self.producer, 'producer')
        inputs = tuple(self.input_refs)
        if not all(isinstance(ref, ArtifactRef) for ref in inputs):
            raise TypeError('input_refs must contain ArtifactRef values')

        inputs = tuple(sorted(inputs))
        if len({ref.key for ref in inputs}) != len(inputs):
            raise DefinitionError('input_refs must contain at most one ref per artifact key')

        object.__setattr__(self, 'input_refs', inputs)


@dataclass(frozen=True)
class PartitionSet:
    keys: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        keys = tuple(self.keys)
        for key in keys:
            _text(key, 'partition key')

        if len(set(keys)) != len(keys):
            raise DefinitionError('partition keys must be unique')

        object.__setattr__(self, 'keys', keys)

    def __contains__(self, partition_key: object) -> bool:
        return partition_key in self.keys


@dataclass(frozen=True, order=True)
class PartitionGuard:
    partition_set_key: ArtifactKey
    partition_key: str

    def __post_init__(self) -> None:
        if not isinstance(self.partition_set_key, ArtifactKey):
            raise TypeError('partition_set_key must be ArtifactKey')

        if self.partition_set_key.partition_key:
            raise DefinitionError('partition_set_key must identify a scalar artifact')
        _text(self.partition_key, 'partition_key')


@dataclass(frozen=True)
class ArtifactDraft:
    key: ArtifactKey
    value: object
    input_refs: tuple[ArtifactRef, ...] = ()

    def __post_init__(self) -> None:
        if not isinstance(self.key, ArtifactKey):
            raise TypeError('artifact write key must be ArtifactKey')

        if isinstance(self.value, PartitionSet) and self.key.partition_key:
            raise DefinitionError('PartitionSet must be written as a scalar artifact')

        object.__setattr__(self, 'input_refs', merge_refs(self.input_refs))


@dataclass(frozen=True)
class ArtifactCommit:
    commit_id: str
    producer: str
    writes: tuple[ArtifactDraft, ...]
    expected_heads: Mapping[ArtifactKey, ArtifactRef | None] = field(default_factory=dict)
    partition_guards: tuple[PartitionGuard, ...] = ()

    def __post_init__(self) -> None:
        _text(self.commit_id, 'artifact commit id')
        _text(self.producer, 'artifact commit producer')
        writes = tuple(self.writes)
        if not writes:
            raise DefinitionError('artifact commit must contain at least one write')
        if not all(isinstance(write, ArtifactDraft) for write in writes):
            raise TypeError('artifact commit writes must contain ArtifactDraft values')
        if len({write.key for write in writes}) != len(writes):
            raise DefinitionError('artifact commit write keys must be unique')

        expected_heads = dict(self.expected_heads)
        for key, ref in expected_heads.items():
            if not isinstance(key, ArtifactKey):
                raise TypeError('expected_heads keys must be ArtifactKey values')
            if ref is not None and not isinstance(ref, ArtifactRef):
                raise TypeError('expected_heads values must be ArtifactRef or None')
            if ref is not None and ref.key != key:
                raise DefinitionError('expected head must identify its artifact key')

        guards = tuple(self.partition_guards)
        if not all(isinstance(guard, PartitionGuard) for guard in guards):
            raise TypeError('partition_guards must contain PartitionGuard values')
        if len(set(guards)) != len(guards):
            raise DefinitionError('partition guards must be unique')

        object.__setattr__(self, 'writes', writes)
        object.__setattr__(self, 'expected_heads', MappingProxyType(expected_heads))
        object.__setattr__(self, 'partition_guards', guards)

    @property
    def output_keys(self) -> tuple[ArtifactKey, ...]:
        return tuple(write.key for write in self.writes)


@dataclass(frozen=True)
class ArtifactSnapshot:
    records: Mapping[ArtifactKey, ArtifactRecord] = field(default_factory=dict)
    partition_sets: Mapping[ArtifactKey, PartitionSet] = field(default_factory=dict)

    def __post_init__(self) -> None:
        records = dict(self.records)
        partition_sets = dict(self.partition_sets)
        for key, record in records.items():
            if not isinstance(key, ArtifactKey) or not isinstance(record, ArtifactRecord):
                raise TypeError('records must map ArtifactKey to ArtifactRecord')
            if record.ref.key != key:
                raise DefinitionError('artifact record key must match its ref')
        for key, partitions in partition_sets.items():
            if not isinstance(key, ArtifactKey) or key.partition_key:
                raise TypeError('partition_sets keys must be scalar ArtifactKey values')
            if not isinstance(partitions, PartitionSet):
                raise TypeError('partition_sets values must be PartitionSet')
            if key not in records:
                raise DefinitionError('partition set must reference a visible artifact record')

        object.__setattr__(self, 'records', MappingProxyType(records))
        object.__setattr__(self, 'partition_sets', MappingProxyType(partition_sets))

    def effective_records(self) -> Mapping[ArtifactKey, ArtifactRecord]:
        effective = dict(self.records)
        changed = True
        while changed:
            changed = False
            for key, record in tuple(effective.items()):
                if any(
                    effective.get(ref.key) is None or effective[ref.key].ref != ref
                    for ref in record.input_refs
                ):
                    del effective[key]
                    changed = True
        return MappingProxyType(effective)


def merge_refs(*groups: Iterable[ArtifactRef]) -> tuple[ArtifactRef, ...]:
    refs: dict[ArtifactKey, ArtifactRef] = {}

    for group in groups:
        for ref in group:
            if not isinstance(ref, ArtifactRef):
                raise TypeError('artifact refs must contain ArtifactRef values')
            previous = refs.get(ref.key)
            if previous is not None and previous != ref:
                raise DefinitionError(f'conflicting refs for artifact key {ref.key}')
            refs[ref.key] = ref

    return tuple(sorted(refs.values()))


__all__ = [
    'ArtifactCommit', 'ArtifactDraft', 'ArtifactKey', 'ArtifactRecord', 'ArtifactRef',
    'ArtifactSnapshot', 'PartitionGuard', 'PartitionSet', 'merge_refs',
]
