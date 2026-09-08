import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ExternalConnectionsPage from "./ExternalConnectionsPage";

const navigate = vi.fn();
const listConnections = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return {
    ...actual,
    useNavigate: () => navigate,
  };
});

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: { account?: string }) => {
      if (key === "modelProvider.mail.connectedHint") {
        return `connected:${options?.account || ""}`;
      }
      return key;
    },
  }),
}));

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: (...args: unknown[]) =>
      listConnections(...args),
  },
}));

vi.mock("@/modules/dataSource/api/unwrap", () => ({
  unwrapApiData: (data: unknown) => data,
}));

describe("ExternalConnectionsPage", () => {
  beforeEach(() => {
    navigate.mockReset();
    listConnections.mockReset();
    listConnections.mockResolvedValue({ data: { items: [] } });
  });

  it("opens the mailbox page from the hub row", async () => {
    render(
      <MemoryRouter>
        <ExternalConnectionsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText("modelProvider.externalConnections.title")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /modelProvider.externalConnections.configure/ }));
    expect(navigate).toHaveBeenCalledWith("/external-connections/mail");
  });

  it("shows connected mailbox names", async () => {
    listConnections.mockImplementation(async ({ provider }: { provider: string }) => {
      if (provider === "qqmail") {
        return { data: { items: [{ display_name: "user@qq.com" }] } };
      }
      return { data: { items: [] } };
    });

    render(
      <MemoryRouter>
        <ExternalConnectionsPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText("connected:user@qq.com")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: /modelProvider.externalConnections.manage/ })).toBeInTheDocument();
  });
});
