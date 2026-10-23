"""Pinned native business check; Core owns scheduling, attempts and recovery."""
import hashlib

BRIEF = (
    'Implement normalize_label(value) in labels.py: strip surrounding whitespace '
    'and lowercase; test ASCII, Unicode and empty input in a disposable directory.'
)


def copy_bound_brief():
    """Publish the exact Brief value frozen in the Core-authorized Attempt Context."""
    from lazymind.chat.engine.subagent.context import require_context
    from lazymind.chat.engine.subagent.tools import save_artifacts

    # transport:value is declared in state.yml. No model argument can replace it.
    context = require_context()
    candidate = context.params.get('remote_inputs', {}).get('brief')
    if not isinstance(candidate, dict):
        raise ValueError('BRIEF_INVALID')
    save_artifacts([{'key': 'admission', 'content_type': 'json', 'value': candidate}])
    return candidate


def check_showcase_brief(candidate):
    """Run before Runtime commits validate_brief; errors deny implement admission."""
    if not isinstance(candidate, dict):
        raise ValueError('BRIEF_INVALID')
    brief = candidate.get('brief')
    if brief is None:
        raise ValueError('BRIEF_MISSING')
    if not isinstance(brief, str):
        raise ValueError('BRIEF_INVALID')
    if not brief.strip():
        raise ValueError('BRIEF_EMPTY')
    if brief != BRIEF:
        raise ValueError('BRIEF_TASK_MISMATCH')
    return {'status': 'PASS', 'brief_sha256': hashlib.sha256(brief.encode()).hexdigest()}
