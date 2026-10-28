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
    saved = _save_artifact('ppt_request', result, content_type='json',
                           source_tool='academic_handoff_submit', internal_publish=True)
    return {'status': result['status'], 'kind': result['kind'], 'paper_count': len(result['input']['papers']),
            'feedback': result['feedback'], 'artifact': saved}
