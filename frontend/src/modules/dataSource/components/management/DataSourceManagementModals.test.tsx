import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./DataSourceProviderPicker", () => ({ default: () => null }));
vi.mock("./CloudCredentialSetupModal", () => ({ default: () => null }));

import DataSourceManagementModals from "./DataSourceManagementModals";

afterEach(cleanup);

function renderAccounts(provider: "notion" | "feishu", existing = true) {
  const add = vi.fn();
  const select = vi.fn();
  const connection = { connectionId: "fixture-connection", accountName: "Fixture account" };
  render(<DataSourceManagementModals vm={{
    t: (key: string) => key,
    authSelectModalOpen: true,
    authSelectProvider: provider,
    validNotionAccounts: existing ? [{ id: "fixture-notion", connection }] : [],
    validFeishuAccounts: existing ? [{ id: "fixture-feishu", connection }] : [],
    handleAddNotionAuthFromSelect: add,
    handleAddFeishuAuthFromSelect: add,
    handleSelectNotionAuthConnection: select,
    handleSelectFeishuAuthConnection: select,
    createProviderModalOpen: false,
    feishuSetupModalOpen: false,
    manualOauthModalOpen: false,
  } as never} />);
  return { add, select, connection };
}

describe("Notion single connection selection", () => {
  it("offers the saved Notion connection without an additional-account action", () => {
    const { select, connection } = renderAccounts("notion");
    expect(screen.getByText("modelProvider.cloudDocuments.notionSingleConnectionHint")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /admin.dataSourceSelectNotionAuthOther/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Fixture account/ }));
    expect(select).toHaveBeenCalledWith(connection);
  });

  it("allows creating the first Notion connection", () => {
    const { add } = renderAccounts("notion", false);
    fireEvent.click(screen.getByRole("button", { name: /admin.dataSourceSelectNotionAuthOther/ }));
    expect(add).toHaveBeenCalledOnce();
  });

  it("keeps additional Feishu accounts available", () => {
    const { add } = renderAccounts("feishu");
    fireEvent.click(screen.getByRole("button", { name: /admin.dataSourceSelectFeishuAuthOther/ }));
    expect(add).toHaveBeenCalledOnce();
    expect(screen.queryByText("modelProvider.cloudDocuments.notionSingleConnectionHint")).not.toBeInTheDocument();
  });
});
