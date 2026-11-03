"""Last-resort display in the planned chart family, using result labels authored by analysis code."""

BAR_STYLES = {'bar_grouped_hatch', 'bar_paired_delta', 'bar_stacked'}
LINE_STYLES = {'line_training_curve', 'line_confidence_band', 'line_loss_with_inset'}
GEOMETRIES = {'bar_grouped_hatch': 'bar', 'line_training_curve': 'line', 'point_interval': 'point'}


def fallback_style(style):
    """Native style whose appearance the simple display reuses; other geometries become point displays."""
    return ('bar_grouped_hatch' if style in BAR_STYLES else
            'line_training_curve' if style in LINE_STYLES else 'point_interval')


def generate_simple_code(data, statistics, spec, reference, previous, error):
    return f"import matplotlib.pyplot as plt\n\nGEOMETRY = '{GEOMETRIES[spec['style']]}'\n" + PROGRAM


PROGRAM = '''

def ordered_axis(rows):
    # A line needs a coordinate that is numeric on every row and has at least two levels.
    for key in rows[0]['coordinates']:
        values = [row['coordinates'].get(key) for row in rows]
        if all(isinstance(v, (int, float)) and not isinstance(v, bool) for v in values) and len(set(values)) > 1:
            return key
    return None


def draw(data, statistics, spec):
    names = data['shared_style'].get('terminology', {})
    identity_colors = data['shared_style'].get('series_colors', {})
    palette = data['shared_style']['colors']
    selected = [row for row in data['analysis_results'] if row['id'] in spec['finding_ids']]
    groups = {}
    for row in selected:
        groups.setdefault((row['metric'], row['unit'], row['kind']), []).append(row)
    x_keys = {key: ordered_axis(rows) if GEOMETRY == 'line' else None for key, rows in groups.items()}
    heights = [2.8 if x_keys[key] else .4*len(rows)+1.2 for key, rows in groups.items()]
    fig, axes = plt.subplots(len(groups), 1, squeeze=False, figsize=(7.2, max(2.8, sum(heights))),
                             gridspec_kw={'height_ratios': heights})
    for axis, ((metric, unit, kind), rows) in zip(axes.ravel(), groups.items()):
        symbol, name = rows[0].get('display_unit', unit), names.get(metric, metric)
        # Display terminology often already ends with its unit, e.g. 'Test Accuracy (%)'.
        quantity = name if not symbol or name.rstrip().endswith(')') else name + ' (' + symbol + ')'
        x_key = x_keys[(metric, unit, kind)]
        if x_key:
            constant = {key for key in rows[0]['coordinates']
                        if len({repr(row['coordinates'].get(key)) for row in rows}) == 1}
            series = {}
            for row in rows:
                identity = tuple(value for key, value in row['coordinates'].items()
                                 if key != x_key and key not in constant)
                series.setdefault(identity, []).append(row)
            for number, (identity, members) in enumerate(series.items()):
                members = sorted(members, key=lambda row: row['coordinates'][x_key])
                x = [row['coordinates'][x_key] for row in members]
                color = ((identity_colors.get(identity[0]) if len(identity) == 1 else None)
                         or palette[(number + 2) % len(palette)])
                label = ', '.join(str(names.get(value, value)) for value in identity) or quantity
                axis.plot(x, [row['value'] for row in members], marker='o', markersize=3.2, linewidth=1.25,
                          color=color, label=label, zorder=3)
                for position, row in zip(x, members):
                    interval = row['interval']
                    if interval is not None and interval['lower'] is not None and interval['upper'] is not None:
                        axis.plot([position, position], [interval['lower'], interval['upper']], color=color,
                                  linewidth=.85, zorder=2)
            axis.set_xlabel(names.get(x_key, x_key))
            axis.set_ylabel(quantity)
            if len(series) > 1:
                axis.legend(frameon=False)
            if kind == 'effect':
                axis.axhline(0, color='#707070', linestyle='--', linewidth=.8, zorder=1)
            axis.grid(axis='y', alpha=.2)
        else:
            for index, row in enumerate(rows):
                if GEOMETRY == 'bar':
                    axis.barh([index], [row['value']], height=.6, color='#546BF5', edgecolor='white', zorder=2)
                else:
                    axis.scatter([row['value']], [index], s=24, color='#546BF5', zorder=3)
                interval = row['interval']
                if interval is not None and interval['lower'] is not None and interval['upper'] is not None:
                    axis.plot([interval['lower'], interval['upper']], [index, index], color='#354CC8',
                              linewidth=1, zorder=4)
            axis.set_yticks(list(range(len(rows))), [names.get(row['label'], row['label']) for row in rows])
            axis.invert_yaxis()
            axis.set_xlabel(quantity)
            if kind == 'effect' or GEOMETRY == 'bar':
                axis.axvline(0, color='#707070', linestyle='--' if kind == 'effect' else '-', linewidth=.8,
                             zorder=1)
            axis.grid(axis='x', alpha=.2)
        axis.spines['top'].set_visible(False)
        axis.spines['right'].set_visible(False)
    fig.tight_layout(pad=1.2)
    return fig
'''
