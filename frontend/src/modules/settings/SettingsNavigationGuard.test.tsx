import { useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider, Modal } from "antd";
import { createMemoryRouter, Link, RouterProvider } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { SettingsNavigationGuard, useSettingsDraft } from "./SettingsNavigationGuard";

vi.mock("react-i18next", async (original) => ({
  ...await original<typeof import("react-i18next")>(),
  useTranslation: () => ({ t: (key: string) => key }),
}));

function Editor({ save }: { save: () => Promise<boolean> }) {
  const [text, setText] = useState("");
  useSettingsDraft({ dirty: text !== "", save, discard: () => setText("") });
  return <>
    <input aria-label="draft" value={text} onChange={(event) => setText(event.target.value)} />
    <Link to="/settings?section=knowledge&tool=web-search">Search settings</Link>
    <Link to="/chat">Chat</Link>
  </>;
}

function ModalEditor() {
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const confirmClose = useSettingsDraft({ dirty: text !== "", discard: () => setText("") });
  return <>
    <input aria-label="draft" value={text} onChange={(event) => setText(event.target.value)} />
    <Link to="/chat">Chat</Link>
    <button onClick={() => setOpen(true)}>Open editor</button>
    <Modal open={open} title="Model configuration" footer={null} onCancel={() => confirmClose(() => setOpen(false))}>
      <input aria-label="modal draft" value={text} onChange={(event) => setText(event.target.value)} />
    </Modal>
  </>;
}

function setup(save = vi.fn().mockResolvedValue(true), withModal = false) {
  const router = createMemoryRouter([
    { path: "/settings", element: <SettingsNavigationGuard>{withModal ? <ModalEditor /> : <Editor save={save} />}</SettingsNavigationGuard> },
    { path: "/chat", element: <p>Chat page</p> },
  ], { initialEntries: ["/chat", "/settings?section=models&view=providers"], initialIndex: 1 });
  render(<ConfigProvider theme={{ token: { motion: false } }}><RouterProvider router={router} /></ConfigProvider>);
  return router;
}

describe("settings unsaved navigation", () => {
  it("allows clean navigation and restores the complete settings URL on back", async () => {
    const router = setup();
    fireEvent.click(screen.getByText("Search settings"));
    await waitFor(() => expect(router.state.location.search).toBe("?section=knowledge&tool=web-search"));
    await act(async () => { await router.navigate(-1); });
    expect(router.state.location.search).toBe("?section=models&view=providers");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("blocks a query-only section change and keeps the draft when canceled", async () => {
    const router = setup();
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    fireEvent.click(screen.getByText("Search settings"));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.stay"));
    expect(router.state.location.search).toBe("?section=models&view=providers");
    expect(screen.getByLabelText("draft")).toHaveValue("unsaved");
  });

  it("blocks browser back and proceeds only after explicit discard", async () => {
    const router = setup();
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    await act(async () => { await router.navigate(-1); });
    expect(router.state.location.pathname).toBe("/settings");
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    expect(await screen.findByText("Chat page")).toBeInTheDocument();
  });

  it("keeps the form on save failure and retries before leaving", async () => {
    const save = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    const router = setup(save);
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    fireEvent.click(screen.getByText("Chat"));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.save"));
    expect(await screen.findByRole("alert")).toHaveTextContent("settingsPage.unsaved.saveFailed");
    expect(router.state.location.pathname).toBe("/settings");
    expect(screen.getByLabelText("draft")).toHaveValue("unsaved");
    fireEvent.click(screen.getByText("settingsPage.unsaved.save"));
    expect(await screen.findByText("Chat page")).toBeInTheDocument();
    expect(save).toHaveBeenCalledTimes(2);
  });

  it("warns on document reload only while there are unsaved changes", () => {
    setup();
    const clean = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(clean);
    expect(clean.defaultPrevented).toBe(false);
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    const dirty = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirty);
    expect(dirty.defaultPrevented).toBe(true);
  });

  it("keeps a reused leave confirmation above a later-mounted editor and preserves close choices", async () => {
    const router = setup(undefined, true);
    fireEvent.change(screen.getByLabelText("draft"), { target: { value: "unsaved" } });
    fireEvent.click(screen.getByText("Chat"));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.stay"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    // The confirmation portal now precedes the editor portal in the document.
    fireEvent.click(screen.getByText("Open editor"));
    const editor = (await screen.findByText("Model configuration")).closest<HTMLElement>('[role="dialog"]')!;
    fireEvent.click(within(editor).getByRole("button", { name: "Close" }));
    const confirmation = (await screen.findByText("settingsPage.unsaved.title")).closest<HTMLElement>('[role="dialog"]')!;
    const editorWrap = editor.closest(".ant-modal-wrap")!;
    const confirmationWrap = confirmation.closest(".ant-modal-wrap")!;
    const confirmationMask = confirmationWrap.parentElement!.querySelector(".ant-modal-mask")!;
    const editorZIndex = Number(getComputedStyle(editorWrap).zIndex);
    expect(Number(getComputedStyle(confirmationWrap).zIndex)).toBeGreaterThan(editorZIndex);
    expect(Number(getComputedStyle(confirmationMask).zIndex)).toBeGreaterThan(editorZIndex);

    fireEvent.keyDown(confirmationWrap, { key: "Escape", keyCode: 27 });
    await waitFor(() => expect(confirmation).not.toBeVisible());
    expect(within(editor).getByLabelText("modal draft")).toHaveValue("unsaved");
    expect(router.state.location.pathname).toBe("/settings");

    fireEvent.click(within(editor).getByRole("button", { name: "Close" }));
    fireEvent.click(await screen.findByText("settingsPage.unsaved.discard"));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByLabelText("draft")).toHaveValue("");
    expect(router.state.location.pathname).toBe("/settings");
  });
});
