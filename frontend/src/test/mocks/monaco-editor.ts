// Minimal stub so Vite/Vitest can resolve the "monaco-editor" specifier in tests.
// The real editor.* API is provided per-test via vi.mock("monaco-editor", factory).
export const editor = {};
export const MarkerSeverity = { Hint: 1, Info: 2, Warning: 4, Error: 8 };
