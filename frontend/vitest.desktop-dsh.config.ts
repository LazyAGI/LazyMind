import { defineConfig } from "vitest/config";
import baseConfig from "./vitest.config";

// The native Go integration test supplies the old Bridge and packaged runtime.
export default defineConfig({
  ...baseConfig,
  test: {
    ...baseConfig.test,
    include: ["src/modules/agentIntegration/AgentIntegrationPage.desktop-dsh.tsx"],
    minWorkers: 1,
    maxWorkers: 1,
    testTimeout: 60_000,
  },
});
