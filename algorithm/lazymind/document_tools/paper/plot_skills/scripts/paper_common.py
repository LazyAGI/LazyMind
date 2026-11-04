"""Shared appearance and standalone export for the approved paper preset.

Only paper_axes/paper_legend are copied into generated programs. File access,
configuration and exports belong to the standalone caller, never to LLM code.
"""
import json
from pathlib import Path

import matplotlib.pyplot as plt
from matplotlib import font_manager
from matplotlib.text import Text


def paper_axes(ax, title, xlabel, ylabel, grid):
    ax.set_title(title, fontsize=9, pad=6, fontweight='normal')
    ax.set_xlabel(xlabel, labelpad=3.5)
    ax.set_ylabel(ylabel, labelpad=4)
    for side, spine in ax.spines.items():
        spine.set_visible(side in ['left', 'bottom'])
        spine.set_color('#333333')
        spine.set_linewidth(.65)
    ax.tick_params(direction='out', length=2.5, width=.6, pad=3)
    ax.set_axisbelow(True)
    ax.grid(False)
    if grid:
        ax.yaxis.grid(True, color='#EBEBEB', linewidth=.45, linestyle='--')


def paper_legend(ax, location):
    return ax.legend(loc=location, frameon=True, fancybox=False,
                     framealpha=1, edgecolor='#C8C8C8', facecolor='white',
                     fontsize=7.3, borderpad=.28, labelspacing=.26,
                     handlelength=1.7, handletextpad=.45, borderaxespad=.4)


def configure(style):
    path = Path(__file__).resolve().parents[1] / 'references/paper_profile.json'
    profile = json.loads(path.read_text())
    base, selected = profile['base'], profile['styles'][style]
    plt.rcParams.update({**base['rc'], **selected.get('rc', {}),
                         'pdf.fonttype': 42, 'ps.fonttype': 42, 'svg.fonttype': 'none'})
    fonts = {font.name for font in font_manager.fontManager.ttflist}
    fallback = next((name for name in ('Noto Serif CJK SC', 'Noto Serif CJK JP',
                                       'Noto Sans CJK SC', 'Noto Sans CJK JP', 'Arial Unicode MS')
                     if name in fonts), None)
    if fallback:
        plt.rcParams['font.family'] = [*plt.rcParams['font.serif'], fallback]
    return base


def make_figure(profile, panels):
    layout = profile['layout']
    if panels not in (1, 2):
        raise ValueError('For more panels choose an explicit layout without shrinking the text')
    fig, axes = plt.subplots(1, panels, figsize=layout['single_inches' if panels == 1 else 'two_inches'])
    fig.subplots_adjust(**layout['single_panel' if panels == 1 else 'two_panels'])
    return fig, axes


def export_figure(fig, output_stem):
    """Export exact physical canvas, embedded TrueType PDF and editable SVG text."""
    for text in fig.findobj(Text):
        if text.get_text().isascii():
            bold = text.get_fontweight() in ('bold', 'heavy', 'semibold', 600, 700, 800, 900)
            path = font_manager.findfont('cmb10' if bold else 'cmr10', fallback_to_default=False)
            text.set_fontproperties(font_manager.FontProperties(
                fname=path, size=text.get_fontsize(), weight=text.get_fontweight()))
    output_stem = Path(output_stem)
    output_stem.parent.mkdir(parents=True, exist_ok=True)
    for ext in ('png', 'pdf', 'svg'):
        fig.savefig(output_stem.with_suffix('.' + ext), dpi=300, facecolor='white')
    plt.close(fig)
