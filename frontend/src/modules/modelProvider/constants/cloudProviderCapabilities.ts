import type { CloudProviderType } from './cloudProviderOptions';

/** Capability chips shown on the cloud documents hub card. */
export type CloudCapabilityId =
  | 'syncKnowledge'
  | 'linkCite'
  | 'chatSearch'
  | 'syncTask'
  | 'defaultRetrieval';

export type CloudQuickActionId = 'knowledge' | 'chat';

export interface CloudProviderCapabilityConfig {
  type: CloudProviderType;
  /** Capabilities unlocked after authentication (or always for local). */
  enabledCapabilities: CloudCapabilityId[];
  /** Lightweight preview when not yet connected. */
  previewCapabilities: CloudCapabilityId[];
  /** i18n key for post-connect scenario copy */
  scenarioKey: string;
  /** i18n key for pre-connect preview scenario */
  previewScenarioKey: string;
  /** Quick jumps shown only when connected / local card is visible. */
  quickActions: CloudQuickActionId[];
}

export const CLOUD_CAPABILITY_I18N_KEYS: Record<CloudCapabilityId, string> = {
  syncKnowledge: 'modelProvider.cloudDocuments.capabilitySyncKnowledge',
  linkCite: 'modelProvider.cloudDocuments.capabilityLinkCite',
  chatSearch: 'modelProvider.cloudDocuments.capabilityChatSearch',
  syncTask: 'modelProvider.cloudDocuments.capabilitySyncTask',
  defaultRetrieval: 'modelProvider.cloudDocuments.capabilityDefaultRetrieval',
};

export const CLOUD_QUICK_ACTION_PATHS: Record<CloudQuickActionId, string> = {
  knowledge: '/lib/knowledge/list',
  chat: '/agent/chat/home',
};

export const cloudProviderCapabilityConfigs: Record<
  CloudProviderType,
  CloudProviderCapabilityConfig
> = {
  local: {
    type: 'local',
    enabledCapabilities: ['defaultRetrieval', 'syncTask', 'syncKnowledge'],
    previewCapabilities: ['defaultRetrieval', 'syncTask', 'syncKnowledge'],
    scenarioKey: 'modelProvider.cloudDocuments.localScenario',
    previewScenarioKey: 'modelProvider.cloudDocuments.localScenario',
    quickActions: ['knowledge', 'chat'],
  },
  feishu: {
    type: 'feishu',
    enabledCapabilities: ['syncKnowledge', 'linkCite', 'syncTask'],
    previewCapabilities: ['syncKnowledge', 'linkCite', 'syncTask'],
    scenarioKey: 'modelProvider.cloudDocuments.feishuScenario',
    previewScenarioKey: 'modelProvider.cloudDocuments.feishuPreviewScenario',
    quickActions: ['knowledge', 'chat'],
  },
  notion: {
    type: 'notion',
    enabledCapabilities: ['syncKnowledge', 'syncTask', 'chatSearch'],
    previewCapabilities: ['syncKnowledge', 'syncTask', 'chatSearch'],
    scenarioKey: 'modelProvider.cloudDocuments.notionScenario',
    previewScenarioKey: 'modelProvider.cloudDocuments.notionPreviewScenario',
    quickActions: ['knowledge', 'chat'],
  },
  googledrive: {
    type: 'googledrive',
    enabledCapabilities: ['chatSearch'],
    previewCapabilities: ['chatSearch'],
    scenarioKey: 'modelProvider.cloudDocuments.googleDriveScenario',
    previewScenarioKey: 'modelProvider.cloudDocuments.googleDrivePreviewScenario',
    quickActions: ['chat'],
  },
};
