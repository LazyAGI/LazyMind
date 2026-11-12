"""Approved extended paper drawing helpers. Actual data is supplied by the caller."""
import numpy as np
import matplotlib.pyplot as plt
import matplotlib.colors as mcolors
from paper_common import paper_axes, paper_legend


def numeric_vector(values):
    data = np.asarray(values, dtype=float)
    if data.ndim != 1 or np.any(np.isinf(data)):
        raise ValueError('Expected a vector of observations; infinity is invalid')
    return data[np.isfinite(data)]


def numeric_cmap(name):
    cmap = plt.get_cmap(name).copy()
    cmap.set_bad('#E5E7EB')
    return cmap


def contrast_text(rgba):
    rgb = np.asarray(rgba[:3])
    linear = np.where(rgb <= .04045, rgb / 12.92, ((rgb + .055) / 1.055) ** 2.4)
    luminance = np.dot(linear, [.2126, .7152, .0722])
    return 'white' if 1.05 / (luminance + .05) >= (luminance + .05) / .05 else '#151515'


def draw_heatmap(ax, values, row_labels, column_labels, title, xlabel, ylabel,
                 limits, signed, number_format, suffix, palette, mode):
    values = np.asarray(values, dtype=float)
    if mode not in ['matrix', 'difference', 'correlation', 'correlation_pairs', 'confusion_counts', 'confusion_row']:
        raise ValueError('Choose a heatmap mode explicitly')
    signed_modes = ['difference', 'correlation', 'correlation_pairs']
    if signed != (mode in signed_modes):
        raise ValueError('Use zero-centered semantics for signed quantities')
    if mode.startswith('confusion'):
        if values.ndim != 2 or values.shape[0] != values.shape[1] or list(row_labels) != list(column_labels):
            raise ValueError('Confusion matrices require the same ordered true/predicted class labels')
        if not np.all(np.isfinite(values)) or np.any(values < 0) or np.any(values != np.floor(values)):
            raise ValueError('Confusion modes take complete nonnegative integer counts, not percentages')
        if mode == 'confusion_row':
            support = values.sum(axis=1, keepdims=True)
            values = np.divide(values * 100, support, out=np.full_like(values, np.nan), where=support > 0)
            if list(limits) != [0, 100]:
                raise ValueError('Row proportions use a common 0 to 100 percent scale')
    if mode == 'correlation':
        if values.ndim != 2 or values.shape[0] != values.shape[1] or list(row_labels) != list(column_labels):
            raise ValueError('Correlation matrices require the same row and column features')
        if not np.allclose(values, values.T, equal_nan=True) or list(limits) != [-1, 1]:
            raise ValueError('Correlations must be symmetric on the shared -1 to 1 scale')
        diagonal = np.diag(values)
        if np.any(np.isfinite(diagonal) & ~np.isclose(diagonal, 1)):
            raise ValueError('Defined correlation diagonal entries must be one')
    if mode == 'correlation_pairs' and list(limits) != [-1, 1]:
        raise ValueError('Correlation pair heatmaps use the shared -1 to 1 scale')
    if values.shape != (len(row_labels), len(column_labels)) or np.any(np.isinf(values)):
        raise ValueError('Heatmap dimensions must match both sets of labels')
    if len(set(row_labels)) != len(row_labels) or len(set(column_labels)) != len(column_labels):
        raise ValueError('Heatmap labels must identify unique rows and columns')
    low, high = limits
    finite = values[np.isfinite(values)]
    if not np.all(np.isfinite(limits)) or not len(finite) or low >= high or finite.min() < low or finite.max() > high:
        raise ValueError('Color limits must include all finite observations')
    if signed and (not low < 0 < high or not np.isclose(-low, high)):
        raise ValueError('Signed heatmaps require an explicit zero center')
    cmap = numeric_cmap(palette)
    norm = mcolors.TwoSlopeNorm(vcenter=0, vmin=low, vmax=high) if signed else mcolors.Normalize(low, high)
    image = ax.pcolormesh(np.arange(len(column_labels) + 1) - .5, np.arange(len(row_labels) + 1) - .5,
                          np.ma.masked_invalid(values), cmap=cmap, norm=norm, shading='flat',
                          edgecolors='white', linewidth=.55, rasterized=False)
    ax.set_xlim(-.5, len(column_labels) - .5)
    ax.set_ylim(len(row_labels) - .5, -.5)
    paper_axes(ax, title, xlabel, ylabel, False)
    for spine in ax.spines.values():
        spine.set_visible(False)
    ax.set_xticks(np.arange(len(column_labels)), column_labels)
    ax.set_yticks(np.arange(len(row_labels)), row_labels)
    ax.tick_params(length=0, pad=4, labelsize=7.5)
    ax.set_xticks(np.arange(len(column_labels) + 1) - .5, minor=True)
    ax.set_yticks(np.arange(len(row_labels) + 1) - .5, minor=True)
    ax.grid(False, which='both')
    ax.tick_params(which='minor', length=0)
    for i in range(values.shape[0]):
        for j in range(values.shape[1]):
            value = values[i, j]
            if not np.isfinite(value):
                text = 'NA'
            elif number_format == 'probability':
                if not 0 <= value <= 1:
                    raise ValueError('Probability display requires values from zero to one')
                text = (r'$<0.001$' if value < .001 else r'$>0.999$' if value > .999 else f'{value:.3f}') + suffix
            else:
                text = format(value, number_format) + suffix
            color = contrast_text(cmap(norm(value))) if np.isfinite(value) else '#505866'
            ax.text(j, i, text, ha='center', va='center', fontsize=7.2, color=color)
    return image


def draw_point_intervals(ax, summaries, methods, categories, colors, edges, markers, title, xlabel, xlim,
                         category_styles, *, method_labels=None):
    """summaries[actual_key] contains triples; method_labels maps keys to display names only."""
    if not methods:
        raise ValueError('No observed series selected: methods must use actual coordinate/summary keys, '
                         'not display labels; available summary keys: ' + repr(list(summaries)))
    if (len(set(methods)) != len(methods)
            or any(len(style) != len(methods) for style in [colors, edges, markers])):
        raise ValueError('Every distinct method needs its own supplied appearance; do not truncate groups')
    if category_styles is not None and len(category_styles) != len(categories):
        raise ValueError('Category appearance requires one identity per category')
    paper_axes(ax, title, xlabel, '', False)
    offsets = np.linspace(-.22, .22, len(methods)) if len(methods) > 1 else [0]
    for method, color, edge, marker, offset in zip(methods, colors, edges, markers, offsets):
        try:
            rows = np.asarray(summaries[method], dtype=float)
        except KeyError:
            raise KeyError(f'Unknown summary key {method!r}; available keys: {list(summaries)!r}. '
                           'Pass display names through method_labels, without renaming data keys.') from None
        if rows.shape != (len(categories), 3) or np.any(np.isinf(rows)):
            raise ValueError('Provide one estimate/low/high triple per category and method; missing cells stay null')
        estimate, low, high = rows.T
        observed = np.isfinite(estimate)
        interval = np.isfinite(low) & np.isfinite(high)
        if np.any(np.isnan(low) != np.isnan(high)) or np.any(interval & ~observed):
            raise ValueError('An interval needs both endpoints and an observed estimate')
        if np.any(low[interval] > high[interval]):
            raise ValueError('Interval endpoints must be ordered')
        finite = rows[np.isfinite(rows)]
        if len(finite) and (finite.min() < xlim[0] or finite.max() > xlim[1]):
            raise ValueError('Do not crop uncertainty intervals')
        error = np.full((2, len(estimate)), np.nan)
        enclosed = interval & (low <= estimate) & (estimate <= high)
        error[:, enclosed] = np.vstack([estimate[enclosed] - low[enclosed], high[enclosed] - estimate[enclosed]])
        label = (method_labels or {}).get(method, method)
        groups = [(np.flatnonzero(observed), label, color, edge, marker)]
        if category_styles is not None:
            groups = [(np.array([i]), style['artist_label'] if len(methods) == 1 else label,
                       style['color'], style['edge'], style['marker'] if len(methods) == 1 else marker)
                      for i, style in enumerate(category_styles) if observed[i]]
        for indices, name, face, outline, shape in groups:
            ax.errorbar(estimate[indices], (np.arange(len(categories)) + offset)[indices],
                        xerr=error[:, indices] if enclosed[indices].any() else None, fmt=shape,
                        markersize=3.7, color=face, markerfacecolor=face, markeredgecolor=outline,
                        markeredgewidth=.5, ecolor=outline, elinewidth=.85,
                        capsize=2, capthick=.65, label=name, zorder=3)
            separate = indices[interval[indices] & ~enclosed[indices]]
            if len(separate):
                positions = (np.arange(len(categories)) + offset)[separate]
                ax.hlines(positions, low[separate], high[separate], colors=outline, linewidth=.85, zorder=3)
                for ends in (low[separate], high[separate]):
                    ax.plot(ends, positions, linestyle='none', marker='|', markersize=4,
                            markeredgewidth=.65, color=outline, label='_nolegend_', zorder=3)
    ax.set_yticks(np.arange(len(categories)), categories)
    ax.set_ylim(len(categories) - .55, -.55)
    ax.set_xlim(xlim)
    ax.xaxis.grid(True, color='#EBEBEB', linewidth=.45, linestyle='--')


def label_difference_axis(ax, comparisons, better, direction_label):
    """Label verified differences without changing artist identity or numeric values.

    comparisons maps each existing series label to (minuend, subtrahend).
    better describes the original metric: 'higher', 'lower', or None when unknown.
    Return handles and explicit subtraction labels for an axes or figure legend.
    """
    handles, series = ax.get_legend_handles_labels()
    if not series or len(set(series)) != len(series) or set(comparisons) != set(series):
        raise ValueError('Provide exactly one comparison for each actual difference series')
    labels = []
    for name in series:
        pair = comparisons[name]
        if (not isinstance(pair, (list, tuple)) or len(pair) != 2
                or any(not isinstance(part, str) or not part.strip() for part in pair)
                or pair[0] == pair[1]):
            raise ValueError('Each difference needs distinct minuend and subtrahend names, in computed order')
        labels.append(f'{pair[0]} - {pair[1]}')
    if better not in ['higher', 'lower', None]:
        raise ValueError('Metric preference must be explicitly higher, lower, or unknown')
    if ((better is None and direction_label is not None)
            or (better is not None and (not isinstance(direction_label, str) or not direction_label.strip()))):
        raise ValueError('Supply a direction label only with a known metric preference')
    low, high = ax.get_xlim()
    if ax.get_xscale() != 'linear' or not low <= 0 <= high:
        raise ValueError('A difference axis must be linear, increasing and include its zero reference')
    ax.axvline(0, color='#888888', linestyle='--', linewidth=.65, zorder=0, label='_nolegend_')
    if better is not None:
        # Separate title and direction strip; annotations stay outside the observations.
        ax.set_title(ax.get_title(), pad=19)
        right = better == 'higher'
        ax.annotate('', xy=(.98 if right else .02, 1.025),
                    xytext=(.80 if right else .20, 1.025), xycoords='axes fraction',
                    arrowprops={'arrowstyle': '->', 'color': '#505050', 'lw': .65},
                    annotation_clip=False)
        ax.text(.78 if right else .22, 1.025, direction_label, transform=ax.transAxes,
                ha='right' if right else 'left', va='center', fontsize=7, color='#505050',
                clip_on=False)
    return handles, labels


def draw_histogram(ax, series, methods, colors, edges, bins, title, xlabel, ylabel):
    """Common observed-value bins; density uses every finite value, without smoothing."""
    if not methods or len(set(methods)) != len(methods) or any(len(style) != len(methods) for style in [colors, edges]):
        raise ValueError('Every distinct method needs its own supplied appearance; do not truncate groups')
    bins = np.asarray(bins, dtype=float)
    if bins.ndim != 1 or len(bins) < 2 or not np.all(np.isfinite(bins)) or not np.all(np.diff(bins) > 0):
        raise ValueError('Histogram edges must strictly increase')
    counts = {}
    paper_axes(ax, title, xlabel, ylabel, True)
    for method, color, edge in zip(methods, colors, edges):
        values = numeric_vector(series[method])
        if not len(values) or values.min() < bins[0] or values.max() > bins[-1]:
            raise ValueError('Every valid observation must fit in the common histogram range')
        ax.hist(values, bins=bins, density=True, histtype='stepfilled', facecolor=color,
                edgecolor=edge, alpha=.20, linewidth=.65, label='_nolegend_')
        ax.hist(values, bins=bins, density=True, histtype='step', color=edge,
                linewidth=1.05, label=method)
        counts[method] = np.histogram(values, bins=bins)[0]
    ax.set_xlim(bins[0], bins[-1])
    ax.set_ylim(bottom=0)
    return counts


def draw_ecdf(ax, series, methods, colors, edges, line_styles, title, xlabel, xlim):
    if (not methods or len(set(methods)) != len(methods)
            or any(len(style) != len(methods) for style in [colors, edges, line_styles])):
        raise ValueError('Every distinct method needs its own supplied appearance; do not truncate groups')
    paper_axes(ax, title, xlabel, 'Cumulative probability', True)
    results = {}
    for method, _color, edge, line_style in zip(methods, colors, edges, line_styles):
        values = np.sort(numeric_vector(series[method]))
        if not len(values) or values[0] < xlim[0] or values[-1] > xlim[1]:
            raise ValueError('Every valid observation must fit in the ECDF range')
        unique, counts = np.unique(values, return_counts=True)
        x = np.r_[xlim[0], unique, xlim[1]]
        y = np.r_[0, np.cumsum(counts) / len(values), 1]
        ax.step(x, y, where='post', color=edge, linestyle=line_style,
                linewidth=1.1, label=method)
        results[method] = [unique, np.cumsum(counts) / len(values)]
    ax.set_xlim(xlim)
    ax.set_ylim(0, 1.025)
    return results


def draw_stacked(ax, values, categories, components, colors, normalized, title, ylabel, orientation='vertical'):
    values = np.asarray(values, dtype=float)
    if len(colors) != len(components) or len(set(components)) != len(components):
        raise ValueError('Supply one stable color per distinct component; never truncate data to a palette')
    if values.shape != (len(categories), len(components)) or not np.all(np.isfinite(values)):
        raise ValueError('Every composition must have complete observed components')
    if np.any(values < 0) or np.any(values.sum(axis=1) <= 0):
        raise ValueError('Compositions require nonnegative components and a positive denominator')
    displayed = values / values.sum(axis=1, keepdims=True) * 100 if normalized else values.copy()
    if orientation == 'horizontal':
        draw_horizontal_stack(ax, displayed, values.sum(axis=1), categories, components, colors,
                              normalized, title, ylabel)
        return displayed
    paper_axes(ax, title, 'Method', ylabel, True)
    bottom = np.zeros(len(categories))
    for index, (name, color) in enumerate(zip(components, colors)):
        bars = ax.bar(np.arange(len(categories)), displayed[:, index], width=.57, bottom=bottom,
                      color=color, edgecolor='white', linewidth=.5, label=name, zorder=3)
        for j, bar in enumerate(bars):
            value = displayed[j, index]
            text = f'{value:.0f}%' if normalized else f'{value:g}'
            ax.text(bar.get_x() + bar.get_width() / 2, bottom[j] + value / 2, text,
                    ha='center', va='center', fontsize=7.2, color=contrast_text(bar.get_facecolor()))
        bottom += displayed[:, index]
    ax.set_xticks(np.arange(len(categories)), categories)
    ax.set_xlim(-.65, len(categories) - .35)
    ax.set_ylim(0, 117 if normalized else bottom.max() * 1.2)
    if not normalized:
        for j, value in enumerate(bottom):
            ax.text(j, value + 2, f'{value:g}', ha='center', va='bottom', fontsize=7.2)
    return displayed


def draw_horizontal_stack(ax, displayed, totals, categories, components, colors, normalized, title, xlabel):
    paper_axes(ax, title, 'Share (%)' if normalized else xlabel, '', False)
    positions, left = np.arange(len(categories)), np.zeros(len(categories))
    for index, (component, color) in enumerate(zip(components, colors)):
        bars = ax.barh(positions, displayed[:, index], left=left, height=.3, color=color,
                       edgecolor='white', linewidth=.5, label=component, zorder=3)
        for row, bar in enumerate(bars):
            width = displayed[row, index]
            middle = left[row] + width / 2
            label = f'{width:.1f}%' if normalized else f'{width:.4g}'
            if width >= displayed[row].sum() * .12:
                ax.text(middle, row, label, ha='center', va='center', fontsize=7.2,
                        color=contrast_text(bar.get_facecolor()))
            elif width > 0:
                ax.annotate(label, (middle, row - .15), (middle, row - .3),
                            ha='center', va='bottom', fontsize=7,
                            arrowprops=dict(arrowstyle='-', color='#555555', linewidth=.6))
        left += displayed[:, index]
    for row, total in enumerate(totals):
        ax.text(left[row], row - .5, f'Total {total:.5g}', ha='right', va='center', fontsize=7.2)
    ax.set_yticks(positions, categories)
    ax.set_ylim(-.65, len(categories) - .35)
    ax.invert_yaxis()
    ax.set_xlim(0, max(left) * 1.05)
    ax.spines['left'].set_visible(False)
    ax.tick_params(axis='y', length=0)
    ax.grid(axis='x', alpha=.2)


def binary_curves(y_true, scores):
    """Threshold ties are processed together. PR uses noninterpolated average precision."""
    labels = np.asarray(y_true)
    score = np.asarray(scores, dtype=float)
    if labels.ndim != 1 or score.shape != labels.shape or not np.all(np.isfinite(score)):
        raise ValueError('Scores and binary labels must be complete, paired vectors')
    if not np.all(np.isin(labels, [0, 1])) or not 0 < labels.sum() < len(labels):
        raise ValueError('Both observed binary classes are required')
    order = np.argsort(-score, kind='stable')
    sorted_y, sorted_score = labels[order], score[order]
    boundaries = np.r_[np.flatnonzero(np.diff(sorted_score)), len(score) - 1]
    tp = np.cumsum(sorted_y)[boundaries]
    fp = 1 + boundaries - tp
    recall = tp / labels.sum()
    fpr = fp / (len(labels) - labels.sum())
    precision = tp / (tp + fp)
    roc_x, roc_y = np.r_[0, fpr], np.r_[0, recall]
    auc = float(np.sum(np.diff(roc_x) * (roc_y[1:] + roc_y[:-1]) / 2))
    ap = float(np.sum(np.diff(np.r_[0, recall]) * precision))
    return {'fpr': roc_x, 'tpr': roc_y, 'recall': np.r_[0, recall],
            'precision': np.r_[1, precision], 'auc': auc, 'ap': ap}


def draw_classification(axes, y_true, scores, methods, edges, line_styles):
    if (not methods or len(set(methods)) != len(methods)
            or any(len(style) != len(methods) for style in [edges, line_styles])):
        raise ValueError('Every distinct method needs its own supplied appearance; do not truncate groups')
    roc_ax, pr_ax = axes
    paper_axes(roc_ax, '(a) ROC curves', 'False positive rate', 'True positive rate', True)
    paper_axes(pr_ax, '(b) Precision-recall curves', 'Recall', 'Precision', True)
    results = {}
    for method, edge, line_style in zip(methods, edges, line_styles):
        result = binary_curves(y_true, scores[method])
        roc_ax.plot(result['fpr'], result['tpr'], color=edge, linestyle=line_style,
                    linewidth=1.15, label=f"{method} (AUC={result['auc']:.2f})")
        pr_ax.step(result['recall'], result['precision'], where='pre', color=edge,
                   linestyle=line_style, linewidth=1.15, label=f"{method} (AP={result['ap']:.2f})")
        results[method] = result
    prevalence = float(np.mean(y_true))
    roc_ax.plot([0, 1], [0, 1], color='#999999', linewidth=.6, linestyle=':', zorder=0)
    pr_ax.axhline(prevalence, color='#999999', linewidth=.6, linestyle=':', zorder=0)
    for ax in axes:
        ax.set_xlim(0, 1)
        ax.set_ylim(0, 1.03)
        ax.set_xticks([0, .25, .5, .75, 1])
        ax.set_yticks([0, .25, .5, .75, 1])
    paper_legend(roc_ax, 'lower right')
    paper_legend(pr_ax, 'lower left')
    return results


def draw_violin(ax, series, methods, colors, edges, title, ylabel):
    if not methods or len(set(methods)) != len(methods) or any(len(style) != len(methods) for style in [colors, edges]):
        raise ValueError('Every distinct method needs its own supplied appearance; do not truncate groups')
    values = [numeric_vector(series[method]) for method in methods]
    if any(len(row) < 5 or np.ptp(row) == 0 for row in values):
        raise ValueError('Violin density needs nonconstant samples, with raw points retained')
    paper_axes(ax, title, 'Method', ylabel, True)
    parts = ax.violinplot(values, positions=np.arange(len(methods)), widths=.72,
                          showmeans=False, showextrema=False, showmedians=False, bw_method=.3, points=150)
    for i, (body, row, color, edge) in enumerate(zip(parts['bodies'], values, colors, edges)):
        body.set_facecolor(color)
        body.set_edgecolor(edge)
        body.set_linewidth(.7)
        body.set_alpha(.36)
        jitter = .12 * np.sin(np.arange(len(row)) * 2.399963229728653)
        ax.scatter(i + jitter, row, s=4, facecolors=color, edgecolors=edge,
                   linewidths=.18, alpha=.56, zorder=3)
        lower, median, upper = np.quantile(row, [.25, .5, .75])
        ax.plot([i, i], [lower, upper], color=edge, linewidth=2.0, zorder=4)
        ax.scatter([i], [median], s=12, facecolors='white', edgecolors=edge, linewidths=.6, zorder=5)
    ax.set_xticks(np.arange(len(methods)), [f'{method}\n(n={len(row)})' for method, row in zip(methods, values)])
    ax.set_xlim(-.6, len(methods) - .4)
    all_values = np.concatenate(values)
    pad = np.ptp(all_values) * .12
    ax.set_ylim(all_values.min() - pad, all_values.max() + pad)
    return values


def draw_colorbar(fig, image, ax, label, ticks):
    """Keep the colorbar vector, with an explicit axis separate from the data panel."""
    if ax is image.axes or ax.figure is not fig or image.axes.figure is not fig:
        raise ValueError('draw_colorbar requires a dedicated colorbar axis in the same figure; '
                         'pass the colorbar axis, not the heatmap data axis')
    ax.set_gid('paper-colorbar')
    cb = fig.colorbar(image, cax=ax, ticks=ticks)
    cb.set_label(label, fontsize=7.5, labelpad=4)
    cb.ax.tick_params(labelsize=7, length=2, width=.5, pad=2)
    cb.outline.set_edgecolor('#BCC2CD')
    cb.outline.set_linewidth(.5)
    if cb.solids is not None:
        cb.solids.set_rasterized(False)
    return cb


def draw_hexbin(ax, x, y, title, xlabel, ylabel, xlim, ylim, gridsize, palette):
    x, y = np.asarray(x, dtype=float), np.asarray(y, dtype=float)
    if x.ndim != 1 or y.shape != x.shape or np.any(np.isinf(x)) or np.any(np.isinf(y)):
        raise ValueError('Supply paired coordinate vectors; infinite coordinates are invalid')
    valid = np.isfinite(x) & np.isfinite(y)
    x, y = x[valid], y[valid]
    if not len(x):
        raise ValueError('Density requires observed pairs')
    bounds = np.asarray([xlim, ylim], dtype=float)
    if bounds.shape != (2, 2) or not np.all(np.isfinite(bounds)) or np.any(bounds[:, 0] >= bounds[:, 1]):
        raise ValueError('Choose finite ordered coordinate limits')
    if x.min() < xlim[0] or x.max() > xlim[1] or y.min() < ylim[0] or y.max() > ylim[1]:
        raise ValueError('Do not crop valid density observations')
    paper_axes(ax, title, xlabel, ylabel, False)
    image = ax.hexbin(x, y, gridsize=gridsize, extent=[*xlim, *ylim], mincnt=1,
                      cmap=numeric_cmap(palette), linewidths=.18, edgecolors='white', rasterized=False)
    ax.set_xlim(xlim)
    ax.set_ylim(ylim)
    if int(image.get_array().sum()) != len(x):
        raise ValueError('Hexbin count must account for every finite pair')
    return image


def draw_contour(ax, x, y, values, levels, line_levels, title, xlabel, ylabel, palette):
    x, y, values = np.asarray(x, dtype=float), np.asarray(y, dtype=float), np.asarray(values, dtype=float)
    levels, line_levels = np.asarray(levels, dtype=float), np.asarray(line_levels, dtype=float)
    if (x.ndim != 1 or y.ndim != 1 or len(x) < 2 or len(y) < 2
            or values.shape != (len(y), len(x)) or not np.all(np.isfinite(values))
            or not np.all(np.isfinite(x)) or not np.all(np.isfinite(y))
            or not np.all(np.diff(x) > 0) or not np.all(np.diff(y) > 0)):
        raise ValueError('Contours need an ordered complete measured grid; do not invent cells')
    if (levels.ndim != 1 or len(levels) < 2
            or not np.all(np.isfinite(levels)) or not np.all(np.diff(levels) > 0)
            or values.min() < levels[0] or values.max() > levels[-1]):
        raise ValueError('Contour levels must enclose the observed values')
    if line_levels.ndim != 1 or np.any(line_levels < levels[0]) or np.any(line_levels > levels[-1]):
        raise ValueError('Line levels must remain within the supplied response scale')
    paper_axes(ax, title, xlabel, ylabel, False)
    mesh_x, mesh_y = np.meshgrid(x, y)
    image = ax.contourf(mesh_x, mesh_y, values, levels=levels, cmap=numeric_cmap(palette),
                        vmin=levels[0], vmax=levels[-1])
    if len(line_levels):
        lines = ax.contour(mesh_x, mesh_y, values, levels=line_levels, colors='white', linewidths=.55)
        norm = mcolors.Normalize(levels[0], levels[-1])
        label_colors = [contrast_text(numeric_cmap(palette)(norm(value))) for value in line_levels]
        ax.clabel(lines, fmt='%g', inline=True, fontsize=7, colors=label_colors)
    ax.scatter(mesh_x.ravel(), mesh_y.ravel(), s=.7, c='#8A95AE', alpha=.6, linewidths=0)
    ax.set_xlim(x[0], x[-1])
    ax.set_ylim(y[0], y[-1])
    return image
