from __future__ import annotations

import importlib.util
from pathlib import Path

from PIL import Image, ImageSequence


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[4]


def _load_tools():
    path = _repo_root() / 'workflows' / 'image-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('image_workflow_tools', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _dejavu_font() -> str:
    path = Path('/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf')
    assert path.is_file()
    return str(path)


def test_caption_layout_centers_text_in_default_normalized_box():
    tools = _load_tools()
    layout = tools._caption_layout(
        (1000, 1000),
        'WOOF',
        font_path=_dejavu_font(),
    )

    assert layout['caption_box_px'] == (150, 750, 850, 930)
    left, top, right, bottom = layout['text_bbox_px']
    text_center = ((left + right) / 2, (top + bottom) / 2)
    box_center = (500, 840)
    assert abs(text_center[0] - box_center[0]) <= 1
    assert abs(text_center[1] - box_center[1]) <= 1
    assert 6 <= layout['font_size'] <= 180


def test_caption_layout_wraps_long_text_and_stays_inside_box():
    tools = _load_tools()
    layout = tools._caption_layout(
        (600, 600),
        'WOOF WOOF WOOF WOOF',
        [0.2, 0.7, 0.8, 0.95],
        _dejavu_font(),
    )

    box_left, box_top, box_right, box_bottom = layout['caption_box_px']
    left, top, right, bottom = layout['text_bbox_px']
    assert '\n' in layout['text']
    assert box_left <= left < right <= box_right
    assert box_top <= top < bottom <= box_bottom


def test_caption_layout_uses_requested_stroke_ratio():
    tools = _load_tools()
    layout = tools._caption_layout(
        (600, 600),
        'WOOF',
        font_path=_dejavu_font(),
        stroke_width_ratio=0.14,
    )

    assert layout['stroke_width_ratio'] == 0.14
    assert layout['stroke_width'] == max(1, round(layout['font_size'] * 0.14))


def test_caption_font_detection_rejects_missing_cjk_glyph_boxes():
    tools = _load_tools()

    missing = tools._font_missing_characters(_dejavu_font(), '收到！')

    assert missing == ['收', '到', '！']


def test_cjk_font_selection_never_falls_back_to_dejavu(monkeypatch):
    tools = _load_tools()
    monkeypatch.setattr(tools, '_CJK_FONT_CANDIDATES', (_dejavu_font(),))

    try:
        tools._caption_font_path('收到！')
    except RuntimeError as exc:
        assert 'does not cover every caption character' in str(exc)
    else:
        raise AssertionError('DejaVu must not be accepted for Chinese captions')


def test_caption_renderer_changes_only_pixels_inside_layout_box(tmp_path, monkeypatch):
    tools = _load_tools()
    source = tmp_path / 'source.png'
    Image.new('RGB', (400, 300), '#BBD7E8').save(source)
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path / 'uploads'))

    result = tools.meme_add_caption(
        str(source),
        'WOOF',
        caption_box=[0.15, 0.75, 0.85, 0.93],
        text_color='#FF0000',
        stroke_color='#0000FF',
        stroke_width_ratio=0.12,
    )

    assert result['success'] is True
    assert result['animated'] is False
    assert result['text_color'] == '#FF0000'
    assert result['stroke_color'] == '#0000FF'
    assert result['stroke_width_ratio'] == 0.12
    assert result['stroke_width'] == max(1, round(result['font_size'] * 0.12))
    output = Image.open(result['local_path']).convert('RGB')
    original = Image.open(source).convert('RGB')
    assert output.size == original.size
    assert output.crop((0, 0, 400, 220)).tobytes() == original.crop((0, 0, 400, 220)).tobytes()
    assert output.crop((60, 225, 340, 279)).tobytes() != original.crop((60, 225, 340, 279)).tobytes()


def test_caption_renderer_applies_caption_to_every_gif_frame(tmp_path, monkeypatch):
    tools = _load_tools()
    source = tmp_path / 'source.gif'
    frames = [
        Image.new('RGB', (320, 240), '#BBD7E8'),
        Image.new('RGB', (320, 240), '#E8D7BB'),
    ]
    frames[0].save(
        source,
        format='GIF',
        save_all=True,
        append_images=frames[1:],
        duration=[80, 120],
        loop=0,
    )
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path / 'uploads'))

    result = tools.meme_add_caption(
        str(source),
        'WOOF',
    )

    assert result['animated'] is True
    output = Image.open(result['local_path'])
    rendered_frames = [frame.convert('RGB') for frame in ImageSequence.Iterator(output)]
    assert len(rendered_frames) == 2
    for rendered, original in zip(rendered_frames, frames):
        assert rendered.crop((48, 180, 272, 224)).tobytes() != original.crop((48, 180, 272, 224)).tobytes()
