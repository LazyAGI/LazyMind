"""Host-owned SubAgent extensions; the runner consumes only this generic contract."""
from dataclasses import dataclass, field
from typing import Any, Callable


@dataclass(frozen=True)
class RuntimeExtension:
    tools: Callable[[], list[Any]] | None = None
    output_instruction: str = ''
    publisher_owned_outputs: bool = False
    inherit_skills: bool = True
    tool_failure_limits: dict[str, int] = field(default_factory=dict)
    tool_call_limits: dict[str, int] = field(default_factory=dict)
    on_tool_results: Callable[[dict, dict], None] | None = None


def resolve_runtime_extension(agent_type: str = '', params: dict | None = None) -> RuntimeExtension:
    # Resolve only host-owned implementations, never import paths from task input.
    # Preserve selection for persisted tasks carrying the original session binding.
    if agent_type == 'pptx_edit' or (params or {}).get('_pptx_edit_session_id'):
        from .pptx_edit import runtime_extension
        return runtime_extension()
    return RuntimeExtension()
