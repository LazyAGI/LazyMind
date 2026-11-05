"""Load bundled plotting references or an explicit override; never execute example code."""
from __future__ import annotations

import copy
import hashlib
import json
import os
import re
from pathlib import Path

from .templates import NATIVE_FUNCTION_GUIDANCE, paper_template

# The figure planner sees only these descriptions, in this order: name the ordinary chart first,
# then when to use it and what it must not receive.
STYLES = {
    'bar_grouped_hatch': ('bar_grouped.py', 'Grouped bar chart: values or means per category and series '
                          '(e.g. method x dataset) with CI error bars when available. Standard main-result '
                          'comparison; hatching only marks a user-designated focal method.'),
    'line_training_curve': ('line_training.py', 'Line chart: values along any ordered numeric x (epochs, steps, '
                            'data size, time, parameter sweep), one line per series, without interval bands.'),
    'line_confidence_band': ('line_selfdistill.py', 'Line chart with CI band: an estimate and its interval at '
                             'each ordered x value, one line per series.'),
    'point_interval': ('paper_extended.py', 'Dot-and-whisker (forest) plot: differences, effects, coefficients '
                       'or many estimates with CIs, especially signed values compared with zero.'),
    'box_raw_points': ('box_raw_points.py', 'Box plot with all raw points: distribution of per-run or '
                       'per-subject values by group; needs raw rows.'),
    'scatter_tradeoff': ('scatter_tradeoff.py', 'Scatter plot: two measured quantities paired by the same runs '
                         'or groups (e.g. accuracy vs. training time).'),
    'bar_paired_delta': ('bar_paired.py', 'Two side-by-side bars per category (baseline, method) with the gap '
                         'annotated; both inputs are nonnegative raw values. Not for precomputed differences.'),
    'bar_stacked': ('paper_extended.py', 'Stacked bars: complete nonnegative additive components per category, '
                    'optionally as within-category proportions.'),
    'heatmap_matrix': ('paper_extended.py', 'Heatmap of a quantitative matrix: values, signed differences, '
                       'correlations or confusion counts.'),
    'violin_raw_points': ('paper_extended.py', 'Violin plot with raw points, median and IQR; needs at least '
                          'five raw values per group.'),
    'histogram_distribution': ('paper_extended.py', 'Histogram of raw values with shared bins.'),
    'ecdf_distribution': ('paper_extended.py', 'ECDF: exact cumulative distribution of raw values.'),
    'line_loss_with_inset': ('line_loss_inset.py', 'Line chart plus a zoomed inset of a narrow window '
                             '(e.g. late-training loss).'),
    'scatter_broken_axis': ('scatter_break.py', 'Scatter with a broken x axis, only when observations form '
                            'two distant clusters that must both be visible.'),
    'scatter_tsne_cluster': ('scatter_tsne.py', 'Scatter of supplied 2D embedding coordinates; never computed '
                             'or invented here.'),
    'density_hexbin': ('paper_extended.py', 'Hexbin density of many paired raw observations, too many for a '
                       'scatter plot.'),
    'contour_response': ('paper_extended.py', 'Contour map of a response measured on a complete 2D grid.'),
    'radar_dual_series': ('radar_comparison.py', 'Radar chart comparing two methods across several comparable '
                          'dimensions on one scale.'),
    'classification_roc_pr': ('paper_extended.py', 'ROC and PR curves from per-case true labels and prediction '
                              'scores.'),
}


class PlotSkills:
    def __init__(self, directory: str | None = None):
        configured = directory or os.environ.get('LAZYMIND_PAPER_PLOT_SKILLS')
        # Ship the reviewed templates with F4 so deployment does not depend on a sibling checkout.
        # Explicit overrides remain authoritative; a broken override must fail rather than silently fall back.
        self.root = (Path(configured).expanduser() if configured else
                     Path(__file__).resolve().parents[1] / 'plot_skills').resolve()
        self.hashes: dict[str, str] = {}
        self.instructions = self.read('SKILL.md')
        preset = 'references/paper_profile.json'
        self.paper_profile = json.loads(self.read(preset))
        if self.paper_profile.get('base', {}).get('version') != 'paper-v1':
            raise ValueError('未知的论文绘图 preset 版本，请核对 skill 与应用版本')

    def read(self, relative: str) -> str:
        path = (self.root / relative).resolve()
        if not path.is_relative_to(self.root):
            raise ValueError('绘图 skill 引用越出配置目录')
        if not path.is_file() or path.stat().st_size > 64 * 1024:
            raise RuntimeError(f'缺少绘图 skill 资源或资源过大: {relative}；请配置 LAZYMIND_PAPER_PLOT_SKILLS')
        raw = path.read_bytes()
        self.hashes[relative] = hashlib.sha256(raw).hexdigest()
        return raw.decode('utf-8')

    def catalog(self) -> dict:
        available = {name: description for name, (_, description) in STYLES.items()
                     if name in self.paper_profile['styles']}
        return {**available,
                'custom': 'other chart geometry; MUST inherit appearance from a named catalog base_style'}

    def script(self, style: str) -> str:
        name = STYLES[style][0]
        path = 'scripts/' + name
        return self.read(path)

    def style_contract(self, style: str, base_style: str | None = None) -> dict:
        selected = base_style if style == 'custom' else style
        if selected not in STYLES or selected not in self.catalog():
            raise ValueError('每张图必须选择已有 skill；custom 必须明确有效的 base_style')
        reference_path = f'references/{selected}.md'
        self.read(reference_path)
        result = self._paper_contract(selected)
        result.update(source_style=selected, reference_path=reference_path, adapted=style == 'custom',
                      helper_module=Path(STYLES[selected][0]).stem,
                      title_placement='metadata_only',
                      bar_colors=result['colors'])
        result['reference_legend_location'] = result.get('legend_location', 'upper right')
        result['legend_location'] = (result['reference_legend_location'] if result.get('preserve_legend_location') else
                                     'upper right' if selected == 'bar_grouped_hatch' else 'best')
        result['rules'] += [
            'Geometry and appearance are separate. Adapt this skill to the actual chart without importing '
            'sample measurements, labels, reference lines, embeddings or axis limits.',
            'Use data.shared_style.series_colors for recurring series named in its series_order; otherwise '
            'keep identity consistent using the shared palette and terminology. Native additive stacks use '
            'stable component colors; quantitative matrices use the actual numeric colormap.',
            'Label each series artist with its actual group. For category bars or boxes, label ticks with '
            'actual groups. Preserve the same identity when sorting; the renderer does not recolor artists.',
            'The renderer applies the declared font/style rc and exports the returned figure. The generated '
            'program chooses axes, legends, labels, layout and actual runtime data bindings.',
            'Legend position adapts to actual data. Do not copy a reference corner when it covers observations.',
            'Return the figure title in Chart.title and panel descriptions in caption. '
            'Keep the full title/caption outside the image and choose short readable panel identifiers.',
        ]
        return result

    def _paper_contract(self, selected: str) -> dict:
        base = copy.deepcopy(self.paper_profile['base'])
        style = copy.deepcopy(self.paper_profile['styles'][selected])
        base.update({key: value for key, value in style.items() if key not in {'rc', 'rules'}})
        base['rc'].update(style.get('rc', {}))
        base['rules'].extend(style.get('rules', []))
        if (not base['colors'] or any(not re.fullmatch(r'#[0-9a-fA-F]{6}', c) for c in base['colors'])
                or base['native_template'] not in {'draw_reference_panel', 'draw_training_panel',
                                                   'draw_distribution', 'draw_tradeoff', 'draw_confidence_panel',
                                                   'draw_loss_zoom', 'draw_embedding', 'draw_broken_scatter',
                                                   'draw_radar', 'draw_paired_panel', 'draw_heatmap',
                                                   'draw_point_intervals', 'draw_histogram', 'draw_ecdf',
                                                   'draw_stacked', 'draw_classification', 'draw_hexbin',
                                                   'draw_contour', 'draw_violin'}):
            raise ValueError('论文绘图 preset 的配色或模板入口无效')
        return base

    def reference(self, style: str, base_style: str | None = None) -> str:
        """Give each drawing model the real helper source and relevant scientific guidance."""
        contract = self.style_contract(style, base_style)
        script = self.script(contract['source_style'])
        common = self.read('scripts/paper_common.py')
        template = paper_template(script, common, contract['function'], contract['native_template'],
                                  tuple(contract.get('helpers', [])))
        return (self.instructions + '\n' + self.read('references/paper_style.md') + '\n'
                + self.read(contract['reference_path']) + '\n' + NATIVE_FUNCTION_GUIDANCE
                + '\nHELPER SOURCE (injected unchanged; no example data or file I/O):\n'
                + '```python\n' + template + '\n```')
