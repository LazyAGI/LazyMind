import asyncio
from lazymind.chat.api.health_routes import health


def test_health_route_reports_process_health_without_external_calls():
    assert asyncio.run(health()) == {'status': 'ok'}
