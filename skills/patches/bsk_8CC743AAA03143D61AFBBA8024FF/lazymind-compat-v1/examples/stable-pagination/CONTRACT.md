# PACT contract — stable support-ticket pagination

Objective: add stable, one-based pagination to the supplied ticket sorter without mutating caller data.
Acceptance: preserve ascending priority and tie order; page boundaries, empty and out-of-range pages; reject bool/non-int page parameters with TypeError and non-positive integers with ValueError; unchanged nested records. Baseline ordering guardrails must pass.
Non-goals: cursor pagination, database integration, deep copies, malformed ticket validation.
Constraints: Python standard library, preserve sorted_tickets signature, add paginate_tickets(tickets, page, page_size), one implementation file.
Plan: write tests; execute baseline; replace in-place sort with sorted and add validation/slicing; rerun identical tests; deliver diff and handoff.
