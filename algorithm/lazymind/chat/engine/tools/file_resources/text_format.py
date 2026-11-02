"""Validated, explicit text formatting shared by HTML and native PPTX edits."""
import math
import re


def text_format(font_size_pt=None, bold=None, italic=None, underline=None, text_color=None):
    result = {}
    if font_size_pt is not None:
        if (type(font_size_pt) not in (int, float) or not math.isfinite(font_size_pt)
                or not 1 <= font_size_pt <= 400):
            raise ValueError('font_size_pt must be a finite number between 1 and 400 pt')
        result['font_size_pt'] = round(font_size_pt, 2)
    for key, value in (('bold', bold), ('italic', italic), ('underline', underline)):
        if value is not None:
            if type(value) is not bool:
                raise ValueError(f'{key} must be true or false; omit it to preserve existing formatting')
            result[key] = value
    if text_color is not None:
        if not isinstance(text_color, str) or not re.fullmatch(r'#?[0-9a-fA-F]{6}', text_color):
            raise ValueError('text_color must be an exact RGB color, for example #CC0000')
        result['text_color'] = '#' + text_color.lstrip('#').upper()
    return result


def format_css(values):
    result = {}
    if 'font_size_pt' in values:
        result['font-size'] = f'{values["font_size_pt"]:g}pt'
    for key, css, yes, no in (('bold', 'font-weight', '700', '400'),
                              ('italic', 'font-style', 'italic', 'normal'),
                              ('underline', 'text-decoration', 'underline', 'none')):
        if key in values:
            result[css] = yes if values[key] else no
    if 'text_color' in values:
        result['color'] = values['text_color']
    return result
