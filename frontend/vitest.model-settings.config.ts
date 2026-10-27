import { defineConfig } from "vitest/config";
import baseConfig from "./vitest.config";

// Keep model configuration, default selection and its chat consumers together.
// Reuse the application DOM setup, aliases and plugins without running the
// unrelated workflow or document-editor suites in this focused regression run.
export default defineConfig({
  ...baseConfig,
  test: {
    ...baseConfig.test,
    include: [
      "src/modules/modelProvider/**/*.{test,spec}.{ts,tsx}",
      "src/modules/settings/SettingsNavigationGuard.test.tsx",
      "src/modules/settings/SettingsPage.models.test.tsx",
      "src/modules/settings/QuickModelSettings.visibility.test.tsx",
      "src/modules/settings/settingsRouteContext.test.ts",
      "src/modules/settings/settingsSectionVisibility.test.ts",
      "src/modules/chat/components/ChatModelSelector/**/*.{test,spec}.{ts,tsx}",
      "src/modules/chat/components/ChatSelector/**/*.{test,spec}.{ts,tsx}",
      "src/modules/chat/store/modelSelection*.{test,spec}.{ts,tsx}",
      "src/modules/chat/components/ChatInput/index.test.tsx",
      "src/modules/chat/components/newChatContainer/hooks/useChatConversation.test.ts",
    ],
    minWorkers: 1,
    maxWorkers: 2,
    passWithNoTests: false,
  },
});
