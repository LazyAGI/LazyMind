"""Two visible x intervals with explicit break marks and no omitted observations."""
import numpy as np
from paper_common import paper_axes, paper_legend


def draw_broken_scatter(fig, series, labels, colors, edges, markers, sizes,
                        windows, ylim, title, xlabel, ylabel, legend_location):
    if not labels or not (len(labels) == len(colors) == len(edges) == len(markers) == len(sizes)):
        raise ValueError('Supply appearance for every scatter group')
    if len(windows) != 2 or not (windows[0][0] < windows[0][1] < windows[1][0] < windows[1][1]):
        raise ValueError('Supply two ordered separated x intervals')
    spans = [hi - lo for lo, hi in windows]
    axes = fig.subplots(1, 2, sharey=True, gridspec_kw={'width_ratios': spans})
    fig.subplots_adjust(left=.075, right=.98, top=.85, bottom=.22, wspace=.10)
    counts = []
    for i, label in enumerate(labels):
        xy = np.asarray(series[label], dtype=float).reshape(-1, 2)
        if np.isinf(xy).any():
            raise ValueError('Scatter coordinates must be finite or null')
        xy = xy[np.isfinite(xy).all(axis=1)]
        counts.append(len(xy))
        if np.any((xy[:, 1] < ylim[0]) | (xy[:, 1] > ylim[1])):
            raise ValueError('The common y axis must include all observed points')
        assigned = np.zeros(len(xy), dtype=int)
        for ax, (lo, hi) in zip(axes, windows):
            mask = (xy[:, 0] >= lo) & (xy[:, 0] <= hi)
            assigned += mask
            ax.scatter(*xy[mask].T, s=sizes[i], marker=markers[i], c=colors[i],
                       edgecolors=edges[i], linewidths=.5, label=label, zorder=4)
        if np.any(assigned != 1):
            raise ValueError('Axis break or outer limits hide observations; choose a continuous axis')
    for ax, limits in zip(axes, windows):
        paper_axes(ax, '', '', '', True)
        ax.set_xlim(*limits)
        ax.set_ylim(*ylim)
    axes[0].set_ylabel(ylabel)
    axes[1].spines['left'].set_visible(False)
    axes[1].tick_params(left=False)
    for ax, x in [(axes[0], 1), (axes[1], 0)]:
        ax.plot([x, x], [0, 0], transform=ax.transAxes, clip_on=False,
                marker=[(-1, -.65), (1, .65)], markersize=7,
                linestyle='none', color='#333333', mew=.65)
    paper_legend(axes[1], legend_location)
    # Figure title is returned as metadata rather than embedded in the canvas.
    fig.text(.53, .055, xlabel, ha='center', fontsize=8.5)
    return axes
