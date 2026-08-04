import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import ExternalServicesPage from "./ExternalServicesPage";
import { modelProvidersApi, unwrapModelProviderData } from "../api";

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGet: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsPost: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch: vi.fn(),
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

vi.mock("../components/ToolManagementSection", () => ({
  default: ({ view }: { view: string }) => <div data-testid={`tool-management-${view}`} />,
}));

vi.mock("../components/DependencyInstallSection", () => ({
  default: () => <div data-testid="dependency-install-section" />,
}));

const providersGetMock = modelProvidersApi.apiCoreModelProvidersGet as unknown as ReturnType<
  typeof vi.fn
>;
const groupsGetMock =
  modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet as unknown as ReturnType<
    typeof vi.fn
  >;

beforeEach(() => {
  vi.clearAllMocks();
  providersGetMock.mockImplementation(({ category }: { category?: string } = {}) => {
    if (category === "datasource") {
      return Promise.resolve({
        data: {
          providers: [{ id: "sciverse", name: "Sciverse", category: "datasource" }],
        },
      });
    }
    return Promise.resolve({
      data: {
        providers: [
          { id: "mineru", name: "MinerU", category: "ocr", is_configured: true, base_url: "https://mineru.net" },
          { id: "tavily", name: "Tavily", category: "search", is_configured: false },
        ],
      },
    });
  });
  groupsGetMock.mockResolvedValue({ data: { groups: [] } });
});

describe("ExternalServicesPage", () => {
  it("renders parsing and search service cards from the API", async () => {
    renderWithProviders(<ExternalServicesPage />);
    await waitFor(() => {
      expect(screen.getByText("MinerU")).toBeInTheDocument();
    });
    expect(screen.getByText("Tavily")).toBeInTheDocument();
    expect(screen.getByTestId("dependency-install-section")).toBeInTheDocument();
    expect(screen.getByTestId("tool-management-mcp")).toBeInTheDocument();
  });

  it("filters services within a category using the category search input", async () => {
    renderWithProviders(<ExternalServicesPage />);
    await waitFor(() => {
      expect(screen.getByText("MinerU")).toBeInTheDocument();
    });
    fireEvent.change(
      screen.getByPlaceholderText("modelProvider.external.parsingSearchPlaceholder"),
      { target: { value: "zzz-no-match" } },
    );
    expect(screen.getByText("modelProvider.external.noMatchedServices")).toBeInTheDocument();
  });

  it("opens the config modal for a service when its card is clicked", async () => {
    renderWithProviders(<ExternalServicesPage />);
    await waitFor(() => {
      expect(screen.getByText("MinerU")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("MinerU"));
    await waitFor(() => {
      expect(document.querySelector(".model-provider-service-config-modal")).not.toBeNull();
    });
  });

  it("shows a retry alert when loading services fails", async () => {
    providersGetMock.mockRejectedValueOnce(new Error("network error"));
    renderWithProviders(<ExternalServicesPage />);
    await waitFor(() => {
      expect(document.querySelector(".ant-alert-error")).not.toBeNull();
    });
  });
});
