import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, testI18n } from "@/test/testUtils";
import WizardTypeStep from "./WizardTypeStep";

const t = testI18n.t.bind(testI18n);

describe("WizardTypeStep", () => {
  it("renders all source type cards with their titles", () => {
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType={null}
        isFeishuSetupReady
        isNotionSetupReady
        onSelectType={vi.fn()}
        onResetFeishuSetup={vi.fn()}
      />,
    );

    expect(screen.getByText("admin.dataSourceTypeLocal")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceTypeFeishu")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceTypeNotion")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAdminOnly")).toBeInTheDocument();
  });

  it("marks the selected type card as selected", () => {
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType="feishu"
        isFeishuSetupReady
        isNotionSetupReady
        onSelectType={vi.fn()}
        onResetFeishuSetup={vi.fn()}
      />,
    );

    const feishuCard = screen.getByText("admin.dataSourceTypeFeishu").closest("button");
    expect(feishuCard).toHaveClass("selected");
  });

  it("calls onSelectType when a card is clicked", () => {
    const onSelectType = vi.fn();
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType={null}
        isFeishuSetupReady
        isNotionSetupReady
        onSelectType={onSelectType}
        onResetFeishuSetup={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByText("admin.dataSourceTypeLocal").closest("button")!);

    expect(onSelectType).toHaveBeenCalledWith("local");
  });

  it("shows the lock hint and lock icon when feishu setup is not ready", () => {
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType={null}
        isFeishuSetupReady={false}
        isNotionSetupReady
        onSelectType={vi.fn()}
        onResetFeishuSetup={vi.fn()}
      />,
    );

    const feishuCard = screen.getByText("admin.dataSourceTypeFeishu").closest("button");
    expect(feishuCard).toHaveClass("locked");
    expect(screen.getByText("admin.dataSourceFeishuLockHint")).toBeInTheDocument();
  });

  it("shows the notion setup required hint when notion setup is not ready", () => {
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType={null}
        isFeishuSetupReady
        isNotionSetupReady={false}
        onSelectType={vi.fn()}
        onResetFeishuSetup={vi.fn()}
      />,
    );

    expect(
      screen.getByText("admin.dataSourceNotionSetupRequiredForCreate"),
    ).toBeInTheDocument();
  });

  it("calls onResetFeishuSetup when clicking the disconnect button on the feishu card while setup is ready", () => {
    const onResetFeishuSetup = vi.fn();
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType={null}
        isFeishuSetupReady
        isNotionSetupReady
        onSelectType={vi.fn()}
        onResetFeishuSetup={onResetFeishuSetup}
      />,
    );

    const feishuCard = screen.getByText("admin.dataSourceTypeFeishu").closest("button")!;
    const resetButton = feishuCard.querySelector(".data-source-type-gate-button")!;
    fireEvent.click(resetButton);

    expect(onResetFeishuSetup).toHaveBeenCalled();
  });

  it("calls onResetNotionSetup when clicking the disconnect button on the notion card while setup is ready", () => {
    const onResetNotionSetup = vi.fn();
    renderWithProviders(
      <WizardTypeStep
        t={t}
        selectedType={null}
        isFeishuSetupReady
        isNotionSetupReady
        onSelectType={vi.fn()}
        onResetFeishuSetup={vi.fn()}
        onResetNotionSetup={onResetNotionSetup}
      />,
    );

    const notionCard = screen.getByText("admin.dataSourceTypeNotion").closest("button")!;
    const resetButton = notionCard.querySelector(".data-source-type-gate-button")!;
    fireEvent.click(resetButton);

    expect(onResetNotionSetup).toHaveBeenCalled();
  });
});
