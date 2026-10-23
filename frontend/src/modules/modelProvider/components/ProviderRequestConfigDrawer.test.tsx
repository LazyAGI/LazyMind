import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ConfigProvider } from "antd";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsNavigationGuard } from "@/modules/settings/SettingsNavigationGuard";
import { getProviderRequestConfig, updateProviderRequestConfig } from "../api";
import ProviderRequestConfigDrawer from "./ProviderRequestConfigDrawer";

vi.mock("../api", () => ({
  getProviderRequestConfig: vi.fn(),
  updateProviderRequestConfig: vi.fn(),
}));
vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: Error) => error.message,
}));
vi.mock("react-i18next", () => {
  const copy: Record<string, string> = {
    title: "Request configuration",
    default: "Default",
    setDefault: "Set as default",
    endpointHint: "Leave blank to use the default endpoint.",
    defaultAddress: "Default endpoint: {{address}}",
    unsupported: "This protocol is not supported.",
    save: "Save",
    cancel: "Cancel",
    saved: "Saved",
    loadFailed: "Could not load request configuration.",
    retry: "Retry",
    baseUrlInvalid: "Enter a valid HTTP or HTTPS endpoint.",
    baseUrlNoCredentials:
      "The endpoint must not include a username or password.",
    baseUrlMax: "Endpoint is too long.",
  };
  const t = (key: string, options?: Record<string, unknown>) => {
    const value = key.startsWith("modelProvider.requestConfig.")
      ? copy[key.split(".").pop()!]
      : key;
    return (value || key).replace(
      "{{address}}",
      String(options?.address || ""),
    );
  };
  return { useTranslation: () => ({ t, i18n: { language: "en-US" } }) };
});

const fetchConfig = vi.mocked(getProviderRequestConfig);
const saveConfig = vi.mocked(updateProviderRequestConfig);
const config = () => ({
  default_protocol: "openai" as const,
  protocols: [
    {
      protocol: "openai" as const,
      name: "OpenAI",
      base_url: "",
      default_base_url: "https://api.openai.com/v1",
      supported: true,
    },
    {
      protocol: "anthropic" as const,
      name: "Anthropic",
      base_url: "https://proxy.test/anthropic",
      default_base_url: "https://api.anthropic.com",
      supported: true,
    },
    {
      protocol: "gemini" as const,
      name: "Gemini",
      base_url: "",
      default_base_url: "",
      supported: false,
    },
    {
      protocol: "openai-responses" as const,
      name: "OpenAI Responses",
      base_url: "",
      default_base_url: "",
      supported: false,
    },
  ],
});
const harness = ({ children }: { children: ReactNode }) => (
  <ConfigProvider theme={{ token: { motion: false } }}>
    <MemoryRouter>
      <SettingsNavigationGuard>{children}</SettingsNavigationGuard>
    </MemoryRouter>
  </ConfigProvider>
);
const props = () => ({
  providerId: "provider-a",
  providerName: "Gateway",
  open: true,
  onClose: vi.fn(),
  onSaved: vi.fn(async () => undefined),
});

describe("provider request configuration drawer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchConfig.mockResolvedValue(config());
    saveConfig.mockResolvedValue(config());
  });

  it("loads protocol overrides and makes unsupported protocols read-only", async () => {
    render(<ProviderRequestConfigDrawer {...props()} />, { wrapper: harness });
    const openai = await screen.findByRole("textbox", { name: "OpenAI" });
    expect(fetchConfig).toHaveBeenCalledWith("provider-a");
    expect(openai).toHaveValue("");
    expect(openai).toHaveAttribute("placeholder", "https://api.openai.com/v1");
    expect(screen.getByRole("textbox", { name: "Anthropic" })).toHaveValue(
      "https://proxy.test/anthropic",
    );
    expect(screen.getByRole("textbox", { name: "Gemini" })).toBeDisabled();
    expect(
      screen.getByRole("textbox", { name: "OpenAI Responses" }),
    ).toBeDisabled();
    expect(screen.getAllByText("This protocol is not supported.")).toHaveLength(
      2,
    );
    expect(
      within(screen.getByRole("region", { name: "OpenAI" })).getByText(
        "Default",
      ),
    ).toBeVisible();
  });

  it("saves the chosen default protocol and trimmed endpoint overrides", async () => {
    const callbacks = props();
    render(<ProviderRequestConfigDrawer {...callbacks} />, {
      wrapper: harness,
    });
    fireEvent.change(await screen.findByRole("textbox", { name: "OpenAI" }), {
      target: { value: " https://gateway.test/v1 " },
    });
    fireEvent.click(
      within(screen.getByRole("region", { name: "Anthropic" })).getByRole(
        "button",
        { name: "Set as default" },
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(saveConfig).toHaveBeenCalledWith("provider-a", {
        default_protocol: "anthropic",
        endpoints: {
          openai: "https://gateway.test/v1",
          anthropic: "https://proxy.test/anthropic",
        },
      }),
    );
    await waitFor(() => expect(callbacks.onSaved).toHaveBeenCalledOnce());
    expect(callbacks.onClose).toHaveBeenCalledOnce();
  });

  it("keeps a blank override to reset an address to the catalog default", async () => {
    render(<ProviderRequestConfigDrawer {...props()} />, { wrapper: harness });
    fireEvent.change(
      await screen.findByRole("textbox", { name: "Anthropic" }),
      { target: { value: "" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(saveConfig).toHaveBeenCalledWith("provider-a", {
        default_protocol: "openai",
        endpoints: { openai: "", anthropic: "" },
      }),
    );
  });

  it("rejects credentials in an API endpoint", async () => {
    render(<ProviderRequestConfigDrawer {...props()} />, { wrapper: harness });
    fireEvent.change(await screen.findByRole("textbox", { name: "OpenAI" }), {
      target: { value: "https://user:secret@gateway.test/v1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText(
        "The endpoint must not include a username or password.",
      ),
    ).toBeVisible();
    expect(saveConfig).not.toHaveBeenCalled();
  });

  it.each([
    "file:///tmp/endpoint",
    "https://gateway.test/v1?key=test",
    "https://gateway.test/v1#messages",
  ])("rejects unsupported API roots: %s", async (endpoint) => {
    render(<ProviderRequestConfigDrawer {...props()} />, { wrapper: harness });
    fireEvent.change(await screen.findByRole("textbox", { name: "OpenAI" }), {
      target: { value: endpoint },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("Enter a valid HTTP or HTTPS endpoint."),
    ).toBeVisible();
    expect(saveConfig).not.toHaveBeenCalled();
  });

  it("retries a failed configuration fetch", async () => {
    fetchConfig.mockRejectedValueOnce(new Error("offline"));
    render(<ProviderRequestConfigDrawer {...props()} />, { wrapper: harness });
    expect(
      await screen.findByText("Could not load request configuration."),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("textbox", { name: "OpenAI" }),
    ).toBeVisible();
    expect(fetchConfig).toHaveBeenCalledTimes(2);
  });

  it("ignores an older supplier request after switching suppliers", async () => {
    let resolveFirst!: (value: ReturnType<typeof config>) => void;
    fetchConfig.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFirst = resolve;
      }),
    );
    const second = config();
    second.protocols[0].base_url = "https://new-supplier.test/v1";
    fetchConfig.mockResolvedValueOnce(second);
    const callbacks = props();
    const view = render(<ProviderRequestConfigDrawer {...callbacks} />, {
      wrapper: harness,
    });
    view.rerender(
      <ProviderRequestConfigDrawer {...callbacks} providerId="provider-b" />,
    );
    expect(await screen.findByRole("textbox", { name: "OpenAI" })).toHaveValue(
      "https://new-supplier.test/v1",
    );
    await act(async () => resolveFirst(config()));
    expect(screen.getByRole("textbox", { name: "OpenAI" })).toHaveValue(
      "https://new-supplier.test/v1",
    );
  });

  it("confirms discarding endpoint edits before closing", async () => {
    const callbacks = props();
    render(<ProviderRequestConfigDrawer {...callbacks} />, {
      wrapper: harness,
    });
    fireEvent.change(await screen.findByRole("textbox", { name: "OpenAI" }), {
      target: { value: "https://draft.test/v1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    const discard = await screen.findByRole("button", {
      name: "settingsPage.unsaved.discard",
    });
    await waitFor(() => expect(discard).toBeVisible());
    expect(callbacks.onClose).not.toHaveBeenCalled();
    fireEvent.click(discard);
    expect(callbacks.onClose).toHaveBeenCalledOnce();
  });
});
