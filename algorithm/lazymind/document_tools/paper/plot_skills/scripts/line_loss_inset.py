"""Observed trajectories with an exact linked zoom window."""
import numpy as np
import matplotlib.patches as mpatches
from paper_common import paper_axes, paper_legend


def draw_loss_zoom(fig, series, labels, colors, edges, markers, line_styles,
                   titles, xlabel, ylabel, xlim, ylim, zoom_xlim, zoom_ylim, legend_location):
    if not labels or not (len(labels) == len(colors) == len(edges) == len(markers) == len(line_styles)):
        raise ValueError('Supply appearance for every trajectory')
    if len(titles) != 2 or not (xlim[0] <= zoom_xlim[0] < zoom_xlim[1] <= xlim[1]
                                and ylim[0] <= zoom_ylim[0] < zoom_ylim[1] <= ylim[1]):
        raise ValueError('The zoom rectangle must be a nonempty subset of the overview limits')
    main = fig.add_axes([.075, .19, .48, .67])
    detail = fig.add_axes([.70, .19, .28, .67])
    zoom_has_data = False
    for i, label in enumerate(labels):
        x, y = [np.asarray(v, dtype=float) for v in series[label]]
        if x.ndim != 1 or y.ndim != 1 or not len(x) or len(x) != len(y):
            raise ValueError('Each trajectory needs aligned one-dimensional coordinates')
        if not np.isfinite(x).all() or np.isinf(y).any() or not np.all(np.diff(x) > 0):
            raise ValueError('Use unique ordered finite x; missing y remains a gap')
        finite = np.isfinite(y)
        if (np.any((x < xlim[0]) | (x > xlim[1]))
                or np.any((y[finite] < ylim[0]) | (y[finite] > ylim[1]))):
            raise ValueError('The overview must include all supplied finite observations')
        zoom_has_data = zoom_has_data or bool(np.any(finite & (x >= zoom_xlim[0]) & (x <= zoom_xlim[1])
                                                     & (y >= zoom_ylim[0]) & (y <= zoom_ylim[1])))
        for ax in [main, detail]:
            ax.plot(x, y, color=colors[i], ls=line_styles[i], lw=1.15, marker=markers[i],
                    markevery=max(1, len(x) // 8), markersize=2.8, markeredgecolor=edges[i],
                    markeredgewidth=.4, label=label, zorder=3 + i)
    if not zoom_has_data:
        raise ValueError('The selected zoom window contains no observed points')
    paper_axes(main, titles[0], xlabel, ylabel, True)
    paper_axes(detail, titles[1], xlabel, '', True)
    main.set_xlim(*xlim)
    main.set_ylim(*ylim)
    detail.set_xlim(*zoom_xlim)
    detail.set_ylim(*zoom_ylim)
    for spine in detail.spines.values():
        spine.set_visible(True)
        spine.set_color('#888888')
        spine.set_linewidth(.6)
    x0, x1 = zoom_xlim
    y0, y1 = zoom_ylim
    main.add_patch(mpatches.Rectangle((x0, y0), x1 - x0, y1 - y0, fill=False,
                                      edgecolor='#666666', lw=.7, ls='--', zorder=7))
    for y in [y0, y1]:
        fig.add_artist(mpatches.ConnectionPatch((x1, y), (x0, y), 'data', 'data',
                       axesA=main, axesB=detail, color='#A0A0A0', lw=.6, ls='--', zorder=1))
    paper_legend(main, legend_location)
    return main, detail
