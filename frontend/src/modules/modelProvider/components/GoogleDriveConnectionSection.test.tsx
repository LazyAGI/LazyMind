import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import GoogleDriveConnectionSection from "./GoogleDriveConnectionSection";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import {
  consumeCloudDataSourceOAuthResult,
  enableCloudConnectionForChat,
  openCenteredPopup,
  requestCloudDataSourceAuthorizeUrl,
} from "@/modules/dataSource/common/feishuOAuth";

vi.mock("@/modules/dataSource/api/clients", () => ({
  dataSourceCloudOauthApi: {
    listConnectionsApiAuthserviceV1CloudConnectionsGet: vi.fn(),
    getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet: vi.fn(),
    saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut: vi.fn(),
    deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete: vi.fn(),
  },
}));

vi.mock("@/modules/dataSource/common/feishuOAuth", () => ({
  CLOUD_DATA_SOURCE_OAUTH_CHANNEL: "lazymind:cloud-data-source-oauth",
  consumeCloudDataSourceOAuthResult: vi.fn(),
  enableCloudConnectionForChat: vi.fn(),
  openCenteredPopup: vi.fn(),
  requestCloudDataSourceAuthorizeUrl: vi.fn(),
}));

const listMock =
  dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet as unknown as ReturnType<
    typeof vi.fn
  >;
const getCredentialsMock =
  dataSourceCloudOauthApi.getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet as unknown as ReturnType<
    typeof vi.fn
  >;
const saveCredentialsMock =
  dataSourceCloudOauthApi.saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut as unknown as ReturnType<
    typeof vi.fn
  >;
const deleteConnectionMock =
  dataSourceCloudOauthApi.deleteConnectionApiAuthserviceV1CloudConnectionsConnectionIdDelete as unknown as ReturnType<
    typeof vi.fn
  >;

describe("GoogleDriveConnectionSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listMock.mockResolvedValue({ data: { items: [] } });
    (consumeCloudDataSourceOAuthResult as any).mockReturnValue(null);
  });

  it("shows the missing status when there is no connection", async () => {
    renderWithProviders(<GoogleDriveConnectionSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.status.missing"),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByText("modelProvider.external.googleDriveConnect"),
    ).toBeInTheDocument();
  });

  it("shows the connected account and a disconnect button when a connection exists", async () => {
    listMock.mockResolvedValue({
      data: {
        items: [
          {
            connection_id: "conn-1",
            display_name: "user@example.com",
            provider_account_meta: {},
          },
        ],
      },
    });
    renderWithProviders(<GoogleDriveConnectionSection />);
    await waitFor(() => {
      expect(screen.getByText("user@example.com")).toBeInTheDocument();
    });
    expect(
      screen.getByText("modelProvider.external.googleDriveReconnect"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("modelProvider.external.googleDriveDisconnect"),
    ).toBeInTheDocument();
  });

  it("disconnects the current connection", async () => {
    listMock.mockResolvedValue({
      data: {
        items: [{ connection_id: "conn-1", provider_account_meta: {} }],
      },
    });
    deleteConnectionMock.mockResolvedValue({});
    renderWithProviders(<GoogleDriveConnectionSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.googleDriveDisconnect"),
      ).toBeInTheDocument();
    });
    fireEvent.click(
      screen.getByText("modelProvider.external.googleDriveDisconnect"),
    );
    await waitFor(() => {
      expect(deleteConnectionMock).toHaveBeenCalledWith({
        connectionId: "conn-1",
      });
    });
  });

  it("opens the configuration modal and loads existing oauth app credentials", async () => {
    getCredentialsMock.mockResolvedValue({
      data: { app_id: "client-123", secret_configured: true },
    });
    renderWithProviders(<GoogleDriveConnectionSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.googleDriveConnect"),
      ).toBeInTheDocument();
    });
    fireEvent.click(
      screen.getByText("modelProvider.external.googleDriveConnect"),
    );
    await waitFor(() => {
      expect(getCredentialsMock).toHaveBeenCalled();
    });
    const input = await screen.findByDisplayValue("client-123");
    expect(input).toBeInTheDocument();
  });

  it("saves credentials and opens the oauth popup when confirming the modal", async () => {
    getCredentialsMock.mockResolvedValue({
      data: { app_id: "", secret_configured: false },
    });
    saveCredentialsMock.mockResolvedValue({});
    (requestCloudDataSourceAuthorizeUrl as any).mockResolvedValue(
      "https://accounts.google.com/oauth",
    );
    (openCenteredPopup as any).mockReturnValue({});
    renderWithProviders(<GoogleDriveConnectionSection />);
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.googleDriveConnect"),
      ).toBeInTheDocument();
    });
    fireEvent.click(
      screen.getByText("modelProvider.external.googleDriveConnect"),
    );
    await waitFor(() => {
      expect(getCredentialsMock).toHaveBeenCalled();
    });
    const clientIdInput = await screen.findByLabelText("OAuth Client ID");
    fireEvent.change(clientIdInput, { target: { value: "new-client-id" } });
    const clientSecretInput = screen.getByLabelText("OAuth Client Secret");
    fireEvent.change(clientSecretInput, { target: { value: "super-secret" } });
    fireEvent.click(
      screen.getByText("modelProvider.external.googleDriveAuthorize"),
    );
    await waitFor(() => {
      expect(saveCredentialsMock).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(requestCloudDataSourceAuthorizeUrl).toHaveBeenCalledWith(
        "googledrive",
        expect.objectContaining({ appId: "new-client-id" }),
      );
    });
    await waitFor(() => {
      expect(openCenteredPopup).toHaveBeenCalled();
    });
  });
});
