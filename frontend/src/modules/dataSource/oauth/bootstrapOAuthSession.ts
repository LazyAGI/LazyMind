import type { FormInstance } from "antd/es/form";
import type { Dispatch, SetStateAction } from "react";
import { DEFAULT_DATA_SOURCE_FILE_TYPES } from "../constants/options";
import type { OAuthState, SourceFormValues, SourceType } from "../constants/types";
import {
  consumeCloudDataSourceOAuthResult,
  consumeFeishuDataSourceOAuthResult,
  consumeFeishuDataSourceWizardDraft,
} from "./storage";
import type {
  CloudDataSourceProvider,
  FeishuDataSourceConnection,
  FeishuDataSourceOAuthMessage,
} from "./types";
import {
  isPendingOAuthWizardDraft,
  shouldOpenWizardFromDraft,
} from "./restoreWizardDraft";

export type { FeishuDataSourceWizardDraft } from "./types";

export { consumeFeishuDataSourceWizardDraft } from "./storage";

interface BootstrapOAuthSessionOptions {
  form: FormInstance<SourceFormValues>;
  setAuthSelectModalOpen: Dispatch<SetStateAction<boolean>>;
  setAuthSelectProvider?: Dispatch<SetStateAction<CloudDataSourceProvider | null>>;
  setWizardMode: Dispatch<SetStateAction<"create" | "edit">>;
  setWizardOpen: Dispatch<SetStateAction<boolean>>;
  setWizardStep: Dispatch<SetStateAction<number>>;
  setSelectedType: Dispatch<SetStateAction<SourceType | null>>;
  setEditingId: Dispatch<SetStateAction<string | null>>;
  setValidatedAgentId: Dispatch<SetStateAction<string | null>>;
  setOauthState: Dispatch<SetStateAction<OAuthState>>;
  setConnectionVerified: Dispatch<SetStateAction<boolean>>;
  setOauthConnection: Dispatch<SetStateAction<FeishuDataSourceConnection | null>>;
  applyOauthResult: (
    payload: FeishuDataSourceOAuthMessage,
    options?: { openWizardOnSuccess?: boolean },
  ) => void;
  reopenCloudSetupModal?: (type: SourceType) => void;
}

export function bootstrapOAuthSession({
  form,
  setAuthSelectModalOpen,
  setAuthSelectProvider,
  setWizardMode,
  setWizardOpen,
  setWizardStep,
  setSelectedType,
  setEditingId,
  setValidatedAgentId,
  setOauthState,
  setConnectionVerified,
  setOauthConnection,
  applyOauthResult,
  reopenCloudSetupModal,
}: BootstrapOAuthSessionOptions) {
  const storedFeishuResult = consumeFeishuDataSourceOAuthResult();
  const storedNotionResult = consumeCloudDataSourceOAuthResult("notion");
  const storedResult = storedNotionResult || storedFeishuResult;
  const draft = consumeFeishuDataSourceWizardDraft();

  if (draft) {
    const normalizedWizardStep = Math.min(Math.max(draft.wizardStep, 0), 1);
    if (draft.authSelectModalOpen !== undefined) {
      setAuthSelectModalOpen(Boolean(draft.authSelectModalOpen));
    }
    if (setAuthSelectProvider && draft.authSelectProvider) {
      setAuthSelectProvider(draft.authSelectProvider);
    }
    setWizardMode(draft.wizardMode);
    setWizardOpen(shouldOpenWizardFromDraft(draft, storedResult?.status === "success"));
    setWizardStep(normalizedWizardStep);
    setSelectedType((draft.selectedType as SourceType | null) || null);
    setEditingId(draft.editingId);
    setValidatedAgentId(draft.validatedAgentId || null);
    setOauthState((draft.oauthState as OAuthState) || "pending");
    setConnectionVerified(Boolean(draft.connectionVerified));
    setOauthConnection(draft.oauthConnection || null);
    window.setTimeout(() => {
      form.setFieldsValue({
        fileTypes: DEFAULT_DATA_SOURCE_FILE_TYPES,
        ...draft.formValues,
      });
    }, 0);

    if (!storedResult && isPendingOAuthWizardDraft(draft) && reopenCloudSetupModal) {
      const providerType = draft.selectedType as SourceType | null;
      if (providerType === "feishu" || providerType === "notion") {
        window.setTimeout(() => {
          reopenCloudSetupModal(providerType);
        }, 0);
      }
    }
  }

  if (storedResult) {
    window.setTimeout(() => {
      applyOauthResult(storedResult, {
        openWizardOnSuccess: Boolean(draft?.openWizardAfterOAuth ?? draft?.wizardOpen),
      });
    }, 0);
  }
}
