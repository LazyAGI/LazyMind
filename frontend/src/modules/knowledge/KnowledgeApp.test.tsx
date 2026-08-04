import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import KnowledgeApp from "./KnowledgeApp";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { resolvedLanguage: "zh-CN", language: "zh-CN" },
  }),
}));

vi.mock("@/i18n/antdLocale", () => ({
  getAntdLocale: () => undefined,
}));

describe("KnowledgeApp", () => {
  it("renders the knowledge layout wrapper with the routed outlet content", () => {
    render(
      <MemoryRouter initialEntries={["/lib/knowledge/child"]}>
        <Routes>
          <Route path="/lib/knowledge" element={<KnowledgeApp />}>
            <Route path="child" element={<div>outlet child</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByText("outlet child")).toBeInTheDocument();
    expect(document.querySelector(".micro-knowledge-page")).toBeInTheDocument();
  });
});
