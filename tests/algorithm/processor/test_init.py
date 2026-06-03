import importlib


def test_processor_package_imports():
    module = importlib.import_module('lazymind.processor')

    assert module.__name__ == 'lazymind.processor'
    assert module.__path__
