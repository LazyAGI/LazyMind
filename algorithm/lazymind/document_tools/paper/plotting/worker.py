"""Execute a drawing program and export its figure; do not invent labels or rebind its artists."""
import __future__
import base64
import contextlib
import io
import json
import re
import sys
import traceback


def apply_export_fonts(figure, families):
    """Use one Unicode-capable family chain even when generated code changes rcParams."""
    from matplotlib.text import Text

    for axis in figure.axes:
        axis.get_xticklabels()
        axis.get_yticklabels()
    for text in figure.findobj(Text):
        text.set_fontfamily(families)
        text.set_math_fontfamily('stix')
        text.set_usetex(False)


def validate_bar_positions(figure):
    """Axes must enclose complete categories and every measured bar endpoint."""
    from matplotlib.container import BarContainer

    for axis in figure.axes:
        for container in axis.containers:
            if not isinstance(container, BarContainer):
                continue
            horizontal = container.orientation == 'horizontal'
            low, high = sorted(axis.get_ylim() if horizontal else axis.get_xlim())
            for bar in container.patches:
                start = bar.get_y() if horizontal else bar.get_x()
                end = start + (bar.get_height() if horizontal else bar.get_width())
                if min(start, end) < low - 1e-9 or max(start, end) > high + 1e-9:
                    dimension = 'y' if horizontal else 'x'
                    raise ValueError(f'Bar is clipped on categorical {dimension} axis: bar spans '
                                     f'{min(start, end):g}..{max(start, end):g}, limits are {low:g}..{high:g}. '
                                     'Set categorical limits to enclose the complete bar and center single-row stacks.')
                value = bar.get_x() + bar.get_width() if horizontal else bar.get_y() + bar.get_height()
                value_low, value_high = sorted(axis.get_xlim() if horizontal else axis.get_ylim())
                if not value_low - 1e-9 <= value <= value_high + 1e-9:
                    dimension = 'x' if horizontal else 'y'
                    raise ValueError(f'Measured bar endpoint {value:g} is outside numeric {dimension} limits '
                                     f'{value_low:g}..{value_high:g}. Include every measured value and check '
                                     'shared axes between quantities with different units.')


def wrapped_text(value, text, renderer, width):
    lines = []
    for paragraph in value.split('\n'):
        current = ''
        for word in re.findall(r'\S+?(?:-|(?=\s|$))', paragraph):
            separator = '' if current.endswith('-') else ' '
            trial = (current + separator + word).strip()
            measured = renderer.get_text_width_height_descent(trial, text.get_fontproperties(), False)[0]
            if current and measured > width:
                lines.append(current)
                current = word
            else:
                current = trial
        lines.append(current)
    return '\n'.join(lines)


def wrap_category_labels(figure):
    """Reflow long categorical ticks without changing their meaning or numeric positions."""
    figure.canvas.draw()
    renderer = figure.canvas.get_renderer()
    for axis in figure.axes:
        if axis.name != 'rectilinear':
            continue
        xlabel = axis.xaxis.label
        xlabel.set_text(wrapped_text(xlabel.get_text(), xlabel, renderer, axis.get_window_extent(renderer).width))
        ticks, texts = axis.get_xticks(), axis.get_xticklabels()
        if len(texts) < 2 or len(ticks) != len(texts):
            continue
        labels = [text.get_text() for text in texts]
        if not any(any(char.isalpha() for char in label) and not label.startswith('$') for label in labels):
            continue
        positions = [axis.transData.transform((tick, axis.get_ylim()[0]))[0] for tick in ticks]
        gaps = [abs(right - left) for left, right in zip(positions, positions[1:]) if abs(right - left) > 1]
        if not gaps:
            continue
        width = min(gaps) * .88
        wrapped = [wrapped_text(label, text, renderer, width) for label, text in zip(labels, texts)]
        if wrapped != labels:
            axis.set_xticks(ticks, wrapped)


def place_figure_legends(figure):
    """Reserve a compact row above the plot when a shared legend covers an axis."""
    figure.canvas.draw()
    renderer = figure.canvas.get_renderer()
    axes = [axis for axis in figure.axes if axis.get_visible()]
    for axis in axes:
        legend = axis.get_legend()
        if legend is not None:
            box = legend.get_window_extent(renderer)
            if axis.bbox.contains(box.x0, box.y0) and axis.bbox.contains(box.x1, box.y1):
                legend.set_loc('best')
    top = max([1.02, *[axis.get_tightbbox(renderer).y1 / figure.bbox.height + .03 for axis in axes]])
    for legend in list(figure.legends):
        box = legend.get_window_extent(renderer)
        if any(box.overlaps(axis.get_tightbbox(renderer)) for axis in axes):
            labels = [text.get_text() for text in legend.get_texts()]
            frame = legend.get_frame()
            replacement = figure.legend(
                legend.legend_handles, labels, loc='lower center', bbox_to_anchor=(.5, top),
                ncol=min(5, len(labels)), fontsize=legend.get_texts()[0].get_fontsize(),
                title=legend.get_title().get_text() or None, frameon=legend.get_frame_on(),
                facecolor=frame.get_facecolor(), edgecolor=frame.get_edgecolor(), framealpha=frame.get_alpha())
            legend.remove()
            figure.canvas.draw()
            top = replacement.get_window_extent(renderer).y1 / figure.bbox.height + .03


def fit_external_text(figure):
    """Reserve subplot space and move external notes clear of axes, without rejecting a figure."""
    figure.canvas.draw()
    renderer = figure.canvas.get_renderer()
    # Respect external notes/legends while fitting genuine subplot axes. Inset/colorbar
    # layouts keep their model-supplied positions; no layout choice becomes an error.
    height, width = figure.bbox.height, figure.bbox.width
    bottom, top, left, right = .02, .98, 0, 1
    for item in [*figure.texts, *figure.legends]:
        box = item.get_window_extent(renderer)
        if box.y1 < height * .2:
            bottom = max(bottom, (box.y1 + 8) / height)
        elif box.y0 > height * .8:
            top = min(top, (box.y0 - 8) / height)
        elif box.x1 < width * .2:
            left = max(left, (box.x1 + 8) / width)
        elif box.x0 > width * .8:
            right = min(right, (box.x0 - 8) / width)
    if bottom < top and left < right and all(axis.get_subplotspec() is not None for axis in figure.axes):
        figure.tight_layout(rect=(left, bottom, right, top), pad=.8)
        figure.canvas.draw()
        renderer = figure.canvas.get_renderer()
    boxes = [axis.get_tightbbox(renderer) for axis in figure.axes if axis.get_visible()]
    boxes = [box for box in boxes if box is not None]
    if not boxes:
        return
    bottom_edge, top_edge = min(box.y0 for box in boxes), max(box.y1 for box in boxes)
    gap = figure.dpi * 2 / 25.4
    for text in figure.texts:
        x, y = text.get_position()
        if y >= .2 and y <= .8:
            continue
        width = figure.bbox.width * .96
        text.set_text(wrapped_text(text.get_text(), text, renderer, width))
        if y < .2:
            text.set_verticalalignment('top')
            text.set_position((x, min(y, (bottom_edge - gap) / height)))
            bottom_edge = text.get_window_extent(renderer).y0
        else:
            text.set_verticalalignment('bottom')
            text.set_position((x, max(y, (top_edge + gap) / height)))
            top_edge = text.get_window_extent(renderer).y1


def render(payload):
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    from matplotlib.figure import Figure
    from matplotlib import font_manager

    contract = payload['spec'].get('style_contract', {})
    rc = dict(contract.get('rc', {}))
    installed = {font.name for font in font_manager.fontManager.ttflist}
    cjk = next((name for name in ('Noto Serif CJK SC', 'Noto Serif CJK JP', 'Noto Sans CJK SC',
                                  'Noto Sans CJK JP', 'Arial Unicode MS', 'SimSun', 'Microsoft YaHei',
                                  'SimHei') if name in installed), 'DejaVu Sans')
    families = [name for name in ('STIXGeneral', 'DejaVu Serif', cjk) if name in installed]
    rc.update({'font.family': families, 'font.serif': families, 'mathtext.fontset': 'stix'})
    colors = contract.get('colors')
    if colors:
        rc['axes.prop_cycle'] = matplotlib.cycler(color=colors)
    # Real text stays visible and inspectable by downstream fact checks.
    rc.update({'svg.fonttype': 'none', 'pdf.fonttype': 42, 'text.usetex': False,
               'axes.unicode_minus': False, 'svg.hashsalt': payload['data_sha256']})
    original = json.dumps([payload['table'], payload['statistics']], sort_keys=True, allow_nan=False)
    with plt.rc_context(rc):
        try:
            namespace = {}
            with contextlib.redirect_stdout(sys.stderr):
                exec(compile(payload['code'], '<generated-plot>', 'exec',
                             flags=__future__.annotations.compiler_flag, dont_inherit=True), namespace)
                figure = namespace['draw'](payload['table'], payload['statistics'], payload['spec'])
            if not isinstance(figure, Figure):
                raise ValueError('draw 必须返回 Matplotlib Figure')
            if figure._suptitle is not None and figure._suptitle.get_text().strip() == payload['spec'].get('title'):
                figure.suptitle('')
            if original != json.dumps([payload['table'], payload['statistics']], sort_keys=True, allow_nan=False):
                raise ValueError('绘图代码修改了输入结果或原始数据，请使用局部副本')
            validate_bar_positions(figure)
            apply_export_fonts(figure, families)
            place_figure_legends(figure)
            wrap_category_labels(figure)
            fit_external_text(figure)
            # Layout can narrow panels after the first label measurement.
            wrap_category_labels(figure)
            fit_external_text(figure)
            apply_export_fonts(figure, families)
            output = {}
            for extension in ('svg', 'png', 'pdf'):
                stream = io.BytesIO()
                metadata = {'Creator': 'LazyMind F4'} if extension != 'png' else {'Software': 'LazyMind F4'}
                if extension == 'svg':
                    metadata['Date'] = None
                figure.savefig(stream, format=extension, dpi=300, bbox_inches='tight', metadata=metadata)
                raw = stream.getvalue()
                if len(raw) > (2 if extension == 'svg' else 4) * 1024 * 1024:
                    raise ValueError(f'{extension} 图表过大，请减少图元或画布尺寸')
                if extension == 'svg':
                    text = raw.decode('utf-8')
                    output['svg'] = text[text.index('<svg'):]
                else:
                    output[extension] = base64.b64encode(raw).decode('ascii')
            return dict(svg=output['svg'], exports={key: output[key] for key in ('png', 'pdf')})
        finally:
            plt.close('all')


if __name__ == '__main__':
    try:
        print(json.dumps(render(json.load(sys.stdin)), ensure_ascii=False, allow_nan=False))
    except Exception as exc:
        traceback.print_exc(file=sys.stderr)
        frames = [dict(file=frame.filename, line=frame.lineno, function=frame.name)
                  for frame in traceback.extract_tb(exc.__traceback__)
                  if frame.filename == '<generated-plot>']
        print(json.dumps({'error': f'{type(exc).__name__}: {str(exc)[:4000]}',
                          'frames': frames[-3:]}, ensure_ascii=False))
        sys.exit(1)
