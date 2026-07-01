import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { FormInstance } from "antd/es/form";
import type { TFunction } from "i18next";
import type { NavigateFunction } from "react-router-dom";
import type {
  FeishuAccountFormValues,
  FeishuAuthAccount,
} from "../../common/feishuAccounts";
import type {
  CloudDataSourceProvider,
  FeishuDataSourceConnection,
  FeishuDataSourceOAuthMessage,
} from "@/modules/dataSource/common/feishuOAuth";
import type {
  DataSourceItem,
  FeishuAppSetup,
  OAuthState,
  PendingOAuthAttempt,
  SourceFormValues,
  SourceType,
} from "../../constants/types";
import type { ScanV2AgentHint } from "../../utils/scanAccessors";
import type { FeishuTargetTreeNode } from "../../utils/feishuTarget";

export type DataSourceView = "assets" | "connectors";
export type FeishuSetupIntent = "create" | "auth" | null;
export type CloudSetupIntent = FeishuSetupIntent;
export type DataSourceSaveMode = "create" | "createAndSync";

export interface StartCloudOAuthOptions {
  setup?: FeishuAppSetup;
  draftSelectedType?: SourceType | null;
  draftWizardStep?: number;
  draftWizardOpen?: boolean;
  draftWizardMode?: "create" | "edit";
  draftEditingId?: string | null;
  draftFormValues?: Record<string, unknown>;
  previousState?: OAuthState;
  previousVerified?: boolean;
  previousConnection?: FeishuDataSourceConnection | null;
  accountId?: string;
  appId?: string;
}

export interface RefreshSourcesOptions {
  page?: number;
  pageSize?: number;
  keyword?: string;
}

/**
 * Shared mutable context for the data source management page.
 *
 * The hook builds this object once per render with all state, setters and refs,
 * then progressively assigns the handler factories' outputs back onto it. Within
 * a handler body, cross-group handlers are accessed lazily via `ctx.someHandler`
 * so the factories can be created in any order without import cycles.
 */
export interface ManagementContext {
  // Framework helpers
  t: TFunction;
  navigate: NavigateFunction;
  form: FormInstance<SourceFormValues>;
  feishuSetupForm: FormInstance<FeishuAccountFormValues>;

  // List state
  sources: DataSourceItem[];
  setSources: Dispatch<SetStateAction<DataSourceItem[]>>;
  activeView: DataSourceView;
  setActiveView: Dispatch<SetStateAction<DataSourceView>>;
  assetSearchValue: string;
  setAssetSearchValue: Dispatch<SetStateAction<string>>;
  sourceListPage: number;
  setSourceListPage: Dispatch<SetStateAction<number>>;
  sourceListPageSize: number;
  setSourceListPageSize: Dispatch<SetStateAction<number>>;
  sourceListTotal: number;
  setSourceListTotal: Dispatch<SetStateAction<number>>;
  scanLoading: boolean;
  setScanLoading: Dispatch<SetStateAction<boolean>>;

  // Wizard state
  wizardOpen: boolean;
  setWizardOpen: Dispatch<SetStateAction<boolean>>;
  wizardStep: number;
  setWizardStep: Dispatch<SetStateAction<number>>;
  wizardMode: "create" | "edit";
  setWizardMode: Dispatch<SetStateAction<"create" | "edit">>;
  selectedType: SourceType | null;
  setSelectedType: Dispatch<SetStateAction<SourceType | null>>;
  editingId: string | null;
  setEditingId: Dispatch<SetStateAction<string | null>>;
  wizardSaving: boolean;
  setWizardSaving: Dispatch<SetStateAction<boolean>>;
  wizardSavingMode: DataSourceSaveMode | null;
  setWizardSavingMode: Dispatch<SetStateAction<DataSourceSaveMode | null>>;

  // Provider / modal state
  createProviderModalOpen: boolean;
  setCreateProviderModalOpen: Dispatch<SetStateAction<boolean>>;
  authSelectModalOpen: boolean;
  setAuthSelectModalOpen: Dispatch<SetStateAction<boolean>>;
  cloudSetupProvider: CloudDataSourceProvider;
  setCloudSetupProvider: Dispatch<SetStateAction<CloudDataSourceProvider>>;
  feishuSetupModalOpen: boolean;
  setFeishuSetupModalOpen: Dispatch<SetStateAction<boolean>>;
  feishuSetupIntent: CloudSetupIntent;
  setFeishuSetupIntent: Dispatch<SetStateAction<CloudSetupIntent>>;
  feishuSetupSubmitting: boolean;
  setFeishuSetupSubmitting: Dispatch<SetStateAction<boolean>>;
  manualOauthModalOpen: boolean;
  setManualOauthModalOpen: Dispatch<SetStateAction<boolean>>;
  manualOauthCallbackValue: string;
  setManualOauthCallbackValue: Dispatch<SetStateAction<string>>;
  manualOauthSubmitting: boolean;
  setManualOauthSubmitting: Dispatch<SetStateAction<boolean>>;

  // OAuth / connection state
  oauthState: OAuthState;
  setOauthState: Dispatch<SetStateAction<OAuthState>>;
  connectionVerified: boolean;
  setConnectionVerified: Dispatch<SetStateAction<boolean>>;
  oauthConnection: FeishuDataSourceConnection | null;
  setOauthConnection: Dispatch<SetStateAction<FeishuDataSourceConnection | null>>;
  notionOauthConnection: FeishuDataSourceConnection | null;
  setNotionOauthConnection: Dispatch<SetStateAction<FeishuDataSourceConnection | null>>;
  feishuAuthAccounts: FeishuAuthAccount[];
  setFeishuAuthAccounts: Dispatch<SetStateAction<FeishuAuthAccount[]>>;
  editingFeishuAccountId: string | null;
  setEditingFeishuAccountId: Dispatch<SetStateAction<string | null>>;
  feishuAppSetup: FeishuAppSetup | null;
  setFeishuAppSetup: Dispatch<SetStateAction<FeishuAppSetup | null>>;
  notionAppSetup: FeishuAppSetup | null;
  setNotionAppSetup: Dispatch<SetStateAction<FeishuAppSetup | null>>;
  oauthAttemptRef: MutableRefObject<PendingOAuthAttempt | null>;

  // Local FS chat state
  localScanChatEnabled: boolean;
  setLocalScanChatEnabled: Dispatch<SetStateAction<boolean>>;
  localScanChatSaving: boolean;
  setLocalScanChatSaving: Dispatch<SetStateAction<boolean>>;

  // Misc state / derived
  validatedAgentId: string | null;
  setValidatedAgentId: Dispatch<SetStateAction<string | null>>;
  canCreateLocalSource: boolean;
  scanAgents: ScanV2AgentHint[];
  isFeishuSetupReady: boolean;
  isNotionSetupReady: boolean;
  isFeishuAuthValid: boolean;
  isNotionAuthValid: boolean;
  sourceListRequestSeqRef: MutableRefObject<number>;
  feishuAuthAccountsLoadedRef: MutableRefObject<boolean>;

  // Tree browse state (from useLocalPathTree / useFeishuTargetTree)
  feishuTargetTreeData: FeishuTargetTreeNode[];
  resetLocalPathBrowseOptions: () => void;
  resetFeishuTargetBrowseOptions: () => void;

  // List actions (createListActions)
  refreshSources: (showSuccessMessage?: boolean, options?: RefreshSourcesOptions) => Promise<void>;
  handleToggleLocalScanChat: (chatEnabled: boolean) => Promise<void>;

  // OAuth engine handlers (createOAuthEngine)
  clearOauthAttempt: () => void;
  restorePreviousOauthState: (messageText?: string, level?: "warning" | "error") => void;
  applyOauthResult: (payload: FeishuDataSourceOAuthMessage) => void;
  refreshFeishuAuthAccounts: () => Promise<void>;
  refreshNotionAuthConnection: () => Promise<void>;
  upsertFeishuAuthAccount: (
    setup: FeishuAccountFormValues,
    status?: OAuthState,
  ) => FeishuAuthAccount;
  saveCloudAppCredentials: (
    provider: CloudDataSourceProvider,
    setup: FeishuAppSetup,
  ) => Promise<void>;
  startCloudOAuth: (
    provider: CloudDataSourceProvider,
    options?: StartCloudOAuthOptions,
  ) => Promise<boolean>;

  // Wizard setup handlers (createWizardSetup)
  resetWizard: () => void;
  openEditWizard: (record: DataSourceItem) => void;
  handleCloseWizard: () => void;
  applySourceType: (type: SourceType) => void;
  openCloudSetupModal: (
    provider: CloudDataSourceProvider,
    intent?: CloudSetupIntent,
    account?: FeishuAuthAccount | null,
  ) => void;
  openFeishuSetupModal: (
    intent?: FeishuSetupIntent,
    account?: FeishuAuthAccount | null,
  ) => void;
  handleSaveFeishuSetup: () => Promise<void>;
  handleResetFeishuSetup: () => void;
  handleResetNotionSetup: () => void;

  // Wizard flow handlers (createWizardFlow)
  handleSelectType: (type: SourceType) => void;
  openSourceCreateWizard: (
    type: SourceType,
    options?: { connection?: FeishuDataSourceConnection | null },
  ) => void;
  handleCreateProviderSelect: (type: SourceType) => void;
  handleSelectFeishuAuthConnection: (connection: FeishuDataSourceConnection) => void;
  handleManageFeishuAuth: () => void;
  handleOpenFeishuGuideFromAuthSelect: () => void;
  handleNextStep: () => void;
  handleSubmitManualOauthCallback: () => Promise<void>;
  openDetailPage: (record: DataSourceItem) => void;
  handleDeleteSource: (record: DataSourceItem) => void;

  // Save handlers (createSaveActions)
  handleSave: (saveMode?: DataSourceSaveMode) => Promise<void>;
}
