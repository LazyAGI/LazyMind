import argparse
import json
import re
from copy import deepcopy
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional

import requests


def call_case(url: str, case_id: Any, timeout: int = 60) -> Dict[str, Any]:
    """
    调用单个 case。

    默认使用 HTTP POST，请求体为:
    {
      "case_id": <case_id>
    }

    返回值要求为 dict；如果接口返回的不是 dict，会包装成 dict 返回。
    """
    response = requests.post(url, json={"case_id": case_id}, timeout=timeout)
    response.raise_for_status()

    data = response.json()
    if isinstance(data, dict):
        return data

    return {
        "success": True,
        "data": data,
    }


def run_case(url: str, case_id: Any, timeout: int = 60) -> Dict[str, Any]:
    """对外暴露的单 case 调用函数，签名固定为 url + case_id -> dict。"""
    return call_case(url=url, case_id=case_id, timeout=timeout)


def run_case_list(
    url: str,
    case_ids: Iterable[Any],
    output_dir: str = "tests/evo/results",
    timeout: int = 60,
) -> Dict[str, Any]:
    """
    批量执行 case 列表，并将每个 case 的结果单独落盘。
    """
    case_id_list = list(case_ids)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%d_%H%M%S")
    base_dir = Path(output_dir) / timestamp
    base_dir.mkdir(parents=True, exist_ok=True)

    items: List[Dict[str, Any]] = []

    for index, case_id in enumerate(case_id_list, start=1):
        case_id_snapshot = deepcopy(case_id)
        item: Dict[str, Any] = {
            "index": index,
            "case_id": case_id_snapshot,
            "success": False,
        }

        try:
            result = run_case(url=url, case_id=case_id_snapshot, timeout=timeout)
            item["success"] = True
            item["result"] = result
        except Exception as exc:
            item["error"] = {
                "type": type(exc).__name__,
                "message": str(exc),
            }

        result_path = base_dir / _result_filename(index=index, case_id=case_id_snapshot)
        result_path.write_text(
            json.dumps(item, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        item["result_file"] = str(result_path)
        items.append(item)

    summary = {
        "url": url,
        "total": len(items),
        "success_count": sum(1 for item in items if item["success"]),
        "failed_count": sum(1 for item in items if not item["success"]),
        "output_dir": str(base_dir),
        "items": items,
    }

    summary_path = base_dir / "summary.json"
    summary_path.write_text(
        json.dumps(summary, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )
    summary["summary_file"] = str(summary_path)
    return summary


def _result_filename(index: int, case_id: Any) -> str:
    case_name = _extract_case_name(case_id)
    return f"{index:03d}_{case_name}.json"


def _extract_case_name(case_id: Any) -> str:
    if isinstance(case_id, dict):
        for key in ("name", "case_name", "id", "title", "question"):
            value = case_id.get(key)
            if value:
                return _sanitize_filename(str(value))

    if isinstance(case_id, str) and case_id.strip():
        return _sanitize_filename(case_id)

    if isinstance(case_id, (int, float)):
        return _sanitize_filename(str(case_id))

    return "case"


def _sanitize_filename(value: str, max_length: int = 80) -> str:
    sanitized = re.sub(r"[^\w\-.]+", "_", value.strip())
    sanitized = sanitized.strip("._")
    if not sanitized:
        return "case"
    return sanitized[:max_length]


def _load_case_ids(cases_file: str) -> List[Any]:
    path = Path(cases_file)
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, list):
        raise ValueError("cases file must be a JSON list of case_id values")
    return data


def main() -> None:
    parser = argparse.ArgumentParser(description="Batch run evo case_ids and save each result.")
    parser.add_argument("--url", required=True, help="目标调用 URL")
    parser.add_argument(
        "--cases-file",
        required=True,
        help="JSON 文件路径，文件内容必须是 case_id 列表",
    )
    parser.add_argument(
        "--output-dir",
        default="tests/evo/results",
        help="结果输出目录",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=60,
        help="单个 case 请求超时时间（秒）",
    )
    args = parser.parse_args()

    summary = run_case_list(
        url=args.url,
        case_ids=_load_case_ids(args.cases_file),
        output_dir=args.output_dir,
        timeout=args.timeout,
    )
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
