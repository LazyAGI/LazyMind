import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import ModelProvidersPage from "./ModelProvidersPage";
import { modelProvidersApi, unwrapModelProviderData } from "../api";

vi.mock("../api", () => ({
  modelProvidersApi: {
    apiCoreModelProvidersGet: vi.fn(),
    apiCoreModelProvidersWithGroupsGet: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGet: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsPost: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdDelete: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdCheckPost: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsPost: vi.fn(),
    apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdDelete: vi.fn(),
  },
  unwrapModelProviderData: (payload: unknown) => {
    if (payload && typeof payload === "object" && "data" in (payload as any)) {
      return (payload as any).data;
    }
    return payload;
  },
}));

const providersGetMock = modelProvidersApi.apiCoreModelProvidersGet as unknown as ReturnType<
  typeof vi.fn
>;
const withGroupsGetMock =
  modelProvidersApi.apiCoreModelProvidersWithGroupsGet as unknown as ReturnType<typeof vi.fn>;
const groupsGetMock =
  modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet as unknown as ReturnType<
    typeof vi.fn
  >;

const defaultProviders = [
  { id: "openai", name: "OpenAI", base_url: "https://api.openai.com/v1/" },
  { id: "anthropic", name: "Anthropic", base_url: "https://api.anthropic.com/v1/" },
];

beforeEach(() => {
  vi.clearAllMocks();
  providersGetMock.mockResolvedValue({ data: { providers: defaultProviders } });
  withGroupsGetMock.mockResolvedValue({ data: { providers: [] } });
  groupsGetMock.mockResolvedValue({ data: { groups: [] } });
});

describe("ModelProvidersPage", () => {
  it("renders providers from the API catalog when no added providers exist", async () => {
    renderWithProviders(<ModelProvidersPage />);
    await waitFor(() => {
      expect(screen.getAllByText("OpenAI").length).toBeGreaterThan(0);
    });
    expect(screen.getAllByText("Anthropic").length).toBeGreaterThan(0);
    expect(screen.getByText("modelProvider.emptyAddedProviders")).toBeInTheDocument();
  });

  it("renders an added provider with its groups and lets you expand models", async () => {
    withGroupsGetMock.mockResolvedValue({
      data: {
        providers: [{ id: "openai", name: "OpenAI", base_url: "https://api.openai.com/v1/" }],
      },
    });
    groupsGetMock.mockResolvedValue({
      data: {
        groups: [
          {
            id: "group-1",
            name: "My OpenAI Group",
            base_url: "https://api.openai.com/v1/",
            is_verified: true,
            user_model_provider_id: "openai",
          },
        ],
      },
    });
    renderWithProviders(<ModelProvidersPage />);
    await waitFor(() => {
      expect(screen.getByText("My OpenAI Group")).toBeInTheDocument();
    });
    expect(screen.getByText("modelProvider.verified")).toBeInTheDocument();
  });

  it("filters providers by search keyword", async () => {
    providersGetMock.mockImplementation(({ keyword }: { keyword?: string } = {}) => {
      if (keyword === "claude") {
        return Promise.resolve({
          data: { providers: [{ id: "anthropic", name: "Anthropic", base_url: "" }] },
        });
      }
      return Promise.resolve({ data: { providers: defaultProviders } });
    });
    renderWithProviders(<ModelProvidersPage />);
    await waitFor(() => {
      expect(screen.getAllByText("OpenAI").length).toBeGreaterThan(0);
    });
    fireEvent.change(screen.getByPlaceholderText("modelProvider.searchPlaceholder"), {
      target: { value: "claude" },
    });
    await waitFor(
      () => {
        expect(screen.queryAllByText("OpenAI").length).toBe(0);
        expect(screen.getByText("Anthropic")).toBeInTheDocument();
      },
      { timeout: 2000 },
    );
  });

  it("opens the group config modal when adding a built-in provider", async () => {
    renderWithProviders(<ModelProvidersPage />);
    await waitFor(() => {
      expect(screen.getAllByText("OpenAI").length).toBeGreaterThan(0);
    });
    const [firstAddButton] = screen.getAllByText("modelProvider.configureAndAdd");
    fireEvent.click(firstAddButton);
    expect(
      document.querySelector(".ant-modal-title"),
    ).not.toBeNull();
  });
});
