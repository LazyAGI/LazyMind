import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import CloudDocumentsPage from "./CloudDocumentsPage";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const useCloudDocumentProvidersMock = vi.fn();

vi.mock("../hooks/useCloudDocumentProviders", () => ({
  useCloudDocumentProviders: () => useCloudDocumentProvidersMock(),
}));

vi.mock("../components/CloudDocumentProviderPanel", () => ({
  __esModule: true,
  default: () => <div data-testid="cloud-document-provider-panel" />,
  CloudDocumentModals: () => <div data-testid="cloud-document-modals" />,
}));

function buildVm(overrides: Partial<CloudDocumentProvidersVm> = {}): CloudDocumentProvidersVm {
  return {
    t,
    loading: false,
    canCreateLocalSource: true,
    localSourceCount: 1,
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    isGoogleDriveAuthValid: false,
    ...overrides,
  } as CloudDocumentProvidersVm;
}

describe("CloudDocumentsPage", () => {
  it("renders the title, subtitle and provider panels", () => {
    useCloudDocumentProvidersMock.mockReturnValue(buildVm());
    renderWithProviders(<CloudDocumentsPage />);
    expect(screen.getByText("modelProvider.cloudDocuments.title")).toBeInTheDocument();
    expect(screen.getByText("modelProvider.cloudDocuments.subtitle")).toBeInTheDocument();
    expect(screen.getByTestId("cloud-document-provider-panel")).toBeInTheDocument();
    expect(screen.getByTestId("cloud-document-modals")).toBeInTheDocument();
  });

  it("shows the ready-provider count out of total when not loading", () => {
    useCloudDocumentProvidersMock.mockReturnValue(
      buildVm({ isFeishuAuthValid: true, canCreateLocalSource: true, localSourceCount: 3 }),
    );
    renderWithProviders(<CloudDocumentsPage />);
    // 3 auth providers (feishu/notion/googledrive) + local = 4 total, 2 ready (local + feishu)
    expect(screen.getByText("2 / 4")).toBeInTheDocument();
  });

  it("shows a loading skeleton instead of the count while loading", () => {
    useCloudDocumentProvidersMock.mockReturnValue(buildVm({ loading: true }));
    const { container } = renderWithProviders(<CloudDocumentsPage />);
    expect(
      container.querySelector(".model-provider-cloud-doc-overview-skeleton"),
    ).not.toBeNull();
  });

  it("excludes the local provider from the total when the user cannot create local sources", () => {
    useCloudDocumentProvidersMock.mockReturnValue(
      buildVm({ canCreateLocalSource: false }),
    );
    renderWithProviders(<CloudDocumentsPage />);
    // 3 auth providers only, none ready
    expect(screen.getByText("0 / 3")).toBeInTheDocument();
  });
});
