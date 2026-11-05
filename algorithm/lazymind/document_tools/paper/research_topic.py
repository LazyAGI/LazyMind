"""Generate one research question from the available F1 materials."""

import copy
import json
import re
from collections.abc import Callable

from lazyllm import AutoModel, ThreadPoolExecutor

from lazymind.common.token_estimation import estimate_tokens

from .contracts import MissingMaterials, validate_value
from .prompts.research_topic_prompt import (
    ABSTRACT_PAPER_GUIDANCE,
    CONTENT_TOPICS,
    NO_PAPER_GUIDANCE,
    PAPER_NOTE_PROMPT,
    QUESTION_PROMPT,
    TOPIC_EXTRACTION_PROMPT,
)


_SECTION_RE = re.compile(r'^##[ \t]+([^\n]+)$', re.MULTILINE)
_PAGE_RE = re.compile(r'<!-- page:(\d+) -->')
_MAX_WORKERS = 4


def _prompt_token_limit() -> int:
    """Resolve the current LLM input budget, leaving output and safety margin."""
    import lazyllm

    from lazymind.chat.engine.agent_runtime.budget import build_context_budget
    from lazymind.model_config import load_model_config

    llm_config = load_model_config().get('llm') or {}
    if isinstance(llm_config, list):
        llm_config = llm_config[0] if llm_config else {}
    if not isinstance(llm_config, dict):
        llm_config = {}

    if str(llm_config.get('source') or '').lower() == 'dynamic':
        dynamic_configs = lazyllm.globals['config'].get('dynamic_model_configs') or {}
        dynamic_llm = dynamic_configs.get('llm') or {}
        if isinstance(dynamic_llm, dict):
            dynamic_llm = dynamic_llm.get('chat') or {}
        if isinstance(dynamic_llm, dict):
            llm_config = {**llm_config, **dynamic_llm}

    budget = build_context_budget(llm_config={'llm': llm_config})
    return max(1, int(budget.effective_input_budget * 0.9))


def _extract_abstract(text: str) -> str | None:
    """Extract a delimited abstract from parsed Markdown or plain text."""
    text = _PAGE_RE.sub('', text)
    start = re.search(r'(?im)^[ \t]*(?:#{1,6}[ \t]+)?(?:abstract\b|摘[ \t]*要)'
                      r'[ \t]*(?:[:：—-][ \t]*)?(?:\r?\n)?', text)
    if not start:
        return None
    body = text[start.end():].lstrip()
    end = re.search(r'(?im)^[ \t]*(?:#{1,6}[ \t]+|(?:\d+(?:\.\d+)*\.?[ \t]+)?'
                    r'(?:key[ \t]*words\b|关键词|关[ \t]*键[ \t]*词|introduction\b|引言|绪论))', body)
    if not end:
        end = re.search(r'\r?\n[ \t]*\r?\n', body)
    if end:
        return body[:end.start()].strip() or None
    return None


def prepare_papers(papers: list[dict], prompt_limit: int | None = None) -> list[dict]:
    """Fill missing abstracts without replacing full text or mutating the caller."""
    validate_value('papers', papers)
    prepared = copy.deepcopy(papers)
    pending = []
    for index, paper in enumerate(prepared):
        if (paper.get('abstract') or '').strip():
            continue
        abstract = _extract_abstract(paper['text'])
        if abstract:
            paper['abstract'] = abstract
        else:
            pending.append(index)
    if pending:
        if prompt_limit is None:
            prompt_limit = _prompt_token_limit()
        for index, paper in zip(
            pending,
            _condensed_papers([prepared[index] for index in pending], prompt_limit),
        ):
            prepared[index] = paper
    return prepared


def infer_topic(request: str, papers: list[dict]) -> str:
    """Infer the F1 topic from the request and prepared paper abstracts."""
    paper_clues = [{'id': paper['id'], 'abstract': paper['abstract']} for paper in papers]
    prompt = TOPIC_EXTRACTION_PROMPT + '\n' + json.dumps(
        {'request': request, 'paper_clues': paper_clues}, ensure_ascii=False,
    )
    if estimate_tokens(prompt) > _prompt_token_limit():
        raise MissingMaterials('研究主题提取输入过长，请缩短要求或减少论文。')
    response = str(AutoModel(model='llm')(prompt) or '').strip()
    if not response:
        raise RuntimeError('研究主题提取模型返回空内容。')
    response = re.sub(r'^(?:<think\b[^>]*>.*?</think>\s*)+', '', response,
                      flags=re.IGNORECASE | re.DOTALL)
    try:
        result = json.loads(response)
    except json.JSONDecodeError as exc:
        raise RuntimeError('研究主题提取模型输出不是有效 JSON。') from exc
    if not isinstance(result, dict) or set(result) != {'topic'}:
        raise RuntimeError('研究主题提取模型输出格式错误。')
    topic = result['topic']
    if topic is None or (isinstance(topic, str) and not topic.strip()):
        raise MissingMaterials('无法确定研究主题，请明确说明研究对象或核心问题。')
    if not isinstance(topic, str):
        raise RuntimeError('研究主题提取模型输出格式错误。')
    return topic.strip()


def split_text_by_prompt_budget(
    text: str, build_prompt: Callable[[str], str], prompt_limit: int, *, overflow_message: str,
) -> list[str]:
    """Split contiguous text using the caller's complete prompt, without changing the source."""
    if not text:
        return []
    if estimate_tokens(build_prompt(text)) <= prompt_limit:
        return [text]

    chunks = []
    start = 0
    while start < len(text):
        low, high = start + 1, len(text)
        end = start
        while low <= high:
            middle = (low + high) // 2
            if estimate_tokens(build_prompt(text[start:middle])) <= prompt_limit:
                end = middle
                low = middle + 1
            else:
                high = middle - 1
        if end == start:
            raise ValueError(overflow_message)
        if end < len(text):
            line_end = text.rfind('\n', start, end)
            if line_end >= start + (end - start) // 2:
                end = line_end + 1
        chunks.append(text[start:end])
        start = end
    return chunks


def group_texts_by_prompt_budget(
    texts: list[str], build_prompt: Callable[[str], str], prompt_limit: int, *, overflow_message: str,
) -> list[list[str]]:
    """Group texts in order within the caller's complete prompt budget; never call a model."""
    groups = []
    group = []
    for text in texts:
        if estimate_tokens(build_prompt(text)) > prompt_limit:
            raise ValueError(overflow_message)
        if group and estimate_tokens(build_prompt('\n\n'.join([*group, text]))) > prompt_limit:
            groups.append(group)
            group = []
        group.append(text)
    if group:
        groups.append(group)
    return groups


def _paper_chunks(text: str, paper: dict, prompt_limit: int) -> list[str]:
    return split_text_by_prompt_budget(
        text, lambda source: _note_prompt(source, paper), prompt_limit,
        overflow_message='研究选题的论文片段提示超出模型输入预算。',
    )


def _note_prompt(source: str, paper: dict) -> str:
    return PAPER_NOTE_PROMPT + '\n' + json.dumps(
        {'title': paper['title'], 'source': source}, ensure_ascii=False,
    )


def _condense(source: str, paper: dict, prompt_limit: int) -> str:
    prompt = _note_prompt(source, paper)
    if estimate_tokens(prompt) > prompt_limit:
        raise ValueError('研究选题的论文片段或摘要超出模型输入预算。')
    note = str(AutoModel(model='llm')(prompt) or '').strip()
    note = re.sub(r'^(?:<think\b[^>]*>.*?</think>\s*)+', '', note,
                  flags=re.IGNORECASE | re.DOTALL)
    if not note or re.match(r'^<think\b', note, flags=re.IGNORECASE):
        raise ValueError(f'论文 {paper["id"]} 的分段提炼为空或思考内容未闭合。')
    return note


def _condensed_papers(papers: list[dict], prompt_limit: int) -> list[dict]:
    paper_chunks = [(paper, _paper_chunks(paper['text'], paper, prompt_limit)) for paper in papers]
    tasks = [(chunk, paper) for paper, chunks in paper_chunks for chunk in chunks]
    if not tasks:
        raise ValueError('论文解析正文为空，无法提炼研究材料。')
    with ThreadPoolExecutor(max_workers=min(_MAX_WORKERS, len(tasks))) as executor:
        notes = list(executor.map(lambda task: _condense(*task, prompt_limit), tasks))

    result = []
    offset = 0
    for paper, chunks in paper_chunks:
        count = len(chunks)
        paper_notes = notes[offset:offset + count]
        offset += count
        while len(paper_notes) > 1:
            groups = group_texts_by_prompt_budget(
                paper_notes, lambda source, current_paper=paper: _note_prompt(source, current_paper), prompt_limit,
                overflow_message=f'论文 {paper["id"]} 的摘要超出合并输入预算。',
            )
            if len(groups) == len(paper_notes):
                raise ValueError(f'论文 {paper["id"]} 的摘要无法在输入预算内继续合并。')
            with ThreadPoolExecutor(max_workers=min(_MAX_WORKERS, len(groups))) as executor:
                paper_notes = list(executor.map(
                    lambda group, current_paper=paper: _condense(
                        '\n\n'.join(group), current_paper, prompt_limit,
                    )
                    if len(group) > 1 else group[0], groups,
                ))
        result.append({**paper, 'abstract': paper_notes[0]})
    return result


def _validate_question_markdown(markdown: str) -> None:
    if not markdown:
        raise ValueError('研究选题输出不能为空。')
    lines = markdown.splitlines()
    if any(lines[0].startswith(fence) and lines[-1].strip() == fence
           for fence in ('```', '~~~')):
        raise ValueError('研究选题输出不能是外层代码围栏。')
    headings = list(_SECTION_RE.finditer(markdown))
    required = ['研究问题']
    for section in required:
        matches = [(index, match) for index, match in enumerate(headings)
                   if match.group(1).strip() == section]
        if len(matches) != 1:
            raise ValueError(f'研究选题输出必须有且只有一个“{section}”二级标题。')
        index, match = matches[0]
        end = headings[index + 1].start() if index + 1 < len(headings) else len(markdown)
        if not markdown[match.end():end].strip():
            raise ValueError(f'研究选题输出的“{section}”章节不能为空。')


def run(inputs: dict) -> dict:
    """topic/request/papers/question/evidence/mode -> question, is_demo."""
    from .runtime_events import generate_markdown, report_progress
    report_progress(15, '正在整理参考论文与摘要')
    prompt_limit = _prompt_token_limit()
    papers = prepare_papers(inputs.get('papers', []) or [], prompt_limit)
    material = {key: inputs.get(key) for key in ('topic', 'request', 'papers', 'question', 'evidence', 'mode')}
    material['papers'] = [{key: paper[key] for key in ('id', 'title', 'abstract')} for paper in papers]
    from .material_bindings import MaterialBindings
    bindings = MaterialBindings(papers)
    material = bindings.model_view(material)
    guidance = ABSTRACT_PAPER_GUIDANCE if papers else NO_PAPER_GUIDANCE
    prompt = QUESTION_PROMPT.format(paper_guidance=guidance, topics='; '.join(CONTENT_TOPICS)) + (
        'Input materials (JSON):\n' + json.dumps(material, ensure_ascii=False)
    )
    if estimate_tokens(prompt) > prompt_limit:
        raise ValueError('研究选题输入超出模型预算，请减少论文或缩短其他材料。')
    report_progress(35, '正在结合文献反馈调整选题' if inputs['mode'] == 'refine' else '正在生成研究选题')
    markdown = generate_markdown(AutoModel(model='llm'), prompt, bindings=bindings)
    markdown = re.sub(r'^(?:<think\b[^>]*>.*?</think>\s*)+', '', markdown,
                      flags=re.IGNORECASE | re.DOTALL)
    if re.match(r'^<think\b', markdown, flags=re.IGNORECASE):
        raise ValueError('研究选题输出格式错误：存在未闭合的思考内容。')
    report_progress(90, '正在校验选题结构与引用')
    _validate_question_markdown(markdown)
    return {'question': markdown, 'is_demo': False}
