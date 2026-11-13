"""Bind registered PPT materials to durable deck paths before page generation."""
from __future__ import annotations

import hashlib
import json
import re
from html import unescape
from pathlib import Path
from urllib.parse import unquote

# Keep spaces inside paths, but do not start at a standalone prose slash.
# Quotes and backticks delimit paths without becoming part of the alias.
_LOCAL_IMAGE = re.compile(
    r'(?<![\w:/])(?:file://)?(?:/(?!\s)|[A-Za-z]:[\\/]|\.{1,2}/|images/|inputs[\\/]background_images[\\/])'
    r"[^\n<>\"'`]+?\.(?:png|jpe?g|gif|webp|bmp|svg)(?=$|[\s\"'`<>),;，。])", re.I,
)
_IMG_SRC = re.compile(r"(<img\b[^>]*?\bsrc\s*=\s*)([\"'])(.*?)\2", re.I)


def _key(value: str) -> str:
    key = unquote(unescape(str(value))).replace('\\', '/')
    if key.lower().startswith('file://'):
        key = key[len('file://'):]
        # Windows as_uri() adds a slash before the drive; native aliases do not.
        if re.match(r'^/[A-Za-z]:/', key):
            key = key[1:]
    return key


def _image_bindings(deck: Path, images: list) -> tuple[dict[str, str], dict[str, list[str]]]:
    aliases: dict[str, str] = {}
    hashes: dict[str, list[str]] = {}
    root = deck.resolve()
    for item in images:
        source = (item.get('path') or item.get('local_path')) if isinstance(item, dict) else item
        if not source or str(source).startswith(('http:', 'https:', 'data:')):
            continue
        file = (root / Path(source)).resolve()
        if not file.is_relative_to(root):
            continue
        relative = file.relative_to(root).as_posix()
        canonical = '../' + relative
        for alias in (source, str(file), relative, canonical):
            aliases[_key(alias)] = canonical
        if file.is_file():
            digest = hashlib.sha256(file.read_bytes()).hexdigest()
            hashes.setdefault(digest[:16], []).append(canonical)
    return aliases, hashes


def _materials(deck: Path) -> tuple[dict[str, str], dict[str, list[str]]]:
    path = deck / 'info_pack.json'
    assets = json.loads(path.read_text(encoding='utf-8')).get('user_assets', {}) if path.is_file() else {}
    aliases, hashes = _image_bindings(deck, assets.get('reference_images', []))
    for alias, target in assets.get('reference_image_aliases', {}).items():
        canonical = aliases.get(_key(target))
        if canonical:
            aliases[_key(alias)] = canonical
    return aliases, hashes


def _resolve(source: str, aliases: dict[str, str], hashes: dict[str, list[str]]) -> str | None:
    key = _key(source)
    if key.lower().startswith(('http:', 'https:', 'data:', '//')):
        return None
    if key in aliases:
        return aliases[key]
    # Core input snapshots carry a content-hash suffix. This also recognizes
    # prior attempts after their workspace was removed, without opening a path
    # supplied by the model or guessing a material from its display filename.
    match = re.search(r'-([0-9a-f]{16})\.[^. /]+$', key, re.I)
    matches = set(hashes.get(match[1].lower(), [])) if match else set()
    return next(iter(matches)) if len(matches) == 1 else None


def bind_prompt_materials(
    deck: Path, brief: str, *, background_image: dict | None = None,
) -> tuple[str, list[str]]:
    aliases, hashes = _materials(deck)
    background_aliases, background_hashes = _image_bindings(deck, [background_image] if background_image else [])
    required: list[str] = []

    def replace(match: re.Match) -> str:
        source = match[0]
        key = _key(source)
        input_slot = re.search(r'(?:^|/)inputs/(background_images|material_images)/', key)
        if input_slot and input_slot[1] == 'background_images':
            material = None
            background = _resolve(source, background_aliases, background_hashes)
        elif input_slot and input_slot[1] == 'material_images':
            material = _resolve(source, aliases, hashes)
            background = None
        else:
            material = aliases.get(key)
            background = background_aliases.get(key) if not material else None
            if not (material or background):
                material = _resolve(source, aliases, hashes)
                background = _resolve(source, background_aliases, background_hashes)
                if material and background and material != background:
                    raise ValueError(f'Ambiguous material image reference: {Path(source).name}; '
                                     'use its registered deck path')
        canonical = material or background
        if not canonical:
            raise ValueError(f'Unregistered material image reference: {Path(source).name}')
        file = (deck / 'pages' / canonical).resolve()
        if not file.is_file():
            raise ValueError(f'Required material image is missing: {canonical}')
        relative = file.relative_to(deck.resolve()).as_posix()
        if material and relative not in required:
            required.append(relative)
        return canonical

    return _LOCAL_IMAGE.sub(replace, brief), required


def normalize_material_sources(html: str, deck: Path) -> str:
    aliases, hashes = _materials(deck)

    def replace(match: re.Match) -> str:
        canonical = _resolve(match[3], aliases, hashes)
        return f'{match[1]}{match[2]}{canonical}{match[2]}' if canonical else match[0]

    return _IMG_SRC.sub(replace, html)


def missing_material_images(html: str, deck: Path, required: list[str]) -> list[str]:
    present = set()
    for match in _IMG_SRC.finditer(html):
        src = _key(match[3])
        if src.startswith(('http:', 'https:', 'data:', '//')):
            continue
        path = (deck / 'pages' / src).resolve()
        try:
            relative = path.relative_to(deck.resolve()).as_posix()
        except ValueError:
            continue
        if path.is_file():
            present.add(relative)
    return [path for path in required if path not in present]


def record_material_aliases(deck: Path, sources: list[dict], input_dir: Path | None = None) -> None:
    """Persist trusted source/input aliases when their bytes match a deck asset."""
    ip_path = deck / 'info_pack.json'
    if not ip_path.is_file():
        return
    ip = json.loads(ip_path.read_text(encoding='utf-8'))
    aliases, _ = _materials(deck)
    full_hashes = {}
    for relative in set(aliases.values()):
        path = (deck / 'pages' / relative).resolve()
        if path.is_file():
            full_hashes[hashlib.sha256(path.read_bytes()).hexdigest()] = path.relative_to(deck.resolve()).as_posix()
    saved = ip.setdefault('user_assets', {}).setdefault('reference_image_aliases', {})
    for item in sources:
        path = Path(str(item.get('path') or ''))
        if path.is_file():
            target = full_hashes.get(hashlib.sha256(path.read_bytes()).hexdigest())
            if target:
                for key in ('path', 'origin'):
                    source = item.get(key)
                    if source and not str(source).startswith(('http:', 'https:', 'data:')):
                        saved[str(source)] = target
    if input_dir and input_dir.is_dir():
        for path in input_dir.iterdir():
            if path.is_file() and path.suffix.lower() in {'.png', '.jpg', '.jpeg', '.webp', '.gif', '.bmp', '.svg'}:
                target = full_hashes.get(hashlib.sha256(path.read_bytes()).hexdigest())
                if target:
                    saved[str(path)] = target
    ip_path.write_text(json.dumps(ip, ensure_ascii=False, indent=2), encoding='utf-8')
