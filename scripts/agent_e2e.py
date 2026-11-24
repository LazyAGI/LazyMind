#!/usr/bin/env python3
# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow==12.0.0", "PyYAML==6.0.3"]
# ///
"""Run real external agents against LazyMind; no business execution is mocked.

Python 3.11+. Credentials stay in existing host stores. Reports intentionally
exclude raw model events, reasoning, tool arguments, and MCP response bodies.
"""
from __future__ import annotations

import argparse
import base64
import contextlib
import hashlib
import json
import os
import plistlib
import queue
import re
import shutil
import subprocess
import sys
import threading
import time
import tomllib
import urllib.parse
import urllib.request
import uuid
import xml.etree.ElementTree as ET
import zipfile
from pathlib import Path

import agent_e2e_platform as host_platform
import agent_e2e_providers as providers

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(__file__).resolve()
AGENTS = ("codex", "workbuddy", "deepseek-harness")
CASES = {
    "ppt": ("ppt-workflow", "使用 PPT 工作流，根据本目录 materials.md 制作3页中文演示文稿。主题为检索增强生成测试，受众为软件测试工程师，科研风格、白底黑字，禁用AI背景图，不联网、不使用模板。最终输出选择原生 PPTX（presentation_output_format=native_pptx），不选择 HTML 工作流分支。必须交付可下载的PPTX，包含目标、方法和结论，不得把HTML预览当成PPTX交付。"),
    "paper": ("paper-writing", "使用论文写作工作流，仅根据本目录 materials.md 写一篇约800字的中文短论文，主题为检索增强生成系统的功能测试方法。输入是研究材料，先生成论文大纲，经测试用户确认后生成正文并结束。无需选题、文献搜索、实验、数据分析或同行审稿。不得编造文献、实验或数据，明确本文为方法说明。必须保存大纲和正文。"),
    "meme": ("image-workflow", "使用 image-workflow 工作流生成一张最简单的白色小猫图片，纯蓝色背景，512×512。主体清晰，不要求特定动作，不添加字幕或其他文字。验收时，生图服务自动附带的 AI 生成类水印不计为文字。只生成一张静态图片，不要视频、GIF或图片合集。不联网搜索。必须真实调用生图能力，并发布可下载的最终图片文件；不能仅发布路径或文字说明后判成功。"),
}
MATERIALS = """# 检索增强生成系统的功能测试方法

本文只提供方法材料，不包含真实实验或已发表文献。
测试目标：确认系统从文档导入、检索到回答和引用的完整链路。
方法一：导入有唯一事实的文档，检查解析完成后能够检索到对应片段。
方法二：提出材料中没有答案的问题，检查系统说明缺少证据。
方法三：对比回答和来源，检查引用定位、权限隔离及文档更新后的结果。
结论：应分别记录执行状态、实际业务结果、产物及尚未验证的范围。
限制：以上是测试设计建议，不代表经过实验验证的效果提升。
"""


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + "." + uuid.uuid4().hex + ".tmp")
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")
    temp.replace(path)


@contextlib.contextmanager
def run_lock(directory):
    """Prevent two supervisors from sending turns into the same host session."""
    try:
        with host_platform.file_lock(Path(directory) / 'supervisor.lock', blocking=False):
            yield
    except BlockingIOError as error:
        raise RuntimeError("This test run already has an active supervisor") from error


def unpack(result):
    if result.get("error"):
        raise RuntimeError("MCP RPC error: " + str(result["error"].get("code")))
    value = result.get("result", result)
    if value.get("isError"):
        # Return a concise diagnostic, without serializing arbitrary tool payloads.
        raise RuntimeError("MCP tool returned an error; inspect the local host diagnostic")
    if isinstance(value.get("structuredContent"), dict):
        return value["structuredContent"]
    for item in value.get("content", []):
        if item.get("type") == "text":
            with contextlib.suppress(ValueError):
                return json.loads(item["text"])
    return value


def safe_diagnostic(text):
    text = re.sub(r'\bsk-[A-Za-z0-9_-]{8,}', '[REDACTED]', str(text))
    text = re.sub(r"(?i)(bearer\s+|(?:token|password|secret|api[_-]?key)[=:]\s*)[^\s,\"}]+", r"\1[REDACTED]", str(text))
    text = re.sub(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+", "[REDACTED]", text)
    return text[:500]


class ConnectionGuardError(RuntimeError):
    """A CI connection must be repaired before business execution can proceed."""


def local_origin(value):
    parsed = urllib.parse.urlsplit(value)
    if (parsed.scheme not in ("http", "https") or parsed.hostname not in ("localhost", "127.0.0.1", "::1")
            or parsed.username or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment):
        raise ConnectionGuardError("invalid_local_server_origin")
    return value.rstrip("/")


def connection_snapshot(args):
    """Local identity pin, not an authorization decision; MCP still authenticates normally."""
    home = args.home.resolve()
    credential_path = home / "credentials.json"
    if credential_path.stat().st_size > 1024 * 1024:
        raise ConnectionGuardError("invalid_credential_file")
    credential = json.loads(credential_path.read_text(encoding='utf-8'))
    server = local_origin(args.server)
    if local_origin(credential.get("server_url", "")) != server:
        raise ConnectionGuardError("credential_server_mismatch")
    token = credential.get("access_token", "")
    if not isinstance(token, str) or not token:
        raise ConnectionGuardError("lazymind_authentication_required")
    subject = ""
    parts = token.split(".")
    if len(parts) == 3 and len(parts[1]) < 16384:
        with contextlib.suppress(ValueError, UnicodeError):
            claims = json.loads(base64.urlsafe_b64decode(parts[1] + "=" * (-len(parts[1]) % 4)))
            subject = claims.get("sub", "") if isinstance(claims, dict) else ""
    identity = [server, credential.get("local_runtime_id", ""), credential.get("username", ""),
                credential.get("tenant_id", ""), subject]
    if not identity[2] and not subject:
        raise ConnectionGuardError("credential_identity_unavailable")
    host_id = (home / "connector-host-id").read_text(encoding='utf-8').strip()
    if not host_id:
        raise ConnectionGuardError("connector_host_id_missing")
    cli = Path(args.cli).resolve()
    snapshot = {"home": str(home), "server": server, "host_id": host_id, "cli": str(cli),
            "account_scope": hashlib.sha256(json.dumps(identity, sort_keys=True).encode()).hexdigest(),
            "cli_sha256": hashlib.sha256(cli.read_bytes()).hexdigest()}
    provider_path = getattr(args, 'providers_config', None)
    if provider_path and Path(provider_path).is_file():
        path = Path(provider_path).resolve()
        snapshot.update(providers_config=str(path), providers_sha256=hashlib.sha256(path.read_bytes()).hexdigest())
    return snapshot


def assert_connection(args, expected):
    current = connection_snapshot(args)
    if any(current[key] != expected.get(key) for key in current):
        raise ConnectionGuardError("connection_context_changed")
    return current


def private_config(path, text):
    """Replace only this test's private configuration, keeping the previous generation."""
    path = Path(path)
    if path.exists():
        backup = path.with_name(path.name + ".before-" + uuid.uuid4().hex[:8])
        shutil.copy2(path, backup)
        backup.chmod(0o600)
    temporary = path.with_name(path.name + ".tmp-" + uuid.uuid4().hex[:8])
    temporary.write_text(text, encoding='utf-8')
    temporary.chmod(0o600)
    temporary.replace(path)


def prepare_connection(args, directory, workflow_id, revision_id, mode, expected=None):
    snapshot = assert_connection(args, expected) if expected else connection_snapshot(args)
    context = {**snapshot, "generation": uuid.uuid4().hex, "workflow_id": workflow_id,
               "revision_id": revision_id, "mode": mode}
    private_config(directory / "connection.json", json.dumps(context, ensure_ascii=False, indent=2))
    return context


def validate_guarded_call(context, name, arguments):
    if context["mode"] == "probe" and name not in ("workflow.list", "workflow.get"):
        raise ConnectionGuardError("read_only_connection_probe")
    if name in ("workflow.get", "workflow.start") and (
            not isinstance(arguments, dict) or arguments.get("workflow_id") != context["workflow_id"]
            or arguments.get("revision_id") != context["revision_id"]):
        raise ConnectionGuardError("stale_workflow_configuration")


def bind_ppt_start_values(context, name, arguments):
    """Bind the fixed CI fixture's confirmed output choice in the actual MCP request."""
    if context["mode"] != "business" or context["workflow_id"] != "ppt-workflow" or name != "workflow.start":
        return arguments
    values = arguments.get("clarification_values")
    if values is None:
        values = {}
    if not isinstance(values, dict):
        raise ConnectionGuardError("invalid_ppt_start_answers")
    return {**arguments, "clarification_values": {**values, "presentation_output_format": "native_pptx"}}


def clean_env(home, server, agent=""):
    allowed = {"HOME", "USER", "LOGNAME", "SHELL", "PATH", "TMPDIR", "TEMP", "TMP", "LANG", "TERM",
               "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "SystemRoot", "COMSPEC", "PATHEXT",
               "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy",
               "SSL_CERT_FILE", "SSL_CERT_DIR", "CODEX_HOME", "DSH_HOME", "WORKBUDDY_CONFIG_DIR",
               'PLAYWRIGHT_BROWSERS_PATH', 'PYTHONUTF8', 'PYTHONIOENCODING', 'LAZYMIND_PPT_EXTERNAL_OFFLINE',
               "OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL",
               "DEEPSEEK_API_KEY", "ARK_API_KEY", "VOLCENGINE_API_KEY"}
    if agent == "workbuddy":
        allowed.update(("CODEBUDDY_API_KEY", "CODEBUDDY_AUTH_TOKEN", "CODEBUDDY_BASE_URL", "CODEBUDDY_INTERNET_ENVIRONMENT"))
    config = Path(os.environ.get("CODEX_HOME", Path.home() / ".codex")) / "config.toml"
    if agent == "codex" and config.is_file():
        model = tomllib.loads(config.read_text(encoding='utf-8'))
        key = model.get("model_providers", {}).get(model.get("model_provider"), {}).get("env_key")
        if isinstance(key, str):
            allowed.add(key)
    env = {k: v for k, v in os.environ.items() if k in allowed or k.startswith("LC_")}
    env.update(LAZYMIND_HOME=str(home), LAZYMIND_SERVER_URL=server,
               LAZYMIND_WORKFLOW_HOST_CONTROL="0", LAZYMIND_AGENT_PROVIDER=agent)
    host_id = Path(home) / "connector-host-id"
    if host_id.is_file():
        env["LAZYMIND_AGENT_HOST_ID"] = host_id.read_text(encoding='utf-8').strip()
    return env


class MCP:
    """Newline JSON stdio transport used by the repository's Go MCP server."""
    def __init__(self, cli, env, timeout=60):
        self.timeout, self.counter = timeout, 0
        self.messages = queue.Queue()
        self.process = subprocess.Popen([*host_platform.connector_command(cli), "mcp", "proxy"], env=env,
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=subprocess.DEVNULL, text=True, encoding='utf-8')
        def read():
            for line in self.process.stdout:
                try:
                    self.messages.put(json.loads(line))
                except ValueError:
                    continue
            self.messages.put(None)
        threading.Thread(target=read, daemon=True).start()
        try:
            self.request("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                         "clientInfo": {"name": "lazymind-agent-e2e", "version": "1"}})
            self.send({"jsonrpc": "2.0", "method": "notifications/initialized"})
        except (RuntimeError, TimeoutError, OSError):
            self.close()
            raise

    def send(self, value):
        self.process.stdin.write(json.dumps(value) + "\n")
        self.process.stdin.flush()

    def request(self, method, params):
        self.counter += 1
        self.send({"jsonrpc": "2.0", "id": self.counter, "method": method, "params": params})
        deadline = time.monotonic() + self.timeout
        while True:
            try:
                value = self.messages.get(timeout=max(.01, deadline - time.monotonic()))
            except queue.Empty as error:
                raise TimeoutError("LazyMind MCP request timed out") from error
            if value is None:
                raise RuntimeError("LazyMind MCP process exited")
            if value.get("id") == self.counter:
                return value
            if "method" in value and "id" in value:
                self.send({"jsonrpc": "2.0", "id": value["id"],
                           "error": {"code": -32601, "message": "Client method unsupported"}})

    def call(self, name, **args):
        return unpack(self.request("tools/call", {"name": name, "arguments": args}))

    def close(self):
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()


def observe(args):
    """Transparent MCP relay: only allowlisted protocol evidence goes to disk."""
    context = json.loads(Path(args.connection_context).read_text(encoding='utf-8')) if args.connection_context else None
    guard_args = argparse.Namespace(home=Path(context["home"]), server=context["server"], cli=context["cli"],
                                    providers_config=context.get('providers_config')) if context else None
    if context:
        assert_connection(guard_args, context)
    proxy_env = {k: v for k, v in os.environ.items() if not k.endswith(('_API_KEY', '_AUTH_TOKEN'))}
    process = subprocess.Popen([*host_platform.connector_command(args.cli), "mcp", "proxy"], env=proxy_env, stdin=subprocess.PIPE,
                               stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    calls = {}
    lock = threading.Lock()
    def record(value):
        with lock, open(args.trace, "a", encoding="utf-8") as stream:
            stream.write(json.dumps(value, ensure_ascii=False) + "\n")
    if context:
        record({"event": "connection_ready", "generation": context["generation"], "time": time.time()})
    def incoming():
        for line in sys.stdin.buffer:
            try:
                request = json.loads(line)
                if request.get("method") == "tools/call":
                    name = request.get("params", {}).get("name", "")
                    event = {"event": "tool_call", "tool": name, "time": time.time()}
                    arguments = request.get("params", {}).get("arguments", {})
                    if context:
                        try:
                            assert_connection(guard_args, context)
                            if json.loads(Path(args.connection_context).read_text(encoding='utf-8'))["generation"] != context["generation"]:
                                raise ConnectionGuardError("connection_generation_changed")
                            validate_guarded_call(context, name, arguments)
                            bound_arguments = bind_ppt_start_values(context, name, arguments)
                            if bound_arguments is not arguments:
                                arguments = bound_arguments
                                request["params"]["arguments"] = arguments
                                line = (json.dumps(request, ensure_ascii=False) + "\n").encode('utf-8')
                                event["presentation_output_format"] = arguments["clarification_values"]["presentation_output_format"]
                        except (ConnectionGuardError, OSError, ValueError) as error:
                            code = str(error) if isinstance(error, ConnectionGuardError) else "connection_context_unavailable"
                            record({"event": "connection_rejected", "tool": name, "code": code, "time": time.time()})
                            failure = {"jsonrpc": "2.0", "id": request.get("id"), "result": {
                                "isError": True, "content": [{"type": "text", "text": code}]}}
                            with lock:
                                sys.stdout.buffer.write((json.dumps(failure) + "\n").encode())
                                sys.stdout.buffer.flush()
                            break
                    calls[request.get("id")] = name
                    if isinstance(arguments, dict):
                        for key in ("step_id", "tool_name", "outcome", "error_code"):
                            if isinstance(arguments.get(key), str):
                                event[key] = arguments[key][:100]
                    record(event)
            except ValueError:
                pass
            try:
                process.stdin.write(line)
                process.stdin.flush()
            except BrokenPipeError:
                break
        with contextlib.suppress(OSError):
            process.stdin.close()
    threading.Thread(target=incoming, daemon=True).start()
    try:
        for line in process.stdout:
            try:
                response = json.loads(line)
                name = calls.pop(response.get("id"), None)
                if name:
                    result = response.get("result", {})
                    failed = bool(response.get("error") or result.get("isError"))
                    event = {"event": "tool_result", "tool": name, "error": failed, "time": time.time()}
                    if failed:
                        event["diagnostic"] = safe_diagnostic(response.get("error") or result.get("content", []))
                    if not failed:
                        value = unpack(response)
                        for key in ("session_id", "workflow_id", "revision_id", "status"):
                            if isinstance(value.get(key), str):
                                event[key] = value[key]
                        state = value.get("state") or value
                        event["continuation"] = state.get("control", {}).get("continuation")
                    record(event)
            except (ValueError, AttributeError, RuntimeError):
                pass
            with lock:
                sys.stdout.buffer.write(line)
                sys.stdout.buffer.flush()
    finally:
        if process.poll() is None:
            process.terminate()
        process.wait()
    return process.returncode


def version(command):
    try:
        result = subprocess.run(command + ["--version"], capture_output=True, text=True, timeout=20, check=False)
        return result.stdout.strip().splitlines()[0] if result.returncode == 0 else "unavailable"
    except (OSError, subprocess.TimeoutExpired, IndexError):
        return "unavailable"


def discover():
    supplied = os.environ.get('LAZYMIND_CI_AGENT_COMMANDS')
    if supplied:
        try:
            commands = json.loads(Path(supplied).read_text(encoding='utf-8'))
            if not isinstance(commands, dict) or set(commands) - set(AGENTS):
                raise ValueError
            if any(not isinstance(row, list) or not row or any(not isinstance(v, str) or not v for v in row)
                   for row in commands.values()):
                raise ValueError
        except (OSError, ValueError):
            raise RuntimeError('Invalid CI Agent command manifest') from None
    else:
        commands = {}
    buddy = Path(os.environ.get("WORKBUDDY_CONFIG_DIR", Path.home() / ".workbuddy"))
    default_app = Path(os.environ.get('LOCALAPPDATA', Path.home() / 'AppData/Local')) / 'Programs/WorkBuddy' if host_platform.IS_WINDOWS else Path('/Applications/WorkBuddy.app')
    app = Path(os.environ.get('WORKBUDDY_APP', default_app))
    cli_roots = [app / 'Contents/Resources/app.asar.unpacked/cli', app / 'resources/app.asar.unpacked/cli']
    nodes = sorted((buddy / 'binaries/node/versions').glob('*/bin/node'), reverse=True)
    nodes += sorted((buddy / 'binaries/node/versions').glob('*/node.exe'), reverse=True)
    node = str(nodes[0]) if nodes else shutil.which('node')
    workbuddy = []
    for cli in cli_roots:
        if node and (cli / 'bin/codebuddy').is_file() and (cli / 'dist/codebuddy-lite-wb.mjs').is_file():
            workbuddy = [node, str(cli / 'bin/codebuddy')]
            break
    defaults = {'codex': [shutil.which('codex')] if shutil.which('codex') else [],
                'workbuddy': workbuddy,
                'deepseek-harness': [shutil.which('dsh')] if shutil.which('dsh') else []}
    return {name: commands.get(name, default) for name, default in defaults.items()}


def setup_dsh(args):
    """Create a separate profile; preserve the installed web/desktop profiles."""
    import yaml
    dsh = shutil.which("dsh")
    if not dsh:
        raise RuntimeError("dsh executable is missing")
    profile = Path(os.environ.get("DSH_HOME", Path.home() / ".dsh")) / "profiles" / args.dsh_profile
    if not profile.exists():
        subprocess.run([dsh, "--profile", args.dsh_profile, "--from-default-profile", "headless", "--help"],
                       check=True, stdout=subprocess.DEVNULL, timeout=120)
    host_version = version([dsh])
    subprocess.run([dsh, "plugin", "--profile", args.dsh_profile, "add",
                    "@deepseek-ai/dsh-mcp-client@" + host_version], check=True, timeout=180)
    if args.dsh_use_stored_model:
        # Read the selected public model route and enable the normal credential
        # service. No credential files are copied and no token is injected.
        class Loader(yaml.SafeLoader):
            pass
        Loader.add_constructor("tag:yaml.org,2002:js", lambda loader, node: loader.construct_scalar(node))
        raw = subprocess.check_output([dsh, "--profile", "web", "--dump-config"], text=True, timeout=30)
        tree = yaml.load(raw, Loader=Loader)
        selected = next(x for x in tree if x.get("id") == "agent-default-model")
        rows = [{"id": name, "disabled": False} for name in ("credentials", "authorization", "deepseek-account")]
        rows.append({"id": "agent-default-model", "config": selected.get("config", {})})
        # Custom providers may be declared by the user's web profile.
        for item in tree:
            if item.get("id") == "llm-pi-ai":
                rows.append({"id": "llm-pi-ai", "config": item.get("config", {})})
        patch = profile / "cordis.patch.yml"
        if patch.exists():
            shutil.copy2(patch, patch.with_name("cordis.patch.before-e2e.yml"))
        patch.write_text(json.dumps(rows, ensure_ascii=False, indent=2), encoding='utf-8')
        patch.chmod(0o600)
        print("Test profile uses the existing web model selection and normal stored authentication")
    print("Prepared DSH test profile: " + args.dsh_profile)
    return 0


def provenance(cli):
    def git(*args):
        return subprocess.check_output(["git", "-C", str(ROOT), *args], text=True).strip()
    # skip-worktree deliberately hides compatibility changes from git status.
    flags = git("ls-files", "-v")
    local = []
    for line in flags.splitlines():
        if line.startswith("S "):
            path = ROOT / line[2:]
            if path.is_file():
                local.append({"path": line[2:], "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
    runtime = 'Existing local LazyMind service; source SHA does not prove the running service build'
    if os.environ.get('LAZYMIND_CI_RUNTIME_PROVENANCE'):
        runtime = json.loads(Path(os.environ['LAZYMIND_CI_RUNTIME_PROVENANCE']).read_text(encoding='utf-8'))
    return {"source_sha": git("rev-parse", "HEAD"), "source_status": git("status", "--short"),
            "protected_local_files": local, "cli_sha256": hashlib.sha256(cli.read_bytes()).hexdigest(),
            "execution_surface": "CLI/MCP; desktop panel control is not covered",
            'platform': sys.platform, 'workflow_sha': os.environ.get('GITHUB_WORKFLOW_SHA'),
            'workflow_ref': os.environ.get('GITHUB_WORKFLOW_REF'), 'runtime_provenance': runtime}


def installed_app_versions():
    versions = {}
    for name in ("WorkBuddy", "DeepSeek Harness", "LazyMind"):
        path = Path("/Applications") / (name + ".app") / "Contents/Info.plist"
        if path.is_file():
            versions[name] = plistlib.loads(path.read_bytes()).get("CFBundleShortVersionString")
    return versions


def doctor(args):
    commands = discover()
    result = {"agents": {name: {"installed": bool(cmd), "version": version(cmd) if cmd else "missing"}
                         for name, cmd in commands.items()}, "server": args.server,
              "desktop_versions": installed_app_versions(), "provenance": provenance(Path(args.cli)),
              "connection_context": connection_snapshot(args), "workflows": []}
    client = MCP(args.cli, clean_env(args.home, args.server))
    try:
        tools = client.request("tools/list", {})["result"]["tools"]
        result["mcp_tools"] = sorted(t["name"] for t in tools)
        listing = client.call("workflow.list")
        for item in listing.get("workflows", []):
            if item.get("workflow_id") in {v[0] for v in CASES.values()}:
                result["workflows"].append({k: item.get(k) for k in ("workflow_id", "revision_id", "revision_no")})
        result["mcp_connected"] = True
    finally:
        client.close()
    write_json(Path(args.output) / "doctor.json", result)
    print(json.dumps({k: v for k, v in result.items() if k not in ("provenance", "mcp_tools")}, ensure_ascii=False, indent=2))
    return 0 if len(result["workflows"]) == 3 else 1


def build_cli(args):
    target = Path(args.output).resolve() / 'bin' / host_platform.executable_name('lazymind')
    target.parent.mkdir(parents=True, exist_ok=True)
    source_hash = connector_source_hash()
    temporary = target.with_name("lazymind-build-" + uuid.uuid4().hex[:8])
    try:
        subprocess.run(["go", "build", "-o", str(temporary), "./cmd/lazymind"],
                       cwd=ROOT / "local/lazymind-cli", check=True, timeout=300)
        if source_hash != connector_source_hash():
            raise ConnectionGuardError("connector_source_changed_during_build")
        record = {**provenance(temporary), "connector_source_hash": source_hash}
        temporary.replace(target)
        write_json(target.parent / "provenance.json", record)
    finally:
        temporary.unlink(missing_ok=True)
    print("Built test CLI: " + str(target))
    return 0


def connector_source_hash():
    root = ROOT / "local/lazymind-cli"
    digest = hashlib.sha256()
    suffixes = {".go", ".mod", ".sum", ".mjs", ".cjs", ".js", ".json", ".tgz", ".md", ".yml", ".yaml"}
    for path in sorted(root.rglob("*")):
        if path.is_file() and path.suffix in suffixes and not any(x in path.parts for x in ("node_modules", ".git")):
            digest.update(path.relative_to(root).as_posix().encode() + b"\0" + hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def ensure_current_cli(args):
    if not args.auto_cli:
        if not Path(args.cli).is_file():
            raise ConnectionGuardError("explicit_connector_cli_missing")
        return
    target = Path(args.cli)
    target.parent.mkdir(parents=True, exist_ok=True)
    with host_platform.file_lock(target.parent / 'build.lock'):
        try:
            record = json.loads((target.parent / "provenance.json").read_text(encoding='utf-8'))
            current = (record.get("connector_source_hash") == connector_source_hash()
                       and record.get("cli_sha256") == hashlib.sha256(target.read_bytes()).hexdigest())
        except (OSError, ValueError):
            current = False
        if not current:
            build_cli(args)


def codex_config(directory, entry, managed=None):
    """Keep the user's authenticated model route, exclude unrelated MCP servers."""
    home = Path(os.environ.get("CODEX_HOME", Path.home() / ".codex"))
    original = home / "config.toml"
    config = tomllib.loads(original.read_text(encoding='utf-8')) if original.exists() and not managed else {}
    if managed:
        if managed['llm']['api'] != 'openai-responses':
            raise providers.ProviderConfigurationError('Codex CI requires an OpenAI Responses model route.')
        config = {'model': managed['llm']['model'], 'model_provider': 'lazymind-ci',
                  'model_reasoning_effort': 'low', 'model_providers': {'lazymind-ci': {
                      'name': 'LazyMind CI', 'base_url': 'http://127.0.0.1:1/v1',
                      'wire_api': 'responses', 'env_key': 'OPENAI_API_KEY'}}}
    rows = []
    for key in ("model", "model_provider", "model_reasoning_effort"):
        if key in config:
            rows.append(f"{key} = {json.dumps(config[key])}")
    provider = config.get("model_provider")
    if provider in config.get("model_providers", {}):
        rows.append(f"[model_providers.{provider}]")
        for key, value in config["model_providers"][provider].items():
            if key in ("experimental_bearer_token", "http_headers"):
                raise ValueError("Inline model-provider credentials are unsupported; use the provider's env_key")
            if isinstance(value, (str, bool, int)):
                rows.append(f"{key} = {json.dumps(value)}")
    rows += ["[mcp_servers.lazymind]", f"command = {json.dumps(entry['command'])}",
             f"args = {json.dumps(entry['args'])}", "startup_timeout_sec = 60", "tool_timeout_sec = 600",
             "[mcp_servers.lazymind.env]"]
    rows += [f"{key} = {json.dumps(value)}" for key, value in entry["env"].items()]
    destination = directory / "codex.config.toml"
    private_config(destination, "\n".join(rows) + "\n")
    return destination


def dsh_disabled_connections(tree):
    """Disable inherited MCP clients/old workflow hosts in this invocation only."""
    if not isinstance(tree, list):
        raise ConnectionGuardError("unsupported_dsh_configuration")
    rows = []
    for item in tree:
        if not isinstance(item, dict):
            raise ConnectionGuardError("unsupported_dsh_configuration")
        name = str(item.get("name", "")).lower()
        config = item.get("config") or {}
        lazy_server = isinstance(config, dict) and str(config.get("serverName", "")).lower() == "lazymind"
        if name.startswith("@deepseek-ai/dsh-mcp-client") or "lazymind" in name or lazy_server:
            if not isinstance(item.get("id"), str) or not item["id"]:
                raise ConnectionGuardError("unsupported_dsh_configuration")
            rows.append({"id": item["id"], "disabled": True})
    return rows


def dsh_connection_patch(command, env, args):
    import yaml
    class Loader(yaml.SafeLoader):
        pass
    Loader.add_constructor("tag:yaml.org,2002:js", lambda loader, node: loader.construct_scalar(node))
    try:
        raw = subprocess.check_output(command + ["--profile", args.dsh_profile, "--dump-config"],
                                      env=env, text=True, stderr=subprocess.DEVNULL, timeout=30)
        return dsh_disabled_connections(yaml.load(raw, Loader=Loader))
    except (subprocess.SubprocessError, yaml.YAMLError):
        # Parser diagnostics can echo inline credentials from a profile.
        raise ConnectionGuardError("dsh_configuration_inspection_failed") from None


def agent_command(name, directory, args, command, session=None):
    entry = {"command": sys.executable,
             "args": [str(SCRIPT), "observe", "--cli", args.cli, "--trace", str(directory / "mcp.jsonl"),
                      "--connection-context", str(directory / "connection.json")],
             "env": {"LAZYMIND_HOME": str(args.home), "LAZYMIND_SERVER_URL": args.server,
                     "LAZYMIND_WORKFLOW_HOST_CONTROL": "0", "LAZYMIND_AGENT_PROVIDER": name}}
    host_id = Path(args.home) / "connector-host-id"
    if host_id.is_file():
        entry["env"]["LAZYMIND_AGENT_HOST_ID"] = host_id.read_text(encoding='utf-8').strip()
    env = clean_env(args.home, args.server, name)
    managed = providers.load(args.providers_config, required=True) if getattr(args, 'managed_models', False) else None
    if managed:
        profile = args.private_profile_root / directory.name
        profile.mkdir(parents=True, exist_ok=True, mode=0o700)
        if profile.resolve().is_relative_to(ROOT):
            raise providers.ProviderConfigurationError('CI 私有运行目录必须位于仓库外。')
        routes = {'version': 1, **providers.public(managed)}
        for route in routes.values():
            if isinstance(route, dict):
                route['api_key_env'] = 'LAZYMIND_CI_PROXY_SESSION'
        private_config(profile / 'providers.json', json.dumps(routes, indent=2))
        env.update(LAZYMIND_CI_PROXY_CONFIG=str(args.providers_config), LAZYMIND_CI_MANAGED_AGENT=name)
        env['LAZYMIND_PPT_EXTERNAL_OFFLINE'] = '1'
    if name == "codex":
        config = codex_config(directory, entry, managed)
        if managed:
            env['CODEX_HOME'] = str(profile / 'codex')
            if host_platform.IS_WINDOWS and os.environ.get('LAZYMIND_CI_CODEX_HOME'):
                env['CODEX_HOME'] = os.environ['LAZYMIND_CI_CODEX_HOME']
                if Path(env['CODEX_HOME']).resolve().is_relative_to(ROOT):
                    raise providers.ProviderConfigurationError('Codex CI home must be outside the checkout.')
            Path(env['CODEX_HOME']).mkdir(mode=0o700, exist_ok=True)
        # Ignore unrelated user MCP entries, but retain the existing auth store.
        settings = tomllib.loads(config.read_text(encoding='utf-8'))
        overrides = []
        def flatten(value, path=()):
            for key, item in value.items():
                nested = (*path, key)
                if isinstance(item, dict):
                    flatten(item, nested)
                else:
                    dotted = ".".join(part if re.fullmatch(r"[\w-]+", part) else json.dumps(part) for part in nested)
                    overrides.extend(["--config", dotted + "=" + json.dumps(item)])
        flatten(settings)
        if managed:
            # The fixed CI fixture invokes the trusted image provider helper.
            # Give that job's workspace sandbox its required network access;
            # keep filesystem write limits and the approval reviewer enabled.
            overrides += ['--config', 'sandbox_workspace_write.network_access=true']
            if host_platform.IS_WINDOWS and os.environ.get('LAZYMIND_CI_CODEX_HOME'):
                overrides += ['--config', 'windows.sandbox="elevated"']
        cmd = command + ["exec"]
        if session:
            cmd += ["resume", session]
        cmd += ["--json", "--skip-git-repo-check", "--ignore-user-config", *overrides,
                "--config", 'sandbox_mode="workspace-write"',
                "--config", 'approval_policy="on-request"',
                "--config", 'approvals_reviewer="auto_review"', "-"]
        return cmd, env
    if name == "workbuddy":
        config = directory / "workbuddy-mcp.json"
        private_config(config, json.dumps({"mcpServers": {"lazymind": entry}}, ensure_ascii=False, indent=2))
        buddy = str(Path(os.environ.get("WORKBUDDY_CONFIG_DIR", Path.home() / ".workbuddy")))
        env.update(WORKBUDDY_CONFIG_DIR=buddy,
                   CODEBUDDY_CONFIG_DIR=buddy,
                   ACC_PRODUCT_CONFIG_PATH=str(Path(command[1]).parents[1] / "product.json"),
                   CODEBUDDY_HOST="workbuddy-desktop", CODEBUDDY_FORCE_LITE_WB_BUNDLE="1")
        cmd = command + ["-p", "--output-format", "stream-json", "--strict-mcp-config",
                         "--mcp-config", str(config)]
        supplied = providers.load(getattr(args, 'providers_config', None)) if getattr(args, 'providers_config', None) else None
        if supplied:
            profile = Path(args.private_profile_root) / directory.name
            if profile.resolve().is_relative_to(ROOT):
                raise providers.ProviderConfigurationError('CI 私有运行目录必须位于仓库外。')
            profile.mkdir(parents=True, exist_ok=True, mode=0o700)
            private_config(profile / 'models.json', json.dumps(providers.workbuddy_models(supplied, proxy=True), indent=2))
            private_config(profile / 'settings.json', json.dumps({'language': 'zh-CN', 'apiKeyHelper': ''}))
            route_config = {'version': 1, **providers.public(supplied)}
            for section in route_config.values():
                if isinstance(section, dict):
                    section['api_key_env'] = 'LAZYMIND_CI_PROXY_SESSION'
            private_config(profile / 'providers.json', json.dumps(route_config, indent=2))
            env.update(WORKBUDDY_CONFIG_DIR=str(profile), CODEBUDDY_CONFIG_DIR=str(profile),
                       LAZYMIND_CI_PROXY_CONFIG=str(args.providers_config), CODEBUDDY_CREDENTIALS_IN_MEMORY='1')
            env.pop('CODEBUDDY_AUTH_TOKEN', None)
            env.pop('CODEBUDDY_INTERNET_ENVIRONMENT', None)
            cmd += ['--model', supplied['llm']['model'], '--setting-sources', 'user',
                    '--allowedTools', 'mcp__lazymind__*', 'ToolSearch', 'DeferExecuteTool', 'Read', 'Write', 'Edit', 'Bash']
            if host_platform.IS_WINDOWS:
                cmd.append('PowerShell')
        if session:
            cmd += ["--resume", session]
        return cmd, env
    context = json.loads((directory / "connection.json").read_text(encoding='utf-8'))
    patch = dsh_connection_patch(command, env, args)
    if managed:
        # The loopback port changes on every turn. invoke() supplies this overlay
        # after the proxy starts; no upstream credential reaches the Agent.
        env['LAZYMIND_CI_DSH_PATCH'] = str(directory / 'dsh-model.patch.json')
    patch.append({"insert": [{"id": "mcp-lazymind-ci-" + context["generation"], "name": "@deepseek-ai/dsh-mcp-client",
                         "config": {"serverName": "lazymind", "transport": "stdio", **entry,
                                    "failOnStartupError": True}}]})
    overlay = directory / "dsh.patch.yml"
    # JSON is valid YAML; avoid a separate YAML dependency.
    private_config(overlay, json.dumps(patch, ensure_ascii=False, indent=2))
    cmd = command + ["--profile", args.dsh_profile, "--patch", str(overlay), "--json"]
    if managed:
        cmd[-1:-1] = ['--patch', env['LAZYMIND_CI_DSH_PATCH']]
    if session:
        cmd += ["--session-id", session]
    return cmd, env


def trace_events(directory):
    path = directory / "mcp.jsonl"
    values = []
    if path.exists():
        for line in path.read_text(encoding='utf-8').splitlines():
            with contextlib.suppress(ValueError):
                values.append(json.loads(line))
    return values


def provider_block_reason(turn):
    """Quota exhaustion cannot be repaired by reconnecting an Agent."""
    for item in turn.get('provider_diagnostics', []):
        if (item.get('code') in ('credit_balance_exhausted', 'insufficient_quota', 'billing_hard_limit_reached')
                or item.get('type') == 'insufficient_quota'):
            return 'provider_quota_exhausted'
    if any(re.search(r'(?i)no credits remaining|credit_balance_exhausted|insufficient_quota', str(value))
           for value in turn.get('diagnostics', [])):
        return 'provider_quota_exhausted'
    return None


def connection_probe_passed(events, context, turn):
    calls = {e.get("tool") for e in events if e.get("event") == "tool_call"}
    succeeded = {e.get("tool") for e in events if e.get("event") == "tool_result" and not e.get("error")}
    current_process = any(e.get("event") == "connection_ready" and e.get("generation") == context["generation"] for e in events)
    current_revision = any(e.get("event") == "tool_result" and e.get("tool") == "workflow.get"
                           and not e.get("error") and e.get("workflow_id") == context["workflow_id"]
                           and e.get("revision_id") == context["revision_id"] for e in events)
    return (current_process and current_revision and {"workflow.list", "workflow.get"} <= succeeded
            and calls <= {"workflow.list", "workflow.get"} and not turn["exit_code"]
            and not turn["host_error"] and not turn.get("timed_out")
            and not any(e.get("event") == "connection_rejected" for e in events))


def probe_agent_connection(args, agent, directory, command, workflow_id, revision_id, expected):
    """One bounded recovery before business starts; never retry a business run."""
    attempts = []
    started = time.monotonic()
    for attempt in range(2):
        assert_connection(args, expected)
        target = directory / "connection-attempts" / (str(attempt + 1) + "-" + uuid.uuid4().hex[:8])
        target.mkdir(parents=True, mode=0o700)
        context = prepare_connection(args, target, workflow_id, revision_id, "probe", expected)
        prompt = ("这是只读连接验证。只使用本次注入的 lazymind MCP，依次实际调用 workflow.list 和 workflow.get；"
                  f"workflow.get 必须显式传入 workflow_id={workflow_id}、revision_id={revision_id}。"
                  "不要启动工作流，不生成文件，不修改任何配置，不读取个人文件或环境变量，不使用其他 MCP。"
                  "失败时报告实际错误并结束。")
        cmd, env = agent_command(agent, target, args, command)
        budget = min(120, int(args.timeout - (time.monotonic() - started)))
        if budget <= 0:
            raise ConnectionGuardError("connection_probe_budget_exhausted")
        turn = invoke(cmd, env, prompt, target, budget)
        events = trace_events(target)
        passed = connection_probe_passed(events, context, turn)
        attempts.append({"attempt": attempt + 1, "generation": context["generation"],
                         "directory": str(target), "status": "passed" if passed else "failed", "turn": turn})
        write_json(directory / "connection-attempts.json", attempts)
        assert_connection(args, expected)
        if passed:
            return attempts
        if reason := provider_block_reason(turn):
            raise ConnectionGuardError(reason)
        if any("Authentication required" in d or "not logged in" in d for d in turn["diagnostics"]):
            raise ConnectionGuardError("host_authentication_required")
    raise ConnectionGuardError("connection_recovery_exhausted")


class LocalAPI:
    """Normal authenticated user API; never forge internal identity headers."""
    def __init__(self, args):
        self.home, self.server = args.home, args.server.rstrip("/")

    def request(self, path, payload=None):
        credential = json.loads((self.home / "credentials.json").read_text(encoding='utf-8'))
        if credential.get("server_url", "").rstrip("/") != self.server:
            raise RuntimeError("Test server differs from the existing credential scope")
        request = urllib.request.Request(self.server + "/api/core" + path,
                    data=json.dumps(payload).encode() if payload is not None else None,
                    headers={"Authorization": "Bearer " + credential["access_token"],
                             "Content-Type": "application/json", "Workflow-Contract-Version": "workflow.v1"})
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                raise RuntimeError("Refusing an authenticated API redirect")
        with urllib.request.build_opener(NoRedirect).open(request, timeout=30) as response:
            value = json.load(response)
        if value.get("ok") is False:
            raise RuntimeError("User control API rejected the command")
        return value.get("result", value.get("data", value))

    def download(self, url):
        parsed = urllib.parse.urlsplit(urllib.parse.urljoin(self.server + "/", url))
        origin = urllib.parse.urlsplit(self.server)
        if (parsed.scheme, parsed.netloc) != (origin.scheme, origin.netloc):
            raise RuntimeError("Artifact must be served by the tested LazyMind instance")
        credential = json.loads((self.home / "credentials.json").read_text(encoding='utf-8'))
        request = urllib.request.Request(parsed.geturl(), headers={"Authorization": "Bearer " + credential["access_token"]})
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                raise RuntimeError("Refusing an authenticated artifact redirect")
        with urllib.request.build_opener(NoRedirect).open(request, timeout=60) as response:
            data = response.read(64 * 1024 * 1024 + 1)
        if len(data) > 64 * 1024 * 1024:
            raise RuntimeError("Artifact exceeds the test download limit")
        return data


def validate_file(path, kind, expected_pages=3):
    path = Path(path)
    evidence = {"file": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                "size_bytes": path.stat().st_size}
    if kind == "ppt":
        with zipfile.ZipFile(path) as archive:
            if archive.testzip() is not None:
                raise ValueError("Corrupt PPTX archive")
            presentation = ET.fromstring(archive.read("ppt/presentation.xml"))
            ids = presentation.findall(".//{http://schemas.openxmlformats.org/presentationml/2006/main}sldId")
            if len(ids) != expected_pages:
                raise ValueError("PPTX slide count differs from the request")
            slides = [n for n in archive.namelist() if re.fullmatch(r"ppt/slides/slide\d+\.xml", n)]
            if len(slides) != expected_pages:
                raise ValueError("PPTX slide relationships do not match its parts")
            text = "".join("".join(ET.fromstring(archive.read(n)).itertext()) for n in slides)
            if "检索" not in text or "测试" not in text:
                raise ValueError("PPTX does not contain the requested topic")
            evidence["slides"] = len(ids)
    elif kind == "meme":
        from PIL import Image
        with Image.open(path) as image:
            image.load()
            if image.width != 512 or image.height != 512:
                raise ValueError("Image dimensions differ from the requested 512x512")
            if getattr(image, "n_frames", 1) != 1:
                raise ValueError("Static image case returned animation")
            evidence.update(width=image.width, height=image.height, format=image.format)
    else:
        text = path.read_text(encoding="utf-8")
        if len(text) < 500 or "检索" not in text or "测试" not in text:
            raise ValueError("Paper is empty, too short, or off topic")
        if not re.search(r"(?m)^#{1,3}\s+", text):
            raise ValueError("Paper has no section headings")
        evidence["characters"] = len(text)
    evidence["visual_or_scientific_quality"] = "not_independently_verified"
    return evidence


def collect_artifacts(client, args, session, directory, case):
    metadata = client.call("workflow.artifact.list", session_id=session, metadata_only=True).get("artifacts", [])
    public = [{k: item.get(k) for k in ("artifact_id", "slot", "slot_id", "revision", "validity", "selected")} for item in metadata]
    destination = directory / "artifacts"
    destination.mkdir(exist_ok=True)
    checks, slots = [], set()
    for item in metadata:
        slot = item.get("slot") or item.get("slot_id", "")
        if item.get("deleted") or item.get("selected") is False or item.get("validity") in ("stale", "invalid"):
            continue
        relevant = ((case == "ppt" and slot == "native_pptx") or
                    (case == "paper" and slot in ("write_outline_document", "write_document_document")) or
                    (case == "meme" and slot in ("generated_image_output",)))
        if not relevant:
            continue
        response = client.call("workflow.artifact.get", artifact_id=item["artifact_id"],
                               result_directory=str(destination.resolve()))
        # Image responses normally render as MCP image blocks and omit value
        # from structuredContent. File handoff preserves the published bytes.
        receipt = response.get("artifact_file")
        if receipt:
            handoff = Path(receipt["result_path"]).resolve()
            handoff.relative_to(destination.resolve())
            if handoff.stat().st_size > 64 * 1024 * 1024:
                raise ValueError("Artifact response exceeds the test transfer limit")
            raw_response = handoff.read_bytes()
            if hashlib.sha256(raw_response).hexdigest() != receipt["sha256"]:
                raise ValueError("Artifact handoff hash does not match its receipt")
            response = json.loads(raw_response)
        value = response.get("artifact", {})
        if value.get("artifact_id") != item["artifact_id"]:
            raise ValueError("Artifact handoff identity does not match its request")
        write_json(destination / (item["artifact_id"] + ".json"), value)
        # Artifact values can be text or a file descriptor. Do not trust a model-supplied path.
        content = value.get("value")
        if isinstance(content, dict) and content.get("content_base64"):
            raw = base64.b64decode(content["content_base64"], validate=True)
            actual_hash = hashlib.sha256(raw).hexdigest()
            expected_hash = str(content.get("sha256", "")).removeprefix("sha256:")
            if expected_hash and expected_hash != actual_hash:
                raise ValueError("Published artifact hash does not match its downloaded bytes")
            filename = Path(content.get("filename") or content.get("name") or slot).name
            path = destination / (item["artifact_id"] + "-" + filename)
            path.write_bytes(raw)
            if case != "paper" or slot == "write_document_document":
                checks.append(validate_file(path, case))
            elif len(raw.decode("utf-8")) < 50:
                raise ValueError("Published paper outline is empty")
            slots.add(slot)
            continue
        if case == "paper":
            text = content if isinstance(content, str) else (content or {}).get("text", "")
            if text and not text.startswith(("http:", "https:", "/api/")):
                path = destination / (slot + ".md")
                path.write_text(text, encoding='utf-8')
                if slot == "write_document_document":
                    checks.append(validate_file(path, case))
                slots.add(slot)
        descriptors = content if isinstance(content, list) else [content]
        for descriptor in descriptors:
            url = descriptor if isinstance(descriptor, str) else next((descriptor.get(k) for k in
                  ("download_url", "url", "file_url", "path") if descriptor.get(k)), None) if isinstance(descriptor, dict) else None
            if not url or not url.startswith(("http://", "https://", "/api/", "/uploads/")):
                continue
            suffix = ".pptx" if case == "ppt" else ".md" if case == "paper" else ".png"
            path = destination / (item["artifact_id"] + suffix)
            path.write_bytes(LocalAPI(args).download(url))
            if case != "paper" or slot == "write_document_document":
                checks.append(validate_file(path, case))
            slots.add(slot)
    return public, checks, slots


def finish_run(args, result, directory, client, commands):
    events = trace_events(directory)
    starts = [e for e in events if e.get("tool") == "workflow.start" and e.get("session_id")]
    sessions = {e["session_id"] for e in starts}
    if len(sessions) != 1:
        result.update(status="failed", reason="expected_one_real_workflow_session")
        return
    if any(e.get("revision_id") != result["revision_id"] for e in starts):
        result.update(status="failed", reason="workflow_revision_mismatch")
        return
    session = next(iter(sessions))
    result["session_id"] = session
    deadline = time.monotonic() + max(0, args.timeout - result.get("connection_probe_seconds", 0)
                                     - sum(t.get("seconds", 0) for t in result["turns"]))
    result.setdefault("review_receipts", [])
    previous_version, idle_turns = None, 0
    for _ in range(args.max_turns + 1):
        state = client.call("workflow.state", session_id=session)
        control = state.get("control", {})
        result["continuation"] = control.get("continuation")
        result["workflow_completed"] = state.get("projection", {}).get("completed", False)
        write_json(directory / "state.json", state)
        if control.get("automatic_retry_steps"):
            result.update(status="failed", reason="business_step_failed:" + ",".join(control["automatic_retry_steps"]))
            return
        if result["continuation"] == "completed" and result["workflow_completed"]:
            inventory, checks, slots = collect_artifacts(client, args, session, directory, result["case"])
            result.update(artifacts=inventory, artifact_checks=checks)
            expected = {"write_outline_document", "write_document_document"} if result["case"] == "paper" else {"native_pptx"} if result["case"] == "ppt" else {"generated_image_output"}
            if checks and expected <= slots:
                result['business_artifacts_verified'] = True
                abnormal_exit = any(t.get('exit_code') or t.get('timed_out') or t.get('host_error') for t in result['turns'])
                result.update(status='failed' if abnormal_exit else 'passed',
                              reason='host_execution_failed_after_business_completion' if abnormal_exit else None)
            else:
                result.update(status="failed", reason="required_published_artifact_not_verified")
            return
        if result["continuation"] in ("stopped", "failed", "binding_required"):
            result.update(status="blocked" if result["continuation"] == "binding_required" else "failed",
                          reason="workflow_" + str(result["continuation"]))
            return
        if state.get("state_version") == previous_version:
            idle_turns += 1
        else:
            idle_turns = 0
        previous_version = state.get("state_version")
        if idle_turns >= 2:
            result.update(status="blocked", reason="agent_did_not_advance_workflow")
            return
        if result["continuation"] == "awaiting_user":
            if control.get("input_request"):
                result.update(status="blocked", reason="unexpected_user_input_required")
                return
            pending = [r for r in control.get("reviews", []) if r.get("status") == "pending"]
            if not args.approve_test_reviews or not pending:
                result.update(status="blocked", reason="test_user_review_required")
                return
            api = LocalAPI(args)
            for review in pending:
                # Only the run ID observed in this private test's workflow.start
                # receipt is eligible; version and manifest must match exactly.
                receipt = api.request("/workflow-sessions/" + urllib.parse.quote(session) + "/control", {
                    "command_id": "e2e-review-" + review["id"] + "-" + str(review["version"]),
                    "kind": "confirm_and_continue", "expected_state_version": state["state_version"],
                    "review_id": review["id"], "review_version": review["version"], "manifest_hash": review["manifest_hash"]})
                result["review_receipts"].append({"review_id": review["id"], "version": review["version"], "receipt": receipt.get("receipt")})
                state = client.call("workflow.state", session_id=session)
        if time.monotonic() >= deadline or len(result["turns"]) >= args.max_turns:
            break
        host_session = next((t["host_session_id"] for t in reversed(result["turns"]) if t.get("host_session_id")), None)
        if not host_session:
            result.update(status="blocked", reason="host_session_id_unavailable_for_resume")
            return
        if result.get("connection_context"):
            assert_connection(args, result["connection_context"])
        command, env = agent_command(result["agent"], directory, args, commands[result["agent"]], host_session)
        prompt = (f"继续本次测试的原运行session_id={session}。先读取workflow.state，严格按当前admission继续。"
                  "如有审核，外部测试用户已通过正常接口提交；不要自行审批。不得新建替代运行，"
                  "不得自动重试终态失败步骤。前一轮CLI已经退出，如果仍有本宿主的active_execution_ids，"
                  "这是上轮未完成的步骤，应使用workflow.step.resume恢复其契约和有效handle再执行，而不是等待另一个Agent。"
                  "完成原始请求后发布实际产物并结束。")
        turn = invoke(command, env, prompt, directory, max(1, min(args.timeout, int(deadline - time.monotonic()))))
        result["turns"].append(turn)
        write_json(directory / "result.json", result)
        if turn["timed_out"] or turn["exit_code"] or turn.get('host_error'):
            break
    last_turn = result["turns"][-1] if result["turns"] else {}
    provider_reason = provider_block_reason(last_turn)
    reason = provider_reason or ("host_execution_error" if (last_turn.get("exit_code") or last_turn.get('host_error'))
                                and not last_turn.get("timed_out") else "execution_budget_exhausted")
    result.update(status="blocked" if provider_reason else "failed", reason=reason)
    client.call("workflow.session.stop", session_id=session, command_id="e2e-budget-stop-" + session)


def report(args):
    """Keep every attempt; a failed business run cannot become a passing probe."""
    output = Path(args.output)
    history = []
    latest = {}
    attempts = []
    for path in output.glob("*/result.json"):
        value = json.loads(path.read_text(encoding='utf-8'))
        events = trace_events(path.parent)
        started = value.get("started_at") or next((e["time"] for e in events if e.get("time")), path.stat().st_mtime)
        attempts.append((started, value))
    for _, value in sorted(attempts, key=lambda attempt: attempt[0]):
        scope = value.get("scope", "business_workflow")
        key = (value["agent"], value["case"], scope)
        latest[key] = value
        history.append({k: value.get(k) for k in ("agent", "case", "scope", "status", "reason", "directory")})
        for previous in value.get("previous_results", []):
            history.append({"agent": value["agent"], "case": value["case"], "scope": scope,
                            "directory": value["directory"], "prior_to_resume": True, **previous})
    lines = ["# LazyMind Agent 集成测试", "", "连接探测与真实业务运行分开统计；保留全部失败和重跑记录。", "",
             "| Agent | 用例 | 范围 | 结果 | 原因 |", "|---|---|---|---|---|"]
    suite = ET.Element("testsuite", name="lazymind-agent-e2e")
    failures = skipped = 0
    for (agent, case, scope), value in sorted(latest.items()):
        lines.append(f"| {agent} | {case} | {scope} | {value['status']} | {str(value.get('reason') or '').replace('|', '/')} |")
        test = ET.SubElement(suite, "testcase", classname=agent + "." + scope, name=case)
        if value["status"] == "failed":
            failures += 1
            ET.SubElement(test, "failure", message=value.get("reason") or "Test failed")
        elif value["status"] != "passed":
            skipped += 1
            ET.SubElement(test, "skipped", message=value.get("reason") or value["status"])
        evidence = {k: value.get(k) for k in ('directory', 'session_id', 'revision_id', 'artifact_checks', 'business_artifacts_verified')}
        evidence['host_execution'] = [{k: turn.get(k) for k in ('exit_code', 'timed_out', 'host_error', 'seconds')}
                                      for turn in value.get('turns', [])]
        ET.SubElement(test, "system-out").text = json.dumps(evidence, ensure_ascii=False)
    suite.set("tests", str(len(latest)))
    suite.set("failures", str(failures))
    suite.set("skipped", str(skipped))
    business = [v for (agent, case, scope), v in latest.items() if scope == "business_workflow"]
    counts = {status: sum(v["status"] == status for v in business) for status in ("passed", "failed", "blocked", "running")}
    lines += ["", f"业务矩阵：{counts['passed']} 项通过，{counts['failed']} 项失败，{counts['blocked']} 项阻塞，{counts['running']} 项运行中。"]
    findings = output / "findings.json"
    if findings.is_file():
        lines += ["", "## 本轮已确认的问题", ""]
        lines += ["- " + finding["description"] for finding in json.loads(findings.read_text(encoding='utf-8'))]
    runtime_record = output / 'hosted-runtime.json'
    if runtime_record.is_file():
        runtime = json.loads(runtime_record.read_text(encoding='utf-8'))
        lines += ['', f"运行环境：GitHub 托管的 {runtime['platform']} 临时虚拟机；LazyMind 从源码构建：`{runtime['source_sha']}`。",
                  f"GitHub 工作流 SHA：`{os.environ.get('GITHUB_WORKFLOW_SHA', 'unknown')}`。"]
        installed = output / 'installed-agents.json'
        if installed.is_file():
            versions = json.loads(installed.read_text(encoding='utf-8'))['versions']
            lines += ['Agent 版本：' + '；'.join(f'{name} `{value}`' for name, value in versions.items()) + '。']
        lines += ['各业务场景的 LazyMind workflow revision 和产物校验见 JUnit system-out 记录。']
    else:
        lines += ['', '本轮使用现有本地 LazyMind 服务；运行中服务的构建版本尚未独立核实。']
    lines += ["",
              "CLI/MCP 测试不覆盖桌面面板继续、停止或恢复。视觉质量、字幕识别和科学正确性不由文件结构检查证明。"]
    (output / "report.md").write_text("\n".join(lines) + "\n", encoding='utf-8')
    ET.ElementTree(suite).write(output / "junit.xml", encoding="utf-8", xml_declaration=True)
    write_json(output / "history.json", history)
    write_json(output / "summary.json", list(latest.values()))
    print("\n".join(lines))
    return 0 if latest and not failures and not skipped else 1


def resume_case(args):
    if not args.result:
        raise ValueError("resume requires --result pointing to this runner's result.json")
    path = Path(args.result).resolve()
    path.relative_to(Path(args.output).resolve())
    with run_lock(path.parent):
        return resume_locked(args, path)


def resume_locked(args, path):
    result = json.loads(path.read_text(encoding='utf-8'))
    owner = result.get("supervisor_pid")
    if owner and owner != os.getpid():
        try:
            os.kill(owner, 0)
        except ProcessLookupError:
            pass
        else:
            raise RuntimeError("This test run still has an active supervisor")
    if path.name != "result.json" or Path(result["directory"]).resolve() != path.parent:
        raise ValueError("Result directory does not match the saved test run")
    if result.get("scope") == "read_only_mcp_connectivity" or result.get("agent") not in AGENTS or result.get("case") not in CASES:
        raise ValueError("Only a saved business test can be resumed")
    result["scope"] = "business_workflow"
    client = None
    try:
        expected = result.get("connection_context") or connection_snapshot(args)
        result["connection_context"] = prepare_connection(args, path.parent, result["workflow_id"],
                                                         result["revision_id"], "business", expected)
        client = MCP(args.cli, clean_env(args.home, args.server))
        result.setdefault("previous_results", []).append({"status": result.get("status"), "reason": result.get("reason"), "turns": len(result.get("turns", []))})
        result.update(status="running", reason=None, supervisor_pid=os.getpid())
        write_json(path, result)
        finish_run(args, result, path.parent, client, discover())
    except ConnectionGuardError as error:
        result.update(status="blocked", reason=str(error))
    except (RuntimeError, TimeoutError, OSError, ValueError, KeyError, zipfile.BadZipFile) as error:
        result.update(status="failed", reason=safe_diagnostic(error))
    finally:
        if client:
            client.close()
        write_json(path, result)
    print(json.dumps({k: result.get(k) for k in ("agent", "case", "status", "reason", "directory")}, ensure_ascii=False))
    return 0 if result["status"] == "passed" else 1


def ppt_export_callback(directory, env):
    """Trusted, offline native capability restricted to this fixed three-slide fixture."""
    root = directory.resolve()
    lock = threading.Lock()
    calls = 0

    def export(body):
        nonlocal calls
        if not isinstance(body, dict) or set(body) != {'deck_dir'} or not isinstance(body['deck_dir'], str):
            raise ValueError('invalid_export_request')
        deck = root / 'deck'
        if deck.is_symlink() or deck.resolve() != deck or Path(body['deck_dir']).resolve() != deck:
            raise ValueError('export_must_use_this_fixture_deck')
        files, pending = [], [deck]
        while pending:
            for path in pending.pop().iterdir():
                if path.is_symlink() or not path.resolve().is_relative_to(deck):
                    raise ValueError('export_fixture_cannot_link_external_files')
                files.append(path)
                if len(files) > 40:
                    raise ValueError('export_fixture_too_many_files')
                if path.is_dir():
                    pending.append(path)
        allowed = {'.html', '.txt', '.md', '.json', '.svg', '.png', '.jpg', '.jpeg', '.pptx'}
        if (len(files) > 40 or any(p.is_symlink() or (p.is_file() and p.suffix.lower() not in allowed) for p in files)
                or sum(p.stat().st_size for p in files if p.is_file()) > 32 * 1024 * 1024
                or {p.name for p in (deck / 'pages').glob('*.html')} != {'page_001.html', 'page_002.html', 'page_003.html'}):
            raise ValueError('invalid_three_slide_fixture')
        if not lock.acquire(blocking=False):
            raise ValueError('export_already_running')
        try:
            calls += 1
            if calls > 3:
                raise ValueError('export_repair_budget_exhausted')
            exporter = ROOT / 'workflows/ppt-workflow/runtime/scripts/export_pptx/html_to_pptx.mjs'
            # No model credentials or GitHub credentials enter Chromium.
            allowed_env = {'HOME', 'USER', 'LOGNAME', 'PATH', 'TMPDIR', 'TEMP', 'TMP', 'LANG',
                           'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'SYSTEMROOT', 'SystemRoot',
                           'COMSPEC', 'PATHEXT', 'PLAYWRIGHT_BROWSERS_PATH'}
            export_env = {k: v for k, v in env.items() if k in allowed_env}
            export_env['LAZYMIND_PPT_EXTERNAL_OFFLINE'] = '1'
            node = shutil.which('node', path=env.get('PATH'))
            if not node:
                raise ValueError('node_not_installed')
            tree = host_platform.ProcessTree([node, str(exporter), '--deck-dir', str(deck),
                '--output', 'presentation.pptx', '--force', '--no-embed-fonts'], cwd=root, env=export_env,
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                stdout, stderr = tree.process.communicate(timeout=240)
            except subprocess.TimeoutExpired:
                tree.stop(force=True)
                tree.process.communicate(timeout=15)
                return {'status': 'failed', 'error': 'native_ppt_export_timeout'}
            finally:
                tree.close()
            payload = next((json.loads(line) for line in reversed(stdout.decode('utf-8', errors='replace').splitlines())
                            if line.startswith('{') and line.endswith('}')), {})
            if tree.process.returncode or not payload.get('success') or payload.get('converted') != 3:
                return {'status': 'failed', 'error': safe_diagnostic(payload.get('error') or stderr.decode('utf-8', errors='replace'))}
            check = validate_file(deck / 'presentation.pptx', 'ppt')
            return {'status': 'ok', 'path': str(deck / 'presentation.pptx'), **check}
        finally:
            lock.release()
    return export


def invoke(command, env, prompt, directory, timeout):
    config_path = env.get('LAZYMIND_CI_PROXY_CONFIG')
    if config_path:
        supplied = providers.load(config_path, required=True)
        context_file = directory / 'connection.json'
        context = json.loads(context_file.read_text(encoding='utf-8')) if context_file.is_file() else {}
        native_export = ppt_export_callback(directory, env) if (context.get('mode') == 'business'
                        and context.get('workflow_id') == 'ppt-workflow' and env.get('LAZYMIND_CI_MANAGED_AGENT')) else None
        with providers.ModelProxy(supplied, ppt_export=native_export) as proxy:
            child_env = {k: v for k, v in env.items() if not k.startswith('LAZYMIND_CI_') and not k.endswith(('_API_KEY', '_AUTH_TOKEN'))}
            child_env.update(CODEBUDDY_BASE_URL=proxy.base_url, CODEBUDDY_API_KEY=proxy.token,
                             LAZYMIND_CI_LLM_BASE_URL=proxy.base_url, LAZYMIND_CI_IMAGE_BASE_URL=proxy.base_url,
                             LAZYMIND_CI_PROXY_SESSION=proxy.token)
            if env.get('LAZYMIND_CI_MANAGED_AGENT') == 'codex':
                child_env['OPENAI_API_KEY'] = proxy.token
                # Append a CLI override after the placeholder, before stdin '-'.
                command = [*command[:-1], '--config',
                           'model_providers.lazymind-ci.base_url=' + json.dumps(proxy.base_url), command[-1]]
            if env.get('LAZYMIND_CI_DSH_PATCH'):
                profile = {'apiKeyEnv': 'CODEBUDDY_API_KEY', 'api': 'openai-responses'
                           if supplied['llm']['api'] == 'openai-responses' else 'openai-completions',
                           'baseURL': proxy.base_url, 'transport': 'sse', 'models': [{
                               'id': supplied['llm']['model'], 'name': supplied['llm']['model'],
                               'contextWindow': 200000, 'maxTokens': 16384, 'input': ['text'],
                               'reasoningEfforts': False}]}
                private_config(env['LAZYMIND_CI_DSH_PATCH'], json.dumps([
                    {'id': 'llm-pi-ai', 'config': {'providers': {'lazymind-ci': profile}}},
                    {'id': 'agent-default-model', 'config': {'provider': 'lazymind-ci',
                                                          'model': supplied['llm']['model']}}]))
            result = invoke_process(command, child_env, prompt, directory, timeout)
            result['provider_diagnostics'] = proxy.diagnostics[-8:]
            return result
    return invoke_process(command, env, prompt, directory, timeout)


def invoke_process(command, env, prompt, directory, timeout):
    """Bound process lifetime, discard reasoning, retain only event type/session IDs."""
    started = time.monotonic()
    tree = host_platform.ProcessTree(command, cwd=directory, env=env, stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    process = tree.process
    session, event_types, diagnostics, host_error, last_message = None, {}, [], False, ""
    output = queue.Queue()
    input_errors = []
    def read_stream(stream, name):
        try:
            while chunk := os.read(stream.fileno(), 65536):
                output.put((name, chunk))
        finally:
            output.put((name, None))
    readers = [threading.Thread(target=read_stream, args=(stream, name), daemon=True)
               for stream, name in ((process.stdout, 'stdout'), (process.stderr, 'stderr'))]
    for reader in readers:
        reader.start()
    def feed_prompt():
        # A CLI may fill stdout/stderr before it reads stdin. Never block the
        # supervisor on a full input pipe before output draining/timeouts start.
        try:
            process.stdin.write(prompt.encode('utf-8'))
            process.stdin.flush()
        except (BrokenPipeError, OSError):
            input_errors.append('host_stdin_closed_before_prompt_finished')
        finally:
            with contextlib.suppress(BrokenPipeError, OSError):
                process.stdin.close()
    writer = threading.Thread(target=feed_prompt, daemon=True)
    writer.start()
    timed_out = False
    buffers = {"stdout": b"", "stderr": b""}
    dropping = {"stdout": False, "stderr": False}

    def diagnostic(value):
        text = str(value)
        # Custom providers and the per-turn proxy need not use sk-/JWT keys.
        # Remove their exact credentials before retaining a bounded summary.
        for key, secret in env.items():
            if len(secret) >= 8 and re.search(r"(?i)(key|token|secret|password|proxy_session)$", key):
                text = text.replace(secret, "[REDACTED]")
        return safe_diagnostic(text)

    def consume(raw):
        nonlocal session, host_error, last_message
        line = raw.decode("utf-8", errors="replace")
        try:
            event = json.loads(line)
            kind = event.get("type", event.get("event", "unknown"))
            event_types[str(kind)] = event_types.get(str(kind), 0) + 1
            session = event.get("thread_id") or event.get("session_id") or event.get("sessionId") or session
            item = event.get("item", {})
            if isinstance(item, dict) and item.get("type") == "agent_message":
                last_message = diagnostic(item.get("text", ""))
            elif kind == 'assistant' and isinstance(event.get('message'), dict):
                for part in event['message'].get('content', []):
                    if isinstance(part, dict) and part.get('type') == 'text':
                        last_message = diagnostic(part.get('text', ''))
            elif kind == 'result' and isinstance(event.get('result'), str):
                last_message = diagnostic(event['result'])
            elif kind == "text" and isinstance(event.get("text"), str):
                last_message = diagnostic(event["text"])
            if event.get("is_error") or kind in ("turn.failed", "error"):
                host_error = True
                diagnostics.append(diagnostic(event.get("errors") or event.get("error") or event.get("message", "Agent reported an error")))
        except (ValueError, AttributeError):
            for marker in ("Unknown argument", "unexpected argument", "No API key", "Unauthorized",
                           "MODULE_NOT_FOUND", "Cannot find package", "not logged in", "Missing credentials"):
                if marker.lower() in line.lower() and marker not in diagnostics:
                    diagnostics.append(marker)
    try:
        active = {'stdout', 'stderr'}
        while active:
            if time.monotonic() - started > timeout:
                timed_out = True
                tree.stop()
                break
            try:
                stream, chunk = output.get(timeout=.5)
            except queue.Empty:
                if process.poll() is not None:
                    # The root CLI has finished. Orphan MCP helpers can retain
                    # pipe handles; clean them up instead of waiting for timeout.
                    for name in active:
                        if buffers[name] and not dropping[name]:
                            consume(buffers[name])
                    break
                continue
            if chunk is None:
                if buffers[stream] and not dropping[stream]:
                    consume(buffers[stream])
                active.discard(stream)
                continue
            buffers[stream] += chunk
            while b"\n" in buffers[stream]:
                line, buffers[stream] = buffers[stream].split(b"\n", 1)
                if not dropping[stream]:
                    consume(line)
                dropping[stream] = False
            if len(buffers[stream]) > 16 * 1024 * 1024:
                buffers[stream] = b""
                dropping[stream] = True
                event_types["oversized_event_omitted"] = event_types.get("oversized_event_omitted", 0) + 1
    finally:
        if process.poll() is None:
            tree.stop()
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            tree.stop(force=True)
            process.wait()
        # Own only this invocation's group; release leftover MCP lease renewers.
        tree.close()
        writer.join(timeout=2)
        for reader in readers:
            reader.join(timeout=2)
        process.stdout.close()
        process.stderr.close()
    if input_errors and not timed_out:
        diagnostics.extend(input_errors)
        host_error = True
    return {"exit_code": process.returncode, "timed_out": timed_out,
            "host_session_id": session, "event_types": event_types,
            "host_error": host_error, "diagnostics": diagnostics, "last_message": last_message,
            "seconds": round(time.monotonic() - started, 2)}


def failure_diagnostics(result, directory):
    """Publish only bounded summaries; never raw events, tool inputs or profiles."""
    fields = ("exit_code", "timed_out", "host_error", "seconds", "event_types", "diagnostics", "last_message", "provider_diagnostics")
    details = {"agent": result["agent"], "case": result["case"], "reason": result.get("reason"),
               "turns": [{k: turn.get(k) for k in fields} for turn in result.get("turns", [])],
               "connection_attempts": [{"status": attempt["status"],
                    "turn": {k: attempt["turn"].get(k) for k in fields}}
                    for attempt in result.get("connection_attempts", [])],
               "mcp_events": trace_events(directory)[-40:]}
    state_path = directory / 'state.json'
    if state_path.is_file():
        state = json.loads(state_path.read_text(encoding='utf-8'))
        control = state.get('control', {})
        details['workflow'] = {k: control.get(k) for k in ('continuation', 'automatic_retry_steps')}
    write_json(directory / 'failure-diagnostics.json', details)
    print('Failure diagnostic: ' + json.dumps(details, ensure_ascii=False), flush=True)


def run_case(args, agent, case, commands):
    directory = Path(args.output).resolve() / f"{agent}-{case}-{uuid.uuid4().hex[:8]}"
    directory.mkdir(parents=True, mode=0o700)
    (directory / "materials.md").write_text(MATERIALS, encoding='utf-8')
    result = {"agent": agent, "case": case, "status": "running", "directory": str(directory),
              "agent_version": version(commands[agent]) if commands[agent] else "missing",
              "provenance": provenance(Path(args.cli)), "turns": []}
    result["started_at"] = time.time()
    result["supervisor_pid"] = os.getpid()
    result["scope"] = "read_only_mcp_connectivity" if args.probe else "business_workflow"
    write_json(directory / "result.json", {**result, "status": "running"})
    if not commands[agent]:
        result["status"] = "blocked"
        result["reason"] = "agent_not_installed"
        write_json(directory / "result.json", result)
        return result
    client = None
    try:
        expected = connection_snapshot(args)
        result["connection_context"] = expected
        client = MCP(args.cli, clean_env(args.home, args.server))
        catalog = client.call("workflow.list").get("workflows", [])
        workflow_id, task = CASES[case]
        workflow = next((w for w in catalog if w.get("workflow_id") == workflow_id), None)
        if not workflow:
            raise RuntimeError("Requested workflow is not published")
        result["workflow_id"], result["revision_id"] = workflow_id, workflow["revision_id"]
        prompt = ("这是独立的LazyMind真实集成测试。MCP连接仅使用名为lazymind的服务；"
                  "可以使用宿主终端、文件读写和原生生成能力执行步骤。"
                  "必须调用workflow.get读取启动契约，再调用workflow.start创建真实运行；"
                  "按返回契约使用宿主生成能力执行步骤、发布产物并完成步骤，不得伪造结果。"
                  "遇到待审核或用户问题时停下，由外部测试用户处理，不可自行审批。"
                  "不要访问本测试目录以外的个人文件，不要修改项目代码或其他会话。\n"
                  f"固定workflow_id={workflow_id}，revision_id={workflow['revision_id']}。\n"
                  + task + ("\n完整读取materials.md。" if case != "meme" else ""))
        if case == "ppt":
            exporter = ROOT / "workflows/ppt-workflow/runtime/scripts/export_pptx/html_to_pptx.mjs"
            browsers = Path(os.environ.get('PLAYWRIGHT_BROWSERS_PATH', str(args.home.parent / "Library/Application Support/LazyMind/deps/editable-ppt/browsers")))
            prompt += ('\n用户已明确选择原生 PPTX。workflow.start 必须显式传入 '
                       'clarification_values.presentation_output_format="native_pptx"，不要只写在 request_context 中；'
                       '测试控制器也会将这个已确认的固定用例参数绑定到实际启动请求，保留其他启动答案。'
                       '其余已确认的启动参数仍按 workflow.get 的契约一并传入。'
                       '分析步骤必须保持相同的 presentation_output_format=native_pptx，不得改成 html。'
                       '以下导出程序使用的 HTML 只是宿主内部中间文件，不是工作流最终输出格式；'
                       '不要调用 HTML 分支的页面准备/发布流程，不发布 preview_html 或 preview_notes。'
                       f"\n宿主可使用已安装的确定性PPT导出程序：{exporter}。"
                       "使用宿主终端执行该程序导出原生 PPTX 文件，不能交付HTML冒充PPTX。"
                       "命令为node <上述程序> --deck-dir <本测试目录的deck子目录> --output presentation.pptx --force --no-embed-fonts；"
                       "HTML页面放deck/pages目录，文件命名为page_001.html、page_002.html、page_003.html。设置LAZYMIND_PPT_EXTERNAL_OFFLINE=1，禁止自动安装依赖、联网或远程资源。"
                       + ('托管 CI 已安装支持中文的 Noto Sans SC，HTML 优先使用该字体。' if os.environ.get('LAZYMIND_CI_RUNTIME_PROVENANCE') else '') +
                       f"已安装浏览器目录若存在则设置PLAYWRIGHT_BROWSERS_PATH={browsers}。"
                       "导出程序只读项目现有工具代码，输出和临时文件必须留在本测试目录。"
                       "按步骤契约发布实际PPTX及其要求的其他产物后再完成步骤。")
        if agent == "deepseek-harness" and case == "meme":
            prompt += ("\n当前DSH模型只支持文本输入。不要调用read_image或把图片加入模型上下文，"
                       "这会导致模型API拒绝整个后续会话。使用实际图片生成工具和宿主文件处理能力；"
                       "通过文件解码检查尺寸与帧数。结构检查不证明图片主体、背景等视觉质量，"
                       "不要声称自己已经目视检查。生成工具若返回1024方图，可在宿主等比例缩为请求的512方图，"
                       "再将该真实图片发布到 generated_base_image 和 generated_image_output。")
        supplied = providers.load(args.providers_config, require_image=case == 'meme' and not args.probe) if agent == 'workbuddy' or getattr(args, 'managed_models', False) else None
        if supplied:
            result['provider_routes'] = providers.public(supplied)
            if case == 'ppt' and getattr(args, 'managed_models', False):
                route_path = args.private_profile_root / directory.name / 'providers.json'
                helper = host_platform.shell_command([sys.executable, str(SCRIPT), 'provider-ppt-export',
                    '--providers-config', str(route_path), '--deck-dir', str(directory / 'deck')])
                prompt += ('\n本次 CI 的可信原生导出服务可调用已安装的 Chromium。请先在 deck/pages 写入上述三个 HTML 文件，'
                           '然后通过宿主终端执行以下辅助命令导出真实 PPTX：\n' + helper +
                           '\n本次导出应使用该服务，避免 Agent 沙箱无法启动 Chromium。服务仅接受本测试 deck 目录、离线资源、'
                           '固定三页和禁用字体嵌入；不得伪造返回结果。这些 HTML 仅为内部导出材料，工作流仍须选择 native_pptx。'
                           '成功后通过 output.local_path、output.content_type=file 发布实际文件到 native_pptx，再完成工作流步骤。')
            if case == 'meme' and 'image' in supplied:
                route_path = args.private_profile_root / directory.name / 'providers.json'
                helper = host_platform.shell_command([sys.executable, str(SCRIPT), 'provider-image', '--providers-config', str(route_path),
                                     '--image-output', str(directory / 'generated-base.png'), '--image-prompt', '<本步骤的无文字英文生图提示词>'])
                prompt += ('\n本次 CI 使用已验证的独立生图服务。generate_image 步骤通过宿主终端执行以下可信辅助程序发起真实生图：\n'
                           + helper + '\n不要使用 Agent 默认生图工具或替代模型，不要读取配置内容或环境变量。'
                           '辅助程序从私有配置读取密钥并返回真实512方图路径和哈希；将同一个真实文件发布到 generated_base_image 和 generated_image_output。'
                           '本用例为普通静态生图，不调用字幕工具，不添加文字。验收时仅豁免生图服务自动附带的 AI 生成类水印。按工作流契约结束。')
        if args.probe:
            prompt = ("只调用lazymind MCP的workflow.list和workflow.get，不创建运行、不生成文件。"
                      f"确认可以读取workflow_id={workflow_id}，revision_id={workflow['revision_id']}。")
        attempts = probe_agent_connection(args, agent, directory, commands[agent], workflow_id,
                                          workflow["revision_id"], expected)
        result["connection_attempts"] = attempts
        result["connection_probe_seconds"] = sum(a["turn"]["seconds"] for a in attempts)
        if args.probe:
            result["status"] = "passed"
            result["scope"] = "read_only_mcp_connectivity"
            result["tools_called"] = ["workflow.list", "workflow.get"]
        else:
            result["connection_context"] = prepare_connection(args, directory, workflow_id,
                                                              workflow["revision_id"], "business", expected)
            command, env = agent_command(agent, directory, args, commands[agent])
            budget = max(1, int(args.timeout - (time.time() - result["started_at"])))
            turn = invoke(command, env, prompt, directory, budget)
            result["turns"].append(turn)
            assert_connection(args, expected)
            events = trace_events(directory)
            result["tools_called"] = sorted({e["tool"] for e in events if e.get("event") == "tool_call"})
            if (turn.get('host_error') or turn.get('exit_code') or turn.get('timed_out')) and (reason := provider_block_reason(turn)):
                result.update(status='blocked', reason=reason)
            elif turn["host_error"]:
                authentication = any("Authentication required" in d for d in turn["diagnostics"])
                result.update(status="blocked" if authentication else "failed",
                              reason="host_authentication_required" if authentication else "host_execution_error")
            else:
                finish_run(args, result, directory, client, commands)
    except ConnectionGuardError as error:
        result.update(status="blocked", reason=str(error))
    except providers.ProviderConfigurationError as error:
        result.update(status='blocked', reason=safe_diagnostic(error))
    except (RuntimeError, TimeoutError, OSError, StopIteration, ValueError, KeyError, zipfile.BadZipFile) as error:
        result["status"] = "failed"
        result["reason"] = safe_diagnostic(error)
    finally:
        if client:
            client.close()
        attempts_file = directory / "connection-attempts.json"
        if attempts_file.exists():
            result["connection_attempts"] = json.loads(attempts_file.read_text(encoding='utf-8'))
    write_json(directory / "result.json", result)
    if result['status'] != 'passed':
        failure_diagnostics(result, directory)
    return result


def main():
    # MCP is UTF-8 on every OS, including Windows consoles with a legacy code page.
    for stream in (sys.stdin, sys.stdout, sys.stderr):
        if hasattr(stream, 'reconfigure'):
            stream.reconfigure(encoding='utf-8')
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("doctor", "run", "observe", "setup-dsh", "report", "resume", "build-cli", 'config-check', 'provider-image', 'provider-ppt-export'))
    parser.add_argument("--cli")
    parser.add_argument("--home", type=Path, default=Path.home() / ".lazymind")
    parser.add_argument("--server", help="Expected loopback origin; defaults to the current LazyMind credential scope")
    parser.add_argument("--output", default=str(ROOT / "outputs/agent-e2e"))
    parser.add_argument("--trace")
    parser.add_argument("--connection-context", help=argparse.SUPPRESS)
    parser.add_argument('--providers-config', type=Path, default=Path(os.environ.get('LAZYMIND_CI_PROVIDERS_CONFIG', providers.DEFAULT_PATH)),
                        help='仓库外私有模型配置；先复制 agent_e2e.providers.example.json，或使用 CI Secrets 环境变量')
    parser.add_argument('--private-profile-root', type=Path, default=Path.home() / '.config/lazymind-ci/workbuddy-profiles')
    parser.add_argument('--managed-models', action='store_true', help='Use fresh CI model routes for all three Agents')
    parser.add_argument('--image-output', type=Path)
    parser.add_argument('--image-prompt')
    parser.add_argument('--deck-dir', type=Path)
    parser.add_argument("--agent", choices=(*AGENTS, "all"), default="all")
    parser.add_argument("--case", choices=(*CASES, "all"), default="all")
    parser.add_argument("--timeout", type=int, default=1200)
    parser.add_argument("--probe", action="store_true", help="Only test actual agent MCP reads")
    parser.add_argument("--dsh-profile", default="lazymind-ci")
    parser.add_argument("--dsh-use-stored-model", action="store_true")
    parser.add_argument("--approve-test-reviews", action="store_true", help="Explicit test-user approval for this run's fixed fixtures only")
    parser.add_argument("--max-turns", type=int, default=12)
    parser.add_argument("--result", help="Saved test result for resume; must be under --output")
    args = parser.parse_args()
    args.home = args.home.expanduser().resolve()
    args.providers_config = args.providers_config.expanduser().resolve()
    args.private_profile_root = args.private_profile_root.expanduser().resolve()
    if args.action == 'provider-ppt-export':
        supplied = providers.load(args.providers_config, required=True)
        if urllib.parse.urlsplit(supplied['llm']['base_url']).hostname != '127.0.0.1' or not args.deck_dir:
            raise ValueError('Native CI export requires this turn\'s loopback capability')
        result = providers.request_json(supplied['llm'], '/ci/ppt-export', {'deck_dir': str(args.deck_dir)}, timeout=260)
        print(json.dumps(result, ensure_ascii=False))
        return 0 if result.get('status') == 'ok' else 1
    if args.action in ('config-check', 'provider-image'):
        try:
            supplied = providers.load(args.providers_config, required=True,
                require_image=args.action == 'provider-image' or args.case in ('meme', 'all'))
            if args.action == 'config-check':
                print(json.dumps({'configured': True, 'providers': providers.public(supplied), 'keys': 'omitted'}, ensure_ascii=False, indent=2))
            else:
                if not args.image_output or not args.image_prompt:
                    parser.error('provider-image 需要 --image-output 和 --image-prompt')
                print(json.dumps(providers.generate_image(supplied, args.image_prompt, args.image_output), ensure_ascii=False))
            return 0
        except providers.ProviderConfigurationError as error:
            print(str(error), file=sys.stderr)
            return 2
    built = Path(args.output).resolve() / 'bin' / host_platform.executable_name('lazymind')
    args.auto_cli = args.cli is None
    args.cli = str(Path(args.cli).resolve()) if args.cli else str(built)
    if args.action == "observe":
        return observe(args)
    if not args.server:
        path = args.home / "credentials.json"
        args.server = json.loads(path.read_text(encoding='utf-8')).get("server_url", "") if path.is_file() else "http://localhost:8090"
    # Local tests must never accidentally transmit credentials to an arbitrary server.
    try:
        args.server = local_origin(args.server)
    except ConnectionGuardError:
        parser.error("This local runner requires an HTTP(S) loopback origin without credentials, path or query")
    if args.action in ("doctor", "run", "resume"):
        ensure_current_cli(args)
    if args.action == "doctor":
        return doctor(args)
    if args.action == "build-cli":
        return build_cli(args)
    if args.action == "setup-dsh":
        if args.dsh_profile in ("web", "desktop", "headless"):
            parser.error("Setup requires a dedicated test profile")
        return setup_dsh(args)
    if args.action == "report":
        return report(args)
    if args.action == "resume":
        return resume_case(args)
    commands = discover()
    results = []
    for agent in AGENTS if args.agent == "all" else [args.agent]:
        for case in CASES if args.case == "all" else [args.case]:
            print(f"Running {agent}/{case}", flush=True)
            result = run_case(args, agent, case, commands)
            results.append(result)
            print(json.dumps({k: result.get(k) for k in ("agent", "case", "status", "reason", "directory")}, ensure_ascii=False), flush=True)
            write_json(Path(args.output) / ("suite-" + str(os.getpid()) + ".json"), results)
    return 0 if all(r["status"] == "passed" for r in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
