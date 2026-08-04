import { describe, expect, it, vi } from "vitest";
import { Form } from "antd";
import { fireEvent, renderWithProviders, screen, testI18n, waitFor } from "@/test/testUtils";
import CloudCredentialSetupModal from "./CloudCredentialSetupModal";
import type { FeishuAccountFormValues } from "@/modules/dataSource/common/feishuAccounts";
import type { CloudDataSourceProvider } from "@/modules/dataSource/common/feishuOAuth";

const t = testI18n.t.bind(testI18n);

function Harness({
  cloudSetupProvider,
  submitting = false,
  onCancel = vi.fn(),
  onSave = vi.fn(),
}: {
  cloudSetupProvider: CloudDataSourceProvider;
  submitting?: boolean;
  onCancel?: () => void;
  onSave?: () => void;
}) {
  const [form] = Form.useForm<FeishuAccountFormValues>();
  return (
    <CloudCredentialSetupModal
      t={t}
      cloudSetupProvider={cloudSetupProvider}
      feishuSetupForm={form}
      open
      submitting={submitting}
      onCancel={onCancel}
      onSave={onSave}
    />
  );
}

describe("CloudCredentialSetupModal", () => {
  it("renders the feishu variant with account name field and feishu-specific title", () => {
    renderWithProviders(<Harness cloudSetupProvider="feishu" />);

    expect(screen.getByText("admin.dataSourceFeishuCredentialModalTitle")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceFeishuAccountName")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceFeishuSetupGuideAction")).toBeInTheDocument();
  });

  it("renders the notion variant without an account name field and with notion hint", () => {
    renderWithProviders(<Harness cloudSetupProvider="notion" />);

    expect(screen.getByText("admin.dataSourceNotionCredentialModalTitle")).toBeInTheDocument();
    expect(screen.queryByText("admin.dataSourceFeishuAccountName")).not.toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceNotionCredentialHint")).toBeInTheDocument();
  });

  it("renders app id and app secret fields for both providers", () => {
    renderWithProviders(<Harness cloudSetupProvider="feishu" />);

    expect(screen.getByText("admin.dataSourceAppId")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAppSecret")).toBeInTheDocument();
  });

  it("calls onSave when clicking the ok button", async () => {
    const onSave = vi.fn();
    renderWithProviders(<Harness cloudSetupProvider="feishu" onSave={onSave} />);

    fireEvent.click(screen.getByText("admin.dataSourceFeishuCredentialSaveAndSelect"));

    await waitFor(() => expect(onSave).toHaveBeenCalled());
  });

  it("calls onCancel when clicking the cancel button", async () => {
    const onCancel = vi.fn();
    renderWithProviders(<Harness cloudSetupProvider="feishu" onCancel={onCancel} />);

    fireEvent.click(screen.getByText("common.cancel"));

    await waitFor(() => expect(onCancel).toHaveBeenCalled());
  });

  it("disables the cancel button while submitting", () => {
    renderWithProviders(<Harness cloudSetupProvider="feishu" submitting />);

    expect(screen.getByText("common.cancel").closest("button")).toBeDisabled();
  });
});
