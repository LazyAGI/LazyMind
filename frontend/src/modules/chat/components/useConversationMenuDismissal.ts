import { useEffect, type Dispatch, type SetStateAction } from "react";

export const conversationMenuClassName = (id: string) => `conversation-actions-${id}`;

// A row context menu can open without ever entering the Dropdown trigger.
// Include portaled submenus, and allow crossing the gap between row and popup.
export function useConversationMenuDismissal(
  openId: string | null,
  setOpenId: Dispatch<SetStateAction<string | null>>,
) {
  useEffect(() => {
    if (!openId) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const cancel = () => { clearTimeout(timer); timer = undefined; };
    const close = () => {
      cancel();
      setOpenId(current => current === openId ? null : current);
    };
    const schedule = () => { if (!timer) timer = setTimeout(close, 150); };
    const inside = (target: EventTarget | null) => {
      if (!(target instanceof Element)) return false;
      return target.closest("[data-conversation-actions]")?.getAttribute("data-conversation-actions") === openId
        || Boolean(target.closest(`.${CSS.escape(conversationMenuClassName(openId))}`));
    };
    const move = (event: PointerEvent) => {
      if (event.pointerType === "touch") return;
      if (inside(event.target)) cancel();
      else schedule();
    };
    const leave = (event: PointerEvent) => {
      if (event.pointerType !== "touch" && event.relatedTarget === null) schedule();
    };
    const scroll = (event: Event) => { if (!inside(event.target)) close(); };
    const keydown = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    document.addEventListener("pointermove", move, true);
    document.addEventListener("pointerout", leave, true);
    document.addEventListener("wheel", scroll, { capture: true, passive: true });
    document.addEventListener("scroll", scroll, true);
    document.addEventListener("keydown", keydown, true);
    window.addEventListener("blur", close);
    return () => {
      cancel();
      document.removeEventListener("pointermove", move, true);
      document.removeEventListener("pointerout", leave, true);
      document.removeEventListener("wheel", scroll, true);
      document.removeEventListener("scroll", scroll, true);
      document.removeEventListener("keydown", keydown, true);
      window.removeEventListener("blur", close);
    };
  }, [openId, setOpenId]);
}
