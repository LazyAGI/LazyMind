"""Validate inert SVG and exported figure files before publication."""
from __future__ import annotations

import base64
import re
import xml.etree.ElementTree as ET


def _embedded_raster(value):
    """Matplotlib embeds heatmaps and colorbars as inert PNG data, including line breaks."""
    match = re.fullmatch(r'data:image/(png|jpeg);base64,([\s\S]+)', value, re.I)
    if match is None:
        return False
    try:
        raw = base64.b64decode(re.sub(r'\s+', '', match[2]), validate=True)
    except ValueError:
        return False
    signature = b'\x89PNG\r\n\x1a\n' if match[1].lower() == 'png' else b'\xff\xd8\xff'
    return raw.startswith(signature)


def validate_svg(svg: str) -> None:
    if not isinstance(svg, str) or not svg.strip() or len(svg.encode()) > 2 * 1024 * 1024:
        raise ValueError('图表必须是非空且不超过 2 MiB 的 SVG')
    if re.search(r'<!\s*(?:DOCTYPE|ENTITY)', svg, re.I):
        raise ValueError('SVG 不允许 DOCTYPE/实体声明')
    try:
        root = ET.fromstring(svg)
    except ET.ParseError as exc:
        raise ValueError('图表必须是有效 SVG') from exc
    if root.tag not in ('svg', '{http://www.w3.org/2000/svg}svg'):
        raise ValueError('图表根节点必须是 SVG')
    forbidden = {'script', 'foreignobject', 'a', 'iframe', 'object', 'embed',
                 'animate', 'animatemotion', 'animatetransform', 'set', 'audio', 'video'}
    for node in root.iter():
        tag = node.tag.rsplit('}', 1)[-1].lower()
        if tag in forbidden:
            raise ValueError('SVG 包含主动内容或外部资源元素')
        for attribute, value in node.attrib.items():
            attribute = attribute.rsplit('}', 1)[-1].lower()
            if attribute.startswith('on') or attribute in {'src', 'base'}:
                raise ValueError('SVG 不允许事件处理器或外部资源')
            if attribute == 'href' and not re.fullmatch(r'#[A-Za-z_][\w:.-]*', value):
                if tag != 'image' or not _embedded_raster(value):
                    raise ValueError('SVG 只允许图内资源引用或内嵌 PNG/JPEG，不允许外部资源或可执行内容')
        styles = [*node.attrib.values()]
        if node.tag.rsplit('}', 1)[-1].lower() == 'style':
            styles.append(node.text or '')
        for style in styles:
            if '\\' in style or '/*' in style:
                raise ValueError('SVG 不允许转义或混淆的资源样式')
            if re.search(r'@import|expression\s*\(|javascript\s*:', style, re.I):
                raise ValueError('SVG 不允许可执行样式或外部样式')
            for reference in re.findall(r'url\s*\((.*?)\)', style, re.I | re.S):
                if not re.fullmatch(r'#[A-Za-z_][\w:.-]*', reference.strip()):
                    raise ValueError('SVG 不允许外部 URL')


def validate_charts(charts) -> None:
    if not isinstance(charts, list) or not 1 <= len(charts) <= 20:
        raise ValueError('charts 必须包含 1..20 张图表')
    identifiers = set()
    for chart in charts:
        if (not isinstance(chart, dict) or not {'id', 'title', 'columns', 'svg'} <= set(chart)
                or set(chart) - {'id', 'title', 'columns', 'svg', 'exports', 'caption'}):
            raise ValueError('每张图表必须包含 id/title/columns/svg')
        identifier = chart['id']
        if (not isinstance(identifier, str)
                or not re.fullmatch(r'[A-Za-z][A-Za-z0-9_-]{0,63}', identifier)
                or identifier in identifiers):
            raise ValueError('图表 ID 必须唯一且只包含字母、数字、下划线或短横线')
        identifiers.add(identifier)
        if not isinstance(chart['title'], str) or not chart['title'].strip():
            raise ValueError('图表标题不能为空')
        if 'caption' in chart and (not isinstance(chart['caption'], str)
                                   or not chart['caption'].strip() or len(chart['caption']) > 7100):
            raise ValueError('图注（含输入数据说明）必须是 1..7100 字的文本')
        columns = chart['columns']
        if (not isinstance(columns, list) or not columns
                or any(not isinstance(c, str) or not c.strip() for c in columns)
                or len(set(columns)) != len(columns)):
            raise ValueError('图表必须列出不重复的数据来源列')
        validate_svg(chart['svg'])
        if 'exports' in chart:
            exports = chart['exports']
            if not isinstance(exports, dict) or set(exports) != {'png', 'pdf'}:
                raise ValueError('图表导出必须同时包含 png/pdf')
            for kind, encoded in exports.items():
                if not isinstance(encoded, str) or len(encoded) > 6 * 1024 * 1024:
                    raise ValueError('图表导出内容过大或无效')
                try:
                    raw = base64.b64decode(encoded, validate=True)
                except ValueError as exc:
                    raise ValueError('图表导出不是有效 base64') from exc
                signature = b'\x89PNG\r\n\x1a\n' if kind == 'png' else b'%PDF-'
                if not raw.startswith(signature) or len(raw) > 4 * 1024 * 1024:
                    raise ValueError('图表导出格式错误或超过 4 MiB')
