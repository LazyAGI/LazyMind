"""Publish the current Chat's frozen paper handoff in this Workflow's format."""
from lazymind.chat.service.academic_handoff import (
    academic_handoff_context,
    academic_read_paper,
    submit_handoff,
)


__all__ = ['academic_handoff_context', 'academic_read_paper', 'academic_handoff_submit']


def academic_handoff_submit():
    """Submit and publish the frozen handoff; return its handoff receipt."""
    result = submit_handoff()
    from lazymind.chat.engine.subagent.tools import _save_artifact
    import json
    body = ('## 任务交接\n\n' + result['feedback'] + '\n\n```json\n'
            + json.dumps(result, ensure_ascii=False, indent=2) + '\n```')
    saved = _save_artifact('handoff_receipt', body, content_type='text',
                           source_tool='academic_handoff_submit', internal_publish=True)
    return {'status': result['status'], 'kind': result['kind'], 'paper_count': len(result['input']['papers']),
            'feedback': result['feedback'], 'artifact': saved}
