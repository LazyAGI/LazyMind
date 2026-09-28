import importlib.util
from pathlib import Path

from channel_gateway.app import app


def test_notification_permissions_and_openapi_cover_public_operations():
    root = Path(__file__).resolve().parents[3]
    spec = importlib.util.spec_from_file_location(
        'permission_extractor', root / 'backend/scripts/extract_api_permissions.py')
    extractor = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(extractor)
    permissions = extractor.extract_from_py_file(root / 'backend/channel-gateway/channel_gateway/app.py')
    by_key = {(item['method'], item['path']): item['permissions'] for item in permissions}
    schema = app.openapi()
    prefix = '/api/channel-gateway/v1'
    for method, path, permission in [
        ('GET', '/channel-accounts/{account_id}', 'qa.read'),
        ('GET', '/channel-accounts/{account_id}/notification-targets', 'qa.read'),
        ('GET', '/channel-accounts/{account_id}/notification-references', 'qa.read'),
        ('POST', '/task-notifications', 'qa.write'),
        ('GET', '/task-notifications', 'qa.read'),
        ('GET', '/task-notifications/{notification_id}', 'qa.read'),
        ('POST', '/task-notifications/{notification_id}:retry', 'qa.write'),
    ]:
        assert by_key[(method, prefix + path)] == [permission]
        assert method.lower() in schema['paths'][prefix + path]
    for item in extractor.extract_from_go_file(root / 'backend/core/routes.go'):
        if 'notification' in item['path']:
            assert item['permissions'] == ['qa.read' if item['method'] == 'GET' else 'qa.write']
    assert schema['components']['schemas']['WeComCredentials']['properties']['secret']['writeOnly'] is True
    assert schema['components']['schemas']['TaskNotificationCreate']['additionalProperties'] is False
