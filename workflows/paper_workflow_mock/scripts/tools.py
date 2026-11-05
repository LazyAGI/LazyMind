"""Publish the current Chat's frozen paper handoff in this Workflow's format."""
import re
from html import escape

from lazymind.chat.service.academic_handoff import (
    academic_handoff_context,
    academic_read_paper,
    submit_handoff,
)


__all__ = ['academic_handoff_context', 'academic_read_paper', 'academic_handoff_submit']


def _markdown_text(value):
    return re.sub(r'([\\`*_{}\[\]()#+.!|>~-])', r'\\\1', escape(value, quote=False))


def academic_handoff_submit():
    """Submit and publish the frozen handoff; return its handoff receipt."""
    result = submit_handoff()
    from lazymind.chat.engine.subagent.tools import _save_artifact
    task = result['input']
    names = {'review': '论文综述', 'reflection': '阅读心得', 'ppt': '汇报 PPT'}
    lines = ['## 任务交接', '', result['feedback'], '',
             '任务：' + names[result['kind']], '', '### 用户要求', '', _markdown_text(task['user_instruction']), '',
             '### 所选论文', '']
    if result['kind'] == 'review':
        scope = task.get('review_scope')
        scope_text = ('已选论文及其直接参考文献；仅去重和检查全文、解析状态，不递归扩展，不做相关性过滤或排序。'
                      if scope == 'direct_references' else '仅使用本次确认的论文范围。')
        lines[6:6] = ['### 文献范围', '', scope_text,
                      '当前仅完成材料交接，后续阅读与正文生成待接入。', '']
    for item in task['papers']:
        paper = item['paper'] if result['kind'] == 'review' else item
        details = ' · '.join(str(paper[key]) for key in ('year', 'source_provider', 'source_version')
                             if paper.get(key))
        title = _markdown_text(paper.get('title') or '未命名论文')
        lines.append('- ' + title + ('（' + _markdown_text(details) + '）' if details else ''))
    if task.get('notes'):
        lines.extend(['', f'已携带 {len(task["notes"])} 条已有笔记。'])
    if result.get('reflection_markdown'):
        lines.extend(['', '已携带上一步心得正文。'])
    body = '\n'.join(lines)
    saved = _save_artifact('handoff_receipt', body, content_type='text',
                           source_tool='academic_handoff_submit', internal_publish=True)
    return {'status': result['status'], 'kind': result['kind'], 'paper_count': len(result['input']['papers']),
            'feedback': result['feedback'], 'artifact': saved}
