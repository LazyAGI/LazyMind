#!/usr/bin/env python3
"""Publish immutable per-case asset ZIPs, or stage their catalog and small covers."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import shutil
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n').encode()


def stage(runtime, published, output=None):
    from PIL import Image, ImageOps
    featured = runtime / 'featured-skills'
    assets = featured / 'assets'
    catalog = json.loads((featured / 'catalog.json').read_text())
    expected = None if output else json.loads(published.read_text())
    if output:
        output.mkdir(parents=True, exist_ok=True)
    result = {'schemaVersion': 1, 'bundles': {}, 'local': {}}
    replacements = {}
    before = sum(p.stat().st_size for p in assets.rglob('*') if p.is_file())
    with tempfile.TemporaryDirectory(prefix='featured-thumbnails-', dir=featured) as temp:
        thumbnails = Path(temp)
        for case in catalog['cases']:
            files = {}
            for asset in case['assets'].values():
                old_url = asset['url']
                relative = old_url.removeprefix('/showcase-assets/')
                source = assets / relative
                data = source.read_bytes()
                if digest(data) != asset['sha256'] or len(data) != asset['size']:
                    raise ValueError(f'Compiled asset differs from catalog: {relative}')
                if asset['role'] == 'cover':
                    with Image.open(io.BytesIO(data)) as image:
                        image = ImageOps.exif_transpose(image).convert('RGB')
                        image.thumbnail((480, 480))
                        # Cover dimensions remain valid for the compiled catalog.
                        if min(image.size) < 64:
                            image = ImageOps.pad(image, (480, 320), color='white')
                        buffer = io.BytesIO()
                        image.save(buffer, format='JPEG', quality=78, optimize=True)
                        small = buffer.getvalue()
                        name = f"{case['id']}/{case['version']}/{digest(small)[:12]}-cover.jpg"
                        target = thumbnails / name
                        target.parent.mkdir(parents=True, exist_ok=True)
                        target.write_bytes(small)
                        replacements[old_url] = '/showcase-assets/' + name
                        asset.update(url=replacements[old_url], sha256=digest(small), size=len(small),
                                     mime='image/jpeg', width=image.width, height=image.height)
                        result['local'][name] = {'sha256': digest(small), 'sizeBytes': len(small)}
                else:
                    files[relative] = {'sha256': digest(data), 'sizeBytes': len(data)}
            if not files:
                continue
            key = f"{case['id']}/{case['version']}"
            revision = digest(encoded(files))
            filename = f"lazymind-featured-{case['id']}-{revision[:16]}.zip"
            if output:
                archive = output / filename
                with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as bundle:
                    for name in sorted(files):
                        info = zipfile.ZipInfo(name)
                        info.compress_type = zipfile.ZIP_DEFLATED
                        info.external_attr = 0o644 << 16
                        bundle.writestr(info, (assets / name).read_bytes())
                entry = {'filename': filename, 'sha256': digest(archive.read_bytes()),
                         'sizeBytes': archive.stat().st_size, 'files': files,
                         'url': 'https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/featured-assets/' + filename,
                         'fallbackUrl': 'https://huggingface.co/datasets/LazyAGI/LazyMind/resolve/main/featured-assets/' + filename}
            else:
                entry = expected['bundles'].get(key)
                if not entry or entry['filename'] != filename or entry['files'] != files:
                    raise ValueError(f'Featured assets changed: publish a new bundle for {key}')
            result['bundles'][key] = entry
        # Replace all presentation/localized URLs alongside the asset records.
        def rewrite(value):
            if isinstance(value, str):
                return replacements.get(value, value)
            if isinstance(value, list):
                return [rewrite(v) for v in value]
            if isinstance(value, dict):
                return {k: rewrite(v) for k, v in value.items()}
            return value
        if expected and set(expected['bundles']) != set(result['bundles']):
            raise ValueError('Published featured case list changed; republish before building')
        shutil.rmtree(assets)
        shutil.copytree(thumbnails, assets)
        (featured / 'catalog.json').write_bytes(encoded(rewrite(catalog)))
        (featured / 'downloads.json').write_bytes(encoded(result))
        if output:
            published.write_bytes(encoded(result))
    report = {'beforeBytes': before, 'bundledThumbnailBytes': sum(x['sizeBytes'] for x in result['local'].values()),
              'remoteZipBytes': sum(x['sizeBytes'] for x in result['bundles'].values()),
              'bundles': len(result['bundles'])}
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    parser.add_argument('--published', type=Path, default=ROOT / 'desktop/featured-assets.json')
    parser.add_argument('--publish', type=Path, help='Generate reviewed ZIPs before staging (release author only)')
    args = parser.parse_args()
    stage(args.runtime, args.published, args.publish)
