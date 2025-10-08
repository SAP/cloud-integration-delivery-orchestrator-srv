I want to design Artifact status sequence. Here is backgroud:
Artifact have to generate a Transport Request number, for Import to target CPI tenant. Then can Import this Transport Request number(TR number) to the target CPI tenant. After TR number imported, the artifact can be deployed, stauts will be RUNNING.
So inconclusion,there is two status from two system: TMS, CPI tenant.
here is what I consider about at present: 0 = unknown, 1 = pending, 2 = Importing, 3 = Imported(i.e. design time deployed), 4 = Deploying, 5 = Deployed(runtime deployed). I didn't consider enough about error status now.

Acknowledging your goal: define a robust, future‑proof Artifact status model that cleanly covers both TMS (transport request lifecycle) and CPI (design-time import + runtime deployment), including error/retry scenarios.

## High-Level Approach
1. Separate concerns: Transport (TMS) vs Deployment (CPI runtime) vs Overall (aggregated).
2. Define a canonical internal state machine (phases + sub-states) rather than a single flat integer.
3. Introduce explicit terminal and retry-capable failure states.
4. Support multi-node aggregation (partial import/deploy).
5. Keep wire/API status stable (string enum), map to numeric codes internally for storage/sorting.

---

## Suggested Model Overview

Instead of one linear list, think in layers:

1. Transport Phase (TR lifecycle)  
   - TR_NOT_REQUESTED  
   - TR_REQUESTING  
   - TR_REQUEST_FAILED (retryable)  
   - TR_READY (has TR number)  
2. Import Phase (design-time)  
   - IMPORT_QUEUED  
   - IMPORTING  
   - IMPORT_PARTIAL (multi-node scenario)  
   - IMPORT_FAILED (retryable)  
   - IMPORTED (design-time success)  
3. Deployment Phase (runtime)  
   - DEPLOY_QUEUED  
   - DEPLOYING  
   - DEPLOY_PARTIAL  
   - DEPLOY_FAILED (retryable)  
   - DEPLOYED (runtime success)  
4. Special / Cross-cutting  
   - CANCELED  
   - SKIPPED (e.g. filter rules say “not applicable”)  
   - ROLLBACKING  
   - ROLLED_BACK  
   - EXPIRED (TR or artifact version no longer valid)  
   - INVALID (structural validation failure)

Then define an Aggregate Status that rolls these up into what you display externally:
- UNKNOWN
- PENDING (pre-TR or queued)
- AWAITING_IMPORT (TR_READY but import not started)
- IMPORTING
- IMPORT_FAILED
- IMPORTED
- DEPLOYING
- DEPLOY_FAILED
- DEPLOYED
- PARTIAL (for either import or deploy partial)
- CANCELED / SKIPPED / ROLLED_BACK / INVALID

---

## Why Not Just One Integer Sequence?

A single linear ladder (0→5) breaks when:
- You need to retry import without losing info about TR readiness.
- Deployment can start only after import, but partial deployments across multiple target nodes create ambiguity.
- You add rollback or cancellation.
- You want to distinguish “not started because TR missing” vs “queued but blocked”.

---

## Recommended Internal Structure

In `Artifact` (or an ancillary status table):

```go
type ArtifactStatusPhase string
type ArtifactAggregateStatus string

const (
    PhaseTransport  ArtifactStatusPhase = "TRANSPORT"
    PhaseImport     ArtifactStatusPhase = "IMPORT"
    PhaseDeploy     ArtifactStatusPhase = "DEPLOY"
    PhaseFinalized  ArtifactStatusPhase = "FINAL"
)

// Canonical aggregate (what UI / API can show)
const (
    AggUnknown        ArtifactAggregateStatus = "UNKNOWN"
    AggPending        ArtifactAggregateStatus = "PENDING"
    AggAwaitingImport ArtifactAggregateStatus = "AWAITING_IMPORT"
    AggImporting      ArtifactAggregateStatus = "IMPORTING"
    AggImportFailed   ArtifactAggregateStatus = "IMPORT_FAILED"
    AggImported       ArtifactAggregateStatus = "IMPORTED"
    AggDeploying      ArtifactAggregateStatus = "DEPLOYING"
    AggDeployFailed   ArtifactAggregateStatus = "DEPLOY_FAILED"
    AggDeployed       ArtifactAggregateStatus = "DEPLOYED"
    AggPartial        ArtifactAggregateStatus = "PARTIAL"
    AggCanceled       ArtifactAggregateStatus = "CANCELED"
    AggSkipped        ArtifactAggregateStatus = "SKIPPED"
    AggRolledBack     ArtifactAggregateStatus = "ROLLED_BACK"
    AggInvalid        ArtifactAggregateStatus = "INVALID"
)
```

Keep your existing integer for backwards compatibility if needed:
```go
// Legacy numeric codes (optional mapping)
const (
    CodeUnknown = iota
    CodePending
    CodeImporting
    CodeImported
    CodeDeploying
    CodeDeployed
    CodeFailedImport
    CodeFailedDeploy
    CodePartial
    CodeCanceled
)
```

---

## State Transition Summary

(Readable pseudograph)

TR_NOT_REQUESTED
  → TR_REQUESTING → TR_REQUEST_FAILED ↺ (retry) → TR_READY
TR_READY
  → IMPORT_QUEUED → IMPORTING → IMPORT_FAILED ↺ (retry) → IMPORTED
IMPORTED
  → DEPLOY_QUEUED → DEPLOYING → DEPLOY_FAILED ↺ (retry) → DEPLOYED
Any (non-final)
  → CANCELED
IMPORTED / DEPLOYED
  → ROLLBACKING → ROLLED_BACK

Partial states can be injected when ≥1 target node succeeded and ≥1 is still pending or failed (IMPORT_PARTIAL / DEPLOY_PARTIAL).

---

## Aggregation Logic (Multi-Node)

For each artifact maintain per-node status (you already have `NodeStatus` map). Derive aggregate:

Pseudo rules (deployment example):
1. If all nodes runtime deployed → AggDeployed
2. If any node failed and none deploying/importing → AggDeployFailed
3. If some deployed and others still deploying/importing → AggDeploying (or AggPartial if you want explicit)
4. If import complete on all but deployment not started anywhere → AggImported
5. If some imported and others importing → AggImporting or AggPartial
6. If all queued → AggPending
7. If any failed during import → AggImportFailed (unless retries in progress, then maybe AggImporting with error flag)

Implement a function:

```go
func DeriveAggregate(importNodeStates, deployNodeStates []NodePhase) ArtifactAggregateStatus
```

---

## Error / Retry Handling

Track:
- `LastError` (string)
- `RetryCount` (int)
- `NextRetryAt` (time.Time)
- `MaxRetries` (config)

Store per-phase or per-node if granular:
```go
type PhaseStatus struct {
    Phase        ArtifactStatusPhase
    State        string
    LastError    string
    RetryCount   int
    UpdatedAt    time.Time
}
```

---

## Suggested Minimal Initial Set (If You Want Lean Start)

If you want to stay lean but future-proof:

1. UNKNOWN
2. PENDING (covers TR_NOT_REQUESTED / REQUESTING)
3. AWAITING_IMPORT (TR_READY)
4. IMPORTING
5. IMPORT_FAILED
6. IMPORTED
7. DEPLOYING
8. DEPLOY_FAILED
9. DEPLOYED
10. PARTIAL (flag overlay rather than separate terminal)
11. CANCELED
12. ROLLED_BACK

Add boolean overlays:
- `Partial bool`
- `Rollback bool`
- `Retrying bool`

This avoids explosion of distinct constants.

---

## Mapping External System Signals

TMS events:
- TR number created → transition to AWAITING_IMPORT (if artifact not yet importing)
- Import job started → IMPORTING
- Import job success (design-time available) → IMPORTED
- Import job failure → IMPORT_FAILED

CPI runtime events:
- Deployment start → DEPLOYING
- Deployment success → DEPLOYED
- Deployment failure → DEPLOY_FAILED

If you poll, create a translator:
```go
func MapTMSStatus(tmsRaw string) (phase ArtifactStatusPhase, intermediate string, terminal bool)
func MapCPIStatus(cpiRaw string) (deployState string)
```
Then fold into aggregate.

---

## Go Implementation Sketch

```go
type ArtifactLifecycle struct {
    Aggregate    ArtifactAggregateStatus `json:"aggregate"`
    Partial      bool                    `json:"partial"`
    Rollback     bool                    `json:"rollback"`
    LastError    string                  `json:"lastError,omitempty"`
    RetryCount   int                     `json:"retryCount"`
    NextRetryAt  *time.Time              `json:"nextRetryAt,omitempty"`
    UpdatedAt    time.Time               `json:"updatedAt"`
}

func (a *ArtifactLifecycle) Transition(next ArtifactAggregateStatus, opts ...TransitionOption) error {
    // validate allowed transitions (guard rails)
}
```

Validation table: keep a `map[ArtifactAggregateStatus]map[ArtifactAggregateStatus]bool` of allowed transitions to prevent illegal jumps (e.g., DEPLOYED → IMPORTING unless rollback resets).

---

## Failure States (More Detail)

Differentiate:
- IMPORT_FAILED (transport & design-time)
- DEPLOY_FAILED (runtime start)
- VALIDATION_FAILED / INVALID (pre-flight schema or dependency failure)
- ROLLBACK_FAILED (optional future)

You can collapse them into one `FAILED` plus `FailurePhase` metadata, but separate constants give clearer dashboards.

---

## Backward Compatibility

If existing code expects the integers 0–6:
- Keep writing those codes while you enrich statuses internally.
- Provide conversion:
```go
func LegacyCodeFromAggregate(a ArtifactAggregateStatus, partial bool) int
```

---

## Migration Plan

1. Add new status columns (e.g., `aggregate_status`, `last_error`, `retry_count`, `partial`) nullable.
2. Backfill existing rows: map old numeric to new aggregate.
3. Switch writer logic to set new fields; keep writing legacy integer for a deprecation window.
4. Update API responses to include both until clients migrate.

---

## Example End-to-End Flow

1. Create artifact → PENDING
2. TR acquired → AWAITING_IMPORT
3. Start import (some nodes) → IMPORTING
4. Some nodes done, others pending → IMPORTING + Partial=true (or PARTIAL aggregate)
5. All imported → IMPORTED
6. Start deploy → DEPLOYING
7. One node fails → DEPLOY_FAILED (retry policy) or DEPLOYING + Partial + LastError
8. Retry success → DEPLOYED

---

## Quick Answer to Your Current List

Your current linear list (0–5) is a good skeleton, but you'll quickly need:
- AWAITING_IMPORT (between TR ready and actual import start)
- Separate FAILED states (import vs deploy)
- Partial indicator (multi-node)
- CANCELED / SKIPPED / ROLLED_BACK (inevitable operational ops)
So adopt a more expressive model now to avoid data migrations later.

---

## Optional: Minimal Code Constants Patch (If You Want)

Just say “add constants” and I can patch in a constants file plus mapping helpers.

---

## Summary

Design around phases + aggregate status + overlays (partial/retry/error) rather than a single escalating integer. Distinguish import vs deploy failures, add awaiting state, and prepare for partial multi-node situations. This gives clarity, extensibility, and cleaner UI logic.

Let me know:
- Do you want me to add the constants file now?
- Prefer lean (10 statuses) or full (phased) model?

Happy to implement whichever you pick.