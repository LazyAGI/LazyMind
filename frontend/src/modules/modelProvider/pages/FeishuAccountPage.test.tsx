import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { Form } from "antd";
import { renderWithProviders } from "@/test/testUtils";
import FeishuAccountPage from "./FeishuAccountPage";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const useFeishuAccountsMock = vi.fn();

vi.mock("../hooks/useFeishuAccounts", () => ({
  useFeishuAccounts: () => useFeishuAccountsMock(),
}));

vi.mock("../components/feishu/FeishuAccountTable", () => ({
  default: (props: { accounts: unknown[] }) => (
    <div data-testid="feishu-account-table">{props.accounts.length}</div>
  ),
}));

vi.mock("../components/feishu/FeishuAccountFormModal", () => ({
  default: (props: { open: boolean }) =>
    props.open ? <div data-testid="feishu-account-form-modal" /> : null,
}));

const vmMock = {
  t,
  callbackUrl: "https://app.example.com/oauth/feishu/callback",
  accounts: [{ id: "1" }, { id: "2" }],
  accountsLoading: false,
  modalOpen: false,
  editingAccountId: null,
  submitting: false,
  manualOauthModalOpen: false,
  manualOauthCallbackValue: "",
  manualOauthSubmitting: false,
  setModalOpen: vi.fn(),
  setEditingAccountId: vi.fn(),
  setManualOauthModalOpen: vi.fn(),
  setManualOauthCallbackValue: vi.fn(),
  openAccountModal: vi.fn(),
  handleSaveAccount: vi.fn(),
  handleAuthorizeAccount: vi.fn(),
  handleDeleteAccount: vi.fn(),
  handleToggleChat: vi.fn(),
  handleSubmitManualOauthCallback: vi.fn(),
};

function Harness() {
  const [form] = Form.useForm();
  useFeishuAccountsMock.mockReturnValue({ ...vmMock, form });
  return <FeishuAccountPage />;
}

describe("FeishuAccountPage", () => {
  it("renders the section title, callback url and account table", () => {
    renderWithProviders(<Harness />);
    expect(
      screen.getByText("modelProvider.cloudDocuments.feishuAccountManagementTitle"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("https://app.example.com/oauth/feishu/callback"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("feishu-account-table")).toHaveTextContent("2");
  });

  it("shows the reauthorize hint when multiple accounts exist", () => {
    renderWithProviders(<Harness />);
    expect(
      screen.getByText("modelProvider.cloudDocuments.feishuAccountReauthorizeHint"),
    ).toBeInTheDocument();
  });

  it("calls openAccountModal when clicking the create button", () => {
    renderWithProviders(<Harness />);
    fireEvent.click(screen.getByText("modelProvider.cloudDocuments.feishuAccountCreate"));
    expect(vmMock.openAccountModal).toHaveBeenCalledTimes(1);
  });

  it("navigates back via the breadcrumb without throwing", () => {
    renderWithProviders(<Harness />);
    const backButton = screen.getByText("modelProvider.cloudDocuments.backToProviders");
    expect(() => fireEvent.click(backButton)).not.toThrow();
  });
});
