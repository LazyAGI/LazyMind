import { describe, expect, it, vi } from "vitest";
import { Form } from "antd";
import { renderWithProviders, screen, testI18n } from "@/test/testUtils";
import WizardConnectionStep from "./WizardConnectionStep";
import type { SourceFormValues, SourceType, SyncMode } from "../../constants/types";

const t = testI18n.t.bind(testI18n);

function Harness({
  selectedType,
  syncMode = "manual",
}: {
  selectedType: SourceType;
  syncMode?: SyncMode;
}) {
  const [form] = Form.useForm<SourceFormValues>();
  return (
    <Form form={form}>
      <WizardConnectionStep
        t={t}
        form={form}
        selectedType={selectedType}
        syncMode={syncMode}
        localPathOptions={[]}
        localPathLoading={false}
        feishuTargetLoading={false}
        feishuTargetTreeData={[]}
        onLoadLocalPathOptions={vi.fn()}
        onSearchLocalPathOptions={vi.fn()}
        onLoadFeishuTargetOptions={vi.fn()}
        onSearchFeishuTargetOptions={vi.fn()}
      />
    </Form>
  );
}

describe("WizardConnectionStep", () => {
  it("renders the basic config and access config sections for the local type", () => {
    renderWithProviders(<Harness selectedType="local" />);

    expect(screen.getByText("admin.dataSourceBasicConfig")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAccessConfig")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAccessPath")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceKnowledgeBaseName")).toBeInTheDocument();
  });

  it("renders the feishu space field for the feishu type", () => {
    renderWithProviders(<Harness selectedType="feishu" />);

    expect(screen.getByText("admin.dataSourceFeishuSpace")).toBeInTheDocument();
  });

  it("renders the notion target type and target fields for the notion type", () => {
    renderWithProviders(<Harness selectedType="notion" />);

    expect(screen.getByText("admin.dataSourceNotionTargetTypeLabel")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceNotionTargetLabel")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceNotionTargetTypePage")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceNotionTargetTypeDatabase")).toBeInTheDocument();
  });

  it("renders the file types field for every type", () => {
    renderWithProviders(<Harness selectedType="local" />);
    expect(screen.getByText("admin.dataSourceFileTypes")).toBeInTheDocument();
  });

  it("renders the sync strategy section with sync mode options", () => {
    renderWithProviders(<Harness selectedType="local" />);

    expect(screen.getByText("admin.dataSourceSyncStrategyTitle")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceSyncModeScheduled")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceSyncModeManual")).toBeInTheDocument();
  });

  it("shows the schedule panel only when syncMode is scheduled", () => {
    const { rerender } = renderWithProviders(<Harness selectedType="local" syncMode="manual" />);
    expect(screen.queryByText("admin.dataSourceScheduleTitle")).not.toBeInTheDocument();

    rerender(<Harness selectedType="local" syncMode="scheduled" />);
    expect(screen.getByText("admin.dataSourceScheduleTitle")).toBeInTheDocument();
  });
});
