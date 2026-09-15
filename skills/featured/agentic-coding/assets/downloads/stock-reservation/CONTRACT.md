# PACT contract — stock reservation without input mutation

Objective: preserve return values and greedy request-order allocation while removing stock mutations.
Acceptance: same signature and tuple-of-dicts result; repeated SKU, shortage, unknown SKU, empty requests, zero quantity and exact depletion; original stock/requests unchanged. Keep exception behavior for missing request keys.
Intentional behavior change: caller stock no longer decremented. Result parity, not full side-effect parity.
Constraints: standard-library Python; one implementation file; nonnegative integer stock and quantities only.
Non-goals: input validation expansion, concurrency, persistence, all-or-nothing transactions, deep copies.
Plan: lock explicit expected values and compare baseline results on copied inputs; reproduce mutation; add a local stock copy and redirect allocation; rerun identical unittest suite; deliver handoff and focused diff.
