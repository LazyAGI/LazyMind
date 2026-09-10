"""Vocabulary review tools that reuse the ask panel without exposing ask_user."""
from __future__ import annotations

import uuid
from typing import Any, Dict, List, Literal, Optional

from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.engine.tools.ask_user import _normalise_questions
from lazymind.chat.engine.tools.infra.core_api_client import get_core_api, post_core_api


def _data(result: Dict[str, Any]) -> Dict[str, Any]:
    body = result.get('response', result)
    if isinstance(body, dict) and isinstance(body.get('data'), dict):
        return body['data']
    return body if isinstance(body, dict) else {}


def get_review_words(count: int = 5) -> Dict[str, Any]:
    """Create/resume a backend review session and preview its next 1-200 words.

    Previewing does not consume words. For a cloze exercise request at least 20
    candidates in one call; ask_words signs only words used as correct answers.
    """
    count = max(1, min(int(count or 5), 200))
    batch = get_core_api('/vocabulary/review/sessions/active/candidates', {'count': count})
    session = batch.get('session') or {}
    session_id = str(session.get('id') or '')
    items = batch.get('questions') or []
    words = [
        {
            'word_id': (item.get('word') or {}).get('id'),
            'term': (item.get('word') or {}).get('term'),
            'meaning': (item.get('word') or {}).get('meaning'),
        }
        for item in items
    ]
    response = {
        'words': words,
        'remaining': len(words),
        'complete': len(words) == 0,
        'cloze_hint': (
            'For type=cloze, call get_review_words once with count >= 20, then use '
            '10-20 unique candidate terms as structured correct_answer values. '
            'Only those correct answers are signed by ask_words.'
        ),
    }
    if not words and session_id:
        response['report'] = finish_review_session(session_id)
    return response


def ask_words(
    mode: Literal['e2c', 'c2e', 'create'],
    type: Literal['choice', 'fill', 'cloze'],
    words: Optional[List[str]] = None,
    questions: Optional[List[Dict[str, Any]]] = None,
    title: Optional[str] = None,
    description: Optional[str] = None,
) -> str:
    """Present a vocabulary batch through the ask panel and stop this turn.

    Objective modes are e2c (choice only) and c2e (choice or fill). The backend
    creates the questions and records submitted answers automatically. Mode create
    accepts model-authored ask-panel questions and requires register_review_words
    on the following turn.
    """
    mode = str(mode or '').strip().lower()
    question_type = str(type or '').strip().lower()
    target_terms = list(words or [])
    if mode == 'create':
        target_terms = [
            str(q.get('correct_answer') if question_type == 'cloze' else q.get('word') or '').strip()
            for q in questions or []
        ]
    target_terms = list(dict.fromkeys(target_terms))
    if not target_terms or any(not term for term in target_terms):
        raise RuntimeError('ask_words requires target words from get_review_words')
    issued = _data(post_core_api(
        '/vocabulary/review/sessions/active/words:issue',
        {'terms': target_terms},
    ))
    session_id = str((issued.get('session') or {}).get('id') or '')
    issued_by_term = {
        str(item.get('term') or '').strip().casefold(): item
        for item in issued.get('items') or []
    }
    if not session_id:
        raise RuntimeError('active vocabulary review session was not created')
    if mode == 'create':
        if not questions:
            raise RuntimeError('questions are required when mode=create')
        weights = {'basic': 3.0, 'intermediate': 2.0, 'advanced': 1.0}
        if question_type == 'cloze':
            correct_answers = [str(q.get('correct_answer') or '').strip() for q in questions]
            if any(not answer for answer in correct_answers):
                raise RuntimeError('every cloze question requires correct_answer')
        hook_items = []
        for index, question in enumerate(questions):
            if question_type == 'cloze':
                answer = str(question.get('correct_answer') or '').strip()
                word_id = str((issued_by_term.get(answer.casefold()) or {}).get('word_id') or '')
            else:
                word = str(question.get('word') or '').strip()
                word_id = str((issued_by_term.get(word.casefold()) or {}).get('word_id') or '')
            criteria = str(question.get('grading_criteria') or '').strip()
            difficulty = str(question.get('difficulty') or '').strip().lower()
            if not word_id:
                raise RuntimeError(f'question {index} does not match an issued word')
            if difficulty not in weights:
                raise RuntimeError(
                    f'question {index} difficulty must be basic, intermediate, or advanced'
                )
            if not criteria:
                raise RuntimeError(f'question {index} requires grading_criteria')
            hook_items.append({
                'question_index': index,
                'word_id': word_id,
                'difficulty': difficulty,
                # Weight is assigned by the tool, never by the model.
                'weight': weights[difficulty],
                'grading_criteria': criteria,
            })
        panel_questions = questions
        if question_type == 'cloze':
            # Correct answers are stored by Core during issuance. Keep them and
            # grading criteria out of the browser-visible ask payload entirely.
            hook = {
                'kind': 'vocabulary_review_objective',
                'session_id': session_id,
                'items': [
                    {
                        'question_index': item['question_index'],
                        'word_id': item['word_id'],
                    }
                    for item in hook_items
                ],
            }
        else:
            hook = {
                'kind': 'vocabulary_review_llm',
                'session_id': session_id,
                'items': hook_items,
            }
    else:
        prepared = _data(post_core_api(
            f'/vocabulary/review/sessions/{session_id}/questions:prepare',
            {'mode': mode, 'question_type': question_type},
        ))
        raw_questions = prepared.get('questions') or []
        panel_questions = [
            {
                'text': q['text'],
                'type': q['type'],
                'choices': q.get('choices') or [],
                **({'allow_other': False} if q['type'] == 'single' else {}),
            }
            for q in raw_questions
        ]
        hook = {
            'kind': 'vocabulary_review_objective',
            'session_id': session_id,
            'items': [
                {'question_index': index, 'word_id': q['word_id']}
                for index, q in enumerate(raw_questions)
            ],
        }
    ask_id = str(uuid.uuid4())
    panel_questions = _normalise_questions(panel_questions)
    payload: Dict[str, Any] = {
        'ask_id': ask_id,
        'questions': panel_questions,
        'review_hook': hook,
        'title': title or '单词复习',
    }
    if description:
        payload['description'] = description
    _write_agent_data('ask_pending', **payload)
    return f'Vocabulary questions sent (ask_id={ask_id}). Waiting for the user.'


def register_review_words(results: List[Dict[str, Any]]) -> str:
    """Register weighted judgments, then return a natural-language next step.

    Each result represents one generated question and requires word_id,
    correct, and the tool-issued weight. Multiple questions for the same word
    are combined before the backend is updated. After all registrations,
    this tool calls get_review_words itself so the model must not explore session
    state in a separate step.
    """
    grouped: Dict[str, Dict[str, float]] = {}
    for result in results or []:
        item_id = str(result.get('word_id') or '').strip()
        if not item_id:
            raise RuntimeError('each result requires word_id')
        weight = float(result.get('weight') or 0)
        if weight <= 0:
            raise RuntimeError('each result requires a positive tool-issued weight')
        bucket = grouped.setdefault(item_id, {'earned': 0.0, 'possible': 0.0})
        bucket['possible'] += weight
        if bool(result.get('correct')):
            bucket['earned'] += weight
    for item_id, score_parts in grouped.items():
        score = score_parts['earned'] / score_parts['possible']
        _data(post_core_api(
            '/vocabulary/review/sessions/active/answers:register',
            {'word_id': item_id, 'score': score},
        ))
    next_batch = get_review_words(count=5)
    if next_batch['complete'] and next_batch['remaining'] == 0:
        return _format_review_report(next_batch.get('report') or {})
    lines = [
        '用户已经回答上一批模型生成的复习题，判断结果已登记。'
        '不要复述或再次处理上一批答案。',
        '后端返回了以下下一批候选词，请根据学习需要选择题型和单词，'
        '并调用 ask_words 继续出题：',
    ]
    lines.extend(
        f"- {word.get('term') or ''}：{word.get('meaning') or ''}"
        for word in next_batch['words']
    )
    lines.append('这些词目前只是候选；只有交给 ask_words 的词才算本批正式使用。')
    return '\n'.join(lines)


def _format_review_report(report: Dict[str, Any]) -> str:
    total = int(report.get('total') or 0)
    correct = int(report.get('correct') or 0)
    incorrect = int(report.get('incorrect') or 0)
    accuracy = float(report.get('accuracy') or 0) * 100
    before = float(report.get('average_interval_before_days') or 0)
    after = float(report.get('average_interval_after_days') or 0)
    difficult = [str(word) for word in report.get('difficult_words') or []]
    lines = [
        '本次复习已全部完成。下面是后端生成的最终学习报告；'
        '请直接用自然语言忠实呈现，不要再出题，也不要自行重算或改写数据。',
        f'共复习 {total} 个单词，正确 {correct} 个，错误 {incorrect} 个，'
        f'正确率 {accuracy:.1f}%。',
        f'平均复习间隔由 {before:.1f} 天调整为 {after:.1f} 天。',
    ]
    if difficult:
        lines.append(f"需要重点巩固的单词：{'、'.join(difficult)}。")
    else:
        lines.append('本次没有需要额外标记的困难单词。')
    return '\n'.join(lines)


def finish_review_session(session_id: str) -> Dict[str, Any]:
    """Complete a fully answered review session and return its backend report.

    The backend rejects completion while any review item remains, so this tool
    must only be called after get_review_words returns complete=true.
    """
    return _data(post_core_api(
        f'/vocabulary/review/sessions/{session_id}:complete',
        {},
    ))


# These chat-native tools own the interactive review protocol.  The generic
# supersession pass keeps the lower-level MCP primitives available to other
# clients while preventing an agent from mixing both protocols in one chat.
get_review_words.__supersedes_tools__ = (
    'vocabulary.review.start',
    'vocabulary.review.next',
)
ask_words.__supersedes_tools__ = ('vocabulary.review.answer',)
