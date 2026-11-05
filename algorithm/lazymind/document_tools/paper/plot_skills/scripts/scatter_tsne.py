"""Plot supplied embedding coordinates; never compute or invent an embedding."""
import numpy as np
from paper_common import paper_axes


def draw_embedding(ax, series, labels, colors, edges, markers, label_positions,
                   title, xlabel, ylabel, xlim, ylim):
    if not labels or not (len(labels) == len(colors) == len(edges) == len(markers)):
        raise ValueError('Supply appearance for every embedding group')
    counts = []
    for i, label in enumerate(labels):
        xy = np.asarray(series[label], dtype=float).reshape(-1, 2)
        if np.isinf(xy).any():
            raise ValueError('Embedding coordinates must be finite or null')
        xy = xy[np.isfinite(xy).all(axis=1)]
        counts.append(len(xy))
        if not len(xy):
            continue
        if (np.any((xy[:, 0] <= xlim[0]) | (xy[:, 0] >= xlim[1]))
                or np.any((xy[:, 1] <= ylim[0]) | (xy[:, 1] >= ylim[1]))):
            raise ValueError('Give the full embedding padding on every side; do not crop points')
        ax.scatter(xy[:, 0], xy[:, 1], s=8, c=colors[i], marker=markers[i], alpha=.7,
                   edgecolors=edges[i], linewidths=.22, label=label, zorder=3)
        position = label_positions[label] if label in label_positions else xy.mean(axis=0)
        ax.text(*position, label, ha='center', va='center', fontsize=7.2,
                bbox=dict(boxstyle='round,pad=.2', facecolor='white', edgecolor=edges[i],
                          linewidth=.45, alpha=.94), zorder=5)
    paper_axes(ax, title, xlabel, ylabel, False)
    ax.set_xlim(*xlim)
    ax.set_ylim(*ylim)
    ax.set_aspect('equal', adjustable='box')
    return counts
