"""Strict wire shapes for the paper guide; Core owns the source catalog."""
import re
from typing import Annotated, Literal

from markdown_it import MarkdownIt
from markdown_it.token import Token
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
    parent_asset_id: SourceID | None = None
    fragment_evidence_ids: Annotated[list[SourceID], Field(max_length=2048)] | None = None
    source_region_id: Annotated[str, StringConstraints(max_length=80)] = ''
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


class OriginalAssetCandidate(Shape):
    id: SourceID
    mode: Literal['region', 'page']


class OriginalAssetCandidates(Shape):
    figure: Annotated[list[OriginalAssetCandidate], Field(max_length=4)]
    formula: Annotated[list[OriginalAssetCandidate], Field(max_length=6)]


class GuideInput(Shape):
    language: Literal['en', 'zh']
    evidence: Annotated[list[Evidence], Field(max_length=2048)]
    coverage: Coverage | None = None
    source_sections: Annotated[list[SourceSection], Field(max_length=512)] = Field(default_factory=list)
    findings: Annotated[list[Finding], Field(max_length=48)] = Field(default_factory=list)
    original_asset_candidates: OriginalAssetCandidates | None = None

    @model_validator(mode='after')
    def check_evidence_catalog(self):
        unique(e.id for e in self.evidence)
        unique(s.id for s in self.source_sections)
        catalog = {e.id: e for e in self.evidence}
        for e in self.evidence:
            unique(e.fragment_evidence_ids or [])
            if e.fragment_evidence_ids and (e.source_type != 'table' or e.parent_asset_id):
                raise ValueError('fragment list requires parent table')
            for child_id in e.fragment_evidence_ids or []:
                child = catalog.get(child_id)
                if child is None or child.parent_asset_id != e.id or child.source_type != 'text':
                    raise ValueError('unknown or cross-parent table fragment')
            if e.parent_asset_id:
                parent = catalog.get(e.parent_asset_id)
                if (parent is None or parent.source_type != 'table' or e.source_type != 'text'
                        or e.id not in (parent.fragment_evidence_ids or []) or e.locator.kind != 'page'
                        or not e.source_region_id or e.source_block_id != parent.source_block_id):
                    raise ValueError('invalid table fragment parent')
        candidates = self.original_asset_candidates
        if candidates is not None:
            kinds = _asset_kinds(self.evidence)
            unique(c.id for c in candidates.figure + candidates.formula)
            for kind in ('figure', 'formula'):
                if any(kinds.get(c.id) != kind for c in getattr(candidates, kind)):
                    raise ValueError('original asset candidate outside packet or kind')
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


ASSET_KINDS = {'chart': 'figure', 'image': 'figure', 'formula': 'formula', 'table': 'table'}


def _asset_kinds(evidence):
    return {item.id: ASSET_KINDS[item.source_type] for item in evidence if item.source_type in ASSET_KINDS}


def section_asset_kinds(data: SectionInput) -> dict[str, str]:
    """Current role's catalog decisions, intersected with actual packet assets."""
    expected = next(s for s in data.plan.sections if s.role == data.role)
    assets = _asset_kinds(data.evidence)
    return {source_id: assets[source_id] for source_id in expected.asset_ids if source_id in assets}


_ASCII_SPACE = ' \t\r\n\v\f'
_LINE_END = re.compile(r'\r\n?|\n')
_SENTENCE_END = '。！？.!?'
_ALIAS_GROUP = r'〔[ \t]*e[1-9][0-9]*(?:[ \t]*[、，,][ \t]*e[1-9][0-9]*)*[ \t]*〕'
_CITATION_TAIL = re.compile(rf'[{_SENTENCE_END}](?:[ \t]*{_ALIAS_GROUP})+\Z')
_CITATION_MARKDOWN = MarkdownIt()
_QUOTE_END = {'"': '"', "'": "'", '“': '”', '‘': '’', '「': '」', '『': '』', '«': '»', '‹': '›'}
_DELIMITER = re.compile(r'''\\.|[$]+|`+|["'“”‘’「」『』«»‹›]''')
_PRICE_BEFORE_SENTENCE = re.compile(r'[0-9][0-9,.]*[.!?][ \t]+[^\W\d_]')


def _protected_sentence_end(prefix: str) -> bool:
    """Recognize literals and leave an unresolved delimiter to semantic review."""
    closer = ''
    for match in _DELIMITER.finditer(prefix):
        token = match[0]
        prev = prefix[match.start() - 1] if match.start() else ''
        following = prefix[match.end():match.end() + 1]
        if token in ("'", '’') and prev.isalnum() and following.isalnum():
            continue  # Apostrophe within a word.
        if closer:
            if token == closer:
                closer = ''
            continue
        if token == "'" and prev.isalnum():
            continue  # A trailing possessive apostrophe.
        if token in (r'\(', r'\['):
            closer = r'\)' if token == r'\(' else r'\]'
        elif token[0] in ('$', '`'):
            if (token == '$' and _PRICE_BEFORE_SENTENCE.match(prefix, match.end())
                    and prefix.find('$', match.end()) == -1):
                continue  # An isolated price in an earlier sentence.
            closer = token
        elif token in _QUOTE_END:
            closer = _QUOTE_END[token]
        elif token.startswith('\\') and match.end() == len(prefix) and token[-1] in _SENTENCE_END:
            return True  # Escaped punctuation is not an ordinary sentence boundary.
    return bool(closer)


def _soft_wrap_continues_sentence(inline: Token) -> bool:
    children = inline.children or []
    final_line = inline.content.rsplit('\n', 1)[-1].lstrip(' \t')
    # Require the soft break immediately before the literal source line of the tail.
    # An earlier soft break cannot stand in for a newline consumed by inline code.
    if (len(children) < 2 or children[-2].type != 'softbreak' or
            children[-1].type != 'text' or children[-1].content != final_line):
        return False
    prose_parts = []
    line_content_types = set()
    for child in reversed(children[:-2]):
        if child.type == 'hardbreak':
            break
        if child.type == 'softbreak':
            # A closed code term may occupy a whole soft-wrapped line of the
            # same sentence. Cross only those literal lines to reach its prose.
            if line_content_types != {'code_inline'}:
                break
            line_content_types.clear()
            continue
        if child.type != 'text' or child.content.strip(_ASCII_SPACE):
            line_content_types.add(child.type)
        if child.type == 'text':
            prose_parts.append(child.content)
    previous_prose = ''.join(reversed(prose_parts)).rstrip(_ASCII_SPACE + ''.join(_QUOTE_END.values()))
    return not previous_prose.endswith(tuple(_SENTENCE_END)) and any(ch.isalnum() for ch in previous_prose)


def _citation_tail_line(text: str) -> str | None:
    end = text.rstrip(_ASCII_SPACE)
    candidate = _CITATION_TAIL.search(end)
    if candidate is None:
        return None
    lines = _LINE_END.split(end)
    line = lines[-1]
    last_line = len(lines) - 1
    before_punctuation = line[:len(line) - len(candidate[0])]
    has_sentence = any(ch.isalnum() for ch in before_punctuation)
    # Use source ranges so a lazy continuation stays quoted, while a
    # heading or a quoted blank line can start a different paragraph.
    tokens = _CITATION_MARKDOWN.parse(end)
    for index, token in enumerate(tokens):
        if token.map is None or not token.map[0] <= last_line < token.map[1]:
            continue
        if token.type == 'blockquote_open':
            return None
        if token.type == 'paragraph_open':
            if not has_sentence and token.map[0] < last_line:
                # The paragraph rule emits its inline content immediately after the opening token.
                has_sentence = _soft_wrap_continues_sentence(tokens[index + 1])
            break
    if not has_sentence:
        return None
    if _protected_sentence_end(end[:candidate.start() + 1]):
        return None
    return line.strip(_ASCII_SPACE)


def _reject_section_citation_tails(data: SectionInput, result: Section) -> None:
    evidence = {item.id: item.text for item in data.evidence}
    for block in result.blocks:
        if block.kind != 'paragraph':
            continue
        line = _citation_tail_line(block.text)
        if line is None:
            continue
        # Match only raw, explicitly bound evidence, never Plan context or
        # another block's bindings. A verbatim source line is not a new citation.
        if any(source_line.strip(_ASCII_SPACE) == line
               for source in block.evidence_ids for source_line in _LINE_END.split(evidence[source])):
            continue
        raise ValueError('request-local source alias in paragraph citation tail')


def bind_output(data, result):
    """Outputs bind only to the supplied packet; Core independently binds to the snapshot."""
    evidence = {item.id for item in data.evidence}
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
        kinds = _asset_kinds(data.evidence)
        candidates = data.original_asset_candidates
        allowed = {id_ for id_, kind in kinds.items() if kind == 'table'}
        chosen = {a.id for a in result.required_assets if a.included}
        for kind in ('figure', 'formula'):
            choices = getattr(candidates, kind) if candidates else []
            allowed.update(c.id for c in choices)
            regions = {c.id for c in choices if c.mode == 'region'}
            if regions and not regions & chosen:
                raise ValueError(f'missing planned original region: {kind}')
        refs([a.id for a in result.required_assets], allowed)
        bind_sections(result.sections, 'evidence_ids', evidence)
    if isinstance(result, Section):
        expected = next(s for s in data.plan.sections if s.role == data.role)
        allowed_assets = section_asset_kinds(data)
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
        remaining_originals = {id_ for id_, kind in allowed_assets.items() if kind != 'table'}
        for block in result.blocks:
            if block.attribution == 'paper_statement' and not (block.claim_ids or block.evidence_ids):
                raise ValueError('unbound paper block')
            if block.kind in ASSET_KINDS.values():
                if allowed_assets.get(block.asset_id) != block.kind:
                    raise ValueError('wrong or unplanned asset')
                if block.kind != 'table':
                    if block.asset_id not in remaining_originals or block.asset_id not in block.evidence_ids:
                        raise ValueError('duplicate original or missing self evidence')
                    remaining_originals.remove(block.asset_id)
            elif block.asset_id is not None:
                raise ValueError('asset on text block')
        if remaining_originals:
            raise ValueError('missing planned original')
        _reject_section_citation_tails(data, result)
