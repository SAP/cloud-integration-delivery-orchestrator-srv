You’re absolutely right to question this. If `ArtifactTenantOperation` truly represents a single artifact version on a single tenant (a “cell” in the matrix), then a cell cannot be “PARTIAL” in the sense of “some done, some not” — it is either NOT_STARTED, IN_PROGRESS, FAILED, COMPLETE (or special transitional states like ROLLBACKING). The “PARTIAL” concept belongs to an aggregation (row, column, or whole request), not to a single cell—unless there is internal sub‑granularity inside that one tenant.

## Why PARTIAL Originally Appeared
I included `ImportPartial` / `DeployPartial` to cover situations where a “cell” might itself span multiple internal units, for example:
- A tenant internally has multiple runtime partitions / runtime nodes you handle individually.
- An “artifact” is a bundle made of sub-components imported/deployed one by one.
If you don’t have that internal fan‑out inside a single tenant, then PARTIAL at the cell level is misleading.

## Recommended Simplification (Cell-Level)
For a pure cell (artifact × tenant) keep states minimal:

ImportState:
- NOT_STARTED
- QUEUED (optional)
- IN_PROGRESS
- FAILED
- COMPLETE

DeployState:
- NOT_STARTED
- QUEUED (optional)
- IN_PROGRESS
- FAILED
- COMPLETE
- ROLLBACKING (optional)
- ROLLED_BACK (optional)
- CANCELED (if you want explicit vs condition)

Remove PARTIAL from both enums.

## Where “Partial” Belongs Instead
Use “partial” only at AGGREGATION layers as a derived condition, never as a primitive cell value:

Examples:
- Artifact across tenants: some COMPLETE, some IN_PROGRESS → AggregateStatus=IMPORTING + Condition PartialProgress=True
- DeliveryRequest across many artifact+tenant cells: some deploy COMPLETE, some failed, some in progress → AggregateStatus=DEPLOYING (or DEPLOY_FAILED if you choose fail-fast) + PartialProgress=True
- Batch / Wave: same logic.

So “partial” becomes an orthogonal condition, not a phase state.

## Updated Conceptual Model

Cell (ArtifactTenantOperation):
```
Transport: NOT_REQUESTED | REQUESTING | READY | FAILED
Import:    NOT_STARTED | QUEUED | IN_PROGRESS | FAILED | COMPLETE
Deploy:    NOT_STARTED | QUEUED | IN_PROGRESS | FAILED | COMPLETE | ROLLBACKING | ROLLED_BACK
```

Aggregation (artifact-level, request-level, batch-level):
- AggregateStatus chosen from: PENDING, AWAITING_IMPORT, IMPORTING, IMPORT_FAILED, IMPORTED, DEPLOYING, DEPLOY_FAILED, DEPLOYED, ROLLBACKING, ROLLED_BACK, CANCELED.
- Conditions may include: PartialProgress, RetryScheduled, LastFailureReason, RollbackInProgress, etc.

## Migration (If You Remove PARTIAL)
If you already stored PARTIAL in any rows (unlikely yet), map them:
- ImportPartial -> ImportInProgress (unless you can detect it is actually mixture; but at cell level treat as IN_PROGRESS)
- DeployPartial -> DeployInProgress

No data loss because PARTIAL wasn’t adding deterministic semantics at the cell granularity.

Pseudo-migration SQL (Postgres example):
```sql
UPDATE artifact_tenant_operations
SET import_state = 'IN_PROGRESS'
WHERE import_state = 'PARTIAL';

UPDATE artifact_tenant_operations
SET deploy_state = 'IN_PROGRESS'
WHERE deploy_state = 'PARTIAL';
```

## If You Later Need Sub-Granularity
Instead of resurrecting PARTIAL at the cell level, model internal units explicitly, e.g.:

`artifact_tenant_substeps`:
(artifact_tenant_operation_id, step_type=IMPORT|DEPLOY, step_name, state)

Then derive cell state purely from substeps (all COMPLETE → COMPLETE; any FAILED and none IN_PROGRESS → FAILED; any IN_PROGRESS → IN_PROGRESS; none started → NOT_STARTED). No need for PARTIAL keyword.

## Optional Code Change (Preview)
If you want to remove PARTIAL now, you’d edit `consts.go`:

Remove:
```
ImportPartial
DeployPartial
```
and adjust the aggregation logic to stop checking for them. The `DeriveAggregate` function then treats only IN_PROGRESS (no partial branch).

If you’d like me to apply that patch, tell me “remove partial states” and I’ll do it.

## Quick Before/After Snapshot

Before (current enums):
Import: NOT_STARTED | QUEUED | IN_PROGRESS | PARTIAL | FAILED | COMPLETE  
Deploy: NOT_STARTED | QUEUED | IN_PROGRESS | PARTIAL | FAILED | COMPLETE | ROLLBACKING | ROLLED_BACK

After (recommended):
Import: NOT_STARTED | QUEUED | IN_PROGRESS | FAILED | COMPLETE  
Deploy: NOT_STARTED | QUEUED | IN_PROGRESS | FAILED | COMPLETE | ROLLBACKING | ROLLED_BACK

“Partial” only appears as:
Condition: { type: "PartialProgress", status: "True", reason: "SomeCompletedSomePending", message: "12/30 operations complete" }

## Summary
- Yes, your intuition is correct: a per-tenant cell does not need PARTIAL.
- Remove PARTIAL from cell-level enums; express partiality through aggregation conditions.
- Migration is trivial: map PARTIAL to IN_PROGRESS if any exist.
- Add substep tables only if you later introduce internal fan-out inside a tenant.

Let me know if you want me to:
A) Remove PARTIAL states in code now.
B) Also add a helper to generate PartialProgress condition at aggregation level.

Just reply with A or A+B and I’ll patch it.