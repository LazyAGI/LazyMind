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
    };
  }
}

export {};
