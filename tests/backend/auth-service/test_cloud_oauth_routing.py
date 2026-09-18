import unittest

from starlette.routing import Match

from api.cloud_oauth import router


class CloudOAuthRoutingTest(unittest.TestCase):
    def test_internal_connection_routes(self):
        cases = [
            ('chat-enabled', 'list_chat_enabled_connections', {}),
            ('target-cache-candidates', 'list_target_cache_connections', {}),
            ('conn-example', 'get_connection_internal', {'connection_id': 'conn-example'}),
        ]
        for suffix, endpoint, params in cases:
            with self.subTest(path=suffix):
                scope = {
                    'type': 'http', 'method': 'GET', 'root_path': '',
                    'path': f'{router.prefix}/connections/internal/{suffix}',
                    'headers': [], 'query_string': b'',
                }
                matches = [(route, route.matches(scope)) for route in router.routes]
                route, (_, child) = next(item for item in matches if item[1][0] == Match.FULL)
                self.assertEqual(route.endpoint.__name__, endpoint)
                self.assertEqual(child['path_params'], params)


if __name__ == '__main__':
    unittest.main()
