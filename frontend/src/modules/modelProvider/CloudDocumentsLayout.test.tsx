import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import CloudDocumentsLayout from "./CloudDocumentsLayout";

describe("CloudDocumentsLayout", () => {
  it("renders the routed outlet content inside the layout frame", () => {
    render(
      <MemoryRouter initialEntries={["/cloud-documents"]}>
        <Routes>
          <Route path="/cloud-documents" element={<CloudDocumentsLayout />}>
            <Route index element={<div>Cloud Docs Outlet</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText("Cloud Docs Outlet")).toBeInTheDocument();
  });

  it("applies the expected layout class names", () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/cloud-documents"]}>
        <Routes>
          <Route path="/cloud-documents" element={<CloudDocumentsLayout />}>
            <Route index element={<div>Cloud Docs Outlet</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(
      container.querySelector(".model-provider-page.model-provider-cloud-doc-layout"),
    ).not.toBeNull();
    expect(container.querySelector(".model-provider-layout-frame")).not.toBeNull();
  });
});
