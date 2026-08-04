import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import ExternalServiceConfigModal, {
  type ExternalServiceConfigModalService,
} from "./ExternalServiceConfigModal";
import { modelProvidersApi, modelProvidersDefaultApi } from "../api";

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersModelProviderIdGroupsGet: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsPost: vi.fn(),
    apiCoreModelProvidersSelectedProvidersPut: vi.fn(),
  },
  modelProvidersDefaultApi: {
    apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete: vi.fn(),
  },
  unwrapModelProviderData: (payload: unknown) => {
    if (payload && typeof payload === "object" && "data" in (payload as any)) {
      return (payload as any).data;
    }
    return payload;
  },
  withModelProviderJsonOptions: (options: any = {}) => ({
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  }),
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code?: string) => code || "",
}));

const groupsGetMock =
  modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet as unknown as ReturnType<
    typeof vi.fn
  >;
const groupsPostMock =
  modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost as unknown as ReturnType<
    typeof vi.fn
  >;
const selectedProvidersPutMock =
  modelProvidersApi.apiCoreModelProvidersSelectedProvidersPut as unknown as ReturnType<
    typeof vi.fn
  >;
const keysPostMock =
  modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost as unknown as ReturnType<
    typeof vi.fn
  >;
const keysDeleteMock =
  modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete as unknown as ReturnType<
    typeof vi.fn
  >;

const baseService: ExternalServiceConfigModalService = {
  key: "provider-1",
  name: "MinerU",
  description: "OCR service",
  fields: ["apiKey"],
  logo: null,
  logoUrl: "",
  tone: "blue",
  status: "missing",
  category: "parsing",
};

describe("ExternalServiceConfigModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    groupsGetMock.mockResolvedValue({ data: { groups: [] } });
  });

  it("renders nothing meaningful when there is no service", () => {
    renderWithProviders(
      <ExternalServiceConfigModal open service={null} onClose={vi.fn()} />,
    );
    expect(
      screen.getByText("modelProvider.external.configureAction"),
    ).toBeInTheDocument();
  });

  it("loads existing keys for the service and shows them masked", async () => {
    groupsGetMock.mockResolvedValue({
      data: { groups: [{ id: "group-1", api_key: "sk-aaaaaaaabbbb\nsk-cccccccc" }] },
    });
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={vi.fn()} />,
    );
    await waitFor(() => {
      expect(groupsGetMock).toHaveBeenCalledWith({ modelProviderId: "provider-1" });
    });
    await waitFor(() => {
      expect(screen.getByText("sk-a****...bbbb")).toBeInTheDocument();
    });
  });

  it("shows an empty-keys message when there are no configured keys", async () => {
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={vi.fn()} />,
    );
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.noKeysConfigured"),
      ).toBeInTheDocument();
    });
  });

  it("creates a new group and selects it as provider when adding the first key", async () => {
    groupsPostMock.mockResolvedValue({
      data: { id: "group-new", api_key: "sk-newkey", check: { success: true } },
    });
    selectedProvidersPutMock.mockResolvedValue({});
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={vi.fn()} />,
    );
    const input = await screen.findByPlaceholderText(
      "modelProvider.external.keyPlaceholder",
    );
    fireEvent.change(input, { target: { value: "sk-newkey" } });
    fireEvent.click(
      screen.getByText("modelProvider.external.verifyAndAddKey"),
    );
    await waitFor(() => {
      expect(groupsPostMock).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(selectedProvidersPutMock).toHaveBeenCalledWith({
        setSelectedProviderOpenAPIRequest: {
          selections: [{ category: "ocr", group_id: "group-new" }],
        },
      });
    });
  });

  it("appends a key to an existing group without recreating it", async () => {
    groupsGetMock.mockResolvedValue({
      data: { groups: [{ id: "group-1", api_key: "sk-existing" }] },
    });
    keysPostMock.mockResolvedValue({});
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={vi.fn()} />,
    );
    const input = await screen.findByPlaceholderText(
      "modelProvider.external.keyPlaceholder",
    );
    fireEvent.change(input, { target: { value: "sk-second-key" } });
    fireEvent.click(
      screen.getByText("modelProvider.external.verifyAndAddKey"),
    );
    await waitFor(() => {
      expect(keysPostMock).toHaveBeenCalled();
    });
    expect(groupsPostMock).not.toHaveBeenCalled();
  });

  it("removes a configured key", async () => {
    groupsGetMock.mockResolvedValue({
      data: { groups: [{ id: "group-1", api_key: "sk-existing" }] },
    });
    keysDeleteMock.mockResolvedValue({});
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={vi.fn()} />,
    );
    await waitFor(() => {
      expect(screen.getByText(/sk-e/)).toBeInTheDocument();
    });
    const deleteButton = document.querySelector(
      "button .anticon-delete",
    )?.closest("button");
    expect(deleteButton).not.toBeNull();
    fireEvent.click(deleteButton!);
    await waitFor(() => {
      expect(keysDeleteMock).toHaveBeenCalled();
    });
  });

  it("toggles key visibility between masked and plain text", async () => {
    groupsGetMock.mockResolvedValue({
      data: { groups: [{ id: "group-1", api_key: "sk-aaaaaaaabbbb" }] },
    });
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={vi.fn()} />,
    );
    await waitFor(() => {
      expect(screen.getByText("sk-a****...bbbb")).toBeInTheDocument();
    });
    const showButton = document.querySelector(
      "button .anticon-eye",
    )?.closest("button");
    fireEvent.click(showButton!);
    expect(screen.getByText("sk-aaaaaaaabbbb")).toBeInTheDocument();
  });

  it("calls onClose when the close button is clicked", async () => {
    const onClose = vi.fn();
    renderWithProviders(
      <ExternalServiceConfigModal open service={baseService} onClose={onClose} />,
    );
    await waitFor(() => {
      expect(
        screen.getByText("modelProvider.external.noKeysConfigured"),
      ).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("common.close"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
