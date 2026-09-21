"""UX 回归检查：锁定身份标识 / 颜色编码 / tooltip 语义 / 真瀑布等关键不变量。

不依赖 pytest，直接运行：
    python {skill_base}/scripts/ux_regression_check.py
全部通过时打印 PASS 并以 0 退出；任一断言失败即抛 AssertionError。
"""

import sys
import tempfile
from pathlib import Path

if __name__ == '__main__' and __package__ is None:
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import pandas as pd
from scripts.chart_generator import ChartGenerator


def main():
    out = tempfile.mkdtemp()
    g = ChartGenerator(output_dir=out)
    texts = g._TEXTS['zh']

    df = pd.DataFrame({
        'name': ['张三', '李四', '王五', '赵六', '钱七'],
        'a': [1.0, 2.0, 3.0, 4.0, 5.0],
        'b': [5.0, 4.0, 3.0, 2.0, 1.0],
        'total': [60.0, 70.0, 80.0, 90.0, 100.0],
        'cls': ['一班', '一班', '二班', '二班', None],
    })

    # ---- P0: waterfall 必须是真瀑布（透明垫底 + 悬浮柱 + 带符号标签）----
    wf = pd.DataFrame({'m': ['1月', '2月', '3月'], 'd': [100.0, -30.0, 50.0]})
    opt = g._waterfall(wf, 'm', ['d'], 't', texts)
    base_s, bar_s = opt['series']
    assert base_s['stack'] == 'waterfall' and base_s['itemStyle']['color'] == 'transparent'
    assert base_s['tooltip']['show'] is False
    assert opt['legend']['data'] == ['d'], '图例只保留增量系列，垫底辅助系列不得出现'
    assert bar_s['stack'] == 'waterfall'
    # 负增量：柱高为绝对值，label 保留符号（-30）
    assert bar_s['data'][1]['value'] == 30.0 and bar_s['data'][1]['label']['formatter'] == '-30'
    assert bar_s['data'][1]['label']['formatter'].startswith('-'), '负增量 label 必须带负号'
    assert g._waterfall_tooltip_js is not None

    # ---- 方案 A: scatter 身份列 + tooltip 语义 ----
    g._label_col, g._color_by = 'name', None
    opt = g._scatter(df, 'a', ['b'], 't', texts)
    assert opt['series'][0]['data'][0] == {'value': [1.0, 5.0], 'name': '张三'}
    assert 'name' not in opt['series'][0]['data'][0]['value']  # 身份不污染数值维度
    assert opt['legend']['type'] == 'scroll', '系列名必须在图例中可见（可编辑）'
    # 轴名是可拖拽 graphic 文本：不压数字/轴线/图例，保存图片随画布生效
    assert 'name' not in opt['xAxis'] and 'name' not in opt['yAxis']
    g_ids = {el['id'] for el in opt['graphic']}
    assert g_ids == {'axisName-x', 'axisName-y0'}
    assert all(el['draggable'] for el in opt['graphic'])
    assert '"a"' in g._item_tooltip_js and '"b"' in g._item_tooltip_js, 'tooltip 必须含轴列名'

    # 无 label 列时 tooltip 仍含轴列名（修复裸元组 ({c}) 问题）
    g._label_col = None
    g._scatter(df, 'a', ['b'], 't', texts)
    assert '"a"' in g._item_tooltip_js

    # scatter 数值 color-by → visualMap 连续着色
    g._label_col, g._color_by = 'name', 'total'
    opt = g._scatter(df, 'a', ['b'], 't', texts)
    assert opt['visualMap']['dimension'] == 2
    assert opt['series'][0]['data'][0]['value'] == [1.0, 5.0, 60.0]

    # scatter 类别 color-by → 拆 series 分色；NaN 归入"未分类"不丢点；多系列保留图例
    g._color_by = 'cls'
    opt = g._scatter(df, 'a', ['b'], 't', texts)
    names = [s['name'] for s in opt['series']]
    assert names == ['一班', '二班', '未分类'], names
    assert sum(len(s['data']) for s in opt['series']) == 5
    assert 'legend' in opt, '多系列 scatter 必须保留图例'

    # ---- 方案 A: bubble 身份 + tooltip + 图例保留 ----
    g._label_col, g._color_by = 'name', None
    opt = g._bubble(df, 'a', ['b', 'total'], 't', texts)
    assert opt['series'][0]['data'][0]['name'] == '张三'
    assert "'<b>' + esc(p.name)" in g._bubble_tooltip_js
    assert 'legend' in opt, '系列名必须在图例中可见（可编辑）'

    # ---- 方案 A: boxplot 离群点带身份 ----
    bx = pd.DataFrame({'name': ['p1', 'p2', 'p3', 'p4', 'p5', 'p6'],
                       'v': [10.0, 11.0, 12.0, 10.5, 11.5, 100.0]})
    g._label_col, g._color_by = 'name', None
    opt = g._boxplot(bx, 'name', ['v'], 't', texts)
    outliers = opt['series'][1]['data']
    assert outliers and outliers[0]['name'] == 'p6' and outliers[0]['value'][1] == 100.0

    # ---- 方案 A: heatmap tooltip 显示 行 × 列: 值；visualMap 替代图例 ----
    hm = pd.DataFrame({'row': ['r1', 'r2'], 'c1': [1.0, 2.0], 'c2': [3.0, 4.0]})
    g._label_col = None
    opt = g._heatmap(hm, 'row', ['c1', 'c2'], 't', texts)
    assert '×' in g._item_tooltip_js and '["c1", "c2"]' in g._item_tooltip_js
    assert 'legend' not in opt, 'heatmap 有 visualMap，不得再显示 series 图例'

    # ---- 图表类型名不得充当图例/轴标签 ----
    tr = pd.DataFrame({'c': ['甲', '乙'], 'v': [1.0, 2.0]})
    assert 'legend' not in g._treemap(tr, 'c', ['v'], 't', texts)
    assert 'legend' not in g._graph(tr, 'c', ['v'], 't', texts)

    # ---- P2: bar/line 标签自动旋转；单系列 bar 数值标签 ----
    many = pd.DataFrame({'c': [f'类目{i}' for i in range(9)], 'v': list(range(9))})
    opt = g._bar(many, 'c', ['v'], 't', texts)
    assert opt['xAxis']['axisLabel'] == {'rotate': 30}
    assert opt['series'][0]['label']['show'] is True
    few = pd.DataFrame({'c': ['a', 'b'], 'v': [1, 2]})
    opt = g._bar(few, 'c', ['v'], 't', texts)
    assert 'axisLabel' not in opt['xAxis']
    opt = g._bar(few, 'c', ['v', 'v'], 't', texts)  # 多系列不加标签防遮挡
    assert 'label' not in opt['series'][0]
    opt = g._line(many, 'c', ['v'], 't', texts)
    assert opt['xAxis']['axisLabel'] == {'rotate': 30}

    # ---- P2: histogram 区间记号 + y 轴为"频数"而非图表类型名 + 图例保留 ----
    hg = pd.DataFrame({'v': [float(i) for i in range(50)]})
    opt = g._histogram(hg, None, ['v'], 't', texts)
    assert opt['xAxis']['data'][0].startswith('[') and opt['xAxis']['data'][0].endswith(')')
    assert opt['xAxis']['data'][-1].endswith(']'), '末箱必须为闭区间'
    gtexts = {el['id']: el['style']['text'] for el in opt['graphic']}
    assert gtexts == {'axisName-x': 'v', 'axisName-y0': '频数'}, 'histogram 轴名走可拖拽 graphic'
    assert 'legend' in opt

    # ---- P2: Okabe-Ito 色板；图例 scroll 防溢出 ----
    opt = g._bar(few, 'c', ['v'], 't', texts)
    assert opt['color'][0] == '#0072B2'
    assert opt['legend']['type'] == 'scroll'

    # ---- dataZoom 场景：图例必须上移避开底部滑块 ----
    big = pd.DataFrame({'c': [f'c{i}' for i in range(30)], 'v': list(range(30))})
    opt = g._bar(big, 'c', ['v'], 't', texts)
    assert 'dataZoom' in opt and opt['legend'].get('bottom') == 30, 'dataZoom 时图例不得压滑块'

    # ---- 双轴图轴名走 graphic：左轴 y0 / 右轴 y1 ----
    opt = g._combo(few.assign(v2=[2, 1]), 'c', ['v', 'v2'], 't', texts)
    gtexts = {el['id']: el['style']['text'] for el in opt['graphic']}
    assert gtexts == {'axisName-y0': 'v', 'axisName-y1': 'v2'}
    assert 'name' not in opt['yAxis'][0] and 'name' not in opt['yAxis'][1]

    # ---- 端到端: 自动探测 label、assumptions 上报、HTML 无占位符残留、千分位 ----
    g2 = ChartGenerator(output_dir=out)
    r = g2.generate_chart(df, 'scatter', title='t', x_axis='a', y_axis=['b'])
    assert r['chart']['success']
    assert r['chart']['assumptions'] and 'name' in r['chart']['assumptions'][0]
    html = Path(r['chart']['html_path']).read_text(encoding='utf-8')
    for ph in ('__ITEM_TOOLTIP__', '__WATERFALL_TOOLTIP__', '__BUBBLE_TOOLTIP__', '__BUBBLE_SYMBOLSIZE__'):
        assert ph not in html, f'占位符残留: {ph}'
    assert 'toLocaleString' in html, 'tooltip 数字必须千分位格式化'
    assert '张三' in html, '身份列值必须出现在散点图数据中'
    # 系列名/轴名重命名面板（分组布局 + 单击编辑 + 轴名拖拽）
    assert 'renamePanel' in html and 'chartOption' in html
    assert '__waterfall_base__' in html, '重命名面板必须排除内部辅助系列'
    assert '点击名称可修改' in html
    assert 'rename-group' in html and "addGroup('系列'" in html and "addGroup('轴'" in html, '面板必须按系列/轴分组'
    assert 'axisName-' in html and 'draggable' in html, '轴名必须是可拖拽 graphic'

    r = g2.generate_chart(df, 'scatter', title='t', x_axis='a', y_axis=['b'], label_col='不存在')
    assert not r['chart']['success'] and r['chart']['error']['code'] == 4003

    # ---- P0: </script> 注入防护（标签计数 parity 法）----
    # 数据值含 </script> 时，产物中 </script> 出现次数必须与干净图表完全一致
    #（多出的即为数据逃逸），且数据以 <\/ 转义形式存在。
    evil = pd.DataFrame({'c': ['</script><img src=x onerror=alert(1)>', 'b'], 'v': [1.0, 2.0]})
    r_clean = g2.generate_chart(evil.assign(c=['a', 'b']), 'bar', title='t', x_axis='c', y_axis=['v'])
    r_evil = g2.generate_chart(evil, 'bar', title='t', x_axis='c', y_axis=['v'])
    assert r_clean['chart']['success'] and r_evil['chart']['success']
    h_clean = Path(r_clean['chart']['html_path']).read_text(encoding='utf-8')
    h_evil = Path(r_evil['chart']['html_path']).read_text(encoding='utf-8')
    assert h_evil.count('</script>') == h_clean.count('</script>'), '数据值中的 </script> 不得逃逸'
    assert '<\\/script>' in h_evil, '数据值必须以 <\\/ 转义形式内嵌'

    # heatmap 路径：行标签内嵌进 tooltip JS（_js_dumps），同样不得逃逸
    hm_evil = pd.DataFrame({'row': ['</script>alert(1)', 'r2'], 'c1': [1.0, 2.0], 'c2': [3.0, 4.0]})
    r_hm = g2.generate_chart(hm_evil, 'heatmap', title='t', x_axis='row', y_axis=['c1', 'c2'])
    assert r_hm['chart']['success']
    h_hm = Path(r_hm['chart']['html_path']).read_text(encoding='utf-8')
    assert h_hm.count('</script>') == h_clean.count('</script>'), 'tooltip JS 中的行标签不得逃逸'

    print('PASS: all UX regression checks passed')


if __name__ == '__main__':
    main()
