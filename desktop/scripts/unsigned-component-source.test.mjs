import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";

test("component build URL validation requires explicit loopback test opt-in", () => {
  const python = process.platform === "win32" ? "python" : "python3";
  const filename = fileURLToPath(new URL("./build-python-components.py", import.meta.url));
  execFileSync(python, ["-I", "-B", "-c", `
import importlib.util, sys
spec = importlib.util.spec_from_file_location('components', sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
for enabled in (False, True):
    module.validate_base_url('https://example.invalid/rag', enabled)
    for address in ('http://localhost/rag', 'http://192.0.2.1/rag', 'http://127.0.0.1.example.invalid/rag', 'http://user@127.0.0.1/rag', 'http://127.0.0.1/rag#fragment'):
        try: module.validate_base_url(address, enabled)
        except ValueError: pass
        else: raise AssertionError(address)
try: module.validate_base_url('http://127.0.0.1:18765/rag')
except ValueError: pass
else: raise AssertionError('default build accepted HTTP')
module.validate_base_url('http://127.0.0.1:18765/rag', True)
`, filename], { stdio: "pipe", env: { ...process.env, LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST: "true" } });
  const help = execFileSync(python, ["-I", "-B", filename, "--help"], { encoding: "utf8" });
  assert.match(help, /--allow-test-loopback-http/);
});

test("only the unsigned builder enables local HTTP for a newly built component", async () => {
  const filename = fileURLToPath(new URL("../electron/electron-builder.config.cjs", import.meta.url));
  const source = fs.readFileSync(filename, "utf8");
  const split = source.slice(source.indexOf("async function splitPythonComponents("), source.indexOf("async function signAndStageEmbeddedRuntime("));
  assert.ok(split);
  for (const [unsigned, rebuild] of [[false, true], [true, true], [true, false]]) {
    const calls = [], module = { exports: {} };
    vm.runInNewContext(`${split}\nmodule.exports=splitPythonComponents;`, {
      module, path, __dirname: path.dirname(filename), unsignedTestBuild: unsigned, macSigningMode: "adhoc",
      process: { arch: "arm64", env: { LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS: String(rebuild) } },
      stageEmbeddedRuntime: () => ({ stagedRuntime: "/fixture/runtime" }), restoreEmbeddedRuntime() {},
      execFile: async (_file, args) => { calls.push(args); return { stdout: args.includes("-c") ? "arm64\n" : "" }; }, console: { log() {} },
    });
    await module.exports("/fixture/out/LazyMind.app/Contents/Resources/runtime");
    const invocation = calls.find(args => /(?:build|stage-published)-python-components\.py$/.test(args[0]));
    assert.ok(invocation);
    assert.equal(invocation.includes("--allow-test-loopback-http"), unsigned && rebuild);
  }
});
