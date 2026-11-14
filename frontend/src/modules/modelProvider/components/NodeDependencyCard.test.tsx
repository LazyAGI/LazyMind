import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import NodeDependencyCard from "./NodeDependencyCard";
const mocks = vi.hoisted(() => ({ status: vi.fn(), install: vi.fn() }));
vi.mock("../api/systemDependencies", () => ({ getNodeDependencyStatus: mocks.status, installNodeDependency: mocks.install }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });
it("opens the installer, retries failures, and displays the installed runtime", async () => {
  mocks.status.mockResolvedValue({ installed: false, installSupported: true });
  mocks.install.mockRejectedValueOnce(new Error("download failed")).mockResolvedValueOnce({
    installed: true, installSupported: true, version: "v24.22.3", path: "/managed/bin/node",
  });
  render(<MemoryRouter initialEntries={["/settings#node-dependency"]}><NodeDependencyCard visible /></MemoryRouter>);
  const install = await screen.findByRole("button", { name: /nodeDependency\.install/ });
  await waitFor(() => expect(install).toBeEnabled());
  fireEvent.click(install);
  await screen.findByText("nodeDependency.failed");
  await waitFor(() => expect(install).toBeEnabled());
  fireEvent.click(install);
  await screen.findByText("v24.22.3");
  expect(screen.getByText("/managed/bin/node")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /nodeDependency\.install/ })).not.toBeInTheDocument();
  expect(mocks.install).toHaveBeenCalledTimes(2);
});
