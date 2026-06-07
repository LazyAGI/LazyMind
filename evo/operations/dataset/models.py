from __future__ import annotations

from dataclasses import asdict, dataclass
from typing import Any


@dataclass(frozen=True)
class DocumentPage:
    source_id: str
    page_index: int
    documents: list[dict[str, Any]]


@dataclass(frozen=True)
class CorpusLoadReport:
    sources: list[dict[str, Any]]
    filters: dict[str, Any]
    document_page_refs: list[str]
    chunk_page_refs: list[str]
    loaded_doc_refs: list[str]
    stats: dict[str, Any]
    skipped: list[dict[str, Any]]
    errors: list[dict[str, Any]]


@dataclass(frozen=True)
class SourceUnitPage:
    snapshot_id: str
    page_index: int
    source_units: list[dict[str, Any]]


@dataclass(frozen=True)
class CorpusSnapshot:
    snapshot_id: str
    source_report_ref: str
    document_page_refs: list[str]
    source_unit_page_refs: list[str]
    documents: list[dict[str, Any]]
    stats: dict[str, Any]
    skipped: list[dict[str, Any]]
    errors: list[dict[str, Any]]


@dataclass(frozen=True)
class CasePreparation:
    case_id: str
    question_type: str
    difficulty: str
    doc_reference: list[dict[str, Any]]
    context_reference: list[dict[str, Any]]
    instruction: str
    prompt: str
    source_snapshot_ref: str
    source_message_id: str = ''


@dataclass(frozen=True)
class DatasetCase:
    id: str
    question: str
    answer: str
    question_type: str
    difficulty: str
    grading_guidance: str
    reference_context: list[str]
    reference_doc: list[str]
    reference_doc_ids: list[str]
    reference_chunk_ids: list[str]
    generate_reason: str
    source_preparation_ref: str
    source_message_id: str = ''


@dataclass(frozen=True)
class EvalDataset:
    id: str
    size: int
    case_ids: list[str]
    case_refs: list[str]
    stats: dict[str, Any]
    checks: dict[str, Any]
    diff: dict[str, Any]
    preview: list[dict[str, Any]]
    source_message_id: str = ''


def artifact_payload(model: Any) -> dict[str, Any]:
    return asdict(model)
