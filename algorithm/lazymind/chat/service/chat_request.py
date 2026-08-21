from __future__ import annotations

from typing import Any, Dict, List, Optional, Union

from pydantic import BaseModel, Field

from lazymind.chat.config import DEFAULT_CHAT_DATASET


class ChatMessageOptions(BaseModel):
    query: str
    user_query: Optional[str] = None
    history: Optional[List[Dict[str, Any]]] = None
    files: Optional[Dict[str, List[str]]] = None
    current_turn_seq: Optional[int] = None


class ChatConversationOptions(BaseModel):
    session_id: str = 'session_id'
    run_id: Optional[str] = None
    conversation_id: Optional[str] = None
    user_id: Optional[str] = None
    mode: Optional[str] = 'auto'
    intent_context: Optional[Dict[str, Any]] = None


class ChatRetrievalOptions(BaseModel):
    filters: Optional[Dict[str, Any]] = None
    databases: Optional[List[Dict[str, Any]]] = None
    dataset: Optional[str] = DEFAULT_CHAT_DATASET
    local_fs_sources: Optional[List[Dict[str, Any]]] = None


class ChatRuntimeOptions(BaseModel):
    debug: Optional[bool] = False
    reasoning: Optional[bool] = False
    thinking_depth: str = 'medium'
    priority: Optional[int] = None
    environment_context: Optional[Dict[str, Any]] = None
    llm_config: Optional[Dict[str, Any]] = None
    ocr_config: Optional[Dict[str, Any]] = None
    tool_config: Optional[Dict[str, Union[str, List[str]]]] = None
    mcp_config: Optional[List[Dict[str, Any]]] = None
    context_usage_preview: bool = False
    context_prompt_export: bool = False
    context_preview_allow_llm_routing: bool = False
    skip_sensitive_filter: bool = False


class ChatPersonalizationOptions(BaseModel):
    use_memory: Optional[bool] = True


class ChatAgentOptions(BaseModel):
    disabled_tools: Optional[List[str]] = None
    available_skills: Optional[List[str]] = None
    has_subagents: Optional[bool] = False
    enable_subagent: Optional[bool] = None


class ChatWorkflowOptions(BaseModel):
    enable_workflow: Optional[bool] = None
    workflow_context: Optional[Dict[str, Any]] = None
    catalog: List[Dict[str, Any]] = Field(default_factory=list)
    disabled_builtin_workflows: List[str] = Field(default_factory=list)
    allowed_workflow_refs: List[str] = Field(default_factory=list)
    activations: List[Dict[str, Any]] = Field(default_factory=list)


class ExplicitResourceBindingsOptions(BaseModel):
    skill_names: List[str] = Field(default_factory=list)
    knowledge_base_ids: List[str] = Field(default_factory=list)
    workflow_refs: List[str] = Field(default_factory=list)
    mentions: List[Dict[str, str]] = Field(default_factory=list)


class ChatRequest(BaseModel):
    message: ChatMessageOptions
    conversation: ChatConversationOptions = Field(default_factory=ChatConversationOptions)
    retrieval: ChatRetrievalOptions = Field(default_factory=ChatRetrievalOptions)
    runtime: ChatRuntimeOptions = Field(default_factory=ChatRuntimeOptions)
    personalization: ChatPersonalizationOptions = Field(default_factory=ChatPersonalizationOptions)
    agent: ChatAgentOptions = Field(default_factory=ChatAgentOptions)
    workflow: ChatWorkflowOptions = Field(default_factory=ChatWorkflowOptions)
    explicit_resource_bindings: ExplicitResourceBindingsOptions = Field(
        default_factory=ExplicitResourceBindingsOptions,
    )
