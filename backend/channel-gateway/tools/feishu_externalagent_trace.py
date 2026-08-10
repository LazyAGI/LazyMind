#!/usr/bin/env python3
"""Trace the live Core/Codex read path used by the Feishu assistant.

This is an integration oracle, not a mock test. Run it where the Core service is
reachable (normally inside the channel-gateway container). It records request
IDs, timings, exact cwd filtering, and the native thread detail returned by the
running Codex app-server.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--core-url", default="http://core:8000")
    parser.add_argument("--actor-user-id", required=True)
    parser.add_argument("--cwd", default="", help="Exact project cwd to inspect")
    parser.add_argument("--project-limit", type=int, default=6)
    parser.add_argument("--thread-limit", type=int, default=4)
    parser.add_argument("--turn-limit", type=int, default=3)
    parser.add_argument("--expect-min-projects", type=int, default=1)
    parser.add_argument("--expect-min-threads", type=int, default=1)
    parser.add_argument(
        "--expect-hidden-thread-id",
        default="",
        help="Thread bound to another actor; its detail route must return 404",
    )
    parser.add_argument(
        "--hidden-owner-user-id",
        default="",
        help="Actual owner used to prove the hidden thread exists before the 404 check",
    )
    parser.add_argument("--output", type=Path)
    return parser.parse_args()


def request_json(
    base_url: str,
    path: str,
    actor_user_id: str,
    request_id: str,
    params: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], int]:
    query = urllib.parse.urlencode(params or {})
    url = f"{base_url.rstrip('/')}{path}"
    if query:
        url = f"{url}?{query}"
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/json",
            "X-User-Id": actor_user_id,
            "X-User-Name": actor_user_id,
            "X-Request-Id": request_id,
        },
    )
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            body = response.read()
    except urllib.error.HTTPError as exc:
        raise RuntimeError(
            f"GET {path} failed with HTTP {exc.code}: {exc.read().decode('utf-8', 'replace')}"
        ) from exc
    elapsed_ms = round((time.monotonic() - started) * 1000)
    payload = json.loads(body)
    if not isinstance(payload, dict) or payload.get("code") != 0:
        raise RuntimeError(f"GET {path} returned an invalid Core response: {payload!r}")
    data = payload.get("data")
    if not isinstance(data, dict):
        raise RuntimeError(f"GET {path} returned non-object data")
    return data, elapsed_ms


def request_status(
    base_url: str,
    path: str,
    actor_user_id: str,
    request_id: str,
    params: dict[str, Any] | None = None,
) -> int:
    query = urllib.parse.urlencode(params or {})
    url = f"{base_url.rstrip('/')}{path}"
    if query:
        url = f"{url}?{query}"
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/json",
            "X-User-Id": actor_user_id,
            "X-User-Name": actor_user_id,
            "X-Request-Id": request_id,
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.status
    except urllib.error.HTTPError as exc:
        return exc.code


def trace_live_read_path(args: argparse.Namespace) -> dict[str, Any]:
    trace_id = f"feishu-ext-{uuid.uuid4().hex}"
    request_ids: dict[str, str] = {}

    def call(
        name: str,
        path: str,
        params: dict[str, Any] | None = None,
    ) -> tuple[dict[str, Any], int]:
        request_id = f"{trace_id}-{name}"
        request_ids[name] = request_id
        return request_json(
            args.core_url,
            path,
            args.actor_user_id,
            request_id,
            params,
        )

    projects: list[dict[str, Any]] = []
    project_pages: list[dict[str, Any]] = []
    project_timings: list[int] = []
    seen_project_cursors: set[str] = set()
    project_cursor = ""
    project_total: int | None = None
    while True:
        page_index = len(project_pages) + 1
        params: dict[str, Any] = {"limit": args.project_limit}
        if project_cursor:
            params["cursor"] = project_cursor
        projects_payload, projects_ms = call(
            f"projects_page_{page_index}",
            "/external-agents/codex/projects",
            params,
        )
        page_projects = projects_payload.get("data")
        if not isinstance(page_projects, list):
            raise RuntimeError("project projection is missing data[]")
        total = int(projects_payload.get("total") or 0)
        if project_total is None:
            project_total = total
        elif project_total != total:
            raise AssertionError("project total changed during pagination")
        projects.extend(
            item for item in page_projects if isinstance(item, dict)
        )
        next_cursor = str(projects_payload.get("nextCursor") or "")
        project_pages.append({
            "cursor": project_cursor,
            "next_cursor": next_cursor,
            "count": len(page_projects),
            "total": total,
        })
        project_timings.append(projects_ms)
        if not next_cursor:
            break
        if next_cursor in seen_project_cursors:
            raise AssertionError("project pagination repeated a cursor")
        seen_project_cursors.add(next_cursor)
        project_cursor = next_cursor
    if len(projects) != project_total:
        raise AssertionError(
            f"project pagination returned {len(projects)} of {project_total}"
        )
    if len(projects) < args.expect_min_projects:
        raise AssertionError(
            f"expected at least {args.expect_min_projects} projects, got {len(projects)}"
        )

    selected_cwd = args.cwd.strip()
    if not selected_cwd and projects:
        selected_cwd = str(projects[0].get("cwd") or "").strip()
    if not selected_cwd:
        raise AssertionError("no project cwd is available for the thread check")

    threads: list[dict[str, Any]] = []
    thread_pages: list[dict[str, Any]] = []
    thread_timings: list[int] = []
    seen_thread_ids: set[str] = set()
    seen_cursors: set[str] = set()
    cursor = ""
    thread_total: int | None = None
    for page_number in range(1, 101):
        params: dict[str, Any] = {
            "cwd": selected_cwd,
            "limit": args.thread_limit,
        }
        if cursor:
            params["cursor"] = cursor
        threads_payload, elapsed_ms = call(
            f"threads_page_{page_number}",
            "/external-agents/codex/threads",
            params,
        )
        page_threads = threads_payload.get("data")
        if not isinstance(page_threads, list):
            raise RuntimeError("thread projection is missing data[]")
        page_total = int(threads_payload.get("total") or 0)
        if thread_total is None:
            thread_total = page_total
        elif page_total != thread_total:
            raise AssertionError(
                f"thread total changed during trace: {thread_total} -> {page_total}"
            )
        page_ids = [str(item.get("id") or "").strip() for item in page_threads]
        duplicates = [item for item in page_ids if item in seen_thread_ids]
        if duplicates:
            raise AssertionError(f"thread pagination repeated ids: {duplicates}")
        seen_thread_ids.update(page_ids)
        threads.extend(page_threads)
        next_cursor = str(threads_payload.get("nextCursor") or "").strip()
        has_more = bool(threads_payload.get("has_more"))
        thread_pages.append({
            "page": page_number,
            "cursor": cursor or None,
            "next_cursor": next_cursor or None,
            "has_more": has_more,
            "count": len(page_threads),
            "ids": page_ids,
        })
        thread_timings.append(elapsed_ms)
        if not has_more:
            if next_cursor:
                raise AssertionError("terminal thread page returned a next cursor")
            break
        if not next_cursor or next_cursor in seen_cursors:
            raise AssertionError(f"invalid/repeated next cursor: {next_cursor!r}")
        seen_cursors.add(next_cursor)
        cursor = next_cursor
    else:
        raise AssertionError("thread pagination exceeded 100 pages")
    if len(threads) < args.expect_min_threads:
        raise AssertionError(
            f"expected at least {args.expect_min_threads} threads for {selected_cwd!r}, "
            f"got {len(threads)}"
        )
    mismatched = [item.get("id") for item in threads if item.get("cwd") != selected_cwd]
    if mismatched:
        raise AssertionError(
            f"exact cwd filtering leaked {len(mismatched)} threads: {mismatched}"
        )
    projected_project = next(
        (item for item in projects if item.get("cwd") == selected_cwd), None
    )
    if projected_project is None:
        raise AssertionError(f"selected cwd {selected_cwd!r} is missing from projects")
    if thread_total != len(threads):
        raise AssertionError(
            f"thread total mismatch: total={thread_total}, collected={len(threads)}"
        )
    if projected_project.get("thread_count") != len(threads):
        raise AssertionError(
            f"project/session count mismatch for {selected_cwd!r}: "
            f"project={projected_project.get('thread_count')}, sessions={len(threads)}"
        )

    thread_id = str(threads[0].get("id") or "").strip()
    if not thread_id:
        raise RuntimeError("first projected thread has no id")
    detail_payload, detail_ms = call(
        "detail",
        f"/external-agents/codex/threads/{urllib.parse.quote(thread_id, safe='')}",
        {"limit": args.turn_limit},
    )
    detail_thread = detail_payload.get("thread")
    if not isinstance(detail_thread, dict) or detail_thread.get("id") != thread_id:
        raise AssertionError("thread detail does not match the selected native thread")

    hidden_thread_id = args.expect_hidden_thread_id.strip()
    hidden_status = None
    hidden_owner_status = None
    hidden_owner_detail: dict[str, Any] | None = None
    hidden_owner_ms = None
    if hidden_thread_id:
        hidden_owner_user_id = args.hidden_owner_user_id.strip()
        if not hidden_owner_user_id:
            raise AssertionError(
                "--hidden-owner-user-id is required with --expect-hidden-thread-id"
            )
        hidden_owner_request_id = f"{trace_id}-hidden-owner-detail"
        request_ids["hidden_owner_detail"] = hidden_owner_request_id
        hidden_owner_payload, hidden_owner_ms = request_json(
            args.core_url,
            f"/external-agents/codex/threads/{urllib.parse.quote(hidden_thread_id, safe='')}",
            hidden_owner_user_id,
            hidden_owner_request_id,
            {"limit": args.turn_limit},
        )
        hidden_owner_status = 200
        owner_thread = hidden_owner_payload.get("thread")
        if not isinstance(owner_thread, dict) or owner_thread.get("id") != hidden_thread_id:
            raise AssertionError(
                "hidden thread owner detail does not match the requested native thread"
            )
        if owner_thread.get("cwd") != selected_cwd:
            raise AssertionError(
                "hidden thread owner detail is not part of the selected exact cwd"
            )
        hidden_owner_detail = {
            "thread_id": owner_thread.get("id"),
            "cwd": owner_thread.get("cwd"),
            "conversation_id": owner_thread.get("conversation_id"),
            "total_turns": hidden_owner_payload.get("total_turns"),
            "returned_turns": len(hidden_owner_payload.get("turns") or []),
        }
        hidden_request_id = f"{trace_id}-hidden-detail"
        request_ids["hidden_detail"] = hidden_request_id
        hidden_status = request_status(
            args.core_url,
            f"/external-agents/codex/threads/{urllib.parse.quote(hidden_thread_id, safe='')}",
            args.actor_user_id,
            hidden_request_id,
        )
        if hidden_status != 404:
            raise AssertionError(
                f"hidden thread detail returned HTTP {hidden_status}, want 404"
            )

    invalid_cursor_request_id = f"{trace_id}-invalid-cursor"
    request_ids["invalid_cursor"] = invalid_cursor_request_id
    invalid_cursor_status = request_status(
        args.core_url,
        "/external-agents/codex/threads",
        args.actor_user_id,
        invalid_cursor_request_id,
        {"cwd": selected_cwd, "cursor": "not-an-offset", "limit": args.thread_limit},
    )
    if invalid_cursor_status != 400:
        raise AssertionError(
            f"invalid thread cursor returned HTTP {invalid_cursor_status}, want 400"
        )

    return {
        "trace_id": trace_id,
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "core_url": args.core_url,
        "actor_user_id": args.actor_user_id,
        "request_ids": request_ids,
        "timings_ms": {
            "project_pages": project_timings,
            "thread_pages": thread_timings,
            "detail": detail_ms,
            "hidden_owner_detail": hidden_owner_ms,
        },
        "projects": {
            "count": len(projects),
            "total": project_total,
            "items": projects,
            "pages": project_pages,
        },
        "threads": {
            "cwd": selected_cwd,
            "count": len(threads),
            "total": thread_total,
            "ids": [item.get("id") for item in threads],
            "pages": thread_pages,
            "invalid_cursor_status": invalid_cursor_status,
        },
        "detail": {
            "thread_id": thread_id,
            "conversation_id": detail_thread.get("conversation_id"),
            "available": detail_thread.get("available"),
            "controlled_by_lazymind": detail_thread.get("controlled_by_lazymind"),
            "total_turns": detail_payload.get("total_turns"),
            "returned_turns": len(detail_payload.get("turns") or []),
        },
        "actor_isolation": {
            "hidden_thread_id": hidden_thread_id or None,
            "hidden_owner_user_id": args.hidden_owner_user_id.strip() or None,
            "hidden_owner_detail_status": hidden_owner_status,
            "hidden_owner_detail": hidden_owner_detail,
            "hidden_detail_status": hidden_status,
        },
    }


def main() -> int:
    args = parse_args()
    try:
        trace = trace_live_read_path(args)
    except (AssertionError, RuntimeError, ValueError) as exc:
        print(f"live trace failed: {exc}", file=sys.stderr)
        return 1
    rendered = json.dumps(trace, ensure_ascii=False, indent=2) + "\n"
    if args.output:
        args.output.write_text(rendered, encoding="utf-8")
    else:
        print(rendered, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
