import { act, cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  session: vi.fn(),
  metadata: vi.fn(),
  tree: vi.fn(),
  content: vi.fn(),
  t: vi.fn((key: string) => key),
}));

vi.mock("@/runtime/mode", async (load) => ({
  ...await load<object>(),
  isDesktopRuntime: () => true,
}));
vi.mock("@/runtime/cloud/session", () => ({
  getCloudSession: mocks.session,
  isCloudBusinessAvailable: (session: { state?: string; configured?: boolean; reachability?: string }) =>
    session.state === "signed_in" && session.configured === true && session.reachability === "reachable",
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
}));
vi.mock("../../cloudResourceApi", () => ({
  getCloudResource: mocks.metadata,
  getCloudResourceTree: mocks.tree,
  getCloudResourceContent: mocks.content,
}));
vi.mock("@/modules/workflow/components/StateGraphEditor", () => ({ default: () => null }));
vi.mock("@/modules/workflow/workflowPreview", () => ({ withWorkflowLayout: (value: string) => value }));
vi.mock("react-markdown", () => ({ default: ({ children }: { children?: React.ReactNode }) => <>{children}</> }));
vi.mock("remark-gfm", () => ({ default: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: mocks.t }) }));

import CloudResourceDetail from "./index";

async function mount() {
  await act(async () => {
    render(
      <MemoryRouter initialEntries={["/memory-management/skills/cloud/cloud-resource"]}>
        <Routes>
          <Route
            path="/memory-management/skills/cloud/:resourceId"
            element={<CloudResourceDetail resourceType="skill" />}
          />
          <Route path="/memory-management/skills" element={<div>local-skill-list</div>} />
        </Routes>
      </MemoryRouter>,
    );
  });
}

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("Cloud resource details while Desktop is not signed in", () => {
  it("returns to the local list without rendering Cloud UI", async () => {
    mocks.session.mockResolvedValue({ state: "signed_out", configured: true, reachability: "reachable" });
    await mount();
    expect(await screen.findByText("local-skill-list")).toBeVisible();
    expect(mocks.metadata).not.toHaveBeenCalled();
    expect(screen.queryByText("admin.memoryCloudLoginRequired")).not.toBeInTheDocument();
    expect(screen.queryByText("admin.memoryCloudViewDetail")).not.toBeInTheDocument();
  });

  it("also returns silently when the local Cloud session snapshot cannot be read", async () => {
    mocks.session.mockRejectedValue(new Error("session unavailable"));
    await mount();
    expect(await screen.findByText("local-skill-list")).toBeVisible();
    expect(mocks.metadata).not.toHaveBeenCalled();
    expect(screen.queryByText("admin.memoryCloudDetailFailed")).not.toBeInTheDocument();
  });
});
