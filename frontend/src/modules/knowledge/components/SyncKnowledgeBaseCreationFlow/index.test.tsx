import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { SyncKnowledgeBaseCreationVm } from "@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation";

const useSyncKnowledgeBaseCreationMock = vi.fn();
vi.mock("@/modules/knowledge/hooks/useSyncKnowledgeBaseCreation", () => ({
  useSyncKnowledgeBaseCreation: (...args: unknown[]) =>
    useSyncKnowledgeBaseCreationMock(...args),
}));

vi.mock("@/modules/dataSource/components/DataSourceWizardModal", () => ({
  default: (props: Record<string, unknown>) => (
    <div data-testid="wizard-modal" data-open={String(props.wizardOpen)}>
      <button type="button" onClick={() => (props.onNext as () => void)()}>
        next
      </button>
      <button
        type="button"
        onClick={() => (props.onSave as (mode: string) => void)("createAndSync")}
      >
        save
      </button>
    </div>
  ),
}));

vi.mock("@/modules/dataSource/components/management/DataSourceManagementModals", () => ({
  default: (props: Record<string, unknown>) => (
    <div data-testid="management-modals" data-hide={String(props.hideProviderModal)} />
  ),
}));

vi.mock("@/modules/dataSource/index.scss", () => ({}));
vi.mock("./index.scss", () => ({}));

import SyncKnowledgeBaseCreationFlow from "./index";

function makeVm(overrides: Partial<SyncKnowledgeBaseCreationVm> = {}): SyncKnowledgeBaseCreationVm {
  return {
    t: ((key: string) => key) as any,
    form: {} as any,
    wizardOpen: false,
    wizardStep: 0,
    setWizardStep: vi.fn(),
    wizardMode: "create",
    selectedType: null,
    syncMode: "scheduled",
    wizardSaving: false,
    wizardSavingMode: null,
    isFeishuSetupReady: false,
    isNotionSetupReady: false,
    localPathOptions: [],
    localPathLoading: false,
    loadLocalPathOptions: vi.fn(),
    handleSearchLocalPathOptions: vi.fn(),
    handleLoadLocalPathChildren: vi.fn(),
    resetLocalPathBrowseOptions: vi.fn(),
    feishuTargetTreeData: [],
    feishuTargetLoading: false,
    loadFeishuTargetOptions: vi.fn(),
    handleSearchFeishuTargetOptions: vi.fn(),
    handleLoadFeishuTargetChildren: vi.fn(),
    handleCloseWizard: vi.fn(),
    handleNextStep: vi.fn(),
    requestSaveWithSyncConfirm: vi.fn(),
    handleSelectType: vi.fn(),
    handleResetFeishuSetup: vi.fn(),
    handleResetNotionSetup: vi.fn(),
    ...overrides,
  } as SyncKnowledgeBaseCreationVm;
}

describe("SyncKnowledgeBaseCreationFlow", () => {
  it("renders the management modals and wizard modal using the provided vm", () => {
    const vm = makeVm({ wizardOpen: true });
    render(<SyncKnowledgeBaseCreationFlow vm={vm} />);

    expect(screen.getByTestId("management-modals")).toBeInTheDocument();
    expect(screen.getByTestId("wizard-modal")).toHaveAttribute("data-open", "true");
    expect(useSyncKnowledgeBaseCreationMock).not.toHaveBeenCalled();
  });

  it("uses useSyncKnowledgeBaseCreation when no vm prop is provided", () => {
    useSyncKnowledgeBaseCreationMock.mockReturnValue(makeVm());
    const onSuccess = vi.fn();
    render(<SyncKnowledgeBaseCreationFlow onSuccess={onSuccess} />);

    expect(useSyncKnowledgeBaseCreationMock).toHaveBeenCalledWith({ onSuccess });
    expect(screen.getByTestId("wizard-modal")).toBeInTheDocument();
  });

  it("forwards hideProviderModal to the management modals", () => {
    const vm = makeVm();
    render(<SyncKnowledgeBaseCreationFlow vm={vm} hideProviderModal />);

    expect(screen.getByTestId("management-modals")).toHaveAttribute("data-hide", "true");
  });

  it("calls requestSaveWithSyncConfirm when the wizard save button is triggered", () => {
    const vm = makeVm();
    render(<SyncKnowledgeBaseCreationFlow vm={vm} />);

    fireEvent.click(screen.getByText("save"));

    expect(vm.requestSaveWithSyncConfirm).toHaveBeenCalledWith("createAndSync");
  });

  it("calls handleNextStep when the wizard next button is triggered", () => {
    const vm = makeVm();
    render(<SyncKnowledgeBaseCreationFlow vm={vm} />);

    fireEvent.click(screen.getByText("next"));

    expect(vm.handleNextStep).toHaveBeenCalled();
  });
});
