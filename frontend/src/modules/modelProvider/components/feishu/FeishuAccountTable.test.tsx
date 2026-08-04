import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import FeishuAccountTable from "./FeishuAccountTable";
import type { FeishuAuthAccount } from "@/modules/dataSource/common/feishuAccounts";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const makeAccount = (
  overrides: Partial<FeishuAuthAccount> = {},
): FeishuAuthAccount => ({
  id: "acc-1",
  name: "My Feishu App",
  appId: "cli_abc123",
  appSecret: "secret",
  chatEnabled: false,
  status: "connected",
  connection: { connectionId: "conn-1" } as any,
  createdAt: "2024-01-01T00:00:00Z",
  ...overrides,
});

const baseProps = {
  t,
  accounts: [] as FeishuAuthAccount[],
  accountsLoading: false,
  onAuthorize: vi.fn(),
  onEdit: vi.fn(),
  onDelete: vi.fn(),
  onToggleChat: vi.fn(),
};

describe("FeishuAccountTable", () => {
  it("renders an empty state when there are no accounts", () => {
    render(<FeishuAccountTable {...baseProps} />);
    expect(
      screen.getByText("admin.dataSourceFeishuAccountEmptyTitle"),
    ).toBeInTheDocument();
  });

  it("renders account rows with name, appId and a valid open platform link", () => {
    render(<FeishuAccountTable {...baseProps} accounts={[makeAccount()]} />);
    expect(screen.getByText("My Feishu App")).toBeInTheDocument();
    expect(screen.getByText("cli_abc123")).toBeInTheDocument();
    expect(
      screen.getByText("modelProvider.cloudDocuments.authValid"),
    ).toBeInTheDocument();
    const link = screen.getByRole("link");
    expect(link).toHaveAttribute(
      "href",
      "https://open.feishu.cn/app/cli_abc123/baseinfo",
    );
  });

  it("shows a no-data placeholder when the appId is not a valid feishu app id", () => {
    render(
      <FeishuAccountTable
        {...baseProps}
        accounts={[makeAccount({ appId: "not-an-app-id" })]}
      />,
    );
    expect(screen.getByText("common.noData")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("shows a pending tag for waiting status", () => {
    render(
      <FeishuAccountTable
        {...baseProps}
        accounts={[makeAccount({ status: "waiting" })]}
      />,
    );
    expect(
      screen.getByText("modelProvider.cloudDocuments.authPending"),
    ).toBeInTheDocument();
  });

  it("enables the chat toggle switch when auth is valid and calls onToggleChat", () => {
    const onToggleChat = vi.fn();
    render(
      <FeishuAccountTable
        {...baseProps}
        accounts={[makeAccount({ chatEnabled: false })]}
        onToggleChat={onToggleChat}
      />,
    );
    const switchButton = screen.getByRole("switch");
    expect(switchButton).not.toBeDisabled();
    fireEvent.click(switchButton);
    expect(onToggleChat).toHaveBeenCalledWith(
      expect.objectContaining({ id: "acc-1" }),
      true,
    );
  });

  it("disables the chat toggle when auth is not valid", () => {
    render(
      <FeishuAccountTable
        {...baseProps}
        accounts={[makeAccount({ status: "expired", connection: null })]}
      />,
    );
    expect(screen.getByRole("switch")).toBeDisabled();
  });

  it("triggers authorize, edit and delete actions", () => {
    const onAuthorize = vi.fn();
    const onEdit = vi.fn();
    const onDelete = vi.fn();
    render(
      <FeishuAccountTable
        {...baseProps}
        accounts={[makeAccount()]}
        onAuthorize={onAuthorize}
        onEdit={onEdit}
        onDelete={onDelete}
      />,
    );
    fireEvent.click(screen.getByText("admin.dataSourceFeishuReconnectAction"));
    expect(onAuthorize).toHaveBeenCalledWith(expect.objectContaining({ id: "acc-1" }));
    fireEvent.click(screen.getByText("common.edit"));
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ id: "acc-1" }));
    fireEvent.click(screen.getByText("common.delete"));
    expect(onDelete).toHaveBeenCalledWith(expect.objectContaining({ id: "acc-1" }));
  });

  it("shows the authorize action label for accounts that are not connected", () => {
    render(
      <FeishuAccountTable
        {...baseProps}
        accounts={[makeAccount({ status: "error" })]}
      />,
    );
    expect(
      screen.getByText("admin.dataSourceFeishuAuthorizeAction"),
    ).toBeInTheDocument();
  });
});
