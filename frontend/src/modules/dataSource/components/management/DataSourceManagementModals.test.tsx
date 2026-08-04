import { describe, expect, it, vi } from "vitest";
import { Form } from "antd";
import { fireEvent, renderWithProviders, screen, testI18n, waitFor } from "@/test/testUtils";
import DataSourceManagementModals from "./DataSourceManagementModals";

const t = testI18n.t.bind(testI18n);

function makeVm(
  form: ReturnType<typeof Form.useForm>[0],
  overrides: Partial<Record<string, unknown>> = {},
) {
  return {
    t,
    feishuSetupForm: form,
    cloudSetupProvider: "feishu",
    feishuSetupModalOpen: false,
    setFeishuSetupModalOpen: vi.fn(),
    feishuSetupSubmitting: false,
    handleSaveFeishuSetup: vi.fn().mockResolvedValue(undefined),
    handleCancelCloudSetup: vi.fn(),
    createProviderModalOpen: false,
    setCreateProviderModalOpen: vi.fn(),
    creatableSourceTypeOptions: [
      { type: "local" as const, icon: null, logoUrl: undefined, adminOnly: true },
    ],
    handleCreateProviderSelect: vi.fn(),
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    isGoogleDriveAuthValid: false,
    isFeishuSetupReady: true,
    isNotionSetupReady: true,
    authSelectModalOpen: false,
    setAuthSelectModalOpen: vi.fn(),
    authSelectProvider: "feishu",
    handleOpenFeishuGuideFromAuthSelect: vi.fn(),
    handleOpenNotionGuideFromAuthSelect: vi.fn(),
    handleAddFeishuAuthFromSelect: vi.fn(),
    handleAddNotionAuthFromSelect: vi.fn(),
    validFeishuAccounts: [],
    validNotionAccounts: [],
    handleSelectFeishuAuthConnection: vi.fn(),
    handleSelectNotionAuthConnection: vi.fn(),
    manualOauthModalOpen: false,
    setManualOauthModalOpen: vi.fn(),
    manualOauthCallbackValue: "",
    setManualOauthCallbackValue: vi.fn(),
    manualOauthSubmitting: false,
    handleSubmitManualOauthCallback: vi.fn(),
    ...overrides,
  } as any;
}

function Harness(props: Partial<Record<string, unknown>>) {
  const [form] = Form.useForm();
  const vm = makeVm(form, props);
  return <DataSourceManagementModals vm={vm} />;
}

function HarnessWithProps({
  vmOverrides,
  ...modalProps
}: { vmOverrides: Partial<Record<string, unknown>> } & Record<string, unknown>) {
  const [form] = Form.useForm();
  const vm = makeVm(form, vmOverrides);
  return <DataSourceManagementModals vm={vm} {...modalProps} />;
}

describe("DataSourceManagementModals", () => {
  it("renders the create provider modal when open, showing the provider picker", () => {
    renderWithProviders(<Harness createProviderModalOpen />);

    expect(screen.getByText("admin.dataSourceCreateKnowledgeSource")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceCreateProviderIntro")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceTypeLocal")).toBeInTheDocument();
  });

  it("does not render the provider modal content when hideProviderModal is true", () => {
    renderWithProviders(
      <HarnessWithProps
        vmOverrides={{ createProviderModalOpen: true }}
        hideProviderModal
      />,
    );

    expect(screen.queryByText("admin.dataSourceCreateProviderIntro")).not.toBeInTheDocument();
  });

  it("renders the feishu auth select modal with account options and an add-new option", () => {
    renderWithProviders(
      <Harness
        authSelectModalOpen
        authSelectProvider="feishu"
        validFeishuAccounts={[
          {
            id: "acc-1",
            name: "Feishu Account",
            connection: { connectionId: "conn-1", accountName: "Feishu Account" },
          },
        ]}
      />,
    );

    expect(screen.getByText("admin.dataSourceSelectFeishuAuthTitle")).toBeInTheDocument();
    expect(screen.getByText("Feishu Account")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceSelectFeishuAuthOther")).toBeInTheDocument();
  });

  it("calls handleSelectFeishuAuthConnection when clicking an existing account card", () => {
    const handleSelectFeishuAuthConnection = vi.fn();
    renderWithProviders(
      <Harness
        authSelectModalOpen
        authSelectProvider="feishu"
        handleSelectFeishuAuthConnection={handleSelectFeishuAuthConnection}
        validFeishuAccounts={[
          {
            id: "acc-1",
            name: "Feishu Account",
            connection: { connectionId: "conn-1", accountName: "Feishu Account" },
          },
        ]}
      />,
    );

    fireEvent.click(screen.getByText("Feishu Account").closest("button")!);

    expect(handleSelectFeishuAuthConnection).toHaveBeenCalledWith({
      connectionId: "conn-1",
      accountName: "Feishu Account",
    });
  });

  it("calls handleAddFeishuAuthFromSelect when clicking the add-new option", () => {
    const handleAddFeishuAuthFromSelect = vi.fn();
    renderWithProviders(
      <Harness
        authSelectModalOpen
        authSelectProvider="feishu"
        handleAddFeishuAuthFromSelect={handleAddFeishuAuthFromSelect}
      />,
    );

    fireEvent.click(screen.getByText("admin.dataSourceSelectFeishuAuthOther").closest("button")!);

    expect(handleAddFeishuAuthFromSelect).toHaveBeenCalled();
  });

  it("renders the notion variant of the auth select modal", () => {
    renderWithProviders(<Harness authSelectModalOpen authSelectProvider="notion" />);
    expect(screen.getByText("admin.dataSourceSelectNotionAuthTitle")).toBeInTheDocument();
  });

  it("renders the manual oauth callback modal and calls setManualOauthCallbackValue on input", () => {
    const setManualOauthCallbackValue = vi.fn();
    renderWithProviders(
      <Harness
        manualOauthModalOpen
        setManualOauthCallbackValue={setManualOauthCallbackValue}
      />,
    );

    expect(screen.getByText("admin.dataSourceOauthManualCallbackTitle")).toBeInTheDocument();

    fireEvent.change(
      screen.getByPlaceholderText("admin.dataSourceOauthManualCallbackPlaceholder"),
      { target: { value: "https://example.com/callback" } },
    );

    expect(setManualOauthCallbackValue).toHaveBeenCalledWith("https://example.com/callback");
  });

  it("calls handleSubmitManualOauthCallback when confirming the manual callback modal", async () => {
    const handleSubmitManualOauthCallback = vi.fn();
    renderWithProviders(
      <Harness
        manualOauthModalOpen
        handleSubmitManualOauthCallback={handleSubmitManualOauthCallback}
      />,
    );

    fireEvent.click(screen.getByText("admin.dataSourceOauthManualCallbackConfirm"));

    await waitFor(() => expect(handleSubmitManualOauthCallback).toHaveBeenCalled());
  });
});
