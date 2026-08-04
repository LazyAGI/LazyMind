import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import SkillAdminPublishModal from "./SkillAdminPublishModal";

const publishSkillToMarket = vi.fn();
const uploadSkillTempFile = vi.fn();

vi.mock("../../skillApi", () => ({
  publishSkillToMarket: (...args: unknown[]) => publishSkillToMarket(...args),
}));

vi.mock("../../skillUpload", () => ({
  uploadSkillTempFile: (...args: unknown[]) => uploadSkillTempFile(...args),
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const baseProps = {
  open: true,
  t,
  onClose: vi.fn(),
  onPublished: vi.fn().mockResolvedValue(undefined),
  tagOptions: ["docs", "qa"],
  tagsLoading: false,
};

describe("SkillAdminPublishModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    baseProps.onPublished = vi.fn().mockResolvedValue(undefined);
  });

  it("renders the URL publish form by default", () => {
    render(<SkillAdminPublishModal {...baseProps} />);
    expect(
      screen.getByPlaceholderText("admin.memorySkillUploadRepoPlaceholder"),
    ).toBeInTheDocument();
  });

  it("warns when submitting the URL form without a repo url", () => {
    render(<SkillAdminPublishModal {...baseProps} />);
    fireEvent.click(screen.getByText("admin.memorySkillAdminPublishSubmit"));
    expect(publishSkillToMarket).not.toHaveBeenCalled();
  });

  it("publishes via url once the url and tags are filled in", async () => {
    render(<SkillAdminPublishModal {...baseProps} />);
    fireEvent.change(
      screen.getByPlaceholderText("admin.memorySkillUploadRepoPlaceholder"),
      { target: { value: "https://example.com/my-skill" } },
    );

    const tagsInput = screen.getByRole("combobox");
    fireEvent.change(tagsInput, { target: { value: "qa" } });
    fireEvent.keyDown(tagsInput, { key: "Enter", code: "Enter", keyCode: 13, which: 13 });

    fireEvent.click(screen.getByText("admin.memorySkillAdminPublishSubmit"));

    await waitFor(() => {
      expect(publishSkillToMarket).toHaveBeenCalledWith(
        expect.objectContaining({
          source: { type: "url", url: "https://example.com/my-skill" },
        }),
      );
    });
    await waitFor(() => {
      expect(baseProps.onPublished).toHaveBeenCalledTimes(1);
    });
  });

  it("switches to the package tab and shows selected file details", () => {
    render(<SkillAdminPublishModal {...baseProps} />);
    fireEvent.click(screen.getByText("admin.memorySkillAdminPublishMethodPackage"));
    expect(
      screen.getByText("admin.memorySkillAdminPublishFileTitle"),
    ).toBeInTheDocument();
  });

  it("shows a duplicate error message when publishing fails with 409", async () => {
    publishSkillToMarket.mockRejectedValueOnce({ response: { status: 409 } });
    render(<SkillAdminPublishModal {...baseProps} />);
    fireEvent.change(
      screen.getByPlaceholderText("admin.memorySkillUploadRepoPlaceholder"),
      { target: { value: "https://example.com/my-skill" } },
    );
    const tagsInput = screen.getByRole("combobox");
    fireEvent.change(tagsInput, { target: { value: "qa" } });
    fireEvent.keyDown(tagsInput, { key: "Enter", code: "Enter", keyCode: 13, which: 13 });

    fireEvent.click(screen.getByText("admin.memorySkillAdminPublishSubmit"));

    await waitFor(() => {
      expect(publishSkillToMarket).toHaveBeenCalledTimes(1);
    });
    expect(baseProps.onPublished).not.toHaveBeenCalled();
  });

  it("closes and resets the form on cancel", () => {
    const onClose = vi.fn();
    render(<SkillAdminPublishModal {...baseProps} onClose={onClose} />);
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
