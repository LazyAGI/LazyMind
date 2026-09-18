import { mergeConfig } from "vitest/config";
import baseConfig from "./vitest.config";

// Scoped test infrastructure for plan 23 and its related component regressions.
export default mergeConfig(baseConfig, {
  test: { setupFiles: ["./src/test/desktopResources.setup.ts"] },
});
