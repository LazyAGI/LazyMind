// `react-markdown-editor-lite` is imported by MarkdownEditor but is not an installed
// dependency (missing from package.json/node_modules). Vite fails to resolve the bare
// import before `vi.mock` factories can run, so we redirect it here via `test.alias`
// in vitest.config.ts and let individual test files `vi.mock` this path as needed.
const MdEditor = () => null;

export default MdEditor;
