import { act, render, screen } from "@testing-library/react";
import { message } from "antd";
import { createRef } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import ChatSelector, { type ChatSelectorImperativeProps } from ".";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "system-admin" }) },
}));
vi.mock("@/modules/chat/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: vi.fn(() => new Promise(() => undefined)),
  }),
}));

describe("ChatSelector", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("reports why the selector cannot open instead of failing silently", () => {
    const ref = createRef<ChatSelectorImperativeProps>();
    const warning = vi.spyOn(message, "warning").mockImplementation(() => undefined as never);

    render(
      <MemoryRouter>
        <ChatSelector
          ref={ref}
          chatConfig={{}}
          disabled
          disabledReason="Knowledge unavailable"
        />
      </MemoryRouter>,
    );

    act(() => ref.current?.open(document.body));

    expect(warning).toHaveBeenCalledWith("Knowledge unavailable");
    expect(screen.queryByPlaceholderText("chat.searchKnowledge")).not.toBeInTheDocument();
  });
});

describe("ChatSelector model requirements", () => {
  it.each([
    { embeddingReady: false, multimodalEmbeddingReady: true },
    { embeddingReady: true, multimodalEmbeddingReady: false },
  ])("still blocks selection when required embedding is unavailable: %j", (readiness) => {
    const { container } = render(<MemoryRouter><ChatSelector chatConfig={{}} {...readiness} /></MemoryRouter>);
    expect(container.querySelector('[aria-disabled="true"]')).not.toBeNull();
    expect(screen.queryByPlaceholderText("chat.searchKnowledge")).not.toBeInTheDocument();
  });
});
