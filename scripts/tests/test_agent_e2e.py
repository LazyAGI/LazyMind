"""Check the runner's evidence boundaries and artifact rejection behavior."""
import base64
import hashlib
import importlib.util
import io
import json
import os
import queue
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import zipfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1]))
SPEC = importlib.util.spec_from_file_location("agent_e2e", Path(__file__).parents[1] / "agent_e2e.py")
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


def connection_fixture(root):
    home = root / "lazymind"
    home.mkdir()
    token = "header." + base64.urlsafe_b64encode(json.dumps({"sub": "ci-account"}).encode()).decode().rstrip("=") + ".signature"
    runner.write_json(home / "credentials.json", {"server_url": "http://localhost:8090", "username": "ci-user",
                      "access_token": token, "refresh_token": "fixture-refresh", "local_runtime_id": "ci-runtime"})
    (home / "connector-host-id").write_text("ci-host")
    cli = root / "cli"
    cli.write_text("fixture connector binary")
    return SimpleNamespace(home=home, server="http://localhost:8090", cli=str(cli), timeout=20, dsh_profile="lazymind-ci")


class ConnectionFallbackTests(unittest.TestCase):
    def test_provider_quota_exhaustion_does_not_reconnect_any_agent(self):
        for agent in runner.AGENTS:
            with self.subTest(agent=agent), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = connection_fixture(root)
                directory = root / 'run'
                directory.mkdir()
                turn = {'exit_code': 1, 'host_error': False, 'seconds': .01, 'diagnostics': [],
                        'provider_diagnostics': [{'status': 429, 'route': '/responses',
                            'code': 'credit_balance_exhausted', 'type': 'insufficient_quota'}]}
                with (patch.object(runner, 'agent_command', return_value=([agent], {})),
                      patch.object(runner, 'invoke', return_value=turn) as invoke,
                      self.assertRaisesRegex(runner.ConnectionGuardError, 'provider_quota_exhausted')):
                    runner.probe_agent_connection(args, agent, directory, [agent],
                        'image-workflow', 'revision', runner.connection_snapshot(args))
                self.assertEqual(invoke.call_count, 1)
                attempts = json.loads((directory / 'connection-attempts.json').read_text())
                self.assertEqual(len(attempts), 1)
                self.assertEqual(attempts[0]['turn']['provider_diagnostics'][0]['status'], 429)

    def test_request_throttling_is_not_classified_as_exhausted_credit(self):
        self.assertIsNone(runner.provider_block_reason({'provider_diagnostics': [
            {'status': 429, 'code': 'rate_limit_exceeded', 'type': 'requests'}]}))
        self.assertEqual(runner.provider_block_reason({'diagnostics': ['429 You have no credits remaining.']}),
                         'provider_quota_exhausted')

    def test_managed_routes_keep_upstream_keys_out_of_all_three_agent_processes(self):
        for agent in runner.AGENTS:
            with self.subTest(agent=agent), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = connection_fixture(root)
                args.managed_models = True
                args.providers_config = root / 'private.json'
                args.private_profile_root = root / 'profiles'
                runner.private_config(args.providers_config, json.dumps({'version': 1, 'llm': {
                    'api_key': 'fixture-upstream-key', 'base_url': 'https://model.example/v1',
                    'model': 'fixture-model', 'api': 'openai-responses'}}))
                directory = root / 'run'
                directory.mkdir()
                runner.prepare_connection(args, directory, 'image-workflow', 'revision', 'probe')
                with patch.object(runner, 'dsh_connection_patch', return_value=[]):
                    command, env = runner.agent_command(agent, directory, args, ['node', str(root / 'cli/bin/codebuddy')])
                with patch.object(runner, 'invoke_process', return_value={}) as invoke:
                    runner.invoke(command, env, 'probe', directory, 10)
                child_command, child_env = invoke.call_args.args[:2]
                self.assertNotIn('fixture-upstream-key', json.dumps(child_command) + json.dumps(child_env))
                self.assertNotIn('LAZYMIND_CI_PROXY_CONFIG', child_env)
                self.assertTrue(child_env['CODEBUDDY_BASE_URL'].startswith('http://127.0.0.1:'))
                if agent == 'codex':
                    self.assertEqual(child_env['OPENAI_API_KEY'], child_env['CODEBUDDY_API_KEY'])
                    self.assertIn('model_providers.lazymind-ci.base_url=' + json.dumps(child_env['CODEBUDDY_BASE_URL']), child_command)
                    self.assertTrue(Path(child_env['CODEX_HOME']).is_relative_to(args.private_profile_root))
                if agent == 'deepseek-harness':
                    overlay = json.loads(Path(env['LAZYMIND_CI_DSH_PATCH']).read_text())
                    self.assertEqual(overlay[0]['config']['providers']['lazymind-ci']['baseURL'], child_env['CODEBUDDY_BASE_URL'])
                    self.assertIn(str(directory / 'dsh.patch.yml'), child_command)
                for path in args.private_profile_root.rglob('*'):
                    if path.is_file():
                        self.assertNotIn('fixture-upstream-key', path.read_text())

    def test_custom_workbuddy_provider_keys_stay_with_parent_and_profiles_use_proxy_references(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = connection_fixture(root)
            args.providers_config = root / 'private.json'
            args.private_profile_root = root / 'profiles'
            private = {'version': 1, 'llm': {'api_key': 'fixture-llm-key', 'base_url': 'https://model.example/v1',
                'model': 'fixture-model', 'api': 'openai-responses'},
                'image': {'api_key': 'fixture-image-key', 'base_url': 'https://image.example/v1',
                'model': 'fixture-image', 'api': 'openai-images'}}
            runner.private_config(args.providers_config, json.dumps(private))
            directory = root / 'run'
            directory.mkdir()
            runner.prepare_connection(args, directory, 'image-workflow', 'revision', 'probe')
            command, env = runner.agent_command('workbuddy', directory, args, ['node', str(root / 'cli/bin/codebuddy')])
            self.assertIn('DeferExecuteTool', command)
            with patch.object(runner, 'invoke_process', return_value={}) as invoke:
                runner.invoke(command, env, 'read-only probe', directory, 10)
            child = invoke.call_args.args[1]
            self.assertTrue(child['CODEBUDDY_BASE_URL'].startswith('http://127.0.0.1:'))
            self.assertNotIn('LAZYMIND_CI_PROXY_CONFIG', child)
            for row in (private['llm'], private['image']):
                self.assertNotIn(row['api_key'], child.values())
                for path in args.private_profile_root.rglob('*.json'):
                    self.assertNotIn(row['api_key'], path.read_text())
            generated = (args.private_profile_root / 'run/models.json').read_text()
            self.assertIn('${CODEBUDDY_API_KEY}', generated)
            self.assertIn('${CODEBUDDY_BASE_URL}', generated)

    def test_workbuddy_ci_authentication_reaches_child_without_entering_generated_configs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = connection_fixture(root)
            runner.prepare_connection(args, root, "image-workflow", "revision", "probe")
            with patch.dict(os.environ, {"CODEBUDDY_API_KEY": "fixture-api-key", "CODEBUDDY_AUTH_TOKEN": "fixture-auth-token",
                                         "CODEBUDDY_INTERNET_ENVIRONMENT": "internal", "UNRELATED_SERVICE_SECRET": "unrelated"}):
                _, env = runner.agent_command("workbuddy", root, args, ["node", str(root / "cli/bin/codebuddy")])
                for other in ("codex", "deepseek-harness", ""):
                    other_env = runner.clean_env(args.home, args.server, other)
                    self.assertNotIn("CODEBUDDY_API_KEY", other_env)
                    self.assertNotIn("CODEBUDDY_AUTH_TOKEN", other_env)
            self.assertEqual(env["CODEBUDDY_API_KEY"], "fixture-api-key")
            self.assertEqual(env["CODEBUDDY_AUTH_TOKEN"], "fixture-auth-token")
            self.assertEqual(env["CODEBUDDY_INTERNET_ENVIRONMENT"], "internal")
            self.assertNotIn("UNRELATED_SERVICE_SECRET", env)
            for name in ("workbuddy-mcp.json", "connection.json"):
                content = (root / name).read_text()
                self.assertNotIn("fixture-api-key", content)
                self.assertNotIn("fixture-auth-token", content)

    def test_stale_default_cli_is_rebuilt_once_and_explicit_cli_is_preserved(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = connection_fixture(root)
            args.auto_cli = True
            def rebuild(args):
                Path(args.cli).write_text("current binary")
                runner.write_json(root / "provenance.json", {"connector_source_hash": "current-source",
                                  "cli_sha256": hashlib.sha256(Path(args.cli).read_bytes()).hexdigest()})
            with (patch.object(runner, "connector_source_hash", return_value="current-source"),
                  patch.object(runner, "build_cli", side_effect=rebuild) as build):
                runner.ensure_current_cli(args)
                runner.ensure_current_cli(args)
                self.assertEqual(build.call_count, 1)
                args.auto_cli = False
                Path(args.cli).write_text("explicit custom binary")
                runner.ensure_current_cli(args)
                self.assertEqual(build.call_count, 1)
                self.assertEqual(Path(args.cli).read_text(), "explicit custom binary")

    def test_token_refresh_is_allowed_but_account_and_server_switches_stop_execution(self):
        with tempfile.TemporaryDirectory() as tmp:
            args = connection_fixture(Path(tmp))
            expected = runner.connection_snapshot(args)
            path = args.home / "credentials.json"
            value = json.loads(path.read_text())
            value["access_token"] = value["access_token"].replace("signature", "renewed-signature")
            runner.write_json(path, value)
            self.assertEqual(runner.assert_connection(args, expected), expected)
            value["username"] = "different-account"
            runner.write_json(path, value)
            with self.assertRaisesRegex(runner.ConnectionGuardError, "context_changed"):
                runner.assert_connection(args, expected)
            value["server_url"] = "http://localhost:8091"
            runner.write_json(path, value)
            with self.assertRaisesRegex(runner.ConnectionGuardError, "server_mismatch"):
                runner.connection_snapshot(args)

    def test_probe_requires_current_process_and_exact_workflow_revision(self):
        context = {"generation": "new", "workflow_id": "image-workflow", "revision_id": "revision-new"}
        turn = {"exit_code": 0, "host_error": False}
        events = [{"event": "connection_ready", "generation": "new"},
                  {"event": "tool_call", "tool": "workflow.list"},
                  {"event": "tool_result", "tool": "workflow.list", "error": False},
                  {"event": "tool_call", "tool": "workflow.get"},
                  {"event": "tool_result", "tool": "workflow.get", "error": False,
                   "workflow_id": "image-workflow", "revision_id": "revision-old"}]
        self.assertFalse(runner.connection_probe_passed(events, context, turn))
        events[-1]["revision_id"] = "revision-new"
        self.assertTrue(runner.connection_probe_passed(events, context, turn))
        events[0]["generation"] = "old-cached-process"
        self.assertFalse(runner.connection_probe_passed(events, context, turn))

    def test_all_agents_recover_once_with_new_private_configs_and_keep_global_configs(self):
        for agent in runner.AGENTS:
            with self.subTest(agent=agent), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = connection_fixture(root)
                directory = root / "test"
                directory.mkdir()
                profile = root / ".codex"
                profile.mkdir()
                original = profile / "config.toml"
                original.write_text('model="fixture"\n[mcp_servers.lazymind]\ncommand="/old/connector"\n')
                buddy = root / ".workbuddy/mcp.json"
                buddy.parent.mkdir()
                buddy.write_text('{"mcpServers":{"lazymind":{"command":"/old/workbuddy"}}}')
                dsh = root / ".dsh/profiles/lazymind-ci/cordis.patch.yml"
                dsh.parent.mkdir(parents=True)
                dsh.write_text(json.dumps([{"id": "old-mcp", "name": "@deepseek-ai/dsh-mcp-client",
                                           "config": {"serverName": "lazymind"}}]))
                saved = {p: p.read_bytes() for p in (original, buddy, dsh)}
                seen = []
                def invoke(command, env, prompt, target, timeout, seen=seen):
                    context = json.loads((target / "connection.json").read_text())
                    seen.append(context["generation"])
                    events = [{"event": "connection_ready", "generation": context["generation"]},
                              {"event": "tool_call", "tool": "workflow.list"},
                              {"event": "tool_result", "tool": "workflow.list", "error": False},
                              {"event": "tool_call", "tool": "workflow.get"},
                              {"event": "tool_result", "tool": "workflow.get", "error": False,
                               "workflow_id": "image-workflow", "revision_id": "old" if len(seen) == 1 else "new"}]
                    (target / "mcp.jsonl").write_text("\n".join(json.dumps(e) for e in events))
                    return {"exit_code": 0, "host_error": False, "diagnostics": [], "seconds": .01}
                command = ["node", str(root / "cli/bin/codebuddy")] if agent == "workbuddy" else [agent]
                with (patch.dict(os.environ, {"HOME": tmp, "CODEX_HOME": str(profile),
                                              "WORKBUDDY_CONFIG_DIR": str(buddy.parent), "DSH_HOME": str(root / ".dsh")}),
                      patch.object(runner.subprocess, "check_output", return_value=dsh.read_text()),
                      patch.object(runner, "invoke", side_effect=invoke)):
                    attempts = runner.probe_agent_connection(args, agent, directory, command,
                                   "image-workflow", "new", runner.connection_snapshot(args))
                self.assertEqual([a["status"] for a in attempts], ["failed", "passed"])
                self.assertEqual(len(set(seen)), 2)
                for path, content in saved.items():
                    self.assertEqual(path.read_bytes(), content)
                if agent == "deepseek-harness":
                    overlay = json.loads((Path(attempts[-1]["directory"]) / "dsh.patch.yml").read_text())
                    self.assertIn({"id": "old-mcp", "disabled": True}, overlay)

    def test_authentication_failure_does_not_loop_or_start_business(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = connection_fixture(root)
            directory = root / "test"
            directory.mkdir()
            with (patch.object(runner, "agent_command", return_value=(["fixture"], {})),
                  patch.object(runner, "invoke", return_value={"exit_code": 0, "host_error": True,
                             "diagnostics": ["Authentication required"], "seconds": .1}) as invoke):
                with self.assertRaisesRegex(runner.ConnectionGuardError, "host_authentication_required"):
                    runner.probe_agent_connection(args, "workbuddy", directory, ["fixture"],
                                   "image-workflow", "revision", runner.connection_snapshot(args))
                self.assertEqual(invoke.call_count, 1)

    def test_recovery_is_bounded_for_all_agents(self):
        for agent in runner.AGENTS:
            with self.subTest(agent=agent), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = connection_fixture(root)
                directory = root / "test"
                directory.mkdir()
                with (patch.object(runner, "agent_command", return_value=(["fixture"], {})),
                      patch.object(runner, "invoke", return_value={"exit_code": 0, "host_error": False,
                                 "diagnostics": [], "seconds": .1}) as invoke):
                    with self.assertRaisesRegex(runner.ConnectionGuardError, "recovery_exhausted"):
                        runner.probe_agent_connection(args, agent, directory, ["fixture"],
                                       "image-workflow", "revision", runner.connection_snapshot(args))
                    self.assertEqual(invoke.call_count, 2)

    def test_dsh_overlay_disables_old_clients_without_disabling_model_auth(self):
        rows = [{"id": "old-mcp", "name": "@deepseek-ai/dsh-mcp-client", "config": {"serverName": "lazymind"}},
                {"id": "old-workflow", "name": "file:/plugins/lazymind-workflow"},
                {"id": "credentials", "name": "@deepseek-ai/dsh-credentials"}]
        self.assertEqual(runner.dsh_disabled_connections(rows), [{"id": "old-mcp", "disabled": True},
                                                                 {"id": "old-workflow", "disabled": True}])
        self.assertNotIn("disabled", rows[0])

    def test_invalid_dsh_profile_diagnostic_does_not_expose_inline_credentials(self):
        with patch.object(runner.subprocess, "check_output", return_value='token: [fixture-private-value'):
            with self.assertRaisesRegex(runner.ConnectionGuardError, "dsh_configuration_inspection_failed") as raised:
                runner.dsh_connection_patch(["dsh"], {}, SimpleNamespace(dsh_profile="ci"))
            self.assertNotIn("fixture-private-value", str(raised.exception))

    def test_private_configuration_keeps_backup_and_drops_inherited_connection_overrides(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "mcp.json"
            path.write_text("old config")
            runner.private_config(path, "new config")
            self.assertEqual(next(root.glob("mcp.json.before-*")).read_text(), "old config")
            if os.name == 'posix':
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with patch.dict(os.environ, {"LAZYMIND_CONNECTOR_CONFIG": "/old/account.json", "LAZYMIND_MCP_URL": "http://localhost:1"}):
                for agent in runner.AGENTS:
                    env = runner.clean_env(root, "http://localhost:8090", agent)
                    self.assertNotIn("LAZYMIND_CONNECTOR_CONFIG", env)
                    self.assertNotIn("LAZYMIND_MCP_URL", env)

    def test_readonly_relay_blocks_workflow_start_before_it_reaches_upstream(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = connection_fixture(root)
            args.cli = str(root / 'fixture-connector.py')
            upstream_log = root / "upstream.jsonl"
            Path(args.cli).write_text('#!' + runner.sys.executable + '\nimport sys,json\n'
                f'log={str(upstream_log)!r}\n'
                'for line in sys.stdin:\n'
                ' r=json.loads(line)\n'
                ' with open(log,"a") as f:f.write(json.dumps(r)+"\\n")\n'
                ' if "id" in r: print(json.dumps({"jsonrpc":"2.0","id":r["id"],"result":{}}),flush=True)\n')
            Path(args.cli).chmod(0o700)
            runner.prepare_connection(args, root, "image-workflow", "revision", "probe")
            request = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
                "name": "workflow.start", "arguments": {"workflow_id": "image-workflow", "revision_id": "revision"}}}
            result = subprocess.run([runner.sys.executable, str(runner.SCRIPT), "observe", "--cli", args.cli,
                                    "--trace", str(root / "mcp.jsonl"), "--connection-context", str(root / "connection.json")],
                                    input=json.dumps(request) + "\n", capture_output=True, text=True, timeout=5, check=False)
            response = json.loads(result.stdout.splitlines()[0])
            self.assertTrue(response["result"]["isError"])
            self.assertFalse(upstream_log.exists())
            self.assertIn("read_only_connection_probe", (root / "mcp.jsonl").read_text())

    def test_live_relay_forwards_reads_and_rejects_account_switch_before_mutation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = connection_fixture(root)
            args.cli = str(root / 'fixture-connector.py')
            upstream_log = root / "upstream.jsonl"
            Path(args.cli).write_text('#!' + runner.sys.executable + '\nimport sys,json\n'
                f'log={str(upstream_log)!r}\n'
                'for line in sys.stdin:\n'
                ' r=json.loads(line)\n'
                ' with open(log,"a") as f:f.write(json.dumps(r)+"\\n")\n'
                ' if "id" in r: print(json.dumps({"jsonrpc":"2.0","id":r["id"],"result":'
                '{"structuredContent":{"workflow_id":"image-workflow","revision_id":"revision"}}}),flush=True)\n')
            Path(args.cli).chmod(0o700)
            runner.prepare_connection(args, root, "image-workflow", "revision", "business")
            process = subprocess.Popen([runner.sys.executable, str(runner.SCRIPT), "observe", "--cli", args.cli,
                        "--trace", str(root / "mcp.jsonl"), "--connection-context", str(root / "connection.json")],
                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            messages = queue.Queue()
            def read_messages():
                for line in process.stdout:
                    messages.put(line)
            reader = threading.Thread(target=read_messages, daemon=True)
            reader.start()
            try:
                request = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {
                    "name": "workflow.get", "arguments": {"workflow_id": "image-workflow", "revision_id": "revision"}}}
                process.stdin.write(json.dumps(request) + "\n")
                process.stdin.flush()
                self.assertEqual(json.loads(messages.get(timeout=10))["id"], 1)
                request["id"] = 2
                request["params"]["name"] = "workflow.start"
                process.stdin.write(json.dumps(request) + "\n")
                process.stdin.flush()
                self.assertEqual(json.loads(messages.get(timeout=10))["id"], 2)
                credential = json.loads((args.home / "credentials.json").read_text())
                credential["username"] = "new-account"
                runner.write_json(args.home / "credentials.json", credential)
                request["id"] = 3
                request["params"]["name"] = "workflow.artifact.publish"
                process.stdin.write(json.dumps(request) + '\n')
                process.stdin.close()
                self.assertTrue(json.loads(messages.get(timeout=10))["result"]["isError"])
                process.wait(timeout=5)
                upstream = [json.loads(line) for line in upstream_log.read_text().splitlines()]
                self.assertEqual([r["params"]["name"] for r in upstream], ["workflow.get", "workflow.start"])
                self.assertIn("connection_context_changed", (root / "mcp.jsonl").read_text())
            finally:
                if process.poll() is None:
                    process.kill()
                process.wait(timeout=5)
                reader.join(timeout=2)
                for stream in (process.stdin, process.stdout, process.stderr):
                    stream.close()


class RunnerBoundaryTests(unittest.TestCase):
    def test_native_export_rejects_external_directories_commands_and_symlinks_before_starting_processes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            callback = runner.ppt_export_callback(root, {})
            with patch.object(runner.host_platform, 'ProcessTree') as process:
                for body in ({'deck_dir': str(root.parent)}, {'deck_dir': str(root / 'deck'), 'command': 'unexpected'},
                             {'deck_dir': None}):
                    with self.assertRaises(ValueError):
                        callback(body)
                deck = root / 'deck'
                (deck / 'pages').mkdir(parents=True)
                for i in range(1, 4):
                    (deck / 'pages' / f'page_{i:03}.html').write_text('<html>fixture</html>')
                try:
                    (deck / 'linked.html').symlink_to(root / 'outside.html')
                except OSError:
                    # Windows without symlink privileges still checks the directory/command guards.
                    pass
                else:
                    with self.assertRaises(ValueError):
                        callback({'deck_dir': str(deck)})
                process.assert_not_called()

    def test_image_artifact_file_handoff_verifies_published_bytes(self):
        from PIL import Image
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            image = io.BytesIO()
            Image.new("RGB", (512, 512), "white").save(image, format="PNG")
            raw_image = image.getvalue()
            class Client:
                def call(self, name, **kwargs):
                    if name == "workflow.artifact.list":
                        return {"artifacts": [{"artifact_id": "published", "slot": "generated_image_output", "selected": True}]}
                    response = {"artifact": {"artifact_id": "published", "value": {
                        "filename": "generated.png", "content_base64": base64.b64encode(raw_image).decode(),
                        "sha256": hashlib.sha256(raw_image).hexdigest()}}}
                    path = Path(kwargs["result_directory"]) / "response.json"
                    path.write_text(json.dumps(response))
                    return {"artifact_file": {"result_path": str(path),
                                              "sha256": hashlib.sha256(path.read_bytes()).hexdigest()},
                            "artifact": {}}
            _, checks, slots = runner.collect_artifacts(Client(), None, "session", directory, "meme")
            self.assertEqual(slots, {"generated_image_output"})
            self.assertEqual(checks[0]["sha256"], hashlib.sha256(raw_image).hexdigest())

    def test_image_base_or_caption_output_is_not_a_final_plain_image(self):
        class Client:
            def call(self, name, **kwargs):
                if name != "workflow.artifact.list":
                    raise AssertionError("A non-final image must not be downloaded as a deliverable")
                return {"artifacts": [{"artifact_id": slot, "slot": slot}
                                      for slot in ("generated_base_image", "meme_static_output")]}
        with tempfile.TemporaryDirectory() as tmp:
            _, checks, slots = runner.collect_artifacts(Client(), None, "session", Path(tmp), "meme")
            self.assertEqual(checks, [])
            self.assertEqual(slots, set())

    def test_artifact_handoff_cannot_escape_the_output_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            class Client:
                def call(self, name, **kwargs):
                    if name == "workflow.artifact.list":
                        return {"artifacts": [{"artifact_id": "published", "slot": "generated_image_output"}]}
                    return {"artifact_file": {"result_path": str(directory / "outside.json"), "sha256": ""}}
            with self.assertRaises(ValueError):
                runner.collect_artifacts(Client(), None, "session", directory, "meme")

    def test_timeout_is_enforced_when_a_host_never_finishes_a_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            started = time.monotonic()
            result = runner.invoke([runner.sys.executable, "-c",
                                    "import sys,time;sys.stdout.write('partial');sys.stdout.flush();time.sleep(20)"],
                                   runner.os.environ.copy(), "", Path(tmp), timeout=.5)
            self.assertTrue(result["timed_out"])
            self.assertLess(time.monotonic() - started, 3)

    def test_large_prompt_and_startup_output_are_drained_in_both_directions(self):
        with tempfile.TemporaryDirectory() as tmp:
            prompt = '测试材料\n' * 150000
            child = '''import json,sys,threading,os
timer=threading.Timer(5, lambda: os._exit(7));timer.start()
print(json.dumps({'type':'startup','text':'x'*1048576}),flush=True)
sys.stderr.write('x'*1048576+'\\n');sys.stderr.flush()
data=sys.stdin.buffer.read()
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':str(len(data))}}),flush=True)
timer.cancel()
'''
            result = runner.invoke_process([sys.executable, '-c', child], os.environ.copy(), prompt, Path(tmp), 4)
            self.assertEqual(result['exit_code'], 0)
            self.assertFalse(result['timed_out'])
            self.assertFalse(result['host_error'])
            self.assertEqual(result['last_message'], str(len(prompt.encode('utf-8'))))

    def test_timeout_remains_active_while_a_host_does_not_read_a_large_prompt(self):
        with tempfile.TemporaryDirectory() as tmp:
            started = time.monotonic()
            result = runner.invoke_process([sys.executable, '-c', 'import time;time.sleep(20)'],
                         os.environ.copy(), 'x'*1048576, Path(tmp), .5)
            self.assertTrue(result['timed_out'])
            self.assertLess(time.monotonic() - started, 3)

    def test_report_preserves_failure_history_and_selects_latest_started_attempt(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            for directory, started, status in [("newer", 2, "passed"), ("older", 1, "failed")]:
                path = output / directory / "result.json"
                runner.write_json(path, {"agent": "codex", "case": "paper", "scope": "business_workflow",
                                  "directory": str(path.parent), "started_at": started, "status": status})
            with runner.contextlib.redirect_stdout(io.StringIO()):
                code = runner.report(SimpleNamespace(output=tmp))
            self.assertEqual(code, 0)
            self.assertEqual(json.loads((output / "summary.json").read_text())[0]["status"], "passed")
            self.assertEqual(len(json.loads((output / "history.json").read_text())), 2)

    def test_subprocess_environment_does_not_inherit_service_secrets(self):
        with tempfile.TemporaryDirectory() as tmp, patch.dict(runner.os.environ, {
            "UNRELATED_SERVICE_SECRET": "must-not-leave-parent",
            "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN": "must-not-leave-parent",
        }):
            env = runner.clean_env(Path(tmp), "http://localhost:8090", "codex")
            self.assertNotIn("UNRELATED_SERVICE_SECRET", env)
            self.assertNotIn("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", env)
            self.assertEqual(env["LAZYMIND_SERVER_URL"], "http://localhost:8090")

    def test_an_active_supervisor_cannot_be_acquired_twice(self):
        with (
            tempfile.TemporaryDirectory() as tmp,
            runner.run_lock(tmp),
            self.assertRaisesRegex(RuntimeError, "active supervisor"),
            runner.run_lock(tmp),
        ):
            self.fail("Second supervisor acquired an active run")

    def test_server_retry_allowance_does_not_trigger_repeated_host_turns(self):
        class Client:
            def call(self, name, **kwargs):
                if name != "workflow.state":
                    raise AssertionError("A failed step must not be retried automatically")
                return {"control": {"continuation": "continue", "automatic_retry_steps": ["write_outline"]},
                        "projection": {"completed": False}, "state_version": 5}
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / "mcp.jsonl").write_text(json.dumps({"tool": "workflow.start", "session_id": "run", "revision_id": "revision"}) + "\n")
            result = {"revision_id": "revision", "turns": []}
            runner.finish_run(SimpleNamespace(timeout=10, max_turns=3), result, directory, Client(), {})
            self.assertEqual(result["status"], "failed")
            self.assertEqual(result["reason"], "business_step_failed:write_outline")

    def test_completed_business_artifact_does_not_hide_a_host_timeout(self):
        class Client:
            def call(self, name, **kwargs):
                return {'control': {'continuation': 'completed'}, 'projection': {'completed': True}}
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / 'mcp.jsonl').write_text(json.dumps({'tool':'workflow.start', 'session_id':'run', 'revision_id':'revision'})+'\n')
            result = {'revision_id':'revision', 'case':'ppt', 'turns':[{'exit_code':-1,'timed_out':True,'seconds':30}]}
            with patch.object(runner, 'collect_artifacts', return_value=([], [{'slides':3}], {'native_pptx'})):
                runner.finish_run(SimpleNamespace(timeout=30, max_turns=3), result, directory, Client(), {})
            self.assertTrue(result['business_artifacts_verified'])
            self.assertEqual(result['status'], 'failed')
            self.assertEqual(result['reason'], 'host_execution_failed_after_business_completion')

    def test_resumed_host_error_with_zero_exit_stops_further_turns(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / 'mcp.jsonl').write_text(json.dumps({
                'tool': 'workflow.start', 'session_id': 'run', 'revision_id': 'revision'}) + '\n')
            result = {'agent': 'workbuddy', 'case': 'paper', 'revision_id': 'revision',
                      'turns': [{'exit_code': 0, 'seconds': .01, 'host_session_id': 'host'}]}
            class Client:
                def __init__(self):
                    self.calls = []
                def call(self, name, **kwargs):
                    self.calls.append(name)
                    return {'control': {'continuation': 'continue'},
                            'projection': {'completed': False}, 'state_version': 1}
            client = Client()
            turn = {'exit_code': 0, 'timed_out': False, 'host_error': True, 'seconds': .01,
                    'diagnostics': ['429 You have no credits remaining.']}
            with (patch.object(runner, 'agent_command', return_value=(['fixture'], {})),
                  patch.object(runner, 'invoke', return_value=turn) as invoke):
                runner.finish_run(SimpleNamespace(timeout=30, max_turns=6), result, directory, client, {'workbuddy': ['fixture']})
            self.assertEqual(invoke.call_count, 1)
            self.assertEqual(client.calls, ['workflow.state', 'workflow.session.stop'])
            self.assertEqual(result['status'], 'blocked')
            self.assertEqual(result['reason'], 'provider_quota_exhausted')

    def test_pending_review_is_not_a_completed_business_result(self):
        class Client:
            def call(self, name, **kwargs):
                return {"control": {"continuation": "awaiting_user", "reviews": [{"status": "pending"}]},
                        "projection": {"completed": True}, "state_version": 3}
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / "mcp.jsonl").write_text(json.dumps({"tool": "workflow.start", "session_id": "run", "revision_id": "revision"}) + "\n")
            result = {"revision_id": "revision", "turns": []}
            runner.finish_run(SimpleNamespace(timeout=10, max_turns=3, approve_test_reviews=False), result, directory, Client(), {})
            self.assertEqual(result["status"], "blocked")
            self.assertEqual(result["reason"], "test_user_review_required")

    def test_mcp_tool_error_is_not_success(self):
        with self.assertRaises(RuntimeError):
            runner.unpack({"result": {"isError": True, "content": [{"type": "text", "text": "private payload"}]}})
        self.assertEqual(runner.unpack({"result": {"content": [{"type": "text", "text": '{"workflows": []}'}]}}), {"workflows": []})

    def test_diagnostics_redact_credentials(self):
        result = runner.safe_diagnostic("Authorization: Bearer secret-value token=private-value password=hidden")
        for secret in ("secret-value", "private-value", "hidden"):
            self.assertNotIn(secret, result)

    def test_failure_summary_retains_final_error_without_reasoning_or_custom_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            env = os.environ.copy()
            env['OPENAI_API_KEY'] = 'custom-provider-credential-12345'
            env['LAZYMIND_CI_PROXY_SESSION'] = 'random-proxy-session-98765'
            command = [sys.executable, '-c', '''import json,os
print(json.dumps({'type':'item.completed','item':{'type':'reasoning','text':'private-reasoning'}}))
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'Export failed '+os.environ['OPENAI_API_KEY']+' '+os.environ['LAZYMIND_CI_PROXY_SESSION']}}))
print(json.dumps({'type':'assistant','message':{'content':[{'type':'thinking','thinking':'private-reasoning'},{'type':'text','text':'Export failed '+os.environ['OPENAI_API_KEY']+' '+os.environ['LAZYMIND_CI_PROXY_SESSION']}]}}))
''']
            turn = runner.invoke_process(command, env, '', directory, 5)
            turn['provider_diagnostics'] = [{'status': 429, 'route': '/responses',
                                             'code': 'credit_balance_exhausted', 'type': 'insufficient_quota'}]
            result = {'agent':'codex','case':'ppt','reason':'business_step_failed:generate_ppt',
                      'turns':[turn], 'private_profile':'must-not-be-published'}
            with runner.contextlib.redirect_stdout(io.StringIO()):
                runner.failure_diagnostics(result, directory)
            public = (directory / 'failure-diagnostics.json').read_text()
            self.assertIn('Export failed', public)
            self.assertIn('credit_balance_exhausted', public)
            for private in ('private-reasoning', env['OPENAI_API_KEY'], env['LAZYMIND_CI_PROXY_SESSION'], 'must-not-be-published'):
                self.assertNotIn(private, public)

    def test_trace_reader_tolerates_interrupted_last_record(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            (path / "mcp.jsonl").write_text(json.dumps({"event": "tool_call", "tool": "workflow.list"}) + '\n{"partial"')
            self.assertEqual(len(runner.trace_events(path)), 1)

    def test_pptx_rejects_a_zip_without_a_presentation(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "fake.pptx"
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("hello.txt", "not a slide deck")
            with self.assertRaises(KeyError):
                runner.validate_file(path, "ppt")

    def test_pptx_checks_slide_count_and_required_topic(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "deck.pptx"
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("ppt/presentation.xml", '<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst><p:sldId id="1"/></p:sldIdLst></p:presentation>')
                archive.writestr("ppt/slides/slide1.xml", "<slide><text>检索增强生成测试</text></slide>")
            with self.assertRaisesRegex(ValueError, "slide count"):
                runner.validate_file(path, "ppt", expected_pages=3)
            self.assertEqual(runner.validate_file(path, "ppt", expected_pages=1)["slides"], 1)

    def test_paper_rejects_empty_and_off_topic_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "paper.md"
            path.write_text("# 完成\n" + "无关正文" * 200)
            with self.assertRaises(ValueError):
                runner.validate_file(path, "paper")


if __name__ == "__main__":
    unittest.main()
