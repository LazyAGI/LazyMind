// Preserve native key identities and modifiers; uiohook exposes keycodes, not committed IME text.
function createEventCollector({ keys, startedAt, bounds, now = Date.now }) {
  const keyNames = new Map(Object.entries(keys).map(([name, code]) => [code, name]));
  const events = [];
  const limitations = ["desktop_keycodes_not_committed_text", "desktop_keyboard_is_global_no_dom"];
  let bytes = 0, lastMove = -Infinity;
  return {
    add(kind, raw) {
      const elapsed = now() - startedAt;
      if (elapsed < 0 || elapsed > 120000) return;
      const modifiers = ["ctrl", "alt", "shift", "meta"].filter((key) => raw[`${key}Key`]);
      const event = { seconds: elapsed / 1000, kind, source: "desktop", modifiers };
      if (kind === "keydown" || kind === "keyup") {
        const name = keyNames.get(raw.keycode) || "Unknown";
        event.key = name;
        event.keycode = raw.keycode;
      } else {
        if (!Number.isFinite(raw.x) || !Number.isFinite(raw.y)) return;
        if (bounds && (raw.x < bounds.x || raw.y < bounds.y || raw.x >= bounds.x + bounds.width || raw.y >= bounds.y + bounds.height)) return;
        if (kind === "mousemove") {
          if (elapsed - lastMove < 250) return;
          lastMove = elapsed;
        }
        event.x = raw.x; event.y = raw.y;
        event.coordinate_space = "os_screen";
        if (bounds) {
          event.normalized_x = (raw.x - bounds.x) / bounds.width;
          event.normalized_y = (raw.y - bounds.y) / bounds.height;
        }
        if (typeof raw.button === "number") event.button = raw.button;
        if (kind === "wheel") { event.amount = raw.amount; event.rotation = raw.rotation; event.direction = raw.direction; }
      }
      const size = Buffer.byteLength(JSON.stringify(event));
      if (events.length >= 1500 || bytes + size > 1500000) {
        if (!limitations.includes("evidence_size_limit")) limitations.push("evidence_size_limit");
        return;
      }
      events.push(event); bytes += size;
    },
    result: () => ({ events, limitations }),
  };
}
module.exports = { createEventCollector };
