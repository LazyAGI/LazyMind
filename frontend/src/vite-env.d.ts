/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL: string;
  readonly VITE_LAZYMIND_MODE?: string;
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
      exportDiagnostics?: () => Promise<string> | string;
      notifyAppReady?: () => void;
    };
  }
}

export {};
