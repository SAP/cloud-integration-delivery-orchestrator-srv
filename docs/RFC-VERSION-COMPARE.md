# RFC: Cross-Tenant Artifact Version Compare

## 目录

1. [需求概述](#1-需求概述)
2. [现有代码分析](#2-现有代码分析)
3. [API 设计](#3-api-设计)
4. [DB 模型设计](#4-db-模型设计)
5. [后端实现计划](#5-后端实现计划)
6. [前端 UI 方案](#6-前端-ui-方案)
7. [执行阶段](#7-执行阶段)

---

## 1. 需求概述

以 Delivery Rule 为单位，比较其 Source Tenant 与各 Included Tenant 中 artifact 的版本一致性。

### 1.1 核心需求

- 用户指定一个 **Delivery Rule ID**，后端自动计算出 Source Tenant 和 Included Tenants
- 异步触发版本快照采集（覆盖该 Rule 的所有 Package、所有 Tenant）
- 查询时通过参数过滤 Package、Design Time / Runtime、仅显示不匹配项等
- 返回结果以 **Package 为分组单位**，展示每个 artifact 在各 tenant 上的版本对比

### 1.2 版本的两种状态

| 类型 | 含义 | CPI API |
|------|------|---------|
| Design Time | artifact 的设计时版本（已上传但不一定部署） | `GetPackageIflows(packageID)` / `GetPackageScriptcollections(packageID)` — 返回当前 active 版本 |
| Runtime | artifact 的运行时版本（已部署运行） | `GetRuntimeArtifacts()` — 批量获取所有 runtime artifacts |

一个 artifact 在同一个 tenant 上可能 Design Time = 1.2.3 但 Runtime = 1.2.2（设计已更新但尚未部署）。

### 1.3 Design Time "active" 版本说明

CPI API 中 `GetPackageIflows` 返回的版本字段，当值为 `"active"` 时，表示该 artifact 处于 **DRAFT 状态**（尚未保存为正式版本号）。在响应中需标记为 `designTimeDraft: true`，方便前端做特殊展示。

---

## 2. 现有代码分析

### 2.1 Delivery Rule → Tenant 关系（已有逻辑）

`DeliveryRule` model (`db/delivery.go:92-112`) 已包含:
- `SourceTenantID` / `SourceTenant` — 源 tenant（由 `service/dr.go:SourceAndRoute()` 计算）
- `IncludedTenants []CpiTenant` — 参与 delivery 的 tenant 列表（many2many）

**结论**: Source Tenant 和 Included Tenants 的计算逻辑 **已存在**，直接从 DB 加载即可，无需重新计算。

### 2.2 Package/Artifact 获取（已有方法）

`CpiClient` (`pkg/cpi/cpi.go`) 已有:
- `GetPackageIflows(ctx, packageID) → []IflowItem` — 获取 package 内所有 Integration Flow
- `GetPackageScriptcollections(ctx, packageID) → []ScriptCollectionItem` — 获取 package 内所有 Script Collection
- `RuntimeArtifact(ctx, artifactId) → RuntimeArtifact` — 获取单个 runtime artifact
- `GetRuntimeArtifacts(ctx) → []RuntimeArtifact` — 获取所有 runtime artifacts（批量，效率更高）

**注意**: CPI API 按 artifact 类型分接口，无法一次调用获取 package 内所有类型的 artifact。需分别调用 `GetPackageIflows` 和 `GetPackageScriptcollections`，然后合并结果。

### 2.3 已有版本比较逻辑

- `service/checks.go:44-76` — `checkVersionDowngradeInTenant`: 逐个 artifact 比较 source vs target 的 design-time 版本，使用 `semver.Compare`
- `pkg/cpi/tenant_compare.go` — **空桩文件**，有 `ArtifactVersion` struct 和未实现的 `compareTenantVersions`

### 2.4 IntegrationService 接口缺口

当前 `IntegrationService` (`service/service.go:29-34`) **未包含**本 feature 所需的方法:

```go
// 当前接口
type IntegrationService interface {
    DeployArtifact(...)
    RuntimeArtifact(...)
    GetDesignTimeIflow(...)
    GetDesignTimeScriptCollection(...)
}

// 需要新增
    GetPackageIflows(ctx context.Context, packageID string) ([]cpi.IflowItem, error)
    GetPackageScriptcollections(ctx context.Context, packageID string) ([]cpi.ScriptCollectionItem, error)
    GetRuntimeArtifacts(ctx context.Context) ([]cpi.RuntimeArtifact, error)
```

### 2.5 JSON 序列化模式

项目中已有 `gorm:"serializer:json"` 的使用先例（`DeliveryRule.TargetNodes`），可复用此模式存储快照数据。

### 2.6 Per-Tenant Client 工厂

项目使用 `IntegrationFactory func(ctx, tenant) (IntegrationService, error)` 模式为每个 tenant 创建独立的 CPI client（`pkg/cpi/manager.go` 提供线程安全缓存）。

---

## 3. API 设计

采用 **异步触发 + 缓存查询** 模型。原因：部分 Delivery Rule 涉及 20+ 个 CPI Tenant，同步调用会导致请求超时。

### 3.1 Trigger 端点

```
POST /api/v1/deliveryRule/:id/versionCompare/trigger
```

- **无 Request Body** — 触发后自动采集该 Rule 下所有 Package 在所有 Tenant 上的全部 artifact 版本
- 异步执行，立即返回触发状态（不返回快照数据，快照数据由 Query 端点负责）
- **Rate Limiting**: 每个 Rule 有可配置的冷却间隔（如 30 分钟），冷却期内重复触发仅返回 rate limiting 提示信息，引导用户通过 GET 端点查询已有快照

#### Response (触发成功)

```json
{
  "status": "running",
  "triggeredAt": "2026-03-10T10:00:00Z",
  "triggeredBy": "user@example.com",
  "message": "Version compare triggered successfully"
}
```

#### Response (冷却期内 — 429 Too Many Requests)

```json
{
  "status": "rate_limited",
  "lastTriggeredAt": "2026-03-10T09:45:00Z",
  "cooldownRemaining": "15m",
  "message": "Cooldown active, use GET /versionCompare to query existing snapshot"
}
```

### 3.2 Query 端点

```
GET /api/v1/deliveryRule/:id/versionCompare
```

#### Query Parameters

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `packageIDs` | `[]string` | 否 | 全部 | 过滤指定 Package（逗号分隔） |
| `designTime` | `bool` | 否 | `true` | 是否包含 Design Time 版本信息 |
| `runTime` | `bool` | 否 | `true` | 是否包含 Runtime 版本信息 |
| `mismatchOnly` | `bool` | 否 | `false` | 仅显示存在版本不匹配的 artifact |

**工作流程**: 从 DB 读取 raw 快照数据 → 实时计算 match 状态 → 应用过滤条件 → 返回就绪响应。

#### Response Body

```json
{
  "snapshot": {
    "status": "completed",
    "triggeredAt": "2026-03-10T10:00:00Z",
    "triggeredBy": "user@example.com",
    "completedAt": "2026-03-10T10:01:30Z"
  },
  "sourceTenant": {
    "ID": 1,
    "CreatedAt": "...",
    "UpdatedAt": "...",
    "name": "source-tenant",
    "host": "source.hci.sap.com",
    "...": "... (完整 db.CpiTenant 实体)"
  },
  "comparedTenants": [
    { "ID": 2, "name": "tenant-a", "host": "a.hci.sap.com", "...": "..." },
    { "ID": 3, "name": "tenant-b", "host": "b.hci.sap.com", "...": "..." }
  ],
  "packages": [
    {
      "packageID": "PackageA",
      "artifacts": [
        {
          "id": "FlowA1",
          "name": "Flow A1",
          "type": "iflow",
          "source": {
            "designTimeVersion": "1.2.3",
            "designTimeDraft": false,
            "runTimeVersion": "1.2.3",
            "runTimeStatus": "STARTED"
          },
          "tenants": {
            "2": {
              "designTimeVersion": "1.2.2",
              "designTimeDraft": false,
              "designTimeMatch": false,
              "runTimeVersion": "1.2.3",
              "runTimeMatch": true,
              "runTimeStatus": "STARTED"
            },
            "3": {
              "designTimeVersion": "active",
              "designTimeDraft": true,
              "designTimeMatch": false,
              "runTimeVersion": "",
              "runTimeMatch": false,
              "runTimeStatus": "",
              "error": "artifact not deployed"
            }
          }
        }
      ]
    }
  ]
}
```

字段说明:
- `snapshot`: 快照元信息（状态、触发时间等）
- `sourceTenant`: **完整的 `db.CpiTenant` 实体**（包含 ID、name、host 等所有字段）
- `comparedTenants`: 完整的 `db.CpiTenant` 实体数组
- `source`: Source Tenant 上该 artifact 的版本信息
- `tenants`: key 为 tenant ID（string），value 为该 tenant 上的版本 + 是否与 source 匹配
- `designTimeDraft`: `true` 表示 Design Time 版本为 "active"（DRAFT 状态，未保存为正式版本号）
- `*Match`: `true` 表示版本与 source **字符串完全一致**（在 GET 请求时实时计算）
- `error`: 当获取失败时填入错误信息（如 artifact 不存在于该 tenant）
- 当 `designTime=false` 时，design-time 相关字段省略；`runTime=false` 同理
- `runTimeStatus`: Runtime artifact 的状态（STARTED/STARTING/ERROR），仅当 `runTime=true` 时填充
- `mismatchOnly=true` 时，仅返回至少有一个 tenant 版本不匹配的 artifact

### 3.3 错误场景

| 场景 | HTTP Status | 处理 |
|------|-------------|------|
| Delivery Rule 不存在 | 404 | 返回错误消息 |
| 无快照数据（从未触发过） | 200 | 返回 `snapshot.status = "none"`，packages 为空 |
| 快照正在采集中 | 200 | 返回 `snapshot.status = "running"`，packages 为空或返回上次完成的数据 |
| 快照采集失败 | 200 | 返回 `snapshot.status = "failed"` + error 信息 |
| 某个 Package 在 source tenant 不存在 | 200 | 该 package 的 artifacts 为空数组 |
| 某个 artifact 在 target tenant 不存在 | 200 | 该 tenant 的 version 为空，error 说明 |
| CPI API 调用失败 | 200 | 对应 tenant 的 error 字段记录错误，不阻断其他 tenant |

---

## 4. DB 模型设计

### 4.1 VersionCompareSnapshot 表

每个 Delivery Rule **至多一条记录**（upsert 模式），存储最近一次版本快照的原始数据。

```go
type VersionCompareSnapshot struct {
    gorm.Model
    DeliveryRuleID uint         `gorm:"uniqueIndex"`
    Status         string       // "running" | "completed" | "failed"
    TriggeredAt    time.Time
    CompletedAt    *time.Time
    TriggeredBy    string
    Data           SnapshotData `gorm:"serializer:json"`
    Error          string
}
```

### 4.2 SnapshotData 结构（JSON 序列化存储）

DB 只存储 **原始版本数据**，不存储 match 计算结果，不存储请求参数。

```go
type SnapshotData struct {
    SourceTenantID  uint                `json:"sourceTenantID"`
    ComparedTenants []uint              `json:"comparedTenants"`  // tenant IDs
    Packages        []PackageSnapshot   `json:"packages"`
}

type PackageSnapshot struct {
    PackageID string             `json:"packageID"`
    Artifacts []ArtifactSnapshot `json:"artifacts"`
}

type ArtifactSnapshot struct {
    ID       string                        `json:"id"`
    Name     string                        `json:"name"`
    Type     string                        `json:"type"`  // "iflow" | "scriptcollection"
    Versions map[uint]ArtifactVersionInfo  `json:"versions"`  // key = tenant ID (包含 source tenant)
}

type ArtifactVersionInfo struct {
    DesignTimeVersion string `json:"designTimeVersion"`  // "active" 表示 DRAFT
    RuntimeVersion    string `json:"runtimeVersion"`
    RuntimeStatus     string `json:"runtimeStatus"`      // 值应与 consts.RuntimeState 一致: STARTED / STARTING / ERROR
    Error             string `json:"error,omitempty"`
}
```

### 4.3 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 每 Rule 几条记录 | 1 条（upsert） | 只需最新快照，避免历史堆积 |
| Match 计算存 DB？ | 否 | GET 时实时计算，保证过滤逻辑灵活 |
| 请求参数存 DB？ | 否 | Trigger 采集全量数据，Query 时再过滤 |
| Source tenant 版本也存？ | 是 | `Versions` map 包含 source tenant ID 的 entry |
| Tenant 信息存 DB？ | 只存 ID | 完整 tenant 信息查询时从 DB 关联加载 |
| 并发触发防护 | DB 级原子 UPDATE（`WHERE status != 'running'`） | 防止两个请求同时触发同一 Rule 的采集，避免 goroutine 竞态 |
| Goroutine Context | 使用 `context.Background()`，不使用请求 context | 请求返回后 request context 被取消，异步 goroutine 中的 API 调用会因 context.Canceled 失败 |
| Package 列表来源 | Source Tenant 实时 API 调用（`GetPackages`） | 以 Source Tenant 为准；不存在于 Source 上的 Package 不出现在比较结果中 |

---

## 5. 后端实现计划

### 5.1 数据流 — Trigger

```
POST /trigger
  │
  ├─ 1. 加载 DeliveryRule (含 SourceTenant + IncludedTenants)
  │
  ├─ 2. Rate Limit 检查: 最近 N 分钟内是否已有 completed 快照
  │     ├─ 是 → 返回 rate_limited 状态 (429)，不重新触发
  │     └─ 否 → 继续
  │
  ├─ 3. 原子 Upsert VersionCompareSnapshot: status="running"
  │     使用 DB 级别原子操作防止并发触发:
  │     UPDATE ... SET status='running' WHERE delivery_rule_id=? AND status != 'running'
  │     如果 affected rows = 0 → 说明已有正在运行的采集，返回 running 状态 (409 Conflict)
  │
  ├─ 4. 启动 goroutine 异步执行:
  │     │
  │     │  ⚠️ 必须使用独立 context (context.Background())，不能使用请求 context
  │     │  （请求返回后 request context 被取消，会导致所有 CPI API 调用失败）
  │     │
  │     ├─ 4a. 获取 Source Tenant 的所有 Package 列表 (通过 CPI API: GetPackages)
  │     │       注意: Package 列表来自 Source Tenant 的实时 API 调用，
  │     │       不存在于 Source Tenant 上的 Package 不会出现在比较结果中。
  │     │
  │     ├─ 4b. 对每个 Tenant 预取 Runtime 数据 (并发 — errgroup):
  │     │     └─ GetRuntimeArtifacts() → 建 map[artifactID]RuntimeArtifact
  │     │        每个 Tenant 只调用一次，缓存在内存中供后续所有 Package 复用
  │     │
  │     ├─ 4c. 对每个 Package (并发 — errgroup):
  │     │     ├─ 对每个 Tenant (并发 — errgroup):
  │     │     │   ├─ FetchPackageArtifacts(packageID) → []db.Artifact (design time)
  │     │     │   └─ 从 4b 的 Runtime 缓存中按 artifactID 查找 Runtime 版本
  │     │     └─ 合并结果到 ArtifactSnapshot.Versions
  │     │
  │     ├─ 4d. 组装 SnapshotData
  │     │
  │     └─ 4e. 更新 DB: status="completed", Data=snapshotData
  │           (失败时: status="failed", Error=errMsg)
  │
  └─ 5. 立即返回 { status: "running", ... }
```

### 5.2 数据流 — Query

```
GET /versionCompare?packageIDs=...&designTime=true&runTime=true&mismatchOnly=false
  │
  ├─ 1. 从 DB 加载 VersionCompareSnapshot (by DeliveryRuleID)
  │     └─ 不存在 → 返回 status="none"
  │
  ├─ 2. 加载 Source Tenant + Compared Tenants 完整实体 (from DB)
  │
  ├─ 3. 对 SnapshotData 实时计算:
  │     ├─ 对每个 artifact 的每个 tenant:
  │     │   ├─ designTimeMatch = (tenant.designTimeVersion == source.designTimeVersion)
  │     │   └─ runTimeMatch = (tenant.runtimeVersion == source.runtimeVersion)
  │     ├─ designTimeDraft = (designTimeVersion == "active")
  │     │
  │     ├─ 过滤 packageIDs (如有)
  │     ├─ 过滤 designTime/runTime (省略不需要的字段)
  │     └─ 过滤 mismatchOnly (移除全部匹配的 artifact)
  │
  └─ 4. 返回 Response
```

### 5.3 并发策略

- **Package 级别并发**: 多个 package 的数据并行获取
- **Tenant 级别并发**: 同一 package 下多个 tenant 的 CPI API 调用并行
- 使用 `errgroup.Group` 控制并发，设置合理的并发上限
- 使用 `sync.Mutex` 保护结果写入
- **错误容忍**: 单个 tenant/artifact 获取失败时记录 error，不阻断其他 tenant

### 5.4 Runtime 优化

- 对每个 tenant 只调用 **一次** `GetRuntimeArtifacts()` 获取全部 runtime artifacts
- 在内存中建 `map[artifactID]RuntimeArtifact` 索引
- 后续按 artifact ID 查找，避免逐个调用 `RuntimeArtifact(id)`

### 5.5 Artifact 类型合并 — `FetchPackageArtifacts` 中心函数

CPI API 按类型分接口（iflow / script collection），且未来可能新增 adapter、value mapping 等类型。

**现有逻辑**: `handler/cpi_handler.go` 的 `GetPackageArtifactsHandler` 已实现了"调两个 API + `wrapArtifact` 归一化为 `[]db.Artifact`"的逻辑，但它耦合在 handler 层，无法被 service 层复用。

**改造方案**: 将核心逻辑下沉到 service 层，提取为中心函数 `FetchPackageArtifacts`：

```go
// service/artifacts.go

// FetchPackageArtifacts 获取 package 内所有类型的 artifact，返回统一的 []db.Artifact。
// 这是一个中心函数，所有需要"获取 package 全部 artifact"的业务逻辑都应调用此函数。
// 未来新增 artifact 类型（adapter, value mapping 等）时，只需在此处增加一路调用。
func FetchPackageArtifacts(ctx context.Context, client IntegrationService, packageID string) ([]db.Artifact, error) {
    // 并发调用 GetPackageIflows + GetPackageScriptcollections (+ 未来的其他类型)
    // 使用 wrapArtifact 归一化为 db.Artifact
    // 返回 []db.Artifact
}

// wrapArtifact 将 CPI 原始类型 (IflowItem / ScriptCollectionItem) 归一化为 db.Artifact DTO。
// 从 handler/cpi_handler.go 迁移至此。
func wrapArtifact(artifactType consts.ArtifactType, artifact any) db.Artifact { ... }
```

**改造影响**:
- `wrapArtifact` 从 `handler/cpi_handler.go` **迁移到** `service/artifacts.go`
- `GetPackageArtifactsHandler` 改为调用 `service.FetchPackageArtifacts`（行为不变，仅提取逻辑）
- Version Compare 的 trigger 直接调用 `FetchPackageArtifacts` 获取 design time artifact 列表
- `ArtifactSnapshot` 中 artifact 的基础信息（ID、Name、Type、PackageID）直接来自 `db.Artifact`

**扩展性**: 未来新增 artifact 类型时，只需修改 `FetchPackageArtifacts` 一处，所有下游消费者（handler、version compare、其他业务逻辑）自动受益。

### 5.6 文件变更清单

| 文件 | 变更 |
|------|------|
| `service/service.go` | `IntegrationService` 接口新增 3 个方法 |
| `service/artifacts.go` | **新建** — `FetchPackageArtifacts` 中心函数 + `wrapArtifact`（从 handler 迁移） |
| `service/version_compare.go` | **新建** — Trigger + Query service 逻辑 |
| `handler/handler.go` | 注册新路由（2 个端点） |
| `handler/version_compare.go` | **新建** — Trigger + Query handler |
| `handler/cpi_handler.go` | 移除 `wrapArtifact`，`GetPackageArtifactsHandler` 改为调用 `service.FetchPackageArtifacts` |
| `pkg/cpi/tenant_compare.go` | 重写 — 定义 SnapshotData 等数据结构 |
| `db/version_compare.go` | **新建** — `VersionCompareSnapshot` model |
| `db/conn.go` | AutoMigrate 新增 model |
| `service/version_compare_test.go` | **新建** — 单元测试 |

### 5.7 IntegrationService 接口扩展

`IntegrationService` 的定位是 **CPI API 的抽象层**（facade），目的是让 service 层可以 mock CPI 调用进行单元测试。现有方法（`DeployArtifact`、`RuntimeArtifact` 等）同样是对 `CpiClient` 的直接透传，不包含编排逻辑。新增的三个方法性质相同——声明 service 层对 CPI API 的新依赖。

```go
type IntegrationService interface {
    // 现有方法
    DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error)
    RuntimeArtifact(ctx context.Context, artifactId string) (cpi.RuntimeArtifact, error)
    GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (cpi.IflowItem, error)
    GetDesignTimeScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (cpi.ScriptCollectionItem, error)

    // 新增: Version Compare 所需的批量查询能力
    GetPackageIflows(ctx context.Context, packageID string) ([]cpi.IflowItem, error)
    GetPackageScriptcollections(ctx context.Context, packageID string) ([]cpi.ScriptCollectionItem, error)
    GetRuntimeArtifacts(ctx context.Context) ([]cpi.RuntimeArtifact, error)
}
```

---

## 6. 前端 UI 方案

### 6.1 整体导航结构

Version Compare 作为 `/jobs` 分组下的独立 AppCard，和 Delivery Requests 同级。采用**两级页面**结构，与 Delivery Request 的导航模式完全对称：

```
HomeView
  └─ /jobs 分组
       ├─ AppCard "Delivery Requests" → /jobs/delivery-request-list → /delivery-request/:planId
       └─ AppCard "Version Compare"   → /jobs/version-compare       → /jobs/version-compare/:ruleId
```

**HomeView AppCard 信息**:
- 标题: "Version Compare"
- 描述: "Cross-Tenant Artifact Version Comparison"
- statusCount: 调用后端新增接口 `GET /api/v1/versionCompare/counts`，展示 Rule 维度的 mismatch 统计（如 Total: 5, "3 with mismatches"）

### 6.2 第一级: Rule 卡片列表页 (`/jobs/version-compare`)

`VersionCompareView.vue` — 展示所有 Delivery Rule 的卡片，每张卡片显示：

```
┌─────────────────────────────────────────────────────────────────┐
│  Version Compare                                    [Breadcrumb]│
│                                                                 │
│  ┌─ Rule: SAP-PO-Migration ─────┐  ┌─ Rule: EDI-Flows ───────┐ │
│  │                               │  │                          │ │
│  │  Source: DEV-01               │  │  Source: DEV-02          │ │
│  │  Tenants: 12                  │  │  Tenants: 5             │ │
│  │  Last Snapshot: 10 min ago    │  │  Last Snapshot: 2h ago  │ │
│  │  ✓ 45 matched  ❌ 3 mismatch │  │  ✓ 20 matched  ❌ 0     │ │
│  │                               │  │                          │ │
│  └───────────────────────────────┘  └──────────────────────────┘ │
│                                                                 │
│  ┌─ Rule: Payment-Hub ──────────┐  ┌─ Rule: ... ──────────────┐ │
│  │                               │  │                          │ │
│  │  Source: DEV-01               │  │  No snapshot yet         │ │
│  │  Tenants: 20                  │  │  Click to trigger        │ │
│  │  Last Snapshot: 1d ago        │  │                          │ │
│  │  ✓ 80 matched  ❌ 12 mismatch│  │                          │ │
│  └───────────────────────────────┘  └──────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

**卡片数据来源**: 新增后端接口 `GET /api/v1/versionCompare/summary`，返回所有 Rule 的快照摘要信息（rule name、source tenant、tenant count、last snapshot time、matched/mismatched counts）。

**点击卡片** → 跳转到 `/jobs/version-compare/:ruleId`

### 6.3 第二级: 比较结果详情页 (`/jobs/version-compare/:ruleId`)

`VersionCompareDetailView.vue` — 展示单个 Rule 的完整比较结果。

#### 6.3.1 结论: 需要专用组件，但基于标准 table

普通的 `DataTable.vue`（flat 单层表格）**不能完全满足**此场景，原因:
1. 结果按 **Package 分组**，需要 grouped/nested 展示
2. Tenant 数量动态变化，列需要动态生成
3. 版本匹配状态需要颜色编码的视觉反馈

**推荐方案**: 基于 UI5 组件构建，内部使用标准 `ui5-table` + `ui5-panel`，不需要引入新的 UI 库。

#### 6.3.2 UI 布局

```
┌─────────────────────────────────────────────────────────┐
│  Version Compare - Rule: {ruleName}                     │
│                                                         │
│  Source: {sourceTenant}                                  │
│  Snapshot: {triggeredAt} by {triggeredBy}  [Refresh] btn│
│                                                         │
│  [☑ Design Time] [☑ Runtime] [☐ Mismatch Only]          │
│                                                         │
│  Packages: [☑ PkgA] [☑ PkgB] [☐ PkgC]                 │
│                                                         │
│  ┌─ Package: PkgA ────────────────────────────────────┐ │
│  │                                                     │ │
│  │  Artifact   │ Type │ Source  │ tenant-a │ tenant-b  │ │
│  │  ───────────────────────────────────────────────────│ │
│  │  FlowA1     │ IF   │ 1.2.3  │ 1.2.2 ❌ │ 1.2.3 ✓  │ │
│  │  ScriptA1   │ SC   │ 2.0.0  │ 2.0.0 ✓  │ 1.9.0 ❌ │ │
│  │  FlowA2     │ IF   │ DRAFT  │ 1.0.0 ❌ │ DRAFT ✓  │ │
│  │                                                     │ │
│  └─────────────────────────────────────────────────────┘ │
│                                                         │
│  ┌─ Package: PkgB ────────────────────────────────────┐ │
│  │  ...                                                │ │
│  └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

### 6.4 交互流程

1. 用户在 HomeView 点击 "Version Compare" AppCard → 进入 Rule 卡片列表页
2. 页面加载时调用 `GET /api/v1/versionCompare/summary` 获取所有 Rule 的快照摘要
3. 用户点击某 Rule 卡片 → 跳转到 `/jobs/version-compare/:ruleId`
4. 详情页加载时调用 GET 查询该 Rule 是否有已有快照
5. 如无快照或数据过旧，用户点击 "Refresh" 触发 POST trigger
6. 前端轮询 GET 直到 status 变为 "completed"
7. 用户通过 checkbox 切换 DT/RT、mismatchOnly、packageIDs 过滤（前端可本地过滤或重新调 GET）
8. 展示分组比较结果

### 6.5 视觉标记

- **匹配** (match=true): 绿色/UI5 Positive design（`ValueState.Positive`）
- **不匹配** (match=false): 红色/UI5 Negative design（`ValueState.Negative`）
- **DRAFT** (designTimeDraft=true): 特殊标签/badge 标记（如 "DRAFT" 文字 + 不同颜色）
- **不存在/错误**: 灰色 + tooltip 显示 error 信息
- **Runtime Status**: 额外的小标签显示 STARTED/ERROR 等状态

### 6.6 前端文件变更

| 文件 | 变更 |
|------|------|
| `src/router/index.ts` | `/jobs` children 新增 `version-compare` 路由 + 新增 `/jobs/version-compare/:ruleId` 路由 |
| `src/service/api.ts` | 新增 `getVersionCompareSummary()`、`triggerVersionCompare(ruleId)`、`getVersionCompare(ruleId, params)`、`getVersionCompareCounts()` |
| `src/service/model.ts` | 新增 request/response 类型定义 |
| `src/views/VersionCompareView.vue` | **新建** — Rule 卡片列表页 |
| `src/views/VersionCompareDetailView.vue` | **新建** — 单 Rule 比较结果详情页 |

---

## 7. 执行阶段

### 阶段一: 后端 — 接口扩展 + 数据结构 + Artifact 中心函数

- [ ] 扩展 `IntegrationService` 接口（新增 3 个方法）
- [ ] 定义数据结构: `VersionCompareSnapshot`, `SnapshotData`, `PackageSnapshot`, `ArtifactSnapshot`, `ArtifactVersionInfo`
- [ ] 定义 DB model 并注册 AutoMigrate
- [ ] 将 `wrapArtifact` 从 `handler/cpi_handler.go` 迁移到 `service/artifacts.go`
- [ ] 实现 `FetchPackageArtifacts` 中心函数（`service/artifacts.go`）
- [ ] 回改 `GetPackageArtifactsHandler` 调用 `service.FetchPackageArtifacts`
- [ ] 确保现有测试通过

### 阶段二: 后端 — Service 层实现

- [ ] 实现 `service/version_compare.go`:
  - [ ] `TriggerVersionCompare` — 异步触发快照采集（调用 `FetchPackageArtifacts` 获取 design time）
  - [ ] `QueryVersionCompare` — 读取缓存 + 实时计算 match + 过滤
- [ ] 并发获取各 tenant 的 artifact 版本（errgroup）
- [ ] Runtime 优化: 每 tenant 一次 `GetRuntimeArtifacts()` + 内存索引
- [ ] 版本比对逻辑（字符串精确匹配）
- [ ] 错误容忍：单个 tenant/artifact 失败不阻断整体
- [ ] Rate limiting: 可配置的冷却间隔

### 阶段三: 后端 — Handler + 路由

- [ ] 实现 `handler/version_compare.go`:
  - [ ] `TriggerVersionCompareHandler` — POST trigger
  - [ ] `QueryVersionCompareHandler` — GET query with filters
  - [ ] `VersionCompareSummaryHandler` — GET 所有 Rule 的快照摘要（供 Rule 卡片列表页使用）
  - [ ] `VersionCompareCountsHandler` — GET Rule 维度的 mismatch 统计（供 HomeView AppCard 使用）
- [ ] 注册路由:
  - [ ] `POST /api/v1/deliveryRule/:id/versionCompare/trigger`
  - [ ] `GET /api/v1/deliveryRule/:id/versionCompare`
  - [ ] `GET /api/v1/versionCompare/summary`
  - [ ] `GET /api/v1/versionCompare/counts`
- [ ] 编写单元测试

### 阶段四: 前端 — API + 类型定义

- [ ] 新增 TypeScript 类型定义
- [ ] 新增 API 调用函数（trigger + query）

### 阶段五: 前端 — UI 组件

- [ ] 实现 `VersionCompareView.vue`（Rule 卡片列表页）
- [ ] 实现 `VersionCompareDetailView.vue`（比较结果详情页）
- [ ] 路由注册（`/jobs/version-compare` + `/jobs/version-compare/:ruleId`）
- [ ] HomeView AppCard 自动显示（由 router children 发现机制驱动）
- [ ] 轮询机制（trigger 后等待 completed）

### 阶段六: 集成测试 + 优化

- [ ] 端到端测试
- [ ] 性能优化（并发控制、大规模 tenant 场景）
- [ ] 错误处理完善
- [ ] Rate limit 配置调优

---

## 附录: 设计决策记录

| # | 决策 | 选择 | 替代方案 | 理由 |
|---|------|------|----------|------|
| 1 | 同步 vs 异步 | 异步 trigger + 缓存 query | 同步 POST | 20+ tenant 场景下同步调用会超时 |
| 2 | 快照存储粒度 | 每 Rule 1 条记录 (upsert) | 每次触发新增记录 | 只需最新结果，避免数据膨胀 |
| 3 | Match 计算位置 | GET 时后端实时计算 | 存入 DB / 推给前端 | 保证过滤灵活性，减少前端逻辑 |
| 4 | 采集范围 | 全量（所有 Package、所有 Tenant） | 按用户选择的 Package 采集 | Trigger 无参数更简洁，Query 时再过滤 |
| 5 | sourceTenant 格式 | 完整 `db.CpiTenant` 实体 | 简化的 `{id, name}` | 前端可能需要 host 等额外字段 |
| 6 | Version 比较方式 | 字符串精确匹配 | semver 比较 | 跨 tenant 一致性检查，相等即可 |
| 7 | DRAFT 处理 | `designTimeDraft: true` 标记 | 将 "active" 转为特殊版本号 | 保留原始信息，前端可自行决定展示方式 |
| 8 | Artifact 类型合并 | 提取 `FetchPackageArtifacts` 中心函数到 service 层，复用 `wrapArtifact` | 在 version compare 中单独实现 | 单一职责，未来新增 artifact 类型只改一处；现有 handler 也受益于复用 |
| 9 | 并发触发防护 | DB 原子 UPDATE（`WHERE status != 'running'`） | 应用层 sync.Mutex | DB 级别在多实例部署下也安全；Mutex 仅适用于单实例 |
| 10 | Goroutine Context | `context.Background()` 独立 context | 复用请求 context | 请求返回后 request context 被取消，会导致异步 goroutine 中所有 API 调用失败 |
| 11 | Package 列表来源 | Source Tenant 实时 API（`GetPackages`） | 从 DeliveryRule 配置获取 | 以 Source Tenant 实际内容为准，避免配置与实际不同步 |
