#!/usr/bin/env python3
"""Manual acceptance aid. Never refreshes credentials or acknowledges notifications.
Only setup/run/cancel/revoke mutate the isolated acceptance runtime through public APIs.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import time
from urllib import error, parse, request


class CheckError(Exception):
    pass


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def output(value):
    print(json.dumps(value, ensure_ascii=False, indent=2), flush=True)


def root():
    location = os.environ.get("LAZYMIND_ACCEPTANCE_ROOT")
    if not location:
        raise CheckError("请先设置 LAZYMIND_ACCEPTANCE_ROOT")
    return Path(location).resolve()


def credentials():
    path = root() / "credentials" / "credentials.json"
    if not path.is_file():
        raise CheckError("验收凭证不存在：请先在验收应用内登录；已退出登录时这是预期结果")
    data = json.loads(path.read_text())
    origin = parse.urlsplit(data["server_url"])
    if origin.scheme != "http" or origin.hostname not in ("localhost", "127.0.0.1", "::1") or origin.username:
        raise CheckError("拒绝向非本地来源发送凭证")
    return data


def info():
    value = credentials()
    encoded = value["access_token"].split(".")[1]
    claims = json.loads(base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4)))
    return {
        "server_url": value["server_url"],
        "user_id": str(claims.get("sub", claims.get("user_id", ""))),
        "role": claims.get("role"),
        "access_sha256": hashlib.sha256(value["access_token"].encode()).hexdigest(),
        "refresh_sha256": hashlib.sha256(value["refresh_token"].encode()).hexdigest(),
        "issued_at": claims.get("iat"), "expires_at": claims.get("exp"),
        "ttl_seconds": int(claims["exp"]) - int(claims["iat"]),
        "remaining_seconds": int(claims["exp"]) - int(time.time()),
        "handoff_present": bool(value.get("desktop_handoff")),
    }


def api(route, method="GET", body=None):
    value = credentials()
    data = None if body is None else json.dumps(body).encode()
    call = request.Request(value["server_url"].rstrip("/") + route, data=data, method=method,
                           headers={"Authorization": "Bearer " + value["access_token"], "Content-Type": "application/json"})
    # Ignore global HTTP proxy settings for loopback. Never follow redirects.
    opener = request.build_opener(request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(call, timeout=15) as response:
            raw = response.read(2 * 1024 * 1024 + 1)
    except error.HTTPError as exc:
        raise CheckError(f"HTTP {exc.code}，路径 {route}；401 时本工具不会刷新，请等待后台续期或在应用内重新登录") from None
    except error.URLError:
        raise CheckError("本地 API 无法连接；检查验收应用是否已就绪") from None
    if len(raw) > 2 * 1024 * 1024:
        raise CheckError("响应超出验收读取上限")
    result = json.loads(raw)
    if isinstance(result, dict) and "data" in result:
        return result["data"]
    return result


def save(name, value):
    destination = root() / name
    with open(destination, "w", encoding="utf-8") as stream:
        os.chmod(destination, 0o600)
        json.dump(value, stream, ensure_ascii=False, indent=2)


def load(name):
    path = root() / name
    if not path.is_file():
        raise CheckError(f"缺少验收记录 {name}，请先执行对应的 setup/baseline/run")
    return json.loads(path.read_text())


def difference(label):
    before, after = load("baseline-" + label + ".json"), info()
    return {"same_user": before["user_id"] == after["user_id"],
            "same_origin": before["server_url"] == after["server_url"],
            "access_changed": before["access_sha256"] != after["access_sha256"],
            "refresh_changed": before["refresh_sha256"] != after["refresh_sha256"],
            "expiry_advanced": after["expires_at"] > before["expires_at"],
            "remaining_seconds": after["remaining_seconds"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["status", "me", "baseline", "compare", "watch", "setup", "run", "task", "cancel", "revoke"])
    parser.add_argument("label", nargs="?", default="default")
    parser.add_argument("--seconds", type=int, default=240)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-zA-Z0-9_-]{1,40}", args.label):
        raise CheckError("label 只允许 1–40 个字母、数字、下划线或短横线")
    if args.action == "status":
        output(info())
    elif args.action == "me":
        identity = api("/api/authservice/auth/me")
        output({key: identity.get(key) for key in ("user_id", "username", "role", "status")})
    elif args.action == "baseline":
        current = info()
        save("baseline-" + args.label + ".json", current)
        output(current)
    elif args.action == "compare":
        output(difference(args.label))
    elif args.action == "watch":
        if not 1 <= args.seconds <= 900:
            raise CheckError("等待上限应为 1–900 秒")
        deadline = time.monotonic() + args.seconds
        previous = None
        while True:
            state = difference(args.label)
            if state != previous:
                output(state)
                previous = state
            if not state["same_user"] or not state["same_origin"]:
                raise CheckError("用户或来源改变，不能算原会话续期通过")
            if state["access_changed"] and state["refresh_changed"] and state["expiry_advanced"] and state["remaining_seconds"] > 0:
                print("PASS: 同一用户的 access/refresh 均轮换，过期时间推进。仍需检查 session renew 日志及系统通知。")
                break
            if time.monotonic() >= deadline:
                raise CheckError("超时：未观察到完整的原用户续期")
            time.sleep(5)
    elif args.action == "setup":
        prefs = api("/api/core/user/notification-preferences")
        if not prefs.get("enabled"):
            raise CheckError("请先在验收应用设置中开启通知总开关")
        state_path = root() / "schedule.json"
        if state_path.exists():
            state = load("schedule.json")
            if state["owner"] != info()["user_id"]:
                raise CheckError("已有验收任务属于其他用户，请使用原账号或新的验收目录")
        else:
            schedule = api("/api/core/schedules", "POST", {
                "name": "后台续期人工验收", "cron_expr": "0 0 1 1 *", "timezone": "Asia/Shanghai",
                "prompt_template": "不要调用工具，不要读取文件。请只回复这一句话：后台通知验收成功。",
                "kb_ids": [], "file_ids": [],
            })
            state = {"schedule_id": schedule["id"], "owner": info()["user_id"]}
            save("schedule.json", state)
        identifier = parse.quote(state["schedule_id"], safe="")
        # Disable automatic triggers immediately; manual run-now supports disabled schedules.
        api(f"/api/core/schedules/{identifier}:cancel", "POST", {})
        view = api(f"/api/core/schedules/{identifier}/notifications")
        config = {"events": {name: {"enabled": name != "waiting", "content": "summary"} for name in ("succeeded", "failed", "waiting")},
                  "channels": {"desktop": {"enabled": True}, "wechat": {"enabled": False}, "feishu": {"enabled": False}, "wecom": {"enabled": False}}}
        configured = api(f"/api/core/schedules/{identifier}/notifications", "PUT", {"revision": view["revision"], "config": config})
        output({**state, "automatic_schedule_disabled": True, "availability": configured.get("availability")})
    elif args.action in ("run", "cancel"):
        state = load("schedule.json")
        if state["owner"] != info()["user_id"]:
            raise CheckError("请用创建验收任务的原账号执行")
        identifier = parse.quote(state["schedule_id"], safe="")
        if args.action == "cancel":
            api(f"/api/core/schedules/{identifier}:cancel", "POST", {})
            output({"automatic_schedule_disabled": True})
        else:
            filename = "run-" + args.label + ".json"
            if (root() / filename).exists():
                raise CheckError("该 label 已运行，避免重复触发。查看 task 或使用新的 label")
            result = api(f"/api/core/schedules/{identifier}:run-now", "POST", {})
            save(filename, {"task_id": result["task_id"], "conversation_id": result.get("conversation_id")})
            output(load(filename))
    elif args.action == "task":
        identifier = parse.quote(load("run-" + args.label + ".json")["task_id"], safe="")
        task = api("/api/core/task-center/tasks/" + identifier)
        notices = api("/api/core/task-center/tasks/" + identifier + "/notifications")
        output({"task_id": identifier, "task_status": task.get("status"),
                "conversation_id": task.get("conversation_id"),
                "notifications": [{key: item.get(key) for key in ("notification_id", "channel", "event", "status", "reason", "title", "body")} for item in notices.get("items", [])]})
    elif args.action == "revoke":
        # Only revoke the isolated acceptance session; never clear the user's normal home.
        value = credentials()
        api("/api/authservice/auth/logout", "POST", {"refresh_token": value["refresh_token"]})
        output({"test_refresh_token_revoked": True, "credential_file_kept_for_background_failure_observation": True})


if __name__ == "__main__":
    try:
        main()
    except CheckError as exc:
        print("CHECK FAILED:", str(exc), file=sys.stderr)
        sys.exit(1)
    except Exception:
        print("CHECK FAILED: 输入或响应格式异常。未输出原始数据，避免泄露凭证。", file=sys.stderr)
        sys.exit(1)
