"""New companion style: independent-run boxes with all valid raw observations."""
import numpy as np
from paper_common import paper_axes


def draw_distribution(ax, series, labels, colors, edges, title, xlabel, ylabel, ylim):
    if not labels or not (len(labels) == len(colors) == len(edges)):
        raise ValueError('Supply matching labels and appearance for every group')
    counts = []
    for i, label in enumerate(labels):
        values = np.asarray(series[label], dtype=float)
        if np.isinf(values).any():
            raise ValueError('Infinite observations are not valid measurements')
        values = values[np.isfinite(values)]
        counts.append(len(values))
        if not len(values):
            continue
        boxes = ax.boxplot([values], positions=[i - .12], widths=.29, patch_artist=True,
                           whis=1.5, showfliers=False,
                           boxprops={'facecolor': colors[i], 'edgecolor': edges[i],
                                     'linewidth': .65, 'alpha': .50},
                           medianprops={'color': '#303030', 'linewidth': .9},
                           whiskerprops={'color': edges[i], 'linewidth': .65},
                           capprops={'color': edges[i], 'linewidth': .65})
        boxes['boxes'][0].set_label(label)
        jitter = ((np.arange(len(values)) * .61803398875) % 1 - .5) * .18
        ax.scatter(i + .18 + jitter, values, s=9, color=colors[i], edgecolors=edges[i],
                   linewidths=.35, alpha=.95, zorder=4, label=label)
    paper_axes(ax, title, xlabel, ylabel, True)
    ax.set_xticks(range(len(labels)), [f'{label}\n(n={n})' for label, n in zip(labels, counts)])
    ax.set_xlim(-.55, len(labels) - .45)
    ax.set_ylim(*ylim)
    return counts
