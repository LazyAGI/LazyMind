"""Audited paper tool metadata and lazy execution boundary; no paper engine at import time."""
from importlib import import_module

from lazymind.chat.workflow.external_ppt_tools import ExternalToolError

PAPER_BINDING = '''"""Audited paper primitives; the executing Agent owns all generation and judgment."""
from lazymind.document_tools.paper.agent_tools import (
    paper_import_sources,
    paper_analyze, paper_plot, paper_prepare_candidate, paper_prepare_inputs,
    paper_execution_status, paper_cancel_execution, paper_preview_revision,
    paper_knowledge_documents, paper_knowledge_read,
    paper_read_material, paper_read_paper, paper_run_experiment, paper_search,
    paper_submit_candidate, paper_read_result,
)
'''

# Existing immutable revisions keep their original audited import binding.
LEGACY_PAPER_BINDING = PAPER_BINDING.replace('    paper_import_sources,\n', '')

TOOL_NAMES = frozenset((
    'paper_import_sources',
    'paper_prepare_inputs',
    'paper_read_material',
    'paper_search',
    'paper_read_paper',
    'paper_knowledge_documents',
    'paper_knowledge_read',
    'paper_analyze',
    'paper_plot',
    'paper_run_experiment',
    'paper_prepare_candidate',
    'paper_execution_status',
    'paper_cancel_execution',
    'paper_preview_revision',
))
STEPS = frozenset(('research_topic', 'literature_review', 'refine_topic', 'experiment',
                   'data_analysis', 'write_outline', 'write_document', 'peer_review',
                   'revise_document', 'reading_reflection', 'finish'))


def validate_step(step_id, step):
    if step_id not in STEPS or step.get('output_validator') != 'paper-agent-v1':
        raise ExternalToolError('TOOL_NOT_ALLOWED', 'This step does not declare the paper Agent contract.')


def load(name):
    try:
        return getattr(import_module('lazymind.document_tools.paper.agent_tools'), name)
    except (ImportError, AttributeError) as exc:
        raise ExternalToolError('TOOL_ADAPTER_UNAVAILABLE', 'The paper tool adapter is unavailable.') from exc


def call(request, function, arguments):
    value = request.trusted_context
    if not isinstance(value, dict) or any(value.get(key) != expected for key, expected in (
            ('revision', request.revision_id), ('step_id', request.step_id))):
        raise ExternalToolError('INVALID_ARGUMENT', 'A matching Core-owned paper context is required.', public=True)
    module = import_module('lazymind.document_tools.paper.agent_tools')
    try:
        with module.paper_scope(value):
            return {'result': function(**arguments)}
    except SyntaxError as exc:
        message = (f'Python syntax error at line {exc.lineno}, column {exc.offset}: {exc.msg}. '
                   'Correct the code and call again.')
        raise ExternalToolError('INVALID_ARGUMENT', message, public=True) from exc
    except ValueError as exc:
        raise ExternalToolError('INVALID_ARGUMENT', str(exc), public=True) from exc
