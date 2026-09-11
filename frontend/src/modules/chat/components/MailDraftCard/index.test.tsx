import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import MailDraftCard from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

vi.mock("@/modules/chat/store/taskCenter", () => ({
  useTaskCenterStore: (selector: (state: any) => unknown) =>
    selector({
      activeConversationId: "",
      artifactsByConversation: {},
    }),
}));

describe("MailDraftCard", () => {
  it("lets the user add and remove attachments before confirm", async () => {
    const onConfirm = vi.fn();
    const { container } = render(
      <MemoryRouter>
        <MailDraftCard
          draft={{
            draft_id: "draft_1",
            revision: 1,
            to: ["a@b.com"],
            subject: "hi",
            body: "body",
            attachments: ["keep.txt"],
            status: "draft",
          }}
          onConfirm={onConfirm}
        />
      </MemoryRouter>,
    );

    expect(screen.getByText("keep.txt")).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "chat.mailDraft.removeAttachment" })[0]);
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    const file = new File(["hello"], "new.txt", { type: "text/plain" });
    fireEvent.change(input, { target: { files: [file] } });
    expect(await screen.findByText("new.txt")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await vi.waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm.mock.calls[0][2].attachments).toEqual([
      expect.objectContaining({ filename: "new.txt", content_base64: expect.any(String) }),
    ]);
    expect(onConfirm.mock.calls[0][2].attachment_paths).toEqual([]);
  });

  it("can attach a conversation upload without using the chat file picker", async () => {
    const onConfirm = vi.fn();
    render(
      <MemoryRouter>
        <MailDraftCard
          draft={{
            draft_id: "draft_3",
            revision: 1,
            to: ["a@b.com"],
            subject: "hi",
            body: "body",
            attachments: [],
            status: "draft",
          }}
          conversationFiles={[{ name: "report.pdf" }]}
          onConfirm={onConfirm}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /report.pdf/ }));
    fireEvent.click(screen.getByRole("button", { name: "chat.mailDraft.confirmSend" }));
    await vi.waitFor(() => expect(onConfirm).toHaveBeenCalled());
    expect(onConfirm.mock.calls[0][2].attachment_paths).toEqual(["report.pdf"]);
    expect(onConfirm.mock.calls[0][2].attachments).toEqual([]);
  });
});
