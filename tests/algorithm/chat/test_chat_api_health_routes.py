import asyncio
from lazymind.chat.api.health_routes import _document_server_check_url, health


def test_health_route_reports_process_health_without_external_calls():
    assert asyncio.run(health()) == {'status': 'ok'}


def test_document_server_check_url_normalizes_multi_endpoint_config():
    assert _document_server_check_url(
        'http://doc-primary:8080/, http://doc-backup:8080/path',
    ) == 'http://doc-primary:8080/docs'
