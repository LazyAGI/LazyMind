import { defineConfig } from 'vitest/config';
import baseConfig from './vitest.config';

// Exercise shared/native controls, hosted panels and the session recovery they depend on.
// Reuse the application's DOM setup, aliases and plugins.
export default defineConfig({
  ...baseConfig,
  test: {
    ...baseConfig.test,
    include: [
      'src/modules/chat/components/WorkflowPanel/Workflow*.test.{ts,tsx}',
      'src/modules/chat/components/WorkflowPanel/workflow*.test.{ts,tsx}',
      'src/modules/chat/components/WorkflowPanel/external/**/*.test.{ts,tsx}',
      'src/modules/chat/pages/workflowRun/**/*.test.{ts,tsx}',
      'src/modules/chat/utils/{loadWorkflowRun,workflowControl,workflowEventStream}.test.ts',
      'src/modules/chat/store/workflow*.test.ts',
      'src/components/{auth,request.cloudSession,request.recovery}.test.ts',
      'src/runtime/{assistantSession,desktopBridge}.test.ts',
    ],
    minWorkers: 1,
    maxWorkers: 2,
    passWithNoTests: false,
  },
});
