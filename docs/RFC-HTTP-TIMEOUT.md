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
  - [阶段三：重构 HttpClient](#阶段三重构-httpclient--context-作为参数--修复-cacheclient)
  - [阶段四：重构 Service 层](#阶段四重构-service-层--包级别函数--结构体方法)
  - [阶段五：重构 Handler 层 + 移除 init()](#阶段五重构-handler-层--移除-init)
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
| `var cacheClient map[string]*HttpClient` | `pkg/env/remotecall.go` | 可变的全局 OAuth2 客户端缓存 |
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
  ├── tms.NewClient(ctx)             [工厂函数，依赖 env 全局]
  ├── cpi.NewClient(ctx, tenant)     [工厂函数，依赖 env 全局]
  └── env.Logger()                   [包级别变量]
  │
service/*
  ├── db.Conn()                      [每个 service 文件都直接使用]
  ├── tms.NewClient(ctx.Background()) [在 goroutine 中创建]
  ├── cpi.NewClient(ctx.Background()) [在 goroutine 中创建]
  ├── xsuaa.GetUserEmail()           [全局单例]
  ├── notify.Send*()                 [每次创建新客户端]
  └── env.Logger()                   [包级别变量]
  │
pkg/tms, pkg/cpi, pkg/xsuaa
  ├── env.TmsCredential()            [读取 CF 环境]
  ├── env.NewClient()                [全局 cacheClient]
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

### 阶段三：重构 HttpClient — Context 作为参数 + 修复 cacheClient

**目标**：解决 Context 反模式和 cacheClient 线程安全问题

**3.1 修改 `pkg/env/remotecall.go`**

```go
// ❌ 当前
type HttpClient struct {
    Context     context.Context  // 存储在结构体中
    HttpClient  *http.Client
    AccessToken string
    ApiURL      string
    // ...
}

var cacheClient map[string]*HttpClient  // 全局可变缓存，非线程安全

func (c *HttpClient) Do(request *HttpRequest) (*[]byte, int, error)

// ✅ 重构后
type HttpClient struct {
    mu           sync.Mutex       // 保护 token 刷新
    HttpClient   *http.Client     // 去掉 Context 字段
    AccessToken  string
    ApiURL       string
    ClientId     string
    ClientSecret string
    AuthUrl      string
}

// Do 接受 ctx 作为第一个参数
func (c *HttpClient) Do(ctx context.Context, request *HttpRequest) (*[]byte, int, error)

// NewClient 不再使用全局缓存，改为直接创建实例
// 缓存由调用方（TmsClient/CpiClient 工厂）负责
func NewClient(ctx context.Context, clientID, clientSecret, authUrl, apiUrl string) (*HttpClient, error)
```

**3.1.1 cacheClient 迁移方案**

将全局 `cacheClient` 移除，改为在各客户端包内管理缓存：
- `pkg/tms/`: TMS 只有一个 clientID，由 `NewClient()` 返回单例
- `pkg/cpi/`: CPI 有多个 tenant，缓存放在调用方（service 层或未来的 factory）
- `pkg/xsuaa/`: 已有 `globalClient` 单例，保持不变

**3.1.2 token 刷新改为实例级**

```go
func (c *HttpClient) Do(ctx context.Context, request *HttpRequest) (*[]byte, int, error) {
    // ... 执行请求 ...
    if resp.StatusCode == 401 {
        c.mu.Lock()
        err := c.refreshToken(ctx)
        c.mu.Unlock()
        if err != nil {
            return nil, 0, err
        }
        return c.Do(ctx, request)  // 重试一次
    }
}

func (c *HttpClient) refreshToken(ctx context.Context) error {
    // 重新获取 OAuth2 token，更新 c.AccessToken
}
```

**3.2 修改 `pkg/tms/` 所有方法签名**

```go
// ❌ 当前
func (t *TmsClient) ImportTransportRequest(nodeID uint, trIDs []uint) (uint, error) {
    childCtx, cancel := context.WithTimeout(t.Context, ImportTimeout)
}

// ✅ 重构后
func (t *TmsClient) ImportTransportRequest(ctx context.Context, nodeID uint, trIDs []uint) (uint, error) {
    childCtx, cancel := context.WithTimeout(ctx, ImportTimeout)
}
```

**3.3 同步修改 `pkg/cpi/` 所有方法签名**

**3.4 更新所有调用方**

| 调用方 | 修改点 |
|--------|--------|
| `service/deliver.go` | goroutine 中传递 `context.Background()` 而非无超时的旧 context |
| `service/sync_status.go` | 同上 |
| `service/checks.go` | 同上 |
| `handler/tms_handler.go` | 使用 `c.Request.Context()` |
| `handler/cpi_handler.go` | 使用 `c.Request.Context()` |

### 阶段四：重构 Service 层 — 包级别函数 → 结构体方法

**目标**：Service 层不再直接依赖全局变量

**4.1 定义 Service 结构体**

```go
// service/service.go
type Service struct {
    DB     *gorm.DB
    Logger *zap.SugaredLogger
    TMS    func(ctx context.Context) (*tms.TmsClient, error)
    CPI    func(ctx context.Context, tenant string) (*cpi.CpiClient, error)
}

func New(db *gorm.DB, logger *zap.SugaredLogger, ...) *Service {
    return &Service{DB: db, Logger: logger, ...}
}
```

**4.2 将包级别函数转为方法**

```go
// ❌ 当前：包级别函数，直接使用 db.Conn()
func QueryDrWithAssociations(drID uint) (*db.DeliveryRequest, error) {
    var dr db.DeliveryRequest
    if err := db.Conn().Preload(...).First(&dr, drID).Error; err != nil {
        return nil, err
    }
    return &dr, nil
}

// ✅ 重构后：方法，使用注入的 DB
func (s *Service) QueryDrWithAssociations(drID uint) (*db.DeliveryRequest, error) {
    var dr db.DeliveryRequest
    if err := s.DB.Preload(...).First(&dr, drID).Error; err != nil {
        return nil, err
    }
    return &dr, nil
}
```

**4.3 涉及的文件和函数**

| 文件 | 函数数 | 全局依赖 |
|------|--------|----------|
| `service/dr.go` | ~11 | `db.Conn()`, `tms.NewClient` |
| `service/deliver.go` | ~3 | `db.Conn()`, `tms.NewClient`, `cpi.NewClient`, `xsuaa.GetUserEmail` |
| `service/approve.go` | ~2 | `db.Conn()`, `xsuaa.GetUserEmail`, `notify.*` |
| `service/cancel.go` | ~1 | `db.Conn()`, `xsuaa.GetUserEmail`, `notify.*` |
| `service/sync_status.go` | ~4 | `db.Conn()`, `tms.NewClient`, `cpi.NewClient`, `notify.*` |
| `service/checks.go` | ~4 | `tms.NewClient`, `cpi.NewClient` |

### 阶段五：重构 Handler 层 + 移除 init()

**目标**：Handler 持有 Service 引用；`init()` 无副作用

**5.1 定义 Handler 结构体**

```go
// handler/handler.go
type Handler struct {
    svc    *service.Service
    logger *zap.SugaredLogger
}

func New(svc *service.Service, logger *zap.SugaredLogger) *Handler {
    return &Handler{svc: svc, logger: logger}
}

func (h *Handler) SetupRoutes(r *gin.Engine) {
    v1 := r.Group("/api/v1")
    v1.GET("/dr/:drId", h.GetDr)
    v1.POST("/dr", h.CreateDr)
    // ...
}
```

**5.2 Handler 方法示例**

```go
// ❌ 当前：包级别函数
func GetDr(c *gin.Context) {
    dr, err := service.QueryDrWithAssociations(drID)
    // ...
}

// ✅ 重构后：方法
func (h *Handler) GetDr(c *gin.Context) {
    dr, err := h.svc.QueryDrWithAssociations(drID)
    // ...
}
```

**5.3 移除 init() 副作用**

```go
// ❌ 当前 pkg/env/env.go
func init() {
    appEnv, err = cfenv.Current()
    if err != nil { panic(...) }
    initDestinations()  // HTTP 调用
}

// ❌ 当前 db/conn.go
func init() {
    conn, err = sql.Open("pgx", dbUri)
    db, err = gorm.Open(...)
    db.AutoMigrate(...)
}

// ✅ 重构后 main.go
func main() {
    // 显式、可错误处理的初始化
    cfg, err := config.Load()       // 读取 CF env / 环境变量
    if err != nil { log.Fatal(err) }

    logger := logging.New(cfg)

    db, err := database.Connect(cfg)
    if err != nil { log.Fatal(err) }

    svc := service.New(db, logger, ...)
    h := handler.New(svc, logger)
    // ...
}
```

**5.4 涉及的文件**

| 文件 | 路由数 | 直接使用 db.Conn() |
|------|--------|-------------------|
| `handler/delivery_request.go` | ~8 | YES |
| `handler/delivery_rule.go` | ~4 | YES |
| `handler/cpi_handler.go` | ~5 | NO（创建 cpi client） |
| `handler/tms_handler.go` | ~3 | NO（创建 tms client） |
| `handler/cpi_tenant.go` | ~4 | YES |
| `handler/approve_dr.go` | ~2 | NO |
| `handler/counts.go` | ~2 | YES |
| `handler/ws.go` | ~1 | NO |
| `handler/native_deliver.go` | ~2 | NO（创建 cpi client） |
| `handler/uaa_handler.go` | ~1 | NO |

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
SKIP_DB_INIT=true go test -v ./pkg/tms/...
```

> 注：测试仍需 `SKIP_DB_INIT` 是因为 `pkg/tms/tms.go` import 了 `db` 包，触发 `db/conn.go` 的 `init()`。阶段四完成后将不再需要。

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

---

## 9. 待确认事项（已决定）

1. [x] **实施节奏**：先实施阶段三（Context 作为参数），后续阶段逐步推进
2. [x] **接口定义**：阶段四定义 `TMSService`（7 方法）和 `CPIService`（8 方法）+ `CPITransferService`（3 方法）接口，便于 mock 测试
3. [x] **统一配置管理**：需要。将 `env.TmsCredential()`, `env.UaaCredential()`, `env.Destinations()`, `env.PostgreUri()` 统一到 `Config` 结构体
4. [x] **`cacheClient` 处理**：替换为实例级 token 管理 + `sync.Mutex` 保护，在阶段三中一并完成。当前问题：非线程安全（data race）、无主动过期、401 刷新无重试上限

---

## 10. 附录

### 10.1 已修改的文件

**阶段一~二**：
- `pkg/tms/tms.go` — 添加超时常量 + 所有方法添加超时
- `pkg/tms/v1.go` — GetTransportRequest 方法添加超时
- `pkg/tms/tms_test.go` — 单元测试

### 10.2 各阶段待修改的文件

**阶段三**（Context 作为参数）：
- `pkg/env/remotecall.go` — HttpClient 移除 Context 字段，Do() 添加 ctx 参数
- `pkg/tms/tms.go` — 所有方法签名添加 ctx
- `pkg/tms/v1.go` — 所有方法签名添加 ctx
- `pkg/cpi/cpi.go` — 所有方法签名添加 ctx
- `pkg/cpi/sync_github.go` — 方法签名添加 ctx
- `pkg/xsuaa/uaa.go` — 方法签名添加 ctx
- `service/*.go` — 更新所有调用
- `handler/*.go` — 更新所有调用

**阶段四**（Service 层 DI）：
- `service/service.go` — 新增，定义 Service 结构体
- `service/dr.go` — 函数 → 方法
- `service/deliver.go` — 函数 → 方法
- `service/approve.go` — 函数 → 方法
- `service/cancel.go` — 函数 → 方法
- `service/sync_status.go` — 函数 → 方法
- `service/checks.go` — 函数 → 方法

**阶段五**（Handler 层 DI + 移除 init）：
- `handler/handler.go` — 新增，定义 Handler 结构体 + SetupRoutes
- `handler/*.go` — 函数 → 方法
- `main.go` — 重写，显式初始化所有依赖
- `pkg/env/env.go` — 移除 init()，改为导出构造函数
- `db/conn.go` — 移除 init()，改为导出构造函数

### 10.3 参考资料

**Context 相关**：
- [Go Context 最佳实践](https://pkg.go.dev/context)
- [Go HTTP Client 超时配置](https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/)
- [Go Blog: Context](https://go.dev/blog/context)

**init() 反模式**：
- [100 Go Mistakes: #3 滥用 init 函数](https://100go.co/3-init-functions/)

**依赖注入**：
- [Go Blog: Wire - Compile-time DI](https://go.dev/blog/wire)
- [Uber Go Style Guide: Dependency Injection](https://github.com/uber-go/guide/blob/master/style.md)
