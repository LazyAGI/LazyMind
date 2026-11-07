"""Deterministic, explicitly grouped displays of executed estimates."""
import math
import inspect
from .labels import display_text

STANDARD_STYLES = {'bar_grouped_hatch', 'bar_paired_delta', 'point_interval',
                   'line_training_curve', 'line_confidence_band'}
LINE_STYLES = {'line_training_curve', 'line_confidence_band'}
PROPORTIONS = {'proportion', 'probability', '比例', '概率', 'proportion0-1', 'probability0-1'}
PERCENTS = {'%', 'percent', 'percentage', '百分比', '百分数'}


def coordinate(row, key, default=None):
    """Use actual coordinates or descriptive result fields without inventing dimensions."""
    if key in row['coordinates']:
        return row['coordinates'][key]
    value = row.get(key)
    return value if isinstance(value, str) else default


def panel_key(row, spec):
    """Metrics explicitly encoded as categories or series share compatible axes."""
    metric = row['metric']
    if spec.get('category') in {'metric', 'label'} or spec.get('series') == 'metric':
        metric = None
    return metric, row['unit'], row['kind'], coordinate(row, spec.get('facet'))


def _levels(rows, key):
    return list(dict.fromkeys(coordinate(row, key) for row in rows))


def _categorical(rows, key):
    return all(isinstance(coordinate(row, key), str) for row in rows)


def _equivalent(rows, left, right):
    pairs = {(coordinate(row, left), coordinate(row, right)) for row in rows}
    return len(pairs) == len(_levels(rows, left)) == len(_levels(rows, right))


def check_encoding(spec, rows, *, check_capacity=True):
    """Resolve only unambiguous grouping, never turn numeric metadata into category labels."""
    for key in ('category', 'series', 'facet'):
        name = spec.get(key)
        missing = [row['id'] for row in rows if name and not isinstance(
            coordinate(row, name), (str, int, float, bool))]
        if missing:
            fields = set(rows[0]['coordinates']) | {key for key, value in rows[0].items() if isinstance(value, str)}
            aliases = [field for field in fields if all(coordinate(row, field) == name for row in rows)]
            if len(missing) == len(rows) and len(aliases) == 1:
                spec[key] = aliases[0]
                continue
            available = set.intersection(*(
                {key for key, value in row['coordinates'].items() if isinstance(value, (str, int, float, bool))}
                | {key for key, value in row.items() if isinstance(value, str)} for row in rows))
            raise ValueError(f'{key}={name!r} is unavailable on the selected results. '
                             f'Result IDs missing this field: {missing}. '
                             f'Available coordinates or descriptive result fields: {sorted(available)}. '
                             'Use an actual field name; category=null uses result labels; '
                             'metric/unit panels are automatic. Keep aggregate summaries as annotations '
                             'or captions when plotting individual observations.')
    if spec['style'] == 'bar_stacked' and spec.get('series') == 'label':
        candidates = [key for key in rows[0]['coordinates'] if key != spec.get('category')
                      and _categorical(rows, key) and _equivalent(rows, key, 'label')]
        if len(candidates) == 1:
            spec['series'] = candidates[0]
    if spec['style'] not in STANDARD_STYLES:
        return
    dimensions = [spec[key] for key in ('category', 'series', 'facet') if spec.get(key)]
    if len(dimensions) != len(set(dimensions)):
        raise ValueError('category, series and facet must describe different coordinates')
    if not spec.get('category'):
        candidates = [key for key in rows[0]['coordinates'] if _categorical(rows, key)
                      and len(_levels(rows, key)) > 1 and key != spec.get('series')]
        if len(candidates) == 1:
            spec['category'] = candidates[0]
    dimensions = [spec[key] for key in ('category', 'series', 'facet') if spec.get(key)]
    if spec.get('category') and not spec.get('facet'):
        candidates = [key for key in rows[0]['coordinates'] if key not in dimensions
                      and _categorical(rows, key) and 1 < len(_levels(rows, key)) < len(rows)
                      and not any(_equivalent(rows, key, dimension) for dimension in dimensions)]
        if len(candidates) == 1:
            spec['facet'] = candidates[0]
        elif len(candidates) > 1:
            raise ValueError('Multiple additional grouping dimensions; choose an explicit facet or separate figures')
    cells = set()
    panels = set()
    for row in rows:
        value = row['value']
        if value is not None and (type(value) not in (int, float) or not math.isfinite(value)):
            raise ValueError('Figure values must be finite executed numbers or explicitly missing')
        panel = panel_key(row, spec)
        identity = (panel, coordinate(row, spec.get('category'), row['label']),
                    coordinate(row, spec.get('series')))
        if identity in cells:
            raise ValueError('Duplicate category/series cell; choose a facet or separate estimands, never pool silently')
        cells.add(identity)
        panels.add(panel)
    if check_capacity and len(panels) > 4:
        raise ValueError('Split the figure into at most four metric/unit/facet panels')
    if spec['style'] in LINE_STYLES:
        name = spec.get('category')
        if not name or any(type(row['coordinates'].get(name)) not in (int, float) for row in rows):
            raise ValueError('Line charts require category to name an ordered numeric coordinate')
        if spec.get('sort', 'source') != 'source':
            raise ValueError('Line charts follow numeric coordinates, never sort by outcome value')
    if spec.get('value_format') == 'percent' and any(
            str(row['unit']).lower() not in PROPORTIONS | PERCENTS for row in rows):
        spec['value_format'] = 'auto'
        spec['format_adjustment'] = ('Percent display applies only to declared proportion/percent units; '
                                     'other panels retain their original numbers and units.')
    if spec.get('annotation', 'auto') in {'auto', 'fraction'}:
        _fraction_encoding(spec, rows)


def _value_annotation(spec, reason):
    spec.update(annotation='value', numerator=None, denominator=None, annotation_adjustment=reason)


def _fraction_encoding(spec, rows):
    for key in ('numerator', 'denominator'):
        name = spec.get(key)
        if name and any(type(row['coordinates'].get(name)) not in (int, float) for row in rows):
            _value_annotation(spec, 'Requested fraction counts are absent from frozen results; using executed values.')
            return
    if not all(row['kind'] != 'effect' and str(row['unit']).lower() in PROPORTIONS | PERCENTS for row in rows):
        if spec.get('annotation') == 'fraction':
            _value_annotation(spec, 'Fraction labels do not apply to this quantity; using executed values.')
        return
    keys = [key for key in rows[0]['coordinates'] if key not in (
        spec.get('category'), spec.get('series'), spec.get('facet')) and all(
            type(coordinate(row, key)) in (int, float) for row in rows)]
    pairs = []
    for denominator in keys:
        for numerator in keys:
            if numerator == denominator:
                continue
            if spec.get('numerator') and numerator != spec['numerator']:
                continue
            if spec.get('denominator') and denominator != spec['denominator']:
                continue
            valid = True
            for row in rows:
                n, d = row['coordinates'][numerator], row['coordinates'][denominator]
                scale = 100 if str(row['unit']).lower() in PERCENTS else 1
                valid &= (d > 0 and 0 <= n <= d and int(n) == n and int(d) == d and row['value'] is not None
                          and math.isclose(n / d * scale, row['value'], rel_tol=1e-8, abs_tol=1e-10))
                if not spec.get('denominator'):
                    valid &= d == row['sample_size']['n']
            if valid:
                pairs.append((numerator, denominator))
    if len(pairs) == 1:
        spec['numerator'], spec['denominator'] = pairs[0]
    elif spec.get('annotation') == 'fraction' or spec.get('numerator') or spec.get('denominator'):
        _value_annotation(spec, 'Fraction counts do not uniquely reproduce the frozen proportion; '
                                'using executed values.')


def evidence_caption(spec, rows, language='en'):
    """Keep the public caption short; complete source records stay in the bound figure plan."""
    zh = language == 'zh-CN'
    groups = {}
    for row in rows:
        facet = coordinate(row, spec.get('facet'), '')
        groups.setdefault(str(facet), []).append(row)
    support = []
    for facet, members in groups.items():
        prefix = f'{facet}: ' if facet else ''
        counts = list(dict.fromkeys(row['sample_size']['n'] for row in members))
        units = list(dict.fromkeys(str(row['sample_size']['unit']) for row in members))
        if len(counts) == 1:
            n = counts[0]
            count = '未知' if zh and n is None else 'unknown' if n is None else n
            text = f'{prefix}n={count}'
            if len(units) > 1:
                text += ('（样本单位记录不一致：' if zh else ' (sample unit records differ: ') + ', '.join(units)
                text += '；逐项见图表方案）' if zh else '; see the figure plan)'
            support.append(text)
        elif len(members) <= 4:
            support.extend(f"{prefix}{row['label']}: n={row['sample_size']['n']} ({row['sample_size']['unit']})"
                           for row in members)
        else:
            support.append(prefix + ('样本量因组而异，逐项见图表方案' if zh else
                                     'Sample sizes vary by group; see the figure plan'))
    intervals = [row['interval'] for row in rows if row.get('interval')]
    if not intervals:
        uncertainty = '未提供区间，不作显著性推断。' if zh else 'No intervals supplied; no significance inferred.'
    else:
        kinds = {'confidence': '置信', 'credible': '可信', 'prediction': '预测'}
        labels = list(dict.fromkeys(
            f"{item['level'] * 100:g}%{kinds[item['kind']] if zh else item['kind']}" for item in intervals))
        uncertainty = ('区间：' if zh else 'Intervals: ') + '、'.join(labels) + '。'
        partial = [item for item in intervals if item['lower'] is None or item['upper'] is None]
        if partial:
            if zh:
                uncertainty += '三角形仅标已提供的端点，另一端未提供。'
            else:
                for item in partial:
                    endpoint = 'upper' if item['lower'] is None else 'lower'
                    bound = item[endpoint]
                    uncertainty += f' only {endpoint} endpoint {bound:g} available; opposite endpoint not supplied.'
    status = ('模拟数据。' if zh else 'Simulation data. ') if spec.get('data_status') == 'simulated' else ''
    tables = list(dict.fromkeys(ref['table_id'] for row in rows for ref in row['columns']))
    names = spec.get('source_names', {})
    sources = [f'{names[key]}（{key}）' if names.get(key) else key for key in tables]
    caption = status + '；'.join(dict.fromkeys(support)) + '。' + uncertainty + (
        '来源：' if zh else ' Sources: ') + ', '.join(sources) + '。'
    limitations = list(dict.fromkeys(spec.get('limitations', [])))
    if limitations:
        caption += ('\n限制：' if zh else '\nLimitations: ') + '；'.join(limitations)
    return caption


def generate_standard_code(data, statistics, spec, reference, previous, error):
    return PROGRAM


def generate_stacked_code(data, statistics, spec, reference, previous, error):
    return STACK_PROGRAM


STACK_PROGRAM = '''import matplotlib.pyplot as plt
import re
''' + inspect.getsource(coordinate) + inspect.getsource(display_text) + '''

def draw(data, statistics, spec):
    rows = [row for row in data['analysis_results'] if row['id'] in spec['finding_ids']]
    if not spec.get('series'):
        raise ValueError('Stacked bars require series to name the component coordinate or metric field')
    if len({(row['unit'], row['kind']) for row in rows}) != 1:
        raise ValueError('Additive components must share their units and result kind')
    names = data['shared_style'].get('terminology', {})
    categories = list(dict.fromkeys(coordinate(row, spec.get('category'), '') for row in rows))
    components = list(dict.fromkeys(coordinate(row, spec['series']) for row in rows))
    cells = {(coordinate(row, spec.get('category'), ''), coordinate(row, spec['series'])): row['value'] for row in rows}
    if len(cells) != len(rows) or any((category, component) not in cells
                                    for category in categories for component in components):
        raise ValueError('Every composition needs exactly one observed value per component')
    values = [[cells[(category, component)] for component in components] for category in categories]
    labels = [display_text(component, names) for component in components]
    colors = spec['style_contract']['component_colors'][:len(components)]
    figure, axis = plt.subplots(figsize=(6.2, max(2.1, .65 * len(categories) + 1.3)))
    metric = rows[0]['metric'] if len({row['metric'] for row in rows}) == 1 else 'Value'
    unit = rows[0].get('display_unit', rows[0]['unit'])
    quantity = display_text(metric, names) + (' (' + unit + ')' if unit else '')
    draw_stacked(axis, values, [display_text(category, names) for category in categories], labels, colors,
                 spec.get('normalize', False), '', quantity, orientation='horizontal')
    axis.legend(loc='upper center', bbox_to_anchor=(.5, -.35), ncol=min(4, len(components)),
                frameon=False, fontsize=7)
    figure.tight_layout(pad=.8)
    return figure
'''


PROGRAM = '''import matplotlib.pyplot as plt
import re
from matplotlib.ticker import PercentFormatter, FuncFormatter
from matplotlib.colors import to_rgb
from matplotlib.patches import Patch
''' + inspect.getsource(panel_key) + inspect.getsource(display_text) + '''

def wrap_label(text, width=14):
    lines, current, length = [], '', 0
    for word in str(text).split():
        if word.isascii():
            if current and length + len(word) > width:
                lines.append(current.strip())
                current, length = '', 0
            current += word + ' '
            length += len(word) + 1
            continue
        for char in word + ' ':
            size = 2 if ord(char) > 255 else 1
            if current and length + size > width:
                lines.append(current.strip())
                current, length = '', 0
            current += char
            length += size
    return '\\n'.join(lines + [current.strip()]).rstrip()


def ordered(values, order):
    ranks = {value: index for index, value in enumerate(order)}
    return sorted(dict.fromkeys(values), key=lambda value: ranks.get(str(value), len(ranks)))


def display(value, names):
    return display_text(value, names)


def coordinate(row, key, default=None):
    """Use actual coordinates or descriptive result fields without inventing dimensions."""
    if key in row['coordinates']:
        return row['coordinates'][key]
    value = row.get(key)
    return value if isinstance(value, str) else default


def category(row, spec):
    return coordinate(row, spec.get('category'), row['label'])


def interval_marks(axis, row, position, color, vertical):
    interval = row['interval']
    if interval is None:
        return
    low, high = interval['lower'], interval['upper']
    if low is not None and high is not None:
        x, y = ([position, position], [low, high]) if vertical else ([low, high], [position, position])
        axis.plot(x, y, color=color, linewidth=1, zorder=4)
    else:
        endpoint = low if low is not None else high
        marker = ('^' if low is not None else 'v') if vertical else ('>' if low is not None else '<')
        x, y = (position, endpoint) if vertical else (endpoint, position)
        axis.scatter(x, y, marker=marker, color=color, s=18, zorder=4)


def numeric_format(rows, spec):
    unit = str(rows[0]['unit']).lower()
    ratio = unit in ('proportion', 'probability', '比例', '概率', 'proportion0-1', 'probability0-1')
    percent = unit in ('%', 'percent', 'percentage', '百分比', '百分数')
    enabled = spec.get('value_format', 'auto') != 'number' and (ratio or percent)
    if unit in ('pp', 'percentage point', 'percentage points', '百分点', '个百分点'):
        return 1, ('个百分点' if spec.get('paper_language') == 'zh-CN' else 'pp'), None
    if enabled:
        if rows[0]['kind'] == 'effect':
            return (100 if ratio else 1), ('个百分点' if spec.get('paper_language') == 'zh-CN' else 'pp'), None
        return (100 if ratio else 1), '%', PercentFormatter(xmax=1 if ratio else 100, decimals=0)
    return 1, (rows[0].get('display_unit') or rows[0]['unit'] or ''), None


def value_text(row, spec, scale, suffix):
    if spec.get('annotation') == 'none':
        return ''
    if spec.get('annotation', 'auto') in ('auto', 'fraction') and spec.get('numerator') and spec.get('denominator'):
        return str(int(row['coordinates'][spec['numerator']])) + '/' + str(int(row['coordinates'][spec['denominator']]))
    value = row['value'] * scale
    visible_unit = suffix if suffix in ('%', 'pp', '百分点', '个百分点') else ''
    return format(value, '.3g') + str(visible_unit)


def draw(data, statistics, spec):
    rows = [row for row in data['analysis_results'] if row['id'] in spec['finding_ids']]
    names = data['shared_style'].get('terminology', {})
    order = data['shared_style'].get('series_order', [])
    colors = data['shared_style'].get('series_colors', {})
    palette = data['shared_style']['colors']
    line = spec['style'] in ('line_training_curve', 'line_confidence_band')
    bars = spec['style'] in ('bar_grouped_hatch', 'bar_paired_delta')
    groups = {}
    for row in rows:
        groups.setdefault(panel_key(row, spec), []).append(row)
    horizontal = bars and max(len(display(category(row, spec), names)) for row in rows) > 18
    columns = 2 if len(groups) > 1 else 1
    panel_rows = (len(groups) + columns - 1) // columns
    category_count = max(len({category(row, spec) for row in members}) for members in groups.values())
    height = 2.2 * panel_rows if columns > 1 else 2.5
    if horizontal and columns == 1:
        height = max(2.4, .34 * category_count + 1.1)
    figure, axes = plt.subplots(panel_rows, columns, squeeze=False, figsize=(6.7 if columns > 1 else 5.8, height))
    for unused in axes.ravel()[len(groups):]:
        figure.delaxes(unused)
    for axis, ((metric, unit, kind, facet), members) in zip(axes.ravel(), groups.items()):
        vertical = line or (bars and not horizontal)
        scale, suffix, formatter = numeric_format(members, spec)
        limit = 1 if scale == 100 else 100
        comparable = [row for row in rows if panel_key(row, spec)[:3] == (metric, unit, kind)]
        endpoints = [value for row in comparable for value in (
            row['value'], (row['interval'] or {}).get('lower'), (row['interval'] or {}).get('upper'))
            if value is not None]
        bounded = bool(formatter) and all(0 <= value <= limit for value in endpoints)
        domain = endpoints + ([0] if bars or kind == 'effect' else [])
        low, high = min(domain or [0]), max(domain or [1])
        span = high - low or max(abs(high), 1)
        numeric_limits = (0 if bars and low >= 0 else low - .12 * span,
                          0 if bars and high <= 0 else high + .16 * span)
        if bounded:
            numeric_limits = (0, limit)
        if line:
            categories = sorted({category(row, spec) for row in members})
        else:
            categories = ordered([category(row, spec) for row in members], order)
            if spec.get('sort') in ('value_ascending', 'value_descending'):
                categories = sorted(categories, key=lambda key: next(
                    (row['value'] for row in members if category(row, spec) == key and row['value'] is not None), 0),
                    reverse=spec['sort'] == 'value_descending')
        series = ordered([coordinate(row, spec.get('series')) for row in members], order)
        width = .76 / len(series)
        bar_handles = []
        for series_index, identity in enumerate(series):
            selected = [row for row in members if coordinate(row, spec.get('series')) == identity]
            color = colors.get(identity) or palette[(series_index + 2) % len(palette)]
            legend = display(identity, names) if identity is not None else None
            if bars:
                bar_handles.append(Patch(facecolor=color, hatch='//' if series_index == 1 else '',
                                         edgecolor='white', label=legend))
                legend = None
            if line:
                selected = sorted(selected, key=lambda row: category(row, spec))
                axis.plot([category(row, spec) for row in selected], [row['value'] for row in selected],
                          marker='o', markersize=3, linewidth=1, color=color, label=legend)
            for row in selected:
                center = category(row, spec) if line else categories.index(category(row, spec))
                position = center if line else center + (series_index - (len(series) - 1) / 2) * width
                if row['value'] is None:
                    continue
                if bars:
                    appearance = dict(color=color, hatch='//' if series_index == 1 else '',
                                      edgecolor='white', linewidth=.45, label=legend, zorder=2)
                    if horizontal:
                        axis.barh(position, row['value'], height=width, **appearance)
                    else:
                        axis.bar(position, row['value'], width=width, **appearance)
                    legend = None
                elif not line:
                    axis.scatter(row['value'], position, color=color, s=20, label=legend, zorder=3, clip_on=False)
                    legend = None
                interval_marks(axis, row, position, '#354CC8', vertical)
                annotation = value_text(row, spec, scale, suffix)
                if annotation:
                    if bars:
                        inside = bounded and row['value'] >= limit * .5
                        offset = -4 if inside or row['value'] < 0 else 3
                        red, green, blue = to_rgb(color)
                        dark = .2126 * red + .7152 * green + .0722 * blue < .5
                        location = (position, row['value']) if vertical else (row['value'], position)
                        displacement = (0, offset) if vertical else (offset, 0)
                        axis.annotate(annotation, location, xytext=displacement,
                                      textcoords='offset points',
                                      ha='center' if vertical else 'right' if offset < 0 else 'left',
                                      va=('top' if offset < 0 else 'bottom') if vertical else 'center',
                                      fontsize=6.7, clip_on=False,
                                      color='white' if inside and dark else '#111111',
                                      bbox=dict(facecolor=color, edgecolor='none', pad=.25) if inside else None)
                    elif not line:
                        axis.annotate(annotation, (row['value'], position), xytext=(0, 5),
                                      textcoords='offset points',
                                      ha='center', va='bottom', fontsize=6.7)
        # Missing category/series cells remain gaps, with a status label rather than a zero-valued bar.
        if not line:
            for index, key in enumerate(categories):
                for series_index, identity in enumerate(series):
                    matching = [row for row in members if category(row, spec) == key and
                                coordinate(row, spec.get('series')) == identity]
                    if not matching or all(row['value'] is None for row in matching):
                        position = index + (series_index - (len(series) - 1) / 2) * width
                        missing = '缺失' if spec.get('paper_language') == 'zh-CN' else 'missing'
                        if vertical:
                            axis.text(position, .02, missing, transform=axis.get_xaxis_transform(),
                                      ha='center', va='bottom', fontsize=6.2, rotation=90)
                        else:
                            axis.text(.02, position, missing, transform=axis.get_yaxis_transform(), fontsize=6.2)
        quantity = display(metric, names) if metric is not None else (
            '数值' if spec.get('paper_language') == 'zh-CN' else 'Value')
        if suffix in ('pp', '百分点', '个百分点'):
            quantity = '变化' if spec.get('paper_language') == 'zh-CN' else 'Change'
        if formatter:
            quantity = '比例' if spec.get('paper_language') == 'zh-CN' else 'Proportion'
        quantity += ' (' + str(suffix) + ')' if suffix else ''
        if vertical:
            axis.set_ylabel(wrap_label(quantity, width=24), fontsize=8)
            if formatter:
                axis.yaxis.set_major_formatter(formatter)
            elif scale != 1:
                axis.yaxis.set_major_formatter(FuncFormatter(
                    lambda value, position, factor=scale: format(value * factor, '.3g')))
            if not line:
                axis.set_xticks(range(len(categories)), [wrap_label(display(key, names)) for key in categories])
                axis.set_xlim(-.65, len(categories) - .35)
            else:
                axis.set_xlabel(display(spec['category'], names), fontsize=8)
            if bars or kind == 'effect':
                axis.axhline(0, color='#707070', linewidth=.6, linestyle='--' if kind == 'effect' else '-', zorder=1)
            axis.set_ylim(*numeric_limits)
            axis.grid(axis='y', alpha=.2)
        else:
            axis.set_yticks(range(len(categories)), [wrap_label(display(key, names)) for key in categories])
            axis.invert_yaxis()
            axis.set_xlabel(wrap_label(quantity, width=40), fontsize=8)
            if formatter:
                axis.xaxis.set_major_formatter(formatter)
            elif scale != 1:
                axis.xaxis.set_major_formatter(FuncFormatter(
                    lambda value, position, factor=scale: format(value * factor, '.3g')))
            if bars or kind == 'effect':
                axis.axvline(0, color='#707070', linewidth=.6, linestyle='--' if kind == 'effect' else '-')
            axis.set_xlim(*numeric_limits)
            axis.grid(axis='x', alpha=.2)
        if facet is not None:
            axis.set_title(display(facet, names), fontsize=8.5, loc='left', pad=7)
        if len(series) > 1:
            if bars:
                handles, labels = bar_handles, [handle.get_label() for handle in bar_handles]
            else:
                handles, labels = axis.get_legend_handles_labels()
            by_label = dict(zip(labels, handles))
            axis.legend(by_label.values(), by_label.keys(), loc='lower right', bbox_to_anchor=(1, 1.01),
                        ncol=min(3, len(series)), frameon=False, fontsize=7, borderaxespad=0)
        axis.tick_params(labelsize=7.3)
        axis.spines['top'].set_visible(False)
        axis.spines['right'].set_visible(False)
    if spec.get('data_status') == 'simulated':
        figure.text(.5, 1, '模拟数据' if spec.get('paper_language') == 'zh-CN' else 'Simulation',
                    ha='center', va='bottom', fontsize=8)
    figure.tight_layout(pad=.8, h_pad=1.2)
    return figure
'''
