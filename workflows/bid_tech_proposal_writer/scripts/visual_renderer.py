"""Workflow-local Pillow renderer for architecture and ten UI/effect layouts."""

from __future__ import annotations

import json
import os
import re
import uuid
from pathlib import Path
from typing import Any

from PIL import Image, ImageDraw, ImageFont


IMAGE_TYPES = {
    'architecture', 'dashboard', 'workbench', 'list', 'form', 'approval',
    'analytics', 'mobile', 'admin', 'login', 'flow',
}
PALETTES = {
    'blue': {'main': '#1E5FAE', 'light': '#3A8EE6', 'accent': '#F59E0B', 'bg': '#F5F7FA',
             'text': '#1F2937', 'muted': '#64748B', 'line': '#D1D5DB', 'soft': '#E8F1FC'},
    'green': {'main': '#16834A', 'light': '#22A95E', 'accent': '#0EA5E9', 'bg': '#F0FDF4',
              'text': '#1F2937', 'muted': '#64748B', 'line': '#CBD5E1', 'soft': '#DCFCE7'},
    'red': {'main': '#B9352A', 'light': '#E05245', 'accent': '#F59E0B', 'bg': '#FAFAFA',
            'text': '#263544', 'muted': '#64748B', 'line': '#D5D9DF', 'soft': '#FDE8E7'},
    'gray': {'main': '#374151', 'light': '#6B7280', 'accent': '#2563EB', 'bg': '#FFFFFF',
             'text': '#111827', 'muted': '#64748B', 'line': '#D1D5DB', 'soft': '#F3F4F6'},
}
FONT_CANDIDATES = (
    '/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc',
    '/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc',
    '/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc',
    '/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',
)


def _run_root() -> Path:
    from lazymind.chat.engine.subagent.context import require_context
    workspace = str(require_context().workspace_path or '').strip()
    if not workspace:
        raise RuntimeError('The active Workflow workspace is unavailable.')
    root = Path(workspace) / 'bid-proposal-images' / uuid.uuid4().hex
    root.mkdir(parents=True, exist_ok=True)
    return root


def _font(size: int) -> ImageFont.FreeTypeFont | ImageFont.ImageFont:
    for candidate in FONT_CANDIDATES:
        if os.path.isfile(candidate):
            try:
                return ImageFont.truetype(candidate, size=size)
            except OSError:
                pass
    return ImageFont.load_default()


def _text(value: Any, limit: int = 80) -> str:
    result = re.sub(r'\s+', ' ', str(value or '')).strip()
    return result if len(result) <= limit else result[:max(1, limit - 1)] + '…'


def _list(value: Any, limit: int = 12) -> list[Any]:
    return list(value[:limit]) if isinstance(value, list) else []


def _label(item: Any, *keys: str) -> str:
    if isinstance(item, dict):
        for key in keys:
            if item.get(key) not in (None, ''):
                return _text(item[key])
        return _text(next(iter(item.values()), ''))
    if isinstance(item, (list, tuple)):
        return _text(item[0] if item else '')
    return _text(item)


def _value(item: Any, *keys: str) -> str:
    if isinstance(item, dict):
        for key in keys:
            if item.get(key) not in (None, ''):
                return _text(item[key])
    if isinstance(item, (list, tuple)) and len(item) > 1:
        return _text(item[1])
    return ''


def _fit(draw: ImageDraw.ImageDraw, value: Any, font: Any, width: int, lines: int = 2) -> list[str]:
    text = _text(value, 300)
    if not text:
        return []
    output: list[str] = []
    current = ''
    for char in text:
        candidate = current + char
        if current and draw.textbbox((0, 0), candidate, font=font)[2] > width:
            output.append(current)
            current = char
            if len(output) >= lines:
                break
        else:
            current = candidate
    if len(output) < lines and current:
        output.append(current)
    if len(''.join(output)) < len(text) and output:
        output[-1] = output[-1].rstrip('…') + '…'
    return output


def _draw_fit(draw: ImageDraw.ImageDraw, xy: tuple[int, int], value: Any, font: Any,
              fill: str, width: int, lines: int = 2, gap: int = 8) -> int:
    x, y = xy
    height = max(18, draw.textbbox((0, 0), '国Ag', font=font)[3])
    for line in _fit(draw, value, font, width, lines):
        draw.text((x, y), line, font=font, fill=fill)
        y += height + gap
    return y


def _card(draw: ImageDraw.ImageDraw, box: tuple[int, int, int, int], palette: dict[str, str],
          fill: str = '#FFFFFF', radius: int = 14, width: int = 2) -> None:
    draw.rounded_rectangle(box, radius=radius, fill=fill, outline=palette['line'], width=width)


def _header(draw: ImageDraw.ImageDraw, spec: dict[str, Any], palette: dict[str, str],
            width: int) -> int:
    draw.rectangle((0, 0, width, 96), fill=palette['main'])
    project = _text(spec.get('project_name') or '投标技术方案', 28)
    title = _text(spec.get('title') or spec.get('hero_title') or '系统功能效果', 45)
    draw.text((44, 18), project, font=_font(19), fill='#DDEBFA')
    draw.text((44, 47), title, font=_font(30), fill='#FFFFFF')
    menu = [_label(item, 'name', 'title') for item in _list(spec.get('menu'), 6)]
    if menu:
        x = width - 64
        for item in reversed(menu):
            tw = draw.textbbox((0, 0), item, font=_font(16))[2] + 34
            x -= tw
            draw.text((x + 17, 42), item, font=_font(16), fill='#FFFFFF')
    return 96


def _architecture(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w) + 28
    layers = _list(spec.get('layers'), 6) or [
        {'name': '接入层', 'modules': ['Web 门户', '移动端', '统一认证', 'API 网关']},
        {'name': '应用层', 'modules': ['业务管理', '流程中心', '数据分析', '运营驾驶舱']},
        {'name': '平台层', 'modules': ['服务治理', '消息中心', '规则引擎', '任务调度']},
        {'name': '数据层', 'modules': ['业务库', '缓存', '搜索引擎', '对象存储']},
        {'name': '资源层', 'modules': ['容器平台', '计算资源', '网络安全', '监控运维']},
    ]
    gap = 14
    layer_h = (h - top - 48 - gap * (len(layers) - 1)) // len(layers)
    accents = [p['main'], '#0F766E', '#7C3AED', '#B45309', '#334155', '#BE123C']
    for index, layer in enumerate(layers):
        y = top + index * (layer_h + gap)
        _card(draw, (42, y, w - 42, y + layer_h), p, p['soft'])
        draw.rounded_rectangle((58, y + 12, 260, y + layer_h - 12), radius=12, fill=accents[index])
        _draw_fit(draw, (80, y + 28), _label(layer, 'name', 'title'), _font(22), '#FFFFFF', 156, 2)
        modules = _list(layer.get('modules') if isinstance(layer, dict) else None, 8)
        if not modules and isinstance(layer, dict):
            modules = _list(layer.get('items') or layer.get('nodes') or layer.get('components'), 8)
        modules = modules or ['核心能力']
        area_x, area_w, item_gap = 282, w - 346, 12
        card_w = (area_w - item_gap * (len(modules) - 1)) // len(modules)
        for pos, module in enumerate(modules):
            x = area_x + pos * (card_w + item_gap)
            _card(draw, (x, y + 12, x + card_w, y + layer_h - 12), p)
            _draw_fit(draw, (x + 14, y + 29), _label(module, 'name', 'title', 'label'), _font(18), p['text'], card_w - 28, 2)


def _kpis(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], y: int, w: int) -> int:
    items = _list(spec.get('kpis') or spec.get('metrics') or spec.get('cards'), 4) or [
        {'label': '业务覆盖', 'value': '全流程'}, {'label': '统一管理', 'value': '一体化'},
        {'label': '运行状态', 'value': '可观测'}, {'label': '安全防护', 'value': '全链路'},
    ]
    gap, margin, height = 18, 46, 124
    cw = (w - margin * 2 - gap * (len(items) - 1)) // len(items)
    for index, item in enumerate(items):
        x = margin + index * (cw + gap)
        _card(draw, (x, y, x + cw, y + height), p)
        draw.text((x + 22, y + 20), _label(item, 'label', 'title', 'name'), font=_font(17), fill=p['muted'])
        draw.text((x + 22, y + 57), _value(item, 'value', 'count', 'detail') or '—', font=_font(31), fill=p['main'])
    return y + height


def _dashboard(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    y = _kpis(draw, spec, p, _header(draw, spec, p, w) + 24, w) + 22
    panels = _list(spec.get('panels'), 4) or [{'title': x} for x in ('业务趋势', '部门排行', '类型分布', '实时动态')]
    gap, margin = 20, 46
    pw, ph = (w - margin * 2 - gap) // 2, (h - y - 42 - gap) // 2
    for index, panel in enumerate(panels[:4]):
        x = margin + (index % 2) * (pw + gap)
        py = y + (index // 2) * (ph + gap)
        _card(draw, (x, py, x + pw, py + ph), p)
        draw.text((x + 22, py + 18), _label(panel, 'title', 'name'), font=_font(20), fill=p['text'])
        base = py + ph - 32
        values = [42, 75, 58, 91, 67, 82, 54]
        bw = (pw - 90) // len(values)
        for bar, value in enumerate(values):
            bh = int((ph - 100) * value / 100)
            draw.rounded_rectangle((x + 42 + bar * bw, base - bh, x + 42 + bar * bw + bw - 12, base),
                                   radius=5, fill=p['light'] if bar % 2 else p['main'])


def _workbench(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w) + 26
    columns = [
        ('待办事项', _list(spec.get('todos'), 8)), ('通知公告', _list(spec.get('notices'), 8)),
        ('快捷入口', _list(spec.get('quick'), 8)),
    ]
    margin, gap = 46, 20
    cw = (w - margin * 2 - gap * 2) // 3
    for col, (title, items) in enumerate(columns):
        x = margin + col * (cw + gap)
        _card(draw, (x, top, x + cw, h - 44), p)
        draw.rectangle((x, top, x + cw, top + 66), fill=p['main'] if col == 0 else p['soft'])
        draw.text((x + 24, top + 20), title, font=_font(22), fill='#FFFFFF' if col == 0 else p['main'])
        items = items or [{'title': f'{title}{index}', 'meta': '业务处理信息'} for index in range(1, 6)]
        row_y = top + 86
        for item in items[:7]:
            draw.ellipse((x + 24, row_y + 7, x + 38, row_y + 21), fill=p['accent'])
            _draw_fit(draw, (x + 52, row_y), _label(item, 'title', 'name', 'text'), _font(18), p['text'], cw - 82, 1)
            sub = _value(item, 'meta', 'sub', 'time')
            if sub:
                draw.text((x + 52, row_y + 32), sub, font=_font(14), fill=p['muted'])
            row_y += 78


def _table(draw: ImageDraw.ImageDraw, box: tuple[int, int, int, int], table: Any,
           p: dict[str, str], rows_limit: int = 8) -> None:
    x1, y1, x2, y2 = box
    value = table if isinstance(table, dict) else {}
    headers = [_text(item, 18) for item in _list(value.get('headers'), 8)] or ['编号', '名称', '类型', '状态', '更新时间', '操作']
    rows = _list(value.get('rows'), rows_limit) or [[f'#{1000 + idx}', '业务事项', '业务类', '处理中', '2026-08-13', '查看'] for idx in range(1, 7)]
    row_h = max(42, (y2 - y1) // (len(rows) + 1))
    col_w = (x2 - x1) // len(headers)
    draw.rectangle((x1, y1, x2, y1 + row_h), fill=p['soft'])
    for col, header in enumerate(headers):
        draw.text((x1 + col * col_w + 12, y1 + 12), header, font=_font(16), fill=p['main'])
    for row_index, raw in enumerate(rows):
        row = list(raw) if isinstance(raw, (list, tuple)) else [_label(raw, 'name', 'title')]
        y = y1 + (row_index + 1) * row_h
        if row_index % 2:
            draw.rectangle((x1, y, x2, min(y2, y + row_h)), fill='#F8FAFC')
        for col in range(len(headers)):
            draw.text((x1 + col * col_w + 12, y + 12), _text(row[col] if col < len(row) else '', 16),
                      font=_font(15), fill=p['text'])
        draw.line((x1, y, x2, y), fill=p['line'], width=1)


def _list_page(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w)
    side = 290
    draw.rectangle((0, top, side, h), fill='#FFFFFF')
    sidebar = _list(spec.get('sidebar'), 8) or ['事项列表', '事项办理', '事项分类', '流程配置', '统计分析']
    for index, item in enumerate(sidebar):
        y = top + 34 + index * 66
        if index == 1:
            draw.rounded_rectangle((20, y - 8, side - 20, y + 46), radius=10, fill=p['soft'])
        draw.text((48, y + 6), _label(item, 'name', 'title'), font=_font(19), fill=p['main'] if index == 1 else p['text'])
    _card(draw, (side + 28, top + 28, w - 40, h - 42), p)
    draw.text((side + 56, top + 52), _text(spec.get('title') or '业务事项列表'), font=_font(26), fill=p['text'])
    draw.rounded_rectangle((side + 56, top + 108, w - 68, top + 176), radius=10, fill=p['soft'])
    draw.text((side + 82, top + 130), '关键词 / 类型 / 状态', font=_font(17), fill=p['muted'])
    draw.rounded_rectangle((w - 190, top + 121, w - 92, top + 166), radius=8, fill=p['main'])
    draw.text((w - 162, top + 132), '查询', font=_font(17), fill='#FFFFFF')
    _table(draw, (side + 56, top + 204, w - 68, h - 88), spec.get('table'), p)


def _form(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w) + 24
    sections = _list(spec.get('sections'), 4) or [
        {'name': '基本信息', 'fields': [{'label': x, 'value': '请输入'} for x in ('申请人', '业务类型', '联系方式', '所属部门')]},
        {'name': '业务材料', 'fields': [{'label': x, 'value': '请选择或上传'} for x in ('事项名称', '办理方式', '证明材料', '备注')]},
    ]
    margin, gap = 56, 18
    panel_h = (h - top - 82 - gap * (len(sections) - 1)) // len(sections)
    for index, section in enumerate(sections):
        y = top + index * (panel_h + gap)
        _card(draw, (margin, y, w - margin, y + panel_h), p)
        draw.text((margin + 24, y + 18), _label(section, 'name', 'title'), font=_font(21), fill=p['main'])
        fields = _list(section.get('fields') if isinstance(section, dict) else None, 8)
        columns = 2
        fw = (w - margin * 2 - 90) // columns
        for pos, field in enumerate(fields):
            x = margin + 28 + (pos % columns) * (fw + 34)
            fy = y + 70 + (pos // columns) * 72
            draw.text((x, fy), _label(field, 'label', 'name'), font=_font(16), fill=p['text'])
            draw.rounded_rectangle((x + 130, fy - 8, x + fw, fy + 39), radius=7, outline=p['line'], width=2, fill='#FFFFFF')
            draw.text((x + 146, fy + 4), _value(field, 'value', 'hint') or '请选择', font=_font(15), fill=p['muted'])
    draw.rounded_rectangle((w - 220, h - 65, w - 60, h - 20), radius=9, fill=p['main'])
    draw.text((w - 172, h - 54), '提交申请', font=_font(18), fill='#FFFFFF')


def _approval(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w) + 26
    steps = _list(spec.get('steps'), 6) or [['申请提交', 'done'], ['材料初审', 'done'], ['部门审核', 'current'], ['领导审批', 'pending'], ['出证归档', 'pending']]
    left, right = 54, w - 54
    step_y = top + 36
    for index, step in enumerate(steps):
        x = left + index * (right - left) // max(1, len(steps) - 1)
        state = _value(step, 'state')
        color = p['main'] if state in {'done', 'current'} or index <= 2 else p['line']
        if index < len(steps) - 1:
            nx = left + (index + 1) * (right - left) // max(1, len(steps) - 1)
            draw.line((x + 18, step_y, nx - 18, step_y), fill=color, width=6)
        draw.ellipse((x - 18, step_y - 18, x + 18, step_y + 18), fill=color)
        draw.text((x - 45, step_y + 30), _label(step, 'name', 'title'), font=_font(16), fill=p['text'])
    panel_y = top + 120
    _card(draw, (54, panel_y, w * 2 // 3, h - 48), p)
    draw.text((82, panel_y + 24), '申请信息', font=_font(22), fill=p['main'])
    fields = _list(spec.get('fields'), 10) or [{'label': x, 'value': '业务信息'} for x in ('申请编号', '申请人', '事项名称', '所属部门', '提交时间', '当前状态')]
    for idx, item in enumerate(fields):
        x = 84 + (idx % 2) * 500
        y = panel_y + 84 + (idx // 2) * 74
        draw.text((x, y), _label(item, 'label', 'name') + '：', font=_font(17), fill=p['muted'])
        draw.text((x + 130, y), _value(item, 'value', 'detail') or '—', font=_font(17), fill=p['text'])
    _card(draw, (w * 2 // 3 + 20, panel_y, w - 54, h - 48), p)
    draw.text((w * 2 // 3 + 48, panel_y + 24), '审批意见流', font=_font(22), fill=p['main'])
    timeline = _list(spec.get('timeline'), 7) or [{'name': f'节点 {i}', 'comment': '处理意见', 'time': '已完成'} for i in range(1, 5)]
    for idx, item in enumerate(timeline):
        y = panel_y + 82 + idx * 105
        draw.ellipse((w * 2 // 3 + 52, y, w * 2 // 3 + 70, y + 18), fill=p['light'])
        draw.text((w * 2 // 3 + 88, y - 4), _label(item, 'name', 'title'), font=_font(17), fill=p['text'])
        _draw_fit(draw, (w * 2 // 3 + 88, y + 28), _value(item, 'comment', 'detail'), _font(15), p['muted'], 460, 2)


def _analytics(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    y = _kpis(draw, spec, p, _header(draw, spec, p, w) + 22, w) + 20
    panels = _list(spec.get('panels'), 4) or [{'title': x, 'chart': kind} for x, kind in [('部门对比', 'bar'), ('业务趋势', 'line'), ('办结分布', 'donut'), ('目标进度', 'progress')]]
    margin, gap = 46, 18
    pw, ph = (w - margin * 2 - gap) // 2, (h - y - 42 - gap) // 2
    for idx, panel in enumerate(panels[:4]):
        x, py = margin + (idx % 2) * (pw + gap), y + (idx // 2) * (ph + gap)
        _card(draw, (x, py, x + pw, py + ph), p)
        draw.text((x + 22, py + 18), _label(panel, 'title', 'name'), font=_font(20), fill=p['text'])
        chart = _value(panel, 'chart') or ('bar', 'line', 'donut', 'progress')[idx]
        if chart == 'donut':
            draw.ellipse((x + pw // 2 - 110, py + 80, x + pw // 2 + 110, py + 300), fill=p['light'])
            draw.ellipse((x + pw // 2 - 62, py + 128, x + pw // 2 + 62, py + 252), fill='#FFFFFF')
            draw.text((x + pw // 2 - 35, py + 168), '82%', font=_font(25), fill=p['main'])
        elif chart == 'progress':
            for row in range(5):
                ry = py + 84 + row * 55
                draw.text((x + 30, ry), f'指标 {row + 1}', font=_font(15), fill=p['muted'])
                draw.rounded_rectangle((x + 150, ry + 2, x + pw - 34, ry + 22), radius=8, fill=p['soft'])
                draw.rounded_rectangle((x + 150, ry + 2, x + 220 + row * 80, ry + 22), radius=8, fill=p['main'])
        else:
            points = [(x + 60 + n * 100, py + ph - 54 - ((n * 47 + idx * 31) % 180)) for n in range(7)]
            if chart == 'line':
                draw.line(points, fill=p['main'], width=6)
                for point in points:
                    draw.ellipse((point[0] - 7, point[1] - 7, point[0] + 7, point[1] + 7), fill=p['accent'])
            else:
                for px, py2 in points:
                    draw.rectangle((px - 22, py2, px + 22, py + ph - 44), fill=p['light'])


def _mobile(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    draw.rectangle((0, 0, w, h), fill=p['soft'])
    draw.text((90, 120), _text(spec.get('title') or '移动端业务应用'), font=_font(44), fill=p['main'])
    _draw_fit(draw, (92, 195), spec.get('desc') or '随时随地办理业务，进度实时可查', _font(24), p['muted'], 700, 3)
    features = _list(spec.get('cards') or spec.get('list'), 6) or [{'title': x, 'value': '可用'} for x in ('移动申报', '待办审批', '消息提醒', '进度查询')]
    for idx, item in enumerate(features):
        x, y = 96 + (idx % 2) * 370, 330 + (idx // 2) * 150
        _card(draw, (x, y, x + 330, y + 116), p)
        draw.text((x + 24, y + 22), _label(item, 'title', 'name'), font=_font(20), fill=p['text'])
        draw.text((x + 24, y + 62), _value(item, 'value', 'sub') or '便捷办理', font=_font(17), fill=p['main'])
    phone = (1120, 62, 1700, 1020)
    draw.rounded_rectangle(phone, radius=62, fill='#111827')
    draw.rounded_rectangle((1140, 86, 1680, 996), radius=45, fill='#FFFFFF')
    draw.rounded_rectangle((1300, 100, 1520, 126), radius=12, fill='#111827')
    draw.rounded_rectangle((1160, 150, 1660, 390), radius=20, fill=p['main'])
    _draw_fit(draw, (1194, 190), spec.get('banner_title') or '一站式移动服务', _font(30), '#FFFFFF', 430, 2)
    draw.text((1194, 285), _text(spec.get('banner_sub') or '业务在线办理 · 进度实时掌握', 40), font=_font(17), fill='#DDEBFA')
    for idx in range(6):
        x, y = 1180 + (idx % 2) * 238, 430 + (idx // 2) * 145
        _card(draw, (x, y, x + 216, y + 116), p, p['bg'])
        draw.ellipse((x + 18, y + 25, x + 64, y + 71), fill=p['light'])
        draw.text((x + 82, y + 36), f'功能 {idx + 1}', font=_font(18), fill=p['text'])


def _admin(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w) + 26
    _card(draw, (42, top, 480, h - 44), p)
    draw.text((70, top + 22), _text(spec.get('tree_title') or '组织架构'), font=_font(22), fill=p['main'])
    tree = _list(spec.get('tree'), 10) or [{'name': x, 'depth': d} for x, d in [('集团总部', 0), ('技术中心', 1), ('研发一部', 2), ('运营中心', 1), ('客户服务部', 2)]]
    for idx, item in enumerate(tree):
        depth = int(item.get('depth') or 0) if isinstance(item, dict) else 0
        y = top + 82 + idx * 58
        draw.text((74 + depth * 34, y), '▸ ' + _label(item, 'name', 'title'), font=_font(18), fill=p['main'] if idx == 2 else p['text'])
    _card(draw, (504, top, w - 42, h - 44), p)
    draw.text((532, top + 22), _text(spec.get('title') or '用户权限管理'), font=_font(22), fill=p['main'])
    _table(draw, (532, top + 78, w - 70, h - 88), spec.get('table'), p)
    modal = spec.get('modal') if isinstance(spec.get('modal'), dict) else None
    if modal:
        box = (1160, 290, 1780, 790)
        draw.rounded_rectangle(box, radius=18, fill='#FFFFFF', outline=p['main'], width=3)
        draw.text((1194, 322), _label(modal, 'title', 'name'), font=_font(23), fill=p['main'])


def _login(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    draw.rectangle((0, 0, w, h), fill=p['main'])
    draw.ellipse((-220, -180, 800, 840), fill=p['light'])
    hero = _text(spec.get('hero_title') or '一体化业务平台', 30)
    draw.text((150, 250), hero, font=_font(48), fill='#FFFFFF')
    draw.text((154, 330), _text(spec.get('hero_sub') or '业务协同 · 数据治理 · 安全运营'), font=_font(25), fill='#E7F1FC')
    features = _list(spec.get('hero_features'), 5) or ['全流程线上办理', '数据实时汇聚共享', '多端协同移动办公']
    for idx, item in enumerate(features):
        draw.ellipse((160, 430 + idx * 76, 180, 450 + idx * 76), fill=p['accent'])
        draw.text((205, 420 + idx * 76), _label(item, 'name', 'title'), font=_font(21), fill='#FFFFFF')
    box = (1130, 150, 1720, 920)
    draw.rounded_rectangle(box, radius=28, fill='#FFFFFF')
    draw.text((1195, 220), _text(spec.get('title') or '账号登录'), font=_font(32), fill=p['text'])
    draw.text((1195, 278), _text(spec.get('sub') or '欢迎登录，请输入账号信息'), font=_font(17), fill=p['muted'])
    for idx, placeholder in enumerate(('请输入账号 / 工号', '请输入登录密码', '请输入验证码')):
        y = 370 + idx * 100
        draw.rounded_rectangle((1195, y, 1655, y + 62), radius=9, outline=p['line'], width=2)
        draw.text((1220, y + 19), placeholder, font=_font(16), fill=p['muted'])
    draw.rounded_rectangle((1195, 690, 1655, 758), radius=10, fill=p['main'])
    draw.text((1375, 711), '登录', font=_font(20), fill='#FFFFFF')


def _flow(draw: ImageDraw.ImageDraw, spec: dict[str, Any], p: dict[str, str], w: int, h: int) -> None:
    top = _header(draw, spec, p, w) + 70
    nodes = _list(spec.get('nodes'), 8) or [{'name': x, 'sub': y} for x, y in [('业务申请', '材料提交'), ('智能预审', '自动校验'), ('部门审核', '协同处理'), ('领导审批', '决策确认'), ('出证归档', '闭环留痕')]]
    margin, gap = 64, 38
    nw = (w - margin * 2 - gap * (len(nodes) - 1)) // len(nodes)
    center_y = 470
    for idx, item in enumerate(nodes):
        x = margin + idx * (nw + gap)
        if idx < len(nodes) - 1:
            nx = margin + (idx + 1) * (nw + gap)
            draw.line((x + nw, center_y, nx - 8, center_y), fill=p['main'], width=7)
            draw.polygon([(nx - 8, center_y - 13), (nx + 14, center_y), (nx - 8, center_y + 13)], fill=p['main'])
        draw.rounded_rectangle((x, center_y - 116, x + nw, center_y + 116), radius=22,
                               fill='#FFFFFF', outline=p['main'], width=4)
        draw.ellipse((x + nw // 2 - 35, center_y - 88, x + nw // 2 + 35, center_y - 18), fill=p['soft'])
        draw.text((x + nw // 2 - 10, center_y - 69), str(idx + 1), font=_font(23), fill=p['main'])
        title = _label(item, 'name', 'title')
        tx = draw.textbbox((0, 0), title, font=_font(21))[2]
        draw.text((x + (nw - tx) // 2, center_y + 5), title, font=_font(21), fill=p['text'])
        sub = _value(item, 'sub', 'owner', 'detail')
        _draw_fit(draw, (x + 24, center_y + 52), sub, _font(16), p['muted'], nw - 48, 2)
    desc = _text(spec.get('desc') or '业务全程在线、节点状态可查、处理过程可追溯', 100)
    draw.text((70, top), desc, font=_font(22), fill=p['muted'])


RENDERERS = {
    'architecture': _architecture, 'dashboard': _dashboard, 'workbench': _workbench,
    'list': _list_page, 'form': _form, 'approval': _approval,
    'analytics': _analytics, 'mobile': _mobile, 'admin': _admin,
    'login': _login, 'flow': _flow,
}


def render_proposal_image(spec_json: str, image_type: str = 'architecture',
                          output_name: str = 'proposal_image.png') -> dict[str, Any]:
    """Render one source-grounded 16:9 proposal image with local Pillow code.

    Args:
        spec_json: JSON object or JSON string following the selected layout fields.
        image_type: architecture, dashboard, workbench, list, form, approval,
            analytics, mobile, admin, login, or flow.
        output_name: Safe filename only; runtime directories are selected internally.

    Returns:
        Metadata including the absolute local PNG path for save_artifacts.
    """
    if isinstance(spec_json, dict):
        spec = spec_json
    else:
        try:
            spec = json.loads(str(spec_json))
        except json.JSONDecodeError as exc:
            raise ValueError(f'spec_json is invalid: {exc.msg}') from exc
    if not isinstance(spec, dict):
        raise ValueError('spec_json must encode a JSON object.')
    kind = str(image_type or spec.get('type') or '').strip().lower()
    if kind not in IMAGE_TYPES:
        raise ValueError('image_type must be one of: ' + ', '.join(sorted(IMAGE_TYPES)))
    scheme = str(spec.get('color_scheme') or 'blue').lower()
    palette = PALETTES.get(scheme, PALETTES['blue'])
    filename = Path(str(output_name or '')).name
    if not filename.lower().endswith('.png'):
        filename += '.png'
    filename = re.sub(r'[^A-Za-z0-9._-]+', '_', filename).strip('._') or f'{kind}.png'
    root = _run_root()
    output = root / filename
    width, height = 1920, 1080
    image = Image.new('RGB', (width, height), palette['bg'])
    draw = ImageDraw.Draw(image)
    RENDERERS[kind](draw, spec, palette, width, height)
    draw.rectangle((1, 1, width - 2, height - 2), outline='#000000', width=3)
    image.save(output, 'PNG', optimize=True)
    if output.stat().st_size < 8000:
        raise RuntimeError(f'Rendered PNG is unexpectedly small: {output}')
    return {
        'path': str(output.resolve()), 'filename': output.name,
        'image_type': kind, 'width': width, 'height': height,
        'color_scheme': scheme if scheme in PALETTES else 'blue',
        'renderer': 'workflow_local_pillow',
    }
