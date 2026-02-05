# Implementation Plan: Cancel Delivery Request Feature

## Overview
Add the ability to cancel a delivery request, which will permanently terminate and forbid any import/deploy operations for that request. The cancellation status should be persistent and prevent any future operations.

## Requirements Summary
- ✅ Cancellation is **allowed** for: `PENDING`, `WAITING_APPROVAL`, `AWAITING_IMPORT`, `IMPORT_FAILED`, `AWAITING_DEPLOY`, `DEPLOY_FAILED`
- ❌ Cancellation is **blocked** for: `IMPORTING`, `DEPLOYING`, `DEPLOYED`, `CANCELED`
- 🔄 Before canceling, sync status first to ensure latest state
- 🔒 Cancellation is **permanent** (cannot be un-canceled)
- 💬 Frontend dialog with cancellation reason
- 📝 Backend stores cancellation reason in Conditions table

---

## Implementation Tasks

### 1. Backend - Service Layer

#### 1.1 Add Cancellation Service Function
**File:** `service/cancel.go` (new file)

**Function:** `CancelDeliveryRequest(drID uint, userID string, reason string) error`

**Logic:**
```go
1. Sync latest status first: call SyncDeliveryStatus(drID, userID)
   - This ensures we have the most current import/deploy states
   
2. Query delivery request with associations
   
3. Validate current aggregate status:
   - ALLOWED: AggPending, AggWaitingApprove, AggAwaitingImport, 
              AggImportFailed, AggWaitingDeploy, AggDeployFailed
   - BLOCKED: AggImporting, AggDeploying, AggDeployed, AggCanceled
   - If blocked, return error with clear message
   
4. Check for any in-progress operations:
   - Query all operations for this delivery request
   - Check if any operation has ImportState = ImportInProgress 
     OR DeployState = DeployInProgress
   - If found, return error: "Cannot cancel: operations in progress"
   
5. Update delivery request:
   - Set aggregate_status = "CANCELED"
   - Set updated_by = userID
   
6. Create cancellation condition:
   - State: CondWarn (or new CondCanceled type)
   - Message: "Delivery request canceled by {userID}. Reason: {reason}"
   - Insert into conditions table
   
7. (Optional) Send JIRA notification if JiraLink exists
```

**Error Handling:**
- Invalid status transitions
- Operations in progress
- Database errors

---

#### 1.2 Add Cancellation Checks in Import/Deploy Operations
**File:** `handler/delivery_request.go`

**Location:** `preDeliverCheck` function (line ~186)

**Add check after delivery request is found:**
```go
if dr.AggregateStatus == lifecycle.AggCanceled {
    return fmt.Errorf("delivery request #%d has been canceled, no operations allowed", req.DeliveryRequestID)
}
```

This check is placed in the handler layer's `preDeliverCheck` function which is called before both `HandleImportOps` and `HandleDeployOps`.

---

#### 1.3 Skip Status Sync for Canceled Delivery Requests
**File:** `service/sync_status.go`

**Function:** `SyncDeliveryStatus` (line 54)

**Add check after line 63:**
```go
if dr.ApprovedAt == nil || dr.ApprovedBy == "" {
    return fmt.Errorf("delivery request %d has not been approved yet", deliveryRequestID)
}

// Add this check:
if dr.AggregateStatus == lifecycle.AggCanceled {
    return nil // Skip sync for canceled delivery requests
}
```

---

### 2. Backend - Handler Layer

#### 2.1 Add Cancellation Handler
**File:** `handler/delivery_request.go` (added to existing file)

**Handler:** `HandleCancelDr(ctx *gin.Context)`

**Request Body:**
```json
{
  "reason": "No longer needed due to rollback of feature X"
}
```

**URL Parameter:** `:id` - delivery request ID

**Response:**
- Success (200): `{"status": "success", "code": 200, "message": "Delivery request canceled successfully"}`
- Error (400): Invalid request or not allowed to cancel
- Error (500): Internal server error

**Implementation:**
```go
1. Parse delivery request ID from URL parameter
2. Bind JSON request body
3. Validate reason (required, min length 10)
4. Get user ID from context: service.UserID(ctx)
5. Call service.CancelDeliveryRequest(drID, userID, reason)
6. Return appropriate HTTP response
```

---

#### 2.2 Add Route
**File:** `main.go`

**Route:** `POST /api/v1/deliveryRequest/:id/cancel`

**Middleware:** Authentication required (existing AuthMiddleware)

---

### 3. Frontend

#### 3.1 Add Cancel Button
**Location:** Delivery Request detail page

**Conditions:**
- Only show if aggregate status allows cancellation
- Disabled/hidden for: IMPORTING, DEPLOYING, DEPLOYED, CANCELED

---

#### 3.2 Add Confirmation Dialog
**Component:** `CancelDeliveryRequestDialog`

**Fields:**
- Title: "Cancel Delivery Request"
- Warning message: "This action is permanent and cannot be undone. All pending import/deploy operations will be terminated."
- Reason textarea (required, min 10 chars)
- Cancel/Confirm buttons

**Validation:**
- Reason is required
- Minimum 10 characters
- Show character count

---

#### 3.3 API Call
**Endpoint:** `POST /api/v1/delivery-request/{id}/cancel`

**Payload:**
```typescript
{
  deliveryRequestId: number;
  reason: string;
}
```

**Success Handling:**
- Show success notification
- Refresh delivery request data
- Update UI to show CANCELED status

**Error Handling:**
- Display error message from API
- Common errors:
  - "Cannot cancel: operations in progress"
  - "Cannot cancel delivery request with status DEPLOYED"
  - "Delivery request already canceled"

---

### 4. Testing

#### 4.1 Unit Tests
**File:** `service/cancel_test.go`

**Test Cases:**
```
✓ Cancel delivery request in AWAITING_IMPORT status
✓ Cancel delivery request in IMPORT_FAILED status  
✓ Cancel delivery request in AWAITING_DEPLOY status
✓ Cancel delivery request in DEPLOY_FAILED status
✗ Reject cancellation for IMPORTING status
✗ Reject cancellation for DEPLOYING status
✗ Reject cancellation for DEPLOYED status
✗ Reject cancellation for already CANCELED status
✗ Reject cancellation with operations in progress
✓ Store cancellation reason in conditions
```

---

#### 4.2 Integration Tests
```
✓ Import operation blocked for canceled delivery request
✓ Deploy operation blocked for canceled delivery request
✓ Status sync skipped for canceled delivery request
✓ Cancellation creates proper condition record
✓ Cancellation is permanent (cannot change status after cancel)
```

---

### 5. Database Changes

**No schema changes required** - the `CANCELED` status already exists in `pkg/lifecycle/consts.go` as `AggCanceled`.

---

### 6. Documentation Updates

#### 6.1 Update README.md
- ~~Remove from TODO list~~ (already added)
- Add to features list

#### 6.2 API Documentation
Document the new endpoint:
```markdown
### Cancel Delivery Request
- **Endpoint:** `POST /api/v1/deliveryRequest/:id/cancel`
- **Method:** POST
- **Auth:** Required
- **URL Param:** `:id` - delivery request ID
- **Body:**
  ```json
  {
    "reason": "Cancellation reason (min 10 chars)"
  }
  ```
- **Success Response:** 200 OK
- **Error Responses:** 
  - 400: Invalid status or operations in progress
  - 500: Server error
```

---

## Implementation Order

### Phase 1: Backend Core (Critical Path)
1. ✅ Create `service/cancel.go` with `CancelDeliveryRequest` function
2. ✅ Add cancellation checks in `service/deliver.go`
3. ✅ Add skip logic in `service/sync_status.go`
4. ✅ Write unit tests for service layer

### Phase 2: Backend API
5. ✅ Create `handler/cancel_dr.go` with handler
6. ✅ Add route in router configuration
7. ✅ Test API endpoint with curl/Postman

### Phase 3: Frontend
8. ✅ Add cancel button to UI
9. ✅ Create confirmation dialog component
10. ✅ Implement API call and error handling
11. ✅ Add UI indicators for CANCELED status

### Phase 4: Testing & Documentation
12. ✅ Integration tests
13. ✅ Update documentation
14. ✅ Manual end-to-end testing

---

## Edge Cases & Considerations

### 1. Race Conditions
**Scenario:** User cancels while an import/deploy is starting
**Solution:** The pre-check in import/deploy operations will catch this and reject the operation

### 2. Partial Operations
**Scenario:** Some operations completed before cancellation
**Solution:** Those operations keep their completed status. Cancellation only prevents new operations.

### 3. JIRA Integration
**Question:** Should cancellation be posted to JIRA?
**Recommendation:** Yes, similar to import success notifications

### 4. Audit Trail
**Covered:** Cancellation reason stored in Conditions table with timestamp and user ID

### 5. UI State Management
**Important:** Ensure UI refreshes after cancellation to reflect CANCELED status and disable all action buttons

---

## Code Quality Checklist

- [ ] Error messages are clear and actionable
- [ ] All database operations use transactions where appropriate
- [ ] Logging added for cancellation events
- [ ] Input validation (reason length, valid drID)
- [ ] Consistent error handling patterns
- [ ] Code follows existing service/handler patterns
- [ ] Comments explain business logic
- [ ] No hardcoded values

---

## Risk Assessment

| Risk | Severity | Mitigation |
|------|----------|------------|
| Cancel during active operation | Medium | Pre-check operations before cancel |
| Race condition on status check | Low | Database transaction isolation |
| User cancels by mistake | Medium | Confirmation dialog + reason required |
| Cannot undo cancellation | Low | Clear warning in UI, permanent by design |

---

## Success Criteria

✅ Users can cancel delivery requests in appropriate statuses  
✅ Canceled delivery requests cannot trigger new import/deploy operations  
✅ Cancellation reason is recorded and visible  
✅ Status sync is skipped for canceled requests  
✅ UI clearly indicates canceled status  
✅ No breaking changes to existing functionality  

---

## Estimated Effort

- **Backend:** 4-6 hours
  - Service layer: 2 hours
  - Handler + route: 1 hour
  - Unit tests: 1-2 hours
  - Integration tests: 1 hour

- **Frontend:** 3-4 hours
  - Cancel button: 0.5 hour
  - Dialog component: 1.5 hours
  - API integration: 1 hour
  - Testing: 1 hour

- **Documentation:** 1 hour

**Total:** 8-11 hours

---

## Future Enhancements (Out of Scope)

- Partial cancellation (cancel specific operations, not entire DR)
- Scheduled cancellation (cancel at specific time)
- Bulk cancellation (cancel multiple DRs)
- Cancel and create new DR (clone and cancel old one)
