"""Read-only bid intelligence client: explicit filters and bounded pagination."""
import argparse
import json
import os
import sys
from datetime import date
from pathlib import Path
import urllib.error
import urllib.request

ENDPOINTS = {
    "search": "SearchProjectForAI",
    "detail": "getZTBProjectDetail",
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
    except urllib.error.URLError as exc:
        return {"ok": False, "error": "network_error", "reason_type": type(exc.reason).__name__}
    except (OSError, ValueError):
        return {"ok": False, "error": "network_or_invalid_response"}


def date_arg(value):
    try:
        return date.fromisoformat(value).isoformat()
    except ValueError:
        raise argparse.ArgumentTypeError('date must be YYYY-MM-DD')


def parser_for_cli():
    parser = argparse.ArgumentParser()
    parser.add_argument('--credential-file', help='Local JSON with BBIAO_API_KEY; never printed')
    sub = parser.add_subparsers(dest="command", required=True)
    search = sub.add_parser("search")
    search.add_argument("--keyword", required=True)
    search.add_argument("--start", type=date_arg, required=True)
    search.add_argument("--end", type=date_arg, required=True)
    search.add_argument('--area', default='')
    search.add_argument('--stage', choices=['全部信息','招标信息','中标信息','合同信息','采购意向'], default='招标信息')
    search.add_argument('--company', default='')
    search.add_argument('--exclude', default='')
    search.add_argument('--page', type=int, default=1)
    search.add_argument('--page-size', type=int, choices=range(1,21), default=5)
    for command in ("detail", "source"):
        p = sub.add_parser(command)
        p.add_argument("--id", type=int, required=True)
        p.add_argument("--published", required=True)
    return parser


def payload_for(args):
    if args.command == "search":
        if args.start > args.end or args.page < 1:
            raise ValueError('invalid date range or page')
        return dict(keyword=args.keyword, startDate=args.start, endDate=args.end,
                    pageId=args.page, pageNumber=args.page_size, className=args.stage,
                    areaName=args.area, companyName=args.company, excludeKW=args.exclude,
                    OrderByType='发布时间')
    if args.id <= 0:
        raise ValueError('invalid project id')
    return dict(id=args.id,publishTime=args.published)


def annotate_search(result, payload):
    """Deduplicate notices, retaining the vendor total and original row count."""
    if not result['ok']:
        return result
    data = result['response'].get('data')
    if not isinstance(data, dict) or not isinstance(data.get('data'), list):
        return {'ok':False,'error':'invalid_search_response'}
    seen, rows = set(), []
    for row in data['data']:
        if not isinstance(row, dict):
            return {'ok':False,'error':'invalid_search_row'}
        identity = (row.get('id'),row.get('publishTime'))
        # Unknown identities are preserved rather than collapsed together.
        if identity[0] is not None and identity[1] is not None:
            if identity in seen:
                continue
            seen.add(identity)
        rows.append(row)
    result['selection'] = dict(filters=payload,api_total=data.get('total'),
        returned_count=len(data['data']),unique_notice_count=len(rows),
        has_next=data.get('hasNext'),
        scope='current page only; notice count is not unique procurement project count')
    data['data'] = rows
    return result


def main():
    parser = parser_for_cli()
    args = parser.parse_args()
    try:
        payload = payload_for(args)
    except ValueError as exc:
        parser.error(str(exc))
    key = None
    if args.credential_file:
        try:
            key = json.loads(Path(args.credential_file).read_text()).get('BBIAO_API_KEY')
            if not isinstance(key, str) or not key:
                raise ValueError('empty key')
        except (OSError, ValueError, AttributeError):
            print(json.dumps({'ok': False, 'error': 'invalid_credential_file'}), file=sys.stderr)
            return 1
    result = request(args.command, payload, key)
    if args.command == 'search':
        result = annotate_search(result, payload)
    # LazyMind surfaces stderr on nonzero exit; keep business errors observable.
    print(json.dumps(result, ensure_ascii=False), file=sys.stdout if result['ok'] else sys.stderr)
    return 0 if result["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
