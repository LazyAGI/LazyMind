"""HTML 模板生成器。将 ECharts option 转为独立的交互式 HTML 文件。"""

import json
import re
import html as html_module
from pathlib import Path
from typing import Dict, Optional
from datetime import datetime


ECHARTS_VERSION = '5.4.3'
ECHARTS_WORDCLOUD_VERSION = '2.1.0'
# ECharts JS 内联到 HTML（避免 file:// 跨域加载失败，确保离线自包含）
_STATIC_DIR = Path(__file__).resolve().parent.parent / 'assets'
ECHARTS_LOCAL = _STATIC_DIR / 'echarts.min.js'
ECHARTS_WORDCLOUD_LOCAL = _STATIC_DIR / 'echarts-wordcloud.min.js'

# 缓存 ECharts JS 内容（避免每次生成图表都读文件）
_ECHARTS_JS_CACHE: Optional[str] = None
_WORDCLOUD_JS_CACHE: Optional[str] = None

# tooltip formatter 占位符：json.dumps 会将其序列化为字符串，
# _save_html 中再将带引号的占位符替换为真正的 JS 函数，避免 ECharts 把函数当纯文本渲染。
TOOLTIP_FORMATTER_AXIS = '__TOOLTIP_FORMATTER_AXIS__'

# bubble symbolSize 占位符：同上，json.dumps 会把 JS 函数序列化为字符串，
# 需在 _save_html 中替换为真正的 JS 函数，否则 ECharts 无法执行导致气泡不渲染。
BUBBLE_SYMBOLSIZE_PLACEHOLDER = '__BUBBLE_SYMBOLSIZE__'

# bubble tooltip formatter 占位符：ECharts 字符串模板不支持 {c[0]} 数组索引，
# 必须使用函数 formatter 才能正确显示数组数据的各字段值。
BUBBLE_TOOLTIP_PLACEHOLDER = '__BUBBLE_TOOLTIP__'

# 通用 item tooltip 占位符（scatter/boxplot 离群点/heatmap 共用）：
# 由渲染器设置 self._item_tooltip_js，_save_html 替换为真正的 JS 函数。
ITEM_TOOLTIP_PLACEHOLDER = '__ITEM_TOOLTIP__'

# waterfall tooltip 占位符：柱高是绝对值，tooltip 需显示带符号的原始增量。
WATERFALL_TOOLTIP_PLACEHOLDER = '__WATERFALL_TOOLTIP__'


def _load_echarts_js() -> str:
    """读取并缓存 ECharts JS 内容，用于内联到 HTML。"""
    global _ECHARTS_JS_CACHE
    if _ECHARTS_JS_CACHE is None:
        _ECHARTS_JS_CACHE = ECHARTS_LOCAL.read_text(encoding='utf-8')
    return _ECHARTS_JS_CACHE


def _load_wordcloud_js() -> str:
    """读取并缓存 wordcloud 插件 JS 内容，用于内联到 HTML。"""
    global _WORDCLOUD_JS_CACHE
    if _WORDCLOUD_JS_CACHE is None:
        _WORDCLOUD_JS_CACHE = ECHARTS_WORDCLOUD_LOCAL.read_text(encoding='utf-8')
    return _WORDCLOUD_JS_CACHE


class HTMLTemplateMixin:
    """HTML 模板生成方法，由 ChartGenerator 继承。"""

    def _tooltip_formatter_axis_js(self, texts: Dict[str, str]) -> str:
        no_data = texts['tooltip_no_data']
        return (
            "function(params) {\n"
            "                var esc = function(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#39;'); };\n"
            "                var res = esc(params[0].axisValue) + '<br/>';\n"
            "                params.forEach(function(p) {\n"
            f"                    var val = (p.value === null || p.value === undefined || p.value === 'NaN' || p.value === 'Infinity') ? '{no_data}' : (typeof p.value === 'number' ? p.value.toLocaleString() : esc(p.value));\n"
            "                    res += p.marker + esc(p.seriesName) + ': ' + val + '<br/>';\n"
            "                });\n"
            "                return res;\n"
            "            }"
        )

    def _save_html(self, option: Dict, title: str, width: int, height: int, chart_type: str = '', data_points: int = 0, texts: Dict[str, str] = None, annotation: str = '') -> Path:
        import hashlib
        self._ensure_output_dir()
        content_str = json.dumps(option, ensure_ascii=False, sort_keys=True)
        suffix = hashlib.md5(content_str.encode()).hexdigest()[:6]
        safe_title = re.sub(r'[\\/:*?"<>|]', '_', title)[:30]
        filename = f"{safe_title}_{suffix}.html"
        path = self.output_dir / filename

        # XSS 防护：转义所有用户输入插入 HTML 的部分
        esc_title = html_module.escape(title)

        option_json = json.dumps(option, ensure_ascii=False, indent=2, allow_nan=False)
        # XSS 防护：转义 </ 防止数据值中的 </script> 提前闭合脚本上下文。
        # JSON/JS 字符串中 \/ 合法且求值后还原为 /，ECharts 拿到的值不变。
        # 必须在占位符替换为 JS 函数之前执行：注入的受信 JS 含正则 /</g，事后替换会破坏其语法。
        option_json = option_json.replace('</', '<\\/')

        # 将占位符字符串替换为真正的 JS 函数（去掉 json.dumps 添加的引号）
        formatter_js = self._tooltip_formatter_axis_js(texts)
        option_json = option_json.replace(
            f'"{self._TOOLTIP_FORMATTER_AXIS}"',
            formatter_js
        )

        # bubble symbolSize 占位符替换为真正的 JS 函数
        if self._bubble_symbolsize_js:
            option_json = option_json.replace(
                f'"{self._BUBBLE_SYMBOLSIZE_PLACEHOLDER}"',
                self._bubble_symbolsize_js
            )
            self._bubble_symbolsize_js = None

        # bubble tooltip formatter 占位符替换为真正的 JS 函数
        if self._bubble_tooltip_js:
            option_json = option_json.replace(
                f'"{self._BUBBLE_TOOLTIP_PLACEHOLDER}"',
                self._bubble_tooltip_js
            )
            self._bubble_tooltip_js = None

        # 通用 item tooltip 占位符替换（scatter/boxplot 离群点/heatmap）
        if self._item_tooltip_js:
            option_json = option_json.replace(
                f'"{self._ITEM_TOOLTIP_PLACEHOLDER}"',
                self._item_tooltip_js
            )
            self._item_tooltip_js = None

        # waterfall tooltip 占位符替换
        if self._waterfall_tooltip_js:
            option_json = option_json.replace(
                f'"{self._WATERFALL_TOOLTIP_PLACEHOLDER}"',
                self._waterfall_tooltip_js
            )
            self._waterfall_tooltip_js = None

        # ECharts JS 内联到 HTML（避免 file:// 跨域加载失败，确保离线自包含）
        echarts_js = _load_echarts_js()

        # wordcloud 插件（仅 wordcloud 图表类型需要，同样内联）
        wordcloud_script = ''
        if chart_type == 'wordcloud':
            wordcloud_js = _load_wordcloud_js()
            wordcloud_script = f'<script>{wordcloud_js}</script>'

        # 宽高比，用于响应式高度计算
        aspect_ratio = width / height if height > 0 else 16 / 9

        # 数据点过多时：容器加横向滚动兜底 + 显示提示
        scroll_hint_html = ''
        if data_points and data_points > self.DATAZOOM_THRESHOLD:
            min_chart_width = data_points * self.MIN_PX_PER_POINT
            chart_style = (
                f"width: 100%; aspect-ratio: {aspect_ratio:.4f}; min-height: 300px; "
                f"min-width: {min_chart_width}px;"
            )
            wrapper_style = "width: 100%; position: relative; overflow-x: auto; overflow-y: hidden;"
            scroll_hint_html = f'<div class="scroll-hint">{html_module.escape(texts["scroll_hint"])}</div>'
        else:
            chart_style = f"width: 100%; aspect-ratio: {aspect_ratio:.4f}; min-height: 300px;"
            wrapper_style = "width: 100%; position: relative;"

        btn_save = html_module.escape(texts['btn_save'])
        btn_fullscreen = html_module.escape(texts['btn_fullscreen'])
        footer_text = html_module.escape(texts['footer'])
        annotation_html = f'<div class="annotation">{html_module.escape(annotation)}</div>' if annotation else ''
        edit_hint = html_module.escape(texts['edit_hint'])
        rename_hint = html_module.escape(texts['rename_hint'])
        rename_group_series = html_module.escape(texts['rename_group_series'])
        rename_group_axis = html_module.escape(texts['rename_group_axis'])
        title_updated_msg = html_module.escape(texts['title_updated'])
        html_lang = 'zh-CN' if texts is self._TEXTS['zh'] else 'en'
        # 无系列概念的图表不渲染"系列"重命名分组（如 pie/heatmap/gauge 等，series name 仅为图表类型名）
        show_series_js = 'false' if chart_type in getattr(self, '_NO_SERIES_CHART_TYPES', set()) else 'true'

        html = f"""<!DOCTYPE html>
<html lang="{html_lang}">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{esc_title}</title>
<script>{echarts_js}</script>
{wordcloud_script}
<style>
* {{ box-sizing: border-box; }}
body {{ font-family: 'Microsoft YaHei', 'PingFang SC', 'Hiragino Sans GB', 'Noto Sans CJK SC', sans-serif; margin: 0; padding: 20px; background: #f8f9fa; }}
.container {{ max-width: {width}px; width: 100%; margin: 0 auto; background: #fff; padding: 30px;
             border-radius: 12px; box-shadow: 0 4px 20px rgba(0,0,0,0.08); }}
.header {{ text-align: center; margin-bottom: 20px; padding-bottom: 15px; border-bottom: 2px solid #4CAF50; }}
.title {{ font-size: clamp(16px, 4vw, 24px); font-weight: 700; color: #2E7D32; margin: 0;
          cursor: text; outline: none; border-bottom: 2px dashed transparent; transition: border-color 0.2s; }}
.title:hover {{ border-bottom-color: #ccc; }}
.title:focus {{ border-bottom-color: #4CAF50; }}
.edit-hint {{ font-size: 11px; color: #bbb; margin-top: 4px; transition: color 0.3s; }}
.subtitle {{ font-size: 12px; color: #999; }}
.chart-wrapper {{ {wrapper_style} }}
.chart {{ {chart_style} }}
.scroll-hint {{ text-align: center; font-size: 12px; color: #888; margin: 8px 0; }}
.controls {{ text-align: center; margin: 15px 0; }}
.btn {{ display: inline-block; padding: 6px 16px; background: #4CAF50; color: #fff;
        border: none; border-radius: 4px; cursor: pointer; font-size: 13px; margin: 0 4px; }}
.btn:hover {{ background: #45a049; }}
.rename-panel {{ text-align: center; margin: 0 0 12px; font-size: 12px; color: #999; }}
.rename-hint {{ display: block; color: #bbb; margin-bottom: 6px; }}
.rename-group {{ margin: 4px 0; }}
.rename-group-label {{ color: #999; margin-right: 2px; }}
.rename-chip {{ display: inline-block; padding: 2px 10px; margin: 2px 4px; border: 1px dashed #ccc;
                border-radius: 10px; color: #555; cursor: text; outline: none; background: #fafafa; }}
.rename-chip:hover, .rename-chip:focus {{ border-color: #4CAF50; background: #fff; }}
.annotation {{ margin: 14px 0 0; padding: 12px 16px; background: #f2f7f2; border-left: 3px solid #4CAF50;
              border-radius: 6px; font-size: 13px; color: #444; line-height: 1.7; text-align: left; }}
.footer {{ margin-top: 25px; padding-top: 15px; border-top: 1px solid #eee; text-align: center;
          font-size: 11px; color: #aaa; }}
@media (max-width: 640px) {{
  .container {{ padding: 15px; }}
  .chart {{ min-height: 250px; }}
}}
@media print {{ .controls {{ display: none; }} .container {{ box-shadow: none; }} }}
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <h1 class="title" contenteditable="true" title="{edit_hint}">{esc_title}</h1>
    <div class="edit-hint">{edit_hint}</div>
    <div class="subtitle">Smart Charts &middot; {datetime.now().strftime('%Y-%m-%d %H:%M')}</div>
  </div>
  <div class="controls">
    <button class="btn" onclick="saveAsImage()">{btn_save}</button>
    <button class="btn" onclick="toggleFull()">{btn_fullscreen}</button>
  </div>
  {scroll_hint_html}
  <div class="rename-panel" id="renamePanel"></div>
  <div class="chart-wrapper">
    <div id="chart" class="chart"></div>
  </div>
  {annotation_html}
  <div class="footer">{footer_text} &middot; ECharts {ECHARTS_VERSION}</div>
</div>
<script>
var chartDom = document.getElementById('chart');
var chart = echarts.init(chartDom);
var chartOption = {option_json};
chart.setOption(chartOption);
// 系列名/轴名重命名面板：按"系列/轴"分组展示名称芯片，单击即可编辑，Enter 确认，Escape 取消。
// 轴名是图上的可拖拽 graphic 文本（id 以 axisName- 开头），改文字用局部 setOption 合并，
// 不回写位置属性，用户拖拽后的位置不受影响；保存图片时名称与位置均随画布生效。
(function() {{
  var panel = document.getElementById('renamePanel');
  var seriesEntries = [], axisEntries = [];
  if ({show_series_js}) {{
    (chartOption.series || []).forEach(function(s) {{
      // 排除 waterfall 透明垫底等内部辅助系列
      if (s.name && s.name.indexOf('__waterfall_base__') !== 0) seriesEntries.push(s);
    }});
  }}
  (chartOption.graphic || []).forEach(function(el) {{
    if (el.id && el.id.indexOf('axisName-') === 0) axisEntries.push(el);
  }});
  if (!seriesEntries.length && !axisEntries.length) {{ panel.style.display = 'none'; return; }}
  var hint = document.createElement('span');
  hint.className = 'rename-hint';
  hint.textContent = '{rename_hint}';
  panel.appendChild(hint);

  function addGroup(labelText, entries, apply) {{
    if (!entries.length) return;
    var group = document.createElement('div');
    group.className = 'rename-group';
    var label = document.createElement('span');
    label.className = 'rename-group-label';
    label.textContent = labelText;
    group.appendChild(label);
    entries.forEach(function(obj) {{
      var chip = document.createElement('span');
      chip.className = 'rename-chip';
      var original = apply('get', obj);
      chip.textContent = original;
      chip.contentEditable = 'true';
      chip.addEventListener('keydown', function(e) {{
        if (e.key === 'Enter') {{ e.preventDefault(); chip.blur(); }}
        if (e.key === 'Escape') {{ chip.textContent = original; chip.blur(); }}
      }});
      chip.addEventListener('blur', function() {{
        var v = chip.textContent.trim();
        if (v && v !== original) {{ apply('set', obj, v); original = v; }}
        else {{ chip.textContent = original; }}
      }});
      group.appendChild(chip);
    }});
    panel.appendChild(group);
  }}

  addGroup('{rename_group_series}', seriesEntries, function(op, s, v) {{
    if (op === 'get') return s.name;
    var old = s.name;
    s.name = v;
    // radar 等图 series.data[0].name 与 series.name 是同一名字，同步更新，避免 tooltip 显示旧名
    var syncedData = false;
    if (s.data && s.data.length && s.data[0] && s.data[0].name === old) {{
      s.data[0].name = v;
      syncedData = true;
    }}
    // legend.data 若显式列出系列名（如 waterfall），同步替换，否则改名后图例失配消失
    if (chartOption.legend && chartOption.legend.data) {{
      chartOption.legend.data = chartOption.legend.data.map(function(n) {{ return n === old ? v : n; }});
    }}
    // 局部合并：只更新系列名与图例，不触碰 graphic 等元素（保留轴名拖拽位置）；
    // 同步了 data[0].name 的系列需把完整 data 传回，否则 ECharts 保留旧 data.name
    chart.setOption({{ series: chartOption.series.map(function(x) {{
                        if (x === s && syncedData) {{ return {{ name: x.name, data: x.data }}; }}
                        return {{ name: x.name }};
                      }}),
                       legend: chartOption.legend }});
  }});
  addGroup('{rename_group_axis}', axisEntries, function(op, el, v) {{
    if (op === 'get') return el.style.text;
    el.style.text = v;
    // 按 id 局部合并，只改文字，不重置拖拽后的位置
    chart.setOption({{ graphic: [{{ id: el.id, style: {{ text: v }} }}] }});
  }});
}})();
window.addEventListener('resize', function() {{ chart.resize(); }});
new ResizeObserver(function() {{ chart.resize(); }}).observe(chartDom);
// {texts['comment_download_name']}
var currentDownloadName = {json.dumps(safe_title, ensure_ascii=False)};
function saveAsImage() {{
  // 保存前临时把 dataZoom 窗口重置为 0~100，导出完整数据后再恢复用户窗口，避免只导出当前可见区间
  var savedZooms = chart.getOption().dataZoom;
  var hasZoom = savedZooms && savedZooms.length;
  if (hasZoom) {{
    chart.setOption({{
      dataZoom: savedZooms.map(function(z) {{
        var c = {{}};
        for (var k in z) {{ c[k] = z[k]; }}
        c.start = 0;
        c.end = 100;
        c.animationDurationUpdate = 0;
        return c;
      }})
    }});
  }}
  requestAnimationFrame(function() {{
    var url = chart.getDataURL({{ type: 'png', pixelRatio: 2, backgroundColor: '#fff' }});
    var a = document.createElement('a'); a.href = url; a.download = currentDownloadName + '.png'; a.click();
    if (hasZoom) {{ chart.setOption({{ dataZoom: savedZooms }}); }}
  }});
}}
function toggleFull() {{
  var el = document.getElementById('chart');
  if (!document.fullscreenElement) el.requestFullscreen();
  else document.exitFullscreen();
}}
// {texts['comment_title_edit']}
// {texts['comment_title_sync']}
var titleEl = document.querySelector('.title');
var editHintEl = document.querySelector('.edit-hint');
var originalTitle = titleEl.textContent;
titleEl.addEventListener('keydown', function(e) {{
  if (e.key === 'Enter') {{ e.preventDefault(); titleEl.blur(); }}
  if (e.key === 'Escape') {{ titleEl.textContent = originalTitle; titleEl.blur(); }}
}});
titleEl.addEventListener('blur', function() {{
  var newTitle = titleEl.textContent.trim();
  if (newTitle && newTitle !== originalTitle) {{
    originalTitle = newTitle;
    chart.setOption({{ title: {{ text: newTitle }} }});
    currentDownloadName = newTitle.replace(/[\\\\/:*?"<>|]/g, '_').substring(0, 30);
    editHintEl.textContent = '{title_updated_msg}';
    editHintEl.style.color = '#4CAF50';
    setTimeout(function() {{ editHintEl.style.color = ''; }}, 3000);
  }} else {{
    titleEl.textContent = originalTitle;
  }}
}});
</script>
</body>
</html>"""
        path.write_text(html, encoding='utf-8')
        return path
