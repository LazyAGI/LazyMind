"""End-to-end smoke tests exercising the three refactored layers.

Run with:
    PYTHONPATH=. python -m evo.tests.test_smoke
"""

from __future__ import annotations

import logging
import sys
import traceback
from typing import Any, Callable

from evo.domain.tool_result import ErrorCode, ToolResult
from evo.harness import analysis as analysis_steps, data_loader
from evo.harness.pipeline import PipelineOptions, build_standard_plan
from evo.harness.plan import Plan, Step, StepContext
from evo.harness.registry import get_registry
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope
from evo.tools import register_all


def _header(name: str) -> None:
    print(f"\n=== {name} ==========================================")


# ---------------------------------------------------------------------------
# Foundations
# ---------------------------------------------------------------------------

def test_domain_tool_result() -> None:
    _header("domain.tool_result envelope")
    ok = ToolResult.success("x", {"a": 1}, latency_ms=1.2)
    assert ok.ok and ok.unwrap() == {"a": 1}
    assert '"ok": true' in ok.to_json()

    fail = ToolResult.failure("x", ErrorCode.INVALID_ARGUMENT, "bad")
    assert not fail.ok
    try:
        fail.unwrap()
    except Exception as exc:
        assert "INVALID_ARGUMENT" in str(exc)
    print("  -> OK")


def test_registry_discovery() -> None:
    _header("tool registry auto-discovery")
    names = set(register_all())
    core_required = {
        "summarize_metrics", "list_bad_cases", "summarize_step_metrics",
        "list_subject_index", "resolve_import",
        "write_artifact", "recall_handle",
    }
    forbidden = {
        "load_judge_data", "load_trace_data", "save_report_bundle",
        "save_report", "format_report_for_display", "create_diagnosis_report",
        "plot_metric_distribution", "plot_pipeline_length_vs_score",
        "diff_metrics", "compare_reports",
    }
    missing = core_required - names
    leaked = forbidden & names
    assert not missing, f"missing core tools: {missing}"
    assert not leaked, f"removed tools resurfaced: {leaked}"
    print(f"  registered: {len(names)} tools (core ok, no removed leaks)")
    print("  -> OK")


# ---------------------------------------------------------------------------
# Session: typed accessors + isolation
# ---------------------------------------------------------------------------

def test_session_typed_accessors() -> None:
    _header("session exposes @property for every state slot")
    cfg = load_config()
    session = create_session(cfg)
    expected = [
        "parsed_judge", "parsed_trace", "trace_meta", "warnings",
        "case_step_features", "global_step_analysis",
        "clustering_global", "clustering_per_step", "flow_analysis",
        "artifacts", "stages_completed", "eval_report_meta",
    ]
    for name in expected:
        prop = getattr(type(session), name, None)
        assert isinstance(prop, property), f"{name} is not a @property on AnalysisSession"
        # And the property returns the same data as the underlying state slot:
        if name == "warnings":
            assert getattr(session, name) == list(getattr(session.state, name))
        elif name == "stages_completed":
            assert getattr(session, name) == frozenset(getattr(session.state, name))
        else:
            assert getattr(session, name) == getattr(session.state, name) \
                or getattr(session, name) is getattr(session.state, name)
    print(f"  {len(expected)} typed accessors present")
    print("  -> OK")


def test_session_state_isolation() -> None:
    _header("session state mutation through controlled setters")
    cfg = load_config()
    s1 = create_session(cfg)
    s2 = create_session(cfg)

    with session_scope(s1):
        data_loader.load_corpus(s1)
    assert len(s1.parsed_judge) > 0
    assert len(s2.parsed_judge) == 0
    assert not hasattr(cfg, "extra")
    assert s1.eval_report_meta is not None
    print(f"  s1 loaded {len(s1.parsed_judge)} cases; s2 empty as expected")
    print("  -> OK")


def test_model_gateway_is_session_scoped() -> None:
    _header("llm/embed gateways live inside session (no global state)")
    cfg = load_config()
    s1 = create_session(cfg)
    s2 = create_session(cfg)
    assert s1.llm is not s2.llm, "each session must own its own llm gateway"
    assert s1.embed is not s2.embed, "each session must own its own embed gateway"
    assert s1.llm is not None and s2.llm is not None
    assert s1.embed is not None and s2.embed is not None

    calls: list[str] = []

    def fake_producer() -> str:
        calls.append("produced")
        return "hello"

    out = s1.llm.call(fake_producer, cache_key="k1")
    assert out == "hello"
    out2 = s1.llm.call(fake_producer, cache_key="k1")
    assert out2 == "hello" and len(calls) == 1, "expected cache hit"
    out3 = s2.llm.call(fake_producer, cache_key="k1")
    assert out3 == "hello" and len(calls) == 2, "different sessions => no cache cross-talk"
    print("  -> OK")


# ---------------------------------------------------------------------------
# Tool layer purity
# ---------------------------------------------------------------------------

def test_tool_returns_typed_result() -> None:
    _header("tools return structured ToolResult")
    from evo.tools.data import list_bad_cases
    from evo.tools.stats import summarize_metrics

    cfg = load_config()
    session = create_session(cfg)
    with session_scope(session):
        data_loader.load_corpus(session)

        summary = summarize_metrics()
        assert isinstance(summary, ToolResult) and summary.ok, summary.error
        assert summary.data["total_cases"] > 0

        bc = list_bad_cases(threshold=0.6, limit=5)
        assert bc.ok, bc.error
        assert bc.data["score_field"] == cfg.badcase_score_field

        assert '"ok": true' in summary.to_json()
    print(f"  total_cases={summary.data['total_cases']}")
    print("  -> OK")


def test_no_session_writes_from_cluster_tools() -> None:
    _header("tool calls do not mutate session.clustering_global (harness owns state)")
    from evo.tools.data import list_bad_cases

    cfg = load_config()
    session = create_session(cfg)
    with session_scope(session):
        data_loader.load_corpus(session)
        analysis_steps.compute_step_features(session)

        result = list_bad_cases(threshold=0.6, limit=5)
        assert result.ok, result.error
        assert session.clustering_global is None, \
            "tool must not populate Session.clustering_global"
        assert not hasattr(session, "_harness_step_values")

        analysis_steps.cluster_global(session, badcase_limit=50,
                                      score_field=cfg.badcase_score_field)
        assert session.clustering_global is not None
    print("  -> OK")


def test_no_session_writes_from_io_tool() -> None:
    _header("write_artifact does not record session.artifacts")
    from evo.tools.io import write_artifact

    cfg = load_config()
    session = create_session(cfg)
    with session_scope(session):
        data_loader.load_corpus(session)
        r = write_artifact(relpath="reports/_smoke_a.txt", content="hi")
        assert r.ok and r.data["size_bytes"] > 0
        assert len(session.artifacts) == 0, \
            f"IO tool must not touch add_artifact (got {list(session.artifacts)})"
    print("  -> OK")


# ---------------------------------------------------------------------------
# Plan / StepContext
# ---------------------------------------------------------------------------

def test_step_context_threads_inter_step_values() -> None:
    _header("Plan: StepContext threads results between steps without setattr")
    cfg = load_config()
    session = create_session(cfg)

    def step_a(ctx: StepContext) -> int:
        return 7

    def step_b(ctx: StepContext) -> int:
        a = ctx.require("a")
        return a * 6

    def step_c(ctx: StepContext) -> dict:
        return {"a": ctx.get("a"), "b": ctx.get("b"), "missing": ctx.get("nope", "fallback")}

    plan = Plan([Step("a", step_a), Step("b", step_b), Step("c", step_c)])
    with session_scope(session):
        result = plan.run(session)

    assert result.success
    assert result.get("a") == 7
    assert result.get("b") == 42
    assert result.get("c") == {"a": 7, "b": 42, "missing": "fallback"}
    # Session must not have been mutated with the legacy smuggling bucket:
    assert not hasattr(session, "_harness_step_values")
    print("  -> OK")


def test_plan_runner_records_outcomes() -> None:
    _header("Plan runner: skip / fail / success outcomes")
    cfg = load_config()
    session = create_session(cfg)

    def step_ok(ctx: StepContext) -> int:
        return 42

    def step_fail(ctx: StepContext) -> int:
        raise RuntimeError("boom")

    def step_zero(ctx: StepContext) -> int:
        return 0

    plan = Plan([
        Step("first", step_ok),
        Step("skipme", step_zero, skip_if=lambda ctx: True),
        Step("optional_fail", step_fail, optional=True),
        Step("final", step_ok),
    ])
    with session_scope(session):
        result = plan.run(session)
    statuses = {o.name: o.status for o in result.outcomes}
    assert statuses == {
        "first": "ok", "skipme": "skipped",
        "optional_fail": "failed", "final": "ok",
    }, statuses
    assert result.success is True
    assert result.get("first") == 42
    assert "first" in session.stages_completed
    print("  -> OK")


def test_standard_plan_definition_is_declarative() -> None:
    _header("standard plan is data — no pipeline internals referenced at build time")
    plan = build_standard_plan(PipelineOptions())
    names = [s.name for s in plan.steps]
    assert names == [
        "load", "features", "cluster_global", "cluster_per_step",
        "flow", "indexer", "conduct", "synthesize",
        "build_report", "persist",
    ], names
    assert any(s.optional for s in plan.steps), "at least one optional step expected"
    print(f"  plan steps: {names}")
    print("  -> OK")


# ---------------------------------------------------------------------------
# Tool registry: middleware + race-safe discovery + LLM JSON view
# ---------------------------------------------------------------------------

def test_tool_middleware_hook() -> None:
    _header("tool registry no longer exposes middleware hooks")
    reg = get_registry()
    assert not hasattr(reg, "add_middleware")
    assert not hasattr(reg, "clear_middlewares")
    print("  -> OK")


def test_registry_lazy_autodiscovery() -> None:
    """Concurrent callers must never observe a partially-populated registry."""
    _header("registry auto-discovers concurrently without lost races")
    import threading
    import time
    import evo.harness.registry as reg_mod

    discovery_calls = 0

    def slow_fake_discover(_pkg: str) -> None:
        nonlocal discovery_calls
        discovery_calls += 1
        time.sleep(0.05)

    reg = reg_mod.ToolRegistry(discovery_package="unused")
    original = reg_mod._discover_package
    reg_mod._discover_package = slow_fake_discover
    try:
        threads = [threading.Thread(target=reg._ensure_discovered) for _ in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
    finally:
        reg_mod._discover_package = original

    assert discovery_calls == 1
    assert reg._discovered is True
    print(f"  8 concurrent callers -> {discovery_calls} discovery invocation(s)")
    print("  -> OK")


def test_tools_have_llm_json_view() -> None:
    _header("tools are callable via JSON envelope (for LLM agents)")
    from evo.harness.registry import get_llm_callable

    cfg = load_config()
    session = create_session(cfg)
    with session_scope(session):
        data_loader.load_corpus(session)
        fn = get_llm_callable("summarize_metrics")
        raw = fn()
        assert '"ok": true' in raw
    print("  -> OK")


# ---------------------------------------------------------------------------
# LLM provider injection
# ---------------------------------------------------------------------------

def test_llm_provider_injection() -> None:
    _header("LLMInvoker uses session.llm_provider; no chat.* import required")
    import sys
    from evo.harness.react import LLMInvoker

    sentinel = object()

    def fake_provider() -> Any:
        return sentinel

    # Wipe any cached chat.pipelines.builders.get_models so we can prove non-import.
    chat_keys = [k for k in sys.modules if k.startswith("chat.pipelines.builders.get_models")]
    for k in chat_keys:
        del sys.modules[k]

    cfg = load_config()
    session = create_session(cfg, llm_provider=fake_provider)
    inv = LLMInvoker(session=session, system_prompt="...")
    assert inv._build_llm() is sentinel
    assert "chat.pipelines.builders.get_models" not in sys.modules, \
        "LLMInvoker must not import the chat module when a provider is supplied."
    print("  -> OK")


def test_llm_invoker_uses_gateway_and_reuses_provider_client() -> None:
    _header("LLMInvoker: gateway call + session-level client reuse")
    from evo.harness.react import LLMInvoker

    provider_calls = 0
    model_calls = 0

    class FakeModel:
        def __call__(self, prompt: str) -> str:
            nonlocal model_calls
            model_calls += 1
            return f"ok:{len(prompt)}"

    def fake_provider() -> Any:
        nonlocal provider_calls
        provider_calls += 1
        return FakeModel()

    session = create_session(load_config(), llm_provider=fake_provider)
    inv = LLMInvoker(session=session, system_prompt="system")
    with session_scope(session):
        first = inv.invoke("hello", cache_key="same", agent="test")
        second = inv.invoke("hello", cache_key="same", agent="test")
    assert first == second
    assert provider_calls == 1
    assert model_calls == 1
    assert any(e.type == "llm_call" and e.payload.get("cache_hit") for e in session.telemetry.history)
    print("  -> OK")


# ---------------------------------------------------------------------------
# ModelGateway ContextVar propagation (regression: ReAct tools were getting
# DATA_NOT_LOADED because _run_with_timeout submitted to a raw ThreadPoolExecutor
# without copying ContextVars from the caller).
# ---------------------------------------------------------------------------

def test_session_handle_store_attached_and_used() -> None:
    _header("session.handle_store attached; tool calls populate handles")
    from evo.tools.stats import summarize_metrics
    register_all()
    session = create_session(load_config())
    assert session.handle_store is not None
    with session_scope(session):
        data_loader.load_corpus(session)
        result = summarize_metrics()
    assert result.ok and result.handle is not None
    assert session.handle_store.get(result.handle) is not None
    print(f"  handle={result.handle}, total={len(session.handle_store)}")
    print("  -> OK")


def test_session_world_store_attached() -> None:
    _header("session.world_store attached; world_model.json initialized")
    session = create_session(load_config())
    assert session.world_store is not None
    assert session.world_store.world.run_id == session.run_id
    assert session.world_store.path.exists()
    print(f"  path={session.world_store.path}")
    print("  -> OK")


def test_react_config_uses_memory_curator_by_default() -> None:
    _header("ReActConfig.use_memory_curator defaults to True")
    from evo.harness.react import ReActConfig
    assert ReActConfig().use_memory_curator is True
    print("  -> OK")


def test_model_gateway_propagates_context() -> None:
    _header("model_gateway propagates ContextVars across timeout pool")
    from contextvars import ContextVar
    from evo.runtime.config import ModelGovernanceConfig
    from evo.runtime.model_gateway import ModelGateway

    cv: ContextVar[str] = ContextVar("test_cv", default="default")
    seen: list[str] = []

    def producer() -> str:
        seen.append(cv.get())
        return "ok"

    cv.set("bound")
    cfg = ModelGovernanceConfig(
        rate_limit_per_sec=100.0, burst=10, max_retries=1,
        retry_base_seconds=0.0, on_failure="raise", producer_timeout_s=5.0,
    )
    gw: ModelGateway[str] = ModelGateway(cfg, name="ctx_test")
    out = gw.call(producer)
    assert out == "ok", out
    assert seen == ["bound"], (
        f"producer saw {seen!r}; ContextVar lost across _TIMEOUT_EXEC"
    )
    print("  ContextVar propagated to _TIMEOUT_EXEC worker")
    print("  -> OK")


def test_full_plan_runs_through_flow() -> None:
    _header("plan: load->features->cluster->flow runs end-to-end with events fanned out")
    cfg = load_config()
    session = create_session(cfg)
    seen: list[str] = []
    session.on("*", lambda ev: seen.append(ev.type))
    score_field = cfg.badcase_score_field
    plan = Plan([
        Step("load", lambda ctx: data_loader.load_corpus(ctx.session)),
        Step("features", lambda ctx: analysis_steps.compute_step_features(ctx.session)),
        Step("cluster_global", lambda ctx: analysis_steps.cluster_global(
            ctx.session, badcase_limit=50, score_field=score_field)),
        Step("cluster_per_step", lambda ctx: analysis_steps.cluster_per_step(
            ctx.session, badcase_limit=50, score_field=score_field)),
        Step("flow", lambda ctx: analysis_steps.analyze_flow(ctx.session),
             skip_if=lambda ctx: not ctx.session.has_stage("cluster_per_step")),
    ])
    with session_scope(session):
        result = plan.run(session)
    assert result.success, [o.error for o in result.failed]
    required = {"corpus.loaded", "features.ready", "clustering.global.ready",
                "clustering.per_step.ready", "stage.completed"}
    missing = required - set(seen)
    assert not missing, f"missing events: {missing}"
    assert len(analysis_steps.collect_exemplar_ids(session)) >= 0
    print(f"  events={sorted(set(seen))}")
    print("  -> OK")


# ---------------------------------------------------------------------------
# Driver
# ---------------------------------------------------------------------------

def _run(tests: list[Callable[[], None]]) -> int:
    failures = 0
    for t in tests:
        try:
            t()
        except Exception:
            failures += 1
            print(f"FAILED: {t.__name__}")
            traceback.print_exc(limit=5)
    print(f"\n{len(tests) - failures}/{len(tests)} passed")
    return 0 if failures == 0 else 1


def main() -> int:
    logging.basicConfig(
        level=logging.WARNING,
        format="%(levelname)s %(name)s: %(message)s",
    )
    return _run([
        test_domain_tool_result,
        test_registry_discovery,
        test_session_typed_accessors,
        test_session_state_isolation,
        test_model_gateway_is_session_scoped,
        test_tool_returns_typed_result,
        test_no_session_writes_from_cluster_tools,
        test_no_session_writes_from_io_tool,
        test_step_context_threads_inter_step_values,
        test_plan_runner_records_outcomes,
        test_standard_plan_definition_is_declarative,
        test_tool_middleware_hook,
        test_registry_lazy_autodiscovery,
        test_tools_have_llm_json_view,
        test_llm_provider_injection,
        test_llm_invoker_uses_gateway_and_reuses_provider_client,
        test_session_handle_store_attached_and_used,
        test_session_world_store_attached,
        test_react_config_uses_memory_curator_by_default,
        test_model_gateway_propagates_context,
        test_full_plan_runs_through_flow,
    ])


if __name__ == "__main__":
    sys.exit(main())
