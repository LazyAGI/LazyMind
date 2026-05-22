export default {
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
