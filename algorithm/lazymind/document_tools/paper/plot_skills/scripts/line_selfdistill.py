"""Mean curves with supplied uncertainty endpoints; no smoothing or invented bands."""
import numpy as np
from paper_common import paper_axes, paper_legend


def draw_confidence_panel(ax, series, labels, colors, edges, markers, line_styles,
                          title, xlabel, ylabel, xlim, ylim, xscale, legend_location):
    """series[label] = (x, mean, lower, upper), using verified absolute endpoints."""
    if not labels or not (len(labels) == len(colors) == len(edges) == len(markers) == len(line_styles)):
        raise ValueError('Supply appearance for every series')
    if xscale not in ('linear', 'log'):
        raise ValueError('Choose a linear or explicitly labelled log x axis')
    counts = []
    for i, label in enumerate(labels):
        x, mean, low, high = [np.asarray(v, dtype=float) for v in series[label]]
        if (any(a.ndim != 1 for a in [x, mean, low, high]) or not len(x)
                or not (len(x) == len(mean) == len(low) == len(high))):
            raise ValueError('Each mean and interval must align with its own x coordinates')
        if not np.isfinite(x).all() or not np.all(np.diff(x) > 0) or any(np.isinf(a).any() for a in [mean, low, high]):
            raise ValueError('Use ordered unique finite x and finite measurements or null gaps')
        if xscale == 'log' and (np.any(x <= 0) or xlim[0] <= 0):
            raise ValueError('Log x requires positive coordinates and limits')
        if np.any(np.isnan(low) != np.isnan(high)):
            raise ValueError('An interval needs both endpoints or neither')
        mask = np.isfinite(mean) & np.isfinite(low) & np.isfinite(high)
        if np.any(np.isfinite(low) & ~np.isfinite(mean)) or np.any(low[mask] > high[mask]):
            raise ValueError('Intervals need a matching mean and ordered endpoints')
        ax.fill_between(x, low, high, where=mask, color=colors[i], alpha=.20, linewidth=0, zorder=1)
        ax.plot(x, mean, color=colors[i], lw=1.25, ls=line_styles[i], marker=markers[i],
                markersize=3.2, markevery=max(1, len(x) // 5), markeredgecolor=edges[i],
                markeredgewidth=.45, label=label, zorder=3 + i)
        counts.append(int(np.isfinite(mean).sum()))
    paper_axes(ax, title, xlabel, ylabel, True)
    ax.set_xscale(xscale)
    ax.set_xlim(*xlim)
    ax.set_ylim(*ylim)
    paper_legend(ax, legend_location)
    return counts
