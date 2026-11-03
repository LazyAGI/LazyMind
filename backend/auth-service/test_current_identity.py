import unittest
from types import SimpleNamespace
from unittest.mock import patch
from core import current_identity as identity


class CurrentIdentityTest(unittest.TestCase):
    def test_disabled_accounts_are_rejected_even_with_existing_credentials(self):
        for user in (None, SimpleNamespace(disabled=True)):
            with self.subTest(user=user), patch.object(identity, 'SessionLocal'), patch.object(
                identity.UserRepository, 'get_by_id', return_value=user
            ), self.assertRaises(Exception):
                identity.current_identity('owner')

    def test_permissions_come_from_live_user_role_and_groups(self):
        user = SimpleNamespace(id='owner', disabled=False, role=SimpleNamespace(name='user'), tenant_id='tenant')
        with patch.object(identity, 'SessionLocal'), patch.object(
            identity.UserRepository, 'get_by_id', return_value=user
        ) as load, patch.object(identity, 'get_effective_permission_codes', return_value={'document.read', 'qa.read'}):
            result = identity.current_identity('owner')
        self.assertEqual(result['permissions'], ['document.read', 'qa.read'])
        self.assertEqual(result['tenant_id'], 'tenant')
        self.assertTrue(load.call_args.kwargs['load_group_permission_groups'])
