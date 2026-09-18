import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EvolutionTaskHeader } from "./EvolutionTaskHeader";
import { EvolutionModelSelect } from "./EvolutionModelSelect";
import { MemoryRouter } from "react-router-dom";
import { hasLiveTerminalStatus } from "../shared/evolutionModels";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));

it("keeps back enabled while a termination request is pending", () => {
  const onBack = vi.fn();
  render(<EvolutionTaskHeader onBack={onBack} controls={{ thread: { status: "running", status_source: "cached" }, checking: true, cancelState: "pending", canCancel: true, readOnly: true, refresh: vi.fn(), cancel: vi.fn() }} />);
  fireEvent.click(screen.getByRole("button", { name: "selfEvolutionControls.back" }));
  expect(onBack).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: "selfEvolutionControls.terminate" })).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("selfEvolutionControls.statusUnknown");
});

it("shows empty state and settings link without inventing a default candidate", () => {
  render(<MemoryRouter><EvolutionModelSelect catalog={{ models: [], can_select: false }} loading={false} error="" onChange={vi.fn()} onRetry={vi.fn()} /></MemoryRouter>);
  expect(screen.getByRole("combobox")).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("selfEvolutionControls.noModels");
  expect(screen.getByRole("link")).toHaveAttribute("href", "/settings?section=models");
});

it.each(["unverified", "passed", "failed"] as const)("keeps a configured model selectable with %s workflow status", validationStatus => {
  const model = { model_ref: "local:model", display_name: "qwen-plus", provider_name: "qwen", source: "personal" as const, validation_status: validationStatus };
  render(<MemoryRouter><EvolutionModelSelect catalog={{ models: [model], can_select: true, configured_default: model, available_default_ref: model.model_ref }} value={model.model_ref} loading={false} error="" onChange={vi.fn()} onRetry={vi.fn()} /></MemoryRouter>);
  expect(screen.getByRole("combobox")).toBeEnabled();
  expect(screen.getByText(`selfEvolutionControls.validation.${validationStatus}`)).toBeVisible();
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
});

it("explains a connection validation problem without calling it workflow failure", () => {
  render(<MemoryRouter><EvolutionModelSelect catalog={{ models: [], can_select: false, unavailable_reason: "connection_unverified" }} loading={false} error="" onChange={vi.fn()} onRetry={vi.fn()} /></MemoryRouter>);
  expect(screen.getByRole("combobox")).toBeDisabled();
  expect(screen.getByRole("status")).toHaveTextContent("selfEvolutionControls.unavailable.connection_unverified");
});

it("accepts terminal confirmation only from live observations", () => {
  for (const status of ["ended", "completed", "canceled", "cancelled", "failed"]) {
    expect(hasLiveTerminalStatus({ status, status_source: "live" })).toBe(true);
    expect(hasLiveTerminalStatus({ status, status_source: "cached" })).toBe(false);
  }
});

it("restores terminating state from the server after a fresh mount", () => {
  render(<EvolutionTaskHeader onBack={vi.fn()} controls={{ thread: { status: "running", runtime_status: "cancelling", cleanup_pending: true, status_source: "live" }, checking: false, cancelState: "idle", canCancel: true, readOnly: true, refresh: vi.fn(), cancel: vi.fn() }} />);
  expect(screen.getByRole("status")).toHaveTextContent("selfEvolutionControls.status.cancelling");
  expect(screen.getByRole("button", { name: "selfEvolutionControls.back" })).toBeEnabled();
});
