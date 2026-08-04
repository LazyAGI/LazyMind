import { describe, expect, it } from "vitest";
import { render, fireEvent, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import ModelProviderLayout from "./index";

function renderLayout(initialPath = "/model-providers/models") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/model-providers" element={<ModelProviderLayout />}>
            <Route path="models" element={<div>Models Outlet</div>} />
            <Route path="default-services" element={<div>Default Services Outlet</div>} />
            <Route path="tools" element={<div>Tools Outlet</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("ModelProviderLayout", () => {
  it("renders all navigation tabs and the routed outlet content", () => {
    renderLayout();
    expect(screen.getByText("modelProvider.tabs.defaultServices")).toBeInTheDocument();
    expect(screen.getByText("modelProvider.tabs.models")).toBeInTheDocument();
    expect(screen.getByText("modelProvider.tabs.tools")).toBeInTheDocument();
    expect(screen.getByText("Models Outlet")).toBeInTheDocument();
  });

  it("marks the tab matching the current path as active", () => {
    renderLayout("/model-providers/tools");
    const toolsTab = screen.getByText("modelProvider.tabs.tools").closest("button");
    expect(toolsTab).toHaveClass("is-active");
    const modelsTab = screen.getByText("modelProvider.tabs.models").closest("button");
    expect(modelsTab).not.toHaveClass("is-active");
  });

  it("navigates to a different tab when clicked", () => {
    renderLayout("/model-providers/models");
    fireEvent.click(screen.getByText("modelProvider.tabs.defaultServices"));
    expect(screen.getByText("Default Services Outlet")).toBeInTheDocument();
  });
});
