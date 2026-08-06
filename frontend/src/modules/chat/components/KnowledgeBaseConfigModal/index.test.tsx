import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { createRef } from "react";
import KnowledgeBaseConfigModal, { type ConfigImperativeProps } from "./index";

const mockAllDocumentCreators = vi.fn();
const mockAllDocumentTags = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/chat/utils/request", () => ({
  DocumentServiceApi: () => ({
    documentServiceAllDocumentCreators: mockAllDocumentCreators,
    documentServiceAllDocumentTags: mockAllDocumentTags,
  }),
}));

describe("KnowledgeBaseConfigModal", () => {
  beforeEach(() => {
    mockAllDocumentCreators.mockReset().mockResolvedValue({
      data: { creators: [{ id: "u1", name: "Alice" }] },
    });
    mockAllDocumentTags.mockReset().mockResolvedValue({
      data: { tags: ["important"] },
    });
  });

  it("fetches creators and tags on mount", async () => {
    render(<KnowledgeBaseConfigModal onChange={vi.fn()} />);
    await waitFor(() => expect(mockAllDocumentCreators).toHaveBeenCalled());
    await waitFor(() => expect(mockAllDocumentTags).toHaveBeenCalled());
  });

  it("opens the modal with prefilled fields via the imperative handle", async () => {
    const ref = createRef<ConfigImperativeProps>();
    render(<KnowledgeBaseConfigModal ref={ref} onChange={vi.fn()} />);
    await waitFor(() => expect(mockAllDocumentCreators).toHaveBeenCalled());

    act(() => {
      ref.current?.onOpen({ creators: ["u1"], tags: ["important"] });
    });

    expect(await screen.findByText("chat.knowledgeAdvancedConfig")).toBeInTheDocument();
  });

  it("calls onChange with the form values when OK is clicked", async () => {
    const onChange = vi.fn();
    const ref = createRef<ConfigImperativeProps>();
    render(<KnowledgeBaseConfigModal ref={ref} onChange={onChange} />);
    await waitFor(() => expect(mockAllDocumentCreators).toHaveBeenCalled());

    act(() => {
      ref.current?.onOpen({});
    });
    await screen.findByText("chat.knowledgeAdvancedConfig");

    fireEvent.click(document.querySelector(".ant-modal-footer button:last-child") as Element);

    await waitFor(() => expect(onChange).toHaveBeenCalled());
  });
});
