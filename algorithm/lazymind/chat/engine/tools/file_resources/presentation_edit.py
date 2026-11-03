from __future__ import annotations

from collections import Counter
import json
import os
import posixpath
from pathlib import Path
import re
import shutil
import subprocess
from typing import Any
from xml.etree import ElementTree as ET
import zipfile


_FONT_TAGS = {'latin', 'ea', 'cs'}
_COLOR_TAGS = {'srgbClr', 'sysClr'}
_HEX_COLOR = re.compile(r'^[0-9A-F]{6}$')
_A_NS = 'http://schemas.openxmlformats.org/drawingml/2006/main'
_P_NS = 'http://schemas.openxmlformats.org/presentationml/2006/main'
_R_NS = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
_PKG_REL_NS = 'http://schemas.openxmlformats.org/package/2006/relationships'
_TITLE_PLACEHOLDERS = {'title', 'ctrTitle'}
_TITLE_NAME_RE = re.compile(r'(^|[\s_-])(title|heading|headline)([\s_-]|$)', re.IGNORECASE)


def _local_name(tag: str) -> str:
    return tag.rsplit('}', 1)[-1]


def _normalize_color(value: Any) -> str:
    color = str(value or '').strip().lstrip('#').upper()
    if not _HEX_COLOR.fullmatch(color):
        raise ValueError(f'invalid RGB color {value!r}; expected #RRGGBB')
    return color


def normalize_color_map(value: Any) -> dict[str, str]:
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError as exc:
            raise ValueError('color_map must be an object or a JSON object string') from exc
    if not isinstance(value, dict) or not value:
        raise ValueError('color_map must contain at least one old-to-new RGB mapping')
    return {_normalize_color(old): _normalize_color(new) for old, new in value.items()}


def normalize_page_numbers(value: Any) -> list[int] | None:
    """Normalize a 1-based page selector; None means every slide."""
    if value is None or value == '':
        return None
    if isinstance(value, int):
        values = [value]
    elif isinstance(value, str):
        try:
            values = [int(item.strip()) for item in value.split(',') if item.strip()]
        except ValueError as exc:
            raise ValueError('pages must contain 1-based integers, for example "1,3"') from exc
    elif isinstance(value, (list, tuple, set)):
        try:
            values = [int(item) for item in value]
        except (TypeError, ValueError) as exc:
            raise ValueError('pages must contain 1-based integers') from exc
    else:
        raise ValueError('pages must be an integer, list, or comma-separated string')
    if not values or any(page < 1 for page in values):
        raise ValueError('pages must contain 1-based integers')
    return list(dict.fromkeys(values))


def _is_title_shape(shape: ET.Element) -> bool:
    placeholder = shape.find(f'.//{{{_P_NS}}}ph')
    if placeholder is not None and placeholder.attrib.get('type', 'body') in _TITLE_PLACEHOLDERS:
        return True
    properties = shape.find(f'./{{{_P_NS}}}nvSpPr/{{{_P_NS}}}cNvPr')
    return bool(properties is not None and _TITLE_NAME_RE.search(properties.attrib.get('name', '')))


def _ordered_slide_parts(archive: zipfile.ZipFile) -> list[str]:
    fallback = sorted(
        (
            name for name in archive.namelist()
            if re.fullmatch(r'ppt/slides/slide\d+\.xml', name)
        ),
        key=lambda name: int(re.search(r'(\d+)\.xml$', name).group(1)),  # type: ignore[union-attr]
    )
    try:
        presentation = ET.fromstring(archive.read('ppt/presentation.xml'))
        relationships = ET.fromstring(archive.read('ppt/_rels/presentation.xml.rels'))
    except (KeyError, ET.ParseError):
        return fallback
    targets = {
        rel.attrib.get('Id'): rel.attrib.get('Target', '')
        for rel in relationships.findall(f'{{{_PKG_REL_NS}}}Relationship')
        if rel.attrib.get('Type', '').endswith('/slide')
    }
    ordered: list[str] = []
    for slide_id in presentation.findall(f'.//{{{_P_NS}}}sldId'):
        target = targets.get(slide_id.attrib.get(f'{{{_R_NS}}}id'))
        if not target:
            continue
        clean_target = target.lstrip('/')
        part = posixpath.normpath(
            clean_target if clean_target.startswith('ppt/') else posixpath.join('ppt', clean_target)
        )
        if part in archive.namelist():
            ordered.append(part)
    return ordered or fallback


def _set_scoped_shape_font(
    shape: ET.Element,
    *,
    source_font: str,
    target_font: str,
) -> int:
    replacements = 0
    properties = list(shape.findall(f'.//{{{_A_NS}}}rPr'))
    properties.extend(shape.findall(f'.//{{{_A_NS}}}defRPr'))
    properties.extend(shape.findall(f'.//{{{_A_NS}}}endParaRPr'))
    # A run may inherit its font. Materialize rPr only for an unconditional
    # replacement; source-specific edits cannot safely infer inherited fonts.
    if not source_font:
        for run in shape.findall(f'.//{{{_A_NS}}}r') + shape.findall(f'.//{{{_A_NS}}}fld'):
            run_properties = run.find(f'{{{_A_NS}}}rPr')
            if run_properties is None:
                run_properties = ET.Element(f'{{{_A_NS}}}rPr')
                run.insert(0, run_properties)
                properties.append(run_properties)
    seen: set[int] = set()
    later_children = {
        'latin': {'ea', 'cs', 'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
        'ea': {'cs', 'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
        'cs': {'sym', 'hlinkClick', 'hlinkMouseOver', 'rtl', 'extLst'},
    }
    for run_properties in properties:
        if id(run_properties) in seen:
            continue
        seen.add(id(run_properties))
        for local in _FONT_TAGS:
            node = run_properties.find(f'{{{_A_NS}}}{local}')
            if node is None:
                if source_font:
                    continue
                node = ET.Element(f'{{{_A_NS}}}{local}')
                children = list(run_properties)
                index = next(
                    (i for i, child in enumerate(children) if _local_name(child.tag) in later_children[local]),
                    len(children),
                )
                run_properties.insert(index, node)
                node.set('typeface', target_font)
                replacements += 1
                continue
            current = node.attrib.get('typeface', '')
            if (not source_font or current.casefold() == source_font.casefold()) and current != target_font:
                node.set('typeface', target_font)
                replacements += 1
    return replacements


def available_font_families() -> set[str]:
    """Return installed font family names without guessing a fallback font."""
    from lazymind.chat.engine.tools.file_resources.font_library import local_fonts
    managed = {alias for font in local_fonts() if font['embeddable'] for alias in font['aliases']}
    fc_list = shutil.which('fc-list')
    if fc_list:
        try:
            result = subprocess.run(
                [fc_list, '--format=%{family}\n'], check=True,
                capture_output=True, text=True, timeout=15,
            )
            return managed | {
                family.strip()
                for line in result.stdout.splitlines()
                for family in line.split(',')
                if family.strip()
            }
        except (OSError, subprocess.SubprocessError):
            pass

    roots = [
        Path('/System/Library/Fonts'), Path('/Library/Fonts'),
        Path.home() / 'Library' / 'Fonts', Path('/usr/share/fonts'),
        Path('/usr/local/share/fonts'), Path(os.environ.get('WINDIR', 'C:/Windows')) / 'Fonts',
    ]
    families: set[str] = set(managed)
    try:
        from fontTools.ttLib import TTFont
    except ImportError:
        TTFont = None  # type: ignore[assignment,misc]
    if TTFont is not None:
        for root in roots:
            if not root.is_dir():
                continue
            for path in root.rglob('*'):
                if path.suffix.lower() not in {'.ttf', '.otf', '.ttc'}:
                    continue
                try:
                    font = TTFont(str(path), lazy=True, fontNumber=0)
                    for record in font['name'].names:
                        if record.nameID in {1, 16}:
                            families.add(record.toUnicode().strip())
                    font.close()
                except Exception:
                    continue
    return {family for family in families if family}


def inspect_pptx(path: str | Path, installed_fonts: set[str] | None = None) -> dict[str, Any]:
    source = Path(path)
    if source.suffix.lower() != '.pptx':
        raise ValueError('presentation editing supports .pptx files only')
    fonts: Counter[str] = Counter()
    colors: Counter[str] = Counter()
    slide_count = 0
    try:
        with zipfile.ZipFile(source) as archive:
            names = set(archive.namelist())
            if '[Content_Types].xml' not in names or 'ppt/presentation.xml' not in names:
                raise ValueError('file is not a valid PowerPoint OOXML package')
            for name in names:
                if not name.startswith('ppt/') or not name.endswith('.xml'):
                    continue
                if re.fullmatch(r'ppt/slides/slide\d+\.xml', name):
                    slide_count += 1
                try:
                    root = ET.fromstring(archive.read(name))
                except ET.ParseError:
                    continue
                for node in root.iter():
                    local = _local_name(node.tag)
                    if local in _FONT_TAGS:
                        family = node.attrib.get('typeface', '').strip()
                        if family and not family.startswith('+'):
                            fonts[family] += 1
                    elif local in _COLOR_TAGS:
                        value = (node.attrib.get('val') or node.attrib.get('lastClr') or '').upper()
                        if _HEX_COLOR.fullmatch(value):
                            colors[value] += 1
    except zipfile.BadZipFile as exc:
        raise ValueError('file is not a valid PPTX ZIP package') from exc

    installed = installed_fonts if installed_fonts is not None else available_font_families()
    installed_folded = {family.casefold() for family in installed}
    return {
        'filename': source.name,
        'slide_count': slide_count,
        'fonts': [
            {
                'family': family,
                'references': count,
                'available': family.casefold() in installed_folded,
            }
            for family, count in fonts.most_common()
        ],
        'missing_fonts': [
            family for family in fonts
            if family.casefold() not in installed_folded
        ],
        'colors': [
            {'rgb': f'#{color}', 'references': count}
            for color, count in colors.most_common()
        ],
        'installed_font_count': len(installed),
    }


def rewrite_pptx(
    source_path: str | Path,
    output_path: str | Path,
    *,
    source_font: str | None = None,
    target_font: str | None = None,
    color_map: dict[str, str] | None = None,
    pages: Any = None,
    text_scope: str = 'all',
) -> dict[str, Any]:
    """Rewrite font/color OOXML attributes while preserving all other parts."""
    source = Path(source_path)
    output = Path(output_path)
    if source.suffix.lower() != '.pptx' or output.suffix.lower() != '.pptx':
        raise ValueError('presentation editing supports .pptx files only')
    normalized_source_font = str(source_font or '').strip()
    normalized_target_font = str(target_font or '').strip()
    selected_pages = normalize_page_numbers(pages)
    normalized_scope = str(text_scope or 'all').strip().lower()
    if normalized_scope not in {'all', 'titles', 'body'}:
        raise ValueError("text_scope must be 'all', 'titles', or 'body'")
    if normalized_source_font and not normalized_target_font:
        raise ValueError('target_font is required when source_font is provided')
    if not normalized_target_font and not color_map:
        raise ValueError('provide target_font and/or color_map')
    normalized_colors = {
        _normalize_color(old): _normalize_color(new)
        for old, new in (color_map or {}).items()
    }

    output.parent.mkdir(parents=True, exist_ok=True)
    font_replacements = 0
    color_replacements = 0
    changed_parts: list[str] = []
    try:
        with zipfile.ZipFile(source) as incoming, zipfile.ZipFile(
            output, 'w', compression=zipfile.ZIP_DEFLATED,
        ) as outgoing:
            names = incoming.namelist()
            if '[Content_Types].xml' not in names or 'ppt/presentation.xml' not in names:
                raise ValueError('file is not a valid PowerPoint OOXML package')
            ordered_slides = _ordered_slide_parts(incoming)
            if selected_pages is not None and any(page > len(ordered_slides) for page in selected_pages):
                raise ValueError(
                    f'pages exceed the {len(ordered_slides)}-slide deck: {selected_pages}'
                )
            selected_slide_parts = (
                {ordered_slides[page - 1] for page in selected_pages}
                if selected_pages is not None else set(ordered_slides)
            )
            for info in incoming.infolist():
                raw = incoming.read(info.filename)
                changed = False
                slide_match = re.fullmatch(r'ppt/slides/slide(\d+)\.xml', info.filename)
                scoped_font_edit = bool(normalized_target_font and (
                    selected_pages is not None or normalized_scope != 'all'))
                selected_slide = bool(slide_match and info.filename in selected_slide_parts)
                should_parse = (
                    info.filename.startswith('ppt/')
                    and info.filename.endswith('.xml')
                    and (not scoped_font_edit or selected_slide)
                )
                if should_parse:
                    try:
                        root = ET.fromstring(raw)
                    except ET.ParseError:
                        root = None
                    if root is not None:
                        if scoped_font_edit:
                            for shape in root.findall(f'.//{{{_P_NS}}}sp'):
                                title = _is_title_shape(shape)
                                if normalized_scope == 'titles' and not title:
                                    continue
                                if normalized_scope == 'body' and title:
                                    continue
                                count = _set_scoped_shape_font(
                                    shape,
                                    source_font=normalized_source_font,
                                    target_font=normalized_target_font,
                                )
                                font_replacements += count
                                changed = changed or count > 0
                        for node in root.iter():
                            local = _local_name(node.tag)
                            if local in _FONT_TAGS and normalized_target_font and not scoped_font_edit:
                                current = node.attrib.get('typeface', '')
                                if (
                                    (not normalized_source_font
                                     or current.casefold() == normalized_source_font.casefold())
                                    and current != normalized_target_font
                                ):
                                    node.set('typeface', normalized_target_font)
                                    font_replacements += 1
                                    changed = True
                            if local in _COLOR_TAGS and normalized_colors:
                                for attribute in ('val', 'lastClr'):
                                    current = node.attrib.get(attribute, '').upper()
                                    replacement = normalized_colors.get(current)
                                    if replacement and replacement != current:
                                        node.set(attribute, replacement)
                                        color_replacements += 1
                                        changed = True
                        if changed:
                            raw = ET.tostring(root, encoding='utf-8', xml_declaration=True)
                            changed_parts.append(info.filename)
                outgoing.writestr(info, raw)
    except zipfile.BadZipFile as exc:
        raise ValueError('file is not a valid PPTX ZIP package') from exc

    if font_replacements == 0 and color_replacements == 0:
        output.unlink(missing_ok=True)
        raise ValueError('the requested font/color values were not present; no new version was created')
    return {
        'font_replacements': font_replacements,
        'color_replacements': color_replacements,
        'changed_parts': changed_parts,
        'pages': selected_pages,
        'text_scope': normalized_scope,
    }
