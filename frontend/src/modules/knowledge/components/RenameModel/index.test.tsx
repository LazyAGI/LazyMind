import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import RenameModel, { RenameModalRef } from "./index";

const documentServiceAllDocumentTags = vi.fn();
vi.mock("@/modules/knowledge/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceAllDocumentTags: documentServiceAllDocumentTags,
  }),
}));

describe("RenameModel", () => {
  beforeEach(() => {
    documentServiceAllDocumentTags.mockResolvedValue({ data: { tags: ["a", "b"] } });
  });

  it("is hidden until onOpen is called via the imperative ref", () => {
    renderWithProviders(<RenameModel onSubmit={vi.fn()} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens the modal with the provided title and pre-filled data", async () => {
    const ref = createRef<RenameModalRef>();
    renderWithProviders(<RenameModel ref={ref} onSubmit={vi.fn()} />);

    ref.current?.onOpen({
      title: "Rename file",
      form: {
        name: "File name",
        namePlaceholder: "Enter a name",
        nameLen: 30,
        nameRules: [],
      },
      data: { name: "existing-name" },
    });

    await waitFor(() => {
      expect(screen.getByText("Rename file")).toBeInTheDocument();
    });
    expect(screen.getByPlaceholderText("Enter a name")).toHaveValue("existing-name");
  });

  it("does not render the tags selector when nameAdd is not set", async () => {
    const ref = createRef<RenameModalRef>();
    renderWithProviders(<RenameModel ref={ref} onSubmit={vi.fn()} />);

    ref.current?.onOpen({
      title: "Rename folder",
      form: {
        name: "Folder name",
        namePlaceholder: "Enter a folder name",
        nameLen: 30,
        nameRules: [],
      },
    });

    await waitFor(() => {
      expect(screen.getByText("Rename folder")).toBeInTheDocument();
    });
    expect(screen.queryByText("knowledge.tags")).not.toBeInTheDocument();
  });

  it("renders the tags selector with the addon suffix when nameAdd is set", async () => {
    const ref = createRef<RenameModalRef>();
    renderWithProviders(<RenameModel ref={ref} onSubmit={vi.fn()} />);

    ref.current?.onOpen({
      title: "Rename document",
      form: {
        name: "File name",
        namePlaceholder: "Enter a name",
        nameLen: 300,
        nameRules: [],
        nameAdd: ".pdf",
      },
      data: { name: "doc" },
    });

    await waitFor(() => {
      expect(screen.getByText("knowledge.tags")).toBeInTheDocument();
    });
    expect(screen.getByText(".pdf")).toBeInTheDocument();
  });

  it("calls onSubmit with the form values and closes the modal on success", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const ref = createRef<RenameModalRef>();
    renderWithProviders(<RenameModel ref={ref} onSubmit={onSubmit} />);

    ref.current?.onOpen({
      title: "Rename file",
      form: {
        name: "File name",
        namePlaceholder: "Enter a name",
        nameLen: 30,
        nameRules: [],
      },
      data: { name: "old-name" },
    });

    await waitFor(() => {
      expect(screen.getByText("Rename file")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /ok|确定/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ name: "old-name" }),
      );
    });

    // jsdom never fires real `transitionend`/`animationend` events, so antd's
    // rc-motion exit animation would otherwise wait forever for the modal
    // nodes to finish leaving; dispatch them manually so it actually unmounts.
    await waitFor(() => {
      document
        .querySelectorAll(".ant-fade-leave, .ant-zoom-leave")
        .forEach((node) => {
          fireEvent.animationEnd(node);
          fireEvent.transitionEnd(node);
        });
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });
});
