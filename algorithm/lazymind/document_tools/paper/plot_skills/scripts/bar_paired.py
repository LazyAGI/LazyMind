"""Paired bars with difference arrows above the shorter bar.

Use draw_paired_panel with actual data; no paper measurements or output paths live here.
"""
import numpy as np
from paper_common import paper_axes, paper_legend


def draw_paired_panel(ax, series, labels, categories, colors, focal_method,
                      title, xlabel, ylabel, ylim, delta_unit, legend_location):
    """Draw baseline then method, with absolute differences in the supplied unit.

Finalize figure dimensions/margins before calling. Values must be nonnegative;
None/NaN is missing, never zero. A missing pair has no difference annotation.
The caller converts proportions to percent before choosing delta_unit='pp'.
"""
    if len(labels) != 2 or len(set(labels)) != 2 or len(colors) != 2:
        raise ValueError('Paired bars require exactly two distinct method labels and their colors')
    if len(categories) == 0 or len(set(categories)) != len(categories):
        raise ValueError('Categories must be nonempty and unique')
    if not isinstance(delta_unit, str) or not delta_unit.strip() or '%' in delta_unit:
        raise ValueError('Specify an absolute difference unit; use pp for percentage points, not %')
    limits = np.asarray(ylim, dtype=float)
    if limits.shape != (2,) or not np.all(np.isfinite(limits)) or limits[0] != 0 or limits[1] <= 0:
        raise ValueError('Paired comparison bars require finite zero-based linear y limits')
    values = []
    for label in labels:
        row = np.asarray(series[label], dtype=float)
        if row.shape != (len(categories),) or np.any(np.isinf(row)) or np.any(row < 0):
            raise ValueError('Supply one nonnegative observation or missing value per category')
        values.append(row)
    observed = np.concatenate(values)
    observed = observed[np.isfinite(observed)]
    span = float(limits[1])
    label_pad = span * (3.3 / 95)
    if not len(observed) or np.max(observed) + 2 * label_pad > limits[1]:
        raise ValueError('Y limits must show the observations and leave room for difference labels')

    x = np.arange(len(categories))
    width, offset = .27, .14
    for row, label, color, shift in zip(values, labels, colors, [-offset, offset]):
        valid = np.isfinite(row)
        ax.bar(x[valid] + shift, row[valid], width, color=color, label=label,
               edgecolor='white', linewidth=.45,
               hatch='//' if label == focal_method else '', zorder=3)
    paper_axes(ax, title, xlabel, ylabel, True)
    ax.set_xticks(x, categories)
    ax.set_xlim(-.55, len(categories) - .35)
    ax.set_yscale('linear')
    ax.set_ylim(limits)
    height_points = ax.get_position().height * ax.get_figure().get_figheight() * 72
    for j, (baseline, method) in enumerate(zip(values[0], values[1])):
        if not (np.isfinite(baseline) and np.isfinite(method)):
            continue
        delta = method - baseline
        top = max(baseline, method)
        # Physical length avoids inflating tiny differences just to fit an arrowhead.
        if abs(delta) / span * height_points >= 3:
            arrow_x = x[j] - offset if delta > 0 else x[j] + offset
            ax.plot([x[j] - offset - width / 2, x[j] + offset + width / 2], [top, top],
                    color='#606060', linewidth=.55, linestyle='--', zorder=4)
            ax.annotate('', xy=(arrow_x, method), xytext=(arrow_x, baseline),
                        arrowprops=dict(arrowstyle='-|>', linewidth=.65, color='#505050',
                                        mutation_scale=3.3, shrinkA=0, shrinkB=0), zorder=5)
        ax.text(x[j], top + label_pad, f'{delta:+.1f} {delta_unit}',
                ha='center', va='bottom', fontsize=7.2)
    if legend_location is not None:
        paper_legend(ax, legend_location)
