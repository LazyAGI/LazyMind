import tempfile
import unittest
from pathlib import Path

from extract_api_permissions import extract_from_go_file


class AgentThreadPermissionsTest(unittest.TestCase):
    def test_resume_wrapper_keeps_write_permission_in_gateway_catalog(self):
        with tempfile.TemporaryDirectory() as directory:
            route = Path(directory) / 'core' / 'routes.go'
            route.parent.mkdir()
            route.write_text(
                'handleAgentThreadAPI(r, "POST", "/agent/threads/{thread_id}/resume", '
                '[]string{"qa.write"}, agent.ResumeThread)',
                encoding='utf-8',
            )
            self.assertEqual(extract_from_go_file(route), [{
                'method': 'POST', 'path': '/api/core/agent/threads/{thread_id}/resume',
                'permissions': ['qa.write'],
            }])


if __name__ == '__main__':
    unittest.main()
