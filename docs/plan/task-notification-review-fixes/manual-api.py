#!/usr/bin/env python3
"""Manual acceptance HTTP helper; no login, refresh, automatic retry or acknowledgement."""
import argparse
import json
import os
from pathlib import Path
import re
import sys
from urllib import error, request

sys.path.insert(0, str(Path(__file__).resolve().parent))
import acceptance as shared


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("method", choices=["GET", "POST", "PUT", "PATCH", "DELETE"])
    parser.add_argument("route")
    parser.add_argument("expected", help="Expected HTTP status, e.g. 200 or 401,403")
    parser.add_argument("output", help="JSON filename within LAZYMIND_ACCEPTANCE_ROOT")
    parser.add_argument("body", nargs="?", help="Request filename within the same directory")
    parser.add_argument("--anonymous", action="store_true")
    args = parser.parse_args()
    if not args.route.startswith("/api/") or any(c in args.route for c in "\r\n#"):
        raise shared.CheckError("Only local /api/ paths are accepted")
    for filename in (args.output, args.body):
        if filename and not re.fullmatch(r"[A-Za-z0-9_-]+\.json", filename):
            raise shared.CheckError("Use a simple JSON filename, without directory components")
    expected = {int(value) for value in args.expected.split(",")}
    credentials = shared.credentials()
    headers = {"Content-Type": "application/json"}
    if not args.anonymous:
        headers["Authorization"] = "Bearer " + credentials["access_token"]
    body = (shared.root() / args.body).read_bytes() if args.body else None
    if body is not None and len(body) > 2 * 1024 * 1024:
        raise shared.CheckError("Request exceeds acceptance size limit")
    call = request.Request(credentials["server_url"].rstrip("/") + args.route,
                           data=body, method=args.method, headers=headers)
    opener = request.build_opener(request.ProxyHandler({}), shared.NoRedirect())
    try:
        response = opener.open(call, timeout=30)
    except error.HTTPError as exc:
        response = exc
    except error.URLError:
        raise shared.CheckError("Local API unavailable; no request was automatically retried") from None
    with response:
        status = response.code
        raw = response.read(2 * 1024 * 1024 + 1)
    if len(raw) > 2 * 1024 * 1024:
        raise shared.CheckError("Response exceeds acceptance size limit")
    result = json.loads(raw) if raw else None
    data = result.get("data", result) if isinstance(result, dict) else result
    shared.save(args.output, data)
    reason = None
    if isinstance(data, dict):
        detail = data.get("detail", {})
        failure = data.get("error", {})
        if isinstance(detail, dict):
            reason = detail.get("reason")
        if not reason and isinstance(failure, dict):
            reason = failure.get("code")
    # Print only stable codes, never arbitrary service messages, QR payloads or credentials.
    if not isinstance(reason, str) or not re.fullmatch(r"[A-Z][A-Z0-9_]{0,100}", reason):
        reason = None
    print(json.dumps({"http_status": status, "reason": reason, "saved": args.output}))
    if status not in expected:
        raise shared.CheckError("Unexpected HTTP status; stop this scenario and inspect its saved response locally")
    if status < 300 and isinstance(result, dict) and "code" in result and result["code"] != 0:
        raise shared.CheckError("HTTP succeeded but the application returned a nonzero code")


if __name__ == "__main__":
    os.umask(0o077)
    try:
        main()
    except shared.CheckError as exc:
        print("CHECK FAILED:", exc, file=sys.stderr)
        sys.exit(1)
    except Exception:
        print("CHECK FAILED: request or response processing failed; raw data omitted", file=sys.stderr)
        sys.exit(1)
