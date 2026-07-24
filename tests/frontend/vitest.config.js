import { fileURLToPath } from "node:url";

export default {
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("../../frontend/src", import.meta.url)),
    },
  },
  testEnvironment: 'jsdom',
  rootDir: '.',
  testMatch: ['**/*.test.js'],
  coverage: {
    provider: 'v8',
    reporter: ['text', 'html'],
    include: ['../../frontend/src/**/*.{ts,tsx}'],
    reportsDirectory: './coverage',
  },
};
