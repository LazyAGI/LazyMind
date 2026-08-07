import type { ReactNode } from "react";

export default function MarkdownEditorMock({ children }: { children?: ReactNode }) {
  return <div data-testid="markdown-editor-mock">{children}</div>;
}
