# RFC: HTTP 超时机制 + 依赖注入架构重构

## 目录

- [1. 问题背景](#1-问题背景)
  - [1.1 问题一：缺少超时机制](#11-问题一缺少超时机制)
  - [1.2 问题二：Context 存储在结构体中（反模式）](#12-问题二context-存储在结构体中反模式)
  - [1.3 问题三：全局状态和 init() 副作用](#13-问题三全局状态和-init-副作用)
  - [1.4 当前依赖关系图](#14-当前依赖关系图)
- [2. 技术方案：依赖注入重构](#2-技术方案依赖注入重构)
  - [2.1 方案概述](#21-方案概述)
  - [2.2 目标架构](#22-目标架构)
  - [2.3 分阶段实施策略](#23-分阶段实施策略)
- [3. 实施计划](#3-实施计划)
  - [阶段一：超时机制试点](#阶段一超时机制试点-已完成)
  - [阶段二：推广超时到所有 TMS 方法](#阶段二推广超时到所有-tms-方法-已完成)
  - [阶段三：重构 HttpClient](#阶段三重构-httpclient--context-作为参数--修复-cacheclient-已完成)
  - [阶段四：Service 层 + Handler 层 DI](#阶段四service-层--handler-层-di-已完成)
  - [阶段五：移除 init() 副作用](#阶段五移除-init-副作用-已完成)
  - [阶段六：推广超时到 CPI 包 + 完善测试](#阶段六推广超时到-cpi-包--完善测试)
- [4. 超时时间设计](#4-超时时间设计)
- [5. 单元测试设计](#5-单元测试设计)
  - [5.1 当前测试（阶段一~二）](#51-当前测试阶段一二)
  - [5.2 阶段四后的测试（目标状态）](#52-阶段四后的测试目标状态)
- [6. 风险评估](#6-风险评估)
- [7. 回滚方案](#7-回滚方案)
- [8. 已完成事项](#8-已完成事项)
- [9. 待确认事项](#9-待确认事项)
- [10. 附录](#10-附录)
  - [10.1 已修改的文件](#101-已修改的文件)
  - [10.2 各阶段待修改的文件](#102-各阶段待修改的文件)
  - [10.3 参考资料](#103-参考资料)

---

## 1. 问题背景

### 1.1 问题一：缺少超时机制

`pkg/tms/` 和 `pkg/cpi/` 中所有 HTTP 请求没有设置超时时间：

```go
childCtx, cancel := context.WithCancel(t.Context)  // 无超时
defer cancel()
```

**风险**：请求可能无限期挂起，导致 goroutine 泄漏和资源耗尽。

### 1.2 问题二：Context 存储在结构体中（反模式）

```go
// pkg/env/remotecall.go
type HttpClient struct {
    Context context.Context  // ← 反模式：存储在结构体中
}
```

Go 最佳实践：Context 应作为函数第一个参数传递，而非存储在结构体中。

### 1.3 问题三：全局状态和 init() 副作用

当前架构大量使用全局变量和 `init()` 中的副作用操作：

| 全局依赖 | 位置 | 副作用 |
|----------|------|--------|
| `var db *gorm.DB` | `db/conn.go init()` | 连接数据库、执行 AutoMigrate |
| `var appEnv *cfenv.App` | `pkg/env/env.go init()` | 读取 CF 环境变量 |
| `var destinationMap` | `pkg/env/env.go init()` | HTTP 调用 Destination 服务 |
| ~~`var cacheClient map[string]*HttpClient`~~ | ~~`pkg/env/remotecall.go`~~ | ~~可变的全局 OAuth2 客户端缓存~~ （阶段三已移除） |
| `var logger` | 多处 | 包级别单例 |
| `var globalClient *UaaClient` | `pkg/xsuaa/uaa.go` | 可变的全局单例 |

**影响**：
1. **无法运行单元测试**：import 包即触发 `init()` → 连接数据库/CF 环境
2. **错误处理受限**：`init()` 只能 panic
3. **隐式依赖**：函数签名不反映其真实依赖
4. **测试隔离困难**：无法注入 mock，全局状态被多处修改

### 1.4 当前依赖关系图

```
main.go
  ├── env.Logger()                   [init 时全局创建]
  ├── handler.*()                    [包级别函数，注册为路由]
  │
handler/*
  ├── db.Conn()                      [直接使用全局 DB]
  ├── service.*()                    [调用包级别函数]
  ├── tms.NewClient(ctx)             [每次请求创建新实例 + fetchToken]
  ├── cpi.NewClient(ctx, tenant)     [每次请求创建新实例 + fetchToken]
  └── env.Logger()                   [包级别变量]
  │
service/*
  ├── db.Conn()                      [每个 service 文件都直接使用]
  ├── tms.NewClient(ctx.Background()) [goroutine 中创建新实例]
  ├── cpi.NewClient(ctx.Background()) [goroutine 中创建新实例]
  ├── xsuaa.GetUserEmail()           [全局单例]
  ├── notify.Send*()                 [每次创建新客户端]
  └── env.Logger()                   [包级别变量]
  │
pkg/tms, pkg/cpi, pkg/xsuaa
  ├── env.TmsCredential()            [读取 CF 环境]
  ├── env.NewClient(ctx, ...)        [直接创建实例，无全局缓存（阶段三后）]
  └── env.Logger()                   [包级别变量]
```

---

## 2. 技术方案：依赖注入重构

### 2.1 方案概述

将所有隐式全局依赖转换为显式的、可注入的依赖。在 `main.go` 中统一创建和组装所有依赖，通过构造函数传递给下游。

**核心原则**：
- `init()` 只做无副作用的操作（如设置常量）
- 所有有副作用的初始化（DB、HTTP 调用、读取环境变量）在 `main()` 中显式执行
- 函数签名反映其真实依赖
- 测试可以注入 mock 而不需要真实环境

### 2.2 目标架构

```
main.go (组装层)
  │
  │  // 1. 显式初始化所有基础设施
  │  cfg := config.Load()           // 读取环境变量/CF env
  │  logger := logging.New(cfg)
  │  db := database.Connect(cfg)
  │  tmsFactory := tms.NewFactory(cfg.TMS)
  │  cpiFactory := cpi.NewFactory(cfg.CPI)
  │
  │  // 2. 注入到 service 层
  │  svc := service.New(service.Deps{
  │      DB:     db,
  │      Logger: logger,
  │      TMS:    tmsFactory,
  │      CPI:    cpiFactory,
  │  })
  │
  │  // 3. 注入到 handler 层
  │  h := handler.New(svc, logger)
  │  router := h.SetupRoutes()
  │  router.Run(":8080")
  │
  ▼
handler (持有 service 引用)
  │
  ▼
service (持有 DB、客户端工厂引用)
  │
  ▼
pkg/tms, pkg/cpi (纯 HTTP 客户端，无全局状态)
```

### 2.3 分阶段实施策略

考虑到当前代码规模（~30 个路由、~15 个 service 函数、3 个外部客户端），采用**分阶段逐步重构**的策略，每个阶段都是可交付的、可测试的。

---

## 3. 实施计划

### 阶段一：超时机制试点（✅ 已完成）

**目标**：验证 `context.WithTimeout` 的超时机制

| 步骤 | 任务 | 状态 |
|------|------|------|
| 1 | 在 `pkg/tms/tms.go` 添加超时常量 | ✅ |
| 2 | 修改 `ImportTransportRequest` 使用 `WithTimeout` | ✅ |
| 3 | 添加超时错误特殊处理 | ✅ |

### 阶段二：推广超时到所有 TMS 方法（✅ 已完成）

| 步骤 | 任务 | 状态 |
|------|------|------|
| 1 | 所有 GET 方法使用 `DefaultRequestTimeout` (30s) | ✅ |
| 2 | 大数据量 GET 方法使用 `LongRequestTimeout` (60s) | ✅ |
| 3 | `pkg/tms/v1.go` 的方法添加超时 | ✅ |
| 4 | 编写 `pkg/tms/tms_test.go` 单元测试 | ✅ |

**超时常量定义**：

```go
const (
    DefaultRequestTimeout = 30 * time.Second  // GET 请求
    LongRequestTimeout    = 60 * time.Second  // 大数据量 GET
    ImportTimeout         = 60 * time.Second  // POST 请求
)
```

### 阶段三：重构 HttpClient — Context 作为参数 + 修复 cacheClient（✅ 已完成）

**目标**：解决 Context 反模式和 cacheClient 线程安全问题

**3.1 `pkg/env/remotecall.go` 重构（已完成）**

| 改动 | 说明 |
|------|------|
| 移除 `Context` 字段 | 不再存储在结构体中 |
| 移除 `Ctx` 字段 | 同上 |
| 移除全局 `cacheClient` map | 非线程安全的全局可变缓存 |
| `Do()` 添加 `ctx context.Context` 参数 | Context 作为函数参数传递 |
| 新增 `fetchToken(ctx)` | 实例级 token 获取，`sync.Mutex` 保护 |
| 401 单次重试 | `Do()` → 401 → `fetchToken()` → 重试一次（不再递归） |
| 嵌入改为指针 | `TmsClient`/`CpiClient`/`UaaClient` 嵌入 `*env.HttpClient`（因含 `sync.Mutex`） |

重构前后对比：

```go
// ❌ 重构前
type HttpClient struct {
    Context     context.Context  // 反模式：存储在结构体中
    HttpClient  *http.Client
    AccessToken string
    ApiURL      string
}
var cacheClient map[string]*HttpClient  // 全局可变缓存，非线程安全
func (c *HttpClient) Do(request *HttpRequest) (*[]byte, int, error)

// ✅ 重构后（当前状态）
type HttpClient struct {
    mu           sync.Mutex       // 保护 AccessToken
    HttpClient   *http.Client
    AccessToken  string
    ApiURL       string
    ClientId     string
    ClientSecret string
    AuthUrl      string
}
func NewClient(ctx context.Context, clientID, clientSecret, authUrl, apiUrl string) (*HttpClient, error)
func (c *HttpClient) Do(ctx context.Context, request *HttpRequest) (*[]byte, int, error)
```

**3.1.1 cacheClient 迁移方案 — 当前中间状态**

全局 `cacheClient` 已移除。当前每次调用 `tms.NewClient()` / `cpi.NewClient()` 都会创建新实例并 `fetchToken()`，这是**有意为之的中间状态**：

- **阶段三**：先解决线程安全和无限递归风险，接受短期内多发 token 请求的代价
- **阶段四**（Service 层 DI）：Client 在启动时/Service 构造时创建一次，存在 Service 结构体中成为长生命周期对象。token 刷新靠 `Do()` 内的 401 重试机制。CPI 多 tenant 情况下，启动时遍历 `env.Destinations()` 为每个 tenant 创建一个 `CpiClient`，存在 `map[string]*cpi.CpiClient` 中

**3.2 修改所有客户端包方法签名（已完成）**

- `pkg/tms/tms.go` + `v1.go`：所有方法添加 `ctx context.Context` 第一个参数
- `pkg/cpi/cpi.go` + `sync_github.go`：所有方法添加 `ctx context.Context`
- `pkg/xsuaa/uaa.go`：`GetUserEmail`, `UserInfo`, `SearchByEmail` 添加 `ctx`

**3.3 更新所有调用方（已完成）**

| 层级 | 文件 | ctx 来源 |
|------|------|----------|
| Handler | `tms_handler.go`, `cpi_handler.go`, `uaa_handler.go`, `native_deliver.go`, `delivery_rule.go` | `c.Request.Context()`（gin context） |
| Service | `deliver.go`, `sync_status.go`, `checks.go` | `context.Background()`（goroutine 生命周期独立） |
| Service | `approve.go`, `cancel.go` | `context.Background()` |
| Service | `dr.go` | 接收调用方传入的 `ctx` |

### 阶段四：Service 层 + Handler 层 DI（✅ 已完成）

**目标**：Service 层和 Handler 层不再直接依赖全局变量；所有依赖通过结构体字段注入

> 注：原计划的阶段四（Service 层）和阶段五（Handler 层）已合并为一个阶段实施。

**4.1 架构决策**

| 决策点 | 选择 | 理由 |
|--------|------|------|
| CPI 多 tenant 策略 | 带缓存的工厂函数（`sync.RWMutex` + `map` 延迟创建） | `env.Destinations()` 包含非 CPI 目标，无法预创建 |
| Logger + Notifier | 两者都注入到 Service | Logger 作为 `*zap.SugaredLogger` 字段；Notifier 定义接口包装 `notify` 包 |
| Handler 兼容策略 | 直接改 handler，不做 wrapper | 合并原阶段五的 handler 重构 |
| Handler 与 Service 的依赖关系 | 两层各自持有客户端引用 | Service 通过接口持有（可 mock），Handler 持有具体类型（passthrough 用） |

**4.2 Service 结构体与接口定义**

```go
// service/service.go
type TMSClient interface {
    GetNodes(ctx context.Context) ([]db.TransportNode, error)
    GetRoutes(ctx context.Context) ([]db.TransportRoute, error)
    ImportTransportRequest(ctx context.Context, nodeID uint, trs []uint) (uint, error)
    GetTransportRequest(ctx context.Context, TrNumber string) (*tms.TransportRequestV1, error)
    TrNodeStatuses(ctx context.Context, trNumber string) (map[uint]tms.TrNodeStatus, error)
    ErrLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error)
}

type CPIClient interface {
    DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error)
    RuntimeArtifact(ctx context.Context, artifactId string) (cpi.RuntimeArtifact, error)
    GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (cpi.IflowItem, error)
    GetDesignTimeScriptCollection(ctx context.Context, scID string, scVersion string) (cpi.ScriptCollectionItem, error)
}

type CPIFactory func(ctx context.Context, tenant string) (CPIClient, error)

type Notifier interface {
    SendApprovalRequest(to []string, drID uint, requestor string, description string) error
    SendDeliveryNotification(to []string, drID uint, status string, message string) error
    AddDeliveryComment(issueKey string, drID uint, message string, status string) error
}

type Service struct {
    DB           *gorm.DB
    Logger       *zap.SugaredLogger
    TMS          TMSClient
    CPI          CPIFactory
    GetUserEmail func(ctx context.Context, userID string) (string, error)
    Notifier     Notifier
}
```

接口设计说明：
- `TMSClient`（6 方法）和 `CPIClient`（4 方法）只包含 Service 层业务逻辑需要的方法
- Handler 层 passthrough 调用的方法（如 `GetNodeTransportRequests`、`GetPackages`）不在接口中，Handler 直接持有具体类型

**4.3 CPI Manager — 线程安全缓存**

```go
// pkg/cpi/manager.go
type Manager struct {
    mu      sync.RWMutex
    clients map[string]*CpiClient
}

func (m *Manager) Get(ctx context.Context, tenant string) (*CpiClient, error) {
    // Fast path: read lock
    m.mu.RLock()
    if cli, ok := m.clients[tenant]; ok {
        m.mu.RUnlock()
        return cli, nil
    }
    m.mu.RUnlock()
    // Slow path: write lock, double-check
    m.mu.Lock()
    defer m.mu.Unlock()
    if cli, ok := m.clients[tenant]; ok { return cli, nil }
    cli, err := NewClient(ctx, tenant)
    // ...
}
```

**4.4 Handler 结构体**

```go
// handler/handler.go
type Handler struct {
    svc          *service.Service
    db           *gorm.DB              // handler 层直接 CRUD
    logger       *zap.SugaredLogger
    tms          *tms.TmsClient        // 具体类型，passthrough
    cpi          *cpi.Manager          // 工厂，passthrough
    xsuaa        *xsuaa.UaaClient      // 具体类型，passthrough
    destinations map[string]env.Destination
}

func (h *Handler) SetupRoutes(v1, v2 *gin.RouterGroup, router *gin.Engine) { ... }
```

Handler 与 Service 同时持有 TMS/CPI 客户端引用的原因：Service 使用接口类型（面向测试/mock），Handler 使用具体类型（面向 API 转发）。底层指向同一个实例，但类型和语义不同。

**4.5 main.go 组装**

```go
func main() {
    ctx := context.Background()
    tmsClient, _ := tms.NewClient(ctx)
    xsuaaClient, _ := xsuaa.NewClient(ctx)
    cpiManager := cpi.NewManager()

    svc := &service.Service{
        DB: db.Conn(), Logger: env.Logger(), TMS: tmsClient,
        CPI: func(ctx context.Context, tenant string) (service.CPIClient, error) {
            return cpiManager.Get(ctx, tenant)
        },
        GetUserEmail: xsuaa.GetUserEmail,
        Notifier:     service.NewDefaultNotifier(),
    }

    h := handler.NewHandler(svc, db.Conn(), env.Logger(), tmsClient, cpiManager, xsuaaClient, env.Destinations())
    // ... router setup ...
    h.SetupRoutes(v1Group, v2Group, router)
}
```

**4.6 重构的文件清单**

**新建文件：**

| 文件 | 说明 |
|------|------|
| `service/service.go` | Service 结构体 + TMSClient/CPIClient/Notifier 接口 + defaultNotifier |
| `pkg/cpi/manager.go` | CPI client 缓存管理器（double-checked locking） |
| `handler/handler.go` | Handler 结构体 + NewHandler() + SetupRoutes() |

**Service 层重构（包级别函数 → `*Service` 方法）：**

| 文件 | 方法数 | 替换的全局依赖 |
|------|--------|---------------|
| `service/dr.go` | 12 | `db.Conn()` → `s.DB`，`tms.NewClient` → `s.TMS` |
| `service/deliver.go` | 5 | `db.Conn()` → `s.DB`，`tms.NewClient` → `s.TMS`，`cpi.NewClient` → `s.CPI`，`xsuaa.GetUserEmail` → `s.GetUserEmail` |
| `service/approve.go` | 3 | `db.Conn()` → `s.DB`，`xsuaa.GetUserEmail` → `s.GetUserEmail`，`notify.*` → `s.Notifier.*` |
| `service/cancel.go` | 1 | `db.Conn()` → `s.DB`，`notify.*` → `s.Notifier.*` |
| `service/sync_status.go` | 5 | `db.Conn()` → `s.DB`，`tms.NewClient` → `s.TMS`，`cpi.NewClient` → `s.CPI`，`notify.*` → `s.Notifier.*` |
| `service/checks.go` | 4 | `tms.NewClient` → `s.TMS`，`cpi.NewClient` → `s.CPI` |

保留为包级别的纯函数：`downstreamfromSource`（`dr.go`）、`checkVersionPattern`（`checks.go`）。
`service/utils.go` 中的 gin.Context 辅助函数（`UserID`、`UserEmail`、`UaaOrigin` 等）保留为包级别函数。

**Handler 层重构（包级别函数 → `*Handler` 方法）：**

| 文件 | 关键替换 |
|------|----------|
| `handler/delivery_request.go` | `service.XXX()` → `h.svc.XXX()`，`db.Conn()` → `h.db` |
| `handler/delivery_rule.go` | 同上 |
| `handler/approve_dr.go` | `service.XXX()` → `h.svc.XXX()` |
| `handler/cpi_handler.go` | 移除 `var logger`，`cpi.NewClient()` → `h.cpi.Get()`，`env.Destinations()` → `h.destinations` |
| `handler/tms_handler.go` | `tms.NewClient()` → `h.tms`（复用长生命周期 client），`logger` → `h.logger` |
| `handler/cpi_tenant.go` | `db.Conn()` → `h.db` |
| `handler/uaa_handler.go` | `xsuaa.NewClient()` → `h.xsuaa` |
| `handler/native_deliver.go` | `cpi.NewClient()` → `h.cpi.Get()` |
| `handler/counts.go` | `db.Conn()` → `h.db` |
| `handler/ws.go` | `WsHandler` → `(h *Handler) WsHandler` |

保留为包级别的纯函数：`checkJIRA`（`delivery_request.go`）、`wrapArtifact`（`cpi_handler.go`）、`parseTenant`（`native_deliver.go`）。

**其他修改：**

| 文件 | 说明 |
|------|------|
| `pkg/xsuaa/uaa.go` | `userEmail` → `UserEmail`（导出） |
| `main.go` | 重写：显式创建所有依赖 → Service → Handler → SetupRoutes |

### 阶段五：移除 init() 副作用（✅ 已完成）

**目标**：`init()` 无副作用；所有有副作用的初始化在 `main()` 中显式执行

> 注：原阶段五中的 Handler 层重构已合并到阶段四完成。此阶段仅处理 `init()` 移除。

**5.1 `pkg/env/env.go` 重构**

| 改动 | 说明 |
|------|------|
| 移除 `init()` | 不再在 import 时执行 CF 环境读取和 HTTP 调用 |
| 新增 `Init() error` | 导出函数，在 `main()` 中显式调用，可返回 error |
| `Logger()` 加 nil 保护 | 调用前若未 `Init()`，自动创建 fallback logger |
| `initDestinations()` 返回 error | 不再 panic，错误向上传递 |

```go
// ❌ 重构前
func init() {
    appEnv, _ = cfenv.Current()   // panic on failure
    logger = NewLogger()
    initDestinations()            // HTTP call, panic on failure
}

// ✅ 重构后
func Init() error {
    appEnv, err = cfenv.Current()
    if err != nil { return fmt.Errorf("failed to load app env: %w", err) }
    logger = NewLogger()
    return initDestinations()     // returns error instead of panic
}
func Logger() *zap.SugaredLogger {
    if logger == nil { logger = NewLogger() }  // fallback for pre-Init() usage
    return logger
}
```

**5.2 `db/conn.go` 重构**

| 改动 | 说明 |
|------|------|
| 移除 `init()` | 不再在 import 时连接数据库 |
| 移除 `var logger = zapgorm2.New(env.Logger().Desugar())` | 包级别变量不再依赖 env |
| 新增 `Connect() (*gorm.DB, error)` | 导出函数，在 `main()` 中显式调用 |
| logger 移入 `Connect()` 内部 | 延迟创建，确保 `env.Init()` 已先执行 |

```go
// ❌ 重构前
var logger = zapgorm2.New(env.Logger().Desugar())  // package-level, triggers env.init()
func init() { /* connect DB, AutoMigrate, panic on failure */ }

// ✅ 重构后
func Connect() (*gorm.DB, error) {
    logger := zapgorm2.New(env.Logger().Desugar())  // local var, safe after env.Init()
    // connect DB, AutoMigrate, return error instead of panic
}
```

**5.3 包级别 `var logger = env.Logger()` 消除**

四个包有 `var logger = env.Logger()`，会在 import 时触发 env 初始化。改为 lazy function：

| 包 | 重构方式 |
|----|---------|
| `pkg/tms/tms.go` | `var logger = env.Logger()` → `func logger() *zap.SugaredLogger { return env.Logger() }` |
| `pkg/cpi/cpi.go` | 同上 |
| `pkg/xsuaa/uaa.go` | 同上 |
| `db/conn.go` | 移入 `Connect()` 内部作为局部变量 |

所有调用点 `logger.XXX` 改为 `logger().XXX`。

**5.4 `pkg/cpi/sync_github.go` 包级别变量修复**

```go
// ❌ 重构前 — import 时触发 env.Destinations()
var gitAuth = &auth.BasicAuth{
    Username: env.Destinations()["API_GIT_MMT_SCC"].User,
    Password: env.Destinations()["API_GIT_MMT_SCC"].Password,
}

// ✅ 重构后 — 延迟到运行时
func gitAuth() *auth.BasicAuth {
    return &auth.BasicAuth{
        Username: env.Destinations()["API_GIT_MMT_SCC"].User,
        Password: env.Destinations()["API_GIT_MMT_SCC"].Password,
    }
}
```

**5.5 `main.go` 显式初始化**

```go
func main() {
    if err := env.Init(); err != nil {
        panic("failed to initialize env: " + err.Error())
    }
    database, err := db.Connect()
    if err != nil {
        panic("failed to connect database: " + err.Error())
    }
    // ... create clients, service, handler ...
}
```

**5.6 测试验证**

`go test -v ./pkg/tms/...` 现在无需 `SKIP_DB_INIT=true` 即可运行，全部 10 个测试通过。import 任何包不再触发 panic。

**5.7 不需要修改的文件**

`pkg/notify/email.go` 和 `pkg/notify/jira.go` 中的 `env.Logger()` 和 `env.GetDestination()` 调用发生在运行时（方法内部），此时 `env.Init()` 已在 `main()` 中完成，无需修改。

### 阶段六：推广超时到 CPI 包 + 完善测试

| 步骤 | 任务 |
|------|------|
| 1 | `pkg/cpi/*.go` 所有方法添加超时 |
| 2 | 为 service 层编写使用 mock 的单元测试 |
| 3 | 统一超时配置管理 |
| 4 | 完善文档 |

---

## 4. 超时时间设计

| 操作类型 | 超时 | 说明 |
|----------|------|------|
| GET 请求（轻量） | 30s | GetNode, GetNodes, GetRoutes |
| GET 请求（重量） | 60s | GetActionResultLog, getTransportLogs |
| POST 请求 | 60s | ImportTransportRequest |

### 4.1 超时触发机制详解

#### 4.1.1 应用层调用链

以 `GetNodes` 为例：

```
Handler 层                    TMS 层                         env 层                        Go 标准库
─────────                    ──────                         ──────                        ─────────

ctx (来自 gin)
     │
     ▼
GetNodes(ctx)
     │
     │  childCtx, cancel := context.WithTimeout(ctx, 30s)
     │  // childCtx 内置了一个 30s 倒计时定时器
     │
     ▼
t.Do(childCtx, &request)
     │
     ▼
doRequest(childCtx, ...)
     │
     │  req, _ = http.NewRequestWithContext(childCtx, ...)
     │  // 把带超时的 childCtx 绑到 http.Request 上
     │
     ▼
c.HttpClient.Do(req)     ←── Go 的 net/http 内部实现（见 4.1.2）
     │
     │  30s 到期 → net/http 检测到 context.Done()
     │  → 关闭 TCP 连接 → 返回 context.DeadlineExceeded
     │
     ▼
errReq != nil → errors.Is(errReq, context.DeadlineExceeded) → true
```

**`defer cancel()` 的作用**：不参与超时触发，是资源清理操作。`context.WithTimeout` 创建时启动内部 `time.Timer`，如果请求提前完成（如 2s），定时器仍运行到 30s 才被 GC。`cancel()` 立即停掉定时器释放资源。Go 官方要求 `WithTimeout`/`WithCancel` 必须调用 cancel，否则 `go vet` 报警告。

#### 4.1.2 Go net/http 标准库内部实现

超时不是我们自己实现的，而是 Go 标准库 `net/http` 的内建机制。以下基于 Go 源码（[golang/go](https://github.com/golang/go)）分析完整链路。

**核心架构**：每个 HTTP/1.1 请求有 3 个协作 goroutine：
1. **Caller goroutine** — 运行 `persistConn.roundTrip()`，通过 `select` 编排请求
2. **writeLoop goroutine** — 向 TCP 连接写请求
3. **readLoop goroutine** — 从 TCP 连接读响应

Context 取消由 Caller goroutine 的 `select` 检测，然后**关闭底层 TCP 连接**，使其他 goroutine 的阻塞 I/O 立即失败。

**完整调用链**：

```
context.WithTimeout(ctx, 30s)  →  创建 ctx，内部启动 time.Timer
         │
         ▼
http.Client.Do(req)
  └─ Client.do()  →  Client.send()  →  send()
         │
         ├─ setRequestCancel()                    [src/net/http/client.go ~L353]
         │    若 Client.Timeout 更早则包装 ctx；
         │    否则保留用户 ctx 的 deadline（我们的场景）
         │
         ▼
Transport.RoundTrip(req)
  └─ Transport.roundTrip()                        [src/net/http/transport.go ~L621]
         │
         ├─ ctx, cancel := context.WithCancelCause(req.Context())   [~L693]
         │
         ├─ 循环顶部快速检查：                       [~L704]
         │    select {
         │    case <-ctx.Done():
         │        return nil, context.Cause(ctx)   // 已超时则立即返回
         │    default:
         │    }
         │
         ├─ getConn()                              [~L1555]
         │    select {
         │    case r := <-w.result:                 // 拿到连接
         │    case <-treq.ctx.Done():               // 等连接时超时
         │        return nil, context.Cause(treq.ctx)
         │    }
         │
         └─ persistConn.roundTrip()                [~L2903]
              │
              │  发送请求到 writeLoop 和 readLoop：
              │    pc.writech  <- writeRequest{...}
              │    pc.reqch    <- requestAndChan{...}
              │
              │  主 select 循环：                    [~L2999]
              │    ctxDoneChan := req.ctx.Done()
              │    for {
              │        select {
              │        case err := <-writeErrCh:     // 写完成
              │        case <-pcClosed:               // 连接关闭
              │        case re := <-resc:             // 收到响应
              │            return handleResponse(re)
              │
              │        case <-ctxDoneChan:            // ★ 超时触发点
              │            select {
              │            case re := <-resc:         // 竞争：响应刚好到达
              │                return handleResponse(re)
              │            default:
              │            }
              │            pc.cancelRequest(          // ★ 取消请求
              │                context.Cause(req.ctx) // = context.DeadlineExceeded
              │            )
              │        }
              │    }
```

**`pc.cancelRequest()` 的实现** （`transport.go ~L2264`）：

```go
func (pc *persistConn) cancelRequest(err error) {
    pc.mu.Lock()
    defer pc.mu.Unlock()
    pc.canceledErr = err                    // 存储 context.DeadlineExceeded
    pc.closeLocked(errRequestCanceled)
}
```

**`pc.closeLocked()` 的实现** （`transport.go ~L3082`）：

```go
func (pc *persistConn) closeLocked(err error) {
    // ...
    pc.conn.Close()      // ★ 关闭 TCP 连接 → 发送 FIN → 阻塞的 Read/Write 立即失败
    close(pc.closech)    // ★ 通知所有 goroutine
}
```

**错误回传路径**：

`pc.conn.Close()` 导致 readLoop/writeLoop 中阻塞的 I/O 返回错误（如 `use of closed network connection`）。但 `mapRoundTripError()`（`transport.go ~L2294`）优先返回存储的 `pc.canceledErr`（即 `context.DeadlineExceeded`），而非 I/O 错误：

```go
func (pc *persistConn) mapRoundTripError(...) error {
    if cerr := pc.canceled(); cerr != nil {
        return cerr    // 返回 context.DeadlineExceeded，而非 I/O 错误
    }
    // ...
}
```

**关键设计**：Go **不使用** `net.Conn.SetDeadline()` 实现 context 超时。而是通过 `select` 监听 `ctx.Done()` channel，触发后**强制关闭 TCP 连接**，然后用 `mapRoundTripError` 将 I/O 错误替换为 context 错误。

#### 4.1.3 context.WithTimeout 的 Done() 实现

`context.WithTimeout` 是如何让 `Done()` channel 在超时后关闭的？以下基于 Go 源码 [`src/context/context.go`](https://github.com/golang/go/blob/master/src/context/context.go) 分析。

**入口：`WithTimeout` → `WithDeadline` → `WithDeadlineCause`**

```go
// ~L704
func WithTimeout(parent Context, timeout time.Duration) (Context, CancelFunc) {
    return WithDeadline(parent, time.Now().Add(timeout))  // 相对时间 → 绝对 deadline
}
```

**核心数据结构**：

```go
// cancelCtx ~L426 — 拥有 done channel
type cancelCtx struct {
    Context
    mu       sync.Mutex
    done     atomic.Value          // chan struct{}，惰性创建，首次 cancel 时关闭
    children map[canceler]struct{} // 子 context 集合
    err      atomic.Value
    cause    error
}

// timerCtx ~L657 — 嵌入 cancelCtx，增加定时器
type timerCtx struct {
    cancelCtx                      // 继承 Done()、Err() 等方法
    timer    *time.Timer           // AfterFunc 定时器
    deadline time.Time
}
```

**`WithDeadlineCause` 的关键步骤** （~L632）：

```go
func WithDeadlineCause(parent Context, d time.Time, cause error) (Context, CancelFunc) {
    // 1. 若 parent deadline 更早，退化为 WithCancel（parent 会先触发）
    if cur, ok := parent.Deadline(); ok && cur.Before(d) {
        return WithCancel(parent)
    }

    // 2. 创建 timerCtx
    c := &timerCtx{deadline: d}
    c.cancelCtx.propagateCancel(parent, c)  // 注册为 parent 的子 context

    // 3. 若 deadline 已过，立即取消
    dur := time.Until(d)
    if dur <= 0 {
        c.cancel(true, DeadlineExceeded, cause)
        return c, func() { c.cancel(false, Canceled, nil) }
    }

    // 4. ★ 设置定时器 — 核心行
    c.mu.Lock()
    defer c.mu.Unlock()
    if c.err.Load() == nil {
        c.timer = time.AfterFunc(dur, func() {
            c.cancel(true, DeadlineExceeded, cause)   // dur 到期后自动调用
        })
    }
    return c, func() { c.cancel(true, Canceled, nil) }
}
```

**`Done()` — 惰性创建 channel**（双重检查锁定）（~L448）：

```go
func (c *cancelCtx) Done() <-chan struct{} {
    d := c.done.Load()
    if d != nil {
        return d.(chan struct{})         // 快速路径：已创建
    }
    c.mu.Lock()
    defer c.mu.Unlock()
    d = c.done.Load()                   // 锁内二次检查
    if d == nil {
        d = make(chan struct{})          // ← 惰性创建 channel
        c.done.Store(d)
    }
    return d.(chan struct{})
}
```

**定时器到期 → `timerCtx.cancel()` → `cancelCtx.cancel()` → 关闭 channel**：

```go
// timerCtx.cancel() ~L678
func (c *timerCtx) cancel(removeFromParent bool, err, cause error) {
    c.cancelCtx.cancel(false, err, cause)   // 委托给 cancelCtx 关闭 channel
    if removeFromParent {
        removeChild(c.cancelCtx.Context, c)
    }
    c.mu.Lock()
    if c.timer != nil {
        c.timer.Stop()                       // 释放定时器资源
        c.timer = nil
    }
    c.mu.Unlock()
}

// cancelCtx.cancel() ~L549 — 关闭 done channel 的实际位置
func (c *cancelCtx) cancel(removeFromParent bool, err, cause error) {
    c.mu.Lock()
    if c.err.Load() != nil {
        c.mu.Unlock()
        return                               // 幂等：已取消则直接返回
    }
    c.err.Store(err)                         // 存储 DeadlineExceeded
    c.cause = cause

    d, _ := c.done.Load().(chan struct{})
    if d == nil {
        c.done.Store(closedchan)             // 优化：无人调用过 Done()，存预关闭 channel
    } else {
        close(d)                             // ★ 关闭 channel，所有 <-ctx.Done() 立即解除阻塞
    }

    for child := range c.children {          // 级联取消所有子 context
        child.cancel(false, err, cause)
    }
    c.children = nil
    c.mu.Unlock()
}
```

`closedchan` 优化（~L423）：包级别预关闭的 channel 单例，避免为从未被 `Done()` 读取的 context 分配新 channel：

```go
var closedchan = make(chan struct{})
func init() { close(closedchan) }
```

**完整事件链**：

```
WithTimeout(parent, 30s)
  │
  └─ WithDeadlineCause(parent, time.Now().Add(30s), nil)
       │
       ├─ 创建 &timerCtx{deadline: d}（嵌入 cancelCtx）
       │
       ├─ propagateCancel(parent, c)  // 注册父子关系
       │
       └─ c.timer = time.AfterFunc(30s, func() { c.cancel(...) })
            │
            │  ... Go runtime 内部：30s 后定时器到期 ...
            │  （AfterFunc 在独立 goroutine 中执行回调）
            │
            └─ timerCtx.cancel(true, DeadlineExceeded, nil)
                 │
                 └─ cancelCtx.cancel(false, DeadlineExceeded, nil)
                      │
                      ├─ c.err.Store(DeadlineExceeded)
                      │
                      ├─ close(d)  ← ★ Done() channel 关闭
                      │               所有 select { case <-ctx.Done(): } 解除阻塞
                      │               net/http 的 persistConn.roundTrip() 检测到
                      │               → cancelRequest() → conn.Close() → 超时错误返回
                      │
                      └─ 级联取消所有子 context
```

---

## 5. 单元测试设计

### 5.1 当前测试（阶段一~二）

已有 `pkg/tms/tms_test.go`，使用 `httptest.NewServer` 模拟 TMS API：

| 测试用例 | 描述 |
|----------|------|
| `TestImportTransportRequest_Success` | 正常请求成功 |
| `TestImportTransportRequest_Timeout` | 请求超时返回 `DeadlineExceeded` |
| `TestImportTransportRequest_ContextCancellation` | Context 取消 |
| `TestGetNodes_Success` | GET 请求成功 |
| `TestGetNodes_Timeout` | GET 超时 |
| `TestGetActionResult_Success` | Action 查询成功 |
| `TestGetActionResult_Timeout` | Action 查询超时 |

**运行命令**：
```bash
go test -v ./pkg/tms/...
```

> 阶段五完成后，测试不再需要 `SKIP_DB_INIT=true`。import 任何包不再触发 `init()` panic。

### 5.2 阶段四后的测试（目标状态）

```go
func TestBatchImportTenantOps(t *testing.T) {
    mockDB := setupTestDB(t)           // SQLite in-memory 或 mock
    mockTMS := &MockTMSClient{...}     // 实现 TMS 接口

    svc := service.New(mockDB, zaptest.NewLogger(t).Sugar(), mockTMS, ...)
    err := svc.BatchImportTenantOps(ctx, drID)
    // 断言...
}
```

**运行命令（目标状态）**：
```bash
go test ./...   # 不需要任何环境变量
```

---

## 6. 风险评估

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| 超时时间设置过短 | 中 | 高 | 先设置宽松超时（60s），逐步收紧 |
| 阶段三 API 签名变更影响调用方 | 高 | 高 | 全项目统一修改，编译器会捕获遗漏 |
| 阶段四~五重构范围大 | 高 | 中 | 分文件重构，每次一个 service 文件 |
| goroutine 中的 context 传递遗漏 | 中 | 高 | 重点审查 `deliver.go`, `sync_status.go` |

---

## 7. 回滚方案

1. **阶段一~二**：将 `WithTimeout` 改回 `WithCancel`
2. **阶段三**：保留新签名，内部使用 `context.Background()`
3. **阶段四~五**：每个阶段可独立回滚（git revert），因为每阶段是完整可工作的状态

---

## 8. 已完成事项

- [x] 阶段一：ImportTransportRequest 超时试点
- [x] 阶段二：推广超时到所有 TMS 方法 + 单元测试
- [x] 阶段三：重构 HttpClient — Context 作为参数 + 修复 cacheClient
  - 移除全局 `cacheClient`，token 管理改为实例级（`sync.Mutex` 保护）
  - `Do()` 添加 `ctx context.Context` 参数，移除结构体中的 `Context`/`Ctx` 字段
  - 嵌入改为指针（`*env.HttpClient`），因含 `sync.Mutex` 不可复制
  - 401 刷新改为单次重试（不再递归）
  - 所有客户端包（tms/cpi/xsuaa）方法签名添加 `ctx`
  - 所有调用方（handler/service）适配新签名
  - **已知中间状态**：每次 `NewClient()` 都 `fetchToken()`，阶段四将 client 提升为长生命周期对象后解决
- [x] 阶段四：Service 层 + Handler 层 DI
  - **新建文件**：`service/service.go`（Service 结构体 + 接口定义 + defaultNotifier）、`pkg/cpi/manager.go`（线程安全 CPI client 缓存）、`handler/handler.go`（Handler 结构体 + SetupRoutes）
  - **Service 层**：6 个文件、30 个函数从包级别函数转为 `*Service` 方法；消除 `db.Conn()`、`tms.NewClient()`、`cpi.NewClient()`、`notify.*` 等全局依赖
  - **Handler 层**：10 个文件所有函数转为 `*Handler` 方法；消除 `db.Conn()`、`var logger`、每次请求 `NewClient()` 等模式
  - **main.go**：重写为显式依赖创建 + 注入。Client 在启动时创建一次成为长生命周期对象，解决了阶段三遗留的"每次 NewClient 都 fetchToken"问题
  - **CPI 多 tenant**：`cpi.Manager` 通过 `sync.RWMutex` + double-checked locking 延迟创建并缓存 client
  - **`pkg/xsuaa/uaa.go`**：`userEmail` → `UserEmail`（导出供 Service 注入）
  - **构建验证**：`go build ./...` 零错误通过
- [x] 阶段五：移除 init() 副作用
  - **`pkg/env/env.go`**：移除 `init()`，新增 `Init() error`；`Logger()` 加 nil 保护（未初始化时返回 nop logger）；`initDestinations()` 改为返回 error
  - **`db/conn.go`**：移除 `init()` 和包级别 `var logger`，新增 `Connect() (*gorm.DB, error)` 显式初始化函数
  - **包级别 `var logger` 消除**：`pkg/tms/tms.go`、`pkg/cpi/cpi.go`、`pkg/xsuaa/uaa.go` 中 `var logger = env.Logger()` 改为 `func logger() *zap.SugaredLogger`，所有 `logger.XXX` → `logger().XXX`
  - **`pkg/cpi/sync_github.go`**：`var gitAuth = ...` 改为 `func gitAuth()` 延迟获取，避免 import 时触发 env 初始化
  - **`main.go`**：添加 `env.Init()` 和 `db.Connect()` 显式调用，移除 `var logger = env.Logger().Desugar()`
  - **测试验证**：`go test -v ./pkg/tms/...` 全部 10 个测试通过，无需 `SKIP_DB_INIT=true`
  - **构建验证**：`go build ./...` 零错误通过

---

## 9. 待确认事项（已决定）

1. [x] **实施节奏**：先实施阶段三（Context 作为参数），后续阶段逐步推进
2. [x] **接口定义**：阶段四定义 `TMSClient`（6 方法）和 `CPIClient`（4 方法）接口，只覆盖 Service 层需要的方法；Handler 层 passthrough 使用具体类型
3. [x] **统一配置管理**：需要。将 `env.TmsCredential()`, `env.UaaCredential()`, `env.Destinations()`, `env.PostgreUri()` 统一到 `Config` 结构体
4. [x] **`cacheClient` 处理**：替换为实例级 token 管理 + `sync.Mutex` 保护，在阶段三中一并完成。当前问题：非线程安全（data race）、无主动过期、401 刷新无重试上限
5. [x] **CPI 多 tenant 策略**：带缓存的工厂函数。`cpi.Manager` 使用 `sync.RWMutex` + `map` 延迟创建，阶段四实现
6. [x] **Logger + Notifier 注入**：两者都注入 Service。Logger 为 `*zap.SugaredLogger` 字段，Notifier 定义接口、`defaultNotifier` 包装 `notify` 包函数
7. [x] **Handler 兼容策略**：直接改 handler（不做 wrapper），合并原阶段五的 handler 重构到阶段四
8. [x] **Handler 与 Service 依赖重复**：保持现状。Service 持有接口（可 mock），Handler 持有具体类型（passthrough），底层同一实例，语义不同

---

## 10. 附录

### 10.1 已修改的文件

**阶段一~二**：
- `pkg/tms/tms.go` — 添加超时常量 + 所有方法添加超时
- `pkg/tms/v1.go` — GetTransportRequest 方法添加超时
- `pkg/tms/tms_test.go` — 单元测试

**阶段三**：
- `pkg/env/remotecall.go` — 重写：移除全局 cacheClient、Context/Ctx 字段；Do() 接受 ctx；fetchToken() + sync.Mutex；401 单次重试
- `pkg/env/env.go` — 更新 initDestinations 中的 HttpRequest 和 Do() 调用
- `pkg/tms/tms.go` — 嵌入改为 `*env.HttpClient`；所有方法签名添加 ctx
- `pkg/tms/v1.go` — 所有方法签名添加 ctx
- `pkg/tms/tms_test.go` — 适配指针嵌入和 ctx 参数
- `pkg/cpi/cpi.go` — 嵌入改为 `*env.HttpClient`；所有方法签名添加 ctx
- `pkg/cpi/sync_github.go` — DownloadArtifact, UploadArtifact, PublishToGithubRelease 添加 ctx
- `pkg/xsuaa/uaa.go` — 嵌入改为 `*env.HttpClient`；GetUserEmail/UserInfo/SearchByEmail 添加 ctx
- `service/dr.go` — SourceAndRoute, GenRouteForRule 添加 ctx
- `service/deliver.go` — goroutine 内传入 context.Background()
- `service/sync_status.go` — 传入 context.Background()
- `service/checks.go` — 传入 context.Background()
- `service/approve.go` — GetUserEmail 传入 context.Background()
- `service/cancel.go` — 同上
- `handler/tms_handler.go` — 传递 gin ctx 给 TMS 方法
- `handler/cpi_handler.go` — 传递 gin ctx 给 CPI 方法
- `handler/uaa_handler.go` — 传递 gin ctx 给 XSUAA 方法
- `handler/native_deliver.go` — 传递 gin ctx 给 CPI 方法
- `handler/delivery_rule.go` — SourceAndRoute 调用传入 ctx

**阶段四**：
- `service/service.go` — 新建：Service 结构体 + TMSClient/CPIClient/Notifier 接口 + defaultNotifier
- `pkg/cpi/manager.go` — 新建：CPI client 缓存管理器（`sync.RWMutex` + double-checked locking）
- `handler/handler.go` — 新建：Handler 结构体 + NewHandler() + SetupRoutes()
- `service/dr.go` — 12 个函数转为 `*Service` 方法
- `service/deliver.go` — 5 个函数转为方法
- `service/approve.go` — 3 个函数转为方法
- `service/cancel.go` — 1 个函数转为方法
- `service/sync_status.go` — 5 个函数转为方法
- `service/checks.go` — 4 个函数转为方法
- `handler/delivery_request.go` — 所有函数转为 `*Handler` 方法；`db.Conn()` → `h.db`
- `handler/delivery_rule.go` — 同上
- `handler/approve_dr.go` — `service.XXX()` → `h.svc.XXX()`
- `handler/cpi_handler.go` — 移除 `var logger`；`cpi.NewClient()` → `h.cpi.Get()`；`env.Destinations()` → `h.destinations`
- `handler/tms_handler.go` — `tms.NewClient()` → `h.tms`；`logger` → `h.logger`
- `handler/cpi_tenant.go` — `db.Conn()` → `h.db`
- `handler/uaa_handler.go` — `xsuaa.NewClient()` → `h.xsuaa`
- `handler/native_deliver.go` — `cpi.NewClient()` → `h.cpi.Get()`
- `handler/counts.go` — `db.Conn()` → `h.db`
- `handler/ws.go` — `WsHandler` → `(h *Handler) WsHandler`
- `pkg/xsuaa/uaa.go` — `userEmail` → `UserEmail`（导出）
- `main.go` — 重写：显式依赖创建 → Service → Handler → SetupRoutes

**阶段五**：
- `pkg/env/env.go` — 移除 `init()`，新增 `Init() error`；`Logger()` 加 nil 保护；`initDestinations()` 返回 error
- `db/conn.go` — 移除 `init()` 和 `var logger`，新增 `Connect() (*gorm.DB, error)`
- `pkg/tms/tms.go` — `var logger = env.Logger()` → `func logger() *zap.SugaredLogger`；所有 `logger.XXX` → `logger().XXX`
- `pkg/tms/v1.go` — `logger.XXX` → `logger().XXX`
- `pkg/cpi/cpi.go` — `var logger = env.Logger()` → `func logger() *zap.SugaredLogger`；所有 `logger.XXX` → `logger().XXX`
- `pkg/cpi/sync_github.go` — `var gitAuth = ...` → `func gitAuth()`；`logger.Infof` → `logger().Infof`
- `pkg/xsuaa/uaa.go` — `var logger = env.Logger()` → `func logger() *zap.SugaredLogger`；所有 `logger.XXX` → `logger().XXX`
- `main.go` — 添加 `env.Init()` + `db.Connect()` 显式调用；移除 `var logger = env.Logger().Desugar()`

### 10.2 各阶段待修改的文件

**阶段四**（Service 层 + Handler 层 DI）— ✅ 已完成：

新建：
- `service/service.go` — Service 结构体 + TMSClient/CPIClient/Notifier 接口 + defaultNotifier
- `pkg/cpi/manager.go` — CPI client 缓存管理器（double-checked locking）
- `handler/handler.go` — Handler 结构体 + NewHandler() + SetupRoutes()

Service 层重构：
- `service/dr.go` — 12 个函数 → `*Service` 方法
- `service/deliver.go` — 5 个函数 → 方法
- `service/approve.go` — 3 个函数 → 方法
- `service/cancel.go` — 1 个函数 → 方法
- `service/sync_status.go` — 5 个函数 → 方法
- `service/checks.go` — 4 个函数 → 方法

Handler 层重构：
- `handler/delivery_request.go` — `service.XXX()` → `h.svc.XXX()`，`db.Conn()` → `h.db`
- `handler/delivery_rule.go` — 同上
- `handler/approve_dr.go` — `service.XXX()` → `h.svc.XXX()`
- `handler/cpi_handler.go` — 移除 `var logger`，`cpi.NewClient()` → `h.cpi.Get()`，`env.Destinations()` → `h.destinations`
- `handler/tms_handler.go` — `tms.NewClient()` → `h.tms`，`logger` → `h.logger`
- `handler/cpi_tenant.go` — `db.Conn()` → `h.db`
- `handler/uaa_handler.go` — `xsuaa.NewClient()` → `h.xsuaa`
- `handler/native_deliver.go` — `cpi.NewClient()` → `h.cpi.Get()`
- `handler/counts.go` — `db.Conn()` → `h.db`
- `handler/ws.go` — `WsHandler` → `(h *Handler) WsHandler`

其他：
- `pkg/xsuaa/uaa.go` — `userEmail` → `UserEmail`（导出）
- `main.go` — 重写：显式创建所有依赖 → Service → Handler → SetupRoutes

**阶段五**（移除 init 副作用）— ✅ 已完成：
- `pkg/env/env.go` — 移除 `init()`，新增 `Init() error`；`Logger()` 加 nil 保护（未初始化时返回 nop logger）；`initDestinations()` 改为返回 error
- `db/conn.go` — 移除 `init()` 和包级别 `var logger`，新增 `Connect() (*gorm.DB, error)` 显式初始化函数
- `pkg/tms/tms.go` — `var logger = env.Logger()` → `func logger() *zap.SugaredLogger`；所有 `logger.XXX` → `logger().XXX`
- `pkg/tms/v1.go` — `logger.XXX` → `logger().XXX`
- `pkg/cpi/cpi.go` — `var logger = env.Logger()` → `func logger() *zap.SugaredLogger`；所有 `logger.XXX` → `logger().XXX`
- `pkg/cpi/sync_github.go` — `var gitAuth = ...` → `func gitAuth()` 延迟获取；`logger.Infof` → `logger().Infof`
- `pkg/xsuaa/uaa.go` — `var logger = env.Logger()` → `func logger() *zap.SugaredLogger`；所有 `logger.XXX` → `logger().XXX`
- `main.go` — 添加 `env.Init()` + `db.Connect()` 显式调用；移除 `var logger = env.Logger().Desugar()`

### 10.3 参考资料

**Context 相关**：
- [Go Context 最佳实践](https://pkg.go.dev/context)
- [Go HTTP Client 超时配置](https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/)
- [Go Blog: Context](https://go.dev/blog/context)
- [`src/context/context.go` — `WithDeadlineCause`, `cancelCtx`, `timerCtx`](https://github.com/golang/go/blob/master/src/context/context.go) — Done() channel 惰性创建 + 定时器关闭机制

**net/http 源码（超时实现）**：
- [`src/net/http/client.go` — `setRequestCancel()`](https://github.com/golang/go/blob/master/src/net/http/client.go) — 包装 context deadline
- [`src/net/http/transport.go` — `roundTrip()`](https://github.com/golang/go/blob/master/src/net/http/transport.go) — 主 select 循环监听 `ctx.Done()`
- [`src/net/http/transport.go` — `persistConn.roundTrip()`](https://github.com/golang/go/blob/master/src/net/http/transport.go) — 超时触发 → `cancelRequest()` → `conn.Close()`
- [`src/net/http/transport.go` — `mapRoundTripError()`](https://github.com/golang/go/blob/master/src/net/http/transport.go) — 将 I/O 错误替换为 `context.DeadlineExceeded`

**init() 反模式**：
- [100 Go Mistakes: #3 滥用 init 函数](https://100go.co/3-init-functions/)

**依赖注入**：
- [Go Blog: Wire - Compile-time DI](https://go.dev/blog/wire)
- [Uber Go Style Guide: Dependency Injection](https://github.com/uber-go/guide/blob/master/style.md)
