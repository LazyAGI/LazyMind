import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const electron = createRequire(new URL("../electron/package.json", import.meta.url));
const yaml = createRequire(electron.resolve("electron-builder/package.json"))("js-yaml");
const load = file => yaml.load(fs.readFileSync(path.resolve(root, file), "utf8"));
const workflow = name => load(`.github/workflows/${name}.yml`);
const platforms = ["macos", "windows"];
const publisher = "./.github/workflows/desktop-update-publish.yml";
const calls = spec => Object.entries(spec.jobs).filter(([, j]) => j.uses === publisher);
const needs = job => [job.needs ?? []].flat();
function context(platform, event, result = "success") {
  return { inputs: { enable_updates: true, enable_unsigned_update_test: false, unsigned_update_test: false, enable_test_signed_updates: false, test_signed_updates: false, use_current_run: true },
    github: { event_name: event, ref: event === "push" ? "refs/tags/v1.2.0" : "refs/heads/test", run_id: "81" },
    vars: { LAZYMIND_DESKTOP_UPDATES_ENABLED: "true", LAZYMIND_DESKTOP_UPDATE_PUBLICATION_ENABLED: "true" },
    matrix: { platform }, needs: { verify: { result } }, cancelled: () => false, failure: () => result === "failure",
    success: () => result === "success", always: () => true, fromJSON: JSON.parse, toJSON: JSON.stringify,
    format: (template, ...args) => template.replace(/\{(\d+)\}/g, (_, i) => args[i]) };
}
function value(s, ctx) {
  if (typeof s !== "string") return s;
  const sole = /^\$\{\{\s*(.*?)\s*\}\}$/.exec(s);
  if (sole) return vm.runInNewContext(sole[1], ctx, { timeout: 300 });
  return s.replace(/\$\{\{\s*(.*?)\s*\}\}/g, (_, expression) => vm.runInNewContext(expression, ctx, { timeout: 300 }));
}
function enabled(job, ctx) {
  return job.if === undefined || Boolean(value(job.if.startsWith("${{") ? job.if : "${{" + job.if + "}}", ctx));
}
function inputValues(job, ctx) {
  return Object.fromEntries(Object.entries(job.with || {}).map(([key, v]) => [key, value(v, ctx)]));
}
function publisherScope(job, ctx) {
  const inputs = inputValues(job, ctx);
  assert.equal(inputs.use_current_run, true);
  assert.notEqual(inputs.unsigned_update_test, true);
  const entry = Object.entries(inputs).find(([, v]) => v === ctx.matrix.platform);
  assert.ok(entry, `no ${ctx.matrix.platform} scope passed to the reusable publication`);
  return inputs;
}
function admittedOfficialCalls(spec, ctx) {
  return calls(spec).filter(([, caller]) => enabled(caller, ctx) && inputValues(caller, ctx).unsigned_update_test !== true);
}
for (const platform of platforms) {
  const other = platforms.find(p => p !== platform);
  const installer = workflow(platform + "-installer");
  test(`${platform} manual and tag updates publish once when ${other} was never built or failed`, () => {
    const dispatch = context(platform, "workflow_dispatch");
    assert.deepEqual(Object.keys(installer.on.workflow_dispatch.inputs).sort(), (platform === "windows"
      ? ["enable_updates","git_ref","enable_test_signed_updates","test_version"] : ["enable_updates","git_ref","signing_mode","enable_unsigned_update_test","source_build_run_id","test_version","test_round"]).sort());
    const manual = admittedOfficialCalls(installer, dispatch);
    assert.equal(manual.length, 1, "enabled installer dispatch must reach its own publisher");
    const [, caller] = manual[0];
    publisherScope(caller, dispatch);
    assert.ok(needs(caller).length > 0 && needs(caller).every(n => n in installer.jobs && !n.includes(other)));
    dispatch.inputs.enable_updates = false;
    assert.equal(admittedOfficialCalls(installer, dispatch).length, 0);

    const release = workflow("desktop-release");
    for (const result of ["skipped","failure"]) {
      const tag = context(platform, "push");
      tag.needs[platform] = { result: "success" }; tag.needs[other] = { result };
      const direct = calls(release).filter(([, j]) => needs(j).includes(platform) && !needs(j).includes(other) && enabled(j, tag));
      const nested = admittedOfficialCalls(installer, tag);
      assert.equal(direct.length + nested.length, 1, `tag must publish ${platform} once with ${other} ${result}`);
      publisherScope((direct[0] || nested[0])[1], tag);
      assert.equal(calls(release).some(([, j]) => needs(j).includes(platform) && needs(j).includes(other)), false,
        "a second joint publisher can overwrite or block an independently admitted release");
    }
  });

  test(`${platform} publisher consumes only this run's platform artifacts and successful native evidence`, () => {
    const spec = workflow("desktop-update-publish");
    const dispatch = context(platform, "workflow_dispatch");
    const caller = admittedOfficialCalls(installer, dispatch)[0];
    const ctx = context(platform, "workflow_dispatch");
    // Exercise the reusable entry even while a missing installer call is red separately.
    const declared = Object.keys(spec.on.workflow_call.inputs).find(k => /platform/.test(k));
    assert.ok(caller || declared, "reusable publisher lacks single-platform scope");
    Object.assign(ctx.inputs, caller ? publisherScope(caller[1], dispatch) : { [declared]: platform });
    const verify = spec.jobs.verify, publish = spec.jobs.publish;
    const matrix = value(verify.strategy.matrix.platform, ctx);
    assert.deepEqual(Array.from(matrix), [platform]);
    assert.match(value(verify["runs-on"], ctx), platform === "windows" ? /^windows-/ : /^macos-/);
    const downloads = job => job.steps.filter(s => s.uses?.startsWith("actions/download-artifact@") && enabled(s, ctx));
    assert.deepEqual(downloads(verify).map(s => value(s.with.name, ctx)), [`desktop-update-${platform}-81`]);
    assert.ok(verify.steps.some(s => enabled(s, ctx) && s.run?.includes("verify-update-ci-pairs.py")));
    const nativeReport = verify.steps.find(s => enabled(s, ctx) && s.uses?.startsWith("actions/upload-artifact@"));
    assert.equal(value(nativeReport.with.name, ctx), `desktop-format-evidence-${platform}-81`);
    assert.ok(enabled(publish, ctx));
    assert.deepEqual(downloads(publish).map(s => value(s.with.name, ctx)).sort(),
      [`desktop-update-${platform}-81`, `desktop-format-evidence-${platform}-81`].sort());
    assert.ok(publish.steps.some(s => enabled(s, ctx) && s.run?.includes("publish-desktop-update.py") && s.run.includes("--expected-policy-sha256")));
    const failed = context(platform, "workflow_dispatch", "failure"); failed.inputs = ctx.inputs;
    assert.equal(enabled(publish, failed), false, "failed native evidence must not publish");
  });
}

// GitHub passes declared workflow_call inputs across the reusable workflow boundary.
function calledInputs(spec, caller, dispatch) {
  const supplied = inputValues(caller, dispatch);
  const declared = spec.on.workflow_call.inputs;
  for (const key of Object.keys(supplied)) assert.ok(key in declared, "undeclared publication input " + key);
  return Object.fromEntries(Object.entries(declared).map(([key, input]) =>
    [key, supplied[key] ?? input.default ?? (input.type === "boolean" ? false : "")]));
}

test("Windows test-signed dispatch produces and verifies only its Windows update in the test environment", () => {
  const installer = workflow("windows-installer");
  const spec = workflow("desktop-update-publish");
  const toggle = installer.on.workflow_dispatch.inputs.enable_test_signed_updates;
  assert.equal(toggle?.type, "boolean");
  assert.equal(toggle.default, false);
  assert.equal(installer.on.workflow_call.inputs.enable_test_signed_updates, undefined);
  const dispatch = context("windows", "workflow_dispatch");
  Object.assign(dispatch.inputs, { enable_updates: false, enable_test_signed_updates: true, test_version: "0.5.301" });
  for (const officialPublication of ["false", "true"]) {
    dispatch.vars.LAZYMIND_DESKTOP_UPDATE_PUBLICATION_ENABLED = officialPublication;
    const selected = calls(installer).filter(([, job]) => enabled(job, dispatch));
    assert.equal(selected.length, 1);
    const [, caller] = selected[0];
    const dependencies = new Set();
    function visit(job) {
      for (const id of needs(job)) {
        if (dependencies.has(id)) continue;
        dependencies.add(id);
        assert.ok(enabled(installer.jobs[id], dispatch), id + " must run");
        visit(installer.jobs[id]);
      }
    }
    visit(caller);
    assert.ok(dependencies.has("build-windows-installer") && dependencies.has("test-windows-installer"));
    const steps = installer.jobs["build-windows-installer"].steps;
    const prepare = steps.find(s => s.run?.includes("scripts/prepare-update-release.mjs"));
    assert.ok(prepare && enabled(prepare, dispatch));
    const archiveName = "desktop-update-windows-" + dispatch.github.run_id;
    const upload = steps.find(s => s.uses?.startsWith("actions/upload-artifact@") && s.with.name?.startsWith("desktop-update-windows-") && value(s.with.name, dispatch) === archiveName);
    assert.ok(upload && enabled(upload, dispatch) && steps.indexOf(upload) > steps.indexOf(prepare));
    const inputs = calledInputs(spec, caller, dispatch);
    assert.equal(inputs.platform, "windows");
    assert.equal(inputs.use_current_run, true);
    assert.equal(inputs.test_signed_updates, true);
    assert.equal(inputs.unsigned_update_test, false);
    for (const result of ["success", "failure", "skipped"]) {
      const ctx = context("windows", "workflow_dispatch", result);
      ctx.inputs = inputs;
      assert.equal(value(spec.jobs.publish.environment, ctx), "desktop_updates_test");
      assert.equal(enabled(spec.jobs.publish, ctx), result === "success");
      if (result !== "success") continue;
      const verify = spec.jobs.verify;
      assert.ok(enabled(verify, ctx));
      assert.deepEqual(Array.from(value(verify.strategy.matrix.platform, ctx)), ["windows"]);
      assert.match(value(verify["runs-on"], ctx), /^windows-/);
      const downloads = verify.steps.filter(s => s.uses?.startsWith("actions/download-artifact@") && enabled(s, ctx));
      assert.deepEqual(downloads.map(s => value(s.with.name, ctx)).sort(), [archiveName, "windows-rag-publication"].sort());
      assert.ok(!downloads.some(s => "run-id" in s.with), "both Windows artifacts must belong to this run");
      assert.ok(verify.steps.some(s => s.run?.includes("verify-update-ci-pairs.py") && enabled(s, ctx)));
    }
  }
});

test("test signing cannot publish to the normal Windows mirrors, including on conflicting inputs", () => {
  const installer = workflow("windows-installer");
  const ctx = context("windows", "workflow_dispatch");
  for (const official of [false, true]) for (const testSigned of [false, true]) {
    Object.assign(ctx.inputs, { enable_updates: official, enable_test_signed_updates: testSigned });
    const selected = calls(installer).filter(([, job]) => enabled(job, ctx));
    assert.equal(selected.length, Number(official !== testSigned));
    for (const id of ["mirror-windows-installer", "publish-windows-rag"]) {
      assert.equal(enabled(installer.jobs[id], ctx), !testSigned, id);
    }
  }
});

test("official Windows and macOS publications use the production environment and macOS ad-hoc uses the test environment", () => {
  const spec = workflow("desktop-update-publish");
  for (const platform of platforms) {
    const installer = workflow(platform + "-installer");
    const dispatch = context(platform, "workflow_dispatch");
    for (const adhoc of (platform === "macos" ? [false, true] : [false])) {
      Object.assign(dispatch.inputs, { enable_updates: !adhoc, enable_unsigned_update_test: adhoc });
      const selected = calls(installer).filter(([, job]) => enabled(job, dispatch));
      assert.equal(selected.length, 1);
      const ctx = context(platform, "workflow_dispatch");
      ctx.inputs = calledInputs(spec, selected[0][1], dispatch);
      assert.equal(value(spec.jobs.publish.environment, ctx), adhoc ? "desktop_updates_test" : "desktop_updates");
    }
  }
});

function platformRun(platform) {
  const spec = workflow("desktop-update-publish");
  const ctx = context(platform, "workflow_dispatch");
  const named = Object.keys(spec.on.workflow_call.inputs).find(k => /platform/.test(k)) || "platform";
  ctx.inputs[named] = platform;
  ctx.inputs.source_build_run_id = "";
  const caller = admittedOfficialCalls(workflow(platform + "-installer"), ctx)[0];
  if (caller) Object.assign(ctx.inputs, publisherScope(caller[1], ctx));
  ctx.vars.LAZYMIND_UPDATE_MODELSCOPE_REPO = "fixture/updates";
  ctx.vars.LAZYMIND_UPDATE_POLICY_KEY_ID = "fixture";
  ctx.secrets = { LAZYMIND_UPDATE_POLICY_PRIVATE_KEY: "fixture key", MODELSCOPE_UPDATE_TOKEN: "fixture token" };
  return { job: spec.jobs.publish, ctx };
}

function publicationBoundary(dir, platform) {
  // The shell, cmp, paths and step ordering are real. The external tools assert
  // their inputs at the boundary; the Python companion runs the actual signer and transport.
  return `uv() {
    local current="" history="" evidence="" output="" policy="" expected="" tool=""
    until [[ "$1" == desktop/scripts/*.py ]]; do shift; done
    tool="$1"; shift
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --current|--directory) current="$2";; --history) history="$2";; --evidence) evidence="$2";;
        --output-dir) output="$2";; --policy) policy="$2";; --expected-policy-sha256) expected="$2";;
        --repo|--validator|--config|--platform) ;; *) return 90;;
      esac
      shift 2
    done
    if [[ "$tool" == */prepare-update-ci-plan.py ]]; then
      [[ -n "$history" && -n "$evidence" && -d "$current" && -f "$history/$LANE/origin/release.json" &&
         -f "$evidence/$LANE/report.json" ]] || return 91
      [[ "$(find "$current" -name release.json | wc -l)" -eq 1 ]] || return 92
      mkdir -p "$output/records"; echo "$PUBLIC_SHA" > "$output/expected-policy-sha256"
      touch "$output/previous-envelope.json" "$output/plan.json" "$RUNNER_TEMP/planned"
    elif [[ "$tool" == */publish-desktop-update.py ]]; then
      [[ "$expected" == "$PUBLIC_SHA" && -f "$policy" && -f "$RUNNER_TEMP/signed" ]] || return 93
      touch "$RUNNER_TEMP/uploaded"
    else return 94; fi
  }
  function ${dir}/update-publisher() {
    [[ "$1" == sign && -f "$RUNNER_TEMP/planned" ]]; shift
    local records="" plan="" previous=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --records) records="$2";; --plan) plan="$2";; --previous-envelope) previous="$2";;
        --output|--config-output) touch "$2";; --key-id|--key-file) ;; *) return 95;;
      esac
      shift 2
    done
    [[ -d "$records" && -f "$plan" && -f "$previous" ]] || return 96
    touch "$RUNNER_TEMP/signed"
  }
  export LANE=${platform}`;
}
for (const platform of platforms) {
  for (const stale of [false, true]) {
    test(`${platform} ${stale ? "stale" : "matching"} evidence passes through its own plan, comparison and publication steps`, { skip: process.platform === "win32" }, t => {
      const { job, ctx } = platformRun(platform);
      const dir = fs.mkdtempSync("/tmp/lm-platform-shell-");
      t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
      ctx.runner = { temp: dir };
      const digest = "a".repeat(64), requests = [];
      let failedStep;
      const write = (folder, relative, data) => {
        const file = path.join(dir, folder, relative);
        fs.mkdirSync(path.dirname(file), { recursive: true });
        fs.writeFileSync(file, data);
      };
      for (const step of job.steps) {
        if (!enabled(step, ctx)) continue;
        if (step.uses?.startsWith("actions/download-artifact@")) {
          const name = value(step.with.name, ctx), dest = value(step.with.path, ctx);
          requests.push(name);
          if (name === `desktop-update-${platform}-81`) write(dest, "target/release.json", "{}");
          else if (name === `desktop-format-evidence-${platform}-81`) {
            write(dest, `${platform}-policy-sha256`, (stale ? "b".repeat(64) : digest) + "\n");
            write(dest, `records/${platform}/origin/release.json`, "{}");
            write(dest, `evidence/${platform}/report.json`, "{}");
          } else assert.fail("single-platform publication requested " + name);
        }
        if (!step.run || !["prepare-update-ci-plan.py", "update-publisher\" sign", "publish-desktop-update.py"].some(s => step.run.includes(s))) continue;
        const env = { PATH: process.env.PATH, HOME: dir, RUNNER_TEMP: dir, PUBLIC_SHA: digest };
        for (const [k, setting] of Object.entries(step.env || {})) env[k] = String(value(setting, ctx));
        const call = spawnSync("/bin/bash", ["-euc", publicationBoundary(dir, platform) + "\n" + step.run], { env, cwd: dir, encoding: "utf8" });
        if (call.status !== 0) { failedStep = step; break; }
      }
      assert.deepEqual(requests.sort(), [`desktop-update-${platform}-81`, `desktop-format-evidence-${platform}-81`].sort());
      // Even the stale case must reach planning with valid source/evidence inputs.
      assert.ok(fs.existsSync(path.join(dir, "planned")));
      if (stale) assert.ok(failedStep?.run.includes("cmp"), "stale native policy digest reached signing");
      else assert.equal(failedStep, undefined, failedStep?.name);
      assert.equal(fs.existsSync(path.join(dir, "signed")), !stale);
      assert.equal(fs.existsSync(path.join(dir, "uploaded")), !stale);
    });
  }
}
test("two successful same-run platform publications archive their policies under distinct artifact names", () => {
  const names = platforms.map(platform => {
    const { job, ctx } = platformRun(platform);
    const save = job.steps.find(s => s.uses?.startsWith("actions/upload-artifact@") && enabled(s, ctx) &&
      s.with.path?.includes("update-policy.json"));
    assert.ok(save);
    const name = value(save.with.name, ctx);
    assert.ok(name.includes(platform) && name.includes("81"), name);
    return name;
  });
  assert.notEqual(names[0], names[1]);
});

test("Windows test signing cannot fall back to official credentials and every native consumer receives only the public certificate", () => {
  const dispatch = context("windows", "workflow_dispatch");
  const installer = workflow("windows-installer"), spec = workflow("desktop-update-publish");
  const build = installer.jobs["build-windows-installer"], install = installer.jobs["test-windows-installer"];
  const pack = build.steps.find(s => s.name === "Build Windows installer");
  dispatch.secrets = { WIN_CSC_LINK: "official-pfx", WIN_CSC_KEY_PASSWORD: "official-pass",
    WINDOWS_TEST_CSC_LINK: "test-pfx", WINDOWS_TEST_CSC_KEY_PASSWORD: "test-pass" };
  assert.equal(value(pack.env.WIN_CSC_LINK, dispatch), "official-pfx");
  Object.assign(dispatch.inputs, { enable_updates: false, enable_test_signed_updates: true, test_version: "0.5.302" });
  assert.equal(value(pack.env.WIN_CSC_LINK, dispatch), "test-pfx");
  assert.equal(value(pack.env.WIN_CSC_KEY_PASSWORD, dispatch), "test-pass");
  dispatch.secrets.WINDOWS_TEST_CSC_LINK = dispatch.secrets.WINDOWS_TEST_CSC_KEY_PASSWORD = "";
  assert.equal(value(pack.env.WIN_CSC_LINK, dispatch), "");
  assert.equal(value(pack.env.WIN_CSC_KEY_PASSWORD, dispatch), "");

  const version = build.steps.find(s => s.id === "version");
  dispatch.inputs.test_version = "0.5.302";
  assert.match(version.run, /windows-test-channel\.mjs configure/);
  assert.equal(value(version.env.TEST_VERSION, dispatch), "0.5.302");
  dispatch.env = { LAZYMIND_UPDATE_BUILD_CONFIG_JSON: "bound test config" };
  assert.equal(value(pack.env.LAZYMIND_DESKTOP_UPDATE_CONFIG_JSON, dispatch), "bound test config");
  const verifyExe = build.steps.find(s => s.run?.includes("windows-test-certificate.ps1") && s.run.includes("-SignedFile"));
  assert.ok(verifyExe && enabled(verifyExe, dispatch), "test-signed dispatch verifies the produced installer");
  const prepare = build.steps.find(s => s.run?.includes("scripts/prepare-update-release.mjs"));
  const stage = build.steps.find(s => s.id === "package");
  assert.ok(build.steps.indexOf(stage) < build.steps.indexOf(verifyExe) && build.steps.indexOf(verifyExe) < build.steps.indexOf(prepare));
  assert.notEqual(verifyExe["continue-on-error"], true);
  assert.doesNotMatch(prepare.if || "", /always\(|failure\(/);
  dispatch.steps = { package: { outputs: { installer_path: "D:/stage/LazyMind signed.exe" } } };
  const installerPath = value(prepare.env.UPDATE_INSTALLER, dispatch);
  assert.equal(installerPath, dispatch.steps.package.outputs.installer_path);
  const env = { ...process.env, UPDATE_INSTALLER: value(verifyExe.env.UPDATE_INSTALLER, dispatch) };
  assert.equal(env.UPDATE_INSTALLER, installerPath);
  // Run the chosen Actions shell body. Only the Authenticode verifier is replaced.
  assert.equal(verifyExe.shell, "bash");
  const mock = 'pwsh() { printf "%s\n" "$@"; return "$VERIFY_EXIT"; }\n';
  for (const code of [0, 17]) {
    const call = spawnSync("bash", ["-ec", mock + value(verifyExe.run, dispatch)], { encoding: "utf8", env: { ...env, VERIFY_EXIT: String(code) } });
    assert.equal(call.status, code, "pin rejection must stop staging: " + call.stderr);
    const args = call.stdout.trim().split(/\r?\n/);
    assert.ok(args.some(a => a.endsWith("windows-test-certificate.ps1")));
    assert.equal(args[args.indexOf("-SignedFile") + 1], installerPath);
  }
  const ctx = context("windows", "workflow_dispatch");
  ctx.inputs = calledInputs(spec, calls(installer).find(([, j]) => enabled(j, dispatch))[1], dispatch);
  for (const [job, context, consumer] of [[build, dispatch, pack], [install, dispatch, install.steps.find(s => s.id === "install")],
    [spec.jobs.verify, ctx, spec.jobs.verify.steps.find(s => s.run?.includes("verify-update-ci-pairs.py"))],
    [spec.jobs.test_installed_upgrade, ctx, spec.jobs.test_installed_upgrade.steps.find(s => s.run?.includes("run-published-upgrade-smoke.py run"))]]) {
    context.vars.LAZYMIND_WINDOWS_TEST_CERT_DER_BASE64 = "pinned-public-cer";
    const pos = job.steps.findIndex(s => s.run?.includes("windows-test-certificate.ps1") && !s.run.includes("-SignedFile") && enabled(s, context));
    assert.ok(pos >= 0 && pos < job.steps.indexOf(consumer), job.name + " must trust the test certificate before using the signed installer");
    assert.equal(value(job.steps[pos].env.WINDOWS_TEST_CERTIFICATE, context), "pinned-public-cer");
    const official = { ...context, inputs: { ...context.inputs, enable_test_signed_updates: false, test_signed_updates: false } };
    assert.ok(!enabled(job.steps[pos], official), "official runs must not trust the test certificate");
    if (job !== build) assert.doesNotMatch(JSON.stringify(job.steps), /WINDOWS_TEST_CSC_LINK|WINDOWS_TEST_CSC_KEY_PASSWORD/);
  }
  const smoke = spec.jobs.test_installed_upgrade.steps.find(s => s.run?.includes("run-published-upgrade-smoke.py run"));
  Object.assign(ctx.vars, { LAZYMIND_WINDOWS_UPDATE_CONFIG_JSON: "official", LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON: "test" });
  assert.equal(value(smoke.env.SMOKE_PUBLIC_CONFIG, ctx), "test");
  ctx.vars.LAZYMIND_WINDOWS_TEST_UPDATE_CONFIG_JSON = "";
  assert.equal(value(smoke.env.SMOKE_PUBLIC_CONFIG, ctx), "");
});

test("the Windows test release is bound to its channel before signing or uploading components, and B runs the installed upgrade", () => {
  const installer = workflow("windows-installer"), spec = workflow("desktop-update-publish");
  const dispatch = context("windows", "workflow_dispatch");
  Object.assign(dispatch.inputs, { enable_updates: false, enable_test_signed_updates: true });
  const ctx = context("windows", "workflow_dispatch");
  ctx.inputs = calledInputs(spec, calls(installer).find(([, j]) => enabled(j, dispatch))[1], dispatch);
  const publish = spec.jobs.publish.steps.filter(s => enabled(s, ctx));
  const check = publish.findIndex(s => s.run?.includes("windows-test-channel.mjs check-release"));
  const sign = publish.findIndex(s => s.run?.includes('update-publisher" sign'));
  const components = publish.findIndex(s => s.run?.includes("publish-python-components.py"));
  const policy = publish.findIndex(s => s.run?.includes("publish-desktop-update.py"));
  assert.ok(check >= 0 && check < sign && sign < components && components < policy);
  ctx.secrets = { LAZYMIND_UPDATE_POLICY_PRIVATE_KEY: "official-private", WINDOWS_TEST_UPDATE_POLICY_PRIVATE_KEY: "test-private",
    MODELSCOPE_UPDATE_TOKEN: "official-upload", WINDOWS_TEST_MODELSCOPE_TOKEN: "test-upload" };
  assert.equal(value(publish[sign].env.UPDATE_PRIVATE_KEY, ctx), "test-private");
  for (const step of [publish[components], publish[policy]]) assert.equal(value(step.env.MODELSCOPE_API_TOKEN, ctx), "test-upload");
  ctx.secrets.WINDOWS_TEST_UPDATE_POLICY_PRIVATE_KEY = ctx.secrets.WINDOWS_TEST_MODELSCOPE_TOKEN = "";
  assert.equal(value(publish[sign].env.UPDATE_PRIVATE_KEY, ctx), "");
  for (const step of [publish[components], publish[policy]]) assert.equal(value(step.env.MODELSCOPE_API_TOKEN, ctx), "");

  assert.match(publish[components].run, /windows-test-channel\.mjs component-prefix/);
  assert.match(publish[components].run, /--signing-mode windows-test/);
  assert.match(publish[components].run, /--prefix "\$\{?component_prefix/);
  const plan = spec.jobs.plan_installed_upgrade;
  assert.ok(enabled(plan, ctx));
  const select = plan.steps.find(s => s.id === "select");
  assert.equal(value(select.env.PLATFORM, ctx), "windows");
  assert.match(select.run, /run-published-upgrade-smoke\.py matrix verification/);
  assert.equal(plan.outputs.lanes, "${{ steps.select.outputs.lanes }}");
  const smoke = spec.jobs.test_installed_upgrade;
  ctx.needs.plan_installed_upgrade = { outputs: { lanes: JSON.stringify([{ platform: "windows" }]) } };
  assert.ok(enabled(smoke, ctx));
  assert.match(value(smoke["runs-on"], ctx), /^windows-/);
  assert.ok(smoke.steps.some(s => s.run?.includes("run-published-upgrade-smoke.py run") && enabled(s, ctx)));
  const installed = installer.jobs["test-windows-installer"].steps.find(s => s.id === "verify");
  assert.match(installed.run, /displayName -eq "LazyMind"/);
  assert.match(installed.run, /Programs\\LazyMind/);
});
