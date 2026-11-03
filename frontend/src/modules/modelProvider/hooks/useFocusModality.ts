import { useEffect, useState } from "react";
import "../modelFocus.scss";

type FocusModality = "keyboard" | "pointer";
let lastModality: FocusModality = "pointer";

/** Keep one keyboard focus cue without showing it after pointer interactions. */
export function useFocusModality() {
  const [modality, setModality] = useState(lastModality);
  useEffect(() => {
    const update = (next: FocusModality) => {
      lastModality = next;
      setModality(next);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Tab" || event.key.startsWith("Arrow")) update("keyboard");
    };
    const onPointerDown = () => update("pointer");
    document.addEventListener("keydown", onKeyDown, true);
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.removeEventListener("pointerdown", onPointerDown, true);
    };
  }, []);
  return modality;
}
