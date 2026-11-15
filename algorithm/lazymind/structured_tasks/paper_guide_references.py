"""Guide-only model view. Short reference identities live within one request."""
from copy import deepcopy
import json

from jsonschema import Draft202012Validator, ValidationError

from .paper_guide_schemas import Extract, Plan, Review, Section, plan_asset_kinds, section_asset_kinds

CANONICAL_PATTERN = r'^ev_[a-f0-9]{64}$'
ALIAS_PATTERN = r'^e[1-9][0-9]*$'
OUTPUT_SCHEMAS = {'extract': Extract, 'plan': Plan, 'section': Section, 'review': Review}

# Only these typed reference positions are encoded. Claim/block IDs and all
# prose/LaTeX remain untouched, even when they literally contain e1 or ev_... .
CLAIM_REFS = ('claims', '*', 'evidence_ids', '*')
OUTPUT_PATHS = {
    'extract': (CLAIM_REFS,),
    'plan': (CLAIM_REFS, ('sections', '*', 'evidence_ids', '*'),
             ('sections', '*', 'asset_ids', '*'), ('required_assets', '*', 'id')),
    'section': (CLAIM_REFS, ('blocks', '*', 'evidence_ids', '*'), ('blocks', '*', 'asset_id')),
    'review': (),
}
COMMON_INPUT_PATHS = (('evidence', '*', 'id'), ('evidence', '*', 'parent_asset_id'),
                      ('evidence', '*', 'fragment_evidence_ids', '*'),
                      ('source_sections', '*', 'evidence_ids', '*'),
                      ('original_asset_candidates', 'figure', '*', 'id'),
                      ('original_asset_candidates', 'formula', '*', 'id'))
INPUT_PATHS = {
    'extract': COMMON_INPUT_PATHS,
    'plan': COMMON_INPUT_PATHS + (('extracts', '*') + CLAIM_REFS,),
    'section': COMMON_INPUT_PATHS + tuple(('plan',) + path for path in OUTPUT_PATHS['plan']),
    'review': (COMMON_INPUT_PATHS + tuple(('plan',) + path for path in OUTPUT_PATHS['plan'])
               + tuple(('sections', '*') + path for path in OUTPUT_PATHS['section'])),
}


def _positions(value, path):
    """Walk a fixed shape, never descend through arbitrary source text/objects."""
    head, *tail = path
    if head == '*':
        if not isinstance(value, list):
            raise ValueError('reference list type')
        keys = range(len(value))
    else:
        if not isinstance(value, dict):
            raise ValueError('reference object type')
        keys = (head,) if head in value else ()
    for key in keys:
        if tail:
            yield from _positions(value[key], tail)
        elif value[key] is not None:
            yield value, key


def model_output_schema(schema):
    """Derive model syntax from the canonical schema without weakening it.

    Only the exact SourceID pattern changes. Tests cross-check every such path
    against the explicit transformation tables, including nested definitions.
    """
    contract = deepcopy(schema.model_json_schema())

    def visit(node):
        if isinstance(node, dict):
            if node.get('pattern') == CANONICAL_PATTERN:
                node['pattern'] = ALIAS_PATTERN
            for child in node.values():
                visit(child)
        elif isinstance(node, list):
            for child in node:
                visit(child)

    visit(contract)
    return contract


class SourceReferences:
    def __init__(self, task, canonical_input):
        self.task = task
        self.model_input = canonical_input.model_dump(exclude_none=True)
        canonical_to_alias = {}
        self._alias_to_canonical = {}
        # Input has already passed its canonical schema. Include context-only
        # references for fidelity; bind_output still restricts citations to the
        # original evidence packet after decoding, not this larger dictionary.
        for path in INPUT_PATHS[task]:
            for parent, key in _positions(self.model_input, path):
                canonical = parent[key]
                if canonical not in canonical_to_alias:
                    alias = f'e{len(canonical_to_alias) + 1}'
                    canonical_to_alias[canonical] = alias
                    self._alias_to_canonical[alias] = canonical
                parent[key] = canonical_to_alias[canonical]
        for index, section in enumerate(self.model_input.get('source_sections', []), 1):
            section['id'] = f's{index}'
        for evidence in self.model_input['evidence']:
            evidence.pop('source_block_id')
            evidence.pop('source_region_id', None)
        self.asset_instruction = ''
        if task in ('plan', 'section'):
            if task == 'plan':
                kinds = plan_asset_kinds(canonical_input)
                label = 'Allowed required_assets decisions by source alias (JSON): '
            else:
                kinds = section_asset_kinds(canonical_input)
                label = 'Allowed visual blocks by source alias for the requested role (JSON): '
            # The instruction contains only actual evidence aliases and fixed kinds.
            assets = {canonical_to_alias[source_id]: kind for source_id, kind in kinds.items()}
            self.asset_instruction = '\n' + label + json.dumps(assets, separators=(',', ':'))

    def canonical_output(self, parsed, max_bytes):
        # Validate the model schema BEFORE expansion or Pydantic's narrow Plan
        # reference deduplication. Raw array limits and types cannot be evaded.
        try:
            Draft202012Validator(model_output_schema(OUTPUT_SCHEMAS[self.task])).validate(parsed)
        except ValidationError:
            raise ValueError('invalid model reference shape') from None
        output = deepcopy(parsed)
        for path in OUTPUT_PATHS[self.task]:
            for parent, key in _positions(output, path):
                alias = parent[key]
                if alias not in self._alias_to_canonical:
                    raise ValueError('unknown source alias')
                parent[key] = self._alias_to_canonical[alias]
        encoded = json.dumps(output, ensure_ascii=False, separators=(',', ':'), allow_nan=False)
        if len(encoded.encode('utf-8')) > max_bytes:
            raise ValueError('canonical output size')
        return output
