"""Capability lifecycle tests; the Core transport is replaced at the HTTP boundary."""
import importlib.util
from pathlib import Path
import sys
import threading
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import Mock, patch


class SettingsActivityTests(unittest.TestCase):
    def setUp(self):
        transport = ModuleType('lazymind.chat.engine.tools.infra.core_api_client')
        self.report = transport.post_core_api = Mock()
        path = Path(__file__).resolve().parents[2] / 'algorithm/lazymind/chat/engine/agent_runtime/settings_activity.py'
        spec = importlib.util.spec_from_file_location('settings_activity_under_test', path)
        self.module = importlib.util.module_from_spec(spec)
        with patch.dict(sys.modules, {transport.__name__: transport}):
            spec.loader.exec_module(self.module)
        self.activity = self.module.SettingsActivity('owner', 'conversation')
        self.addCleanup(self.activity.close)

    def tool(self, name='remote_search', source='mcp'):
        return SimpleNamespace(
            tool_name=name, tool_source=source, tool_origin='server', arguments={'secret': 'never-report-this'})

    def test_mcp_records_only_identity_and_ends_after_the_call(self):
        with self.activity.tool_scope(self.tool()):
            self.assertEqual(len(self.activity._entries), 1)
            self.assertEqual(self.report.call_args.args[0], '/internal/settings/activity')
            self.assertTrue(self.report.call_args.args[1]['active'])
            self.assertEqual(self.report.call_args.kwargs, {'user_id': 'owner'})
        self.assertFalse(self.report.call_args.args[1]['active'])
        self.assertEqual(self.activity._entries, {})
        first_run = self.report.call_args.args[1]['run_id']
        with self.activity.tool_scope(self.tool()):
            self.assertEqual(self.report.call_args.args[1]['run_id'], first_run)
        self.assertNotIn('secret', repr(self.report.call_args_list))

    def test_skill_stays_active_until_run_end(self):
        for name in ['get_skill', 'run_script', 'read_reference']:
            with self.activity.tool_scope(self.tool(name, 'skill')):
                pass
        self.assertEqual(self.report.call_count, 1)
        self.assertEqual(len(self.activity._entries), 1)
        self.activity.close()
        self.assertFalse(self.report.call_args.args[1]['active'])
        self.assertTrue(self.activity._stop.is_set())

    def test_cancel_does_not_hide_a_dispatched_call(self):
        with self.activity.tool_scope(self.tool()):
            self.activity.close()
            self.assertFalse(self.activity._stop.is_set())
            self.assertEqual(len(self.activity._entries), 1)
        self.assertTrue(self.activity._stop.is_set())
        self.assertFalse(self.report.call_args.args[1]['active'])

    def test_cancel_waits_for_a_running_skill_script(self):
        with self.activity.tool_scope(self.tool('run_script', 'skill')):
            self.activity.close()
            self.assertEqual(len(self.activity._entries), 1)
        self.assertTrue(self.activity._stop.is_set())

    def test_tool_exception_still_clears_activity(self):
        with self.assertRaisesRegex(ValueError, 'tool failed'):
            with self.activity.tool_scope(self.tool()):
                raise ValueError('tool failed')
        self.assertEqual(self.activity._entries, {})

    def test_unknown_builtin_does_not_report(self):
        with self.activity.tool_scope(self.tool('calculator', 'builtin')):
            pass
        self.report.assert_not_called()

    def test_failed_registration_never_dispatches_an_invisible_call(self):
        self.report.side_effect = RuntimeError('private transport details')
        with self.assertRaisesRegex(RuntimeError, '^Unable to register active capability use. Please retry.$'):
            with self.activity.tool_scope(self.tool()):
                self.fail('must not dispatch')
        self.assertEqual(self.activity._entries, {})

    def test_cleanup_failure_does_not_replace_tool_result(self):
        self.report.side_effect = [None, RuntimeError('offline')]
        with self.activity.tool_scope(self.tool()):
            pass
        self.assertEqual(self.activity._entries, {})

    def test_builtin_workflow_skill_is_independent_of_personal_skills(self):
        manager = Mock()
        manager._get_visible_skill_info.return_value = ({'path': '/skills/workflow-agent-kit'}, None)
        self.activity._skill_manager = manager
        self.activity._skill_root = 'remote://skills'
        with self.activity.tool_scope(self.tool('get_skill', 'skill')):
            pass
        self.report.assert_not_called()
        manager._get_visible_skill_info.return_value = ({'path': 'remote://skills/personal'}, None)
        with self.activity.tool_scope(self.tool('get_skill', 'skill')):
            pass
        self.assertEqual(self.report.call_count, 1)

    def test_concurrent_skill_calls_share_one_run_record(self):
        barrier = threading.Barrier(4)
        errors = []

        def run():
            try:
                with self.activity.tool_scope(self.tool('get_skill', 'skill')):
                    barrier.wait(timeout=3)
            except Exception as error:
                errors.append(error)
        threads = [threading.Thread(target=run) for _ in range(4)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(timeout=4)
        self.assertFalse(errors)
        self.assertEqual(self.report.call_count, 1)
        self.activity.close()
        self.assertEqual(self.report.call_count, 2)


if __name__ == '__main__':
    unittest.main()
