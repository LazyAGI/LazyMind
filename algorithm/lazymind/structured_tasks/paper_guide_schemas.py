"""Strict wire shapes for the paper guide; Core owns the source catalog."""
import re
from typing import Annotated, Literal

from pydantic import AfterValidator, BaseModel, ConfigDict, Field, StringConstraints, field_validator, model_validator


Role = Literal['background', 'question', 'contribution', 'justification']
ROLES = ('background', 'question', 'contribution', 'justification')
Argument = Literal['method_system', 'experiment_finding', 'observational_quantitative', 'qualitative_case',
                   'theory_mathematics', 'historical_humanities_normative', 'review_synthesis',
                   'dataset_benchmark_resource']
Attribution = Literal['paper_statement', 'teaching', 'interpretation', 'external_background']
Support = Literal['supported', 'partial', 'unsupported', 'unavailable', 'not_applicable']
BlockKind = Literal['paragraph', 'heading', 'list', 'example', 'callout', 'detail', 'figure', 'formula', 'table']
Issue = Literal['unsupported', 'overstatement', 'missing_condition', 'attribution', 'causal_overreach',
                'visual_inference', 'coverage', 'coherence', 'missing_explanation', 'inconsistency']
ID = Annotated[str, StringConstraints(pattern=r'^[A-Za-z0-9_-]{1,80}$')]
SourceID = Annotated[str, StringConstraints(pattern=r'^ev_[a-f0-9]{64}$')]
RawText = Annotated[str, StringConstraints(max_length=262144)]
PageIndex = Annotated[int, Field(ge=0, le=100000)]
UNSAFE = re.compile(r'</?[a-z][a-z0-9:-]*(?:[\s/][^<>]*)?>|<\s*[!?]|(?:javascript|vbscript|data|file):'
                    r'|!?\[[^\]\n]*\]\s*(?:\(|\[)'
                    r'|(?m:^\s*\[[^\]\n]+\]:)|!\[\[|(?:https?://|www\.)|`{3}|(?m:^\s*~{3})'
                    r'|[\x00\u202a\u202b\u202d\u202e\u2066-\u2069]', re.I)


INLINE_MATH = re.compile(r'\$[^$\n]+\$')
MATH_ANGLE_VARIABLE = re.compile(r'<[c-hj-ortv-z]>', re.I)


def safe_prose(value):
    checked = INLINE_MATH.sub(lambda match: MATH_ANGLE_VARIABLE.sub('x', match[0]), value)
    if not value.strip() or UNSAFE.search(checked):
        raise ValueError('expected safe prose')
    return value


def prose(max_length):
    return Annotated[str, StringConstraints(max_length=max_length), AfterValidator(safe_prose)]


class Shape(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class Cell(Shape):
    row: PageIndex
    column: PageIndex


class Page(Shape):
    page: PageIndex
    kind: Literal['page']


class TextSpan(Shape):
    page: PageIndex
    kind: Literal['text_span']
    start: Annotated[int, Field(ge=0)] = 0
    end: Annotated[int, Field(gt=0)]
    bbox: Annotated[list[float], Field(min_length=4, max_length=4)]

    @model_validator(mode='after')
    def check_span(self):
        if self.start >= self.end:
            raise ValueError('inverted text range')
        return self


class TableCells(Shape):
    page: PageIndex
    kind: Literal['table_cells']
    bbox: Annotated[list[float], Field(min_length=4, max_length=4)]
    cells: Annotated[list[Cell], Field(max_length=64)] = Field(default_factory=list)
    headers: Annotated[list[RawText], Field(max_length=64)] = Field(default_factory=list)


class Formula(Shape):
    page: PageIndex
    kind: Literal['formula']
    bbox: Annotated[list[float], Field(min_length=4, max_length=4)] | None = None
    latex: RawText


class Figure(Shape):
    page: PageIndex
    kind: Literal['figure_region']
    bbox: Annotated[list[float], Field(min_length=4, max_length=4)]


class Evidence(Shape):
    id: SourceID
    source_block_id: Annotated[str, StringConstraints(min_length=1, max_length=256)]
    source_type: Annotated[str, StringConstraints(max_length=80)]
    text: RawText
    locator: Annotated[Page | TextSpan | TableCells | Formula | Figure, Field(discriminator='kind')]


class Coverage(Shape):
    body: bool
    conclusion: bool
    limitations: bool
    references: bool
    appendix: bool
    abstract_only: bool
    page_warnings: list[PageIndex]
    warnings: list[RawText]


class SourceSection(Shape):
    id: SourceID
    title: RawText
    kind: Literal['abstract', 'conclusion', 'limitations', 'references', 'appendix', 'section', 'page_group']
    pages: list[PageIndex]
    evidence_ids: list[SourceID]


class Claim(Shape):
    id: ID
    statement: prose(1200)
    attribution: Attribution
    evidence_ids: Annotated[list[SourceID], Field(max_length=32)]
    argument_type: Argument | None
    conditions: Annotated[list[prose(400)], Field(max_length=16)]
    support_status: Support

    @model_validator(mode='after')
    def check_support(self):
        if self.support_status in ('supported', 'partial') and (
            not self.evidence_ids or self.attribution in ('teaching', 'external_background')
        ):
            raise ValueError('unbound support claim')
        if self.attribution == 'paper_statement' and (self.support_status == 'not_applicable' or (
            not self.evidence_ids and self.support_status != 'unavailable'
        )):
            raise ValueError('unbound paper claim')
        return self


class Extract(Shape):
    claims: Annotated[list[Claim], Field(max_length=48)]
    open_questions: Annotated[list[prose(500)], Field(max_length=24)]


class Profiles(Shape):
    primary: Argument | None
    secondary: Annotated[list[Argument], Field(max_length=7)]
    uncertainty_reason: Annotated[str, StringConstraints(max_length=600)]

    @model_validator(mode='after')
    def check_classification(self):
        if self.uncertainty_reason:
            safe_prose(self.uncertainty_reason)
        if self.primary is None and (not self.uncertainty_reason or self.secondary):
            raise ValueError('uncertain profile needs a reason')
        if len(set(self.secondary)) != len(self.secondary) or self.primary in self.secondary:
            raise ValueError('duplicate profiles')
        return self


class SectionPlan(Shape):
    role: Role
    title: prose(180)
    focus: prose(1000)
    terms: Annotated[list[prose(220)], Field(max_length=32)]
    claim_ids: Annotated[list[ID], Field(max_length=96)]
    evidence_ids: Annotated[list[SourceID], Field(max_length=128)]
    asset_ids: Annotated[list[SourceID], Field(max_length=32, description=(
        'Every ID must have exactly one required_assets decision with included=true. '
        'Excluded or undecided assets cannot appear here.'))]

    @field_validator('claim_ids', 'evidence_ids', 'asset_ids', mode='after')
    @classmethod
    def canonical_reference_sets(cls, values):
        # Runs AFTER raw list length, strict element types and ID format checks.
        # Preserve first-use order; this never merges source/claim definitions.
        return list(dict.fromkeys(values))


class AssetDecision(Shape):
    id: Annotated[SourceID, Field(description=(
        'Supplied evidence with source_type image/chart/formula/table only, even when excluded; '
        'never a text-only caption or mention.'))]
    included: Annotated[bool, Field(description=(
        'False means a supplied catalog asset is not displayed, not an unavailable external resource.'))]
    reason: prose(500)


class Plan(Shape):
    profiles: Profiles
    questions: Annotated[list[prose(600)], Field(min_length=1, max_length=16)]
    claims: Annotated[list[Claim], Field(max_length=96)]
    sections: Annotated[list[SectionPlan], Field(min_length=4, max_length=4)]
    term_policy: Annotated[list[prose(600)], Field(max_length=32)]
    required_assets: Annotated[list[AssetDecision], Field(max_length=128, description=(
        'One decision per asset ID. Every included=true ID must occur in a section asset_ids; '
        'every section asset_ids entry must have an included=true decision here.'))]

    @model_validator(mode='after')
    def check_roles_and_coverage(self):
        if tuple(s.role for s in self.sections) != ROLES:
            raise ValueError('section order')
        claims = unique(c.id for c in self.claims)
        unique(a.id for a in self.required_assets)
        included = {a.id for a in self.required_assets if a.included}
        if bind_sections(self.sections, 'claim_ids', claims) != claims:
            raise ValueError('claim coverage')
        if bind_sections(self.sections, 'asset_ids', included) != included:
            raise ValueError('asset coverage')
        return self


class Block(Shape):
    id: ID
    kind: BlockKind
    text: prose(7500)
    attribution: Attribution
    claim_ids: Annotated[list[ID], Field(max_length=48)]
    evidence_ids: Annotated[list[SourceID], Field(max_length=32)]
    asset_id: SourceID | None = None


class Section(Shape):
    role: Role
    title: prose(180)
    blocks: Annotated[list[Block], Field(min_length=1, max_length=64)]
    claims: Annotated[list[Claim], Field(max_length=48, description=(
        'Definitions of NEW claims only. Never repeat or redefine a plan claim here. '
        'Reference existing plan claims through claim_ids; use [] when no new claims are needed.'))]
    claim_ids: Annotated[list[ID], Field(max_length=96)]
    summary: prose(1200)


class SectionInputVersion(Section):
    version: Annotated[int, Field(ge=1)] | None = None
    publication_status: Annotated[str, StringConstraints(max_length=64)] | None = None


class Finding(Shape):
    target_id: ID
    target_type: Literal['claim', 'block']
    issue_type: Issue
    suggestion: prose(800)


class Review(Shape):
    findings: Annotated[list[Finding], Field(max_length=48)]


class GuideInput(Shape):
    language: Literal['en', 'zh']
    evidence: Annotated[list[Evidence], Field(max_length=2048)]
    coverage: Coverage | None = None
    source_sections: Annotated[list[SourceSection], Field(max_length=512)] = Field(default_factory=list)
    findings: Annotated[list[Finding], Field(max_length=48)] = Field(default_factory=list)

    @model_validator(mode='after')
    def check_evidence_catalog(self):
        unique(e.id for e in self.evidence)
        unique(s.id for s in self.source_sections)
        return self


class PlanInput(GuideInput):
    extracts: Annotated[list[Extract], Field(max_length=8)] = Field(default_factory=list)


class SectionInput(GuideInput):
    plan: Plan
    role: Role
    prior_summary: Annotated[str, StringConstraints(max_length=4096)] = ''


class ReviewInput(GuideInput):
    plan: Plan
    sections: Annotated[list[SectionInputVersion], Field(min_length=1, max_length=4)]

    @model_validator(mode='after')
    def check_review_scope(self):
        if len(self.sections) != 1 and tuple(s.role for s in self.sections) != ROLES:
            raise ValueError('review requires one section or the complete ordered guide')
        planned = {claim.id for claim in self.plan.claims}
        generated = unique(claim.id for section in self.sections for claim in section.claims)
        if planned & generated:
            raise ValueError('review claim ID collides with plan')
        unique(block.id for section in self.sections for block in section.blocks)
        return self


def unique(values):
    values = list(values)
    keys = set(values)
    if len(values) != len(keys):
        raise ValueError('duplicate IDs')
    return keys


def refs(values, allowed):
    keys = unique(values)
    if not keys <= allowed:
        raise ValueError('unknown ID')
    return keys


def bind_sections(sections, field, allowed):
    used = set()
    for section in sections:
        used |= refs(getattr(section, field), allowed)
    return used


def bind_output(data, result):
    """Outputs bind only to the supplied packet; Core independently binds to the snapshot."""
    evidence = {item.id for item in data.evidence}
    asset_kind = {'chart': 'figure', 'image': 'figure', 'formula': 'formula', 'table': 'table'}
    assets = {item.id: asset_kind[item.source_type] for item in data.evidence if item.source_type in asset_kind}
    if isinstance(result, Review):
        claims = {c.id for c in data.plan.claims}
        claims.update(c.id for s in data.sections for c in s.claims)
        blocks = {b.id for s in data.sections for b in s.blocks}
        for finding in result.findings:
            if finding.target_id not in (claims if finding.target_type == 'claim' else blocks):
                raise ValueError('review target outside scope')
        return
    claim_ids = unique(c.id for c in result.claims)
    for claim in result.claims:
        refs(claim.evidence_ids, evidence)
    if isinstance(result, Plan):
        refs([a.id for a in result.required_assets], assets.keys())
        bind_sections(result.sections, 'evidence_ids', evidence)
    if isinstance(result, Section):
        expected = next(s for s in data.plan.sections if s.role == data.role)
        if result.role != data.role or result.title != expected.title:
            raise ValueError('wrong role or title')
        plan_claims = {c.id for c in data.plan.claims}
        if plan_claims & claim_ids:
            raise ValueError('new claims cannot overwrite plan claims')
        section_claims = refs(result.claim_ids, plan_claims | claim_ids)
        unique(b.id for b in result.blocks)
        used = bind_sections(result.blocks, 'claim_ids', section_claims)
        if used != section_claims or not claim_ids <= used:
            raise ValueError('unbound section claims')
        if not set(expected.claim_ids) <= used:
            raise ValueError('missing planned claim')
        for claim in data.plan.claims:
            if claim.id in used:
                refs(claim.evidence_ids, evidence)
        bind_sections(result.blocks, 'evidence_ids', evidence)
        for block in result.blocks:
            if block.attribution == 'paper_statement' and not (block.claim_ids or block.evidence_ids):
                raise ValueError('unbound paper block')
            if block.kind in asset_kind.values():
                if assets.get(block.asset_id) != block.kind or block.asset_id not in expected.asset_ids:
                    raise ValueError('wrong or unplanned asset')
            elif block.asset_id is not None:
                raise ValueError('asset on text block')
