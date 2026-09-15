"""Small, fixed-endpoint tender API client. Credentials are never returned."""
import argparse
import json
import os
from pathlib import Path
import urllib.error
import urllib.request

ENDPOINTS = {
    "search": "SearchProjectForAI",
    "detail": "getZTBProjectDetail",
    "files": "getZTBProjectFiles",
    "source": "getCollectUrl",
}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(command, payload, key=None):
    key = key or os.environ.get("BBIAO_API_KEY", "")
    if not key:
        return {"ok": False, "error": "missing_api_key"}
    url = "https://gate.gov-bid.com/outer-gateway/bid/" + ENDPOINTS[command]
    req = urllib.request.Request(
        url,
        data=json.dumps(dict(payload, key=key)).encode(),
        headers={"Content-Type": "application/json"}, method="POST",
    )
    try:
        with urllib.request.build_opener(NoRedirect).open(req, timeout=20) as response:
            body = response.read(8_000_001)
        if len(body) > 8_000_000:
            return {"ok": False, "error": "response_too_large"}
        data = json.loads(body)
        if not isinstance(data, dict):
            return {"ok": False, "error": "invalid_response"}
        # HTTP 200 does not imply a successful business response.
        ok = str(data.get("code")) in {"200", "0"}
        result = {"ok": ok, "response": data}
        return json.loads(json.dumps(result, ensure_ascii=False).replace(key, "[REDACTED]"))
    except urllib.error.HTTPError as exc:
        return {"ok": False, "error": "http_error", "status": exc.code}
    except (OSError, ValueError):
        return {"ok": False, "error": "network_or_invalid_response"}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--credential-file', help='Local JSON with BBIAO_API_KEY; never printed')
    sub = parser.add_subparsers(dest="command", required=True)
    search = sub.add_parser("search")
    search.add_argument("--keyword", required=True)
    search.add_argument("--start", required=True)
    search.add_argument("--end", required=True)
    for command in ("detail", "files", "source"):
        p = sub.add_parser(command)
        p.add_argument("--id", type=int, required=True)
        p.add_argument("--published", required=True)
    args = parser.parse_args()
    if args.command == "search":
        payload = dict(keyword=args.keyword, startDate=args.start, endDate=args.end,
                       pageId=1, pageNumber=3, className="招标信息")
    else:
        payload = {"projectId" if args.command == "files" else "id": args.id,
                   "publishTime": args.published}
    key = None
    if args.credential_file:
        try:
            key = json.loads(Path(args.credential_file).read_text()).get('BBIAO_API_KEY')
            if not isinstance(key, str) or not key:
                raise ValueError('empty key')
        except (OSError, ValueError, AttributeError):
            print(json.dumps({'ok': False, 'error': 'invalid_credential_file'}))
            return 1
    result = request(args.command, payload, key)
    print(json.dumps(result, ensure_ascii=False))
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
