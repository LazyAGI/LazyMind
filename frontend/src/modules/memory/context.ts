import { createContext, useContext } from "react";
import { useOutletContext } from "react-router-dom";

export const MemoryManagementContext = createContext<any>(null);

export function useMemoryManagementOutletContext() {
  const embeddedContext = useContext(MemoryManagementContext);
  const outletContext = useOutletContext<any>();
  return embeddedContext || outletContext;
}
