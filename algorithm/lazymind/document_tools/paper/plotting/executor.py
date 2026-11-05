"""Execute generated figures through the platform's isolated Python backend."""
from __future__ import annotations

import json
import os
from pathlib import Path

from ..execution import container


class PlotEnvironmentError(RuntimeError):
    """Deployment errors cannot be fixed by rewriting plotting code."""


def configuration() -> int:
    try:
        container.validate_environment()
    except container.ContainerEnvironmentError as exc:
        raise PlotEnvironmentError(str(exc)) from exc
    try:
        timeout = int(os.environ.get('LAZYMIND_PAPER_PLOT_TIMEOUT', '60'))
        if not 1 <= timeout <= 300:
            raise ValueError
    except ValueError as exc:
        raise PlotEnvironmentError('绘图超时必须是 1..300 秒') from exc
    return timeout


def execute_plot(payload: dict) -> dict:
    timeout = configuration()
    raw = json.dumps(payload, ensure_ascii=False, allow_nan=False).encode()
    if len(raw) > 40 * 1024 * 1024:
        raise ValueError('绘图输入超过 40 MiB')
    try:
        return container.execute(payload, Path(__file__).with_name('worker.py'), timeout, label='绘图')
    except container.ContainerEnvironmentError as exc:
        raise PlotEnvironmentError(str(exc)) from exc
