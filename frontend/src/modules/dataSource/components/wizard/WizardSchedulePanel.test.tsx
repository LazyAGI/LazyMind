import { describe, expect, it } from "vitest";
import { Form } from "antd";
import { useEffect } from "react";
import { fireEvent, renderWithProviders, screen, testI18n } from "@/test/testUtils";
import WizardSchedulePanel from "./WizardSchedulePanel";
import type { SourceFormValues } from "../../constants/types";

const t = testI18n.t.bind(testI18n);

function Harness({ initialWeekdays = [] as string[] }) {
  const [form] = Form.useForm<SourceFormValues>();
  useEffect(() => {
    form.setFieldValue("scheduleWeekdays", initialWeekdays);
  }, [form, initialWeekdays]);
  return (
    <Form form={form}>
      <WizardSchedulePanel t={t} form={form} />
    </Form>
  );
}

describe("WizardSchedulePanel", () => {
  it("renders the schedule title and weekday shortcuts", () => {
    renderWithProviders(<Harness />);

    expect(screen.getByText("admin.dataSourceScheduleTitle")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceScheduleShortcutWorkdays")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceScheduleShortcutWeekends")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceScheduleShortcutEveryday")).toBeInTheDocument();
  });

  it("marks the workdays shortcut active when workdays are selected", () => {
    renderWithProviders(<Harness initialWeekdays={["1", "2", "3", "4", "5"]} />);

    const workdaysButton = screen
      .getByText("admin.dataSourceScheduleShortcutWorkdays")
      .closest("button")!;
    expect(workdaysButton).toHaveClass("is-active");
  });

  it("selects workdays when clicking the workdays shortcut from an empty selection", () => {
    renderWithProviders(<Harness />);

    const workdaysButton = screen
      .getByText("admin.dataSourceScheduleShortcutWorkdays")
      .closest("button")!;
    fireEvent.click(workdaysButton);

    expect(workdaysButton).toHaveClass("is-active");
  });

  it("clears the selection when clicking an already-active shortcut", () => {
    renderWithProviders(<Harness initialWeekdays={["6", "7"]} />);

    const weekendsButton = screen
      .getByText("admin.dataSourceScheduleShortcutWeekends")
      .closest("button")!;
    expect(weekendsButton).toHaveClass("is-active");

    fireEvent.click(weekendsButton);

    expect(weekendsButton).not.toHaveClass("is-active");
  });

  it("renders weekday checkboxes for all display-order days", () => {
    renderWithProviders(<Harness />);

    expect(screen.getByText("admin.dataSourceScheduleWeekdayShort7")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceScheduleWeekdayShort1")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceScheduleWeekdayShort6")).toBeInTheDocument();
  });
});
