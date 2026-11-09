from dataclasses import dataclass
from typing import Callable

from pydantic import BaseModel

from .paper import formula_prompt, visual_prompt
from .schemas import FormulaInput, FormulaOutput, VisualInput, VisualOutput


@dataclass(frozen=True)
class TaskSpec:
    version: str
    input_schema: type[BaseModel]
    output_schema: type[BaseModel]
    instruction: Callable[[BaseModel], str]
    max_output_bytes: int = 256 << 10
    timeout_seconds: int = 120


TASKS = {
    'paper.formula.explain': TaskSpec('formula-explanation-v7-low-latency', FormulaInput,
                                      FormulaOutput, formula_prompt),
    'paper.visual.explain': TaskSpec('paper-reading-v2-grounded-findings', VisualInput,
                                     VisualOutput, visual_prompt, max_output_bytes=1 << 20),
}


def is_structured_task(name: str) -> bool:
    # Reserve the namespace so a typo never falls through to the generic task.
    return name in TASKS or name.startswith('paper.')
