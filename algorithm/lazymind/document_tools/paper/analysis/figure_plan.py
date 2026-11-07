"""Bind checkable figure choices to executed evidence before drawing."""
import copy
from typing import Annotated, Literal

from pydantic import Field

from .schema import Object, Text, parse


class FigureDraft(Object):
    title: Text
    purpose: Text
    style: Annotated[Text, Field(description='Exact catalog key, or custom with base_style for a specialized geometry.')]
    finding_ids: list[Text] = Field(default_factory=list, description='Exact executed result IDs to display, in order.')
    result_indices: list[int] = Field(
        default_factory=list, description='Legacy zero-based selection; prefer finding_ids.')
    requirements: str = ''
    source_chart_id: str | None = Field(default=None, description='For redraw: exact existing chart ID to preserve.')
    category: str | None = Field(default=None, description=(
        'Existing coordinate or descriptive result field; null uses result labels. Lines require numeric coordinates.'))
    series: str | None = Field(
        default=None, description='Field key from result coordinates or metadata whose values distinguish series.')
    facet: str | None = Field(
        default=None, description=(
            'Existing coordinate or descriptive result field defining panels; metric/unit panels are automatic.'))
    value_format: Literal['auto', 'number', 'percent'] = 'auto'
    normalize: bool = Field(
        default=False, description='For additive stacked bars, show each composition as percentages.')
    annotation: Literal['auto', 'value', 'fraction', 'none'] = 'auto'
    numerator: str | None = Field(default=None, description='Actual numerator coordinate for fraction labels.')
    denominator: str | None = Field(default=None, description='Actual denominator coordinate for fraction labels.')
    caption: str = Field(
        default='', max_length=1200, description='Concise interpretation in the paper language; no source inventory.')
    sort: Literal['source', 'value_ascending', 'value_descending'] = 'source'
    data_status: Literal['supplied', 'simulated'] = Field(
        default='supplied', description='Use simulated when explicit. Supplied does not claim real-world measurement.')
    base_style: str | None = Field(
        default=None, description='Exact reusable catalog key supplying appearance/helpers for custom geometry.')


class FigureArrangement(Object):
    figures: Annotated[list[FigureDraft], Field(max_length=20)]
    all_existing_figures: bool = Field(default=False, description=(
        'Redraw scope: true when the current request selects every existing figure; honor exclusions and limits.'))
    terminology: dict[str, str] = Field(default_factory=dict, description=(
        'One FLAT object: actual source values/metric names -> short string labels in the paper language. '
        'Never nest maps under coordinate keys; never rename data keys.'))
    series_order: list[str] = Field(default_factory=list, description=(
        'Preferred order of actual coordinate strings from results, never translated display labels.'))


def coordinate_values(rows):
    """Describe observed coordinates without inventing values or choosing a plot dimension."""
    values = {}
    for row in rows:
        for name, value in row['coordinates'].items():
            if value is not None and isinstance(value, (str, int, float, bool)):
                levels = values.setdefault(name, [])
                if value not in levels:
                    levels.append(value)
    return values


def _flat_terminology(value, rows):
    """Accept grouped aliases only when their observed identities and flat meaning are unambiguous."""
    if not isinstance(value, dict) or not isinstance(value.get('terminology'), dict):
        return value
    coordinates = coordinate_values(rows)
    labels = {}
    for key, label in value['terminology'].items():
        if isinstance(label, dict):
            if key not in coordinates or any(name not in coordinates[key] for name in label):
                raise ValueError('Grouped terminology must use an observed coordinate and its exact values')
            aliases = label.items()
        else:
            aliases = [(key, label)]
        for source, display in aliases:
            if not isinstance(display, str):
                raise ValueError('Terminology display labels must be strings, never nested maps or numbers')
            if source in labels and labels[source] != display:
                raise ValueError(f'Conflicting terminology for the same source value: {source!r}')
            labels[source] = display
    return dict(value, terminology=labels)


def bind_figures(value, analysis):
    """Keep the proposal inspectable; sources, counts and intervals come from executed rows."""
    from ..plotting.skills import PlotSkills
    from ..plotting.standard import STANDARD_STYLES, check_encoding, panel_key
    from .results import result_rows
    rows = result_rows(analysis)
    arrangement = parse(FigureArrangement, _flat_terminology(value, rows), 'analysis.figures', extra='ignore')
    catalog = PlotSkills().catalog()
    findings = analysis['findings']
    figures = []
    for index, draft in enumerate(arrangement['figures'], 1):
        # A free-form style description plus an explicit available base has one
        # unambiguous execution meaning: custom geometry using that base's helpers.
        if draft['style'] not in catalog and draft.get('base_style') in catalog and draft['base_style'] != 'custom':
            draft['requirements'] = '\n'.join(filter(None, (draft['style'], draft['requirements'])))
            draft['style'] = 'custom'
        style = draft['base_style'] if draft['style'] == 'custom' else draft['style']
        if style not in catalog or style == 'custom':
            raise ValueError(f'Figure {index}: unavailable skill {style!r}; available: {list(catalog)}')
        identifiers = draft.pop('finding_ids')
        indices = draft.pop('result_indices')
        if not identifiers and not indices:
            raise ValueError(f'Figure {index}: select exact finding_ids from the executed result catalog')
        positions = {row['id']: position for position, row in enumerate(rows)}
        if len(identifiers) != len(set(identifiers)) or any(key not in positions for key in identifiers):
            raise ValueError(f'Figure {index}: finding_ids must be unique exact executed result IDs')
        if any(position < 0 or position >= len(findings) for position in indices):
            raise ValueError(f'Figure {index}: result_indices must refer to positions 0..{len(findings) - 1}')
        if identifiers and indices and identifiers != [rows[position]['id'] for position in indices]:
            raise ValueError(f'Figure {index}: finding_ids and legacy result_indices select different results or order')
        indices = [positions[key] for key in identifiers] if identifiers else list(dict.fromkeys(indices))
        selected_rows = [rows[position] for position in indices]
        try:
            check_encoding(draft, selected_rows, check_capacity=False)
        except ValueError as exc:
            raise ValueError(f'Figure {index} ({draft["title"]}), selected finding_ids '
                             f'{[row["id"] for row in selected_rows]}: {exc}') from exc
        groups = [selected_rows]
        if draft['style'] in STANDARD_STYLES:
            # Capacity is a layout choice. Split intact evidence instead of failing the whole analysis.
            panels = {}
            for row in selected_rows:
                key = panel_key(row, draft)
                panels.setdefault(key, []).append(row)
            groups, batch, panel_count = [], [], 0
            limit = None if draft['style'].startswith('line_') else 32
            for panel in panels.values():
                for offset in range(0, len(panel), limit or len(panel)):
                    chunk = panel[offset:offset + limit] if limit else panel
                    if batch and (panel_count == 4 or (limit and len(batch) + len(chunk) > limit)):
                        groups.append(batch)
                        batch, panel_count = [], 0
                    batch.extend(chunk)
                    panel_count += 1
            if batch:
                groups.append(batch)
        if len(groups) == 1:
            groups = [selected_rows]
        if len(groups) > 1 and draft.get('source_chart_id'):
            raise ValueError('An existing redraw cannot change figure identity; select a smaller existing scope.')
        for part, group in enumerate(groups, 1):
            spec = copy.deepcopy(draft)
            if len(groups) > 1:
                spec['title'] += f' ({part}/{len(groups)})'
            check_encoding(spec, group)
            columns = {(ref['table_id'], ref['column']): ref for row in group for ref in row['columns']}
            figures.append(dict(spec, id=f'fig{len(figures) + 1}',
                                finding_ids=[row['id'] for row in group], columns=list(columns.values()),
                                evidence=[{key: copy.deepcopy(row[key]) for key in (
                                    'id', 'label', 'metric', 'value', 'unit', 'comparison', 'scope', 'columns',
                                    'sample_size', 'interval')} for row in group]))
    observed = list(dict.fromkeys(value for levels in coordinate_values(result_rows(analysis)).values()
                                  for value in levels if isinstance(value, str)))
    # Ordering is a presentation hint; unknown labels never become data identities
    # or remove observed groups. Each panel selects its own coordinate from results.
    order = list(dict.fromkeys([key for key in arrangement['series_order'] if key in observed] + observed))
    return dict(figures=figures, terminology=arrangement['terminology'], series_order=order,
                all_existing_figures=arrangement['all_existing_figures'])
