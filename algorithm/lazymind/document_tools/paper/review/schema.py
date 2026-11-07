"""Shared review contract for the reviewer and downstream revision."""
import hashlib
from typing import Annotated, Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

NonEmptyText = Annotated[str, Field(min_length=1, pattern=r'\S')]
Digest = Annotated[str, Field(pattern=r'^[0-9a-f]{64}$')]


class ReviewIssue(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    severity: Literal['major', 'minor', 'suggestion']
    category: Literal['method', 'evidence', 'analysis', 'structure', 'language', 'citation']
    location: NonEmptyText = Field(description='Manuscript section path; identify whole-document or missing content')
    quote: NonEmptyText | None = Field(description='Verbatim manuscript excerpt; null only for whole/missing content')
    problem: NonEmptyText = Field(
        description='Issue and observable evidence; distinguish errors, clarification, unknowns')
    suggestion: NonEmptyText = Field(
        description='Smallest actionable correction; do not invent materials or experiments')
    action_type: Literal['text_revision', 'needs_material', 'needs_experiment']


FactCategory = Literal['numbers', 'comparison', 'literature', 'data_status', 'figure']
FactStatus = Literal['verified', 'conflict', 'insufficient', 'not_applicable']


class FactSource(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    path: NonEmptyText = Field(description='JSON Pointer into manuscript or its supplied papers/charts')
    quote: NonEmptyText | None = Field(default=None, description='Exact excerpt when the source is text')
    value_text: NonEmptyText | None = Field(default=None, description='Exact quantity within source quote')
    column: NonEmptyText | None = Field(default=None, description='Numeric column in supplied columns/rows table')
    where: dict[str, str | int | float | bool] = Field(default_factory=dict, description=(
        'Exact equality filters in that table; group values must be explicit in the manuscript, not guessed aliases'))


class FactClaim(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    category: FactCategory
    location: NonEmptyText
    quote: NonEmptyText = Field(description='Exact contiguous manuscript excerpt, never a source quotation')
    asserted_text: NonEmptyText = Field(description='Exact number, direction word or status label within quote')
    operation: Literal['value', 'sum', 'mean', 'difference', 'ratio', 'percentage',
                       'percentage_points', 'relative_change', 'direction', 'denominator',
                       'status', 'citation_support', 'figure_value']
    sources: list[FactSource] = Field(min_length=1)


class CheckedFact(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    category: FactCategory
    status: FactStatus
    location: NonEmptyText
    quote: NonEmptyText
    evidence_locations: list[str]
    calculation: NonEmptyText
    suggestion: NonEmptyText
    verification_method: Literal['deterministic', 'semantic_read'] = 'deterministic'
    reading: 'LiteratureReading | None' = None


class LiteratureReading(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    claim_index: int = Field(ge=0)
    status: Literal['verified', 'conflict', 'insufficient']
    reason: NonEmptyText
    passages: list[FactSource]


class LiteratureReadings(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    readings: list[LiteratureReading]


class FactCheck(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    category: FactCategory
    status: FactStatus
    reason: NonEmptyText
    evidence_locations: list[str]
    checks: list[CheckedFact] = Field(default_factory=list)


class ReviewResult(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    summary: NonEmptyText
    issues: list[ReviewIssue]
    limitations: list[NonEmptyText]
    fact_claims: list[FactClaim] = Field(default_factory=list, description=(
        'Claims to check, not verification verdicts. Only host code assigns fact-check status.'))


class ReviewRepair(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    replacements: dict[str, Any] = Field(
        description='Only requested paths and replacements; issues.2 is a complete issue')


class QuoteLines(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    start_line: int = Field(ge=1)
    end_line: int = Field(ge=1)


class RepairedIssue(ReviewIssue):
    quote: QuoteLines | None = Field(
        description='Contiguous source line range copied by code; null only for whole/missing content')


class SavedReviewIssue(ReviewIssue):
    id: Annotated[str, Field(pattern=r'^R[1-9][0-9]*$')]
    disposition: Literal['fact', 'advice', 'unverified'] = 'unverified'


class SavedReview(ReviewResult):
    issues: list[SavedReviewIssue]
    manuscript_sha256: Digest
    review_sha256: Digest
    fact_checks: list[FactCheck] = Field(default_factory=list)

    @model_validator(mode='after')
    def unique_ids(self):
        ids = [issue.id for issue in self.issues]
        if len(ids) != len(set(ids)):
            raise ValueError('审稿问题编号不能重复')
        categories = [check.category for check in self.fact_checks]
        if categories and (len(categories) != 5 or len(set(categories)) != 5):
            raise ValueError('事实核对必须完整包含五类且不能重复')
        return self


def text_digest(value: str) -> str:
    return hashlib.sha256(value.encode('utf-8')).hexdigest()
