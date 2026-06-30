from __future__ import annotations

from typing import Any, Dict, List, Optional, Union

from pydantic import BaseModel, Field

from lazymind.chat.config import DEFAULT_CHAT_DATASET


class ChatMessageOptions(BaseModel):
    query: str
    history: Optional[List[Dict[str, Any]]] = None
    files: Optional[Dict[str, List[str]]] = None
    current_turn_seq: Optional[int] = None


class ChatConversationOptions(BaseModel):
    session_id: str = 'session_id'
    conversation_id: Optional[str] = None
    user_id: Optional[str] = None
    mode: Optional[str] = 'auto'


class ChatRetrievalOptions(BaseModel):
    filters: Optional[Dict[str, Any]] = None
    databases: Optional[List[Dict[str, Any]]] = None
    dataset: Optional[str] = DEFAULT_CHAT_DATASET
    local_fs_sources: Optional[List[Dict[str, Any]]] = None


class ChatRuntimeOptions(BaseModel):
    debug: Optional[bool] = False
    reasoning: Optional[bool] = False
    priority: Optional[int] = None
    trace: Optional[bool] = False
    environment_context: Optional[Dict[str, Any]] = None
    llm_config: Optional[Dict[str, Any]] = None
    tool_config: Optional[Dict[str, Union[str, List[str]]]] = None
    mcp_config: Optional[List[Dict[str, Any]]] = None


class ChatPersonalizationOptions(BaseModel):
    use_memory: Optional[bool] = True
    memory: Optional[str] = None
    user_preference: Optional[str] = None


class ChatAgentOptions(BaseModel):
    disabled_tools: Optional[List[str]] = None
    available_skills: Optional[List[str]] = None
    has_subagents: Optional[bool] = False
    enable_subagent: Optional[bool] = None


class ChatPluginOptions(BaseModel):
    enable_plugin: Optional[bool] = None
    plugin_context: Optional[Dict[str, Any]] = None
    ask_response: Optional[Dict[str, Any]] = None


class ChatRequest(BaseModel):
    message: ChatMessageOptions
    conversation: ChatConversationOptions = Field(default_factory=ChatConversationOptions)
    retrieval: ChatRetrievalOptions = Field(default_factory=ChatRetrievalOptions)
    runtime: ChatRuntimeOptions = Field(default_factory=ChatRuntimeOptions)
    personalization: ChatPersonalizationOptions = Field(default_factory=ChatPersonalizationOptions)
    agent: ChatAgentOptions = Field(default_factory=ChatAgentOptions)
    plugin: ChatPluginOptions = Field(default_factory=ChatPluginOptions)
