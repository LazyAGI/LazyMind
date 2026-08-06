import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, testI18n } from "@/test/testUtils";
import DataSourceProviderPicker from "./DataSourceProviderPicker";

const navigateMock = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return { ...actual, useNavigate: () => navigateMock };
});

const t = testI18n.t.bind(testI18n);

function makeVm(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    t,
    creatableSourceTypeOptions: [
      { type: "local" as const, icon: null, logoUrl: undefined, adminOnly: true },
      { type: "feishu" as const, icon: null, logoUrl: undefined, adminOnly: false },
      { type: "notion" as const, icon: null, logoUrl: undefined, adminOnly: false },
    ],
    handleCreateProviderSelect: vi.fn(),
    isFeishuAuthValid: false,
    isNotionAuthValid: false,
    isGoogleDriveAuthValid: false,
    isFeishuSetupReady: true,
    isNotionSetupReady: true,
    ...overrides,
  } as any;
}

describe("DataSourceProviderPicker", () => {
  it("renders a card for every creatable source type", () => {
    renderWithProviders(<DataSourceProviderPicker vm={makeVm()} />);

    expect(screen.getByText("admin.dataSourceTypeLocal")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceTypeFeishu")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceTypeNotion")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAdminOnly")).toBeInTheDocument();
  });

  it("calls handleCreateProviderSelect when clicking a non-cloud card", () => {
    const vm = makeVm();
    renderWithProviders(<DataSourceProviderPicker vm={vm} />);

    fireEvent.click(screen.getByText("admin.dataSourceTypeLocal").closest("button")!);

    expect(vm.handleCreateProviderSelect).toHaveBeenCalledWith("local");
  });

  it("shows the auth valid tag and unlocked state when cloud auth is valid", () => {
    const vm = makeVm({ isFeishuAuthValid: true });
    renderWithProviders(<DataSourceProviderPicker vm={vm} />);

    const feishuCard = screen.getByText("admin.dataSourceTypeFeishu").closest("button")!;
    expect(feishuCard).not.toHaveClass("locked");
    expect(screen.getByText("admin.dataSourceProviderAuthValid")).toBeInTheDocument();
  });

  it("locks the cloud card and shows the credential missing hint when setup is not ready", () => {
    const vm = makeVm({ isFeishuSetupReady: false });
    renderWithProviders(<DataSourceProviderPicker vm={vm} />);

    const feishuCard = screen.getByText("admin.dataSourceTypeFeishu").closest("button")!;
    expect(feishuCard).toHaveClass("locked");
    expect(
      screen.getByText("admin.dataSourceCreateFeishuAuthRequiredHint"),
    ).toBeInTheDocument();
  });

  it("renders and navigates for the google drive option when showGoogleDrive is true", () => {
    const vm = makeVm();
    renderWithProviders(<DataSourceProviderPicker vm={vm} showGoogleDrive />);

    const googleDriveCard = screen
      .getByText("admin.dataSourceTypeGoogleDrive")
      .closest("button")!;
    fireEvent.click(googleDriveCard);

    expect(navigateMock).toHaveBeenCalledWith("/cloud-documents/google-drive");
    expect(vm.handleCreateProviderSelect).not.toHaveBeenCalled();
  });

  it("does not render the google drive option when showGoogleDrive is false", () => {
    renderWithProviders(<DataSourceProviderPicker vm={makeVm()} />);
    expect(screen.queryByText("admin.dataSourceTypeGoogleDrive")).not.toBeInTheDocument();
  });
});
