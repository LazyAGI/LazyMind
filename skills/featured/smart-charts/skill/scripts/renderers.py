"""图表渲染器。21 种图表类型的 ECharts option 生成方法。"""

import json
import warnings
import pandas as pd
import numpy as np
from typing import Dict, List, Tuple

if __name__ == '__main__' and __package__ is None:
    import sys
    from pathlib import Path
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
    from scripts.exceptions import ChartError, ErrorCode
else:
    from .exceptions import ChartError, ErrorCode


# ── 关系列检测（graph/sankey/stats 三处共用，保证一致）──
# 别名分两类：exact 精确命中；contains 包含命中（覆盖「来源/去向/起点/终点」等复合列名）。
# value 列的误匹配代价低（仅影响边权重，不影响图结构），故以精确命中为主，避免误伤「总金额」等列。
_RELATION_ALIASES = {
    'source': {'exact': ('源', 'source', 'from', 'src'),
               'contains': ('来源', '起点', '起始', '源节点')},
    'target': {'exact': ('目标', 'target', 'to', 'dst', 'dest'),
               'contains': ('去向', '终点', '到达', '目的', '目标节点')},
    'value':  {'exact': ('权重', 'weight', '值', 'value', 'val', '流量', '金额', '数量'),
               'contains': ('权重', 'weight')},
}


def _match_relation_alias(cl: str, role: str) -> bool:
    alias = _RELATION_ALIASES[role]
    return cl in alias['exact'] or any(a in cl for a in alias['contains'])


def _detect_relation_cols(df):
    """统一的关系列检测：返回 (source_col, target_col, value_col)，未识别为 None。

    graph/sankey/stats 三处共用本函数，避免各维护一份关键词表导致不一致。
    优先精确命中，再用包含匹配覆盖「来源/去向/起点/终点」等常见中文列名。
    """
    source_col = target_col = value_col = None
    for col in df.columns:
        cl = str(col).strip().lower()
        if source_col is None and _match_relation_alias(cl, 'source'):
            source_col = col
        elif target_col is None and _match_relation_alias(cl, 'target'):
            target_col = col
        elif value_col is None and _match_relation_alias(cl, 'value'):
            value_col = col
    return source_col, target_col, value_col


def _safe_str_list(series) -> List[str]:
    """将 Series 转为字符串列表，NaN/None 转为空字符串。

    pandas 3.0+ 的 str dtype 下 astype(str) 不会把 NaN 转成 'nan'，
    而是保留 float('nan')，导致 json.dumps(allow_nan=False) 崩溃。
    先 fillna('') 再 astype(str) 可规避此问题。
    Categorical 列（如 pd.cut 产物）的 categories 不含 ''，直接
    fillna('') 抛 "Cannot setitem on a Categorical with a new category"，
    先转 object dtype 再填充。
    """
    if isinstance(series.dtype, pd.CategoricalDtype):
        series = series.astype('object')
    return series.fillna('').astype(str).tolist()


def _js_dumps(obj) -> str:
    """序列化数据到 JS 字面量上下文，转义 </ 防 </script> 提前闭合脚本标签。

    \/ 在 JS 字符串中合法且求值后还原为 /，前端拿到的值不变。
    用于内嵌 option JSON 之外的、由 Python 直接拼接进 <script> 的数据数组
    （tooltip JS 中的列名/行标签等）。
    """
    return json.dumps(obj, ensure_ascii=False).replace('</', '<\\/')


def _sanitize_value(v):
    """将 NaN / Inf 转为 None，并将浮点数保留两位小数，确保 JSON 序列化后 ECharts 可正常渲染。"""
    if isinstance(v, float):
        if np.isnan(v) or np.isinf(v):
            return None
        return round(v, 2)
    return v


def _sanitize_series(data):
    """清洗列表中的 NaN / Inf 值。"""
    return [_sanitize_value(v) for v in data]


def _labeled_points(df, x_col, num_cols, label_col=None, numeric_color_col=None, x_is_numeric=True):
    """生成带身份标识的点数据（scatter/bubble 共用）。

    每个点为 {'value': [x, *num_cols, (color)], 'name': label}：
    - name 来自 label_col（身份列），tooltip 中作为点的标题；
    - numeric_color_col 存在时把其值追加到 value 末尾，供 visualMap 按维度着色。
    x/y/color 任一为 NaN 的行被跳过（与原实现一致：无法定位的点不渲染）。
    """
    # 向量化 NaN 过滤：任一 x/y 列为 NaN 即整行跳过（color 列 NaN 不跳过，追加 None）
    # pandas 3.0 CoW 下 notna().to_numpy() 返回只读数组，就地 &= 会抛
    # "output array is read-only"，先 copy 出可写副本再累积
    mask = df[x_col].notna().to_numpy().copy()
    for c in num_cols:
        mask &= df[c].notna().to_numpy()
    sub = df[mask]
    x_list = sub[x_col].tolist()
    num_lists = [sub[c].tolist() for c in num_cols]
    color_list = sub[numeric_color_col].tolist() if numeric_color_col is not None else None
    label_list = sub[label_col].tolist() if label_col is not None else None
    pts = []
    for i in range(len(sub)):
        xv = _sanitize_value(float(x_list[i])) if x_is_numeric else str(x_list[i])
        vals = [xv] + [_sanitize_value(float(v)) for v in (lst[i] for lst in num_lists)]
        if color_list is not None:
            cv = color_list[i]
            vals.append(_sanitize_value(float(cv)) if pd.notna(cv) else None)
        pt = {'value': vals}
        if label_list is not None:
            lv = label_list[i]
            pt['name'] = '' if pd.isna(lv) else str(lv)
        pts.append(pt)
    return pts


def _point_tooltip_js(dim_names, has_label):
    """scatter/bubble 的 item tooltip JS：点标题（身份列）+ 各维度 "列名: 值"。"""
    label_line = "if (p.name) { s += '<b>' + esc(p.name) + '</b><br/>'; }" if has_label else ""
    return (
        "function(p) {\n"
        f"  var names = {_js_dumps(list(dim_names))};\n"
        "  var esc = function(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#39;'); };\n"
        "  var fmt = function(v) { return (typeof v === 'number') ? v.toLocaleString() : esc(v); };\n"
        "  var s = '';\n"
        f"  {label_line}\n"
        "  var vals = Array.isArray(p.value) ? p.value : [p.value];\n"
        "  for (var i = 0; i < names.length && i < vals.length; i++) {\n"
        "    s += esc(names[i]) + ': ' + fmt(vals[i]) + '<br/>';\n"
        "  }\n"
        "  return s;\n"
        "}"
    )


def _maybe_rotate_labels(x_data):
    """类目过多或标签过长时旋转 x 轴标签，避免长中文类目名重叠。"""
    if len(x_data) > 8 or (x_data and max(len(s) for s in x_data) > 6):
        return {'rotate': 30}
    return None


def _axis_name_graphics(x_name=None, y_names=(), x_bottom=42):
    """轴名称渲染为可拖拽的 graphic 文本元素。

    放在轴配置里（name/nameGap）位置固定，长名称不是压刻度数字、就是压图例。
    graphic 文本默认放在留白区（x 轴名在标签与图例之间，y 轴名在绘图区上方），
    用户可在图上直接拖拽微调，保存图片时随画布状态生效。
    id 以 axisName- 开头，供 HTML 重命名面板同步修改文字。
    x_bottom：x 轴名初始底距；启用 dataZoom 的图表应传更大值避开底部滑块。
    """
    graphics = []
    if x_name:
        graphics.append({'id': 'axisName-x', 'type': 'text', 'draggable': True, 'cursor': 'move',
                         'left': 'center', 'bottom': x_bottom,
                         'style': {'text': x_name, 'fontSize': 12, 'fill': '#666'}})
    for i, name in enumerate(y_names):
        pos = {'left': '6%', 'top': 38} if i == 0 else {'right': '6%', 'top': 38}
        graphics.append({'id': f'axisName-y{i}', 'type': 'text', 'draggable': True, 'cursor': 'move',
                         **pos, 'style': {'text': name, 'fontSize': 12, 'fill': '#666'}})
    return graphics


class ChartRenderersMixin:
    """21 种图表的 ECharts option 生成方法，由 ChartGenerator 继承。"""

    def _base(self, title, texts: Dict[str, str], x_label='', y_label='', x_data_len=0, y_data_len=0):
        opt = {
            'title': {'text': title, 'left': 'center', 'textStyle': {'fontSize': 16}},
            'tooltip': {'trigger': 'axis', 'formatter': self._TOOLTIP_FORMATTER_AXIS},
            'legend': {'top': 'bottom', 'type': 'scroll'},  # scroll 防止系列过多时图例溢出
            'grid': {'left': '3%', 'right': '4%', 'bottom': '12%', 'containLabel': True},
            # Okabe-Ito 色觉安全色板（红绿色弱可区分）
            'color': ['#0072B2', '#E69F00', '#009E73', '#D55E00', '#CC79A7', '#56B4E9', '#F0E442', '#999999'],
        }
        # 数据点过多时启用 dataZoom，让用户可在图内拖动/缩放查看完整数据
        zooms = []
        if x_data_len and x_data_len > self.DATAZOOM_THRESHOLD:
            zooms.append({'type': 'inside', 'start': 0, 'end': 100})
            zooms.append({'type': 'slider', 'start': 0, 'end': 100, 'height': 20, 'bottom': 8})
            opt['grid']['bottom'] = '20%'
        if y_data_len and y_data_len > self.DATAZOOM_THRESHOLD:
            zooms.append({'type': 'inside', 'orient': 'vertical', 'start': 0, 'end': 100})
            zooms.append({'type': 'slider', 'orient': 'vertical', 'start': 0, 'end': 100, 'width': 16, 'right': 4})
            opt['grid']['right'] = '8%'
        if zooms:
            opt['dataZoom'] = zooms
            # 底部滑块占 0~28px，图例上移避免与滑块重叠
            opt['legend'] = {'bottom': 30, 'type': 'scroll'}
        return opt

    def _line(self, df, x, y, title, texts):
        x_data = _safe_str_list(df[x])
        opt = self._base(title, texts, x_data_len=len(x_data))
        opt['xAxis'] = {'type': 'category', 'data': x_data}
        rot = _maybe_rotate_labels(x_data)
        if rot:
            opt['xAxis']['axisLabel'] = rot
        opt['yAxis'] = {'type': 'value'}
        opt['graphic'] = _axis_name_graphics(x, y)
        opt['series'] = [
            {'name': col, 'type': 'line', 'smooth': True, 'data': _sanitize_series(df[col].tolist())}
            for col in y
        ]
        return opt

    def _bar(self, df, x, y, title, texts):
        x_data = _safe_str_list(df[x])
        opt = self._base(title, texts, x_data_len=len(x_data))
        opt['xAxis'] = {'type': 'category', 'data': x_data}
        rot = _maybe_rotate_labels(x_data)
        if rot:
            opt['xAxis']['axisLabel'] = rot
        opt['yAxis'] = {'type': 'value'}
        opt['graphic'] = _axis_name_graphics(x, y)
        opt['series'] = [
            {'name': col, 'type': 'bar', 'data': _sanitize_series(df[col].tolist())}
            for col in y
        ]
        if len(y) == 1:
            # 单系列时直接显示数值标签（多系列显示会互相遮挡，仍靠 tooltip 读值）
            opt['series'][0]['label'] = {'show': True, 'position': 'top'}
        return opt

    def _pie(self, df, x, y, title, texts):
        opt = self._base(title, texts)
        opt['tooltip'] = {'trigger': 'item', 'formatter': '{a} <br/>{b}: {c} ({d}%)'}
        # 图例置于底部（继承 _base 的横向 scroll），避免竖排左置图例与扇区外的长标签重叠
        y_col = y[0] if y else df.columns[-1]
        if not pd.api.types.is_numeric_dtype(df[y_col]):
            # 数值列为空或为分类列时，对 X 列做频数统计，展示各类占比
            counts = df[x].astype(str).value_counts()
            data = [{'name': str(k), 'value': _sanitize_value(float(v))} for k, v in counts.items()]
            y_col = texts['axis_frequency']
        else:
            data = [{'name': str(n), 'value': _sanitize_value(float(v))}
                for n, v in zip(df[x].tolist(), df[y_col].tolist())]
        opt['series'] = [{
            'name': y_col, 'type': 'pie', 'radius': ['40%', '70%'], 'data': data,
            'label': {'show': True, 'formatter': '{b}: {c} ({d}%)'},
            'emphasis': {'itemStyle': {'shadowBlur': 10, 'shadowColor': 'rgba(0,0,0,0.5)'}},
        }]
        return opt

    def _scatter(self, df, x, y, title, texts):
        """散点图：每个点携带身份（label_col），tooltip 显示 身份 + 列名: 值。

        color_by（可选）：
        - 数值列 → visualMap 连续着色（dimension=2）；
        - 类别列 → 每类拆为一个 series，自动配色并进 legend。
        """
        y_col = y[0] if y else df.columns[-1]
        label_col, color_col = self._label_col, self._color_by
        x_is_numeric = pd.api.types.is_numeric_dtype(df[x])
        numeric_color = color_col is not None and pd.api.types.is_numeric_dtype(df[color_col])

        if x_is_numeric:
            opt = self._base(title, texts)
            opt['xAxis'] = {'type': 'value', 'scale': True}
        else:
            x_data = _safe_str_list(df[x])
            opt = self._base(title, texts, x_data_len=len(x_data))
            opt['xAxis'] = {'type': 'category', 'data': x_data}
        opt['yAxis'] = {'type': 'value', 'scale': True}
        opt['graphic'] = _axis_name_graphics(x, [y_col])

        dims = [x, y_col] + ([color_col] if numeric_color else [])
        self._item_tooltip_js = _point_tooltip_js(dims, has_label=label_col is not None)
        opt['tooltip'] = {'trigger': 'item', 'formatter': self._ITEM_TOOLTIP_PLACEHOLDER}

        if color_col is not None and not numeric_color:
            # 类别列 → 按类别拆 series（NaN 归入"未分类"，不静默丢点）
            color_s = df[color_col].where(df[color_col].notna(), texts['series_uncategorized']).astype(str)
            opt['series'] = [
                {'name': cat, 'type': 'scatter', 'symbolSize': 10,
                 'data': _labeled_points(df[color_s == cat], x, [y_col], label_col, None, x_is_numeric)}
                for cat in color_s.unique()
            ]
        else:
            data = _labeled_points(df, x, [y_col], label_col,
                                   color_col if numeric_color else None, x_is_numeric)
            opt['series'] = [{'name': y_col, 'type': 'scatter', 'data': data, 'symbolSize': 10}]
            if numeric_color:
                cvals = [p['value'][2] for p in data if p['value'][2] is not None]
                if cvals:
                    opt['visualMap'] = {'min': min(cvals), 'max': max(cvals), 'dimension': 2,
                                        'calculable': True, 'orient': 'vertical', 'right': '1%', 'top': 'center'}
                    opt['grid']['right'] = '10%'  # 为右侧 visualMap 留出空间
        return opt

    def _area(self, df, x, y, title, texts):
        opt = self._line(df, x, y, title, texts)
        for s in opt['series']:
            s['areaStyle'] = {'opacity': 0.5}
        return opt

    def _radar(self, df, x, y, title, texts):
        opt = self._base(title, texts)
        opt['tooltip'] = {'trigger': 'item'}
        # 向量化求每行维度最大值（忽略 NaN），替代逐行 iterrows
        arr = df[y].to_numpy(dtype='float64')
        with warnings.catch_warnings():
            # 全 NaN 行 nanmax 返回 nan 并告警，交由下方 `or 100` 兜底，与旧逻辑一致
            warnings.simplefilter('ignore', RuntimeWarning)
            row_max = np.nanmax(arr, axis=1) * 1.2 if arr.shape[1] else np.full(len(df), 100.0)
        indicator = [{'name': n, 'max': _sanitize_value(float(m)) or 100}
                     for n, m in zip(_safe_str_list(df[x]), row_max)]
        opt['radar'] = {'indicator': indicator, 'shape': 'polygon'}
        # 每个数值列(y)是一个独立系列，共用同一套 indicator；
        # value 与 indicator 一一对应，长度必须一致；NaN 填充为 0 以保持多边形闭合，
        # 与上方 indicator max 计算忽略 NaN 的语义一致（缺失维度按 0 分呈现）。
        opt['series'] = [{
            'name': str(c),
            'type': 'radar',
            'data': [{
                'name': str(c),
                'value': [0 if pd.isna(v) else _sanitize_value(v) for v in df[c].tolist()],
            }],
        } for c in y]
        return opt

    def _heatmap(self, df, x, y, title, texts):
        x_data = _safe_str_list(df[x])
        y_data = y
        opt = self._base(title, texts, x_data_len=len(y_data), y_data_len=len(x_data))
        # item tooltip 显示 "行标签 × 列标签: 值"，而非默认的坐标索引数组
        self._item_tooltip_js = (
            "function(p) {\n"
            f"  var xs = {_js_dumps(y_data)};\n"
            f"  var ys = {_js_dumps(x_data)};\n"
            "  var esc = function(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#39;'); };\n"
            "  var fmt = function(v) { return (typeof v === 'number') ? v.toLocaleString() : esc(v); };\n"
            "  return esc(ys[p.value[1]]) + ' × ' + esc(xs[p.value[0]]) + ': ' + fmt(p.value[2]);\n"
            "}"
        )
        opt['tooltip'] = {'position': 'top', 'formatter': self._ITEM_TOOLTIP_PLACEHOLDER}
        # 向量化取值：一次转 NumPy 数组，替代逐单元格 df.iloc[i][col]（每次都构造 Series，大矩阵慢百倍）
        arr = np.round(df[y_data].to_numpy(dtype='float64'), 2)
        # NaN/Inf → None，与 _sanitize_value 语义一致
        matrix = [[j, i, (float(arr[i, j]) if np.isfinite(arr[i, j]) else None)]
                  for i in range(arr.shape[0]) for j in range(arr.shape[1])]
        finite = arr[np.isfinite(arr)]
        vmin = float(finite.min()) if finite.size else 0
        vmax = float(finite.max()) if finite.size else 100
        opt['xAxis'] = {'type': 'category', 'data': y_data, 'splitArea': {'show': True}}
        rot = _maybe_rotate_labels(y_data)
        if rot:
            opt['xAxis']['axisLabel'] = rot
        opt['yAxis'] = {'type': 'category', 'data': x_data, 'splitArea': {'show': True}}
        # visualMap 已承担数值图例职责，series 图例只会显示图表类型名（"热力图"），移除
        opt.pop('legend', None)
        # visualMap 放在图表左侧纵向显示，不遮挡图表正文区域
        opt['visualMap'] = {'min': vmin, 'max': vmax, 'calculable': True,
                            'orient': 'vertical', 'left': '1%', 'bottom': '10%'}
        opt['grid']['left'] = '10%'  # 为左侧 visualMap 留出空间
        opt['series'] = [{'name': texts['series_heatmap'], 'type': 'heatmap', 'data': matrix, 'label': {'show': True}}]
        return opt

    def _treemap(self, df, x, y, title, texts):
        opt = self._base(title, texts)
        opt['tooltip'] = {'trigger': 'item'}
        # 区块上已有名称标签，series 图例只显示图表类型名，移除
        opt.pop('legend', None)
        y_col = y[0] if y else df.columns[-1]
        data = [{'name': str(n), 'value': _sanitize_value(float(v))}
                for n, v in zip(df[x].tolist(), df[y_col].tolist())]
        opt['series'] = [{'name': texts['series_treemap'], 'type': 'treemap', 'data': data, 'roam': False, 'breadcrumb': {'show': True}}]
        return opt

    def _graph(self, df, x, y, title, texts):
        opt = self._base(title, texts)
        # 检测是否有"源/目标"列格式
        source_col, target_col, weight_col = _detect_relation_cols(df)

        if source_col and target_col:
            # 源/目标/权重格式
            node_names = set(_safe_str_list(df[source_col]) + _safe_str_list(df[target_col]))
            nodes = [{'name': n} for n in node_names]
            src_list = df[source_col].tolist()
            tgt_list = df[target_col].tolist()
            has_weight = weight_col and weight_col in df.columns
            w_list = df[weight_col].tolist() if has_weight else None
            links = []
            for i in range(len(src_list)):
                link = {'source': str(src_list[i]), 'target': str(tgt_list[i])}
                if has_weight:
                    w = _sanitize_value(float(w_list[i]))
                    if w is not None:
                        link['value'] = w
                links.append(link)
        else:
            # 通用格式：x 列为节点名，y 列为值，链式连接
            y_col = y[0] if y else df.columns[-1]
            if pd.api.types.is_numeric_dtype(df[y_col]):
                nodes = [{'name': str(n), 'value': _sanitize_value(float(v))}
                         for n, v in zip(df[x].tolist(), df[y_col].tolist())]
            else:
                nodes = [{'name': str(n)} for n in df[x].tolist()]
            links = [{'source': nodes[i]['name'], 'target': nodes[i+1]['name'], 'value': 1} for i in range(len(nodes)-1)]

        opt['tooltip'] = {'trigger': 'item'}
        # 节点上已有名称标签，series 图例只显示图表类型名，移除
        opt.pop('legend', None)
        opt['series'] = [{'name': texts['series_graph'], 'type': 'graph', 'layout': 'force', 'data': nodes, 'links': links,
                          'roam': True, 'label': {'show': True}, 'force': {'repulsion': 200, 'edgeLength': [50, 150]}}]
        return opt

    def _boxplot(self, df, x, y, title, texts):
        opt = self._base(title, texts)
        opt['tooltip'] = {'trigger': 'item', 'axisPointer': {'type': 'shadow'}}
        cols = y if len(y) > 0 else df.select_dtypes(include=[np.number]).columns[:5].tolist()
        box_data = []
        outlier_data = []  # 格式: [[categoryIndex, value], ...]
        for cat_idx, col in enumerate(cols):
            s = df[col].dropna()
            if s.empty:
                box_data.append([0, 0, 0, 0, 0])
                continue
            q1, q2, q3 = float(s.quantile(0.25)), float(s.quantile(0.5)), float(s.quantile(0.75))
            iqr = q3 - q1
            lo = max(float(s.min()), q1 - 1.5 * iqr)
            hi = min(float(s.max()), q3 + 1.5 * iqr)
            box_data.append([lo, q1, q2, q3, hi])
            # ECharts scatter 在 category xAxis 下需要 [xIndex, yValue] 格式
            outliers = s[(s < lo) | (s > hi)]
            for idx, val in outliers.items():
                pt = {'value': [cat_idx, _sanitize_value(float(val))]}
                if self._label_col is not None:
                    lv = df.loc[idx, self._label_col]
                    pt['name'] = '' if pd.isna(lv) else str(lv)
                outlier_data.append(pt)
        opt['xAxis'] = {'type': 'category', 'data': cols, 'boundaryGap': True}
        rot = _maybe_rotate_labels(cols)
        if rot:
            opt['xAxis']['axisLabel'] = rot
        opt['yAxis'] = {'type': 'value'}
        # 离群点 tooltip：显示身份（label_col）+ 所属列名 + 值，而非默认的坐标数组
        self._item_tooltip_js = (
            "function(p) {\n"
            f"  var cols = {_js_dumps(cols)};\n"
            "  var esc = function(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#39;'); };\n"
            "  var s = p.name ? '<b>' + esc(p.name) + '</b><br/>' : '';\n"
            "  return s + esc(cols[p.value[0]]) + ': ' + p.value[1];\n"
            "}"
        )
        opt['series'] = [
            {'name': texts['series_boxplot'], 'type': 'boxplot', 'data': box_data},
            {'name': texts['series_outliers'], 'type': 'scatter', 'data': outlier_data, 'symbolSize': 8,
             'tooltip': {'formatter': self._ITEM_TOOLTIP_PLACEHOLDER}},
        ]
        return opt

    def _waterfall(self, df, x, y, title, texts):
        """瀑布图：透明垫底 series + 增量 series（stack 实现悬浮柱）。

        增量值可正可负：柱高取绝对值，垫底高度取累计起点，保证柱子"悬浮"；
        每个柱子的 label 和 tooltip 显示带符号的原始增量，避免绝对值造成误读。
        垫底 series 透明、silent、不进 legend、tooltip 不显示。
        """
        y_col = y[0] if y else df.columns[-1]
        x_data = _safe_str_list(df[x])
        base, bars = [], []
        cum = 0.0
        for v in df[y_col].tolist():
            v = float(v) if pd.notna(v) else 0.0
            signed = '%g' % round(v, 2)  # 带符号的原始增量，用于 label/tooltip
            if v >= 0:
                base.append(round(cum, 2))
                bars.append({'value': round(v, 2), 'label': {'formatter': signed}})
            else:
                base.append(round(cum + v, 2))
                bars.append({'value': round(-v, 2), 'label': {'formatter': signed}})
            cum += v
        opt = self._base(title, texts, x_data_len=len(x_data))
        opt['xAxis'] = {'type': 'category', 'data': x_data}
        rot = _maybe_rotate_labels(x_data)
        if rot:
            opt['xAxis']['axisLabel'] = rot
        opt['yAxis'] = {'type': 'value'}
        opt['graphic'] = _axis_name_graphics(x, [y_col])
        # 图例只保留增量系列（垫底辅助系列不显示）
        opt['legend'] = {'top': 'bottom', 'type': 'scroll', 'data': [y_col]}
        self._waterfall_tooltip_js = (
            "function(p) {\n"
            "  var esc = function(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#39;'); };\n"
            "  var v = (p.data && p.data.label) ? p.data.label.formatter : p.value;\n"
            f"  return '<b>' + esc(p.name) + '</b><br/>' + esc(p.seriesName) + ': ' + v;\n"
            "}"
        )
        opt['tooltip'] = {'trigger': 'item', 'formatter': self._WATERFALL_TOOLTIP_PLACEHOLDER}
        opt['series'] = [
            {'name': '__waterfall_base__', 'type': 'bar', 'stack': 'waterfall', 'silent': True,
             'itemStyle': {'color': 'transparent'}, 'emphasis': {'itemStyle': {'color': 'transparent'}},
             'tooltip': {'show': False}, 'data': base},
            {'name': y_col, 'type': 'bar', 'stack': 'waterfall', 'data': bars,
             'label': {'show': True, 'position': 'top'}, 'itemStyle': {'color': '#5470c6'}},
        ]
        return opt

    def _gauge(self, df, x, y, title, texts):
        num_cols = df.select_dtypes(include=[np.number]).columns
        y_col = y[0] if y else (num_cols[0] if len(num_cols) else None)
        if y_col is None:
            raise ChartError(
                "仪表盘需要至少一个数值列",
                ErrorCode.CHART_CONFIG_ERROR,
                details={'suggestion': '提供数值类型的列，或用 transform_code 生成数值列'},
            )
        value = float(df[y_col].mean())
        value = _sanitize_value(value)
        if value is None:
            value = 0
        data_max = float(df[y_col].max())
        max_val = data_max * 1.05 if data_max > 100 else 100.0
        max_val = _sanitize_value(max_val) or 100
        opt = {'title': {'text': title, 'left': 'center'}}
        opt['series'] = [{'name': texts['series_gauge'], 'type': 'gauge', 'max': max_val,
                          'detail': {'formatter': '{value}'},
                          'data': [{'value': value, 'name': y_col}],
                          'axisLine': {'lineStyle': {'width': 10, 'color': [[0.3, '#67e0e3'], [0.7, '#37a2da'], [1, '#fd666d']]}}}]
        return opt

    def _sankey(self, df, x, y, title, texts):
        opt = {'title': {'text': title, 'left': 'center'}}
        # 检测是否有"源/目标"列格式
        source_col, target_col, value_col = _detect_relation_cols(df)

        nodes, links = [], []
        if source_col and target_col:
            # 源/目标/值格式
            node_names = set(_safe_str_list(df[source_col]) + _safe_str_list(df[target_col]))
            nodes = [{'name': n} for n in node_names]
            src_list = df[source_col].tolist()
            tgt_list = df[target_col].tolist()
            has_value = value_col and value_col in df.columns
            v_list = df[value_col].tolist() if has_value else None
            for i in range(len(src_list)):
                val = 1
                if has_value:
                    v = _sanitize_value(float(v_list[i]))
                    val = v if v is not None else 1
                links.append({'source': str(src_list[i]), 'target': str(tgt_list[i]), 'value': val})
        else:
            # 通用格式：链式连接
            y_col = y[0] if y else df.columns[-1]
            names = df[x].tolist()
            numeric_y = pd.api.types.is_numeric_dtype(df[y_col])
            vals = df[y_col].tolist() if numeric_y else [1] * len(names)
            nodes = [{'name': str(n)} for n in names]
            for i in range(1, len(names)):
                val = _sanitize_value(float(vals[i])) if numeric_y and pd.notna(vals[i]) else 1
                links.append({'source': str(names[i-1]), 'target': str(names[i]), 'value': val if val is not None else 1})

        opt['tooltip'] = {'trigger': 'item'}
        opt['series'] = [{'name': texts['series_sankey'], 'type': 'sankey', 'data': nodes, 'links': links,
                          'emphasis': {'focus': 'adjacency'}, 'lineStyle': {'curveness': 0.5}}]
        return opt

    def _funnel(self, df, x, y, title, texts):
        opt = self._base(title, texts)
        y_col = y[0] if y else df.columns[-1]
        data = [{'name': str(n), 'value': _sanitize_value(float(v))}
                for n, v in zip(df[x].tolist(), df[y_col].tolist())]
        all_vals = [d['value'] for d in data if d['value'] is not None]
        max_val = max(all_vals) if all_vals else 100
        opt['series'] = [{'name': texts['series_funnel'], 'type': 'funnel', 'left': '10%', 'top': 60, 'bottom': 60, 'width': '80%',
                          'min': 0, 'max': max_val, 'sort': 'descending', 'gap': 2,
                          'label': {'show': True, 'position': 'inside'},
                          'data': data}]
        return opt

    def _sunburst(self, df, x, y, title, texts):
        opt = {'title': {'text': title, 'left': 'center'}}
        y_col = y[0] if y else df.columns[-1]
        data = [{'name': str(n), 'value': _sanitize_value(float(v))}
                for n, v in zip(df[x].tolist(), df[y_col].tolist())]
        opt['series'] = [{'name': texts['series_sunburst'], 'type': 'sunburst', 'data': data, 'radius': [0, '90%'],
                          'label': {'rotate': 'radial'}}]
        return opt

    def _wordcloud(self, df, x, y, title, texts):
        opt = {'title': {'text': title, 'left': 'center'}}
        y_col = y[0] if y else df.columns[-1]
        data = [{'name': str(n), 'value': _sanitize_value(float(v))}
                for n, v in zip(df[x].tolist(), df[y_col].tolist())]
        opt['series'] = [{'name': texts['series_wordcloud'], 'type': 'wordCloud', 'shape': 'circle',
                          'sizeRange': [12, 60], 'rotationRange': [-90, 90], 'rotationStep': 45,
                          'data': data}]
        return opt

    def _histogram(self, df, x, y, title, texts):
        """直方图：对连续数值列分箱后用 bar 渲染（barCategoryGap=0 消除间隙）。

        与 bar 的本质区别：bar 用于离散类别比较，histogram 用于连续变量分布形状展示。
        """
        num_cols = df.select_dtypes(include=[np.number]).columns
        y_col = y[0] if y else (num_cols[0] if len(num_cols) else None)
        if y_col is None:
            raise ChartError(
                "直方图需要至少一个数值列",
                ErrorCode.CHART_CONFIG_ERROR,
                details={'suggestion': '提供数值类型的列，或用 transform_code 生成数值列'},
            )
        s = df[y_col].dropna()
        if s.empty:
            raise ChartError(
                f"列 {y_col} 无有效数值，无法生成直方图",
                ErrorCode.DATA_EMPTY,
                details={'column': y_col, 'suggestion': '选择数值类型的列'},
            )
        # Sturges 规则：n_bins = log2(N) + 1
        n_bins = max(10, int(np.log2(len(s)) + 1))
        counts, edges = np.histogram(s, bins=n_bins)
        # 半开区间记号 [a, b)，末箱闭区间 [a, b]，避免边界归属歧义
        labels = [f'[{edges[i]:.1f}, {edges[i+1]:.1f})' for i in range(len(counts))]
        labels[-1] = labels[-1][:-1] + ']'
        opt = self._base(title, texts, x_data_len=len(labels))
        opt['tooltip'] = {'trigger': 'item', 'formatter': '{b}<br/>{c}'}
        opt['xAxis'] = {'type': 'category', 'data': labels}
        rot = _maybe_rotate_labels(labels)
        if rot:
            opt['xAxis']['axisLabel'] = rot
        opt['yAxis'] = {'type': 'value'}
        # 分箱数超过 dataZoom 阈值时底部有滑块，x 轴名底距加大避开
        x_bottom = 64 if len(labels) > self.DATAZOOM_THRESHOLD else 42
        opt['graphic'] = _axis_name_graphics(y_col, [texts['axis_frequency']], x_bottom=x_bottom)
        opt['series'] = [{
            'name': y_col, 'type': 'bar', 'data': counts.tolist(),
            'barCategoryGap': '0%',  # 直方图无间隙
            'itemStyle': {'color': '#5470c6'},
        }]
        return opt

    def _stacked_bar(self, df, x, y, title, texts):
        """堆叠柱状图：复用 _bar 后为每个 series 加 stack 字段。"""
        opt = self._bar(df, x, y, title, texts)
        for s in opt['series']:
            s['stack'] = 'total'
            s['emphasis'] = {'focus': 'series'}
        return opt

    def _bubble(self, df, x, y, title, texts):
        """气泡图：3 列数值（x, y, size），symbolSize 按第三列缩放。"""
        opt = self._base(title, texts)
        if len(y) < 2:
            raise ChartError(
                "气泡图至少需要 2 个 Y 轴数值列（y 值 + size 值）",
                ErrorCode.CHART_CONFIG_ERROR,
                details={'given_y': y, 'suggestion': '提供 --y-axis <y_col> <size_col> 两个数值列'},
            )
        y_col, size_col = y[0], y[1]
        if not pd.api.types.is_numeric_dtype(df[x]):
            raise ChartError(
                f"气泡图的 X 轴需要数值列，当前列 '{x}' 不是数值类型",
                ErrorCode.CHART_CONFIG_ERROR,
                details={'given_x': x, 'suggestion': '选择数值类型的 X 轴列，或用 transform_code 生成数值列'},
            )
        label_col, color_col = self._label_col, self._color_by
        numeric_color = color_col is not None and pd.api.types.is_numeric_dtype(df[color_col])
        max_size = float(df[size_col].max())
        if max_size <= 0 or np.isnan(max_size):
            max_size = 1.0
        opt['xAxis'] = {'type': 'value', 'scale': True}
        opt['yAxis'] = {'type': 'value', 'scale': True}
        opt['graphic'] = _axis_name_graphics(x, [y_col])
        # tooltip / symbolSize 是 JS 函数（ECharts 字符串模板不支持 {c[0]} 数组索引），
        # 使用占位符，在 _save_html 中替换为真正的 JS 函数。
        # tooltip 显示 身份（label_col）+ 各维度 "列名: 值"
        dims = [x, y_col, size_col] + ([color_col] if numeric_color else [])
        self._bubble_tooltip_js = _point_tooltip_js(dims, has_label=label_col is not None)
        opt['tooltip'] = {'trigger': 'item', 'formatter': self._BUBBLE_TOOLTIP_PLACEHOLDER}
        self._bubble_symbolsize_js = f'function(v){{ return Math.sqrt(v[2]/{max_size})*50+5; }}'

        def _series(sub):
            return {'type': 'scatter', 'symbolSize': self._BUBBLE_SYMBOLSIZE_PLACEHOLDER,
                    'data': _labeled_points(sub, x, [y_col, size_col], label_col,
                                            color_col if numeric_color else None, True)}

        if color_col is not None and not numeric_color:
            # 类别列 → 按类别拆 series（NaN 归入"未分类"，不静默丢点）
            color_s = df[color_col].where(df[color_col].notna(), texts['series_uncategorized']).astype(str)
            opt['series'] = [dict(_series(df[color_s == cat]), name=cat) for cat in color_s.unique()]
        else:
            opt['series'] = [dict(_series(df), name=texts['series_bubble'])]
            if numeric_color:
                cvals = [p['value'][3] for p in opt['series'][0]['data'] if p['value'][3] is not None]
                if cvals:
                    opt['visualMap'] = {'min': min(cvals), 'max': max(cvals), 'dimension': 3,
                                        'calculable': True, 'orient': 'vertical', 'right': '1%', 'top': 'center'}
                    opt['grid']['right'] = '10%'  # 为右侧 visualMap 留出空间
        return opt

    def _pareto(self, df, x, y, title, texts):
        """帕累托图：排序后的 bar + 累积百分比折线（双 yAxis）。"""
        num_cols = df.select_dtypes(include=[np.number]).columns
        y_col = y[0] if y else (num_cols[0] if len(num_cols) else None)
        if y_col is None:
            raise ChartError(
                "帕累托图需要至少一个数值列",
                ErrorCode.CHART_CONFIG_ERROR,
                details={'suggestion': '提供数值类型的列，或用 transform_code 生成数值列'},
            )
        df_s = df[[x, y_col]].dropna().sort_values(y_col, ascending=False).reset_index(drop=True)
        x_data = _safe_str_list(df_s[x])
        vals = _sanitize_series(df_s[y_col].tolist())
        total = sum(v for v in vals if v is not None) or 1.0
        cum, s = [], 0.0
        for v in vals:
            s += (v if v is not None else 0)
            cum.append(round(s / total * 100, 2))
        opt = self._base(title, texts, x_data_len=len(x_data))
        opt['tooltip'] = {'trigger': 'axis', 'formatter': self._TOOLTIP_FORMATTER_AXIS}
        x_axis = {'type': 'category', 'data': x_data}
        rot = _maybe_rotate_labels(x_data)
        if rot:
            x_axis['axisLabel'] = rot
        opt['xAxis'] = [x_axis]
        opt['yAxis'] = [
            {'type': 'value', 'position': 'left'},
            {'type': 'value', 'max': 100, 'position': 'right',
             'axisLabel': {'formatter': '{value}%'}},
        ]
        opt['graphic'] = _axis_name_graphics(x, [y_col, '%'])
        opt['series'] = [
            {'name': y_col, 'type': 'bar', 'data': vals, 'yAxisIndex': 0,
             'itemStyle': {'color': '#5470c6'}},
            {'name': texts['series_pareto'], 'type': 'line', 'data': cum, 'yAxisIndex': 1,
             'lineStyle': {'color': '#ee6666'}, 'symbol': 'circle', 'symbolSize': 6},
        ]
        return opt

    def _combo(self, df, x, y, title, texts):
        """组合图（双轴）：第一个 Y 列走 bar（左轴），其余 Y 列走 line（右轴）。"""
        if len(y) < 2:
            raise ChartError(
                "组合图至少需要 2 个 Y 轴列（bar 列 + line 列）",
                ErrorCode.CHART_CONFIG_ERROR,
                details={'given_y': y, 'suggestion': '提供 --y-axis <bar_col> <line_col> [<line_col> ...]'},
            )
        x_data = _safe_str_list(df[x])
        opt = self._base(title, texts, x_data_len=len(x_data))
        opt['tooltip'] = {'trigger': 'axis', 'formatter': self._TOOLTIP_FORMATTER_AXIS}
        bar_col, line_cols = y[0], y[1:]
        x_axis = {'type': 'category', 'data': x_data}
        rot = _maybe_rotate_labels(x_data)
        if rot:
            x_axis['axisLabel'] = rot
        opt['xAxis'] = [x_axis]
        opt['yAxis'] = [
            {'type': 'value', 'position': 'left'},
            {'type': 'value', 'position': 'right',
             'axisLabel': {'formatter': '{value}'}},
        ]
        opt['graphic'] = _axis_name_graphics(x, [bar_col, ' / '.join(line_cols)])
        series = [{
            'name': bar_col, 'type': 'bar', 'data': _sanitize_series(df[bar_col].tolist()),
            'yAxisIndex': 0, 'itemStyle': {'color': '#5470c6'},
        }]
        line_colors = ['#ee6666', '#91cc75', '#fac858', '#73c0de']
        for i, col in enumerate(line_cols):
            series.append({
                'name': col, 'type': 'line', 'data': _sanitize_series(df[col].tolist()),
                'yAxisIndex': 1, 'smooth': True,
                'lineStyle': {'color': line_colors[i % len(line_colors)]},
            })
        opt['series'] = series
        return opt
