"""Model-facing Ask User inputs; host-authored cards keep their existing contract."""
from __future__ import annotations

from typing import Annotated, Any, Dict, List, Literal, Union

from pydantic import (
    BaseModel, ConfigDict, Discriminator, Field, StringConstraints, Tag, TypeAdapter,
    ValidationError, WithJsonSchema, field_validator, model_validator,
)

from lazyllm.tools.agent import ToolExecutionError


MAX_SINGLE_SUGGESTIONS = 3
MAX_MULTIPLE_SUGGESTIONS = 4

_Text = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]


class _Input(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True, allow_inf_nan=False)


class _AnswerValidation(_Input):
    format: Literal['', 'integer', 'positive_integer'] = ''
    example: _Text | None = Field(default=None, description='A useful example of the expected answer, when needed.')
    min_length: int = Field(default=0, ge=0)
    max_length: int = Field(default=0, ge=0, description='Zero means no business length limit.')
    min: float | None = None
    max: float | None = None

    @model_validator(mode='after')
    def ordered_bounds(self):
        errors = []
        if self.max_length and self.min_length > self.max_length:
            errors.append('min_length must not exceed max_length')
        if self.min is not None and self.max is not None and self.min > self.max:
            errors.append('min must not exceed max')
        if errors:
            raise ValueError('; '.join(errors))
        return self


class _BooleanLabels(_Input):
    yes: _Text = Field(description='Complete affirmative action, e.g. 生成 AI 底图; not just 确定.')
    no: _Text = Field(description='Complete negative action, e.g. 不生成 AI 底图; not just 取消.')

    @model_validator(mode='after')
    def distinct_labels(self):
        if self.yes == self.no:
            raise ValueError('yes and no must describe two different decisions')
        return self


class _Attachment(_Input):
    when_choice: _Text
    material_id: _Text
    accept: Annotated[List[_Text], Field(min_length=1)]
    max_files: int = 1
    required: bool = False

    @field_validator('max_files')
    @classmethod
    def valid_max_files(cls, value):
        if value != -1 and value < 1:
            raise ValueError('max_files must be positive, or -1 for unlimited')
        return value


class _Question(_Input):
    id: _Text = Field(description='Stable question id; reuse the declared business field id when provided.')
    text: _Text = Field(description='One decision dimension. This is the question body, not title or description.')
    reason: _Text = Field(description='Briefly explain why the answer is needed or what it changes.')
    required: bool = Field(description='Whether the user must answer before submitting; this is not a default answer.')


class _ChoiceQuestion(_Question):
    allow_other: bool | None = Field(default=None, description='Open suggestions include Other by default.')
    validation: _AnswerValidation | None = None

    @field_validator('choices', check_fields=False)
    @classmethod
    def unique_choices(cls, value):
        if len(value) != len(set(value)):
            raise ValueError('choices must be different')
        if '__ask_user_other__' in value:
            raise ValueError('Other is added by the system; omit it from choices')
        return value


class _SingleQuestion(_ChoiceQuestion):
    type: Literal['single']
    choice_policy: Literal['seed', 'subset'] = 'seed'
    choices: Annotated[List[_Text], Field(min_length=1, max_length=MAX_SINGLE_SUGGESTIONS)]
    attachment: _Attachment | None = Field(default=None, description='Only use a host-declared upload contract.')

    @model_validator(mode='after')
    def declared_upload_choice(self):
        if self.attachment and self.attachment.when_choice not in self.choices:
            raise ValueError('attachment.when_choice must match a declared choice')
        return self


class _FixedSingleQuestion(_SingleQuestion):
    choice_policy: Literal['fixed']
    choices: Annotated[List[_Text], Field(min_length=1)] = Field(
        description='Complete known business enum. Never declare fixed merely to bypass suggestion limits.',
    )


class _MultipleQuestion(_ChoiceQuestion):
    type: Literal['multiple']
    choice_policy: Literal['seed', 'subset'] = 'seed'
    choices: Annotated[List[_Text], Field(min_length=1, max_length=MAX_MULTIPLE_SUGGESTIONS)]


class _FixedMultipleQuestion(_MultipleQuestion):
    choice_policy: Literal['fixed']
    choices: Annotated[List[_Text], Field(min_length=1)] = Field(
        description='Complete known business enum. Selecting one item is sufficient.',
    )


class _BooleanQuestion(_Question):
    type: Literal['boolean']
    boolean_labels: _BooleanLabels


class _TextQuestion(_Question):
    type: Literal['text']
    validation: _AnswerValidation | None = Field(
        default=None, description='Provide useful examples and real business limits only; never invent a length limit.',
    )


def _question_kind(value):
    if isinstance(value, dict):
        kind, policy = value.get('type'), value.get('choice_policy')
    else:
        kind, policy = getattr(value, 'type', None), getattr(value, 'choice_policy', None)
    return f'{kind}_fixed' if kind in ('single', 'multiple') and policy == 'fixed' else kind


_ModelQuestion = Annotated[
    Union[
        Annotated[_SingleQuestion, Tag('single')],
        Annotated[_FixedSingleQuestion, Tag('single_fixed')],
        Annotated[_MultipleQuestion, Tag('multiple')],
        Annotated[_FixedMultipleQuestion, Tag('multiple_fixed')],
        Annotated[_BooleanQuestion, Tag('boolean')],
        Annotated[_TextQuestion, Tag('text')],
    ],
    Discriminator(_question_kind),
]
_EXAMPLES = [
    [{'id': 'channel', 'type': 'single', 'text': '这份稿件要发布到哪里？',
      'reason': '发布渠道会影响格式要求。', 'required': True, 'choices': ['公众号', '公司官网']}],
    [{'id': 'scope', 'type': 'multiple', 'text': '本次要改造哪些现有模块？',
      'reason': '需要明确本次的执行范围。', 'required': True, 'choices': ['搜索', '订单']}],
    [{'id': 'background', 'type': 'boolean', 'text': '是否生成 AI 底图？',
      'reason': '生成底图会增加制作时间。', 'required': True,
      'boolean_labels': {'yes': '生成 AI 底图', 'no': '不生成 AI 底图'}}],
    [{'id': 'source', 'type': 'text', 'text': '请提供需要润色的原文。',
      'reason': '需要原文才能保留原意。', 'required': True,
      'validation': {'example': '今天开会讨论了垃圾分类，决定先从办公室做起。'}}],
]
_QUESTIONS = TypeAdapter(Annotated[
    List[_ModelQuestion], Field(min_length=1, examples=_EXAMPLES),
])


def _inline_schema(schema):
    # The existing compatible providers include endpoints that reject local $ref.
    # Expand the finite input models rather than changing the shared tool transport.
    definitions = schema.get('$defs', {})

    def expand(value):
        if isinstance(value, list):
            return [expand(item) for item in value]
        if isinstance(value, dict):
            if '$ref' in value:
                return expand({**definitions[value['$ref'].removeprefix('#/$defs/')],
                               **{key: item for key, item in value.items() if key != '$ref'}})
            return {key: expand(item) for key, item in value.items() if key not in {'$defs', 'title'}}
        return value

    return expand(schema)


# Validate the full input *after* a host wrapper restores authoritative enums and
# upload metadata. The tool manager still receives the complete generated schema.
ModelQuestions = Annotated[List[Dict[str, Any]], WithJsonSchema(_inline_schema(_QUESTIONS.json_schema()))]


def validate_model_questions(questions):
    errors = []
    try:
        validated = _QUESTIONS.validate_python(questions)
    except ValidationError as exc:
        for error in exc.errors(include_url=False, include_context=False, include_input=False):
            parts = list(error['loc'])
            if len(parts) > 1 and isinstance(parts[0], int):
                # Pydantic inserts the selected union tag before the actual field.
                parts.pop(1)
            path = 'questions' + ''.join(f'[{part}]' if isinstance(part, int) else f'.{part}' for part in parts)
            errors.append(f'{path}: {error["msg"]}')
    ids = set()
    for index, question in enumerate(questions if isinstance(questions, list) else []):
        question_id = question.get('id') if isinstance(question, dict) else None
        if not isinstance(question_id, str) or not question_id.strip():
            continue
        question_id = question_id.strip()
        if question_id in ids:
            errors.append(f'questions[{index}].id: Question ids must be unique')
        ids.add(question_id)
    if errors:
        raise ToolExecutionError('Invalid arguments: ' + '; '.join(errors)
                                 + '. Fix these fields and preserve the other questions.')
    return [question.model_dump(exclude_unset=True, exclude_none=True) for question in validated]
