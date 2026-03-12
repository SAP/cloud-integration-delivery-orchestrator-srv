# RFC: Cross-Tenant Artifact Version Compare

## 目录

1. [需求概述](#1-需求概述)
2. [现有代码分析](#2-现有代码分析)
3. [API 设计](#3-api-设计)
4. [DB 模型设计](#4-db-模型设计)
5. [后端实现计划](#5-后端实现计划)
6. [前端 UI 方案](#6-前端-ui-方案)
7. [执行阶段](#7-执行阶段)
8. [Global Included Packages](#8-global-included-packages)

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

### 3.4 Included Packages 管理端点

全局 Package 白名单配置，用于控制 version compare 的采集范围。详见 [Section 8](#8-global-included-packages)。

```
GET /api/v1/versionCompare/includedPackages
```

返回当前全局 include 列表。

#### Response

```json
{
  "packages": [
    { "id": 1, "packageID": "PackageA", "description": "Core integration flows", "createdBy": "user@example.com" },
    { "id": 2, "packageID": "PackageB", "description": "EDI mappings", "createdBy": "user@example.com" }
  ]
}
```

```
PUT /api/v1/versionCompare/includedPackages
```

批量替换整个 include 列表（事务操作：先删除旧记录，再插入新记录）。

#### Request Body

```json
{
  "packages": [
    { "packageID": "PackageA", "description": "Core integration flows" },
    { "packageID": "PackageB", "description": "EDI mappings" }
  ]
}
```

#### Response

```json
{
  "packages": [
    { "id": 3, "packageID": "PackageA", "description": "Core integration flows", "createdBy": "user@example.com" },
    { "id": 4, "packageID": "PackageB", "description": "EDI mappings", "createdBy": "user@example.com" }
  ]
}
```

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
    ModifiedBy        string `json:"modifiedBy,omitempty"`  // 最后 design-time 修改人（CPI API 返回；仅 Integration Flow 有值，Script Collection 无此字段）
    ModifiedAt        string `json:"modifiedAt,omitempty"`  // 最后 design-time 修改时间（同上）
    RuntimeVersion    string `json:"runtimeVersion"`
    RuntimeStatus     string `json:"runtimeStatus"`      // 值应与 consts.RuntimeState 一致: STARTED / STARTING / ERROR
    Error             string `json:"error,omitempty"`
}
```

### 4.3 VersionCompareIncludedPackage 表（全局白名单）

全局 Package 白名单，控制 version compare 的采集范围。所有 Delivery Rule 共享同一份列表。

```go
type VersionCompareIncludedPackage struct {
    gorm.Model
    PackageID   string `gorm:"uniqueIndex"` // CPI Package ID (e.g. "PackageA")
    Description string                       // 说明 (optional, e.g. "Core integration flows")
    CreatedBy   string
}
```

**语义**:
- **列表为空** → 比较所有 Package（向后兼容，与当前行为一致）
- **列表非空** → 仅比较列表中的 Package（白名单模式）

**应用时机**: `collectVersionSnapshot` 的 goroutine 中，在 `GetPackages()` 之后、per-package 循环之前，加载白名单并过滤。

### 4.4 设计决策

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
| ModifiedBy/ModifiedAt | 仅存储 + 仅对 Source Tenant 返回 | 用户关注"谁在源端做了最后一次提交"；Target Tenant 版本会被覆盖，修改人无意义 |
| SC 缺少 ModifiedBy | 接受 API 限制，显示 `-` | SAP CPI `ScriptCollectionDesigntimeArtifacts` 端点（list 和 single-item）均不返回 `ModifiedBy`/`ModifiedAt`，属于 API 设计缺陷，无法绕过 |
| Package 白名单范围 | 全局（所有 Rule 共享） | 某些 Package 本身是 template 生成的（包含数百个仅名称/配置不同的 artifact），无论在哪个 Rule 下都不需要 version compare |
| 白名单空列表语义 | 空 = 全部比较 | 向后兼容，不影响现有功能；非空时切换为白名单模式 |
| Include vs Exclude | Include List（白名单） | 用户希望显式控制哪些 Package 参与比较，白名单模式更精确 |

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
  │     ├─ 4a.1. 应用全局 Package 白名单过滤:
  │     │       加载 VersionCompareIncludedPackage 表。
  │     │       如果列表非空 → 仅保留列表中的 Package（白名单模式）。
  │     │       如果列表为空 → 保留所有 Package（向后兼容）。
  │     │       被过滤的 Package 不会产生任何 CPI API 调用，也不会出现在快照数据中。
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

#### 5.3.1 Runtime Index 的 Mutex 设计与性能分析

`collectVersionSnapshot` 预取 runtime artifact 时（`service/version_compare.go:151-192`），使用全局 `sync.Mutex` 保护 `runtimeIndex map[uint]map[string]RuntimeArtifact` 的写入。每个 tenant 对应一个 goroutine，所有 goroutine 共享同一把锁。

**为什么不会产生性能问题：**

每个 goroutine 的工作分为两部分：

| 阶段 | 操作 | 耗时量级 | 是否持锁 |
|------|------|----------|----------|
| 网络 I/O | `s.CPI()` + `GetRuntimeArtifacts()` | 数百毫秒 ~ 数秒 | 否 |
| 构建本地索引 | `for _, a := range artifacts { index[a.ID] = a }` | 微秒 | 否 |
| 写入共享 map | `runtimeIndex[tenant.ID] = index` | 纳秒（单次指针赋值） | **是** |

关键设计：**临界区只包含一次 map 赋值**（`runtimeIndex[tenant.ID] = index`），本地索引的构建在锁外完成。以 10 个 tenant、每个 API 调用 500ms 为例：

- 并行 I/O 总耗时 ≈ 500ms
- 10 次串行 map 写入 ≈ 1μs
- 锁竞争额外开销 ≈ 1μs / 500ms ≈ **0.0002%**

**替代方案及其 trade-off：**

| 方案 | 优点 | 缺点 | 适用场景 |
|------|------|------|----------|
| **当前：`sync.Mutex`** | 简单直观，goroutine 数量不固定时代码清晰 | 理论上存在锁竞争 | 临界区极窄（纳秒级）时性能影响可忽略 |
| 预分配 slice + 按索引写入 | 完全无锁 | 需要提前确定 goroutine 数量，与 `errgroup` 的动态错误处理搭配略显僵硬 | 临界区较宽（毫秒级）时值得考虑 |

`collectPackageSnapshot`（第 243 行）采用了预分配 slice 方案（`results := make([]tenantArtifacts, len(tenants))`），因为该场景下 tenant 列表固定、goroutine 与索引一一对应。runtime 预取阶段保留 mutex 方案，因为其临界区极窄且代码更简洁。

**结论：当前 mutex 设计在可预见的 tenant 规模下（数十个量级）不构成性能瓶颈，无需优化。**

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
| `service/service.go` | `IntegrationService` 接口新增 4 个方法（含 `GetPackages`） |
| `service/artifacts.go` | **新建** — `FetchPackageArtifacts` 中心函数 + `WrapArtifact`（从 handler 迁移） |
| `service/version_compare.go` | **新建** — Trigger + Query + Summary + Counts service 逻辑 + 响应类型定义 |
| `handler/handler.go` | 注册新路由（4 个端点） |
| `handler/version_compare.go` | **新建** — Trigger + Query + Summary + Counts handler |
| `handler/cpi_handler.go` | 移除 `wrapArtifact`，`GetPackageArtifactsHandler` 改为调用 `service.FetchPackageArtifacts` |
| `pkg/cpi/tenant_compare.go` | 清理空桩代码，数据结构统一定义在 `db/version_compare.go` |
| `db/version_compare.go` | **新建** — `VersionCompareSnapshot` model + `SnapshotData` 等 JSON 序列化类型 |
| `db/conn.go` | AutoMigrate 新增 model |
| `service/version_compare_test.go` | **新建** — 单元测试 |

### 5.7 Global Included Packages 过滤逻辑

`collectVersionSnapshot` 中 `GetPackages()` 之后增加白名单过滤：

```go
// After: packages, err := sourceClient.GetPackages(ctx)
var included []db.VersionCompareIncludedPackage
s.DB.Find(&included)

if len(included) > 0 {
    includeSet := make(map[string]bool, len(included))
    for _, inc := range included {
        includeSet[inc.PackageID] = true
    }
    var filtered []cpi.CPIPackage
    for _, pkg := range packages {
        if includeSet[pkg.ID] {
            filtered = append(filtered, pkg)
        }
    }
    packages = filtered
}
```

**效果**: 被过滤的 Package 完全不会产生后续 CPI API 调用（per-tenant FetchPackageArtifacts），也不会出现在快照数据或查询结果中。

### 5.8 IntegrationService 接口扩展

`IntegrationService` 的定位是 **CPI API 的抽象层**（facade），目的是让 service 层可以 mock CPI 调用进行单元测试。现有方法（`DeployArtifact`、`RuntimeArtifact` 等）同样是对 `CpiClient` 的直接透传，不包含编排逻辑。新增的四个方法性质相同——声明 service 层对 CPI API 的新依赖。

```go
type IntegrationService interface {
    // 现有方法
    DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error)
    RuntimeArtifact(ctx context.Context, artifactId string) (cpi.RuntimeArtifact, error)
    GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (cpi.IflowItem, error)
    GetDesignTimeScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (cpi.ScriptCollectionItem, error)

    // 新增: Version Compare 所需的批量查询能力
    GetPackages(ctx context.Context) ([]cpi.CPIPackage, error)
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

#### 6.2.1 Included Packages 管理

列表页 header 区域新增 **"Manage Included Packages"** 按钮（因为是全局配置，放在列表页而非详情页）。

点击打开一个 dialog，用于管理全局 Package 白名单。Dialog 分为两个区域：

**上方：已选列表**
- 显示当前 include 列表（Package ID + Description），每项有删除按钮
- 空列表时显示提示："When the list is empty, all packages are compared."

**下方：从 Tenant 添加**
- `<ui5-select>` 租户下拉框（数据来源：`GET /api/v1/cpiTenant`，复用已有 API）
- 选择租户后自动调用 `GET /api/v1/tanant/packages?tenant={tenantId}` 拉取该租户的 Package 列表
- 以 checkbox 列表展示可选 Package（自动排除已在白名单中的条目）
- 每个 checkbox 显示 `Package ID` + `Name`（帮助用户识别）
- **"Add Selected"** 按钮将勾选的 Package 加入上方列表
- `Description` 字段自动填充为 Package 的 `Name`（用户可在保存前修改）

**Footer**: Save + Cancel（不变，PUT 整体替换）

> **注意**: 不需要新增任何后端 API。`GetCpiTenants` 和 `GetPackages` 均为已有接口。

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
│  [☑ Design Time] [☑ Runtime] [☑ Mismatch Only]          │
│                                                         │
│  Packages: [Select All] [Deselect All]  (max-height 滚动)│
│  [☑ PkgA] [☑ PkgB] [☐ PkgC]                            │
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
- **Runtime Status**: 仅在非 STARTED 状态时显示标签（如 ERROR）。有 Runtime 版本即隐含 STARTED，无需额外标记

### 6.6 前端文件变更

| 文件 | 变更 |
|------|------|
| `src/router/index.ts` | `/jobs` children 新增 `version-compare` 路由 + 新增 `/jobs/version-compare/:ruleId` 路由 |
| `src/service/api.ts` | 新增 `getVersionCompareSummary()`、`triggerVersionCompare(ruleId)`、`getVersionCompare(ruleId, params)`、`getVersionCompareCounts()`；后续新增 `GetIncludedPackages()`、`UpdateIncludedPackages()` |
| `src/service/model.ts` | 新增 request/response 类型定义；后续新增 `VersionCompareIncludedPackage` type |
| `src/views/VersionCompareView.vue` | **新建** — Rule 卡片列表页；后续新增 "Manage Included Packages" 按钮 + dialog |
| `src/views/VersionCompareDetailView.vue` | **新建** — 单 Rule 比较结果详情页 |

---

## 7. 执行阶段

### 阶段一: 后端 — 接口扩展 + 数据结构 + Artifact 中心函数 ✅

- [x] 扩展 `IntegrationService` 接口（新增 3 个方法）— `service/service.go:29-41`
- [x] 定义数据结构: `VersionCompareSnapshot`, `SnapshotData`, `PackageSnapshot`, `ArtifactSnapshot`, `ArtifactVersionInfo` — `db/version_compare.go`
- [x] 定义 DB model 并注册 AutoMigrate — `db/conn.go:59`
- [x] 将 `wrapArtifact` 从 `handler/cpi_handler.go` 迁移到 `service/artifacts.go`（导出为 `WrapArtifact`）
- [x] 实现 `FetchPackageArtifacts` 中心函数（`service/artifacts.go`，使用 errgroup 并发获取 iflow + SC）
- [x] 回改 `GetPackageArtifactsHandler` 调用 `service.FetchPackageArtifacts` — `handler/cpi_handler.go:61-77`
- [x] 确保编译通过（`go build ./...` + `go vet ./...` 零错误）

> **实现备注**:
> - `CpiClient` 已实现全部 7 个 `IntegrationService` 方法，无需额外 adapter struct
> - 数据结构统一放在 `db/version_compare.go`（而非 `pkg/cpi/tenant_compare.go`），因为 `SnapshotData` 等类型通过 `gorm:"serializer:json"` 与 DB model 紧耦合
> - `pkg/cpi/tenant_compare.go` 原有的空桩代码已清理，仅保留 package comment 指向新位置

### 阶段二: 后端 — Service 层实现 ✅

- [x] 实现 `service/version_compare.go`:
  - [x] `TriggerVersionCompare` — 异步触发快照采集（`context.Background()` goroutine，DB 原子并发保护）
  - [x] `QueryVersionCompare` — 读取缓存 + 实时计算 match + 过滤（packageIDs / designTime / runTime / mismatchOnly）
  - [x] `GetVersionCompareSummary` — 所有 Rule 的快照摘要（含 matched/mismatched/total 计数）
  - [x] `GetVersionCompareCounts` — Rule 维度 mismatch 统计（matched / mismatched / no_data / running / failed）
- [x] 并发获取各 tenant 的 artifact 版本（errgroup，`SetLimit(10)` 限流）
- [x] Runtime 优化: 每 tenant 一次 `GetRuntimeArtifacts()` + `map[artifactID]RuntimeArtifact` 内存索引
- [x] 版本比对逻辑（字符串精确匹配，DT + RT 独立比对）
- [x] 错误容忍：单个 tenant/artifact 失败记录 warn 日志但不阻断整体采集
- [x] Rate limiting: 5 分钟冷却间隔（`versionCompareCooldown` 常量）
- [x] 扩展 `IntegrationService` 新增 `GetPackages` 方法（Trigger 需要从 Source Tenant 获取包列表）

> **实现备注**:
> - 新建 `service/version_compare.go`（约 440 行）：4 个 Service 方法 + 1 个内部采集函数 + 响应类型定义 + 辅助函数
> - Trigger 流程: 加载 Rule → 冷却检查 → DB 原子 UPDATE (WHERE status != 'running') → goroutine 采集
> - 采集流程: GetPackages (source) → per-tenant GetRuntimeArtifacts (errgroup) → per-package×per-tenant FetchPackageArtifacts (errgroup, limit=10) → 组装 SnapshotData → DB 更新
> - Query 响应包含 `tenants` 数组（携带 name + isSource 元数据），避免前端额外查询
> - `computeMismatchCounts` 辅助函数被 Summary 和 Counts 两个端点复用
> - `parsePackageIDs` 辅助函数用于 Handler 层解析 query 参数（Phase 3 使用）

### 阶段三: 后端 — Handler + 路由 ✅

- [x] 实现 `handler/version_compare.go`:
  - [x] `TriggerVersionCompareHandler` — POST trigger
  - [x] `QueryVersionCompareHandler` — GET query with filters
  - [x] `VersionCompareSummaryHandler` — GET 所有 Rule 的快照摘要（供 Rule 卡片列表页使用）
  - [x] `VersionCompareCountsHandler` — GET Rule 维度的 mismatch 统计（供 HomeView AppCard 使用）
- [x] 注册路由:
  - [x] `POST /api/v1/deliveryRule/:id/versionCompare/trigger`
  - [x] `GET /api/v1/deliveryRule/:id/versionCompare`
  - [x] `GET /api/v1/versionCompare/summary`
  - [x] `GET /api/v1/versionCompare/counts`
- [ ] 编写单元测试

> **实现备注**:
> - `handler/version_compare.go` (~85 行): 4 个 handler 函数，遵循项目现有模式
> - `parsePackageIDs` 导出为 `ParsePackageIDs` 供 handler 层调用
> - Query 参数默认值: `designTime`/`runTime` 默认 `true`（仅当显式传 `"false"` 时关闭）；`mismatchOnly` 默认 `false`
> - Trigger handler 根据 `TriggerResult.Status` 返回不同 HTTP 状态码: `running`→200, `rate_limited`→429, `conflict`→409
> - `triggeredBy` 使用 `service.UserEmail(ctx)` 获取当前用户邮箱

### 阶段四: 前端 — API + 类型定义 ✅

- [x] 新增 TypeScript 类型定义 — `src/service/model.ts`（8 个类型: `SnapshotStatus`, `TriggerStatus`, `TriggerResult`, `VersionCompareTenantInfo`, `VersionCompareArtifactTenantInfo`, `VersionCompareArtifact`, `VersionComparePackage`, `VersionCompareResponse`, `VersionCompareSummaryItem`）
- [x] 新增 API 调用函数 — `src/service/api.ts`（4 个函数: `TriggerVersionCompare`, `QueryVersionCompare`, `GetVersionCompareSummary`, `GetVersionCompareCounts`）

> **实现备注**:
> - 类型定义与后端 Go struct 的 JSON tag 一一对应
> - `QueryVersionCompare` 通过 `params` 传递 `designTime`/`runTime`/`mismatchOnly` 查询参数（布尔值转字符串）
> - `GetVersionCompareCounts` 返回 `AppCount` 类型（与 HomeView AppCard 框架统一）

### 阶段五: 前端 — UI 组件 ✅

- [x] 实现 `VersionCompareView.vue`（Rule 卡片列表页）
- [x] 实现 `VersionCompareDetailView.vue`（比较结果详情页）
- [x] 路由注册（`/jobs/version-compare` + `/jobs/version-compare/:ruleId`）
- [x] HomeView AppCard 自动显示（由 router children 发现机制驱动 + `versionCompareCounts` 状态函数）
- [x] 轮询机制（trigger 后 3s 间隔 poll GET 直到 status !== 'running'）

> **实现备注**:
>
> **VersionCompareView.vue (Rule 卡片列表页)**:
> - 调用 `GetVersionCompareSummary` 获取所有 Rule 的快照摘要
> - 每张卡片显示: Rule 名称、Source Tenant、Tenant 数量、状态 Tag、matched/mismatched/total 计数
> - 每张卡片有 Trigger Scan 按钮（`event.stopPropagation()` 防止冒泡到卡片点击）
> - 点击卡片跳转到 `/jobs/version-compare/:ruleId`
>
> **VersionCompareDetailView.vue (比较结果详情页)**:
> - 标题显示 Rule 名称（通过 `GetDeliveryRule(ruleId)` 并行加载）
> - Meta 区域: Source Tenant、Snapshot 时间 + 触发者
> - 三组 Filter: Design Time / Runtime / Mismatch Only（**Mismatch Only 默认勾选**；变更时重新调 GET）
> - Package Filter: 当有多个 Package 时显示 checkbox 列表，前端本地过滤；带 Select All / Deselect All 按钮；checkbox 区域有 `max-height: 6rem` 限高滚动
> - 比较表格按 Package 分组，每个 Package 一个 `<ui5-panel>`（可折叠），内含 `<ui5-table>`
> - **每个 Tenant 一列**（非 DT/RT 分两列）：单元格内 DT/RT 上下堆叠
> - Source 列无 match 标记；Target 列每行背景色标识匹配状态（绿/红）
> - **Modified By / Modified At 列**（表格最后两列）：仅显示 Source Tenant 的 design-time 最后修改人和修改时间。注意：SAP CPI API 仅 Integration Flow 返回 `ModifiedBy`/`ModifiedAt`，Script Collection 不返回这两个字段（显示 `-`）
> - DRAFT 标签: `designTimeDraft` 时显示 `<ui5-tag>DRAFT</ui5-tag>`
> - RuntimeStatus 标签: 仅在非 STARTED 状态（如 ERROR）时显示，有 Runtime 版本即隐含 STARTED 无需冗余标记
> - 错误信息: `error` 字段通过 `title` 属性作为 tooltip
> - N/A: artifact 在 tenant 上不存在时显示灰色斜体 "N/A" + tooltip
> - `onUnmounted` 清理 poll timer，防止组件销毁后继续轮询
> - Go `map[uint]` JSON 序列化为 string key，通过 `Record<number, ...>` + JS 自动类型转换处理

### 阶段六: 集成测试 + 优化

- [ ] 端到端测试
- [ ] 性能优化（并发控制、大规模 tenant 场景）
- [ ] 错误处理完善
- [ ] Rate limit 配置调优

### 阶段七: Global Included Packages ✅

- [x] 后端 — DB Model:
  - [x] 新增 `VersionCompareIncludedPackage` struct — `db/version_compare.go`
  - [x] 注册 AutoMigrate — `db/conn.go`
- [x] 后端 — Service:
  - [x] `GetIncludedPackages` — 查询当前白名单 — `service/version_compare.go`
  - [x] `UpdateIncludedPackages` — 事务批量替换白名单 — `service/version_compare.go`
  - [x] `collectVersionSnapshot` 中增加 include 过滤逻辑 — `service/version_compare.go`
- [x] 后端 — Handler + 路由:
  - [x] `IncludedPackagesHandler` (GET) — `handler/version_compare.go`
  - [x] `UpdateIncludedPackagesHandler` (PUT) — `handler/version_compare.go`
  - [x] 注册路由: `GET /api/v1/versionCompare/includedPackages` + `PUT /api/v1/versionCompare/includedPackages` — `handler/handler.go`
- [x] 后端 — 测试:
  - [x] DB 层测试: `VersionCompareIncludedPackage` CRUD — `db/version_compare_test.go` (4 tests)
  - [x] Service 层测试: `GetIncludedPackages` / `UpdateIncludedPackages` + trigger 过滤逻辑 — `service/version_compare_test.go` (5 tests)
- [x] 前端 — 类型 + API:
  - [x] 新增 `VersionCompareIncludedPackage` type — `src/service/model.ts`
  - [x] 新增 `GetIncludedPackages()` / `UpdateIncludedPackages()` — `src/service/api.ts`
- [x] 前端 — UI:
  - [x] `VersionCompareView.vue` 新增 "Manage Included Packages" 按钮 + `ui5-dialog`（添加/移除/保存）
- [x] 编译验证: `go build ./...` + `go vet ./...` + 前端 `vite build`

> **实现备注**:
> - `VersionCompareIncludedPackage` model 使用 `gorm:"uniqueIndex"` 在 `PackageID` 上保证唯一性
> - `UpdateIncludedPackages` 使用 GORM Transaction：先 `DELETE WHERE 1=1`，再逐条 `Create`，保证原子性
> - `loadIncludedPackageFilter()` 内部 helper：空表返回 `nil`（表示不过滤），非空返回 `map[string]bool`
> - `collectVersionSnapshot` 在 `GetPackages()` 之后调用 `loadIncludedPackageFilter()`，若返回非 nil 则过滤
> - 前端 dialog 使用 `ui5-dialog` + `ui5-toolbar` footer 模式，与项目其他 dialog 一致
> - ~~前端 dialog 支持手动输入 Package ID + Description，添加/移除后点 Save 批量提交~~ → 已由 Phase 7a 替换为 tenant 选择模式

### 阶段 7a: Included Packages — Tenant 选择模式 UX 改进

**背景**: Phase 7 的手动输入 Package ID 方式不够友好，用户需要准确记住 Package ID。改为从 Tenant 的 Package 列表中选择，更直观。

**变更范围**: 纯前端（`VersionCompareView.vue` dialog 重写），无后端改动。

- [ ] 前端 — 重写 dialog 状态管理:
  - [ ] 移除手动输入状态: `newPackageID`, `newPackageDesc`
  - [ ] 新增 tenant 选择状态: `tenants` (租户列表), `selectedTenantId` (选中的租户), `tenantPackages` (该租户的 Package 列表), `loadingPackages` (加载中标志), `selectedNewPkgIds` (checkbox 选中的 Package ID Set)
- [ ] 前端 — 重写 dialog 逻辑:
  - [ ] `openIncludedDialog()`: 除加载白名单外，同时调用 `GetCpiTenants()` 加载租户列表
  - [ ] `onTenantChange(tenantId)`: 调用 `GetPackages(tenantId)` 加载 Package 列表，自动排除已在白名单中的条目
  - [ ] `addSelectedPackages()`: 将 checkbox 勾选的 Package 加入 `includedPackages`（`Description` 自动填充为 `Name`），清空 checkbox 选中状态，并从可选列表中移除已添加项
  - [ ] 保留 `removePackage(index)` 和 `saveIncludedPackages()` 逻辑不变
- [ ] 前端 — 重写 dialog 模板:
  - [ ] 上方: 已选 Package 列表（保留现有删除按钮样式）
  - [ ] 下方: `<ui5-select>` 租户下拉 + Package checkbox 列表 + "Add Selected" 按钮
  - [ ] Package checkbox 每项显示 `Id` + `Name`
  - [ ] 加载状态: tenant packages 加载时显示 `<ui5-busy-indicator>`
  - [ ] 空状态处理: 无可选 Package 时提示 "All packages from this tenant are already included"
- [ ] 编译验证: `vite build` 通过

> **实现备注**:
> - `GetCpiTenants` 和 `GetPackages` 是已有 API，直接复用
> - `<ui5-select>` 需要导入 `@ui5/webcomponents/dist/Select.js` 和 `@ui5/webcomponents/dist/Option.js`
> - checkbox 过滤逻辑: `tenantPackages.filter(p => !includedSet.has(p.Id))`，其中 `includedSet` 由当前 `includedPackages` 的 `packageID` 构建
> - 移除白名单项时需同步更新可选列表（重新应用排除逻辑）

---

## 8. Global Included Packages

### 8.1 需求背景

某些 CPI Package 包含大量由 template 生成的 artifact（数百个），这些 artifact 仅在名称和配置上有区别，对 version compare 没有意义。如果纳入比较：
- **性能浪费**: 数百个 artifact × 多个 tenant = 数千次无意义的 CPI API 调用
- **结果噪声**: 大量无意义的 mismatch 淹没真正需要关注的差异
- **快照膨胀**: 存储大量无用数据

### 8.2 设计方案

采用 **全局 Package 白名单（Include List）** 模式：

| 属性 | 说明 |
|------|------|
| **作用范围** | 全局，所有 Delivery Rule 共享 |
| **存储** | 独立表 `VersionCompareIncludedPackage`（非 per-rule 字段） |
| **空列表语义** | 比较所有 Package（向后兼容，当前行为） |
| **非空列表语义** | 仅比较列表中的 Package（白名单模式） |
| **应用时机** | Trigger 采集阶段，`GetPackages()` 之后、per-package 循环之前 |
| **影响范围** | 被过滤的 Package 不产生 API 调用，不出现在快照/查询/统计中 |

### 8.3 为什么选择 Include List 而非 Exclude List

| 维度 | Include List | Exclude List |
|------|-------------|-------------|
| 语义 | "只比较这些" | "比较所有，除了这些" |
| 新 Package 行为 | 不自动纳入（需手动添加） | 自动纳入 |
| 用户偏好 | 显式控制，更精确 | 省事，但可能引入噪声 |

选择 Include List 的原因：用户希望显式控制哪些 Package 参与比较，确保结果只包含有意义的内容。

### 8.4 API 端点

详见 [Section 3.4](#34-included-packages-管理端点)。

- `GET /api/v1/versionCompare/includedPackages` — 获取白名单
- `PUT /api/v1/versionCompare/includedPackages` — 批量替换白名单

PUT 使用整体替换而非逐条 CRUD，原因：
- 配置场景下"查看全貌 → 编辑 → 保存"更自然
- 前端不需要 diff 逻辑
- 单事务保证一致性（先 DELETE ALL 再 INSERT）

### 8.5 前端交互

详见 [Section 6.2.1](#621-included-packages-管理)。

- "Manage Included Packages" 按钮位于 VersionCompareView（列表页）header
- 点击打开 dialog，分为 **已选列表** 和 **从 Tenant 添加** 两个区域
- 已选列表区域显示当前白名单条目，支持逐条删除
- 添加区域通过 tenant 下拉选择 → 加载该 tenant 的 Package 列表 → checkbox 多选 → "Add Selected"
- 空列表提示用户当前为"全部比较"模式
- 保存时调用 `PUT /api/v1/versionCompare/includedPackages` 批量替换

#### 8.5.1 不需要新增后端 API

| 需求 | 已有 API | 说明 |
|------|----------|------|
| 获取 Tenant 列表 | `GET /api/v1/cpiTenant` | 返回所有 `CpiTenant`，提供 `ID` 和 `Name` |
| 获取 Tenant 的 Package 列表 | `GET /api/v1/tanant/packages?tenant={tenantId}` | 返回 `Package[]`，包含 `Id`、`Name`、`Description` |
| 读取白名单 | `GET /api/v1/versionCompare/includedPackages` | 已实现 |
| 保存白名单 | `PUT /api/v1/versionCompare/includedPackages` | 已实现，整体替换 |

#### 8.5.2 UX 流程

```
用户点击 "Manage Included Packages"
  → 打开 dialog
  → 上方显示已选列表（从 GET includedPackages 加载）
  → 下方: 用户从 <ui5-select> 选择一个 Tenant
  → 自动调用 GetPackages(tenantId) 加载该 Tenant 的 Package 列表
  → 展示为 checkbox 列表（排除已在白名单中的条目）
  → 用户勾选需要的 Package → 点击 "Add Selected"
  → 选中项添加到上方已选列表（Description 自动填充为 Package Name）
  → 用户可以从已选列表中删除不需要的条目
  → 点击 Save → PUT 整体替换
```

---

## 附录: 设计决策记录

| # | 决策 | 选择 | 替代方案 | 理由 |
|---|------|------|----------|------|
| 1 | 同步 vs 异步 | 异步 trigger + 缓存 query | 同步 POST | 20+ tenant 场景下同步调用会超时 |
| 2 | 快照存储粒度 | 每 Rule 1 条记录 (upsert) | 每次触发新增记录 | 只需最新结果，避免数据膨胀 |
| 3 | Match 计算位置 | GET 时后端实时计算 | 存入 DB / 推给前端 | 保证过滤灵活性，减少前端逻辑 |
| 4 | 采集范围 | 全量（所有 Package、所有 Tenant），受全局白名单约束 | 按用户选择的 Package 采集 | Trigger 无参数更简洁，Query 时再过滤；全局白名单在 trigger 阶段排除无意义 Package |
| 5 | sourceTenant 格式 | 完整 `db.CpiTenant` 实体 | 简化的 `{id, name}` | 前端可能需要 host 等额外字段 |
| 6 | Version 比较方式 | 字符串精确匹配 | semver 比较 | 跨 tenant 一致性检查，相等即可 |
| 7 | DRAFT 处理 | `designTimeDraft: true` 标记 | 将 "active" 转为特殊版本号 | 保留原始信息，前端可自行决定展示方式 |
| 8 | Artifact 类型合并 | 提取 `FetchPackageArtifacts` 中心函数到 service 层，复用 `wrapArtifact` | 在 version compare 中单独实现 | 单一职责，未来新增 artifact 类型只改一处；现有 handler 也受益于复用 |
| 9 | 并发触发防护 | DB 原子 UPDATE（`WHERE status != 'running'`） | 应用层 sync.Mutex | DB 级别在多实例部署下也安全；Mutex 仅适用于单实例 |
| 10 | Goroutine Context | `context.Background()` 独立 context | 复用请求 context | 请求返回后 request context 被取消，会导致异步 goroutine 中所有 API 调用失败 |
| 11 | Package 列表来源 | Source Tenant 实时 API（`GetPackages`） | 从 DeliveryRule 配置获取 | 以 Source Tenant 实际内容为准，避免配置与实际不同步 |
| 12 | 测试文件位置 | 与被测代码同 package 目录 | 集中 `test/` 目录 | Go 约定：`_test.go` 与实现文件同目录，`go test ./...` 自动发现；集中测试目录是 Java/JS 惯例，在 Go 中为反模式 |
| 13 | 测试框架 | 标准库 `testing`（`t.Errorf`/`t.Fatalf`） | testify | 减少外部依赖，与项目现有风格一致 |
| 14 | 测试数据库 | 本地 PostgreSQL（`LOCAL_POSTGRES_URI`） | SQLite | 保证 GORM 行为一致（JSON 序列化、`TRUNCATE CASCADE` 等 PostgreSQL 特有语义） |
| 15 | 测试隔离 | 目标 ID 清理（`testCleanup` struct + `t.Cleanup`） | `TRUNCATE CASCADE` / `DELETE FROM` | 共享 DB 中不能删除其他数据；`testCleanup` 追踪测试创建的 ID，按 FK 安全顺序删除 |
| 16 | Package 过滤模式 | Include List（全局白名单） | Exclude List（全局黑名单） | 用户偏好显式控制比较范围，白名单更精确 |
| 17 | Package 过滤范围 | 全局（所有 Rule 共享） | Per-Rule 配置 | template Package 无论在哪个 Rule 下都不需要比较；全局配置减少重复维护 |
| 18 | 空白名单语义 | 空 = 比较所有 Package | 空 = 不比较任何 Package | 向后兼容，不影响现有功能 |
| 19 | 白名单存储位置 | 独立表 `VersionCompareIncludedPackage` | `DeliveryRule` 字段 | 全局配置不应绑定到单个 Rule；独立表语义清晰 |
| 20 | 白名单更新 API | PUT 整体替换 | 逐条 CRUD (POST/DELETE) | 配置场景下"查看→编辑→保存"更自然；单事务保证一致性 |
| 21 | 白名单添加方式 | 从 Tenant Package 列表选择 | 手动输入 Package ID | 用户无需记忆 Package ID；直接从 CPI 实时数据选择更直观、减少输入错误；不需要新增后端 API（复用 `GetCpiTenants` + `GetPackages`） |
| 22 | Runtime STARTED 标签 | 有 RT 版本时不显示 STARTED 标签 | 始终显示 STARTED/ERROR 标签 | 有 Runtime 版本即隐含 STARTED 状态，显示 STARTED 标签是冗余信息；仅显示异常状态（如 ERROR）减少视觉噪音 |
