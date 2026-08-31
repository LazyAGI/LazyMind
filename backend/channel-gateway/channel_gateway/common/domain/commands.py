from __future__ import annotations

from enum import Enum
from functools import reduce
from operator import or_
from typing import Annotated, Any, Literal, TypeAlias

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, model_validator


SCHEMA_VERSION = '1'
RESOLVED_RESOURCE_SELECTIONS_KEY = '_channel_gateway_resolved_resources'
RESOLVED_CONVERSATION_TARGET_KEY = '_channel_gateway_resolved_conversation'


class ActionKind(str, Enum):
    CHAT = 'chat'
    NEW = 'conversation.new'
    LIST = 'conversation.list'
    SWITCH = 'conversation.switch'
    CURRENT = 'conversation.current'
    STOP = 'conversation.stop'
    HISTORY_MORE = 'history.more'
    SELECTION_CHOOSE = 'selection.choose'
    CAPABILITY_LIST = 'capability.list'
    CAPABILITY_CONFIGURE = 'capability.configure'
    CONVERSATION_SETTINGS = 'conversation.settings'
    CONVERSATION_SETTINGS_UPDATE = 'conversation.settings.update'


ResourceType = Literal[
    'knowledge_base',
    'skill',
    'workflow',
    'tool',
    'prompt',
    'conversation',
    'personalization',
]
AssistantProvider: TypeAlias = Literal[
    'lazymind',
    'codex',
    'cursor',
    'workbuddy',
]
ASSISTANT_PROVIDERS: tuple[AssistantProvider, ...] = (
    'lazymind',
    'codex',
    'cursor',
    'workbuddy',
)
Evidence: TypeAlias = Annotated[str, Field(min_length=1, max_length=300)]
GroundingMessage: TypeAlias = Annotated[str, Field(min_length=1, max_length=4000)]
PreparedResourcePosition: TypeAlias = Annotated[
    str,
    Field(pattern=r'^[0-7]$'),
]


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


class _ClearableResourceChangeBase(_ResourceChangeBase):
    model_config = ConfigDict(
        extra='forbid',
        frozen=True,
        json_schema_extra=_CLEAR_SELECTOR_SCHEMA,
    )

    selector: ResourceSelector | None = None

    @model_validator(mode='after')
    def validate_selector(self) -> '_ClearableResourceChangeBase':
        _validate_clear_selector(self.operation, self.selector)
        return self


class KnowledgeBaseResourceChange(_ClearableResourceChangeBase):
    resource_type: Literal['knowledge_base']
    operation: Literal['use', 'disable', 'clear']
    scope: Literal['turn', 'conversation', 'global'] = Field(
        description=(
            'turn applies once, conversation persists in the selected conversation, '
            'global changes the user default.'
        )
    )


class SkillResourceChange(_NamedResourceChangeBase):
    resource_type: Literal['skill']
    operation: Literal['use'] = Field(
        description='Messaging channels only select a Skill for one turn.'
    )
    scope: Literal['turn'] = Field(
        description='Messaging-channel Skill selection applies to one turn.'
    )


class ToolResourceChange(_ClearableResourceChangeBase):
    resource_type: Literal['tool']
    operation: Literal['use', 'disable', 'clear']
    scope: Literal['turn', 'global'] = Field(
        description='Tool changes apply to one turn or to the user global default.'
    )


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
    assistant: AssistantProvider = 'lazymind'
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
        'executor',
        'knowledge_base',
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


class ConversationWorkflowEnabledSetting(_StrictModel):
    setting: Literal['workflow_enabled']
    enabled: bool


class ConversationWorkflowModeSetting(_StrictModel):
    setting: Literal['workflow_mode']
    mode: Literal['auto', 'dynamic']


class ConversationSubagentSetting(_StrictModel):
    setting: Literal['subagent']
    enabled: bool


class ConversationExecutorSetting(_StrictModel):
    setting: Literal['executor']
    executor_id: str = Field(min_length=1, max_length=64)


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
    | ConversationWorkflowEnabledSetting
    | ConversationWorkflowModeSetting
    | ConversationSubagentSetting
    | ConversationExecutorSetting
    | AccountSkillSetting
    | AccountToolSetting
    | AccountPersonalizationSetting
    | AccountWorkflowSetting,
    Field(discriminator='setting'),
]


class ConversationSettingsUpdateParameters(_StrictModel):
    change: ConversationSettingChange
    expected_conversation_id: str | None = Field(
        default=None,
        min_length=1,
        max_length=512,
    )
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=8,
        description='Verbatim substrings requesting a persistent setting change.',
    )


class ConversationStopParameters(_StrictModel):
    evidence: list[Evidence] = Field(
        min_length=1,
        max_length=2,
        description='Verbatim substring requesting the active generation to stop.',
    )


class _CommandBase(_StrictModel):
    schema_version: Literal['1']


class ChatCommand(_CommandBase):
    command: Literal[ActionKind.CHAT]
    parameters: ChatParameters


class ConversationNewCommand(_CommandBase):
    command: Literal[ActionKind.NEW]
    parameters: ConversationNewParameters


class ConversationListCommand(_CommandBase):
    command: Literal[ActionKind.LIST]
    parameters: ConversationListParameters


class ConversationSwitchCommand(_CommandBase):
    command: Literal[ActionKind.SWITCH]
    parameters: ConversationSwitchParameters


class ConversationCurrentCommand(_CommandBase):
    command: Literal[ActionKind.CURRENT]
    parameters: ConversationCurrentParameters


class ConversationStopCommand(_CommandBase):
    command: Literal[ActionKind.STOP]
    parameters: ConversationStopParameters


class HistoryMoreCommand(_CommandBase):
    command: Literal[ActionKind.HISTORY_MORE]
    parameters: HistoryMoreParameters


class SelectionChooseCommand(_CommandBase):
    command: Literal[ActionKind.SELECTION_CHOOSE]
    parameters: SelectionChooseParameters


class CapabilityListCommand(_CommandBase):
    command: Literal[ActionKind.CAPABILITY_LIST]
    parameters: CapabilityListParameters


class CapabilityConfigureCommand(_CommandBase):
    command: Literal[ActionKind.CAPABILITY_CONFIGURE]
    parameters: CapabilityConfigureParameters


class ConversationSettingsCommand(_CommandBase):
    command: Literal[ActionKind.CONVERSATION_SETTINGS]
    parameters: ConversationSettingsParameters


class ConversationSettingsUpdateCommand(_CommandBase):
    command: Literal[ActionKind.CONVERSATION_SETTINGS_UPDATE]
    parameters: ConversationSettingsUpdateParameters


COMMAND_TYPES = (
    ChatCommand,
    ConversationNewCommand,
    ConversationListCommand,
    ConversationSwitchCommand,
    ConversationCurrentCommand,
    ConversationStopCommand,
    HistoryMoreCommand,
    SelectionChooseCommand,
    CapabilityListCommand,
    CapabilityConfigureCommand,
    ConversationSettingsCommand,
    ConversationSettingsUpdateCommand,
)
_CommandUnion = reduce(or_, COMMAND_TYPES)
CommandEnvelope: TypeAlias = Annotated[_CommandUnion, Field(discriminator='command')]

COMMAND_ADAPTER = TypeAdapter(CommandEnvelope)


class PreparedResourceItem(_StrictModel):
    id: str = Field(min_length=1, max_length=512)
    name: str = Field(default='', max_length=200)
    can_disable: bool = True


class PreparedResourceSelection(_StrictModel):
    resource_type: ResourceType
    item: PreparedResourceItem


class PreparedConversationTarget(_StrictModel):
    conversation_id: str = Field(min_length=1, max_length=512)


class SelectionContinuation(_StrictModel):
    """Validated command suspended until the user selects one displayed item."""

    schema_version: Literal['1'] = SCHEMA_VERSION
    selection_field: Literal['conversation_target', 'resource_change']
    command: dict[str, Any]
    grounding_messages: list[GroundingMessage] = Field(min_length=1, max_length=10)
    resource_change_index: int | None = Field(default=None, ge=0, le=7)
    prepared_resources: dict[
        PreparedResourcePosition,
        PreparedResourceSelection,
    ] = Field(default_factory=dict, max_length=8)
    prepared_conversation_target: PreparedConversationTarget | None = None

    @model_validator(mode='after')
    def validate_command_shape(self) -> 'SelectionContinuation':
        command = COMMAND_ADAPTER.validate_python(self.command)
        if self.selection_field == 'conversation_target':
            if not isinstance(command, ConversationSwitchCommand):
                raise ValueError('conversation target continuation requires switch command')
            if self.resource_change_index is not None:
                raise ValueError('conversation target continuation has no resource index')
            if self.prepared_resources or self.prepared_conversation_target is not None:
                raise ValueError('conversation target continuation cannot be pre-resolved')
        else:
            changes = list(getattr(command.parameters, 'resource_changes', []))
            if (
                self.resource_change_index is None
                or self.resource_change_index >= len(changes)
            ):
                raise ValueError('resource continuation index is out of range')
            for position, prepared in self.prepared_resources.items():
                prepared_index = int(position)
                if prepared_index >= self.resource_change_index:
                    raise ValueError('prepared resource must precede pending selection')
                if changes[prepared_index].resource_type != prepared.resource_type:
                    raise ValueError('prepared resource type does not match command')
            if (
                isinstance(command, ConversationSwitchCommand)
                and self.prepared_conversation_target is None
            ):
                raise ValueError('switch resource continuation requires resolved target')
        return self


def command_kind(command: CommandEnvelope) -> ActionKind:
    return command.command
