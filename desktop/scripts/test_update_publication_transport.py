import importlib.util
from pathlib import Path
import unittest
from urllib.request import Request

spec = importlib.util.spec_from_file_location('publisher_transport', Path(__file__).with_name('publish-desktop-update.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class RedirectTests(unittest.TestCase):
    def test_redirects_keep_exact_https_origin_pins(self):
        handler = module.PinnedRedirects(['https://modelscope.cn/', 'https://cdn.example.invalid'])
        request = Request('https://modelscope.cn/datasets/fixture/updates/resolve/master/package.zip')
        allowed = handler.redirect_request(request, None, 302, '', {}, 'https://cdn.example.invalid/file?signature=fixture')
        self.assertEqual(allowed.full_url, 'https://cdn.example.invalid/file?signature=fixture')
        for target in ('http://modelscope.cn/file', 'https://evil.invalid/file',
                       'https://modelscope.cn.evil.invalid/file', 'https://user:secret@modelscope.cn/file'):
            with self.assertRaises(ValueError):
                handler.redirect_request(request, None, 302, '', {}, target)


if __name__ == '__main__':
    unittest.main()
