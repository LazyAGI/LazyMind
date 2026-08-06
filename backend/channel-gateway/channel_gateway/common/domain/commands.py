from __future__ import annotations

from enum import Enum
from functools import reduce
from operator import or_
from typing import Annotated, Any, ClassVar, Literal, TypeAlias

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, model_validator


SCHEMA_VERSION = '1'


class ActionKind(str, Enum):
    CHAT = 'chat'
    NEW = 'conversation.new'
    LIST = 'conversation.list'
    SWITCH = 'conversation.switch'
    CURRENT = 'conversation.current'
    HISTORY_MORE = 'history.more'
    SELECTION_CHOOSE = 'selection.choose'
    CAPABILITY_LIST = 'capability.list'
    CAPABILITY_CONFIGURE = 'capability.configure'
    CONVERSATION_SETTINGS = 'conversation.settings'
    CONVERSATION_SETTINGS_UPDATE = 'conversation.settings.update'
    WORKFLOW_INVOKE = 'workflow.invoke'
    CLARIFY = 'clarify'


ResourceType = Literal[
    'knowledge_base',
    'skill',
    'plugin',
    'tool',
    'prompt',
    'conversation',
    'personalization',
]
Evidence: TypeAlias = Annotated[str, Field(min_length=1, max_length=300)]
GroundingMessage: TypeAlias = Annotated[str, Field(min_length=1, max_length=4000)]


class _StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', frozen=True)


class IndexTarget(_StrictModel):
    kind: Literal['index']
    value: str = Field(
        pattern=r'^[1-9][0-9]*$',
        max_length=200,
        description='Positive index from the latest displayed conversation list.',
    )


class NameTarget(_StrictModel):
    kind: Literal['name']
    value: str = Field(
        min_length=1,
        max_length=200,
        description='Verbatim conversation-title phrase.',
    )


IntentTarget: TypeAlias = Annotated[
    IndexTarget | NameTarget,
    Field(discriminator='kind'),
]


class ResourceIndexSelector(_StrictModel):
    kind: Literal['index']
    value: str = Field(
        pattern=r'^[1-9][0-9]*$',
        max_length=3,
        description='Positive decimal index from state.latest_selection.',
    )


class ResourceNameSelector(_StrictModel):
    kind: Literal['name']
    value: str = Field(
        min_length=1,
        max_length=200,
        description='Verbatim configurable-resource name phrase from the user message.',
    )


ResourceSelector: TypeAlias = Annotated[
    ResourceIndexSelector | ResourceNameSelector,
    Field(discriminator='kind'),
]


class _ResourceChangeBase(_StrictModel):
    evidence: Evidence = Field(
        description='Verbatim user-message substring requesting this resource change.',
    )


class _NamedResourceChangeBase(_ResourceChangeBase):
    selector: ResourceSelector = Field(
        description='Typed resource name or displayed-list index.'
    )


_CLEAR_SELECTOR_SCHEMA = {
    'allOf': [
        {
            'if': {
                'properties': {'operation': {'const': 'clear'}},
                'required': ['operation'],
            },
            'then': {'properties': {'selector': {'type': 'null'}}},
            'else': {
                'properties': {'selector': {'not': {'type': 'null'}}},
                'required': ['selector'],
            },
        }
    ]
}


class KnowledgeBaseResourceChange(_ResourceChangeBase):
    model_config = ConfigDict(
        extra='forbid',
        frozen=True,
        json_schema_extra=_CLEAR_SELECTOR_SCHEMA,
    )

    resource_type: Literal['knowledge_base']
    selector: ResourceSelector | None = None
    operation: Literal['use', 'disable', 'clear']
    scope: Literal['turn', 'conversation', 'global'] = Field(
        description=(
            'turn applies once, conversation persists in the selected conversation, '
            'global changes the user default.'
        )
    )

    @model_validator(mode='after')
    def validate_selector(self) -> 'KnowledgeBaseResourceChange':
        _validate_clear_selector(self.operation, self.selector)
        return self


class SkillResourceChange(_NamedResourceChangeBase):
    resource_type: Literal['skill']
    operation: Literal['use'] = Field(
        description='Messaging channels only select a Skill for one turn.'
    )
    scope: Literal['turn'] = Field(
        description='Messaging-channel Skill selection applies to one turn.'
    )


class ToolResourceChange(_ResourceChangeBase):
    model_config = ConfigDict(
        extra='forbid',
        frozen=True,
        json_schema_extra=_CLEAR_SELECTOR_SCHEMA,
    )

    resource_type: Literal['tool']
    selector: ResourceSelector | None = None
    operation: Literal['use', 'disable', 'clear']
    scope: Literal['turn', 'global'] = Field(
        description='Tool changes apply to one turn or to the user global default.'
    )

    @model_validator(mode='after')
    def validate_selector(self) -> 'ToolResourceChange':
        _validate_clear_selector(self.operation, self.selector)
        return self


class PersonalizationResourceChange(_ResourceChangeBase):
    resource_type: Literal['personalization']
    operation: Literal['use', 'disable'] = Field(
        description='use enables personal habits; disable turns them off.'
    )
    scope: Literal['turn', 'global'] = Field(
        description='Personalization changes apply to one turn or to the global default.'
    )


ResourceChange: TypeAlias = Annotated[
    KnowledgeBaseResourceChange
    | SkillResourceChange
    | ToolResourceChange
    | PersonalizationResourceChange,
    Field(discriminator='resource_type'),
]
RESOURCE_CHANGE_ADAPTER = TypeAdapter(ResourceChange)


def _validate_clear_selector(
    operation: str,
    selector: ResourceSelector | None,
) -> None:
    if operation == 'clear' and selector is not None:
        raise ValueError('clear never accepts a resource selector')
    if operation != 'clear' and selector is None:
        raise ValueError('use and disable require a resource selector')


class ChatParameters(_StrictModel):
    message: str = Field(
        min_length=1,
        max_length=4000,
        description=(
            'Verbatim task sent to Chat. When resource_changes is empty this must equal the '
            'entire input.message character for character, including negated, hypothetical, '
            'or explanatory control words; never summarize, clean, or omit any part.'
        ),
    )
    resource_changes: list[ResourceChange] = Field(
        default_factory=list,
        max_length=8,
        description='Optional resource settings applied to this chat turn.',
    )


class ConversationNewParameters(_StrictModel):
    message: str = Field(
        default='',
        max_length=4000,
        description='Verbatim task to execute immediately; empty waits for the next message.',
    )
    resource_changes: list[ResourceChange] = Field(default_factory=list, max_length=8)
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings explicitly requesting a new conversation.',
    )


class ConversationListParameters(_StrictModel):
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings explicitly requesting the conversation list.',
    )


class ConversationSwitchParameters(_StrictModel):
    target: IntentTarget
    message: str = Field(
        default='',
        max_length=4000,
        description=(
            'Verbatim task to execute after switching. It must be non-empty whenever the '
            'input asks for any work in addition to changing conversations.'
        ),
    )
    resource_changes: list[ResourceChange] = Field(default_factory=list, max_length=8)
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings explicitly requesting the switch.',
    )


class ConversationCurrentParameters(_StrictModel):
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings asking which conversation is current.',
    )


class HistoryMoreParameters(_StrictModel):
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings requesting older history in the current conversation.',
    )


class SelectionChooseParameters(_StrictModel):
    index: str = Field(
        pattern=r'^[1-9][0-9]*$',
        max_length=3,
        description='Positive index selected from state.latest_selection.',
    )
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=4,
        description='Verbatim substrings selecting the displayed index.',
    )


class CapabilityListParameters(_StrictModel):
    capabilities: list[ResourceType] = Field(
        min_length=1,
        max_length=7,
        description='Configurable resource categories whose names or status should be listed.',
    )
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings requesting configurable resource names or status.',
    )


class CapabilityConfigureParameters(_StrictModel):
    resource_changes: list[ResourceChange] = Field(min_length=1, max_length=8)
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings explicitly requesting resource configuration.',
    )


class ConversationSettingsParameters(_StrictModel):
    section: Literal[
        'overview',
        'knowledge_base',
        'plugin',
        'subagent',
        'skill',
        'tool',
        'personalization',
        'workflow',
    ] = 'overview'
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings requesting persistent settings used by the current conversation.',
    )


class ConversationKnowledgeBaseSetting(_StrictModel):
    setting: Literal['knowledge_base']
    dataset_id: str = Field(min_length=1, max_length=512)
    enabled: bool


class ConversationPluginSetting(_StrictModel):
    setting: Literal['plugin']
    enabled: bool


class ConversationPluginModeSetting(_StrictModel):
    setting: Literal['plugin_mode']
    mode: Literal['auto', 'dynamic']


class ConversationSubagentSetting(_StrictModel):
    setting: Literal['subagent']
    enabled: bool


class AccountSkillSetting(_StrictModel):
    setting: Literal['skill']
    skill_id: str = Field(min_length=1, max_length=512)
    enabled: bool


class AccountToolSetting(_StrictModel):
    setting: Literal['tool']
    tool_name: str = Field(min_length=1, max_length=512)
    enabled: bool


class AccountPersonalizationSetting(_StrictModel):
    setting: Literal['personalization']
    enabled: bool


class AccountWorkflowSetting(_StrictModel):
    setting: Literal['workflow']
    workflow_ref: str = Field(min_length=1, max_length=512)
    enabled: bool


ConversationSettingChange: TypeAlias = Annotated[
    ConversationKnowledgeBaseSetting
    | ConversationPluginSetting
    | ConversationPluginModeSetting
    | ConversationSubagentSetting
    | AccountSkillSetting
    | AccountToolSetting
    | AccountPersonalizationSetting
    | AccountWorkflowSetting,
    Field(discriminator='setting'),
]


class ConversationSettingsUpdateParameters(_StrictModel):
    change: ConversationSettingChange
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings requesting a persistent setting change.',
    )


class WorkflowInvokeParameters(_StrictModel):
    workflow_ref: str = Field(
        min_length=1,
        max_length=512,
        description=(
            'Exact workflow ref from state.available_workflows. Never invent or '
            'rewrite a workflow ref.'
        ),
    )
    message: str = Field(
        min_length=1,
        max_length=4000,
        description=(
            'The complete verbatim user task to execute with the selected workflow.'
        ),
    )
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description=(
            'Verbatim substrings showing that the selected workflow directly '
            'matches the user request.'
        ),
    )


class ClarifyParameters(_StrictModel):
    clarification_question: str = Field(
        min_length=1,
        max_length=300,
        description='Short question needed before a command can be executed safely.',
    )


class _CommandBase(_StrictModel):
    schema_version: Literal['1']


_CHAT_DESCRIPTION = (
    'Answer a question or perform work in the current conversation. This includes questions '
    'about contents, documents, files, records, topics, or information inside the active or '
    'default knowledge base, questions explaining channel controls, and negated or hypothetical '
    'control statements; it does not list configurable resource names or enabled status.'
)
_NEW_DESCRIPTION = (
    'Start a new conversation only when explicitly requested. An optional task in the same '
    'message is executed immediately in that new conversation.'
)
_LIST_DESCRIPTION = 'List the user conversations so the user can inspect or select one.'
_SWITCH_DESCRIPTION = (
    'Switch to a previously displayed conversation by index or to a uniquely named '
    'conversation. Return the complete command when the input also contains a task or resource '
    'change, or when state.latest_selection has no suspended continuation.'
)
_CURRENT_DESCRIPTION = 'Report which conversation is currently selected on this channel.'
_HISTORY_DESCRIPTION = 'Load older history from the currently selected conversation.'
_SELECTION_CHOOSE_DESCRIPTION = (
    'Choose one numbered item only when state.latest_selection.has_continuation is true and '
    'the entire input solely answers that pending selection. The Gateway resumes the suspended '
    'command. If there is no continuation, or the input adds a task, operation, scope, or other '
    'change, return the complete conversation.switch or capability.configure command instead.'
)
_CAPABILITY_LIST_DESCRIPTION = (
    'List names or enabled status of configurable knowledge bases, Skills, tools, or '
    'personalization. Never use this to answer what content is stored inside a knowledge base.'
)
_CAPABILITY_CONFIGURE_DESCRIPTION = (
    'Select, enable, disable, or clear an explicitly named configurable resource for a '
    'supported turn, conversation, or global scope. Return the complete command for a new '
    'request, including a numeric selector from a displayed list. Use selection.choose only '
    'for a pure answer to state.latest_selection.has_continuation.'
)
_CONVERSATION_SETTINGS_DESCRIPTION = (
    'Show persistent settings for the current conversation and account: knowledge bases, '
    'Plugin execution mode, SubAgent, Skills, tools, personalization, and workflows. '
    'This never selects a resource for only the next turn.'
)
_CONVERSATION_SETTINGS_UPDATE_DESCRIPTION = (
    'Persistently update one setting for the current conversation or account. '
    'This never performs one-turn capability invocation.'
)
_WORKFLOW_INVOKE_DESCRIPTION = (
    'Invoke one available workflow when it directly matches the primary requested '
    'deliverable. Select workflow_ref exactly from state.available_workflows and '
    'preserve the complete user task verbatim. Do not use for an incidental sub-step, '
    'for a workflow absent from the supplied catalog, or when the user explicitly '
    'rejects that workflow.'
)
_CLARIFY_DESCRIPTION = (
    'Ask one clarification question when no command and required parameters can be selected '
    'safely. It performs no other action.'
)


class ChatCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CHAT
    description: ClassVar[str] = _CHAT_DESCRIPTION
    command: Literal[ActionKind.CHAT] = Field(description=_CHAT_DESCRIPTION)
    parameters: ChatParameters


class ConversationNewCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.NEW
    description: ClassVar[str] = _NEW_DESCRIPTION
    command: Literal[ActionKind.NEW] = Field(description=_NEW_DESCRIPTION)
    parameters: ConversationNewParameters


class ConversationListCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.LIST
    description: ClassVar[str] = _LIST_DESCRIPTION
    command: Literal[ActionKind.LIST] = Field(description=_LIST_DESCRIPTION)
    parameters: ConversationListParameters


class ConversationSwitchCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.SWITCH
    description: ClassVar[str] = _SWITCH_DESCRIPTION
    command: Literal[ActionKind.SWITCH] = Field(description=_SWITCH_DESCRIPTION)
    parameters: ConversationSwitchParameters


class ConversationCurrentCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CURRENT
    description: ClassVar[str] = _CURRENT_DESCRIPTION
    command: Literal[ActionKind.CURRENT] = Field(description=_CURRENT_DESCRIPTION)
    parameters: ConversationCurrentParameters


class HistoryMoreCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.HISTORY_MORE
    description: ClassVar[str] = _HISTORY_DESCRIPTION
    command: Literal[ActionKind.HISTORY_MORE] = Field(description=_HISTORY_DESCRIPTION)
    parameters: HistoryMoreParameters


class SelectionChooseCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.SELECTION_CHOOSE
    description: ClassVar[str] = _SELECTION_CHOOSE_DESCRIPTION
    command: Literal[ActionKind.SELECTION_CHOOSE] = Field(
        description=_SELECTION_CHOOSE_DESCRIPTION
    )
    parameters: SelectionChooseParameters


class CapabilityListCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CAPABILITY_LIST
    description: ClassVar[str] = _CAPABILITY_LIST_DESCRIPTION
    command: Literal[ActionKind.CAPABILITY_LIST] = Field(
        description=_CAPABILITY_LIST_DESCRIPTION
    )
    parameters: CapabilityListParameters


class CapabilityConfigureCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CAPABILITY_CONFIGURE
    description: ClassVar[str] = _CAPABILITY_CONFIGURE_DESCRIPTION
    command: Literal[ActionKind.CAPABILITY_CONFIGURE] = Field(
        description=_CAPABILITY_CONFIGURE_DESCRIPTION
    )
    parameters: CapabilityConfigureParameters


class ConversationSettingsCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CONVERSATION_SETTINGS
    description: ClassVar[str] = _CONVERSATION_SETTINGS_DESCRIPTION
    command: Literal[ActionKind.CONVERSATION_SETTINGS] = Field(
        description=_CONVERSATION_SETTINGS_DESCRIPTION
    )
    parameters: ConversationSettingsParameters


class ConversationSettingsUpdateCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CONVERSATION_SETTINGS_UPDATE
    description: ClassVar[str] = _CONVERSATION_SETTINGS_UPDATE_DESCRIPTION
    command: Literal[ActionKind.CONVERSATION_SETTINGS_UPDATE] = Field(
        description=_CONVERSATION_SETTINGS_UPDATE_DESCRIPTION
    )
    parameters: ConversationSettingsUpdateParameters


class WorkflowInvokeCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.WORKFLOW_INVOKE
    description: ClassVar[str] = _WORKFLOW_INVOKE_DESCRIPTION
    command: Literal[ActionKind.WORKFLOW_INVOKE] = Field(
        description=_WORKFLOW_INVOKE_DESCRIPTION
    )
    parameters: WorkflowInvokeParameters


class ClarifyCommand(_CommandBase):
    name: ClassVar[ActionKind] = ActionKind.CLARIFY
    description: ClassVar[str] = _CLARIFY_DESCRIPTION
    command: Literal[ActionKind.CLARIFY] = Field(description=_CLARIFY_DESCRIPTION)
    parameters: ClarifyParameters


COMMAND_TYPES = (
    ChatCommand,
    ConversationNewCommand,
    ConversationListCommand,
    ConversationSwitchCommand,
    ConversationCurrentCommand,
    HistoryMoreCommand,
    SelectionChooseCommand,
    CapabilityListCommand,
    CapabilityConfigureCommand,
    ConversationSettingsCommand,
    ConversationSettingsUpdateCommand,
    WorkflowInvokeCommand,
    ClarifyCommand,
)
_CommandUnion = reduce(or_, COMMAND_TYPES)
CommandEnvelope: TypeAlias = Annotated[_CommandUnion, Field(discriminator='command')]

COMMAND_ADAPTER = TypeAdapter(CommandEnvelope)

COMMAND_SELECTION_RULES = (
    'Classify the whole input and preserve every requested operation and parameter.',
    'Use conversation.settings or conversation.settings.update for persistent settings of the '
    'current conversation. Never translate those requests into a one-turn resource change.',
    'Use capability.configure when the entire input only changes resource settings. Use '
    'chat with resource_changes only when an independent question or task must also run.',
    'A conversation.new or conversation.switch input that also requests work must include '
    'that verbatim work in parameters.message.',
    'Use capability.list only to enumerate configurable resource names or enabled state; '
    'questions about information stored inside a resource are chat.',
    'Use selection.choose only for a pure numbered answer when '
    'state.latest_selection.has_continuation is true.',
    'Use workflow.invoke only with an exact ref from state.available_workflows and '
    'only when that workflow directly serves the primary requested deliverable.',
)


class SelectionContinuation(_StrictModel):
    """Validated command suspended until the user selects one displayed item."""

    schema_version: Literal['1'] = SCHEMA_VERSION
    selection_field: Literal['conversation_target', 'resource_change']
    command: dict[str, Any]
    grounding_messages: list[GroundingMessage] = Field(min_length=1, max_length=10)
    resource_change_index: int | None = Field(default=None, ge=0, le=7)
    prepared_catalog: dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode='after')
    def validate_command_shape(self) -> 'SelectionContinuation':
        command = COMMAND_ADAPTER.validate_python(self.command)
        if self.selection_field == 'conversation_target':
            if not isinstance(command, ConversationSwitchCommand):
                raise ValueError('conversation target continuation requires switch command')
            if self.resource_change_index is not None:
                raise ValueError('conversation target continuation has no resource index')
        else:
            changes = list(getattr(command.parameters, 'resource_changes', []))
            if (
                self.resource_change_index is None
                or self.resource_change_index >= len(changes)
            ):
                raise ValueError('resource continuation index is out of range')
        return self


def command_registry(
    allowed_commands: set[ActionKind] | None = None,
) -> dict[str, Any]:
    command_types = tuple(
        command_type
        for command_type in COMMAND_TYPES
        if (
            allowed_commands is None
            or command_type.name in allowed_commands
        )
    )
    if not command_types:
        raise ValueError('command registry must contain at least one command')
    adapter = (
        COMMAND_ADAPTER
        if command_types == COMMAND_TYPES
        else TypeAdapter(
            Annotated[
                reduce(or_, command_types),
                Field(discriminator='command'),
            ]
        )
    )
    return {
        'schema_version': SCHEMA_VERSION,
        'commands': [
            {'name': command_type.name.value, 'description': command_type.description}
            for command_type in command_types
        ],
        'selection_rules': list(COMMAND_SELECTION_RULES),
        'output_schema': adapter.json_schema(),
    }


def command_kind(command: CommandEnvelope) -> ActionKind:
    return command.command
