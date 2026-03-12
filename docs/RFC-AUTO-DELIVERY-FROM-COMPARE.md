# RFC: Auto-Create Delivery Request from Version Compare Mismatches

## 目录

1. [需求概述](#1-需求概述)
2. [现有系统分析](#2-现有系统分析)
3. [核心设计决策](#3-核心设计决策)
4. [API 设计](#4-api-设计)
5. [后端实现计划](#5-后端实现计划)
6. [前端 UI 方案](#6-前端-ui-方案)
7. [执行阶段](#7-执行阶段)
8. [测试计划](#8-测试计划)

---

## 1. 需求概述

### 1.1 背景

Version Compare 功能已经可以比较各 tenant 间 artifact 的 design time / runtime 版本差异。当用户发现 mismatch 时，目前需要手动创建 Delivery Request (DR)、逐个选择需要 deliver 的 artifacts、生成 Transport Request (TR)、提交审批。

本 feature 的目标是**基于 Version Compare 的 design time 版本差异，半自动化创建 Delivery Request**，减少手动操作。

### 1.2 核心需求

- 在 Version Compare 详情页（`VersionCompareDetailView`）增加一个 action button
- 用户点击后，系统分两步完成：
  1. **Preview 阶段**：分析 Version Compare 缓存数据，返回分类后的 artifact 列表（可纳入、DRAFT、版本模式不匹配、已在活跃 DR 中的重复项）
  2. **Create 阶段**：用户在 Dialog 中确认选择后，系统创建 DR 并预填选中的 artifacts（TR 为空）
- 用户被引导至 DR 详情页，看到预填好的 artifact 列表，然后走现有流程：生成 TR → Update → 审批 → Import → Deploy

### 1.3 关键约束

- **只比较 Design Time 版本**：Runtime 版本差异不触发自动 DR 创建（因为 Runtime 差异可能仅是部署延迟）
- **DRAFT 排除**：Source tenant 中 `DesignTimeVersion == "Active"` 的 artifacts 不纳入 DR（DRAFT 不稳定，无法生成 TR）
- **任意 target 不一致即纳入**：只要有一个 target tenant 的 DT 版本与 source 不同，该 artifact 就被纳入 DR。TMS chain 会自动处理传播
- **走 TMS 路径 (v1)**：使用完整的 DR → TR → Import → Deploy 生命周期
- **半自动**：系统只负责创建 DR + 预填 artifacts，TR 生成和审批由用户完成
- **两步流程**：Preview → 用户选择 → Create（不是一步到位）
- **始终新建 DR**：即使之前已从同一 snapshot 创建过 DR，每次都新建独立的 DR
- **重复检测 + 用户决定**：检测已在活跃 DR 中的 artifacts，标记为重复，由用户在 Dialog 中决定是否纳入

### 1.4 VC-DR 关联

- `DeliveryRequest` 新增 `VersionCompareSnapshotID *uint` nullable FK
- 手动创建的 DR 为 nil，自动创建的 DR 指向 snapshot
- 一个 snapshot 可关联多个 DR（一对多）
- 排除记录**不持久化**，仅在 API 响应中瞬态返回

---

## 2. 现有系统分析

### 2.1 Version Compare 数据源

Version Compare 的缓存数据（`VersionCompareSnapshot.Data`）中包含所有信息：

```go
type SnapshotData struct {
    SourceTenantID  uint              `json:"sourceTenantID"`
    ComparedTenants []uint            `json:"comparedTenants"`
    Packages        []PackageSnapshot `json:"packages"`
}

type ArtifactSnapshot struct {
    ID       string                        `json:"id"`
    Name     string                        `json:"name"`
    Type     string                        `json:"type"`
    Versions map[uint]ArtifactVersionInfo  `json:"versions"`
}

type ArtifactVersionInfo struct {
    DesignTimeVersion string `json:"designTimeVersion"`
    // ... runtime, modifiedBy/At, etc.
}
```

**可以直接从 snapshot 数据中提取**：
- Source tenant 的 DT 版本
- 每个 target tenant 的 DT 版本
- 是否 DRAFT（`DesignTimeVersion == "Active"`，case-insensitive）
- Artifact 元数据（ID, Name, Type, PackageID）

### 2.2 Delivery Request 创建流程

现有 DR 创建需要：

1. **CreateDr**：`POST /api/v1/deliveryRequest` — 需要 `Name`, `DeliveryRule.ID`, 可选 `JiraLink`
2. **InsertTenantOps**：`POST /api/v1/deliveryRequest/insertOps` — 需要每个 op 携带 `Artifact`, `TenantID`, `TransportRequestNumber`

**问题**：`InsertTenantOps` 当前**强制要求** `TransportRequestNumber` 非空且通过 `TrExist` 校验。但在自动创建 DR 时，artifacts 还没有 TR。

### 2.3 需要修改的核心逻辑

| 组件 | 当前行为 | 需要修改为 |
|------|---------|-----------|
| `InsertTenantOps` | TR 为空时 `TrExist` 报错 | TR 为空时跳过 `TrExist` |
| `UpdateTenantOps` | TR 变更时校验 `TrExist` | TR 从空变为非空时校验 `TrExist` |
| `Approve` | 不校验 TR | **新增**：校验所有 ops 的 TR 合法性 |
| `RequestApproval` | 不校验 TR | **新增**：校验所有 ops 的 TR 合法性 |
| `DeliveryRequest` model | 无 VC 关联 | 新增 `VersionCompareSnapshotID *uint` |

---

## 3. 核心设计决策

### 3.1 两步流程：Preview + Create

**Preview API** 负责分析 snapshot 数据并返回分类结果，让用户在 Dialog 中做出选择。
**Create API** 接收用户明确选择的 artifact 列表，创建 DR + 预填 ops。

分两步的原因：
- 用户需要看到重复检测结果并做出决定
- 版本模式检查和 DRAFT 检测结果需要展示给用户
- 用户可能想排除某些 artifacts

### 3.2 InsertTenantOps — TR Optional

修改 `InsertTenantOps`，当 `TransportRequestNumber` 为空时跳过 `TrExist` 校验：

```go
// 修改后
if op.TransportRequestNumber != "" {
    if _, err := s.TrExist(op, &sourceTenant); err != nil {
        errOps[op.ID] = ...
        continue
    }
}
```

**向后兼容**：现有前端始终提供 TR，行为不变。新 flow 可以不提供 TR。

### 3.3 Approve 阶段新增 TR 校验

在 `Approve` 和 `RequestApproval` 中直接调用已有的 `BatchTrExist`（无需额外封装函数）：
- `BatchTrExist` → `TrExist` 内部已检查 `TransportRequestNumber == ""`
- 批量校验 TR 合法性（存在性、RELEASED 状态、source node 匹配、content 匹配）

**安全保障**：无论 ops 是手动还是自动创建的，审批前都必须有合法 TR。

### 3.4 DRAFT 排除策略

Preview 阶段，遍历 snapshot 数据时：

```go
sourceVersion := art.Versions[sourceTenantID]
if strings.EqualFold(sourceVersion.DesignTimeVersion, "active") {
    // 标记为 draft，不放入 includable 列表
}
```

DRAFT artifacts 在 Dialog 中**显示但不可勾选**（disabled），附 DRAFT 标签。

### 3.5 Mismatch 判断逻辑

一个 artifact 被判定为 DT mismatch 的条件：
1. Source tenant 有该 artifact 的 DT 版本（`sourceHasData == true`）— 如果 source 无数据则跳过
2. **至少一个** target tenant 的 DT 版本与 source 不同（`targetDT != sourceDT`，string equality）
3. Target tenant 的 DT 版本可以为空（意味着该 artifact 在 target 上不存在，视为 mismatch）

被判定为 mismatch 后，进一步分类（**按优先级顺序**，互斥）：
1. **DRAFT** → source DT 是 `"Active"` (case-insensitive) — 最高优先级
2. **Version Pattern 不匹配** → source DT 版本不匹配 rule 的 `VersionPattern`
3. **Duplicate** → 已在同一 Delivery Rule 的活跃 DR 中（状态不是 `CANCELED` 和 `DEPLOYED`）
4. **Includable** → 以上都不是，可以纳入 DR

**注意**：分类是互斥的。一个 artifact 同时满足 DRAFT 和 Duplicate 时，只显示为 DRAFT（因为 DRAFT 是不可操作的，无需再提示重复）。同理，满足 Version Pattern 不匹配的 artifact 即使也在活跃 DR 中，也只显示为 Version Pattern 不匹配。

### 3.6 Version Pattern Check

`checkVersionPattern` 在 **Preview 阶段** 执行（轻量，纯 glob 匹配）。
不匹配的 artifacts 在 Dialog 中**显示但不可勾选**。

### 3.7 Version Downgrade Check

`checkVersionDowngradeInTenant` 在 **Create 阶段** 执行（重量，需实时调 CPI API）。
降级检查失败的 artifacts 不会被纳入 DR，错误信息在 Create 响应中返回。

### 3.8 重复检测

在 **Preview 阶段**：
1. 查询同一 Delivery Rule 下所有活跃 DR（状态 != `CANCELED` 且 != `DEPLOYED`）
2. 收集这些 DR 中所有 ops 的 `(ArtifactTechID, ArtifactVersion)` 组合
3. 对于每个 mismatch artifact，检查是否已在活跃 DR 中
4. 标记为 `duplicate`，包含关联的 DR ID 和名称
5. Dialog 中 duplicate artifacts 默认**未勾选**，但用户可以主动勾选纳入

### 3.9 VC-DR 关联

`DeliveryRequest` 新增 `VersionCompareSnapshotID *uint`：
- 自动创建的 DR 设置此字段指向 snapshot
- 手动创建的 DR 此字段为 nil
- 可以查询同一 snapshot 产生的所有 DR

### 3.10 Snapshot 数据一致性（防过期）

`VersionCompareSnapshot` 采用 upsert 模式（每个 Rule 仅一条记录，`uniqueIndex`）。重新 trigger scan 时同一条记录被原地更新，`ID` 不变但 `Data` 变了。这意味着仅靠 `snapshotID` 无法检测 Preview 到 Create 之间数据是否已变更。

**解决方案**：Preview 额外返回 `snapshotCompletedAt`（snapshot 的 `CompletedAt` 时间戳），Create 时回传 `snapshotID` + `snapshotCompletedAt`。后端校验两者都匹配，如果不匹配（说明中间被重新 trigger 了），返回 409 Conflict。

---

## 4. API 设计

### 4.1 Preview API

#### `GET /api/v1/deliveryRule/:id/versionCompare/previewDR`

**请求体**：无（GET 无 body）

Preview 返回全量 DT mismatch artifacts 的分类列表。Package 级别的过滤由前端 Dialog 处理（按 packageID 分组展示，用户勾选/取消 artifacts）。

**成功响应** (200)：

```json
{
  "status": "success",
  "code": 200,
  "result": {
    "snapshotID": 42,
    "snapshotCompletedAt": "2026-03-12T14:30:00Z",
    "ruleName": "Release 5.2",
    "requireJira": true,
    "artifacts": [
      {
        "artifactID": "MyIflow_A",
        "artifactName": "My Integration Flow A",
        "packageID": "Package_X",
        "type": "Integration Flow",
        "sourceVersion": "5.2.10",
        "category": "includable"
      },
      {
        "artifactID": "MyIflow_B",
        "artifactName": "My Integration Flow B",
        "packageID": "Package_X",
        "type": "Integration Flow",
        "sourceVersion": "Active",
        "category": "draft"
      },
      {
        "artifactID": "MyIflow_C",
        "artifactName": "My Integration Flow C",
        "packageID": "Package_Y",
        "type": "Script Collection",
        "sourceVersion": "6.1.3",
        "category": "versionPattern",
        "reason": "version 6.1.3 does not match pattern 5.2.*"
      },
      {
        "artifactID": "MyIflow_D",
        "artifactName": "My Integration Flow D",
        "packageID": "Package_X",
        "type": "Integration Flow",
        "sourceVersion": "5.2.8",
        "category": "duplicate",
        "existingDR": {
          "id": 99,
          "name": "Auto DR - Rule 1 - 2026-03-10"
        }
      }
    ],
    "summary": {
      "totalMismatch": 4,
      "includable": 1,
      "draft": 1,
      "versionPattern": 1,
      "duplicate": 1
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `snapshotID` | 当前 snapshot 的 DB ID，Create 时需要回传 |
| `snapshotCompletedAt` | Snapshot 完成时间戳，Create 时需要回传（用于防过期检测） |
| `ruleName` | Delivery Rule 名称（前端展示用） |
| `requireJira` | Rule 是否要求 JIRA link |
| `artifacts` | 所有 DT mismatch artifacts 的分类列表 |
| `artifacts[].category` | 分类：`includable` / `draft` / `versionPattern` / `duplicate` |
| `artifacts[].reason` | `versionPattern` 和 `duplicate` 时附带原因说明 |
| `artifacts[].existingDR` | `duplicate` 时附带已存在的活跃 DR 信息 |
| `summary` | 各分类的计数统计 |

**错误响应**：

| HTTP Code | 场景 |
|-----------|------|
| 404 | Delivery Rule 不存在、Version Compare 无已完成的 snapshot |
| 409 | 没有 DT mismatch artifacts（所有 tenant 版本一致） |

### 4.2 Create API

#### `POST /api/v1/deliveryRule/:id/versionCompare/createDR`

**请求体**：

```json
{
  "name": "Auto DR from Version Compare - 2026-03-12",
  "jiraLink": "https://jira.tools.sap/browse/PROJ-123",
  "snapshotID": 42,
  "snapshotCompletedAt": "2026-03-12T14:30:00Z",
  "artifactKeys": [
    {"artifactID": "MyIflow_A", "packageID": "Package_X"},
    {"artifactID": "MyIflow_D", "packageID": "Package_X"}
  ]
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 否 | DR 名称。空则后端自动生成 |
| `jiraLink` | string | 视 Rule | Rule 要求 JIRA 时必填 |
| `snapshotID` | uint | 是 | Preview 返回的 snapshot ID |
| `snapshotCompletedAt` | time | 是 | Preview 返回的 snapshot 完成时间，用于防过期检测 |
| `artifactKeys` | []ArtifactKey | 是 | 用户选择的 artifacts（由 artifactID + packageID 唯一标识） |

`artifactKeys` 允许用户精确控制哪些 artifacts 纳入 DR。用户在 Dialog 中可以：
- 取消勾选某些 `includable` artifacts
- 勾选某些 `duplicate` artifacts（即使有重复警告也想纳入）

**成功响应** (200)：

```json
{
  "status": "success",
  "code": 200,
  "result": {
    "deliveryRequest": {
      "ID": 123,
      "Name": "Auto DR from Version Compare - 2026-03-12",
      "AggregateStatus": "PENDING",
      "VersionCompareSnapshotID": 42,
      "ArtifactTenantOperations": [
        {
          "ArtifactTechID": "MyIflow_A",
          "ArtifactVersion": "5.2.10",
          "TransportRequestNumber": "",
          "RequestState": "NOT_REQUESTED",
          "ImportState": "NOT_STARTED",
          "DeployState": "NOT_STARTED"
        }
      ]
    },
    "summary": {
      "requested": 2,
      "created": 2,
      "errors": []
    }
  }
}
```

| 响应字段 | 说明 |
|---------|------|
| `deliveryRequest` | 创建的 DR 对象（含 ops，ops 的 TR 为空） |
| `summary.requested` | 用户请求纳入的 artifact 数量 |
| `summary.created` | 实际成功纳入的数量（version downgrade check 可能排除部分） |
| `summary.errors` | 因降级检查等原因在创建阶段失败的 artifact 列表 |

**错误响应**：

| HTTP Code | 场景 |
|-----------|------|
| 400 | JIRA 校验失败、artifactKeys 为空、snapshotID 无效 |
| 404 | Delivery Rule 不存在、Snapshot 不存在 |
| 409 | Snapshot 数据已变更（`snapshotCompletedAt` 与当前 snapshot 不匹配，说明中间被重新 trigger 了） |
| 500 | 内部错误 |

### 4.3 修改的现有 API 行为

| API | 修改 |
|-----|------|
| `POST /api/v1/deliveryRequest/insertOps` | TR 为空时跳过 TrExist |
| `PUT /api/v1/deliveryRequest/updateOps` | TR 从空变为非空时校验 TrExist；TR 保持空时不校验 |
| `POST /api/v1/deliveryRequest/requestApproval` | 新增：校验所有 ops 的 TR 合法性 |
| `POST /api/v1/deliveryRequest/approve` | 新增：校验所有 ops 的 TR 合法性 |

---

## 5. 后端实现计划

### 5.1 Phase 1: DB Model 修改 + 核心 DR 逻辑

#### 5.1.1 `db/delivery.go` — DeliveryRequest 新增 FK

```go
type DeliveryRequest struct {
    // ... 现有字段 ...
    VersionCompareSnapshotID *uint                   `json:"versionCompareSnapshotID,omitempty"`
    VersionCompareSnapshot   *VersionCompareSnapshot `gorm:"foreignKey:VersionCompareSnapshotID" json:"-"`
}
```

注意：JSON 序列化时排除 `VersionCompareSnapshot` 关联对象（太大），只保留 ID。

#### 5.1.2 `service/dr.go` — `InsertTenantOps` 修改

```go
// TR 为空时跳过校验（允许后续补填）
if op.TransportRequestNumber != "" {
    if _, err := s.TrExist(op, &sourceTenant); err != nil {
        errOps[op.ID] = fmt.Errorf("transport request check failed for artifact %s: %s", op.ArtifactTechID, err)
        continue
    }
}
```

#### 5.1.3 `service/dr.go` — `UpdateTenantOps` 修改

```go
if existingOp.TransportRequestNumber != draftOp.TransportRequestNumber {
    // 只在新 TR 非空时校验（从空到非空，或从一个非空到另一个非空）
    if draftOp.TransportRequestNumber != "" {
        if _, err := s.TrExist(draftOp, &sourceTenant); err != nil {
            errOps[draftOp.ID] = ...
            continue
        }
    }
}
```

#### 5.1.4 `service/approve.go` — 新增 TR 校验

直接在 `Approve()` 和 `RequestApproval()` 中调用已有的 `BatchTrExist`，无需额外封装。
`BatchTrExist` → `TrExist` 内部已检查 `TransportRequestNumber == ""`，逻辑零冗余。

```go
// In Approve(), after status/ops checks, before setting approved status:
if _, err := s.BatchTrExist(dr.ArtifactTenantOperations, &dr.SourceTenant); err != nil {
    return nil, err
}

// In RequestApproval(), after loading DR with associations and status checks:
if _, err := s.BatchTrExist(dr.ArtifactTenantOperations, &dr.SourceTenant); err != nil {
    return err
}
```

注意：`RequestApproval()` 需要改为使用 `QueryDrWithAssociations` 加载 DR（原来是 `DB.First`），以获取 `ArtifactTenantOperations` 和 `SourceTenant`。

在 `Approve()` 中，在现有 status/ops 校验之后、设置 approved 状态之前调用。
在 `RequestApproval()` 中，加载 DR with associations 后调用。

### 5.2 Phase 2: Preview + Create Service 函数

#### 5.2.1 类型定义

```go
// --- Preview ---

// PreviewDRRequest — 无字段，Preview 返回全量 mismatch artifacts
// Package 级别过滤由前端 Dialog 处理

type PreviewDRArtifact struct {
    ArtifactID    string            `json:"artifactID"`
    ArtifactName  string            `json:"artifactName"`
    PackageID     string            `json:"packageID"`
    Type          string            `json:"type"`
    SourceVersion string            `json:"sourceVersion"`
    Category      string            `json:"category"` // "includable" | "draft" | "versionPattern" | "duplicate"
    Reason        string            `json:"reason,omitempty"`
    ExistingDR    *ExistingDRInfo   `json:"existingDR,omitempty"`
}

type ExistingDRInfo struct {
    ID   uint   `json:"id"`
    Name string `json:"name"`
}

type PreviewDRResponse struct {
    SnapshotID          uint                `json:"snapshotID"`
    SnapshotCompletedAt time.Time           `json:"snapshotCompletedAt"`
    RuleName            string              `json:"ruleName"`
    RequireJira         bool                `json:"requireJira"`
    Artifacts           []PreviewDRArtifact `json:"artifacts"`
    Summary             PreviewDRSummary    `json:"summary"`
}

type PreviewDRSummary struct {
    TotalMismatch  int `json:"totalMismatch"`
    Includable     int `json:"includable"`
    Draft          int `json:"draft"`
    VersionPattern int `json:"versionPattern"`
    Duplicate      int `json:"duplicate"`
}

// --- Create ---

type ArtifactKey struct {
    ArtifactID string `json:"artifactID"`
    PackageID  string `json:"packageID"`
}

type CreateDRFromMismatchRequest struct {
    Name                string        `json:"name"`
    JiraLink            string        `json:"jiraLink"`
    SnapshotID          uint          `json:"snapshotID"`
    SnapshotCompletedAt time.Time     `json:"snapshotCompletedAt"`
    ArtifactKeys        []ArtifactKey `json:"artifactKeys"`
}

type CreateDRFromMismatchResponse struct {
    DeliveryRequest db.DeliveryRequest       `json:"deliveryRequest"`
    Summary         CreateDRFromMismatchSummary `json:"summary"`
}

type CreateDRFromMismatchSummary struct {
    Requested int                 `json:"requested"`
    Created   int                 `json:"created"`
    Errors    []MismatchSkipError `json:"errors"`
}

type MismatchSkipError struct {
    ArtifactID string `json:"artifactID"`
    Reason     string `json:"reason"`
}
```

#### 5.2.2 Preview 函数 `PreviewDRFromMismatch`

```go
func (s *Service) PreviewDRFromMismatch(ruleID uint) (PreviewDRResponse, error) {
    // 1. 加载 delivery rule
    rule, err := s.GetDeliveryRuleWithAcc(ruleID)

    // 2. 加载 completed snapshot
    var snapshot db.VersionCompareSnapshot
    // WHERE delivery_rule_id = ruleID AND status = 'completed'
    // 如果不存在 → 404

    // 3. 查询活跃 DR 的 ops（重复检测）
    var activeDRs []db.DeliveryRequest
    // WHERE delivery_rule_id = ruleID AND aggregate_status NOT IN ('CANCELED', 'DEPLOYED')
    // Preload ArtifactTenantOperations
    // 构建 existingOps map: (artifactTechID, artifactVersion) → DR info

    // 4. 遍历 snapshot.Data.Packages → Artifacts（全量，不做 package 过滤）
    for _, pkg := range snapshot.Data.Packages {
        for _, art := range pkg.Artifacts {
            sourceVI, sourceHasData := art.Versions[snapshot.Data.SourceTenantID]

            // 4a. 跳过 source 无数据的 artifact
            if !sourceHasData || sourceVI.DesignTimeVersion == "" {
                continue
            }

            // 4b. 检查是否有 DT mismatch
            hasMismatch := false
            for _, targetID := range snapshot.Data.ComparedTenants {
                targetVI := art.Versions[targetID]
                if targetVI.DesignTimeVersion != sourceVI.DesignTimeVersion {
                    hasMismatch = true
                    break
                }
            }
            if !hasMismatch { continue }

            // 4c. 按优先级分类（互斥）
            if strings.EqualFold(sourceVI.DesignTimeVersion, "active") {
                → category = "draft"
            } else if !checkVersionPattern(version, rule.VersionPattern) {
                → category = "versionPattern", reason = "..."
            } else if existingOps has (art.ID, sourceVI.DesignTimeVersion) {
                → category = "duplicate", existingDR = {id, name}
            } else {
                → category = "includable"
            }
        }
    }

    // 5. 返回分类结果 + summary 统计
    // 包含 snapshotID 和 snapshotCompletedAt（用于 Create 时的防过期校验）
    return PreviewDRResponse{
        SnapshotID:          snapshot.ID,
        SnapshotCompletedAt: *snapshot.CompletedAt,
        RuleName:            rule.Name,
        RequireJira:         rule.RequireJira,
        Artifacts:           artifacts,
        Summary:             summary,
    }, nil
}
```

#### 5.2.3 Create 函数 `CreateDRFromMismatch`

```go
func (s *Service) CreateDRFromMismatch(ruleID uint, req CreateDRFromMismatchRequest, user string) (CreateDRFromMismatchResponse, error) {
    // 1. 加载 delivery rule
    rule, err := s.GetDeliveryRuleWithAcc(ruleID)

    // 2. 加载 snapshot 并校验一致性
    var snapshot db.VersionCompareSnapshot
    // WHERE id = req.SnapshotID AND delivery_rule_id = ruleID AND status = 'completed'
    // 不存在 → 404
    // snapshot.CompletedAt != req.SnapshotCompletedAt → 409 (数据已过期，需重新 Preview)

    // 3. JIRA 校验
    if rule.RequireJira && req.JiraLink == "" { → 400 }
    checkJIRA(req.JiraLink, rule) // 格式校验

    // 4. 自动生成 name（如未提供）
    if req.Name == "" {
        req.Name = fmt.Sprintf("Auto DR - %s - %s", rule.Name, time.Now().Format("2006-01-02 15:04"))
    }

    // 5. 从 snapshot 中查找用户选择的 artifacts
    artifactKeySet := map[ArtifactKey]bool{}
    for _, k := range req.ArtifactKeys { artifactKeySet[k] = true }

    type artifactWithPkg struct {
        art db.ArtifactSnapshot
        pkg db.PackageSnapshot
    }
    var mismatchArtifacts []artifactWithPkg
    for _, pkg := range snapshot.Data.Packages {
        for _, art := range pkg.Artifacts {
            key := ArtifactKey{ArtifactID: art.ID, PackageID: pkg.PackageID}
            if !artifactKeySet[key] { continue }
            mismatchArtifacts = append(mismatchArtifacts, artifactWithPkg{art: art, pkg: pkg})
        }
    }

    // 6. 逐个构建 ops，容错处理（先收集，再决定是否创建 DR）
    var validOps []db.ArtifactTenantOperation
    var skipErrors []MismatchSkipError

    for _, item := range mismatchArtifacts {
        art := item.art
        sourceVI := art.Versions[snapshot.Data.SourceTenantID]

        op := db.ArtifactTenantOperation{
            TenantID:          rule.SourceTenantID,
            ArtifactTechID:    art.ID,
            ArtifactVersion:   sourceVI.DesignTimeVersion,
            Artifact: db.Artifact{
                TechID:    art.ID,
                Version:   sourceVI.DesignTimeVersion,
                Name:      art.Name,
                Type:      db.ArtifactType(art.Type),
                PackageID: item.pkg.PackageID,
            },
            TransportRequestNumber: "", // 空 — 后续补填
        }

        // LoadArtifact (FirstOrCreate)
        a, err := s.LoadArtifact(op)
        if err != nil {
            skipErrors = append(skipErrors, MismatchSkipError{ArtifactID: art.ID, Reason: err.Error()})
            continue
        }
        op.Artifact = a
        op.ArtifactID = a.ID

        // Version downgrade check（容错 — 不 block 其他 artifacts）
        downgradeErr := false
        for _, tenant := range rule.IncludedTenants {
            if tenant.ID == rule.SourceTenantID { continue }
            if err := s.checkVersionDowngradeInTenant(&op, &tenant); err != nil {
                skipErrors = append(skipErrors, MismatchSkipError{ArtifactID: art.ID, Reason: err.Error()})
                downgradeErr = true
                break // 跳出内层 tenant 循环
            }
        }
        if downgradeErr { continue } // 跳过此 artifact

        // 设置初始状态
        op.ImportState = lifecycle.ImportNotStarted
        op.DeployState = lifecycle.DeployNotStarted
        op.RequestState = lifecycle.RequestPending
        op.CreatedBy = user

        validOps = append(validOps, op)
    }

    // 7. 如果没有 valid ops，不创建空 DR，返回错误
    if len(validOps) == 0 {
        return CreateDRFromMismatchResponse{}, fmt.Errorf("no artifacts passed validation checks")
    }

    // 8. 创建 DR（含 VersionCompareSnapshotID）
    dr := db.DeliveryRequest{
        Name:                     req.Name,
        JiraLink:                 req.JiraLink,
        DeliveryRuleID:           rule.ID,
        SourceTenantID:           rule.SourceTenantID,
        AggregateStatus:          lifecycle.AggPending,
        VersionCompareSnapshotID: &snapshot.ID,
        CreatedBy: user, UpdatedBy: user,
    }
    s.DB.Create(&dr)

    // 9. 设置 DeliveryRequestID 并批量创建 ops
    for i := range validOps {
        validOps[i].DeliveryRequestID = dr.ID
    }
    s.DB.Create(&validOps)

    // 10. 返回 DR + summary
    // 重新加载 DR with associations
    drLoaded, _ := s.QueryDrWithAssociations(dr.ID)
    return CreateDRFromMismatchResponse{
        DeliveryRequest: *drLoaded,
        Summary: CreateDRFromMismatchSummary{
            Requested: len(req.ArtifactKeys),
            Created:   len(validOps),
            Errors:    skipErrors,
        },
    }, nil
}
```

**关键设计点**：
- 先收集 validOps，**确认非空后才创建 DR**，避免空 DR 孤儿
- Version downgrade check 使用 `break` + flag 替代 labeled loop
- Snapshot 一致性校验使用 `snapshotID` + `snapshotCompletedAt` 双重检查

### 5.3 Phase 3: Handler + Route 注册

#### 5.3.1 `handler/version_compare.go` — 两个新 Handler

```go
func (h *Handler) HandlePreviewDRFromMismatch(c *gin.Context) {
    ruleID := parseRuleID(c)
    resp, err := h.svc.PreviewDRFromMismatch(uint(ruleID))
    // 错误处理...
    c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": resp})
}

func (h *Handler) HandleCreateDRFromMismatch(c *gin.Context) {
    ruleID := parseRuleID(c)
    var req service.CreateDRFromMismatchRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.AbortWithStatusJSON(http.StatusBadRequest, ...)
        return
    }
    user := service.UserID(c)
    if req.Name == "" {
        req.Name = fmt.Sprintf("Auto DR - Rule %d - %s", ruleID, time.Now().Format("2006-01-02 15:04"))
    }
    resp, err := h.svc.CreateDRFromMismatch(uint(ruleID), req, user)
    // 错误处理...
    c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": resp})
}
```

#### 5.3.2 `handler/handler.go` — 注册路由

```go
v1.GET("/deliveryRule/:id/versionCompare/previewDR",  h.HandlePreviewDRFromMismatch)
v1.POST("/deliveryRule/:id/versionCompare/createDR",  h.HandleCreateDRFromMismatch)
```

---

## 6. 前端 UI 方案

### 6.1 入口位置

在 `VersionCompareDetailView.vue` 的顶部 toolbar 区域，增加一个 "Create Delivery Request" 按钮。

**按钮条件**：
- Snapshot 状态为 `completed`
- 按钮 disabled 条件：无 DT mismatch（前端可从已有 VC 数据初步判断）

### 6.2 两步 Dialog

#### Step 1: Preview（点击按钮后立即调用 Preview API）

```
+--------------------------------------------------------------+
|  Create Delivery Request from Mismatches                     |
+--------------------------------------------------------------+
|                                                                |
|  Rule: Release 5.2          Snapshot: 2026-03-12 14:30        |
|                                                                |
|  --- Artifacts ---                                             |
|                                                                |
|  [Search: _______________]                                     |
|                                                                |
|  ✅ Includable (10)                                            |
|  ┌─────────────────────────────────────────────────────┐      |
|  │ [x] MyIflow_A        | Package_X | IFlow  | 5.2.10 │      |
|  │ [x] MyIflow_E        | Package_X | IFlow  | 5.2.9  │      |
|  │ [x] MySC_F           | Package_Y | SC     | 5.2.5  │      |
|  │ ...                                                  │      |
|  └─────────────────────────────────────────────────────┘      |
|                                                                |
|  ⚠️ Already in Active DR (2) — uncheck to exclude             |
|  ┌─────────────────────────────────────────────────────┐      |
|  │ [ ] MyIflow_D  | Pkg_X | 5.2.8 | → DR #99 "..."   │      |
|  │ [ ] MySC_G     | Pkg_Y | 5.2.4 | → DR #101 "..."  │      |
|  └─────────────────────────────────────────────────────┘      |
|                                                                |
|  ❌ Excluded — DRAFT (1)                                       |
|  ┌─────────────────────────────────────────────────────┐      |
|  │    MyIflow_B          | Package_X | IFlow  | DRAFT  │      |
|  └─────────────────────────────────────────────────────┘      |
|                                                                |
|  ❌ Excluded — Version Pattern (1)                             |
|  ┌─────────────────────────────────────────────────────┐      |
|  │    MyIflow_C | Pkg_Y | SC | 6.1.3 ≠ 5.2.*          │      |
|  └─────────────────────────────────────────────────────┘      |
|                                                                |
|  --- DR Details ---                                            |
|                                                                |
|  Name:      [Auto DR - Release 5.2 - 2026-03-12 15:04]       |
|  JIRA Link: [____________________________] (required)         |
|                                                                |
|                    [Cancel]    [Create (10 artifacts)]          |
+--------------------------------------------------------------+
```

**分组逻辑**：
1. **Includable** — 默认全选（checked），用户可取消
2. **Duplicate** — 默认未选（unchecked），用户可勾选
3. **DRAFT** — 不可选（disabled），灰色显示
4. **Version Pattern** — 不可选（disabled），灰色显示

按钮文字动态显示选中数量：`Create (N artifacts)`

#### Step 2: 创建结果

调用 Create API 后，Dialog 内容切换为结果展示：

```
+--------------------------------------------------------------+
|  Delivery Request Created                                     |
+--------------------------------------------------------------+
|                                                                |
|  ✅ DR #123 "Auto DR - Release 5.2 - 2026-03-12 15:04"      |
|                                                                |
|  Created: 10 artifacts                                        |
|  Errors: 0                                                     |
|                                                                |
|  (如有 errors，显示列表)                                        |
|                                                                |
|  Next: Go to DR detail page to generate Transport Requests    |
|                                                                |
|        [Close]    [Go to Delivery Request →]                   |
+--------------------------------------------------------------+
```

### 6.3 DR 详情页的适配

现有 DR 详情页（`DeliveryRequestView.vue`）需要适配 TR 为空的 ops：

1. **显示 TR 状态**：对于 `TransportRequestNumber == ""` 的 ops，显示 "TR Required" 标签（`design="Negative"`）
2. **Generate TRs 按钮**：扩展为支持已保存但 TR 为空的 source ops（当前只支持 `addOps`）
3. **Update 按钮**：有 TR 为空的已保存 ops 时也允许 Update（通过 `UpdateOps` 更新 TR）
4. **Request Approval**：后端拦截（`BatchTrExist`），前端也在所有 ops 有 TR 时才 enable

---

## 7. 执行阶段

### Phase 1: DB Model + 核心 DR 逻辑修改 — ✅ COMPLETE

**范围**：Backend only
**状态**：已完成，commit `e28203e`（2025-06-xx）
**变更文件**：
- `db/delivery.go` — `DeliveryRequest` 新增 `VersionCompareSnapshotID *uint` + FK 关联
- `service/dr.go` — `InsertTenantOps`: TR 为空时跳过 `TrExist`
- `service/dr.go` — `UpdateTenantOps`: TR 为空时跳过 `TrExist`
- `service/approve.go` — `Approve` + `RequestApproval` 中直接调用 `BatchTrExist`
- `service/approve.go` — `RequestApproval` 重构为使用 `QueryDrWithAssociations` 加载关联数据

**备注**：
- AutoMigrate 无需额外修改（`DeliveryRequest` 已在 `db/conn.go` 中注册）
- 原设计中的 `validateAllOpsHaveTR` 包装函数经代码审查后移除，改为直接调用 `BatchTrExist`（因 `TrExist` 内部已处理空 TR 检查）

### Phase 2: Preview + Create Service 实现 — ⏳ TODO

**范围**：Backend

1. `service/version_compare.go` — 新增所有类型定义
2. `service/version_compare.go` — 实现 `PreviewDRFromMismatch`
3. `service/version_compare.go` — 实现 `CreateDRFromMismatch`
4. `handler/version_compare.go` — 两个新 Handler
5. `handler/handler.go` — 注册路由

### Phase 3: 后端测试 — ⏳ TODO

**范围**：Backend

1. Phase 1 修改的测试（TR optional、approve validation）
2. Preview 和 Create 的测试

### Phase 4: 前端 — Preview Dialog + Create Flow — ⏳ TODO

**范围**：Frontend

1. `src/service/api.ts` — 新增 API 函数和类型
2. `src/service/model.ts` — 更新 DeliveryRequest 类型（新增 VersionCompareSnapshotID）
3. `src/views/VersionCompareDetailView.vue` — 按钮 + Dialog + 两步流程

### Phase 5: 前端 — DR 详情页适配 — ⏳ TODO

**范围**：Frontend

1. `src/views/DeliveryRequestView.vue` — 适配 TR 为空的 ops
2. Generate TRs 扩展支持空 TR source ops
3. Request Approval disable 逻辑

---

## 8. 测试计划

### 8.1 后端测试

| 测试用例 | Phase | 描述 |
|---------|-------|------|
| `TestInsertTenantOps_EmptyTR` | 1 | TR 为空时 InsertOps 成功 |
| `TestInsertTenantOps_WithTR` | 1 | TR 非空时仍校验 TrExist（向后兼容） |
| `TestUpdateTenantOps_EmptyToNonEmpty` | 1 | TR 从空更新为非空，校验 |
| `TestUpdateTenantOps_EmptyToEmpty` | 1 | TR 保持空，跳过校验 |
| `TestApprove_MissingTR` | 1 | 有 ops 缺 TR → 拒绝 |
| `TestApprove_AllTRPresent` | 1 | 所有 ops 有 TR → 通过 |
| `TestRequestApproval_MissingTR` | 1 | 提交审批时缺 TR → 拒绝 |
| `TestPreviewDR_Basic` | 2 | 有 mismatch → 返回分类列表 |
| `TestPreviewDR_DraftDetection` | 2 | DRAFT artifacts 标记为 draft |
| `TestPreviewDR_VersionPatternDetection` | 2 | 版本不匹配标记为 versionPattern |
| `TestPreviewDR_DuplicateDetection` | 2 | 已在活跃 DR 中的标记为 duplicate |
| `TestPreviewDR_NoMismatch` | 2 | 无 mismatch → 409 |
| `TestPreviewDR_NoSnapshot` | 2 | 无 snapshot → 404 |
| `TestCreateDR_Basic` | 2 | 选中 artifacts → 创建 DR + ops |
| `TestCreateDR_SnapshotMismatch` | 2 | snapshotID 不匹配 → 409 |
| `TestCreateDR_SnapshotStale` | 2 | snapshotCompletedAt 不匹配（中间被重新 trigger）→ 409 |
| `TestCreateDR_VersionDowngradeSkip` | 2 | 降级检查失败 → 容错，返回 errors |
| `TestCreateDR_EmptyArtifactKeys` | 2 | 空 artifactKeys → 400 |
| `TestCreateDR_JiraRequired` | 2 | Rule 要求 JIRA 但未提供 → 400 |
| `TestCreateDR_SnapshotFKSet` | 2 | 创建的 DR 的 VersionCompareSnapshotID 正确设置 |
| `TestCreateDR_DuplicateIncluded` | 2 | 用户选择了 duplicate artifact → 正常纳入 |

### 8.2 前端手动测试

- [ ] Version Compare 详情页显示 "Create Delivery Request" 按钮
- [ ] 点击按钮 → 调用 Preview API → Dialog 显示分类列表
- [ ] Includable artifacts 默认全选
- [ ] Duplicate artifacts 默认未选，可手动勾选
- [ ] DRAFT / Version Pattern artifacts 不可选（disabled）
- [ ] Create 按钮文字显示选中数量
- [ ] 填写 name + JIRA → Create → 显示结果
- [ ] "Go to Delivery Request" 跳转正确
- [ ] DR 详情页正确显示 TR 为空的 ops
- [ ] Generate TRs 对空 TR ops 正常工作
- [ ] Request Approval 在有空 TR 时被阻止
