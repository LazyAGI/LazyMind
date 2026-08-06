import { describe, expect, it, vi } from "vitest";
import { Form } from "antd";
import { fireEvent, renderWithProviders, screen, testI18n } from "@/test/testUtils";
import DataSourceWizardModal from "./DataSourceWizardModal";
import type { SourceFormValues, SourceType } from "../constants/types";

const t = testI18n.t.bind(testI18n);

function Harness({
  wizardMode = "create" as "create" | "edit",
  wizardStep = 0,
  selectedType = null as SourceType | null,
  saving = false,
  savingMode,
  allowTypeSelection = true,
  onClose = vi.fn(),
  onPrev = vi.fn(),
  onNext = vi.fn(),
  onSave = vi.fn(),
  onSelectType = vi.fn(),
}) {
  const [form] = Form.useForm<SourceFormValues>();
  return (
    <DataSourceWizardModal
      t={t}
      wizardMode={wizardMode}
      wizardOpen
      wizardStep={wizardStep}
      form={form}
      selectedType={selectedType}
      isFeishuSetupReady
      isNotionSetupReady
      syncMode="manual"
      saving={saving}
      savingMode={savingMode}
      allowTypeSelection={allowTypeSelection}
      onClose={onClose}
      onPrev={onPrev}
      onNext={onNext}
      onSave={onSave}
      onSelectType={onSelectType}
      onResetFeishuSetup={vi.fn()}
    />
  );
}

describe("DataSourceWizardModal", () => {
  it("shows the create title and step type on the first step", () => {
    renderWithProviders(<Harness wizardStep={0} />);

    expect(screen.getByText("admin.dataSourceCreate")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceTypeLocal")).toBeInTheDocument();
  });

  it("shows the edit title in edit mode", () => {
    renderWithProviders(<Harness wizardMode="edit" wizardStep={1} selectedType="local" />);
    expect(screen.getByText("admin.dataSourceEdit")).toBeInTheDocument();
  });

  it("shows the empty state when on the connection step without a selected type", () => {
    renderWithProviders(<Harness wizardStep={1} selectedType={null} />);
    expect(screen.getByText("admin.dataSourceSelectTypeInPrevStep")).toBeInTheDocument();
  });

  it("shows the connection step form when a type is selected", () => {
    renderWithProviders(<Harness wizardStep={1} selectedType="local" />);
    expect(screen.getByText("admin.dataSourceKnowledgeBaseName")).toBeInTheDocument();
  });

  it("calls onNext when clicking next on the first step", () => {
    const onNext = vi.fn();
    renderWithProviders(<Harness wizardStep={0} onNext={onNext} />);

    fireEvent.click(screen.getByText("admin.dataSourceWizardNext"));

    expect(onNext).toHaveBeenCalled();
  });

  it("calls onSave with create when clicking the create-only button on the final step", () => {
    const onSave = vi.fn();
    renderWithProviders(<Harness wizardStep={1} selectedType="local" onSave={onSave} />);

    fireEvent.click(screen.getByText("admin.dataSourceCreateOnly"));

    expect(onSave).toHaveBeenCalledWith("create");
  });

  it("calls onSave with createAndSync when clicking the create-and-sync button", () => {
    const onSave = vi.fn();
    renderWithProviders(<Harness wizardStep={1} selectedType="local" onSave={onSave} />);

    fireEvent.click(screen.getByText("admin.dataSourceCreateAndSync"));

    expect(onSave).toHaveBeenCalledWith("createAndSync");
  });

  it("calls onClose when clicking cancel while not saving", () => {
    const onClose = vi.fn();
    renderWithProviders(<Harness wizardStep={0} onClose={onClose} />);

    fireEvent.click(screen.getByText("common.cancel"));

    expect(onClose).toHaveBeenCalled();
  });

  it("does not render the prev button on the first step", () => {
    renderWithProviders(<Harness wizardStep={0} />);
    expect(screen.queryByText("admin.dataSourceWizardPrev")).not.toBeInTheDocument();
  });

  it("renders the prev button on the second step in create mode", () => {
    renderWithProviders(<Harness wizardStep={1} selectedType="local" />);
    expect(screen.getByText("admin.dataSourceWizardPrev")).toBeInTheDocument();
  });
});
