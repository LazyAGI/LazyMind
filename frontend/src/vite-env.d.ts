/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL: string;
  readonly VITE_LAZYMIND_MODE?: string;
  readonly VITE_VOCABULARY_ENABLED?: string;
  readonly VITE_HIDE_EVO?: string;
  readonly VITE_APP_LOGO?: string;
  readonly VITE_APP_CHAT_TITLE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

declare global {
  interface Window {
    BASENAME?: string;
    lazymindDesktop?: {
      recordingNativeStart?: () => Promise<{ session_id: string; startedAt: number }>;
      recordingNativeStop?: (id: string) => Promise<import("./modules/chat/components/SkillRecording/nativeCapture").NativeRecordingResult>;
      recordingNativeCancel?: () => Promise<void>;
      recordingNativeSettings?: () => Promise<void>;
      onRecordingNativeEvent?: (handler: (event: import("./modules/chat/components/SkillRecording/nativeCapture").NativeRecordingEvent) => void) => () => void;
      recordingInputPermission?: () => Promise<{ granted: boolean }>;
      recordingInputSettings?: () => Promise<void>;
      recordingInputStart?: (startedAt: number) => Promise<{ session_id: string }>;
      recordingInputStop?: (id: string) => Promise<import("./modules/chat/components/SkillRecording/api").RecordingEvidence>;
      recordingInputCancel?: (id: string) => Promise<void>;
      openLogsDir?: () => Promise<void> | void;
      openDataDir?: () => Promise<void> | void;
      runtimeStatus?: () => Promise<unknown> | unknown;
      restartRuntime?: () => Promise<unknown> | unknown;
      resetRuntime?: (scope?: "kb" | "all") => Promise<unknown> | unknown;
      localFolderAccessStatus?: () => Promise<unknown> | unknown;
      chooseLocalDiscoveryRoots?: () => Promise<unknown> | unknown;
      discoverLocalFolders?: () => Promise<unknown> | unknown;
      authorizeLocalFolders?: (paths: string[]) => Promise<unknown> | unknown;
      selectFolder?: () => Promise<string | null> | string | null;
      selectLocalWorkspace?: () => Promise<unknown> | unknown;
      reauthorizeLocalWorkspace?: (workspaceId: string) => Promise<unknown> | unknown;
      authorizeLocalWorkspace?: (selectionToken: string) => Promise<unknown> | unknown;
      exportDiagnostics?: () => Promise<string> | string;
      openCloudLogin?: (url: string) => Promise<unknown> | unknown;
      openCloudRegister?: () => Promise<unknown> | unknown;
      openCloudTokenPlan?: (url: string) => Promise<unknown> | unknown;
      notifyAppReady?: () => void;
    };
  }
}

export {};
