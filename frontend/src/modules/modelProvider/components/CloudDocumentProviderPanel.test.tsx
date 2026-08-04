import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import CloudDocumentProviderPanel, { CloudDocumentModals } from "./CloudDocumentProviderPanel";
import type { CloudDocumentProvidersVm } from "../hooks/useCloudDocumentProviders";
import { Form } from "antd";

vi.mock("@/modules/dataSource/common/FeishuCredentialHintAlert", () => ({
  FeishuCredentialHintAlertFromForm: () => (
    <div data-testid="feishu-credential-hint" />
  ),
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

function buildVm(overrides: Partial<CloudDocumentProvidersVm> = {}): CloudDocumentProvidersVm {
  return {
    t,
    loading: false,
    feishuSetupForm: undefined as any,
    cloudSetupProvider: "feishu",
    feishuSetupModalOpen: false,
    setFeishuSetupModalOpen: vi.fn(),
    feishuSetupIntent: null,
    setFeishuSetupIntent: vi.fn(),
    feishuSetupSubmitting: false,
    canCreateLocalSource: true,
    localSourceCount: 2,
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    isGoogleDriveAuthValid: false,
    isFeishuSetupReady: true,
    isNotionSetupReady: true,
    validFeishuAccounts: [],
    notionOauthConnection: null,
    googleDriveConnection: null,
    handleManageFeishuAuth: vi.fn(),
    handleManageLocalSource: vi.fn(),
    handleManageGoogleDrive: vi.fn(),
    handleOpenNotionSetup: vi.fn(),
    openCloudSetupModal: vi.fn(),
    handleSaveFeishuSetup: vi.fn(),
    refreshPageData: vi.fn(),
    cloudDocumentsPath: "/model-provider/cloud-documents",
    ...overrides,
  } as CloudDocumentProvidersVm;
}

describe("CloudDocumentProviderPanel", () => {
  it("shows skeleton placeholders while loading", () => {
    const { container } = renderWithProviders(
      <CloudDocumentProviderPanel vm={buildVm({ loading: true })} />,
    );
    expect(container.querySelectorAll(".ant-skeleton").length).toBeGreaterThan(0);
  });

  it("renders the local source row with its directory count", () => {
    renderWithProviders(<CloudDocumentProviderPanel vm={buildVm()} />);
    expect(
      screen.getByText("modelProvider.cloudDocuments.localTitle"),
    ).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("hides the local source row when the user cannot create local sources", () => {
    renderWithProviders(
      <CloudDocumentProviderPanel vm={buildVm({ canCreateLocalSource: false })} />,
    );
    expect(
      screen.queryByText("modelProvider.cloudDocuments.localTitle"),
    ).not.toBeInTheDocument();
  });

  it("calls handleManageLocalSource when the manage button is clicked", () => {
    const handleManageLocalSource = vi.fn();
    renderWithProviders(
      <CloudDocumentProviderPanel vm={buildVm({ handleManageLocalSource })} />,
    );
    fireEvent.click(screen.getByText("modelProvider.cloudDocuments.manageLocal"));
    expect(handleManageLocalSource).toHaveBeenCalledTimes(1);
  });

  it("shows the auth valid tag and calls handleManageFeishuAuth for a connected feishu provider", () => {
    const handleManageFeishuAuth = vi.fn();
    renderWithProviders(
      <CloudDocumentProviderPanel
        vm={buildVm({ isFeishuAuthValid: true, handleManageFeishuAuth })}
      />,
    );
    expect(
      screen.getByText("modelProvider.cloudDocuments.authValid"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByText("modelProvider.cloudDocuments.manageAccount"));
    expect(handleManageFeishuAuth).toHaveBeenCalledTimes(1);
  });

  it("marks a provider as locked when it is not authorized and setup is not ready", () => {
    const { container } = renderWithProviders(
      <CloudDocumentProviderPanel
        vm={buildVm({ isFeishuSetupReady: false, isFeishuAuthValid: false })}
      />,
    );
    expect(
      container.querySelector(".model-provider-cloud-doc-resource-row.is-locked"),
    ).not.toBeNull();
    expect(
      screen.getByText("modelProvider.cloudDocuments.credentialMissing"),
    ).toBeInTheDocument();
  });

  it("calls handleManageGoogleDrive for the google drive provider", () => {
    const handleManageGoogleDrive = vi.fn();
    renderWithProviders(
      <CloudDocumentProviderPanel vm={buildVm({ handleManageGoogleDrive })} />,
    );
    fireEvent.click(screen.getAllByText("modelProvider.cloudDocuments.configureConnection").at(-1)!);
    expect(handleManageGoogleDrive).toHaveBeenCalledTimes(1);
  });
});

describe("CloudDocumentModals", () => {
  function FormHarness({ vm }: { vm: Partial<CloudDocumentProvidersVm> }) {
    const [feishuSetupForm] = Form.useForm();
    return (
      <CloudDocumentModals vm={buildVm({ feishuSetupForm, ...vm })} />
    );
  }

  it("shows the feishu credential modal title and hint when open", () => {
    renderWithProviders(
      <FormHarness vm={{ feishuSetupModalOpen: true, cloudSetupProvider: "feishu" }} />,
    );
    expect(
      screen.getByText("modelProvider.cloudDocuments.feishuCredentialModalTitle"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("feishu-credential-hint")).toBeInTheDocument();
  });

  it("shows the notion credential modal title and hint when open", () => {
    renderWithProviders(
      <FormHarness vm={{ feishuSetupModalOpen: true, cloudSetupProvider: "notion" }} />,
    );
    expect(
      screen.getByText("modelProvider.cloudDocuments.notionCredentialModalTitle"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("modelProvider.cloudDocuments.notionCredentialHint"),
    ).toBeInTheDocument();
  });

  it("calls handleSaveFeishuSetup when confirming the modal", () => {
    const handleSaveFeishuSetup = vi.fn();
    renderWithProviders(
      <FormHarness
        vm={{ feishuSetupModalOpen: true, cloudSetupProvider: "feishu", handleSaveFeishuSetup }}
      />,
    );
    fireEvent.click(
      screen.getByText("modelProvider.cloudDocuments.credentialSaveAndAuthorize"),
    );
    expect(handleSaveFeishuSetup).toHaveBeenCalledTimes(1);
  });

  it("closes the modal via cancel when not submitting", () => {
    const setFeishuSetupModalOpen = vi.fn();
    renderWithProviders(
      <FormHarness
        vm={{
          feishuSetupModalOpen: true,
          cloudSetupProvider: "feishu",
          setFeishuSetupModalOpen,
        }}
      />,
    );
    fireEvent.click(screen.getByText("common.cancel"));
    expect(setFeishuSetupModalOpen).toHaveBeenCalledWith(false);
  });
});
