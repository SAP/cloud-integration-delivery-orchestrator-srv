# Implementation Plan: Cancel Delivery Request Feature

## Status: ✅ IMPLEMENTED

## Overview
Add the ability to cancel a delivery request, which will permanently terminate and forbid any import/deploy operations for that request. The cancellation status should be persistent and prevent any future operations.

## Requirements Summary
- ✅ Cancellation is **allowed** for: `PENDING`, `WAITING_APPROVAL`, `AWAITING_IMPORT`, `IMPORT_FAILED`, `AWAITING_DEPLOY`, `DEPLOY_FAILED`
- ❌ Cancellation is **blocked** for: `IMPORTING`, `DEPLOYING`, `DEPLOYED`, `CANCELED`
- 🔄 Before canceling, sync status first to ensure latest state
- 🔒 Cancellation is **permanent** (cannot be un-canceled)
- 💬 Frontend dialog with cancellation reason (optional)
- 📝 Backend stores cancellation reason in Conditions table

---

## Implementation Summary

### Files Created
| File | Description |
|------|-------------|
| `service/cancel.go` | Core cancellation logic with `CancelDeliveryRequest()` function |

### Files Modified
| File | Changes |
|------|---------|
| `handler/delivery_request.go` | Added `preDeliverCheck()` cancellation check, `HandleCancelDr` handler, `CancelDrRequest` struct |
| `service/sync_status.go` | Changed `return nil` to `return error` for canceled DRs (line 67-69) |
| `main.go` | Added route `POST /api/v1/deliveryRequest/cancel` |
| `pkg/lifecycle/consts_test.go` | Added unit tests for cancellation logic |

---

## Final Implementation Details

### 1. Backend - Service Layer

#### 1.1 Cancellation Service Function
**File:** `service/cancel.go`

**Function:** `CancelDeliveryRequest(drID uint, userID string, reason string) error`

**Final Logic:**
```go
1. Sync status first: call SyncDeliveryStatus(drID, userID)
   - Ignore "not approved yet" error (PENDING/WAITING_APPROVAL are cancellable)
   - Return all other sync errors to caller
   
2. Re-query delivery request to get updated aggregate status (after sync)
   
3. Validate current aggregate status against cancellableStatuses map:
   - ALLOWED: AggPending, AggWaitingApprove, AggAwaitingImport, 
              AggImportFailed, AggWaitingDeploy, AggDeployFailed
   - BLOCKED: AggImporting, AggDeploying, AggDeployed, AggCanceled
   
4. Update delivery request:
   - Set aggregate_status = "CANCELED"
   - Set updated_by = userID
   
5. Create cancellation condition:
   - State: CondWarn
   - Message: "Delivery request canceled by {userEmail}. Reason: {reason}"
   
6. Send JIRA notification if JiraLink exists (async)

7. Send email notification to related users (async)
```

---

#### 1.2 Cancellation Checks in Import/Deploy Operations
**File:** `handler/delivery_request.go`

**Location:** `preDeliverCheck` function (line ~194)

```go
if dr.AggregateStatus == lifecycle.AggCanceled {
    return fmt.Errorf("delivery request #%d has been canceled, no operations allowed", req.DeliveryRequestID)
}
```

---

#### 1.3 Status Sync Returns Error for Canceled Delivery Requests
**File:** `service/sync_status.go` (line 67-69)

```go
// Return error for canceled delivery requests
if dr.AggregateStatus == lifecycle.AggCanceled {
    return fmt.Errorf("delivery request %d is already canceled", deliveryRequestID)
}
```

**Note:** Changed from `return nil` to return error for consistency.

---

### 2. Backend - Handler Layer

#### 2.1 Cancellation Handler
**File:** `handler/delivery_request.go`

**Handler:** `HandleCancelDr(ctx *gin.Context)`

**Request Body:**
```json
{
  "deliveryRequestID": 123,
  "reason": "No longer needed due to rollback of feature X"
}
```

**Response:**
- Success (200): `{"status": "success", "code": 200, "message": "Delivery request canceled successfully"}`
- Error (400): Invalid request or not allowed to cancel

---

#### 2.2 Route
**File:** `main.go`

**Route:** `POST /api/v1/deliveryRequest/cancel`

**Middleware:** Authentication required (existing AuthMiddleware)

---

### 3. Unit Tests

**File:** `pkg/lifecycle/consts_test.go`

**Test Cases Added:**
| Test | Description |
|------|-------------|
| `TestDeriveAggregateStatus_CanceledIsTerminal` | Verifies CANCELED is preserved regardless of import/deploy states (8 sub-tests) |
| `TestCancellableStatuses_Definition` | Documents and validates 6 cancellable statuses |
| `TestCancelStatusTransitions` | Tests each status for cancellation eligibility (10 sub-tests) |
| `TestDeriveAggregateStatus_DeployedWithDisabled` | Verifies DeployDisabled is treated as complete |
| `TestDeriveAggregateStatus_ImportDisabledProgressesToDeploy` | Verifies ImportDisabled allows progression |

---

## API Documentation

### Cancel Delivery Request
- **Endpoint:** `POST /api/v1/deliveryRequest/cancel`
- **Method:** POST
- **Auth:** Required
- **Body:**
  ```json
  {
    "deliveryRequestID": 123,
    "reason": "Cancellation reason (optional)"
  }
  ```
- **Success Response:** 
  ```json
  {
    "status": "success",
    "code": 200,
    "message": "Delivery request canceled successfully"
  }
  ```
- **Error Responses:** 
  - 400: `{"status": "fail", "code": 400, "error": "cannot cancel delivery request #123 with status IMPORTING"}`
  - 400: `{"status": "fail", "code": 400, "error": "delivery request #123 is already canceled"}`
  - 400: `{"status": "fail", "code": 400, "error": "failed to sync delivery status before cancel: ..."}`

---

## Cancellation Flow

```
POST /api/v1/deliveryRequest/cancel
    │
    └─► HandleCancelDr (handler)
            │
            └─► CancelDeliveryRequest (service)
                    │
                    ├─► SyncDeliveryStatus(drID, userID)
                    │       │
                    │       ├─► If not approved: "not approved yet" error
                    │       │       └─► Ignored ✓ (PENDING/WAITING_APPROVAL cancellable)
                    │       │
                    │       ├─► If already canceled: "already canceled" error
                    │       │       └─► Propagated to caller ✗
                    │       │
                    │       ├─► If TMS/CPI sync fails
                    │       │       └─► Propagated to caller ✗
                    │       │
                    │       └─► Success: updates operations & aggregate status
                    │
                    ├─► Re-query DR (get fresh status after sync)
                    │
                    ├─► Validate status is cancellable
                    │       └─► If IMPORTING/DEPLOYING/DEPLOYED: error ✗
                    │
                    ├─► Update aggregate_status = CANCELED
                    │
                    ├─► Insert cancellation condition
                    │
                    ├─► Send JIRA notification (async)
                    │
                    └─► Send email notification (async)
```

---

## Implementation Checklist

### Phase 1: Backend Core ✅
- [x] Create `service/cancel.go` with `CancelDeliveryRequest` function
- [x] Add cancellation check in `handler/delivery_request.go` `preDeliverCheck()`
- [x] Change `return nil` to `return error` in `service/sync_status.go` for canceled DRs

### Phase 2: Backend API ✅
- [x] Add `HandleCancelDr` handler in `handler/delivery_request.go`
- [x] Add `CancelDrRequest` struct
- [x] Add route in `main.go`

### Phase 3: Unit Tests ✅
- [x] Add cancellation tests in `pkg/lifecycle/consts_test.go`
- [x] Test CANCELED is terminal state
- [x] Test cancellable vs non-cancellable statuses

### Phase 4: Frontend (TODO)
- [ ] Add cancel button to UI
- [ ] Create confirmation dialog component
- [ ] Implement API call and error handling
- [ ] Add UI indicators for CANCELED status

---

## Design Decisions

### 1. Sync Before Cancel
**Decision:** Always call `SyncDeliveryStatus()` before cancellation to get real-time status from TMS/CPI.

**Rationale:** Prevents canceling a DR that appears as `AWAITING_IMPORT` in DB but is actually `IMPORTING` in TMS.

### 2. Ignore "Not Approved Yet" Error
**Decision:** Ignore this specific sync error to allow PENDING/WAITING_APPROVAL cancellation.

**Rationale:** These statuses have no operations started yet, so DB status is accurate. Sync is not needed but calling it simplifies the code (no status pre-check in cancel.go).

### 3. Return Error for Canceled DRs in Sync
**Decision:** Changed `SyncDeliveryStatus()` to return error instead of `nil` for canceled DRs.

**Rationale:** Consistent error handling. "Already canceled" error should propagate and block repeat cancellation.

### 4. Handler in Existing File
**Decision:** Add handler to `handler/delivery_request.go` instead of creating new file.

**Rationale:** Follows existing patterns. Cancel is a delivery request operation.

### 5. No Reason Length Limit
**Decision:** Reason field has no minimum length validation.

**Rationale:** Per user request. Reason is optional - empty string is allowed.

---

## Edge Cases Handled

| Edge Case | Solution |
|-----------|----------|
| Repeat cancellation | Returns "already canceled" error |
| Cancel during import/deploy | Sync first gets real-time status, blocks if in-progress |
| Cancel unapproved DR | Ignores "not approved yet" sync error, allows cancellation |
| Sync failure | Blocks cancellation, returns error to caller |
| Concurrent cancellation | Second request gets "already canceled" error |

---

## Success Criteria ✅

- [x] Users can cancel delivery requests in appropriate statuses  
- [x] Canceled delivery requests cannot trigger new import/deploy operations  
- [x] Cancellation reason is recorded in Conditions table  
- [x] Status sync returns error for canceled requests  
- [x] Unit tests verify cancellation logic  
- [x] No breaking changes to existing functionality
