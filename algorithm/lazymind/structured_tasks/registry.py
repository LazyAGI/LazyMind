from dataclasses import dataclass
from typing import Callable

from pydantic import BaseModel

from .paper import formula_prompt, visual_prompt
from .schemas import FormulaInput, FormulaOutput, VisualInput, VisualOutput
from . import paper_explainer


@dataclass(frozen=True)
class TaskSpec:
    version: str
    input_schema: type[BaseModel]
    output_schema: type[BaseModel]
    instruction: Callable[..., str]
    max_output_bytes: int = 256 << 10
    timeout_seconds: int = 120
    validate_output: Callable[[BaseModel, BaseModel], None] | None = None
    reject_duplicate_keys: bool = False
    prefer_native_json: bool = False


TASKS = {
    'paper.explainer.plan': TaskSpec(paper_explainer.EXPLAINER_VERSION, paper_explainer.PlanInput,
                                   paper_explainer.Plan, paper_explainer.plan_prompt,
                                   validate_output=paper_explainer.bind_plan,
                                   timeout_seconds=120, reject_duplicate_keys=True, prefer_native_json=True),
    'paper.explainer.chapter': TaskSpec(paper_explainer.EXPLAINER_VERSION, paper_explainer.ChapterInput,
                                      paper_explainer.Chapter, paper_explainer.chapter_prompt,
                                      timeout_seconds=120, validate_output=paper_explainer.bind_chapter,
                                      reject_duplicate_keys=True, prefer_native_json=True),
    'paper.formula.explain': TaskSpec('formula-explanation-v7-low-latency', FormulaInput,
                                      FormulaOutput, formula_prompt),
    'paper.visual.explain': TaskSpec('paper-reading-v2-grounded-findings', VisualInput,
                                     VisualOutput, visual_prompt, max_output_bytes=1 << 20),
}


def is_structured_task(name: str) -> bool:
    # Reserve the namespace so a typo never falls through to the generic task.
    return name in TASKS or name.startswith('paper.')
