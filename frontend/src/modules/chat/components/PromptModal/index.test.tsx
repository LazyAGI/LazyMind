import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { createRef } from "react";
import PromptModal, { type PromptImperativeProps } from "./index";

const mockListPrompts = vi.fn();
const mockCreatePrompt = vi.fn();
const mockUpdatePrompt = vi.fn();
const mockDeletePrompt = vi.fn();
const mockFavoritePrompt = vi.fn();
const mockUnfavoritePrompt = vi.fn();
const mockUsePrompt = vi.fn();
const mockCreatePromptCategory = vi.fn();
const mockDeletePromptCategory = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key),
    i18n: { language: "zh-CN" },
  }),
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => `error:${code}`,
}));

vi.mock("@/modules/chat/utils/request", () => ({
  PromptServiceApi: () => ({
    listPrompts: mockListPrompts,
    createPrompt: mockCreatePrompt,
    updatePrompt: mockUpdatePrompt,
    deletePrompt: mockDeletePrompt,
    favoritePrompt: mockFavoritePrompt,
    unfavoritePrompt: mockUnfavoritePrompt,
    usePrompt: mockUsePrompt,
    createPromptCategory: mockCreatePromptCategory,
    deletePromptCategory: mockDeletePromptCategory,
  }),
}));

function samplePrompts() {
  return [
    {
      id: "p1",
      display_name: "Greeting",
      content: "Hello there",
      category: "general",
      source: "custom",
      is_favorite: false,
      usage_count: 3,
    },
    {
      id: "p2",
      display_name: "Preset One",
      content: "Preset content",
      category: "general",
      source: "preset",
      is_favorite: true,
      usage_count: 1,
    },
  ];
}

async function openModal(ref: React.RefObject<PromptImperativeProps>) {
  await act(async () => {
    ref.current?.onOpen();
  });
}

describe("PromptModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListPrompts.mockResolvedValue({
      data: {
        prompts: samplePrompts(),
        custom_categories: [],
        facets: { scopes: { all: 2 }, categories: { general: 2 }, category_total: 1 },
        total: 2,
      },
    });
    mockUsePrompt.mockResolvedValue({});
    mockFavoritePrompt.mockResolvedValue({});
    mockUnfavoritePrompt.mockResolvedValue({});
    mockCreatePrompt.mockResolvedValue({ data: { id: "p3" } });
    mockDeletePrompt.mockResolvedValue({});
  });

  it("is hidden until onOpen is called, then fetches and renders prompts", async () => {
    const ref = createRef<PromptImperativeProps>();
    render(<PromptModal ref={ref} onSelectPrompt={vi.fn()} />);
    expect(screen.queryByText("chat.promptTemplateTitle")).not.toBeInTheDocument();

    await openModal(ref);

    await waitFor(() => expect(mockListPrompts).toHaveBeenCalled());
    expect(await screen.findByText("Greeting")).toBeInTheDocument();
    expect(screen.getByText("Preset One")).toBeInTheDocument();
  });

  it("selects a prompt, calls onSelectPrompt, records usage and closes the modal", async () => {
    const onSelectPrompt = vi.fn();
    const ref = createRef<PromptImperativeProps>();
    render(<PromptModal ref={ref} onSelectPrompt={onSelectPrompt} />);
    await openModal(ref);
    await screen.findByText("Greeting");

    fireEvent.click(screen.getByRole("button", { name: /chat.usePrompt Greeting/ }));

    expect(onSelectPrompt).toHaveBeenCalledWith("Hello there");
    await waitFor(() => expect(mockUsePrompt).toHaveBeenCalledWith("p1"));

    // jsdom lacks the `AnimationEvent` constructor, so rc-motion (used by
    // antd's Modal) falls back to the vendor-prefixed `webkitAnimationEnd`
    // event instead of the plain `animationend`; dispatch it manually so the
    // exit animation resolves and the modal collapses to `display: none`.
    await waitFor(() => {
      document
        .querySelectorAll(".ant-fade-leave, .ant-zoom-leave")
        .forEach((node) => {
          node.dispatchEvent(new Event("webkitAnimationEnd", { bubbles: true }));
          fireEvent.transitionEnd(node);
        });
      expect(screen.getByText("chat.promptTemplateTitle")).not.toBeVisible();
    });
  });

  it("toggles favorite optimistically and calls the favorite API", async () => {
    const ref = createRef<PromptImperativeProps>();
    render(<PromptModal ref={ref} onSelectPrompt={vi.fn()} />);
    await openModal(ref);
    await screen.findByText("Greeting");

    fireEvent.click(screen.getByRole("button", { name: "chat.promptFavorite" }));

    await waitFor(() => expect(mockFavoritePrompt).toHaveBeenCalledWith("p1"));
  });

  it("does not show edit/delete actions for non-manageable (preset) prompts", async () => {
    const ref = createRef<PromptImperativeProps>();
    render(<PromptModal ref={ref} onSelectPrompt={vi.fn()} />);
    await openModal(ref);
    await screen.findByText("Preset One");

    expect(
      screen.queryByRole("button", { name: `common.edit Preset One` }),
    ).not.toBeInTheDocument();
  });

  it("creates a new prompt through the create form", async () => {
    const ref = createRef<PromptImperativeProps>();
    render(<PromptModal ref={ref} onSelectPrompt={vi.fn()} />);
    await openModal(ref);
    await screen.findByText("Greeting");

    fireEvent.click(screen.getByText("chat.newTemplate"));
    fireEvent.change(screen.getByPlaceholderText("chat.enterPromptTitle"), {
      target: { value: "New Prompt" },
    });
    fireEvent.change(screen.getByPlaceholderText("chat.enterPromptContent"), {
      target: { value: "New content" },
    });

    fireEvent.click(document.querySelector(".prompt-edit-modal .ant-modal-footer button:last-child") as Element);

    await waitFor(() =>
      expect(mockCreatePrompt).toHaveBeenCalledWith(
        expect.objectContaining({ display_name: "New Prompt", content: "New content" }),
      ),
    );
  });

  it("filters prompts by keyword after the debounce delay", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const ref = createRef<PromptImperativeProps>();
    render(<PromptModal ref={ref} onSelectPrompt={vi.fn()} />);
    await act(async () => {
      ref.current?.onOpen();
    });
    await waitFor(() => expect(mockListPrompts).toHaveBeenCalled());
    mockListPrompts.mockClear();

    fireEvent.change(screen.getByLabelText("chat.searchPromptPlaceholder"), {
      target: { value: "greet" },
    });

    await act(async () => {
      vi.advanceTimersByTime(300);
    });

    await waitFor(() =>
      expect(mockListPrompts).toHaveBeenCalledWith(
        expect.objectContaining({ keyword: "greet" }),
        expect.anything(),
      ),
    );
    vi.useRealTimers();
  });
});
