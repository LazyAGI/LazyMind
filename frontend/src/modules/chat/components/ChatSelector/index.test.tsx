import { describe, expect, it, vi, beforeEach } from "vitest";
import { createRef } from "react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ChatSelector, { type ChatSelectorImperativeProps } from "./index";
import type { ChatConfig } from "../ChatConfigs";

const mockListDatasets = vi.fn();
const mockSetDefault = vi.fn();
const mockUnsetDefault = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => vi.fn(),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => ({ role: "member" }) },
}));

vi.mock("@/modules/chat/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: mockListDatasets,
    datasetServiceSetDefaultDataset: mockSetDefault,
    datasetServiceUnsetDefaultDataset: mockUnsetDefault,
  }),
}));

const baseChatConfig = {} as ChatConfig;

function openPopover() {
  fireEvent.click(screen.getByText("chat.knowledgeBase"));
}

describe("ChatSelector", () => {
  beforeEach(() => {
    mockListDatasets.mockReset();
    mockSetDefault.mockReset();
    mockUnsetDefault.mockReset();
    mockListDatasets.mockResolvedValue({
      data: {
        datasets: [
          { dataset_id: "ds-1", display_name: "Dataset One", default_dataset: false },
          { dataset_id: "ds-2", display_name: "Dataset Two", default_dataset: true },
        ],
      },
    });
  });

  it("fetches and lists knowledge bases when the popover is opened", async () => {
    render(<ChatSelector chatConfig={baseChatConfig} />);
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalledWith({ pageSize: 1000 }));

    openPopover();

    expect(await screen.findByText("Dataset One")).toBeInTheDocument();
    expect(screen.getByText("Dataset Two")).toBeInTheDocument();
  });

  it("auto-selects the default dataset and notifies onChange", async () => {
    const onChange = vi.fn();
    render(<ChatSelector chatConfig={baseChatConfig} onChange={onChange} />);

    await waitFor(() =>
      expect(onChange).toHaveBeenCalledWith(["ds-2"], [], []),
    );
  });

  it("toggles a non-pinned item's selection when clicked", async () => {
    const onChange = vi.fn();
    render(<ChatSelector chatConfig={baseChatConfig} onChange={onChange} />);
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalled());
    openPopover();

    const item = await screen.findByText("Dataset One");
    fireEvent.click(item);

    await waitFor(() =>
      expect(onChange).toHaveBeenCalledWith(
        expect.arrayContaining(["ds-1", "ds-2"]),
        [],
        [],
      ),
    );
  });

  it("disables opening the popover through the imperative handle when embedding is not ready", () => {
    const ref = createRef<ChatSelectorImperativeProps>();
    render(
      <ChatSelector chatConfig={baseChatConfig} ref={ref} embeddingReady={false} />,
    );

    ref.current?.open(document.body);

    expect(screen.queryByPlaceholderText("chat.searchKnowledge")).not.toBeInTheDocument();
  });

  it("opens the popover through the imperative handle when embedding is ready", async () => {
    const ref = createRef<ChatSelectorImperativeProps>();
    render(<ChatSelector chatConfig={baseChatConfig} ref={ref} />);
    await waitFor(() => expect(mockListDatasets).toHaveBeenCalled());

    ref.current?.open(document.body);

    expect(await screen.findByPlaceholderText("chat.searchKnowledge")).toBeInTheDocument();
  });
});
