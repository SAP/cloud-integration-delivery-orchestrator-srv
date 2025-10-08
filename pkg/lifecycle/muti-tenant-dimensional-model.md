You want more depth now that:  
- There are multiple CPI target tenants (nodes) per artifact.  
- You also do batch import/deploy of multiple artifacts into a single tenant (bulk operations).  

I’ll expand the hybrid Phase + Sub-State + Conditions approach to cover BOTH dimensions:  
1) Artifact across many tenants (horizontal fan‑out).  
2) Batch operations (vertical grouping of many artifacts for one tenant or one transport route).  

## Outline (what follows)
1. Core concepts recap (phases, aggregate, conditions)  
2. Multi‑tenant dimensional model (Artifact x Tenant matrix)  
3. Batch operation modeling (bulk import/deploy)  
4. Data model additions (tables/fields)  
5. Status propagation algorithm (bottom → up)  
6. Partial / degraded semantics (precise rules)  
7. Retry & backoff at multiple layers  
8. Event & audit design (optional but recommended)  
9. Concurrency & idempotency patterns  
10. Rollback & cancellation mechanics  
11. Example JSON payloads (matrix + aggregation)  
12. Suggested Go struct sketches  
13. Query & indexing strategies  
14. Testing scenarios (table-driven)  
15. Incremental migration plan  

---

## 1. Core Concepts (Recap Quickly)
Internal per-phase states:
- Transport: NOT_REQUESTED | REQUESTING | READY | FAILED  
- Import: NOT_STARTED | QUEUED | IN_PROGRESS | PARTIAL | FAILED | COMPLETE  
- Deploy: NOT_STARTED | QUEUED | IN_PROGRESS | PARTIAL | FAILED | COMPLETE | ROLLBACKING | ROLLED_BACK  

Aggregate (externally exposed artifact-level):
PENDING, AWAITING_IMPORT, IMPORTING, IMPORT_FAILED, IMPORTED, DEPLOYING, DEPLOY_FAILED, DEPLOYED, ROLLBACKING, ROLLED_BACK, CANCELED, UNKNOWN (+ optional overlay flags).

Conditions: Add orthogonal truth bits (PartialProgress, RetryScheduled, RateLimited, RollbackInProgress, etc.).

---

## 2. Multi‑Tenant Dimensional Model

Think of delivery as a matrix:

            Target Tenant (Node)
            T1   T2   T3   T4 ...
Artifact A:  a1  a2   a3   a4
Artifact B:  b1  b2   b3   b4
Artifact C:  c1  c2   c3   c4

Each cell (ArtifactTenantOperation) has its own per-phase lifecycle.  
Top-level artifact status is derived from set {all cells for that artifact}.  
DeliveryRequest (if you have it as a grouping) aggregates all artifacts (rows) across all chosen tenants (columns).

Why?  
- Failures are often localized (e.g., T2 import fails, others OK).  
- You need precise visibility to permit selective retries without redoing everything.  
- Partial semantics become deterministic.

---

## 3. Batch Operations
Two batching directions:

A. Batch across artifacts for one tenant (common: “Deploy these 25 artifacts to tenant T3”).  
B. Batch across tenants for one artifact (fan‑out).  

Model a logical “BatchJob” entity that orchestrates N ArtifactTenantOperations:
- BatchJobType: IMPORT or DEPLOY
- Scope: {tenantID, artifactIDs[]} OR {artifactID, tenantIDs[]}
- State: QUEUED, IN_PROGRESS, PARTIAL, FAILED, COMPLETE, CANCELED

Batch jobs help:  
- Rate limiting (parallel item cap).  
- Aggregated retry rules (stop after K failures).  
- Consistent rollback (if > threshold fail, abort remaining).  

---

## 4. Data Model Additions

Proposed tables / structs:

1. artifact_tenant_operations  
   - id  
   - artifact_id (FK)  
   - tenant_id (FK)  
   - transport_state  
   - import_state  
   - deploy_state  
   - last_error  
   - retry_count_import  
   - retry_count_deploy  
   - next_retry_at  
   - conditions (JSON)  
   - timestamps  

2. batch_jobs  
   - id  
   - job_type (IMPORT|DEPLOY)  
   - scope_type (BY_TENANT | BY_ARTIFACT)  
   - tenant_id (nullable)  
   - artifact_id (nullable)  
   - aggregate_status  
   - failed_count  
   - success_count  
   - total_items  
   - conditions (JSON)  
   - created_by / timestamps  

3. batch_job_items (optional if you want explicit join instead of deriving from matrix)  
   - id  
   - batch_job_id  
   - artifact_tenant_operation_id  
   - status (QUEUED|RUNNING|FAILED|SUCCESS|SKIPPED|CANCELED)  
   - last_error  

4. artifact_events (optional event sourcing)  
   - id  
   - artifact_id  
   - tenant_id (nullable)  
   - operation (e.g., IMPORT_START)  
   - prev_state  
   - new_state  
   - reason_code  
   - payload (JSON)  
   - timestamp  

Add indexes:
- artifact_tenant_operations (artifact_id, tenant_id) UNIQUE  
- artifact_tenant_operations (tenant_id, deploy_state, import_state) for filtering  
- batch_job_items (batch_job_id, status)  

---

## 5. Status Propagation Algorithm

Bottom (cell) → row (artifact) → batch / request.

Algorithm (per Artifact):

1. Collect all cell states for artifact A.  
2. Derive artifact-level phase states:
   - Transport: READY if ALL cells READY; PARTIAL_READY if some READY; FAILED if any FAILED and none IN_PROGRESS; else REQUESTING.
   - Import: COMPLETE if all cells import COMPLETE; FAILED if any FAILED AND none IN_PROGRESS; PARTIAL if mixture COMPLETE + IN_PROGRESS; IN_PROGRESS if any IN_PROGRESS; NOT_STARTED otherwise.
   - Deploy: analogous logic.

3. Map to AggregateStatus with priority order:
   ROLLBACKING > ROLLED_BACK > CANCELED > any deploy FAILED > deploying (in-progress/partial) > imported > importing > awaiting_import > pending > deployed (terminal success).

4. If mixture of COMPLETE and NOT_STARTED in a phase (rare if orchestrated strictly), classify as PARTIAL + set Condition `PhaseDrift=True` (indicates orchestration skew).

For DeliveryRequest or BatchJob:
- Failed if threshold (e.g., >0 or >X%) cells failed.
- Partial if successes + in-progress coexist.
- Complete only when all terminal (success/skip) and no failures (unless policy allows graceful degrade).

---

## 6. Partial / Degraded Semantics

Define explicit numeric thresholds:

| Concept | Definition |
|---------|------------|
| PartialProgress | at least one success and at least one pending/in-progress/failed |
| Degraded | failures present but policy allows continuing (e.g., non-critical tenants) |
| Blocked | no progress for > configured stall timeout |
| Starved | queued but not scheduled due to concurrency caps |

Represent each as a condition.

Example condition object:
```json
{
  "type": "PartialProgress",
  "status": "True",
  "reason": "SomeCompletedSomePending",
  "message": "17/42 tenant operations completed"
}
```

---

## 7. Retry & Backoff (Multi-Layer)

Per cell:
- Maintain retry counters per phase (import vs deploy).
- Exponential backoff: next = base * 2^(retry_count), capped at max.

Batch-level:
- If certain failure reason codes are systemic (RATE_LIMIT, NETWORK), pause entire batch (Condition RateLimited=True) and reschedule.

Global strategies:
- Circuit breaker: if failure rate > threshold across recent window, throttle new operations (Condition GlobalThrottled=True).

---

## 8. Event & Audit Design

Event types (sample):
- TRANSPORT_REQUEST_SUBMITTED
- TRANSPORT_REQUEST_READY
- IMPORT_START
- IMPORT_NODE_COMPLETE (payload: node/tenant)
- IMPORT_FAILED
- DEPLOY_START
- DEPLOY_NODE_COMPLETE
- DEPLOY_FAILED
- ROLLBACK_START
- ROLLBACK_COMPLETE
- RETRY_SCHEDULED
- STATUS_AGGREGATED (rare—maybe skip this one)

Store reason_code enums (UPSTREAM_TIMEOUT, AUTH_DENIED, VALIDATION_ERROR, CONFLICT_VERSION).

Benefits:
- Post hoc analytics (“Mean time from TR_READY to IMPORTED”).
- Replay to reconstruct future enriched metrics.

---

## 9. Concurrency & Idempotency

Per batch job:
- Concurrency limit (e.g., 10 tenant operations concurrently).
- Use a work queue (channel or DB row locking).
- Idempotency key: (artifact_id, tenant_id, phase) to prevent double execution on retries if a transient network error occurs after success but before acknowledgment.

Idempotent pattern:
- Before starting work, mark cell row with `execution_token=uuid` and `phase_state=IN_PROGRESS` using an optimistic version field.
- On completion, update to COMPLETE only if token matches and state is still IN_PROGRESS (avoid out-of-order writes).

---

## 10. Rollback & Cancellation

Rollback triggers:
- Policy threshold (e.g., >20% deploy failed).
- Manual user command.

Implementation:
1. Mark higher-level entity (Batch or DeliveryRequest) Condition RollbackInProgress=True.
2. For already DEPLOYED cells: enqueue rollback tasks.
3. For IN_PROGRESS: allow completion or cancel based on policy.
4. For NOT_STARTED: mark SKIPPED.

Rollback outcomes:
- If all rollback tasks succeed → ROLLED_BACK.
- Partial rollback failure: keep ROLLBACKING until manual intervention or mark Degraded and open incident.

---

## 11. Example JSON (Artifact with 4 tenants)

```json
{
  "artifactId": "iflow-123",
  "aggregateStatus": "DEPLOYING",
  "transportState": "READY",
  "importState": "COMPLETE",
  "deployState": "IN_PROGRESS",
  "conditions": [
    {"type": "TransportReady","status": "True","reason": "TRNumberAcquired"},
    {"type": "ImportComplete","status": "True"},
    {"type": "PartialProgress","status": "True","reason": "SomeNodesDeploying","message": "2/4 tenants deployed"},
    {"type": "RetryScheduled","status": "False"}
  ],
  "tenantMatrix": [
    {
      "tenantId": 10,
      "name": "cpi-dev",
      "states": {"import": "COMPLETE", "deploy": "COMPLETE"},
      "lastError": ""
    },
    {
      "tenantId": 11,
      "name": "cpi-test",
      "states": {"import": "COMPLETE", "deploy": "IN_PROGRESS"}
    },
    {
      "tenantId": 12,
      "name": "cpi-preprod",
      "states": {"import": "COMPLETE", "deploy": "FAILED"},
      "lastError": "RUNTIME_TIMEOUT",
      "retryCountDeploy": 1,
      "nextRetryAt": "2025-10-01T09:25:00Z"
    },
    {
      "tenantId": 13,
      "name": "cpi-prod",
      "states": {"import": "COMPLETE", "deploy": "NOT_STARTED"}
    }
  ]
}
```

---

## 12. Go Struct Sketches

```go
type ArtifactTenantOperation struct {
    ID          uint
    ArtifactID  uint
    TenantID    uint
    TransportState TransportState
    ImportState    ImportState
    DeployState    DeployState
    LastError      string
    RetryCountImport int
    RetryCountDeploy int
    NextRetryAt    *time.Time
    ConditionsJSON datatypes.JSON // or []byte
    UpdatedAt      time.Time
    CreatedAt      time.Time
}

type BatchJob struct {
    ID             uint
    JobType        string // IMPORT or DEPLOY
    ScopeType      string // BY_TENANT or BY_ARTIFACT
    TenantID       *uint
    ArtifactID     *uint
    AggregateStatus AggregateStatus
    SuccessCount   int
    FailedCount    int
    TotalItems     int
    ConditionsJSON datatypes.JSON
    CreatedBy      string
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type Condition struct {
    Type               string    `json:"type"`
    Status             string    `json:"status"` // True, False, Unknown
    Reason             string    `json:"reason,omitempty"`
    Message            string    `json:"message,omitempty"`
    LastTransitionTime time.Time `json:"lastTransitionTime"`
}
```

Aggregation utility (pseudo):

```go
func AggregateArtifact(cells []ArtifactTenantOperation) (transport TransportState, importState ImportState, deployState DeployState, conds []Condition, agg AggregateStatus) {
    // Collect frequency maps per phase
    // Derive states using priority logic
    // Build conditions (PartialProgress when mix)
    // Compute aggregate via previously defined priority chain
    return
}
```

---

## 13. Query / Index Strategies

Queries you’ll need:
- “All artifacts with deploy failures for prod tenants”: index on (tenant_id, deploy_state).
- “Artifacts still awaiting import”: filter import_state=NOT_STARTED and transport_state=READY.
- “Retry due tasks”: `next_retry_at <= now AND (import_state=FAILED OR deploy_state=FAILED)` + index on (next_retry_at).
- Batch progress: COUNT WHERE batch_job_id=? AND status IN (...).

Indexes:
- artifact_tenant_operations (artifact_id, tenant_id) UNIQUE
- artifact_tenant_operations (deploy_state)
- artifact_tenant_operations (import_state)
- artifact_tenant_operations (next_retry_at)
- batch_job_items (batch_job_id, status)

Use partial indexes if DB = Postgres (e.g., only for FAILED states).

---

## 14. Testing Scenarios

Table-driven test examples:

1. All cells NOT_STARTED → Aggregate=PENDING  
2. All transport READY, none import started → AWAITING_IMPORT  
3. Some import IN_PROGRESS, others NOT_STARTED → IMPORTING + PartialProgress=True  
4. Import mixture COMPLETE + FAILED, no IN_PROGRESS → IMPORT_FAILED (not PARTIAL)  
5. All import COMPLETE, deployments NOT_STARTED → IMPORTED  
6. Deploy states: COMPLETE + IN_PROGRESS + FAILED → DEPLOY_FAILED (why? failure outranks in-progress) but you might choose DEPLOYING + condition LastFailureReason if you prefer continuous mode; define rule clearly.  
7. Deploy: COMPLETE + IN_PROGRESS only → DEPLOYING  
8. All deploy COMPLETE → DEPLOYED  
9. Rollback flag set → ROLLBACKING regardless of underlying states.  
10. ROLLED_BACK states terminal.  

Also test idempotent retry update: two concurrent goroutines → only one transition persists.

---

## 15. Incremental Migration Plan

Phase 1 (Introduce Matrix):
- Create `artifact_tenant_operations`.
- Populate from existing artifact records for current tenants (import state = derived).
- Keep old artifact integer status; derive it from aggregate for compatibility.

Phase 2 (Dual Write):
- On any status change, update cell + recompute artifact aggregate + rewrite legacy integer.

Phase 3 (Clients Migrate):
- Expose new JSON (include both old code and new fields).
- After clients switch, deprecate legacy integer.

Phase 4 (Conditions & Retry):
- Add conditions store.
- Add scheduler for retries scanning failed cells.

Phase 5 (Batch Jobs):
- Introduce batch job orchestration API endpoints.
- Track job-level metrics & auditing.

Phase 6 (Rollback & Advanced Policies):
- Add rollback transitions + condition-driven gating.

---

## Decision Points You Should Clarify (Before Coding)
- Should deployment proceed if some imports failed (graceful degrade) or is it strict all-or-nothing?
- Failure priority: Do you surface failures immediately (DEPLOY_FAILED) or show DEPLOYING with a failure condition until all attempts exhausted?
- Partial semantics: prefer “Aggregate stays in *ING unless 100% success or 100% terminal failure” or “Fail fast on any failure”.
- Retry policy per phase (import vs deploy) independent or shared?

I can implement either “fail fast” or “tolerant” logic—just state preference.

---

## Recommended Defaults (If you don’t decide yet)
- Fail fast for IMPORT failures (don’t attempt deployment for cells that never imported).
- Tolerant for DEPLOY (allow other tenants to continue while some retry).
- Aggregate shows DEPLOY_FAILED only when no remaining active deploy attempts (i.e., all failing or finished). While still chasing retries, show DEPLOYING + condition LastFailureReason.

---

## Next Step
If you want to proceed, tell me:
1. Fail-fast or tolerant deploy mode?
2. Lean or full model now?
3. Add batch job scaffolding now or later?

Reply with those choices (e.g., “tolerant deploy, lean model, add batch later”) and I’ll generate code scaffolding (status package + structs + aggregation helper + test skeleton).

Let me know your preferences and I’ll implement.