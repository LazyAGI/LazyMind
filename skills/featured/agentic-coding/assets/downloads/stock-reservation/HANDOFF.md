# Handoff — non-mutating stock reservation

## Delivered
Create a shallow remaining = stock.copy() and direct availability checks/deductions to remaining. The signature, greedy request ordering and tuple-of-dicts return are unchanged; removing input mutation is an intentional side-effect change.

## Scope and files
solution.py is the only implementation change (one added line, two redirected lines). baseline.py is the original input, test_solution.py contains the locked tests, CONTRACT.md records scope. No production stock system or dependencies were touched.

## Executed evidence
Codex ran `python3 -m unittest -v test_solution` before and after the implementation change, with identical tests.
Before: 10 test methods; 7 pass, 3 fail (normal stock mutation, insertion for zero quantity, partial mutation before KeyError). Exit 1.
After: 10 test methods pass, exit 0. One method contains 100 baseline/solution return-value comparisons: available A 0..3 × first quantity 0..4 × second quantity 0..4, each with unknown X quantity 1.
Example returns ({"USB-C": 3, "HDMI": 2}, {"USB-C": 4, "SSD": 1}) while caller stock stays {"USB-C": 5, "HDMI": 2}.
Audit directory includes both logs, runtime metadata, unchanged-test hashes and change.patch.

## Risks and rollback
Callers previously relying on in-place stock deduction must now explicitly persist their allocation; this function does not provide a stock commit. No database transaction, atomicity or concurrency guarantees. Contract inputs are dictionaries of nonnegative integer quantities; no expanded validation and no deep-copy guarantee.
Rollback: replace only the reviewed solution.py implementation with baseline.py through a scoped integration change; that restores the original mutations. Preserve unrelated changes.
Tests executed in Codex, not LazyMind. LazyMind execution depends on available tools. Production integration and concurrency remain unverified.
