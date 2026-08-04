import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Form } from "antd";
import FeishuAccountFormModal from "./FeishuAccountFormModal";

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

function Harness({
  isEditing = false,
  submitting = false,
  onCancel = vi.fn(),
  onOk = vi.fn(),
}: {
  isEditing?: boolean;
  submitting?: boolean;
  onCancel?: () => void;
  onOk?: () => void;
}) {
  const [form] = Form.useForm();
  return (
    <FeishuAccountFormModal
      t={t}
      open
      isEditing={isEditing}
      submitting={submitting}
      form={form}
      onCancel={onCancel}
      onOk={onOk}
    />
  );
}

describe("FeishuAccountFormModal", () => {
  it("renders the create title and form fields when not editing", () => {
    render(<Harness />);
    expect(
      screen.getByText("admin.dataSourceFeishuAccountCreate"),
    ).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAppId")).toBeInTheDocument();
    expect(screen.getByText("admin.dataSourceAppSecret")).toBeInTheDocument();
  });

  it("renders the edit title when isEditing is true", () => {
    render(<Harness isEditing />);
    expect(
      screen.getByText("admin.dataSourceFeishuAccountEdit"),
    ).toBeInTheDocument();
  });

  it("calls onOk when clicking the confirm button", () => {
    const onOk = vi.fn();
    render(<Harness onOk={onOk} />);
    fireEvent.click(
      screen.getByText("admin.dataSourceFeishuAccountSaveAndAuthorize"),
    );
    expect(onOk).toHaveBeenCalledTimes(1);
  });

  it("calls onCancel when clicking cancel while not submitting", () => {
    const onCancel = vi.fn();
    render(<Harness onCancel={onCancel} />);
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("disables the cancel button and shows a loading confirm button while submitting", () => {
    const onCancel = vi.fn();
    render(<Harness submitting onCancel={onCancel} />);
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onCancel).not.toHaveBeenCalled();
    const cancelButton = screen.getByText("common.cancel").closest("button");
    expect(cancelButton).toBeDisabled();
  });
});
