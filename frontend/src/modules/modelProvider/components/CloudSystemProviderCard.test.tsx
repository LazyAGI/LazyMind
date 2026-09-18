import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import CloudSystemProviderCard from "./CloudSystemProviderCard";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const availableModels = [
  {
    id: "lazymind-text-default",
    name: "LazyMind Text",
    modelType: "llm",
    availability: "available" as const,
  },
  {
    id: "lazymind-vision-default",
    name: "LazyMind Vision with an intentionally long localized display name",
    modelType: "vlm",
    availability: "degraded" as const,
  },
];

describe("LazyMind Cloud system Provider card", () => {
  it("shows a read-only system Provider and its account-visible models", () => {
    render(
      <CloudSystemProviderCard
        state="ready"
        models={availableModels}
        onLogin={vi.fn()}
        onOpenPlan={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.getByText("LazyMind Cloud")).toBeInTheDocument();
    expect(screen.getByText("modelProvider.cloudSystemBadge")).toBeInTheDocument();
    expect(screen.getByText("LazyMind Text")).toBeInTheDocument();
    expect(screen.getByText(/intentionally long localized/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /add|edit|delete|api key/i })).not.toBeInTheDocument();
  });

  it("offers the correct recovery action without hiding personal Providers", () => {
    const onLogin = vi.fn();
    const onOpenPlan = vi.fn();
    const onRetry = vi.fn();
    const { rerender } = render(
      <CloudSystemProviderCard
        state="signed_out"
        models={[]}
        onLogin={onLogin}
        onOpenPlan={onOpenPlan}
        onRetry={onRetry}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.cloudSystemLogin" }));
    expect(onLogin).toHaveBeenCalledTimes(1);

    rerender(
      <CloudSystemProviderCard
        state="plan_required"
        models={[]}
        onLogin={onLogin}
        onOpenPlan={onOpenPlan}
        onRetry={onRetry}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "modelProvider.cloudSystemOpenPlan" }));
    expect(onOpenPlan).toHaveBeenCalledTimes(1);

    rerender(
      <CloudSystemProviderCard
        state="error"
        models={[]}
        onLogin={onLogin}
        onOpenPlan={vi.fn()}
        onRetry={onRetry}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "common.retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
