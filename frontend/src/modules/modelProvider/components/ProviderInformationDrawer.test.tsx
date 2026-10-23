import { useState } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ProviderInformationDrawer } from "./ProviderInformationDrawer";
import type { AddedProvider } from "../pages/ModelProvidersPage";

const locale = vi.hoisted(() => ({ language: "en-US" }));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ i18n: { resolvedLanguage: locale.language } }),
}));

const standard: AddedProvider = {
  id: "qwen", name: "Qwen", brandKey: "Qwen", serviceKey: "standard",
  source: "Qwen", brand: "Qwen", headline: "", baseUrl: "https://saved.example.test/v1",
  capabilities: ["LLM_CHAT"], models: [], groups: [],
};
const coding: AddedProvider = {
  ...standard, id: "qwen-coding", name: "QwenCodingPlan", serviceKey: "coding",
  baseUrl: "https://coding.example.test/v1",
};
const token: AddedProvider = {
  ...standard, id: "qwen-token", name: "QwenTokenPlan", serviceKey: "token",
  baseUrl: "https://token.example.test/v1",
};

function fixture() {
  return {
    open: true, provider: standard, providers: [token, coding, standard],
    onClose: vi.fn(), onConfigure: vi.fn(), onAfterClose: vi.fn(),
  };
}

function draw(props: ReturnType<typeof fixture>) {
  const renderDrawer = (next: ReturnType<typeof fixture>) => <ConfigProvider theme={{ token: { motion: false } }}>
    <ProviderInformationDrawer {...next} />
  </ConfigProvider>;
  const view = render(renderDrawer(props));
  return { ...view, rerenderDrawer: (next: ReturnType<typeof fixture>) => view.rerender(renderDrawer(next)) };
}

beforeEach(() => {
  locale.language = "en-US";
  vi.clearAllMocks();
});

describe("provider information drawer", () => {
  it("opens with the provider introduction, official website, and safe named sources before connection help", async () => {
    const props = fixture();
    // The configured provider's last saved baseline takes precedence over a stale list item.
    draw({ ...props, providers: [coding, { ...standard, baseUrl: "https://stale.example.test/v1" }] });
    const dialog = within(await screen.findByRole("dialog"));
    expect(dialog.getByRole("heading", { name: "Alibaba Cloud Model Studio · Standard API" })).toBeInTheDocument();
    expect(dialog.getByText("Alibaba Cloud Model Studio is a platform for building AI applications with Qwen and other models. It brings model inference, evaluation, fine-tuning, and application development into one cloud service.", { selector: "p" })).toBeVisible();
    const website = dialog.getByRole("link", { name: /Official website/ });
    expect(website).toBeVisible();
    expect(website).toHaveAttribute("target", "_blank");
    expect(website).toHaveAttribute("rel", "noopener noreferrer");
    expect(website).toHaveAttribute("href", "https://www.aliyun.com/product/bailian");
    expect(website).toHaveTextContent("https://www.aliyun.com/product/bailian");
    expect(website).not.toHaveAttribute("href", standard.baseUrl);
    expect(within(dialog.getByRole("region", { name: "Product highlights" })).getAllByRole("listitem")).toHaveLength(3);
    expect(dialog.getByRole("region", { name: "Official sources" })).toBeVisible();
    const help = dialog.getByRole("button", { name: "Connection help" });
    expect(help).toHaveAttribute("aria-expanded", "false");
    expect(dialog.queryByRole("region", { name: "Get started" })).not.toBeInTheDocument();
    expect(dialog.queryByRole("region", { name: "Saved API address" })).not.toBeInTheDocument();
    fireEvent.click(help);
    expect(within(dialog.getByRole("region", { name: "Get started" })).getAllByRole("listitem")).toHaveLength(3);
    const address = dialog.getByRole("region", { name: "Saved API address" });
    expect(address).toHaveTextContent("https://saved.example.test/v1");
    expect(address).not.toHaveTextContent("https://stale.example.test/v1");
    expect(dialog.queryByRole("textbox")).not.toBeInTheDocument();
    const sources = within(dialog.getByRole("region", { name: "Official sources" }));
    const links = sources.getAllByRole("link");
    expect(links.length).toBeGreaterThan(0);
    for (const link of links) {
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
      expect(link.getAttribute("href")).toMatch(/^https:\/\//);
      expect(link.textContent?.trim()).not.toBe("");
    }
    expect(sources.getByText(/2026-10-23/)).toBeInTheDocument();
    expect(props.onConfigure).not.toHaveBeenCalled();
  });

  it("browses siblings in immutable service order without switching configuration", async () => {
    const props = fixture();
    draw(props);
    const comparison = within(await screen.findByRole("region", { name: "Services from this provider" }));
    expect(comparison.queryByRole("columnheader", { name: "Credentials" })).not.toBeInTheDocument();
    expect(comparison.getByRole("columnheader", { name: "Introduction" })).toBeInTheDocument();
    expect(comparison.getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual([
      "Read Alibaba Cloud Model Studio · Standard API",
      "Read Alibaba Cloud Model Studio · Coding Plan",
      "Read Alibaba Cloud Model Studio · Token Plan",
    ]);
    fireEvent.click(comparison.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" }));
    expect(await screen.findByRole("dialog", { name: "Alibaba Cloud Model Studio · Coding Plan · Provider information" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" })).toHaveAttribute("aria-current", "page");
    fireEvent.click(screen.getByRole("button", { name: "Connection help" }));
    expect(screen.getByRole("region", { name: "Saved API address" })).toHaveTextContent("https://coding.example.test/v1");
    expect(props.onConfigure).not.toHaveBeenCalled();
    expect(props.onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Configure this service" }));
    expect(props.onConfigure).toHaveBeenCalledOnce();
    expect(props.onConfigure).toHaveBeenCalledWith("qwen-coding");
    expect(props.onClose).not.toHaveBeenCalled();
  });

  it("closes connection help when reading another service and when reopening the drawer", async () => {
    const props = fixture();
    const view = draw(props);
    fireEvent.click(screen.getByRole("button", { name: "Connection help" }));
    expect(screen.getByRole("region", { name: "Saved API address" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" }));
    expect(screen.getByRole("button", { name: "Connection help" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("region", { name: "Saved API address" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Connection help" }));
    view.rerenderDrawer({ ...props, open: false });
    await waitFor(() => expect(props.onAfterClose).toHaveBeenCalledOnce());
    view.rerenderDrawer(props);
    expect(await screen.findByRole("button", { name: "Connection help" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("region", { name: "Saved API address" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Connection help" }));
    view.rerenderDrawer({ ...props, provider: token });
    expect(screen.getByRole("button", { name: "Connection help" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("region", { name: "Saved API address" })).not.toBeInTheDocument();
  });

  it("returns to configuration without invoking a service switch", async () => {
    const props = fixture();
    draw(props);
    expect(screen.queryByRole("button", { name: "Configure this service" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Return to configuration" }));
    expect(props.onClose).toHaveBeenCalledOnce();
    expect(props.onConfigure).not.toHaveBeenCalled();
  });

  it("starts an explicitly browsed guide at the top and focuses its heading without disturbing incidental refreshes", async () => {
    const props = fixture();
    const view = draw(props);
    const body = document.querySelector<HTMLElement>(".provider-information-drawer .ant-drawer-body")!;
    body.scrollTop = 650;
    fireEvent.click(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" }));
    expect(body.scrollTop).toBe(0);
    expect(screen.getByRole("heading", { name: "Alibaba Cloud Model Studio · Coding Plan" })).toHaveFocus();
    body.scrollTop = 180;
    const close = screen.getByRole("button", { name: "Return to configuration" });
    close.focus();
    view.rerenderDrawer({ ...props, provider: { ...standard }, providers: [...props.providers] });
    expect(body.scrollTop).toBe(180);
    expect(close).toHaveFocus();
    expect(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" })).toHaveAttribute("aria-current", "page");
  });

  it("resets the reading target on reopening and on a changed configured provider", async () => {
    const props = fixture();
    const view = draw(props);
    fireEvent.click(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" }));
    view.rerenderDrawer({ ...props, open: false });
    await waitFor(() => expect(props.onAfterClose).toHaveBeenCalledOnce());
    view.rerenderDrawer(props);
    expect(await screen.findByRole("dialog", { name: "Alibaba Cloud Model Studio · Standard API · Provider information" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" }));
    view.rerenderDrawer({ ...props, provider: token });
    expect(await screen.findByRole("dialog", { name: "Alibaba Cloud Model Studio · Token Plan · Provider information" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Configure this service" })).not.toBeInTheDocument();
    expect(props.onConfigure).not.toHaveBeenCalled();
  });

  it("falls back to the configured provider when the service being read is removed", async () => {
    const props = fixture();
    const view = draw(props);
    fireEvent.click(screen.getByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" }));
    view.rerenderDrawer({ ...props, providers: [standard, token] });
    expect(await screen.findByRole("dialog", { name: "Alibaba Cloud Model Studio · Standard API · Provider information" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Configure this service" })).not.toBeInTheDocument();
    view.rerenderDrawer({ ...props, providers: [] });
    expect(screen.getByRole("heading", { name: "Alibaba Cloud Model Studio · Standard API" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Services from this provider" })).not.toBeInTheDocument();
  });

  it("keeps a same-name custom supplier's branding and generic content isolated", async () => {
    const logo = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==";
    const custom = { ...standard, id: "custom", name: "Qwen", isCustom: true, protocolName: "Qwen", logoDataUrl: logo, baseUrl: "" };
    draw({ ...fixture(), provider: custom, providers: [coding, custom, standard] });
    const dialog = within(await screen.findByRole("dialog", { name: "Qwen · Provider information" }));
    expect(dialog.getByRole("heading", { name: "Qwen" })).toBeInTheDocument();
    expect(document.querySelector(".provider-information-logo img")).toHaveAttribute("src", logo);
    expect(dialog.queryByRole("region", { name: "Services from this provider" })).not.toBeInTheDocument();
    expect(dialog.queryByRole("link")).not.toBeInTheDocument();
    expect(dialog.queryByRole("region", { name: "Product highlights" })).not.toBeInTheDocument();
    fireEvent.click(dialog.getByRole("button", { name: "Connection help" }));
    expect(dialog.getByRole("region", { name: "Saved API address" })).toHaveTextContent("No API address saved");
  });

  it("does not include custom collisions in built-in service comparisons", async () => {
    draw({ ...fixture(), providers: [standard, { ...coding, id: "custom", isCustom: true }, token] });
    const comparison = within(await screen.findByRole("region", { name: "Services from this provider" }));
    expect(comparison.queryByRole("button", { name: "Read Alibaba Cloud Model Studio · Coding Plan" })).not.toBeInTheDocument();
    expect(comparison.getAllByRole("button")).toHaveLength(2);
  });

  it("keeps unknown suppliers generic and presents self-hosted project information", async () => {
    const unknown = { ...standard, id: "unknown", name: "Private Gateway", brandKey: undefined, serviceKey: undefined, protocolName: "Qwen" };
    const props = { ...fixture(), provider: unknown, providers: [unknown, coding] };
    const view = draw(props);
    const dialog = within(await screen.findByRole("dialog", { name: "Private Gateway · Provider information" }));
    expect(dialog.queryByRole("link")).not.toBeInTheDocument();
    expect(dialog.queryByRole("region", { name: "Product highlights" })).not.toBeInTheDocument();
    expect(dialog.queryByRole("region", { name: "Services from this provider" })).not.toBeInTheDocument();
    const local = { ...unknown, id: "local", name: "vLLM", source: "vLLM", protocolName: "OpenAI" };
    view.rerenderDrawer({ ...props, provider: local, providers: [local] });
    expect(await screen.findByRole("heading", { name: "vLLM" })).toBeInTheDocument();
    expect(screen.getByText(/vLLM is an open-source inference and serving engine/, { selector: "p" })).toBeVisible();
    expect(screen.getByRole("link", { name: /Official website/ })).toHaveAttribute("href", "https://vllm.ai/");
    expect(screen.getByRole("region", { name: "Official sources" })).toBeVisible();
    expect(screen.queryByRole("region", { name: "Saved API address" })).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Services from this provider" })).not.toBeInTheDocument();
  });

  it("localizes the reading page and safe source labels in Chinese", async () => {
    locale.language = "zh-CN";
    draw(fixture());
    expect(await screen.findByRole("dialog", { name: "阿里云百炼 · 标准API · 供应商简介" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /官方网站/ })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "接入帮助" }));
    expect(screen.getByRole("region", { name: "已保存的API地址" })).toHaveTextContent("https://saved.example.test/v1");
    expect(screen.getByRole("button", { name: "查看阿里云百炼 · Coding Plan说明" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "返回配置" })).toBeInTheDocument();
  });

  it("exposes keyboard focus cues and calls the close-completion callback for focus restoration", async () => {
    const configured = vi.fn();
    function Harness() {
      const [open, setOpen] = useState(false);
      return <ConfigProvider theme={{ token: { motion: false } }}>
        <button id="information-trigger" onClick={() => setOpen(true)}>Open information</button>
        <ProviderInformationDrawer open={open} provider={standard} providers={[standard]} onClose={() => setOpen(false)} onConfigure={configured}
          onAfterClose={() => document.getElementById("information-trigger")?.focus()} />
      </ConfigProvider>;
    }
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Open information" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.keyDown(document, { key: "Tab" });
    expect(dialog.closest(".model-provider-focus-keyboard")).not.toBeNull();
    fireEvent.keyDown(dialog, { key: "Escape", keyCode: 27, which: 27 });
    await waitFor(() => expect(screen.getByRole("button", { name: "Open information" })).toHaveFocus());
    expect(configured).not.toHaveBeenCalled();
  });
});
