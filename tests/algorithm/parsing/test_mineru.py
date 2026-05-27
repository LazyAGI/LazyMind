import importlib
import runpy
import sys
import types


def test_mineru_imports_server_class():
    mineru = importlib.import_module('lazymind.parsing.service.mineru')

    assert mineru.MineruServer is not None


def test_mineru_main_starts_configured_server(monkeypatch):
    created = []

    class FakeMineruServer:
        def __init__(self, **kwargs):
            self.kwargs = kwargs
            self.started = False
            self.waited = False
            created.append(self)

        def start(self):
            self.started = True

        def wait(self):
            self.waited = True

    mineru_module = types.ModuleType('lazyllm.tools.servers.mineru.mineru_server_module')
    mineru_module.MineruServer = FakeMineruServer
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.servers', types.ModuleType('lazyllm.tools.servers'))
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.servers.mineru', types.ModuleType('lazyllm.tools.servers.mineru'))
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.servers.mineru.mineru_server_module', mineru_module)
    monkeypatch.setenv('LAZYMIND_MINERU_SERVER_PORT', '19000')
    monkeypatch.setenv('LAZYMIND_MINERU_BACKEND', 'vlm')
    monkeypatch.setenv('LAZYMIND_MINERU_CACHE_DIR', '/tmp/mineru-cache')
    monkeypatch.setenv('LAZYMIND_MINERU_IMAGE_SAVE_DIR', '/tmp/mineru-images')

    runpy.run_module('lazymind.parsing.service.mineru', run_name='__main__')

    assert len(created) == 1
    assert created[0].kwargs == {
        'port': 19000,
        'default_backend': 'vlm',
        'cache_dir': '/tmp/mineru-cache',
        'image_save_dir': '/tmp/mineru-images',
    }
    assert created[0].started is True
    assert created[0].waited is True
