#!/usr/bin/env python3
"""Convert verified research data into deterministic Mermaid chart source."""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build a Mermaid chart from supplied, already-verified data."
    )
    parser.add_argument(
        "--chart",
        required=True,
        choices=("xy-line", "xy-bar", "pie", "quadrant"),
        help="Chart type to generate.",
    )
    parser.add_argument("--title", required=True, help="Chart title.")
    parser.add_argument("--data-json", help="Non-empty JSON array of data rows.")
    parser.add_argument(
        "--point",
        action="append",
        default=[],
        help=(
            "Safe repeatable input. Use label=value for xy/pie charts and "
            "label=x,y for quadrant charts. Do not combine with --data-json."
        ),
    )
    parser.add_argument("--x-key", default="x", help="X/category field for XY and quadrant charts.")
    parser.add_argument("--y-key", default="y", help="Y/value field for XY and quadrant charts.")
    parser.add_argument("--label-key", default="label", help="Label field for pie and quadrant charts.")
    parser.add_argument("--value-key", default="value", help="Value field for pie charts.")
    parser.add_argument("--x-axis-label", default="X", help="X-axis label.")
    parser.add_argument("--y-axis-label", default="Y", help="Y-axis label.")
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


def number_from_text(raw: str, field: str) -> float:
    try:
        value = float(raw.strip())
    except ValueError as exc:
        raise ValueError(f"{field} must be a finite number") from exc
    if not math.isfinite(value):
        raise ValueError(f"{field} must be a finite number")
    return value


def split_point(raw: str) -> tuple[str, str]:
    if "=" not in raw:
        raise ValueError("point must use label=value or label=x,y")
    label, value = raw.rsplit("=", 1)
    label = label.strip()
    if not label:
        raise ValueError("point label must be non-empty")
    return label, value.strip()


def load_points(args: argparse.Namespace) -> list[dict[str, Any]]:
    if not args.point:
        raise ValueError("provide either --point or --data-json")
    rows: list[dict[str, Any]] = []
    for raw in args.point:
        label, value = split_point(raw)
        if args.chart in {"xy-line", "xy-bar"}:
            rows.append({args.x_key: label, args.y_key: number_from_text(value, args.y_key)})
        elif args.chart == "pie":
            rows.append(
                {args.label_key: label, args.value_key: number_from_text(value, args.value_key)}
            )
        else:
            coordinates = [part.strip() for part in value.split(",")]
            if len(coordinates) != 2:
                raise ValueError("quadrant point must use label=x,y")
            rows.append(
                {
                    args.label_key: label,
                    args.x_key: number_from_text(coordinates[0], args.x_key),
                    args.y_key: number_from_text(coordinates[1], args.y_key),
                }
            )
    return rows


def text_value(value: Any, field: str) -> str:
    if value is None or isinstance(value, (dict, list, bool)):
        raise ValueError(f"{field} must be a non-empty scalar")
    text = str(value).strip()
    if not text:
        raise ValueError(f"{field} must be a non-empty scalar")
    return text


def finite_number(value: Any, field: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{field} must be a finite number")
    number = float(value)
    if not math.isfinite(number):
        raise ValueError(f"{field} must be a finite number")
    return number


def format_number(number: float) -> str:
    if number == 0:
        return "0"
    if number.is_integer():
        return str(int(number))
    return format(number, ".12g")


def quoted(text: str) -> str:
    return json.dumps(text, ensure_ascii=False)


def plain_mermaid_label(text: str, field: str) -> str:
    cleaned = re.sub(r"[\r\n,:;\[\]{}]", " ", text)
    cleaned = re.sub(r"\s+", " ", cleaned).strip()
    if not cleaned:
        raise ValueError(f"{field} has no usable Mermaid label characters")
    return cleaned


def ensure_unique(labels: list[str], field: str) -> None:
    if len(set(labels)) != len(labels):
        raise ValueError(f"{field} values must be unique")


def build_xy(args: argparse.Namespace, rows: list[dict[str, Any]]) -> tuple[str, list[dict[str, Any]]]:
    labels = [text_value(row.get(args.x_key), args.x_key) for row in rows]
    values = [finite_number(row.get(args.y_key), args.y_key) for row in rows]
    ensure_unique(labels, args.x_key)

    series = "line" if args.chart == "xy-line" else "bar"
    mermaid = "\n".join(
        [
            "xychart-beta",
            f"  title {quoted(text_value(args.title, 'title'))}",
            f"  x-axis {quoted(text_value(args.x_axis_label, 'x-axis-label'))} "
            f"[{', '.join(quoted(label) for label in labels)}]",
            f"  y-axis {quoted(text_value(args.y_axis_label, 'y-axis-label'))}",
            f"  {series} [{', '.join(format_number(value) for value in values)}]",
        ]
    )
    table = [{args.x_key: label, args.y_key: value} for label, value in zip(labels, values)]
    return mermaid, table


def build_pie(args: argparse.Namespace, rows: list[dict[str, Any]]) -> tuple[str, list[dict[str, Any]]]:
    labels = [text_value(row.get(args.label_key), args.label_key) for row in rows]
    values = [finite_number(row.get(args.value_key), args.value_key) for row in rows]
    ensure_unique(labels, args.label_key)
    if any(value < 0 for value in values):
        raise ValueError(f"{args.value_key} must not contain negative values for a pie chart")
    if not any(value > 0 for value in values):
        raise ValueError(f"{args.value_key} must contain at least one positive value")

    lines = ["pie showData", f"  title {text_value(args.title, 'title')}"]
    lines.extend(f"  {quoted(label)} : {format_number(value)}" for label, value in zip(labels, values))
    table = [{args.label_key: label, args.value_key: value} for label, value in zip(labels, values)]
    return "\n".join(lines), table


def build_quadrant(
    args: argparse.Namespace, rows: list[dict[str, Any]]
) -> tuple[str, list[dict[str, Any]]]:
    labels = [
        plain_mermaid_label(text_value(row.get(args.label_key), args.label_key), args.label_key)
        for row in rows
    ]
    ensure_unique(labels, args.label_key)
    points: list[tuple[float, float]] = []
    for row in rows:
        x_value = finite_number(row.get(args.x_key), args.x_key)
        y_value = finite_number(row.get(args.y_key), args.y_key)
        if not 0 <= x_value <= 1 or not 0 <= y_value <= 1:
            raise ValueError("quadrant x and y values must be between 0 and 1")
        points.append((x_value, y_value))

    title = plain_mermaid_label(text_value(args.title, "title"), "title")
    x_axis = plain_mermaid_label(text_value(args.x_axis_label, "x-axis-label"), "x-axis-label")
    y_axis = plain_mermaid_label(text_value(args.y_axis_label, "y-axis-label"), "y-axis-label")
    lines = [
        "quadrantChart",
        f"  title {title}",
        f"  x-axis 低{x_axis} --> 高{x_axis}",
        f"  y-axis 低{y_axis} --> 高{y_axis}",
        "  quadrant-1 高X高Y",
        "  quadrant-2 低X高Y",
        "  quadrant-3 低X低Y",
        "  quadrant-4 高X低Y",
    ]
    lines.extend(
        f"  {label}: [{format_number(x_value)}, {format_number(y_value)}]"
        for label, (x_value, y_value) in zip(labels, points)
    )
    table = [
        {args.label_key: label, args.x_key: x_value, args.y_key: y_value}
        for label, (x_value, y_value) in zip(labels, points)
    ]
    return "\n".join(lines), table


def build_chart(args: argparse.Namespace, rows: list[dict[str, Any]]) -> dict[str, Any]:
    if args.chart in {"xy-line", "xy-bar"}:
        mermaid, table = build_xy(args, rows)
    elif args.chart == "pie":
        mermaid, table = build_pie(args, rows)
    else:
        mermaid, table = build_quadrant(args, rows)
    return {
        "chart_type": args.chart,
        "mermaid": mermaid,
        "data_table": table,
        "note": "Chart source reflects supplied values only; verify units, periods, coverage, and source citations in the report.",
    }


def main() -> int:
    args = parse_args()
    try:
        if args.data_json and args.point:
            raise ValueError("do not combine --point with --data-json")
        rows = load_rows(args.data_json) if args.data_json else load_points(args)
        result = build_chart(args, rows)
    except ValueError as exc:
        print(json.dumps({"status": "error", "error": str(exc)}, ensure_ascii=False))
        return 2

    print(json.dumps({"status": "ok", "result": result}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
