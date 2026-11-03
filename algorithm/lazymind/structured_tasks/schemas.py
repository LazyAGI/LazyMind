from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


Text = Annotated[str, Field(max_length=16000)]
Identifier = Annotated[str, Field(min_length=1, max_length=500)]


class InputModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class SourceBlock(InputModel):
    id: Identifier
    text: Text
    page: int | None = Field(default=None, ge=0)
    bbox: list[float] | None = Field(default=None, min_length=4, max_length=4)


class TargetFormula(InputModel):
    id: Identifier
    latex: Annotated[str, Field(min_length=1)]


class FormulaInput(InputModel):
    language: Literal['en', 'zh']
    target_formula: TargetFormula
    source_blocks: list[SourceBlock] = Field(max_length=64)
    target_symbol: Identifier | None = None

    @model_validator(mode='after')
    def focus_occurs_in_formula(self):
        if self.target_symbol is not None and (
            not self.target_symbol.strip() or self.target_symbol not in self.target_formula.latex
        ):
            raise ValueError('invalid focus')
        return self


class TargetBlock(InputModel):
    id: Identifier
    type: Literal['image', 'table']
    page: int = Field(ge=0)


class VisualInput(InputModel):
    language: Literal['en', 'zh']
    target_block: TargetBlock
    source_blocks: list[SourceBlock] = Field(max_length=64)


class OutputModel(BaseModel):
    # Core supplies metadata and verifies citations against the immutable source.
    model_config = ConfigDict(extra='ignore', strict=True)


class Step(OutputModel):
    text: Annotated[str, Field(min_length=1, max_length=12000)]
    evidence_ids: list[Identifier] = Field(default_factory=list, max_length=64)
    inferred: bool = True


class Symbol(OutputModel):
    symbol: Identifier
    meaning: Annotated[str, Field(min_length=1, max_length=8000)]
    evidence_ids: list[Identifier] = Field(default_factory=list, max_length=64)
    inferred: bool = True

    @model_validator(mode='before')
    @classmethod
    def known_alias(cls, data):
        if isinstance(data, dict) and (
            not data.get('meaning')
            or isinstance(data.get('meaning'), str) and not data['meaning'].strip()
        ):
            return {**data, 'meaning': data.get('text')}
        return data


class SummaryOutput(OutputModel):
    summary: Annotated[str, Field(min_length=1, max_length=16000)]

    @model_validator(mode='before')
    @classmethod
    def known_alias(cls, data):
        if isinstance(data, dict) and (
            not data.get('summary')
            or isinstance(data.get('summary'), str) and not data['summary'].strip()
        ):
            return {**data, 'summary': data.get('explain')}
        return data


class FormulaOutput(SummaryOutput):
    symbols: list[Symbol] = Field(max_length=64)
    steps: list[Step] = Field(default_factory=list, max_length=32)
    # Optional repairs are bounded by total response size. Core discards unsafe
    # or oversized repairs without losing the otherwise usable explanation.
    display_latex: str = ''
    repair_note: str = ''


class VisualOutput(SummaryOutput):
    observations: list[Step] = Field(max_length=24)
