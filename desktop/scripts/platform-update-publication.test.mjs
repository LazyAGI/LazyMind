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
  return { inputs: { enable_updates: true, enable_unsigned_update_test: false, unsigned_update_test: false, use_current_run: true },
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
      ? ["enable_updates","git_ref"] : ["enable_updates","git_ref","signing_mode","enable_unsigned_update_test","source_build_run_id","test_version","test_round"]).sort());
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
