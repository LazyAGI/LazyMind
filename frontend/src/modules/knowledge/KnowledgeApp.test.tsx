import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ availability: "missing" }));

vi.mock("@/modules/modelProvider/contexts/RAGComponentContext", () => ({
  useRAGComponent: () => ({ availability: mocks.availability, refresh: vi.fn() }),
}));
vi.mock("@/modules/modelProvider/components/PythonComponentDependencies", () => ({
  RAGComponentNotice: () => <div>rag-notice</div>,
}));
vi.mock("./layout", () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }));

import KnowledgeApp from "./KnowledgeApp";

function renderApp() {
  return render(
    <MemoryRouter initialEntries={["/lib/knowledge/list"]}>
      <Routes>
        <Route path="/lib/knowledge" element={<KnowledgeApp />}>
          <Route path="list" element={<div>knowledge-list</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("KnowledgeApp optional RAG component", () => {
  beforeEach(() => {
    mocks.availability = "missing";
  });

  it("keeps knowledge-base storage accessible without the RAG component", () => {
    renderApp();

    expect(screen.getByText("rag-notice")).toBeInTheDocument();
    expect(screen.getByText("knowledge-list")).toBeInTheDocument();
  });

  it("mounts knowledge-base routes after the component is active", () => {
    mocks.availability = "ready";
    renderApp();

    expect(screen.getByText("knowledge-list")).toBeInTheDocument();
  });
});
