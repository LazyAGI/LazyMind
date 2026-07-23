"""SubAgent infrastructure: per-task DB persistence, tools, and ReAct runner."""

SUBAGENT_CORE_TOOL_NAMES = (
    'save_artifact',
    'get_artifact',
    'list_artifacts',
    'list_knowledge_bases',
    'read_user_attachment',
    'find_user_attachment',
    'string_replace',
    'find_artifact',
    'patch_artifact',
    'discard_draft',
)

SUBAGENT_ATTACHMENT_CONTEXT_KEY = '_attachment_context'
