#!/usr/bin/env python3
"""Calculate descriptive statistics for comparable-company metrics."""

from __future__ import annotations

import argparse
import json
import math
import statistics
import sys
from collections import OrderedDict
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Calculate count, median, mean, minimum, and maximum for comparable-company metrics."
    )
    input_group = parser.add_mutually_exclusive_group(required=True)
    input_group.add_argument(
        "--data-json",
        help='JSON array such as [{"company":"A","ev_revenue":4.2},{"company":"B","ev_revenue":5.1}]',
    )
    input_group.add_argument(
        "--value",
        action="append",
        dest="values",
        metavar="COMPANY=VALUE",
        help="Single-metric observation; repeat for each company, for example --value A=4.2 --value B=5.1.",
    )
    parser.add_argument(
        "--metric",
        default="value",
        help="Metric name used with --value (default: value).",
    )
    return parser.parse_args()


def load_rows(raw: str) -> list[dict[str, Any]]:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError(f"invalid JSON: {exc.msg}") from exc
    if not isinstance(value, list) or not value:
        raise ValueError("data must be a non-empty JSON array")
    if not all(isinstance(item, dict) for item in value):
        raise ValueError("every array item must be an object")
    return value


def load_value_rows(entries: list[str], metric: str) -> list[dict[str, Any]]:
    metric_name = metric.strip()
    if not metric_name or metric_name == "company":
        raise ValueError("metric must be a non-empty name other than 'company'")

    rows: list[dict[str, Any]] = []
    for entry in entries:
        if "=" not in entry:
            raise ValueError(f"invalid --value {entry!r}; expected COMPANY=VALUE")
        company, raw_value = entry.split("=", 1)
        company = company.strip()
        raw_value = raw_value.strip()
        if not company or not raw_value:
            raise ValueError(f"invalid --value {entry!r}; company and value are required")
        try:
            value: Any = float(raw_value)
        except ValueError:
            value = raw_value
        rows.append({"company": company, metric_name: value})
    return rows


def finite_number(value: Any) -> float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    number = float(value)
    return number if math.isfinite(number) else None


def metric_names(rows: list[dict[str, Any]]) -> list[str]:
    names: OrderedDict[str, None] = OrderedDict()
    for row in rows:
        for key in row:
            if key != "company":
                names.setdefault(str(key), None)
    return list(names)


def rounded(number: float) -> float:
    return round(number, 6)


def calculate(rows: list[dict[str, Any]]) -> dict[str, Any]:
    companies = [str(row.get("company") or f"row-{index + 1}") for index, row in enumerate(rows)]
    metrics: dict[str, Any] = {}

    for metric in metric_names(rows):
        observations = []
        excluded = []
        for company, row in zip(companies, rows):
            value = finite_number(row.get(metric))
            if value is None:
                excluded.append(company)
            else:
                observations.append({"company": company, "value": value})

        values = [item["value"] for item in observations]
        if not values:
            metrics[metric] = {
                "count": 0,
                "excluded_companies": excluded,
                "statistics": None,
            }
            continue

        metrics[metric] = {
            "count": len(values),
            "excluded_companies": excluded,
            "statistics": {
                "median": rounded(statistics.median(values)),
                "mean": rounded(statistics.fmean(values)),
                "minimum": rounded(min(values)),
                "maximum": rounded(max(values)),
            },
            "observations": observations,
        }

    return {
        "company_count": len(rows),
        "companies": companies,
        "metrics": metrics,
        "note": "Statistics include finite numeric values only; review accounting periods, units, and comparability separately.",
    }


def main() -> int:
    args = parse_args()
    try:
        rows = load_rows(args.data_json) if args.data_json is not None else load_value_rows(args.values, args.metric)
        result = calculate(rows)
    except ValueError as exc:
        print(json.dumps({"status": "error", "error": str(exc)}, ensure_ascii=False))
        return 2

    print(json.dumps({"status": "ok", "result": result}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
