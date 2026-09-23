import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  session: vi.fn(),
  list: vi.fn(),
  login: vi.fn(),
  openLogin: vi.fn(),
  openRegister: vi.fn(),
  reserve: vi.fn(),
  closePopup: vi.fn(),
}));

vi.mock("@/runtime/cloud/session", () => ({
  getCloudSession: mocks.session,
  beginCloudLogin: mocks.login,
  isCloudBusinessAvailable: (session: { state?: string; configured?: boolean; reachability?: string }) =>
    session.state === "signed_in" && session.configured === true && session.reachability === "reachable",
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed",
}));
vi.mock("@/runtime/desktopBridge", () => ({
  reserveCloudLoginPopup: mocks.reserve,
  openCloudLogin: mocks.openLogin,
  openCloudRegister: mocks.openRegister,
  closeCloudLoginPopup: mocks.closePopup,
}));
vi.mock("../../cloudResourceApi", () => ({
  listCloudResources: mocks.list,
  downloadCloudResource: vi.fn(),
}));

import CloudResourceTable from "./CloudResourceTable";

const t = (key: string) => key;

describe("CloudResourceTable unsigned catalog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.session.mockResolvedValue({ state: "signed_out", configured: true, reachability: "reachable" });
    mocks.list.mockResolvedValue([]);
    mocks.reserve.mockReturnValue({ close: vi.fn() });
    mocks.login.mockResolvedValue({ authorization_url: "https://cloud.example/zh/desktop/authorize" });
    mocks.openLogin.mockResolvedValue({ ok: true });
    mocks.openRegister.mockResolvedValue({ ok: true });
  });
  afterEach(cleanup);

  it("shows the login page instead of a blank catalog when Cloud is signed out", async () => {
    render(<CloudResourceTable resourceType="skill" t={t} />);
    expect(await screen.findByText("admin.memoryCloudLoginRequired")).toBeVisible();
    expect(screen.getByRole("button", { name: "layout.cloudLogin" })).toBeVisible();
    expect(screen.getByRole("button", { name: "admin.memoryCloudRegister" })).toBeVisible();
    expect(mocks.list).not.toHaveBeenCalled();
  });

  it("lists Cloud resources after a signed-in session", async () => {
    mocks.session.mockResolvedValue({
      state: "signed_in",
      configured: true,
      reachability: "reachable",
    });
    mocks.list.mockResolvedValue([{
      resource_id: "cloud-skill",
      resource_type: "skill",
      resource_name: "cloud-skill",
      content_size: 20,
      format_schema: "lazymind.resource-manifest/v2",
      updated_at: "2026-09-07T00:00:00Z",
      presence_status: "download_required",
      local_exists: false,
    }]);
    render(<CloudResourceTable resourceType="skill" t={t} />);
    expect((await screen.findAllByText("cloud-skill")).length).toBeGreaterThan(0);
    expect(screen.queryByText("admin.memoryCloudLoginRequired")).not.toBeInTheDocument();
  });

  it("starts Cloud login from the unsigned empty state", async () => {
    render(<CloudResourceTable resourceType="skill" t={t} />);
    fireEvent.click(await screen.findByRole("button", { name: "layout.cloudLogin" }));
    await waitFor(() => expect(mocks.openLogin).toHaveBeenCalled());
  });
});
