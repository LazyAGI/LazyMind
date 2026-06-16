from __future__ import annotations

import asyncio
import json
import os
import shutil
import time
import uuid
from pathlib import Path
from typing import Any

from fastapi import Body, FastAPI, HTTPException, Request, Response
from sse_starlette.sse import EventSourceResponse

from evo import normalize_chat_stream_url, normalize_http_origin, validate_id
from evo.artifact_flow import EvoFlowRuntime, FlowStepState
from evo.artifact_runtime import ArtifactKey, ArtifactRef

BODY_REQUIRED = Body(...)
BODY_DEFAULT = Body(default_factory=dict)
RUN_ID = "run_1"
MAX_CREATE_THREAD_CASES = 1000
MAX_CREATE_THREAD_WORKERS = 32
RESULT_ARTIFACTS = {
    "datasets": ("eval.dataset",),
    "eval-reports": ("eval.summary", "abtest.candidate_eval_summary"),
    "analysis-reports": ("analysis.summary",),
    "abtests": ("abtest.comparison",),
    "diffs": ("repair.verified_patch",),
}
STAGE_LABELS = {
    "dataset": "数据集生成",
    "eval": "评测",
    "analysis": "分析",
    "repair": "修复",
    "abtest": "ABTest",
}


def create_app() -> FastAPI:
    hub = EvoMessageHub(Path(os.getenv("LAZYMIND_EVO_BASE_DIR") or "/var/lib/lazymind/evo"))
    app = FastAPI(title="evo flow service", version="artifact-runtime")
    app.state.hub = hub

    @app.get("/healthz")
    def healthz() -> dict:
        return {"ok": True, "service": "evo-flow"}

    @app.get("/livez")
    def livez() -> dict:
        return {"alive": True}

    @app.get("/readyz")
    def readyz() -> dict:
        return {"ready": True}

    @app.post("/v1/evo/threads")
    async def create_thread(body: dict = BODY_REQUIRED) -> dict:
        return await asyncio.to_thread(hub.create_thread, body)

    @app.get("/v1/evo/threads")
    def list_threads() -> list[dict]:
        return hub.list_threads()

    @app.get("/v1/evo/threads/statuses")
    def list_thread_statuses() -> dict:
        rows = [
            hub.flow_status(meta["id"]) | {
                "title": meta.get("title", ""),
                "mode": meta.get("mode", "interactive"),
                "created_at": meta.get("created_at"),
                "updated_at": meta.get("updated_at"),
            }
            for meta in hub.list_threads()
        ]
        counts: dict[str, int] = {}
        for row in rows:
            counts[row["status"]] = counts.get(row["status"], 0) + 1
        return {"total": len(rows), "counts": counts, "threads": rows}

    @app.get("/v1/evo/threads/{thread_id}")
    def get_thread(thread_id: str) -> dict:
        return hub.get_thread(thread_id)

    @app.delete("/v1/evo/threads/{thread_id}")
    def delete_thread(thread_id: str) -> dict:
        return hub.delete_thread(thread_id)

    @app.get("/v1/evo/threads/{thread_id}/history")
    def history(thread_id: str) -> dict:
        return hub.history(thread_id)

    @app.get("/v1/evo/threads/{thread_id}/flow-status")
    def flow_status(thread_id: str) -> dict:
        return hub.flow_status(thread_id)

    @app.post("/v1/evo/threads/{thread_id}:messages")
    @app.post("/v1/evo/threads/{thread_id}/messages")
    async def post_message(thread_id: str, request: Request, body: dict = BODY_REQUIRED):
        if "text/event-stream" in request.headers.get("accept", ""):
            hub.reject_message(thread_id, body)
        return await asyncio.to_thread(hub.post_message, thread_id, body)

    @app.post("/v1/evo/threads/{thread_id}/start")
    async def start(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.start, thread_id, body)

    @app.post("/v1/evo/threads/{thread_id}/pause")
    async def pause(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.pause, thread_id)

    @app.post("/v1/evo/threads/{thread_id}/cancel")
    async def cancel(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.cancel, thread_id)

    @app.post("/v1/evo/threads/{thread_id}/retry")
    async def retry(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.retry, thread_id, body)

    @app.post("/v1/evo/threads/{thread_id}/continue")
    async def continue_thread(thread_id: str, body: dict = BODY_DEFAULT) -> dict:
        return await asyncio.to_thread(hub.continue_thread, thread_id, body)

    @app.post("/v1/evo/threads/{thread_id}/auto/step")
    async def auto_step(thread_id: str) -> dict:
        return await asyncio.to_thread(hub.start, thread_id, {})

    @app.post("/v1/evo/threads/{thread_id}/auto/start")
    async def auto_start(thread_id: str, request: Request, body: dict = BODY_DEFAULT):
        if "text/event-stream" in request.headers.get("accept", ""):
            return EventSourceResponse(_single_sse("auto_start", {"thread_id": thread_id, **hub.start(thread_id, body)}))
        return await asyncio.to_thread(hub.start, thread_id, body)

    @app.post("/v1/evo/threads/{thread_id}/auto/stop")
    def auto_stop(thread_id: str) -> dict:
        return hub.pause(thread_id)

    @app.get("/v1/evo/threads/{thread_id}:events")
    @app.get("/v1/evo/threads/{thread_id}/events")
    def events(thread_id: str, request: Request, since: int = 0) -> EventSourceResponse:
        hub.get_thread(thread_id)
        last = request.headers.get("last-event-id") or ""
        return EventSourceResponse(hub.events(thread_id, int(last) if last.isdigit() else since))

    @app.get("/v1/evo/threads/{thread_id}/results/{kind}")
    def results(thread_id: str, kind: str) -> list[dict]:
        return hub.results(thread_id, kind)

    @app.get("/v1/evo/threads/{thread_id}/artifacts/{artifact_id}")
    def artifact(thread_id: str, artifact_id: str) -> dict:
        return hub.artifact(thread_id, artifact_id)

    @app.get("/v1/evo/threads/{thread_id}/reports/{report_id}/content")
    def thread_report_content(thread_id: str, report_id: str, fmt: str = ""):
        content = hub.report_content(thread_id, report_id)
        if fmt in {"md", "markdown", "text"}:
            return Response(content, media_type="text/markdown; charset=utf-8")
        return {"thread_id": thread_id, "report_id": report_id, "content": content}

    @app.get("/v1/evo/reports/{report_id}/content")
    def report_content(report_id: str, fmt: str = ""):
        thread_id, artifact_id = _scoped_report_id(report_id)
        content = hub.report_content(thread_id, artifact_id)
        if fmt in {"md", "markdown", "text"}:
            return Response(content, media_type="text/markdown; charset=utf-8")
        return {"thread_id": thread_id, "report_id": artifact_id, "content": content}

    @app.get("/v1/evo/diffs/{apply_id}/{filename:path}")
    def diff_content(apply_id: str, filename: str) -> Response:
        return Response(hub.diff_content(apply_id, filename), media_type="text/x-diff; charset=utf-8")

    return app


def get_app() -> FastAPI:
    return create_app()


class EvoMessageHub:
    def __init__(self, base_dir: Path):
        self.base_dir = base_dir
        self.threads_dir = base_dir / "state" / "threads"
        self._artifact_flows: dict[str, EvoFlowRuntime] = {}

    def create_thread(self, payload: dict[str, Any]) -> dict:
        mode = str(payload.get("mode") or "interactive").strip()
        if mode not in {"auto", "interactive"}:
            raise HTTPException(400, f"bad mode {mode!r}")
        try:
            inputs = _normalize_inputs(dict(payload.get("inputs") or {}))
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        thread_id, now = f"thr-{uuid.uuid4().hex[:8]}", time.time()
        meta = {
            "id": thread_id,
            "thread_id": thread_id,
            "mode": mode,
            "title": str(payload.get("title") or ""),
            "inputs": inputs,
            "model_config": payload.get("llm_config") or {},
            "status": "idle",
            "created_at": now,
            "updated_at": now,
        }
        self._write_meta(thread_id, meta)
        if mode == "auto" and payload.get("start_auto"):
            self.start(thread_id, payload)
        return self._meta(thread_id)

    def list_threads(self) -> list[dict]:
        rows = [_read_json(path) for path in self.threads_dir.glob("*/thread.json")]
        return sorted([row for row in rows if row], key=lambda row: row.get("updated_at") or 0, reverse=True)

    def get_thread(self, thread_id: str) -> dict:
        return self._meta(thread_id)

    def delete_thread(self, thread_id: str) -> dict:
        self._meta(thread_id)
        self._close_flow(thread_id)
        run_root, thread_dir = self._run_root(thread_id), self._thread_dir(thread_id)
        run_deleted, thread_deleted = run_root.exists(), thread_dir.exists()
        shutil.rmtree(run_root, ignore_errors=True)
        shutil.rmtree(thread_dir, ignore_errors=True)
        return {
            "thread_id": thread_id,
            "deleted_run": run_deleted,
            "deleted_thread": thread_deleted,
            "cancelled": False,
        }

    def history(self, thread_id: str) -> dict:
        self._meta(thread_id)
        return {"thread_id": thread_id, "messages": _read_messages(self._thread_dir(thread_id) / "messages.jsonl")}

    def start(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self._meta(thread_id)
        flow = self._artifact_flow(thread_id)
        state = flow.start_full_flow(
            command_id=str(payload.get("command_id") or f"start:{thread_id}"),
            run_id=RUN_ID,
            config=self._artifact_flow_config(thread_id),
        )
        return self._artifact_flow_response(thread_id, state)

    def pause(self, thread_id: str) -> dict:
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            self._update_meta(thread_id, status="paused", pending_checkpoint=None, updated_at=time.time())
            return {"status": "paused", "thread_id": thread_id}
        state = self._artifact_flow(thread_id).pause_flow(command_id=f"pause:{uuid.uuid4().hex}", run_id=RUN_ID)
        response = self._artifact_flow_response(thread_id, state)
        return response | {"status": "paused", "pending_checkpoint": None}

    def cancel(self, thread_id: str) -> dict:
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            self._update_meta(thread_id, status="cancelled", pending_checkpoint=None, updated_at=time.time())
            return {"status": "cancelled", "thread_id": thread_id}
        state = self._artifact_flow(thread_id).cancel_flow(command_id=f"cancel:{uuid.uuid4().hex}", run_id=RUN_ID)
        return self._artifact_flow_response(thread_id, state)

    def retry(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            raise HTTPException(409, "thread has no flow to retry")
        flow = self._artifact_flow(thread_id)
        flow.runtime.controller.retry_failed(
            RUN_ID,
            command_id=str((payload or {}).get("command_id") or f"retry:{uuid.uuid4().hex}"),
        )
        flow.run_until_idle(command_id=f"retry-idle:{uuid.uuid4().hex}", run_id=RUN_ID)
        state = flow.step_store.get(RUN_ID)
        if state is None:
            raise HTTPException(409, "thread has no flow to retry")
        return self._artifact_flow_response(thread_id, state) | {"retried": True}

    def continue_thread(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            raise HTTPException(409, "thread has no flow to continue")
        state = self._artifact_flow(thread_id).continue_flow(
            command_id=str(payload.get("command_id") or f"continue:{uuid.uuid4().hex}"),
            run_id=RUN_ID,
        )
        return self._artifact_flow_response(thread_id, state) | {"resumed": True}

    def post_message(self, thread_id: str, payload: dict[str, Any]) -> dict:
        self.reject_message(thread_id, payload)

    def reject_message(self, thread_id: str, payload: dict[str, Any]) -> None:
        self._meta(thread_id)
        content = str(payload.get("content") or payload.get("message") or "").strip()
        if not content:
            raise HTTPException(400, "message content required")
        raise HTTPException(409, "message/NL intervention is not migrated for artifact flow")

    async def post_message_stream(self, thread_id: str, payload: dict[str, Any]):
        message_id = str(payload.get("message_id") or f"msg_{thread_id}_{uuid.uuid4().hex[:8]}")

        def emit(event: str, data: dict[str, Any]) -> dict:
            return _sse(event, {"thread_id": thread_id, "message_id": message_id, **data})

        yield emit("intent_start", {})
        try:
            self.post_message(thread_id, {**payload, "message_id": message_id})
        except HTTPException as exc:
            yield emit("error", {"code": exc.status_code, "message": exc.detail})

    async def events(self, thread_id: str, since: int = 0):
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            return
        cursor, idle_ticks = max(0, since), 0
        flow = self._artifact_flow(thread_id)
        while True:
            events = flow.runtime.controller.event_log.scan_since(cursor, limit=100)
            for event in events:
                cursor = max(cursor, event.seq)
                yield _sse("message", {
                    "seq": event.seq,
                    "event_id": f"artifact:{event.seq}",
                    "event_type": event.event_type,
                    "run_id": event.run_id,
                    "payload": event.payload,
                }, str(event.seq))
            status = self.flow_status(thread_id)["status"]
            if status in {"ended", "failed", "cancelled"} and not events:
                yield _sse("done", {"thread_id": thread_id, "status": status}, str(cursor + 1))
                return
            idle_ticks = idle_ticks + 1 if not events else 0
            if status in {"idle", "paused", "waiting_checkpoint"} and idle_ticks > 4:
                return
            await asyncio.sleep(0.5)

    def flow_status(self, thread_id: str) -> dict:
        meta = self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            return _flow_status_row(thread_id, str(meta.get("status") or "idle"), [])
        flow = self._artifact_flow(thread_id)
        state = flow.step_store.get(RUN_ID)
        status = _artifact_flow_http_status(state)
        return _flow_status_row(
            thread_id,
            status,
            [],
            latest_abtest_status=_abtest_status(flow),
            report_ready=self._artifact_runtime_row(thread_id, "eval.summary") is not None,
            pending_checkpoint=_artifact_checkpoint_payload(state),
        ) | {
            "current_step": "" if state is None else state.current_step,
            "completed_steps": [] if state is None else list(state.completed_steps),
            "stale_steps": [] if state is None else list(state.stale_steps),
        }

    def results(self, thread_id: str, kind: str) -> list[dict]:
        self._meta(thread_id)
        if kind not in RESULT_ARTIFACTS:
            raise HTTPException(404, f"unknown result kind: {kind}")
        return [row for artifact_id in RESULT_ARTIFACTS[kind] if (row := self._artifact_runtime_row(thread_id, artifact_id))]

    def artifact(self, thread_id: str, artifact_id: str) -> dict:
        row = self._artifact_runtime_row(thread_id, artifact_id)
        if row is None:
            raise HTTPException(404, f"artifact not found: {artifact_id}")
        return row

    def report_content(self, thread_id: str, artifact_id: str) -> str:
        data = self.artifact(thread_id, artifact_id)["data"]
        if isinstance(data, dict):
            for key in ("markdown", "report", "content", "text", "summary"):
                value = data.get(key)
                if isinstance(value, str) and value.strip():
                    return value
        return json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str)

    def diff_content(self, apply_id: str, filename: str) -> str:
        del filename
        thread_id, artifact_id = _scoped_report_id(apply_id) if ":" in apply_id else self._find_artifact(apply_id)
        data = self.artifact(thread_id, artifact_id)["data"]
        if isinstance(data, dict):
            for key in ("diff", "patch", "content"):
                value = data.get(key)
                if isinstance(value, str) and value.strip():
                    return value
            if data.get("status") in {"skipped", "skipped_no_bad_case"}:
                return "No code changes were produced for this repair step.\n"
        raise HTTPException(404, f"diff content not found: {apply_id}")

    def _artifact_flow(self, thread_id: str) -> EvoFlowRuntime:
        if thread_id not in self._artifact_flows:
            inputs = self._artifact_flow_config(thread_id)
            path = self._artifact_runtime_path(thread_id)
            path.parent.mkdir(parents=True, exist_ok=True)
            self._artifact_flows[thread_id] = EvoFlowRuntime.open(path, case_count=int(inputs["num_cases"]))
        return self._artifact_flows[thread_id]

    def _close_flow(self, thread_id: str) -> None:
        flow = self._artifact_flows.pop(thread_id, None)
        if flow is not None:
            flow.close()

    def _has_artifact_flow(self, thread_id: str) -> bool:
        return thread_id in self._artifact_flows or self._artifact_runtime_path(thread_id).exists()

    def _artifact_runtime_row(self, thread_id: str, artifact_id: str) -> dict | None:
        self._meta(thread_id)
        if not self._has_artifact_flow(thread_id):
            return None
        flow = self._artifact_flow(thread_id)
        key, requested_version = _artifact_selector(artifact_id)
        ref = ArtifactRef(key, requested_version) if requested_version is not None else flow.runtime.stores.artifact_store.latest(key)
        if ref is None:
            return None
        record = flow.runtime.stores.artifact_store.get(ref)
        if record is None:
            return None
        return {
            "artifact_id": key.artifact_id,
            "partition": key.partition,
            "ref": str(ref),
            "schema": record.value.schema,
            "data": record.value.payload,
        }

    def _find_artifact(self, artifact_id: str) -> tuple[str, str]:
        for meta in self.list_threads():
            thread_id = str(meta.get("id") or "")
            if thread_id and self._artifact_runtime_row(thread_id, artifact_id) is not None:
                return thread_id, artifact_id
        raise HTTPException(404, f"artifact not found: {artifact_id}")

    def _artifact_flow_config(self, thread_id: str) -> dict[str, Any]:
        meta = self._meta(thread_id)
        raw_inputs = dict(meta.get("inputs") or {})
        try:
            inputs = _normalize_inputs(raw_inputs)
        except ValueError as exc:
            raise HTTPException(400, str(exc)) from exc
        if inputs != raw_inputs:
            self._update_meta(thread_id, inputs=inputs, updated_at=time.time())
        return inputs | {"model_config": meta.get("model_config") or {}}

    def _artifact_flow_response(self, thread_id: str, state: FlowStepState) -> dict:
        status = _artifact_flow_http_status(state)
        checkpoint = _artifact_checkpoint_payload(state)
        self._update_meta(thread_id, status=status, pending_checkpoint=checkpoint, updated_at=time.time())
        return {
            "status": status,
            "thread_id": thread_id,
            "run_id": state.run_id,
            "current_step": state.current_step,
            "completed_steps": list(state.completed_steps),
            "stale_steps": list(state.stale_steps),
            "gate_status": state.gate_status,
            "gate_artifact_ref": "" if state.gate_artifact_ref is None else str(state.gate_artifact_ref),
            "pending_checkpoint": checkpoint,
        }

    def _thread_dir(self, thread_id: str) -> Path:
        return self.threads_dir / thread_id

    def _run_root(self, thread_id: str) -> Path:
        return self.base_dir / "dev-runs" / thread_id

    def _artifact_runtime_path(self, thread_id: str) -> Path:
        return self._run_root(thread_id) / "artifact-runtime.sqlite"

    def _meta(self, thread_id: str) -> dict:
        meta = _read_json(self._thread_dir(thread_id) / "thread.json")
        if not meta:
            raise HTTPException(404, f"thread {thread_id} not found")
        return meta

    def _write_meta(self, thread_id: str, meta: dict) -> None:
        _write_json(self._thread_dir(thread_id) / "thread.json", meta)

    def _update_meta(self, thread_id: str, **patch: Any) -> None:
        meta = self._meta(thread_id)
        meta.update(patch)
        self._write_meta(thread_id, meta)


def _single_sse(event: str, payload: dict[str, Any]):
    async def gen():
        yield _sse(event, payload)

    return gen()


def _sse(event: str, payload: dict[str, Any], event_id: str | None = None) -> dict:
    row = {"event": event, "data": json.dumps({"type": event, **payload}, ensure_ascii=False, default=str)}
    if event_id:
        row["id"] = event_id
    return row


def _flow_status_row(
    thread_id: str,
    status: str,
    active_task_ids: list[str],
    *,
    latest_abtest_status: str | None = None,
    report_ready: bool = False,
    pending_checkpoint: dict | None = None,
) -> dict:
    return {
        "thread_id": thread_id,
        "status": status,
        "active_task_ids": active_task_ids,
        "latest_abtest_id": "abtest.comparison" if latest_abtest_status else None,
        "latest_abtest_status": latest_abtest_status,
        "report_ready": report_ready,
        "pending_checkpoint": pending_checkpoint,
    }


def _artifact_flow_http_status(state: FlowStepState | None) -> str:
    if state is None:
        return "idle"
    if state.gate_status == "completed":
        return "ended"
    if state.gate_status == "cancelled":
        return "cancelled"
    if state.gate_status in {"paused", "stale"}:
        return "waiting_checkpoint"
    return "running"


def _artifact_checkpoint_payload(state: FlowStepState | None) -> dict | None:
    if state is None or state.gate_status not in {"paused", "stale"}:
        return None
    return {
        "checkpoint_id": f"artifact_gate:{state.current_step}",
        "checkpoint_kind": "stage_gate",
        "stage": state.current_step,
        "next_stage": state.next_step or "",
        "message": f'{STAGE_LABELS.get(str(state.current_step), state.current_step)}已完成，请确认是否继续执行下一步。',
        "gate_artifact_ref": "" if state.gate_artifact_ref is None else str(state.gate_artifact_ref),
    }


def _abtest_status(flow: EvoFlowRuntime) -> str | None:
    ref = flow.runtime.stores.artifact_store.latest(ArtifactKey.of("abtest.comparison"))
    if ref is None:
        return None
    record = flow.runtime.stores.artifact_store.get(ref)
    if record is None or not isinstance(record.value.payload, dict):
        return "completed"
    return str(record.value.payload.get("status") or "completed")


def _artifact_selector(value: str) -> tuple[ArtifactKey, int | None]:
    text = value.strip()
    if not text:
        raise HTTPException(400, "artifact id required")
    version = None
    if "@v" in text:
        text, raw_version = text.rsplit("@v", 1)
        try:
            version = int(raw_version)
        except ValueError as exc:
            raise HTTPException(400, f"bad artifact version: {value}") from exc
    partition = ""
    if text.endswith("]") and "[" in text:
        text, partition = text[:-1].split("[", 1)
    return ArtifactKey(text, partition), version


def _scoped_report_id(value: str) -> tuple[str, str]:
    text = str(value or "").strip()
    if ":" not in text:
        raise HTTPException(400, "global report content requires scoped id: {thread_id}:{artifact_ref}")
    thread_id, artifact_id = (part.strip() for part in text.split(":", 1))
    if not thread_id or not artifact_id:
        raise HTTPException(400, "global report content requires scoped id: {thread_id}:{artifact_ref}")
    return thread_id, artifact_id


def _normalize_inputs(inputs: dict[str, Any]) -> dict[str, Any]:
    normalized = dict(inputs)
    dataset_id = _dataset_id(normalized)
    normalized["kb_id"] = normalized["dataset_id"] = dataset_id
    if "dataset_name" in normalized:
        normalized["dataset_name"] = dataset_id
    normalized["target_chat_url"] = _chat_url(normalized.get("target_chat_url"))
    normalized["candidate_chat_url"] = _optional_chat_url(normalized.get("candidate_chat_url"))
    if normalized["candidate_chat_url"] and normalized["candidate_chat_url"] == normalized["target_chat_url"]:
        raise ValueError("candidate_chat_url must differ from target_chat_url")
    normalized["router_admin_url"] = _admin_url(normalized.get("router_admin_url"))
    normalized["num_cases"] = _bounded_positive_int(_case_count_value(normalized), "num_cases", MAX_CREATE_THREAD_CASES)
    normalized.pop("case_count", None)
    max_workers = inputs["max_workers"] if "max_workers" in inputs else os.getenv("EVO_FLOW_WORKERS", "2")
    normalized["max_workers"] = _bounded_positive_int(max_workers, "max_workers", MAX_CREATE_THREAD_WORKERS)
    return normalized


def _dataset_id(inputs: dict[str, Any]) -> str:
    ids = {str(inputs.get(key) or "").strip() for key in ("kb_id", "dataset_id") if str(inputs.get(key) or "").strip()}
    if len(ids) > 1:
        raise ValueError("dataset id aliases must match")
    if ids:
        return validate_id(ids.pop(), "dataset_id")
    legacy = str(inputs.get("dataset_name") or "").strip()
    return validate_id(legacy, "dataset_id") if legacy else "algo"


def _chat_url(value: Any) -> str:
    url = str(value or os.getenv("LAZYMIND_EVO_TARGET_CHAT_URL") or "http://chat:8046/api/chat/stream").strip()
    return normalize_chat_stream_url(url.replace("http://evo-chat:", "http://chat:"), "target_chat_url")


def _optional_chat_url(value: Any) -> str:
    url = str(value or "").strip()
    return normalize_chat_stream_url(url, "candidate_chat_url") if url else ""


def _admin_url(value: Any) -> str:
    url = str(value or os.getenv("LAZYMIND_EVO_ROUTER_ADMIN_URL") or "").strip()
    return normalize_http_origin(url, "router_admin_url") if url else ""


def _case_count_value(inputs: dict[str, Any]) -> Any:
    values = [inputs[key] for key in ("num_cases", "case_count") if key in inputs]
    if len(values) == 2 and str(values[0]) != str(values[1]):
        raise ValueError("num_cases and case_count must match")
    return values[0] if values else os.getenv("EVO_FLOW_CASE_COUNT", "20")


def _bounded_positive_int(value: Any, field: str, maximum: int) -> int:
    try:
        out = int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"{field} must be a positive integer") from exc
    if out < 1:
        raise ValueError(f"{field} must be a positive integer")
    if out > maximum:
        raise ValueError(f"{field} must be <= {maximum}")
    return out


def _read_messages(path: Path) -> list[dict]:
    if not path.exists():
        return []
    rows = []
    for index, line in enumerate(path.read_text(encoding="utf-8").splitlines()):
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        if row.get("role") in {"user", "assistant"} and row.get("content"):
            rows.append({"id": f"msg-{index + 1}", "role": row["role"], "content": row["content"], "ts": row.get("ts")})
    return rows


def _read_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}


def _write_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(f".{os.getpid()}.{time.time_ns()}.tmp")
    tmp.write_text(json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True, default=str), encoding="utf-8")
    tmp.replace(path)
