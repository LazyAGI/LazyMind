from dataclasses import dataclass
from typing import Callable

from pydantic import BaseModel

from .paper import formula_prompt, visual_prompt
from .paper_guide import extract_prompt, plan_prompt, review_prompt, section_prompt
from .paper_guide_schemas import (
    Extract, GuideInput, Plan, PlanInput, Review, ReviewInput, Section, SectionInput, bind_output,
)
from .schemas import FormulaInput, FormulaOutput, VisualInput, VisualOutput


@dataclass(frozen=True)
class TaskSpec:
    version: str
    input_schema: type[BaseModel]
    output_schema: type[BaseModel]
    instruction: Callable[[BaseModel], str]
    max_output_bytes: int = 256 << 10
    timeout_seconds: int = 120
    validate_output: Callable[[BaseModel, BaseModel], None] | None = None
    reject_duplicate_keys: bool = False
    prefer_native_json: bool = False


PAPER_GUIDE_VERSION = 'paper-guide-v9'


def _guide(input_schema, output_schema, prompt):
    return TaskSpec(PAPER_GUIDE_VERSION, input_schema, output_schema, prompt, validate_output=bind_output,
                    reject_duplicate_keys=True, prefer_native_json=True)


TASKS = {
    'paper.guide.extract': _guide(GuideInput, Extract, extract_prompt),
    'paper.guide.plan': _guide(PlanInput, Plan, plan_prompt),
    'paper.guide.section': _guide(SectionInput, Section, section_prompt),
    'paper.guide.review': _guide(ReviewInput, Review, review_prompt),
    'paper.formula.explain': TaskSpec('formula-explanation-v7-low-latency', FormulaInput,
                                      FormulaOutput, formula_prompt),
    'paper.visual.explain': TaskSpec('paper-reading-v2-grounded-findings', VisualInput,
                                     VisualOutput, visual_prompt, max_output_bytes=1 << 20),
}


def is_structured_task(name: str) -> bool:
    # Reserve the namespace so a typo never falls through to the generic task.
    return name in TASKS or name.startswith('paper.')
