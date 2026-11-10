"""Conservative PPTX-to-PPTX theme and layout application."""
from __future__ import annotations

from copy import deepcopy
import hashlib
from pathlib import Path
import re
from zipfile import ZipFile, ZIP_DEFLATED

from lxml import etree

from .pptx_template_fill import NS, _load, _xml
from .pptx_layout_check import inspect_layout
from .pptx_text_style import font_availability

TITLE_TYPES = {'title', 'ctrTitle'}
BODY_TYPES = {'body', 'obj', 'subTitle'}


def _theme_part(data: dict[str, bytes]) -> str | None:
    names = sorted(
        (name for name in data if re.fullmatch(r'ppt/theme/theme\d+\.xml', name)),
        key=lambda name: int(re.search(r'(\d+)\.xml$', name).group(1)),
    )
    return names[0] if names else None


def _theme_style(data: dict[str, bytes]) -> dict:
    name = _theme_part(data)
    if name is None:
        return {'part': None, 'colors': {}, 'major_fonts': [], 'minor_fonts': []}
    root = _xml(data[name])
    scheme = root.find('.//a:themeElements/a:clrScheme', NS)
    colors = {}
    if scheme is not None:
        for entry in scheme:
            value = next(iter(entry), None)
            if value is not None:
                color = value.get('lastClr') or value.get('val')
                if color and re.fullmatch(r'[0-9A-Fa-f]{6}', color):
                    colors[etree.QName(entry).localname] = color.upper()
    fonts = root.find('.//a:themeElements/a:fontScheme', NS)

    def families(kind):
        node = fonts.find(f'a:{kind}Font', NS) if fonts is not None else None
        result = []
        if node is not None:
            for key in ('latin', 'ea', 'cs'):
                child = node.find(f'a:{key}', NS)
                if child is not None and child.get('typeface'):
                    result.append(child.get('typeface'))
        return list(dict.fromkeys(result))

    return {
        'part': name, 'colors': colors,
        'major_fonts': families('major'), 'minor_fonts': families('minor'),
    }


def _shape_role(shape) -> str | None:
    ph = shape.find('p:nvSpPr/p:nvPr/p:ph', NS)
    kind = ph.get('type', 'body') if ph is not None else ''
    if kind in TITLE_TYPES:
        return 'title'
    if kind in BODY_TYPES:
        return 'body'
    props = shape.find('p:nvSpPr/p:cNvPr', NS)
    name = (props.get('name') if props is not None else '').casefold()
    if any(token in name for token in ('title', '标题')):
        return 'title'
    return None


def _shape_transform(shape):
    return shape.find('p:spPr/a:xfrm', NS)


def _blueprint(root) -> dict[str, list]:
    result = {'title': [], 'body': []}
    for shape in root.findall('.//p:sp', NS):
        role = _shape_role(shape)
        transform = _shape_transform(shape)
        if role and transform is not None:
            result[role].append(deepcopy(transform))
    return result


def _apply_blueprint(root, blueprint) -> int:
    changed = 0
    offsets = {'title': 0, 'body': 0}
    for shape in root.findall('.//p:sp', NS):
        role = _shape_role(shape)
        current = _shape_transform(shape)
        if role is None or current is None:
            continue
        index = offsets[role]
        offsets[role] += 1
        targets = blueprint.get(role) or []
        if index >= len(targets):
            continue
        target = targets[index]
        if etree.tostring(current) != etree.tostring(target):
            current.getparent().replace(current, deepcopy(target))
            changed += 1
    return changed


def _safe_background(root):
    background = root.find('p:cSld/p:bg', NS)
    if background is None or background.find('.//a:blip', NS) is not None:
        return None
    return deepcopy(background)


def _apply_background(root, background) -> bool:
    if background is None:
        return False
    common = root.find('p:cSld', NS)
    current = common.find('p:bg', NS)
    if current is not None:
        common.remove(current)
    common.insert(0, deepcopy(background))
    return True


def _set_shape_fonts(shape, family: str) -> int:
    if not family:
        return 0
    changed = 0
    props = []
    for xpath in ('.//a:rPr', './/a:defRPr', './/a:endParaRPr'):
        props.extend(shape.findall(xpath, NS))
    for node in props:
        for key in ('latin', 'ea', 'cs'):
            font = node.find(f'a:{key}', NS)
            if font is None:
                font = etree.Element('{%s}%s' % (NS['a'], key))
                later = {
                    'latin': {'ea', 'cs', 'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
                    'ea': {'cs', 'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
                    'cs': {'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
                }[key]
                children = list(node)
                index = next((index for index, child in enumerate(children)
                              if etree.QName(child).localname in later), len(children))
                node.insert(index, font)
            if font.get('typeface') != family:
                font.set('typeface', family)
                changed += 1
    return changed


def _replace_theme_colors(root, mapping: dict[str, str]) -> int:
    changed = 0
    for node in root.findall('.//a:srgbClr', NS):
        current = (node.get('val') or '').upper()
        if current in mapping and mapping[current] != current:
            node.set('val', mapping[current])
            changed += 1
    return changed


def apply_pptx_template(source: Path, template: Path, output: Path) -> dict:
    """Apply safe PPTX theme/layout cues while preserving source slide objects."""
    for label, path in (('source', source), ('template', template), ('output', output)):
        if path.suffix.lower() != '.pptx':
            raise ValueError(f'{label} must be a .pptx file')
    if source.resolve() == template.resolve():
        raise ValueError('source and template must be different PPTX files')
    if output.resolve() in {source.resolve(), template.resolve()}:
        raise ValueError('output must not overwrite source or template')
    source_hash = hashlib.sha256(source.read_bytes()).hexdigest()
    template_hash = hashlib.sha256(template.read_bytes()).hexdigest()
    infos, data, slides = _load(source)
    _, template_data, template_slides = _load(template)
    if not template_slides:
        raise ValueError('template contains no slides')

    source_style = _theme_style(data)
    target_style = _theme_style(template_data)
    if target_style['part'] is None:
        raise ValueError('template has no PowerPoint theme part')
    source_theme_parts = sorted(name for name in data if re.fullmatch(r'ppt/theme/theme\d+\.xml', name))
    for name in source_theme_parts:
        data[name] = template_data[target_style['part']]

    palette = {
        source_color: target_style['colors'][role]
        for role, source_color in source_style['colors'].items()
        if role in target_style['colors']
    }
    title_font = next((font for font in target_style['major_fonts'] if font and not font.startswith('+')), '')
    body_font = next((font for font in target_style['minor_fonts'] if font and not font.startswith('+')), '')
    if not title_font:
        title_font = body_font
    if not body_font:
        body_font = title_font
    font_report = font_availability({font for font in (title_font, body_font) if font})

    source_size = _xml(data['ppt/presentation.xml']).find('p:sldSz', NS)
    target_size = _xml(template_data['ppt/presentation.xml']).find('p:sldSz', NS)
    same_size = bool(source_size is not None and target_size is not None
                     and source_size.get('cx') == target_size.get('cx')
                     and source_size.get('cy') == target_size.get('cy'))
    warnings = []
    if not same_size:
        warnings.append({
            'code': 'PAGE_SIZE_PRESERVED',
            'message': 'Template page size differs; source page size and geometry were preserved.',
        })
    if font_report['unavailable_locally']:
        warnings.append({
            'code': 'TEMPLATE_FONTS_UNAVAILABLE',
            'fonts': font_report['unavailable_locally'],
            'message': 'Template fonts are unavailable locally; no fallback font was substituted.',
        })

    layout_changes = 0
    background_changes = 0
    font_changes = 0
    color_changes = 0
    changed_parts = set(source_theme_parts)
    blueprints = [_blueprint(root) for _, root in template_slides]
    backgrounds = [_safe_background(root) for _, root in template_slides]
    source_checks = {
        page: inspect_layout(data, name, root, page)
        for page, (name, root) in enumerate(slides, 1)
    }

    for page, (name, root) in enumerate(slides, 1):
        target_index = min(page - 1, len(template_slides) - 1)
        if same_size:
            layout_changes += _apply_blueprint(root, blueprints[target_index])
        if _apply_background(root, backgrounds[target_index]):
            background_changes += 1
        for shape in root.findall('.//p:sp', NS) + root.findall('.//p:graphicFrame', NS):
            font_changes += _set_shape_fonts(shape, title_font if _shape_role(shape) == 'title' else body_font)
        color_changes += _replace_theme_colors(root, palette)
        data[name] = etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)
        changed_parts.add(name)

    # Apply palette/font mapping to charts and diagrams without changing their data.
    source_font_map = {
        old.casefold(): body_font
        for old in [*source_style['major_fonts'], *source_style['minor_fonts']]
        if old and body_font
    }
    for name, raw in list(data.items()):
        if not name.startswith('ppt/') or not name.endswith('.xml') or name in changed_parts:
            continue
        try:
            root = _xml(raw)
        except etree.XMLSyntaxError:
            continue
        local_colors = _replace_theme_colors(root, palette)
        local_fonts = 0
        for node in root.xpath('.//a:latin | .//a:ea | .//a:cs', namespaces=NS):
            replacement = source_font_map.get((node.get('typeface') or '').casefold())
            if replacement and node.get('typeface') != replacement:
                node.set('typeface', replacement)
                local_fonts += 1
        if local_colors or local_fonts:
            color_changes += local_colors
            font_changes += local_fonts
            data[name] = etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)
            changed_parts.add(name)

    # Reject a layout mapping that introduces new estimated overflow. Fall back
    # to theme/background/font application rather than publishing damaged pages.
    newly_overflowing = []
    if layout_changes:
        for page, (name, root) in enumerate(slides, 1):
            before = {(item['shape_id'], item['text']): item for item in source_checks[page]}
            for item in inspect_layout(data, name, root, page):
                old = before.get((item['shape_id'], item['text']))
                if item['status'] == 'overflow' and (old is None or old['status'] != 'overflow'):
                    newly_overflowing.append({'page': page, 'shape_id': item['shape_id']})
    if newly_overflowing:
        # Re-run without geometry updates. This keeps the operation atomic and
        # still applies the template's theme/font/palette safely.
        warnings.append({
            'code': 'LAYOUT_ADAPTATION_SKIPPED',
            'message': 'Template placeholder geometry introduced estimated overflow and was skipped.',
            'shapes': newly_overflowing,
        })
        return apply_pptx_template_style_only(source, template, output, warnings)

    output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(output, 'w', compression=ZIP_DEFLATED) as archive:
        originals = {item.filename: item for item in infos}
        for name, raw in data.items():
            archive.writestr(originals.get(name, name), raw)
    return {
        'source_sha256': source_hash,
        'template_sha256': template_hash,
        'slide_count': len(slides),
        'theme_parts_replaced': len(source_theme_parts),
        'font_replacements': font_changes,
        'color_replacements': color_changes,
        'background_pages': background_changes,
        'layout_shapes_adapted': layout_changes,
        'page_size_preserved': True,
        'original_unchanged': True,
        'template_unchanged': True,
        'unsupported': ['picture_backgrounds', 'slide_level_decorations', 'smartart_layout_remapping'],
        'font_availability': font_report,
        'warnings': warnings,
        'representation': 'native_pptx',
        'generation_status': 'completed',
        'visual_validation': 'conservative_estimate_only',
    }


def apply_pptx_template_style_only(source: Path, template: Path, output: Path, warnings=None) -> dict:
    """Style-only fallback used when placeholder geometry is unsafe."""
    infos, data, slides = _load(source)
    _, template_data, template_slides = _load(template)
    source_style, target_style = _theme_style(data), _theme_style(template_data)
    source_theme_parts = sorted(name for name in data if re.fullmatch(r'ppt/theme/theme\d+\.xml', name))
    for name in source_theme_parts:
        data[name] = template_data[target_style['part']]
    palette = {source_color: target_style['colors'][role]
               for role, source_color in source_style['colors'].items() if role in target_style['colors']}
    title_font = next((f for f in target_style['major_fonts'] if f and not f.startswith('+')), '')
    body_font = next((f for f in target_style['minor_fonts'] if f and not f.startswith('+')), '') or title_font
    title_font = title_font or body_font
    font_report = font_availability({font for font in (title_font, body_font) if font})
    backgrounds = [_safe_background(root) for _, root in template_slides]
    font_changes = color_changes = background_changes = 0
    for page, (name, root) in enumerate(slides, 1):
        if _apply_background(root, backgrounds[min(page - 1, len(backgrounds) - 1)]):
            background_changes += 1
        for shape in root.findall('.//p:sp', NS) + root.findall('.//p:graphicFrame', NS):
            font_changes += _set_shape_fonts(shape, title_font if _shape_role(shape) == 'title' else body_font)
        color_changes += _replace_theme_colors(root, palette)
        data[name] = etree.tostring(root, xml_declaration=True, encoding='UTF-8', standalone=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(output, 'w', compression=ZIP_DEFLATED) as archive:
        originals = {item.filename: item for item in infos}
        for name, raw in data.items():
            archive.writestr(originals.get(name, name), raw)
    return {
        'source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(),
        'template_sha256': hashlib.sha256(template.read_bytes()).hexdigest(),
        'slide_count': len(slides), 'theme_parts_replaced': len(source_theme_parts),
        'font_replacements': font_changes, 'color_replacements': color_changes,
        'background_pages': background_changes, 'layout_shapes_adapted': 0,
        'page_size_preserved': True, 'original_unchanged': True, 'template_unchanged': True,
        'unsupported': ['picture_backgrounds', 'slide_level_decorations', 'smartart_layout_remapping'],
        'font_availability': font_report,
        'warnings': list(warnings or []), 'representation': 'native_pptx',
        'generation_status': 'completed', 'visual_validation': 'conservative_estimate_only',
    }
