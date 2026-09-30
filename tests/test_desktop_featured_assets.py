"""Stage Unicode featured catalogs even when Windows defaults to cp1252."""
import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch

from PIL import Image

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('featured_assets', ROOT / 'desktop/scripts/stage-featured-assets.py')
featured = importlib.util.module_from_spec(spec)
spec.loader.exec_module(featured)


class FeaturedAssetsEncodingTests(unittest.TestCase):
    def test_windows_git_checkout_preserves_published_asset_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            (root / '.gitattributes').write_bytes((ROOT / '.gitattributes').read_bytes())
            fixtures = {
                'skills/featured/example/assets/demo.html': '<h1>精选案例</h1>\n<p>content</p>\n'.encode('utf-8'),
                'skills/featured/example/assets/nested/demo.svg': b'<svg>\n</svg>\n',
                'skills/featured/example/assets/cover.png': b'\x89PNG\r\n\x00binary\n',
            }
            for name, data in fixtures.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            subprocess.run(['git', '-C', str(root), '-c', 'core.autocrlf=false', 'add', '.'], check=True)
            for name in fixtures:
                (root / name).unlink()
            subprocess.run(['git', '-C', str(root), '-c', 'core.autocrlf=true',
                            'checkout-index', '--all'], check=True)
            for name, data in fixtures.items():
                with self.subTest(asset=name):
                    self.assertEqual((root / name).read_bytes(), data)

    def test_unicode_catalog_and_published_manifest_under_cp1252(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            runtime = root / 'runtime'
            assets = runtime / 'featured-skills/assets'
            assets.mkdir(parents=True)
            buffer = io.BytesIO()
            Image.new('RGB', (100, 100), 'white').save(buffer, format='PNG')
            cover = buffer.getvalue()
            html = '<h1>精选案例</h1>'.encode('utf-8')
            records = {}
            for name, data, role in [('cover.png', cover, 'cover'), ('demo.html', html, 'preview')]:
                (assets / name).write_bytes(data)
                records[role] = {'url': '/showcase-assets/' + name, 'sha256': hashlib.sha256(data).hexdigest(),
                                 'size': len(data), 'role': role}
            catalog = {'cases': [{'id': 'case', 'version': '1', 'title': '精选案例', 'assets': records,
                                  'localized': {'zh': {'title': '中文案例', 'cover': '/showcase-assets/cover.png'}}}]}
            catalog_path = runtime / 'featured-skills/catalog.json'
            catalog_path.write_text(json.dumps(catalog, ensure_ascii=False), encoding='utf-8')
            files = {'demo.html': {'sha256': hashlib.sha256(html).hexdigest(), 'sizeBytes': len(html)}}
            revision = hashlib.sha256(featured.encoded(files)).hexdigest()
            entry = {'filename': f'lazymind-featured-case-{revision[:16]}.zip', 'files': files, 'sizeBytes': 123,
                     'url': 'https://example.invalid/primary.zip',
                     'fallbackUrl': 'https://huggingface.co/datasets/example/案例.zip'}
            published = root / 'published.json'
            published.write_text(json.dumps({'bundles': {'case/1': entry}}, ensure_ascii=False), encoding='utf-8')
            original_read_text = Path.read_text
            # Reproduce the Actions locale on any host. Explicit encodings retain their normal behavior.
            def cp1252_default(path, encoding=None, errors=None):
                return original_read_text(path, encoding=encoding or 'cp1252', errors=errors)
            with patch.object(Path, 'read_text', cp1252_default), contextlib.redirect_stdout(io.StringIO()):
                featured.stage(runtime, published)
            staged = json.loads(catalog_path.read_text(encoding='utf-8'))
            downloads = json.loads((runtime / 'featured-skills/downloads.json').read_text(encoding='utf-8'))
            case = staged['cases'][0]
            self.assertEqual(case['title'], '精选案例')
            self.assertEqual(case['localized']['zh']['title'], '中文案例')
            self.assertEqual(case['localized']['zh']['cover'], case['assets']['cover']['url'])
            self.assertEqual(downloads['bundles']['case/1'], entry)
            self.assertFalse((assets / 'demo.html').exists())
            thumbnail = next(assets.rglob('*.jpg'))
            self.assertEqual(hashlib.sha256(thumbnail.read_bytes()).hexdigest(), case['assets']['cover']['sha256'])
            self.assertEqual(len(list(assets.rglob('*.jpg'))), 1)


if __name__ == '__main__':
    unittest.main()
