// Browser-local diagnostics for a foreground, first-time generation. Store only durations.
export function recordGuideLatency(milestone: "outline" | "firstParagraph", started: number) {
  const duration = Math.round(performance.now() - started);
  const name = `lazymind.paper-guide.click-to-${milestone}`;
  try {
    performance.measure(name, { start: started, duration });
    const saved: unknown = JSON.parse(localStorage.getItem(name) || "{}").samples;
    const samples: number[] = Array.isArray(saved) ? saved.filter(Number.isFinite).slice(-99) : [];
    samples.push(duration);
    const sorted = [...samples].sort((a, b) => a - b);
    const percentile = (p: number) => sorted[Math.ceil(sorted.length * p) - 1];
    localStorage.setItem(name, JSON.stringify({ samples, p50: percentile(0.5), p90: percentile(0.9) }));
  } catch {
    // Timing and storage availability must not affect reading.
  }
}
