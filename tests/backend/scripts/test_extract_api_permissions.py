import importlib.util
from pathlib import Path


SCRIPT_PATH = (
    Path(__file__).resolve().parents[3]
    / 'backend'
    / 'scripts'
    / 'extract_api_permissions.py'
)


def _load_extractor():
    spec = importlib.util.spec_from_file_location(
        'extract_api_permissions', SCRIPT_PATH
    )
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_empty_go_permissions_are_kept_as_login_only_route(tmp_path):
    routes = tmp_path / 'core' / 'routes.go'
    routes.parent.mkdir()
    routes.write_text(
        'handleAPI(r, "GET", "/user/ui-preferences", '
        '[]string{}, handler)\n',
        encoding='utf-8',
    )

    assert _load_extractor().extract_from_go_file(routes) == [
        {
            'method': 'GET',
            'path': '/api/core/user/ui-preferences',
            'permissions': [],
        }
    ]
