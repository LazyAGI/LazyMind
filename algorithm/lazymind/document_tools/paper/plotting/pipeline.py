"""Render one figure with bounded local code repair; never regenerate statistics."""
import hashlib
import os
import traceback

from .code import validate_code
from .executor import PlotEnvironmentError, execute_plot
from .model import PlotModelError
from .templates import compose_native_program
from ..analysis.validation import validate_charts
from ..analysis import trace as diagnostics


class FigureRenderError(RuntimeError):
    def __init__(self, message, history):
        self.history = history
        super().__init__(message, history)

    def __str__(self):
        return str(self.args[0])


def limits():
    try:
        attempts = int(os.environ.get('LAZYMIND_PAPER_PLOT_ATTEMPTS', '5'))
        concurrency = int(os.environ.get('LAZYMIND_PAPER_PLOT_CONCURRENCY', '4'))
        if not 1 <= attempts <= 5 or not 1 <= concurrency <= 4:
            raise ValueError
    except ValueError as exc:
        raise ValueError('绘图尝试次数须为 1..5，并发数须为 1..4') from exc
    return attempts, concurrency


def draw_one(bound, measured, spec, reference, attempts, code_generator):
    previous, error, history = '', '', []
    for attempt in range(1, attempts + 1):
        candidate, candidate_spec = previous, dict(spec)
        try:
            details = dict(figure_id=spec['id'], attempt=attempt)
            name = diagnostics.figure_name(spec['id']) + ('-fallback' if spec.get('fallback') else '')
            with diagnostics.stage('figure_code_generation', **details):
                candidate = code_generator(bound, measured, candidate_spec, reference, previous, error)
                diagnostics.save(f'figure-{name}-code-{attempt}.py', candidate)
            with diagnostics.stage('figure_code_validation', **details):
                validate_code(candidate)
                runtime_code = compose_native_program(candidate, reference, candidate_spec)
            previous = candidate
            spec.update(candidate_spec)
            with diagnostics.stage('figure_execution', **details):
                rendered = execute_plot(dict(table=bound, statistics=measured, spec=spec,
                                             data_sha256=measured['data_sha256'], code=runtime_code))
            with diagnostics.stage('figure_validation', **details):
                chart = dict(id=spec['id'], title=spec['title'], columns=spec['columns'], **rendered)
                if spec.get('caption'):
                    chart['caption'] = spec['caption']
                validate_charts([chart])
                diagnostics.save(f'figure-{name}-exports.json', dict(
                    id=spec['id'], svg_bytes=len(rendered['svg'].encode()), formats=sorted(rendered['exports']),
                    code_sha256=hashlib.sha256(runtime_code.encode()).hexdigest()))
            history.append(dict(attempt=attempt, status='passed_execution_and_file_checks'))
            return chart, dict(
                id=spec['id'], code=runtime_code, code_sha256=hashlib.sha256(runtime_code.encode()).hexdigest(),
                attempts=history, data_contract=spec['data_contract'], style_contract=spec['style_contract'],
                first_attempt_passed_execution_and_file_checks=attempt == 1)
        except (PlotEnvironmentError, PlotModelError):
            raise
        except (ValueError, TypeError, KeyError, SyntaxError, RuntimeError) as exc:
            previous = candidate
            detail = ''.join(traceback.format_exception_only(type(exc), exc)).strip()[-6000:]
            history.append(dict(attempt=attempt, status='failed', error=detail))
            error = '\n'.join(f'Attempt {item["attempt"]}: {item["error"]}' for item in history)[-12000:]
    raise FigureRenderError(f'图表 {spec["id"]} 在 {attempts} 次尝试后失败: {history[-1]["error"]}', history)
