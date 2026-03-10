# Service-Layer Interfaces: TransportService & IntegrationService

## 1. 为什么需要这两个接口

`service.TransportService` 和 `service.IntegrationService` 是定义在 `service/service.go` 中的接口。它们的核心目的是：

**将 Service 层与具体的 HTTP client 实现解耦，使 Service 层的业务逻辑可以独立测试。**

没有这两个接口时，`Service` 结构体直接持有 `*tms.TmsClient` 和 `*cpi.CpiClient`。这意味着：
- 测试任何 Service 方法都必须有真实的 HTTP 服务端（TMS API / CPI API）
- 无法控制外部 API 的返回值来覆盖边界条件
- 测试变得缓慢、不稳定、不可重复

有了接口后，测试时可以注入 mock 实现，精确控制每个方法的返回值。

## 2. 为什么接口只包含部分方法

以 `TransportService` 为例：

```
tms.TmsClient 有 13 个方法（12 个导出 + 1 个未导出）
TransportService 接口只定义了 6 个方法
```

**接口只包含 Service 层实际调用的方法。** 这是 Go 语言接口设计的核心原则。

### 2.1 Go 的隐式接口（Implicit Interface）

Go 的接口满足是**隐式**的——不需要 `implements` 关键字。只要一个类型实现了接口定义的所有方法，它就自动满足该接口：

```go
// 接口定义（在 service 包）
type TransportService interface {
    GetNodes(ctx context.Context) ([]db.TransportNode, error)
    // ... 其他 5 个方法
}

// 具体类型（在 tms 包）——没有任何 "implements" 声明
type TmsClient struct { ... }
func (t *TmsClient) GetNodes(ctx context.Context) ([]db.TransportNode, error) { ... }
// ... TmsClient 还有很多其他方法

// TmsClient 自动满足 TransportService，因为它实现了接口要求的全部 6 个方法
var _ TransportService = (*tms.TmsClient)(nil) // 编译期验证
```

这与 Java/C# 的**显式接口**不同。Java 中必须声明 `class TmsClient implements TransportService`，且类必须实现接口的全部方法。Go 中接口是**由使用方定义的**，而不是由实现方声明的。

### 2.2 Interface Segregation Principle（接口隔离原则）

Go 社区推崇的设计原则：

> **"Accept interfaces, return structs"**
> **"The bigger the interface, the weaker the abstraction"** — Rob Pike

接口应该尽可能小，只包含消费方需要的方法。这被称为**接口隔离原则**（SOLID 的 I）。

如果 `TransportService` 包含了 `TmsClient` 的全部 13 个方法：
- 写 mock 时需要实现 13 个方法，即使测试只关心其中 1-2 个
- 接口变成了具体类型的 "镜像"，失去了抽象的意义
- 新增一个 `TmsClient` 方法就需要同步修改接口，耦合度反而增加

### 2.3 完整的方法调用映射

下表展示了每个具体方法被谁调用、是否在接口中：

#### TMS 方法

| TmsClient 方法 | 在 TransportService 中? | Handler 层调用 | Service 层调用 |
|---|---|---|---|
| `GetNodes` | **YES** | `tms_handler.go` (具体类型) | `dr.go` (通过接口) |
| `GetNodeID` | No | — | — |
| `GetNode` | No | — | — |
| `GetNodeName` | No | — | — |
| `GetRoutes` | **YES** | `tms_handler.go` (具体类型) | `dr.go` (通过接口) |
| `GetNodeTransportRequests` | No | `tms_handler.go` (具体类型) | — |
| `ImportTransportRequest` | **YES** | — | `deliver.go` (通过接口) |
| `GetActionResult` | No | — | — |
| `GetActionResultLog` | No | — | — |
| `getTransportLogs` | No (未导出) | — | — |
| `ErrLogsInTransportLog` | **YES** | — | `sync_status.go` (通过接口) |
| `GetTransportRequest` | **YES** | — | `checks.go` (通过接口) |
| `TrNodeStatuses` | **YES** | — | `sync_status.go` (通过接口) |

#### CPI 方法

| CpiClient 方法 | 在 IntegrationService 中? | Handler 层调用 | Service 层调用 |
|---|---|---|---|
| `GetPackages` | No | `cpi_handler.go` (具体类型) | — |
| `GetPackage` | No | — | — |
| `ImportPackage` | No | — | — |
| `GetPackageIflows` | No | `cpi_handler.go` (具体类型) | — |
| `GetPackageIflow` | No | — | — |
| `GetDesignTimeIflow` | **YES** | `native_deliver.go` (具体类型) | `checks.go` (通过接口) |
| `DeployIflow` | No | — | — |
| `DeployScriptCollection` | No | — | — |
| `DeployArtifact` | **YES** | — | `deliver.go` (通过接口) |
| `CheckDeployStatusByTaskID` | No | — | — |
| `DeleteIflow` | No | — | — |
| `GetPackageScriptcollections` | No | `cpi_handler.go` (具体类型) | — |
| `GetDesignTimeScriptCollection` | **YES** | `native_deliver.go` (具体类型) | `checks.go` (通过接口) |
| `DeleteScriptCollection` | No | — | — |
| `UndeployRuntimeArtifacts` | No | — | — |
| `GetRuntimeArtifacts` | No | `cpi_handler.go` (具体类型) | — |
| `CheckUndeployStatus` | No | — | — |
| `RuntimeArtifact` | **YES** | `native_deliver.go` (具体类型) | `sync_status.go` (通过接口) |
| `SyncToGithub` | No | `native_deliver.go` (具体类型) | — |
| `DownloadArtifact` | No | `native_deliver.go` (具体类型) | — |
| `UploadArtifact` | No | `native_deliver.go` (具体类型) | — |

**规律**：接口中的方法 = Service 层通过 `s.TMS` 或 `s.CPI` 调用的方法。Handler 层直接使用具体类型，不经过接口。

## 3. 两层为什么使用不同的类型

```
┌─────────────────────────────────────────────────┐
│                   main.go                        │
│  tmsClient := tms.NewClient(...)                │
│  cpiManager := cpi.NewManager()                 │
│                                                  │
│  svc.TMS = tmsClient     // 赋值给接口           │
│  h.tms   = tmsClient     // 赋值给具体类型       │
│                                                  │
│  底层是同一个实例                                  │
└─────────────────────────────────────────────────┘
         │                          │
         ▼                          ▼
┌──────────────────┐    ┌──────────────────────┐
│   Service 层      │    │     Handler 层        │
│                   │    │                       │
│ s.TMS (接口)      │    │ h.tms (*TmsClient)   │
│ 只能调用接口定义   │    │ 可以调用所有方法       │
│ 的 6 个方法        │    │                       │
│                   │    │ 包括 passthrough:      │
│ 可 mock 测试      │    │ GetNodeTransportReqs  │
│                   │    │ GetPackages 等         │
└──────────────────┘    └──────────────────────┘
```

- **Service 层**持有**接口**：只暴露业务逻辑需要的方法子集，方便 mock 测试
- **Handler 层**持有**具体类型**：因为 handler 很多路由只是简单的 passthrough（收到 HTTP 请求 → 调 client 方法 → 返回 JSON），不需要也不值得做 mock 测试

这是一个 intentional 的设计选择——不是所有调用都需要通过接口。只在**需要可测试性**的地方引入接口抽象。

## 4. Go 接口 vs Java/C# 接口的关键区别

| 特性 | Go | Java / C# |
|---|---|---|
| 接口满足方式 | **隐式**（duck typing） | **显式**（`implements`/`:`） |
| 接口定义位置 | 通常在**消费方**包中 | 通常在**实现方**包或独立包中 |
| 接口大小倾向 | 越小越好（1-5 个方法） | 经常是完整的 API 表面 |
| 一个类型能满足多少接口 | 无限个（只要方法匹配） | 必须显式声明每一个 |
| 空接口 | `interface{}` / `any` | `Object` |

Go 的隐式接口使得**在消费方按需定义小接口**成为自然且推荐的做法。你不需要在 `pkg/tms` 包中声明"我实现了 TransportService"——只要方法签名匹配，编译器自动完成关联。

## 5. 如果不用接口会怎样

```go
// 假设 Service 直接持有具体类型
type Service struct {
    TMS *tms.TmsClient  // 具体类型，而不是接口
}

// 测试时的困境：
func TestImportAndCheckStatus(t *testing.T) {
    // 必须创建真实的 TmsClient → 需要 HTTP server
    // 必须 mock 整个 HTTP 层（httptest.NewServer）
    // 无法精确控制 TmsClient 单个方法的行为
    // 如果想让 GetNodes 返回错误但 ImportTransportRequest 成功 → 很难实现
}
```

用接口后：

```go
type mockTMS struct{}
func (m *mockTMS) GetNodes(ctx context.Context) ([]db.TransportNode, error) {
    return []db.TransportNode{{ID: 1, Name: "DEV"}}, nil  // 精确控制返回值
}
func (m *mockTMS) ImportTransportRequest(...) (uint, error) {
    return 0, fmt.Errorf("simulated failure")  // 模拟失败场景
}
// ... 只需实现 6 个方法，不是 13 个

func TestDeliveryFlow(t *testing.T) {
    svc := &Service{TMS: &mockTMS{}}
    // 现在可以精确测试各种业务场景
}
```

## 6. 总结

| 问题 | 答案 |
|---|---|
| 接口的目的是什么？ | **Service 层可测试性**——通过接口注入 mock 来单元测试业务逻辑 |
| 为什么不包含所有方法？ | Go 接口隔离原则——只定义消费方需要的最小方法集 |
| 和具体 Client 冲突吗？ | 不冲突。`TransportService` 是抽象（行为契约），`TmsClient` 是实现。命名已区分 |
| Handler 层为什么不用接口？ | Handler 多为 passthrough，不需要 mock；直接用具体类型更简单直接 |
| 底层是同一个实例吗？ | 是。`main.go` 中 `tmsClient` 同时赋值给 `svc.TMS`（接口）和 `h.tms`（具体类型） |
