"""Explicit chart colors independent of the template's brand accent colors."""
import re

from lxml import etree

C = 'http://schemas.openxmlformats.org/drawingml/2006/chart'
A = 'http://schemas.openxmlformats.org/drawingml/2006/main'
PALETTES = {
    'blue': ['326BE6', '94A3B8', '14A394', 'E69A35', '8767BD', '56B4E9', '6B7280', 'CC79A7'],
    'teal': ['139F91', '94A3B8', '4378C8', 'E69A35', '8767BD', '56B4E9', '6B7280', 'CC79A7'],
    'orange': ['DD8B32', '94A3B8', '4378C8', '139F91', '8767BD', '56B4E9', '6B7280', 'CC79A7'],
    'purple': ['8064B0', '94A3B8', '139F91', 'DD8B32', '4378C8', '56B4E9', '6B7280', 'CC79A7'],
}


def chart_colors(palette=None, index=0):
    if palette is None:
        return list(PALETTES[list(PALETTES)[index % len(PALETTES)]])
    if isinstance(palette, str) and palette in PALETTES:
        return list(PALETTES[palette])
    if not isinstance(palette, list) or not 2 <= len(palette) <= 30:
        raise ValueError('Chart palette must be blue/teal/orange/purple or 2-30 distinct RGB colors')
    colors = [str(c).lstrip('#').upper() for c in palette]
    if any(not re.fullmatch('[0-9A-F]{6}', c) for c in colors) or len(set(colors)) != len(colors):
        raise ValueError('Chart palette requires distinct six-digit RGB colors')
    return colors


def _fill(parent, color):
    props = parent.find(f'{{{C}}}spPr')
    if props is None:
        props = etree.Element(f'{{{C}}}spPr')
        # CT_Ser puts shape properties after tx and before points/values.
        before = next((child for child in parent if etree.QName(child).localname
                       not in ('idx', 'order', 'tx')), None)
        if before is None:
            parent.append(props)
        else:
            parent.insert(parent.index(before), props)
    for node in list(props):
        if etree.QName(node).localname in ('solidFill', 'noFill', 'gradFill', 'pattFill', 'blipFill'):
            props.remove(node)
    fill = etree.Element(f'{{{A}}}solidFill')
    etree.SubElement(fill, f'{{{A}}}srgbClr', val=color)
    props.insert(0, fill)
    line = props.find(f'{{{A}}}ln')
    if line is None:
        line = etree.SubElement(props, f'{{{A}}}ln')
    for node in list(line):
        if etree.QName(node).localname in ('solidFill', 'noFill', 'gradFill', 'pattFill'):
            line.remove(node)
    line_fill = etree.Element(f'{{{A}}}solidFill')
    etree.SubElement(line_fill, f'{{{A}}}srgbClr', val=color)
    line.insert(0, line_fill)


def style_chart(root, palette=None, index=0):
    colors = chart_colors(palette, index)
    series = root.findall(f'.//{{{C}}}ser')
    for i, item in enumerate(series):
        color = colors[i % len(colors)]
        _fill(item, color)
        points = item.findall(f'{{{C}}}dPt')
        for point in points:
            item.remove(point)
        # Distinguish categories in single-series bars and parts-of-whole charts.
        if len(series) == 1 and etree.QName(item.getparent()).localname in ('barChart', 'pieChart', 'doughnutChart'):
            count = item.find(f'.//{{{C}}}val//{{{C}}}ptCount')
            total = int(count.get('val', '0')) if count is not None else 0
            for point_index in range(total):
                point = etree.Element(f'{{{C}}}dPt')
                etree.SubElement(point, f'{{{C}}}idx', val=str(point_index))
                _fill(point, colors[point_index % len(colors)])
                before = next((child for child in item if etree.QName(child).localname
                               in ('dLbls', 'cat', 'val', 'extLst')), None)
                item.insert(item.index(before) if before is not None else len(item), point)
    return colors
