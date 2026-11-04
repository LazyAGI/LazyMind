"""Dual-series radar with a shared explicit zero-based scale on every dimension."""
import numpy as np


def draw_radar(ax, series, labels, categories, colors, edges, markers, line_styles,
               title, radial_max, grid_levels, focal_method):
    if ax.name != 'polar' or len(labels) != 2 or len(categories) < 3:
        raise ValueError('Use a polar axes, two compared methods and at least three dimensions')
    if not (len(labels) == len(colors) == len(edges) == len(markers) == len(line_styles)):
        raise ValueError('Supply appearance for both methods')
    if not np.isfinite(radial_max) or radial_max <= 0:
        raise ValueError('Use the same disclosed positive maximum for all zero-based axes')
    levels = np.asarray(grid_levels, dtype=float)
    if not len(levels) or not np.isfinite(levels).all() or np.any(levels <= 0) or np.any(levels > radial_max):
        raise ValueError('Grid levels must belong to the disclosed shared scale')
    theta = np.linspace(0, 2 * np.pi, len(categories), endpoint=False)
    angles = np.r_[theta, theta[0]]
    ax.set_theta_zero_location('N')
    ax.set_theta_direction(-1)
    ax.set_ylim(0, radial_max * 1.22)
    ax.set_xticks([])
    ax.set_yticks([])
    ax.grid(False)
    ax.set_frame_on(False)
    for level in levels:
        ax.plot(angles, np.full(len(angles), level), color='#D9D9D9', lw=.5, ls='--', zorder=1)
        ax.text(np.pi / len(categories), level, f'{level:g}', fontsize=7, color='#888888',
                ha='center', va='center', bbox=dict(facecolor='white', alpha=.8, pad=.1, edgecolor='none'), zorder=2)
    for angle in theta:
        ax.plot([angle, angle], [0, radial_max], color='#E4E4E4', lw=.45, ls='--', zorder=1)
    for i, label in enumerate(labels):
        values = np.asarray(series[label], dtype=float)
        if values.ndim != 1 or len(values) != len(categories) or not np.isfinite(values).all():
            raise ValueError('Every radar dimension needs an actual value; do not zero-fill missing dimensions')
        if np.any(values < 0) or np.any(values > radial_max):
            raise ValueError('Values must fit the shared scale; disclose any input normalization')
        focal = label == focal_method
        outline = colors[i] if focal else edges[i]
        ax.fill(angles, np.r_[values, values[0]], facecolor=colors[i], alpha=.13, zorder=3)
        ax.plot(angles, np.r_[values, values[0]], color=outline, lw=1.1, ls=line_styles[i],
                marker=markers[i], markersize=2.7, markerfacecolor=colors[i],
                markeredgecolor=edges[i], markeredgewidth=.4, label=label, zorder=4)
        for angle, value in zip(theta, values):
            offset = .08 if focal or i == 1 and focal_method is None else -.10
            ax.text(angle, max(.02 * radial_max, value + offset * radial_max), f'{value:g}',
                    ha='center', va='center', fontsize=7, color=edges[i] if focal else '#202020', zorder=6,
                    bbox=dict(facecolor='white', edgecolor='none', alpha=.88, pad=.2))
    for angle, label in zip(theta, categories):
        align = 'left' if np.sin(angle) > .15 else 'right' if np.sin(angle) < -.15 else 'center'
        ax.text(angle, 1.12 * radial_max, label, ha=align, va='center', fontsize=8)
    ax.set_title(title, fontsize=9, pad=5)
    ax.legend(loc='upper center', bbox_to_anchor=(.5, -.025), ncol=2, columnspacing=1.1)
