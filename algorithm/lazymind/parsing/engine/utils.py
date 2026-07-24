from pathlib import Path

from lazyllm.thirdparty import PIL


def _safe_name(value: str) -> str:
    normalized = ''.join(c if c.isalnum() or c in ('-', '_') else '_' for c in value.strip())
    return normalized or 'image'


def normalize_image_file(image_path: str, normalized_root: Path) -> str:
    src = Path(image_path).resolve()
    target_dir = normalized_root / _safe_name(src.parent.name or 'root')
    target_dir.mkdir(parents=True, exist_ok=True)
    dst = target_dir / f'{_safe_name(src.stem)}.jpg'

    with PIL.Image.open(src) as img:
        if getattr(img, 'n_frames', 1) > 1:
            img.seek(0)
        if img.mode in ('RGBA', 'LA') or (img.mode == 'P' and 'transparency' in img.info):
            rgba = img.convert('RGBA')
            background = PIL.Image.new('RGB', rgba.size, (255, 255, 255))
            background.paste(rgba, mask=rgba.getchannel('A'))
            rgb = background
        else:
            rgb = img.convert('RGB')
        rgb.save(dst, format='JPEG', quality=95)
    return str(dst)
