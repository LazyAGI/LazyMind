import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { isLocalLikeRuntimeMode, getRuntimeMode } from "@/runtime/mode";
import { getPythonComponents } from "../api/systemDependencies";
import { PYTHON_COMPONENTS_CHANGED_EVENT, usePythonComponentTask, pythonComponentTaskBusy } from "../store/pythonComponentTask";

type Availability = "checking" | "ready" | "missing" | "error";
const RAGComponentContext = createContext<{ availability: Availability; refresh: () => void }>({
  availability: "ready", refresh: () => {},
});

export function RAGComponentProvider({ children }: { children: ReactNode }) {
  const [availability, setAvailability] = useState<Availability>("checking");
  const requestId = useRef(0);
  const { pathname } = useLocation();
  const phase = usePythonComponentTask(state => state.phase);
  const refresh = useCallback(async () => {
    const id = ++requestId.current;
    if (pythonComponentTaskBusy(usePythonComponentTask.getState().phase)) {
      setAvailability("checking");
      return;
    }
    try {
      const items = await getPythonComponents();
      if (id === requestId.current) {
        setAvailability(items.some(item => item.id === "rag" && !item.active) ? "missing" : "ready");
      }
    } catch {
      if (id === requestId.current) {
        setAvailability(isLocalLikeRuntimeMode(getRuntimeMode()) ? "error" : "ready");
      }
    }
  }, []);
  useEffect(() => {
    void refresh();
    window.addEventListener("focus", refresh);
    window.addEventListener(PYTHON_COMPONENTS_CHANGED_EVENT, refresh);
    return () => {
      ++requestId.current;
      window.removeEventListener("focus", refresh);
      window.removeEventListener(PYTHON_COMPONENTS_CHANGED_EVENT, refresh);
    };
  }, [refresh, pathname, phase]);
  return <RAGComponentContext.Provider value={{ availability, refresh }}>{children}</RAGComponentContext.Provider>;
}

export function useRAGComponent() {
  return useContext(RAGComponentContext);
}
