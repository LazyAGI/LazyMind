import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ChatFiles from "./index";
import { readPreviewFile } from "../ArtifactPreview/target";
import { resolveMarkdownImageUrlAsync } from "@/modules/knowledge/utils/imageUrl";
import { message } from "antd";
import { downloadStream } from "../../utils/download";

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveMarkdownImageUrlAsync: vi.fn(async () => "/api/core/static-files/report.pdf?sig=signed"),
}));
vi.mock("../ArtifactPreview/target", () => ({
  readPreviewFile: vi.fn(async () => new Uint8Array([1, 2, 3]).buffer),
  urlPreviewTarget: (url: string, filename: string) => ({ url, filename }),
}));
vi.mock("../../utils/download", () => ({ downloadStream: vi.fn() }));

describe("chat attachment downloads", () => {
  it("downloads the referenced file bytes with its original filename", async () => {
    render(<ChatFiles files={[{ name: "report.pdf", uid: "existing", uri: "/var/lib/lazymind/uploads/report.pdf" }]} />);
    fireEvent.click(screen.getByRole("button", { name: /report.pdf/ }));
    await waitFor(() => expect(downloadStream).toHaveBeenCalledWith(expect.any(Blob), "report.pdf"));
    expect(resolveMarkdownImageUrlAsync).toHaveBeenCalledWith("/var/lib/lazymind/uploads/report.pdf");
    expect(readPreviewFile).toHaveBeenCalledWith(expect.objectContaining({ url: "/api/core/static-files/report.pdf?sig=signed" }), expect.any(AbortSignal));
  });

  it("shows a download error when an attachment is no longer accessible", async () => {
    vi.mocked(readPreviewFile).mockRejectedValueOnce(new Error("unavailable"));
    const error = vi.spyOn(message, "error").mockImplementation(() => (() => undefined) as any);
    render(<ChatFiles files={[{ name: "expired.pdf", uid: "expired", uri: "/static-files/expired.pdf" }]} />);
    fireEvent.click(screen.getByRole("button", { name: /expired.pdf/ }));
    await waitFor(() => expect(error).toHaveBeenCalled());
    error.mockRestore();
  });

  it("keeps unavailable or legacy attachments as noninteractive labels", () => {
    render(<ChatFiles files={[
      { name: "missing.pdf", uid: "missing", uri: "/static-files/missing.pdf", unavailable: true },
      { name: "legacy.pdf", uid: "legacy" },
    ]} />);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText(/missing.pdf/)).toBeInTheDocument();
    expect(screen.getByText("legacy.pdf")).toBeInTheDocument();
  });
});
