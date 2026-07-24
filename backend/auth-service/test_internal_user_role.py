import unittest
import uuid
from unittest.mock import patch

from api import user as user_api


class InternalUserRoleTest(unittest.TestCase):
    def test_internal_role_response_contains_current_role_and_status(self):
        user_id = uuid.uuid4()
        detail = {
            'user_id': str(user_id),
            'role_name': 'system-admin',
            'tenant_id': 'tenant-1',
            'status': 'active',
        }

        with patch.object(user_api.user_service, 'get_user', return_value=detail) as get_user:
            response = user_api.get_user_role_internal(str(user_id), None)

        get_user.assert_called_once_with(user_id)
        self.assertEqual(
            response,
            {
                'user_id': str(user_id),
                'role': 'system-admin',
                'tenant_id': 'tenant-1',
                'disabled': False,
            },
        )


if __name__ == '__main__':
    unittest.main()
