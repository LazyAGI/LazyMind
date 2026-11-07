import copy
import json
import re
from typing import Iterator, Literal

import lazyllm
from lazyllm import AutoModel, ThreadPoolExecutor
from lazyllm.tools.writer.tools.base import WriterToolBase
from pydantic import BaseModel, ConfigDict, Field

from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.common.token_estimation import estimate_tokens
from lazymind.model_config import _role_entry, load_model_config

from .contracts import MissingMaterials, validate_input
from .material_bindings import MaterialBindings
from .publication import render_references
from .runtime_events import report_progress
from .prompts.literature_review_prompt import (
    INPUT_PROMPT,
    READING_NOTE_PROMPT,
    SYNTHESIS_PROMPT,
)


class _ReadingNote(BaseModel):
    model_config = ConfigDict(extra='forbid', str_strip_whitespace=True)
    note: str = Field(min_length=1)
    citation: str
    review_role: Literal['paper', 'background']


def _input_budget() -> int:
    injected = lazyllm.globals['config'].get('dynamic_model_configs', {})
    role = injected.get('llm', {}).get('chat') or _role_entry(load_model_config().get('llm'))
    return build_context_budget(llm_config={'llm': role}).effective_input_budget


def _prompt(instruction: str, data: dict) -> str:
    data = copy.deepcopy(data)

    def render(value):
        return INPUT_PROMPT.format(instruction=instruction, data=json.dumps(value, ensure_ascii=False))

    limit = _input_budget()
    prompt = render(data)
    if estimate_tokens(prompt) <= limit:
        return prompt
    data['truncated'] = True
    passages = []
    for key in ('papers', 'sources', 'background'):
        for paper in data.get(key, []):
            field = max((name for name in ('note', 'text', 'abstract', 'snippet') if paper.get(name)),
                        key=lambda name: estimate_tokens(paper[name]), default=None)
            if field:
                passages.append((paper, field, paper[field]))
                paper[field] = ''
    available = limit - estimate_tokens(render(data))
    if not passages or available < len(passages):
        raise ValueError('研究要求和文献信息超出模型输入预算，请缩短要求或减少材料。')
    for paper, field, text in passages:
        paper[field] = next(_chunks(text, available // len(passages)))
    prompt = render(data)
    if estimate_tokens(prompt) > limit:
        raise ValueError('文献材料截断后仍超出模型输入预算。')
    return prompt


def _chunks(text: str, max_tokens: int) -> Iterator[str]:
    while text:
        low, high = 0, len(text)
        while low < high:
            middle = (low + high + 1) // 2
            if estimate_tokens(json.dumps(text[:middle], ensure_ascii=False)) <= max_tokens:
                low = middle
            else:
                high = middle - 1
        if low == 0:
            raise ValueError('研究要求和资料信息已占满模型的可用输入空间。')
        yield text[:low]
        text = text[low:]


def _read_note(source: dict, context: dict, input_tokens: int) -> dict:
    writer = WriterToolBase(llm=AutoModel(model='llm'))
    text = source.get('text') or ''
    if not text.strip():
        text = source['abstract']
        source['reading'] = {'content_type': 'abstract'}
    data = {**{key: value for key, value in source.items() if key not in ('text', 'abstract')},
            'research': context, 'source_id': source['id'],
            'citation': source.get('citation', ''), 'text': ''}
    classified = source.get('review_role') == 'background'
    while True:
        overhead = estimate_tokens(INPUT_PROMPT.format(
            instruction=READING_NOTE_PROMPT, data=json.dumps(data, ensure_ascii=False),
        ))
        notes = []
        for chunk in _chunks(text, input_tokens - overhead):
            reading = writer._call_llm_structured(
                INPUT_PROMPT.format(instruction=READING_NOTE_PROMPT,
                                    data=json.dumps({**data, 'text': chunk}, ensure_ascii=False)),
                _ReadingNote,
            )
            if not classified:
                data['review_role'] = reading.review_role
                classified = True
            notes.append(reading.note)
            if data.get('review_role') == 'background':
                data['citation'] = ''
            elif not data['citation']:
                data['citation'] = reading.citation
        if len(notes) == 1:
            source['review_role'] = data['review_role']
            if source['review_role'] == 'background':
                source.pop('citation', None)
            else:
                source['citation'] = data['citation']
            return {**{key: source[key] for key in (
                'id', 'title', 'citation', 'reading', 'review_role', 'is_seed', 'is_important',
                'seed_relation', 'direction_relation', 'key_contribution',
            ) if key in source}, 'note': notes[0], 'citation': data['citation']}
        condensed = '\n\n'.join(notes)
        if estimate_tokens(condensed) >= estimate_tokens(text):
            raise ValueError('阅读笔记未能缩短输入内容，无法继续合并。')
        text = condensed


def _notes(sources: list[dict], context: dict) -> list[dict]:
    if not sources:
        return []
    input_tokens = _input_budget()
    with ThreadPoolExecutor() as executor:
        return list(executor.map(lambda source: _read_note(source, context, input_tokens), sources))


def _render(inputs: dict, notes: list[dict], background: list[dict]) -> str:
    warnings = list(dict.fromkeys(warning for paper in inputs['papers']
                                  for warning in paper.get('retrieval_warnings', [])))
    if warnings:
        report_progress(84, '资料获取提示：' + '；'.join(warnings))
    report_progress(85, '正在生成文献综述')
    bindings = MaterialBindings(notes + background)
    from .runtime_events import generate_markdown
    prompt = _prompt(SYNTHESIS_PROMPT, bindings.model_view({
        'research': {key: inputs.get(key) for key in ('topic', 'request', 'question')},
        'sources': notes, 'background': background,
    }))
    document = generate_markdown(AutoModel(model='llm'), prompt, bindings=bindings)
    # The model writes prose; Python owns reference membership and display numbering.
    language = 'en' if re.search(r'(?m)^## References\s*$', document) else 'zh'
    body = re.split(r'(?m)^## (?:参考文献|References)\s*$', document, maxsplit=1)[0].rstrip()
    markers = {'[' + paper['id'] + ']': f'[\\[{number}\\]](#paper-ref-{paper["id"]})'
               for number, paper in enumerate(notes, 1)}
    markers.update({'[' + paper['id'] + ']': '' for paper in background})
    if markers:
        body = re.sub('|'.join(re.escape(marker) for marker in markers), lambda match: markers[match[0]], body)
    return body + '\n\n' + render_references(notes, paper_language=language)


def run(inputs: dict) -> dict:
    inputs = validate_input('literature_review', inputs)
    sources = [paper for paper in inputs['papers'] if paper.get('review_role') != 'background']
    if not sources:
        raise MissingMaterials('没有可综述的论文，请先检索或提供可读论文；不能用模型知识替代文献依据。')
    background = [paper for paper in inputs['papers'] if paper.get('review_role') == 'background']
    context = {key: inputs.get(key) for key in ('topic', 'request', 'question')}
    report_progress(20, '正在整理已选论文的阅读笔记')
    notes = _notes(sources, context)
    background_notes = _notes(background, context)
    background_notes.extend(note for note in notes if note['review_role'] == 'background')
    notes = [note for note in notes if note['review_role'] == 'paper']
    if not notes:
        raise MissingMaterials('上传材料均为背景或实验资料，没有可综述的学术论文，请检索或补充论文。')
    return {'evidence': _render(inputs, notes, background_notes), 'papers': inputs['papers'], 'is_demo': False}
