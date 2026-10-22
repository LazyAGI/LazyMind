import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from export_tool_trace import redact_opaque


class ToolTraceExport(unittest.TestCase):
    def test_encrypted_message_is_omitted(self):
        value = redact_opaque({"message": "gAAAAfixture", "task_name": "example"})
        self.assertEqual(value["message"]["encrypted_payload"], "[OMITTED]")
        self.assertNotIn("gAAAAfixture", json.dumps(value))
        self.assertEqual(value["task_name"], "example")

    def test_only_tool_events_from_matching_workspace(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source, output = root / "rollout.jsonl", root / "tools.jsonl"
            rows = [
                {"type": "session_meta", "payload": {"id": "test-only", "cwd": str(root)}},
                {"timestamp": "2026-01-01T00:00:00Z", "type": "response_item",
                 "payload": {"type": "reasoning", "text": "PRIVATE_TEST_SENTINEL"}},
                {"timestamp": "2026-01-01T00:00:01Z", "type": "response_item",
                 "payload": {"type": "function_call", "name": "spawn_agent", "call_id": "c1",
                             "arguments": json.dumps({"message": "gAAAAfixture"})}},
                {"timestamp": "2026-01-01T00:00:02Z", "type": "response_item",
                 "payload": {"type": "function_call_output", "call_id": "c1",
                             "output": '{"task_name":"test-agent"}'}},
            ]
            source.write_text("".join(json.dumps(r) + "\n" for r in rows))
            script = Path(__file__).with_name("export_tool_trace.py")
            command = [sys.executable, str(script), "--session-file", str(source),
                       "--expected-cwd", str(root), "--output", str(output)]
            subprocess.run(command, check=True, capture_output=True)
            data = output.read_text()
            self.assertNotIn("PRIVATE_TEST_SENTINEL", data)
            self.assertNotIn("gAAAAfixture", data)
            self.assertEqual(len(data.splitlines()), 2)
            command[command.index("--expected-cwd") + 1] = str(root / "other")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
