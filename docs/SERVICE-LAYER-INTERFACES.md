# Service-Layer Interfaces: TransportService & IntegrationService

## 目录

- [1. Go 的隐式接口：是的，这就是你理解的那样](#1-go-的隐式接口是的这就是你理解的那样)
- [2. 为什么 Go 选择了隐式接口](#2-为什么-go-选择了隐式接口)
  - [2.1 设计初衷：反对类型层级](#21-设计初衷反对类型层级)
  - [2.2 核心优势：事后定义接口](#22-核心优势事后定义接口)
  - [2.3 技术实现：静态检查的 Duck Typing](#23-技术实现静态检查的-duck-typing)
  - [2.4 权威资料汇总](#24-权威资料汇总)
  - [2.5 Go 接口 vs Java 接口](#25-go-接口-vs-java-接口)
  - [2.6 Method vs Function：何时用哪个](#26-method-vs-function何时用哪个)
- [3. 隐式接口的核心用途（不仅仅是测试）](#3-隐式接口的核心用途不仅仅是测试)
  - [3.1 标准库中的单方法接口](#31-标准库中的单方法接口)
  - [3.2 事后定义接口](#32-事后定义接口after-the-fact-abstraction)
  - [3.3 跨包解耦](#33-跨包解耦zero-import-decoupling)
  - [3.4 "Accept interfaces, return structs" 模式](#34-accept-interfaces-return-structs-模式)
  - [3.5 能力对比总结](#35-能力对比总结)
- [4. 为什么接口只包含部分方法](#4-为什么接口只包含部分方法)
  - [4.1 完整的方法调用映射](#41-完整的方法调用映射)
- [5. 两层为什么使用不同的类型](#5-两层为什么使用不同的类型)
- [6. 如果不用接口会怎样](#6-如果不用接口会怎样)
- [7. 本工程的 Service 层设计评估](#7-本工程的-service-层设计评估)
  - [7.1 设计原则](#71-设计原则)
  - [7.2 当前做得好的部分](#72-当前做得好的部分)
  - [7.3 需要改进的部分](#73-需要改进的部分)
  - [7.4 改进建议总览](#74-改进建议总览)
  - [7.5 判断标准：何时放 Service，何时放 Handler](#75-判断标准何时放-service何时放-handler)
- [8. 总结](#8-总结)
- [9. 接口化的核心场景：隔离 I/O 边界](#9-接口化的核心场景隔离-io-边界)
  - [9.1 "副作用"还是"外部依赖"？](#91-副作用还是外部依赖)
  - [9.2 更精确的判断标准：外部依赖](#92-更精确的判断标准外部依赖-external-dependency)
  - [9.3 副作用 ⊃ 外部依赖](#93-副作用--外部依赖)
  - [9.4 本工程的验证](#94-本工程的验证)
  - [9.5 务实的例外：为什么 DB 没有被接口化](#95-务实的例外为什么-db-没有被接口化)
  - [9.6 一句话总结](#96-一句话总结)

## 1. Go 的隐式接口：是的，这就是你理解的那样

> **只要任何类型实现了一个接口定义的所有方法，它就自动满足该接口。不需要任何显式声明。**
>
> **必须是全部方法——少一个编译器就会报错。** 方法签名（名称、参数类型、返回类型）也必须完全一致。多实现额外的方法不影响。

```go
// 接口定义（在 service 包）
type TransportService interface {
    GetNodes(ctx context.Context) ([]db.TransportNode, error)
    GetRoutes(ctx context.Context) ([]db.TransportRoute, error)
    // ... 共 6 个方法
}

// 具体类型（在 tms 包）——没有 "implements" 声明
type TmsClient struct { ... }
func (t *TmsClient) GetNodes(ctx context.Context) ([]db.TransportNode, error) { ... }
func (t *TmsClient) GetRoutes(ctx context.Context) ([]db.TransportRoute, error) { ... }
// ... TmsClient 有 13 个方法，其中 6 个签名恰好匹配 TransportService

// 编译器自动识别：TmsClient 满足 TransportService
var svc Service
svc.TMS = tmsClient  // 合法——无需任何额外声明
```

这意味着：
- `tms` 包完全不需要知道 `TransportService` 接口的存在
- 接口是由**消费方**（`service` 包）定义的，不是由实现方声明的
- 你甚至可以为标准库中的类型定义新接口，它们会自动满足

## 2. 为什么 Go 选择了隐式接口

### 2.1 设计初衷：反对类型层级

Go 的设计者（Rob Pike、Ken Thompson、Robert Griesemer）在 Google 内部大规模 C++/Java 项目中观察到一个反复出现的问题：**类型层级（type hierarchy）导致代码脆弱且难以演化。**

Rob Pike 在 2012 年 SPLASH 大会的主题演讲中明确解释了这个设计选择：

> "Type hierarchies result in brittle code. The hierarchy must be designed early, often as the first step of designing the program, and early decisions can be difficult to change once the program is written. As a consequence, the model encourages early overdesign as the programmer tries to predict every possible use the software might require, adding layers of type and abstraction just in case. This is upside down."
>
> "The way pieces of a system interact should adapt as it grows, not be fixed at the dawn of time."
>
> "Note too that the elimination of the type hierarchy also eliminates a form of dependency hierarchy. Interface satisfaction allows the program to grow organically without predetermined contracts."
>
> — Rob Pike, [Go at Google: Language Design in the Service of Software Engineering](https://go.dev/talks/2012/splash.article) (2012)

### 2.2 核心优势：事后定义接口（After-the-fact Interfaces）

Go 官方 FAQ 给出的直接解释：

> "A Go type implements an interface by implementing the methods of that interface, nothing more. This property allows interfaces to be defined and used without needing to modify existing code. It enables a kind of structural typing that promotes separation of concerns and improves code re-use, and makes it easier to build on patterns that emerge as the code develops."
>
> — [Go FAQ: Why doesn't Go have "implements" declarations?](https://go.dev/doc/faq#implements_interface)

关于为什么没有类型继承：

> "Rather than requiring the programmer to declare ahead of time that two types are related, in Go a type automatically satisfies any interface that specifies a subset of its methods. Besides reducing the bookkeeping, this approach has real advantages... Interfaces can be added after the fact if a new idea comes along or for testing -- without annotating the original types. Because there are no explicit relationships between types and interfaces, there is no type hierarchy to manage or discuss."
>
> "It takes some getting used to but this implicit style of type dependency is one of the most productive things about Go."
>
> — [Go FAQ: Why is there no type inheritance?](https://go.dev/doc/faq#inheritance)

### 2.3 技术实现：静态检查的 Duck Typing

Russ Cox（Go 团队技术负责人）在他的经典文章中解释了底层机制：

> "Go's interfaces let you use duck typing like you would in a purely dynamic language like Python but still have the compiler catch obvious mistakes like passing an int where an object with a Read method was expected."
>
> "A value of type Binary can be passed to ToString, which will format it using the String method, even though the program never says that Binary intends to implement Stringer. There's no need: the runtime can see that Binary has a String method, so it implements Stringer, even if the author of Binary has never heard of Stringer."
>
> — Russ Cox, [Go Data Structures: Interfaces](https://research.swtch.com/interfaces) (2009)

### 2.4 权威资料汇总

| 资料 | 作者 | 链接 |
|------|------|------|
| Go FAQ: 为什么没有 implements 声明 | Go Team | https://go.dev/doc/faq#implements_interface |
| Go FAQ: 为什么没有类型继承 | Go Team | https://go.dev/doc/faq#inheritance |
| Go 语言规范: Interface types | Go Team | https://go.dev/ref/spec#Interface_types |
| Go Data Structures: Interfaces | Russ Cox | https://research.swtch.com/interfaces |
| SPLASH 2012 Keynote | Rob Pike | https://go.dev/talks/2012/splash.article |
| The Laws of Reflection | Rob Pike | https://go.dev/blog/laws-of-reflection |

### 2.5 Go 接口 vs Java 接口

| 特性 | Go | Java |
|------|------|------|
| 接口满足方式 | **隐式**（结构化类型） | **显式**（`implements` 关键字） |
| 接口定义位置 | 通常在**消费方**包中 | 通常在**实现方**包或独立包中 |
| 接口大小倾向 | 越小越好（1-5 个方法） | 经常是完整的 API 表面 |
| 能否事后添加接口 | 可以，不需要修改已有代码 | 不行，必须回去修改类的声明 |
| 类型能满足多少接口 | 无限个（只要方法签名匹配） | 必须显式声明每一个 |

**根本区别**：Java 的接口是实现方的**承诺**（"我实现了这个接口"），Go 的接口是消费方的**要求**（"我需要这些能力"）。Go 的设计让系统可以在不修改已有代码的情况下自然演化。

### 2.6 Method vs Function：何时用哪个

注意：**只有 method 才能满足接口，function 不行。** 但 Go 并不鼓励一律使用 method，判断标准是是否需要状态：

| 情况 | 选择 | 例子 |
|------|------|------|
| 依赖状态（连接、配置、缓存） | Method | `client.Get(url)` — 需要 token、timeout 等 |
| 纯计算/转换，只依赖输入参数 | Function | `strings.ToUpper("hello")`、`strconv.Atoi("42")` |
| 需要满足接口或支持多态 | Method | `TmsClient` 的方法满足 `TransportService` 接口 |

## 3. 隐式接口的核心用途（不仅仅是测试）

可测试性只是隐式接口的用途之一。以下是它在 Go 生态中更广泛的核心应用：

### 3.1 标准库中的单方法接口

Go 标准库大量使用极小的隐式接口，它们是整个生态的基石：

**`io.Reader` / `io.Writer`** — 通用 I/O 组合

```go
type Reader interface { Read(p []byte) (n int, err error) }
type Writer interface { Write(p []byte) (n int, err error) }
```

`*os.File`、`*bytes.Buffer`、`*strings.Reader`、`net.Conn`、`*gzip.Writer`、`http.Response.Body` 全都自动满足这些接口。它们来自不同的包、不同的作者——但可以无缝组合：

```go
// 这一个函数适用于：文件到文件、网络到文件、HTTP body 到磁盘、gzip 流到 buffer...
func ProcessData(src io.Reader, dst io.Writer) error {
    _, err := io.Copy(dst, src)
    return err
}
```

**`error`** — Go 中最基本的隐式接口

```go
type error interface { Error() string }
```

任何类型只要有 `Error() string` 方法就是一个 error。你可以创建带有业务字段（错误码、字段名等）的自定义错误类型，它们自动被所有期望 `error` 的地方接受。

**`fmt.Stringer`** — 跨包的字符串表示

```go
type Stringer interface { String() string }
```

任何类型实现了 `String() string`，`fmt.Println`、`fmt.Sprintf("%v", ...)` 就会自动调用它——你的类型不需要 import `fmt`，`fmt` 也不需要知道你的类型存在。

### 3.2 事后定义接口（After-the-Fact Abstraction）

这是 Go 隐式接口与 Java 显式接口的**根本能力差异**。你可以在具体类型已经存在之后，为它们定义新接口——不需要修改任何已有代码。

```go
// 两个来自不同第三方包的类型，各自独立开发：
// package github:  func (c *Client) Get(url string) (*Response, error)
// package gitlab:  func (c *APIClient) Get(url string) (*Response, error)

// 你事后定义一个接口——两个已有类型自动满足它：
type HTTPGetter interface {
    Get(url string) (*Response, error)
}

func FetchREADME(client HTTPGetter, repo string) (string, error) { ... }

// 两个都能直接传入，无需修改 github 或 gitlab 包：
FetchREADME(&github.Client{}, "repo")
FetchREADME(&gitlab.APIClient{}, "repo")
```

**在 Java 中这不可能**——你必须回去修改 `github.Client` 和 `gitlab.APIClient` 的源码，添加 `implements HTTPGetter`。如果它们是第三方库，你根本改不了。

### 3.3 跨包解耦（Zero-Import Decoupling）

包 A 定义接口，包 B 的类型满足它——**双方互不 import**：

```go
// package notifier — 定义它需要的能力，不 import email 或 slack
type MessageSender interface {
    Send(to, body string) error
}

// package email — 不知道 notifier 的存在
type SMTPClient struct { Host string }
func (c *SMTPClient) Send(to, body string) error { /* SMTP 逻辑 */ }

// package slack — 也不知道 notifier 的存在
type Webhook struct { URL string }
func (w *Webhook) Send(to, body string) error { /* POST 到 Slack */ }

// main — 唯一同时 import 三者的地方，负责组装
svc := notifier.NewService(&email.SMTPClient{Host: "smtp.example.com"})
svc := notifier.NewService(&slack.Webhook{URL: "https://hooks.slack.com/..."})
```

依赖方向：`main` → `notifier`、`email`、`slack`。但 `notifier` 不依赖 `email` 或 `slack`，`email`/`slack` 不依赖 `notifier`。

### 3.4 "Accept interfaces, return structs" 模式

> **这是 Go 函数签名设计中最重要的一条原则。**

Go 社区推崇的函数签名原则：**参数用接口（接受最小能力集），返回值用具体类型（给调用方完整能力）**。

为什么这条原则如此重要？因为它同时解决了两个方向的问题：

- **参数用接口** → 调用方灵活：任何满足接口的类型都能传入，函数的适用范围最大化
- **返回值用具体类型** → 调用方强大：拿到完整类型，可以访问所有字段和方法，不丢失信息

如果反过来做（参数用具体类型，返回值用接口），两边都会受限：调用方必须构造特定类型，拿到的返回值又只有接口定义的子集能力。

```go
// 不好：接受具体类型，只能处理文件
func CountWords(f *os.File) (int, error) { ... }

// 好：接受 io.Reader，适用于文件、字符串、HTTP body、gzip 流...
func CountWords(r io.Reader) (int, error) {
    scanner := bufio.NewScanner(r)
    count := 0
    for scanner.Scan() {
        count += len(strings.Fields(scanner.Text()))
    }
    return count, scanner.Err()
}

// 全部可用：
CountWords(os.Stdin)
CountWords(strings.NewReader("hello world"))
CountWords(resp.Body)
CountWords(gzipReader)
```

#### 本工程实例：`FetchPackageArtifacts`

阶段一重构产生的 `service/artifacts.go:FetchPackageArtifacts` 正是这条原则的体现：

```go
// 参数: IntegrationService（接口）— 接受任何满足接口的 CPI client
// 返回: []db.Artifact（具体类型）— 给调用方完整的 Artifact 结构体
func FetchPackageArtifacts(ctx context.Context, client IntegrationService, packageID string) ([]db.Artifact, error)
```

- Handler 层传入 `*cpi.CpiClient`（通过 `cpi.Manager.Get()` 获得）→ 满足 `IntegrationService`
- Version Compare 的 Trigger 通过 `IntegrationFactory` 获得的 client → 同样满足 `IntegrationService`
- 测试时传入 mock → 同样满足 `IntegrationService`

**一个函数，三个完全不同的调用场景，零代码修改。** 这就是"参数用接口"的威力。

### 3.5 能力对比总结

| 能力 | Go（隐式） | Java（显式） |
|------|-----------|-------------|
| 类型满足它不知道的接口 | 可以 | 不行 |
| 类型已存在后定义新接口 | 可以，不改已有代码 | 不行，必须修改所有实现类 |
| 包 A 使用包 B 的类型，B 不 import A | 可以 | 不行，B 必须 import A 的接口 |
| 组合来自不相关包的类型 | 自动 | 需要 adapter 或共享接口 jar |
| 消费方定义接口 | 自然惯用法 | 需要额外包装类 |

## 4. 为什么接口只包含部分方法

以 `TransportService` 为例：`tms.TmsClient` 有 13 个方法，但 `TransportService` 只定义了 6 个。

**这是 Go 社区推崇的接口隔离原则（Interface Segregation Principle）**：

> **"The bigger the interface, the weaker the abstraction"** — Rob Pike

接口只包含消费方（Service 层）实际调用的方法。如果包含全部 13 个：
- 写 mock 时需要实现 13 个方法，即使测试只关心其中 1-2 个
- 接口变成了具体类型的 "镜像"，失去了抽象的意义
- 新增一个 `TmsClient` 方法就需要同步修改接口，耦合度反而增加

### 4.1 完整的方法调用映射

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

## 5. 两层为什么使用不同的类型

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
- **Handler 层**持有**具体类型**：handler 很多路由只是 passthrough（收到 HTTP 请求 → 调 client → 返回 JSON），不需要 mock 测试

## 6. 如果不用接口会怎样

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

## 7. 本工程的 Service 层设计评估

### 7.1 设计原则

你对 `service/` 目录的设计初衷是正确的：

> **将涉及调用多个 client 或 DB 的复杂业务逻辑放在 Service 层，简单的 passthrough 留在 Handler 层。**

这是标准的分层架构模式，适用于 Go 项目。关键判断标准：**一个操作是否需要编排（orchestrate）多个外部依赖？**

### 7.2 当前做得好的部分

以下 Service 方法正确地体现了"编排多个依赖"的设计：

| Service 方法 | 编排的依赖 | 复杂度 |
|---|---|---|
| `SourceAndRoute` (dr.go) | TMS (GetRoutes + GetNodes) + BFS 图算法 | 高 |
| `InsertTenantOps` (dr.go) | DB + TMS (TrExist) + CPI (DeliveryRuleCheck) | 高 |
| `BatchImportTenantOps` (deliver.go) | DB + TMS (Import) + CPI (版本检查) + Email | 高 |
| `BatchDeployTenantOps` (deliver.go) | DB + CPI (DeployArtifact) + Email | 高 |
| `SyncDeliveryStatus` (sync_status.go) | DB + TMS (TrNodeStatuses) + CPI (RuntimeArtifact) + JIRA | 最高 |
| `RequestApproval` (approve.go) | DB + XSUAA (Email解析) + Notifier | 中 |
| `CancelDeliveryRequest` (cancel.go) | DB + TMS + CPI (状态同步) + Notifier (JIRA + Email) | 高 |

以下 Handler 正确地保持了 passthrough 的简洁：

| Handler | 行为 |
|---|---|
| `GetPackagesHandler` | CPI GetPackages → JSON |
| `GetTmsNodesHandler` | TMS GetNodes → JSON |
| `HandleImportOps` | 参数校验 → `svc.BatchImportTenantOps()` |
| `HandleApproveDeliveryRequest` | 参数校验 → `svc.Approve()` |

### 7.3 需要改进的部分

分析发现部分 Handler 包含了本该在 Service 层的业务逻辑：

#### 严重：`handler/native_deliver.go` — `NativeDeliver`

这是最大的违规。Handler 中包含了完整的多系统编排流水线：
1. 创建源 CPI client
2. 遍历 artifact，逐一调用 CPI 获取 designtime 元数据
3. 下载 artifact → 同步到 GitHub
4. 遍历目标 tenant，上传 artifact → 轮询部署状态（阻塞 `time.Sleep`，最多 50 秒）

**应迁移到 `service/native_deliver.go`**，使 Handler 缩减为：
```go
func (h *Handler) NativeDeliver(ctx *gin.Context) {
    var req NativeDeliverRequest
    if err := ctx.BindJSON(&req); err != nil { ... }
    if err := h.svc.NativeDeliver(ctx, req); err != nil { ... }
    ctx.JSON(200, gin.H{"status": "success"})
}
```

#### 中等：`CreateDr` / `UpdateDr`（delivery_request.go）

包含 JIRA 格式校验、状态机检查、DeliveryRule 加载、DB 写入等业务逻辑。应提取为 `service.CreateDeliveryRequest()` / `service.UpdateDeliveryRequest()`。

#### 中等：`UpsertDeliveryRule`（delivery_rule.go）

包含 `SourceAndRoute()` 调用 + DB 事务 + 关联管理。事务逻辑应迁移到 Service 层。

#### 轻微：`service/utils.go` 的位置

`UserID(ctx *gin.Context)`、`UserEmail(ctx *gin.Context)` 等函数依赖 `gin.Context`，是 HTTP 层工具。放在 `service/` 中导致 service 层反向依赖 HTTP 框架。应迁移到 `handler/context.go`。

### 7.4 改进建议总览

| 问题 | 严重度 | 当前位置 | 建议迁移到 |
|---|---|---|---|
| `NativeDeliver` 编排逻辑 | **严重** | `handler/native_deliver.go` | `service/native_deliver.go` |
| `CreateDr` 业务逻辑 | 中等 | `handler/delivery_request.go` | `service/dr.go` |
| `UpdateDr` 业务逻辑 | 中等 | `handler/delivery_request.go` | `service/dr.go` |
| `UpsertDeliveryRule` 事务 | 中等 | `handler/delivery_rule.go` | `service/dr.go` |
| `preDeliverCheck` 状态校验 | 轻微 | `handler/delivery_request.go` | `service/deliver.go` |
| `checkJIRA` 业务校验 | 轻微 | `handler/delivery_request.go` | `service/checks.go` |
| gin.Context 工具函数 | 架构 | `service/utils.go` | `handler/context.go` |

### 7.5 判断标准：何时放 Service，何时放 Handler

| 条件 | 放在哪里 |
|---|---|
| 调用单个 client 方法，直接返回结果 | Handler (passthrough) |
| 只有简单的 DB CRUD，没有业务校验 | Handler 可直接操作 |
| 涉及多个外部依赖的编排 | **Service** |
| 包含业务规则校验（状态机、版本比较、权限检查等） | **Service** |
| 包含 DB 事务（需要原子性的多步操作） | **Service** |
| 需要异步处理（goroutine + 状态回写） | **Service** |
| 需要发送通知（邮件、JIRA） | **Service** |

## 8. 总结

| 问题 | 答案 |
|---|---|
| Go 的接口满足规则？ | 任何类型实现了接口的全部方法就自动满足，无需显式声明 |
| 为什么这样设计？ | 避免类型层级的脆弱性，支持事后定义接口，兼顾 duck typing 灵活性和编译期安全性 |
| 接口的目的？ | Service 层可测试性——通过接口注入 mock 来单元测试业务逻辑 |
| 什么方法需要接口化？ | 跨越 I/O 边界的外部依赖调用（非"副作用"——后者是超集，日志等副作用通常不值得接口化） |
| DB 为什么没有接口化？ | GORM 链式 API 表面太大，接口化成本过高；SQLite 内存 DB 提供了可接受的测试方案（务实的 trade-off） |
| 函数签名原则？ | **参数用接口，返回值用具体类型**——调用方灵活性最大化，返回信息无损失 |
| 为什么不包含所有方法？ | Go 接口隔离原则——只定义消费方需要的最小方法集 |
| Service 层的设计原则是否是最佳实践？ | 是。"复杂编排逻辑放 Service，passthrough 留 Handler"是标准分层架构 |
| 需要调整的地方？ | `NativeDeliver` 应迁到 Service（严重），`CreateDr`/`UpdateDr`/`UpsertDeliveryRule` 的业务逻辑应迁到 Service（中等） |

## 9. 接口化的核心场景：隔离 I/O 边界

### 9.1 "副作用"还是"外部依赖"？

一个自然的直觉是：**需要被接口化的方法都是具有副作用的。** 这个观察方向正确，但不够精确。

在函数式编程语境下，"副作用"(side effect) 指函数除了返回值以外对外部世界产生的任何可观察影响：网络请求、磁盘 I/O、数据库写入、修改全局状态等。按这个定义，`IntegrationService` 的所有方法确实都有副作用——它们全部发 HTTP 请求到 CPI API。

但问题在于：

| 方法 | 副作用类型 | 改变了外部状态？ |
|---|---|---|
| `DeployArtifact` | HTTP POST | 是（触发部署） |
| `GetPackageIflows` | HTTP GET | 否（只读） |
| `GetRuntimeArtifacts` | HTTP GET | 否（只读） |
| `RuntimeArtifact` | HTTP GET | 否（只读） |

GET 请求是幂等的读操作，在日常理解中通常不被认为是"副作用"。但在 FP 严格定义下它们确实是（非纯函数，结果依赖外部状态，不可预测）。所以"副作用"这个词**方向正确但容易引起歧义**。

### 9.2 更精确的判断标准：外部依赖 (External Dependency)

需要被接口化的方法的真正共同特征是：**依赖外部系统，其行为在测试环境中不可控。**

```
需要接口化（跨越 I/O 边界）:
  CpiClient.GetPackageIflows()       → 依赖 CPI API（外部 HTTP 服务）
  CpiClient.DeployArtifact()         → 依赖 CPI API（外部 HTTP 服务）
  TmsClient.ImportTransportRequest() → 依赖 TMS API（外部 HTTP 服务）
  DB 操作                             → 依赖 PostgreSQL（外部数据库）

不需要接口化（纯内存计算）:
  WrapArtifact(type, item)            → 输入确定，输出确定，无外部依赖
  SourceAndRoute() 的 BFS 图算法部分   → 纯计算逻辑
  strings.ToUpper("hello")            → 标准库纯函数
```

`WrapArtifact` 没有副作用（纯函数），也不需要接口化——因为它没有外部依赖，测试时直接调用即可，结果完全可预测。这就是为什么在阶段一重构中，`WrapArtifact` 被提取为一个**导出函数**而非**接口方法**。

### 9.3 副作用 ⊃ 外部依赖

"副作用"是"外部依赖"的超集：

```
┌─────────────────────────────────────────┐
│ 副作用 (Side Effects)                    │
│                                          │
│  ┌──────────────────────────────────┐    │
│  │ 外部依赖 (External Dependencies) │    │
│  │                                   │    │
│  │  CPI API 调用  ← 已接口化        │    │
│  │  TMS API 调用  ← 已接口化        │    │
│  │  Email/JIRA    ← 已接口化        │    │
│  │  DB 读写       ← 未接口化*       │    │
│  │                                   │    │
│  └──────────────────────────────────┘    │
│                                          │
│  写日志     ← 通常不值得接口化            │
│  修改局部变量 ← 不需要接口化              │
│  读环境变量   ← 视情况而定                │
│                                          │
└─────────────────────────────────────────┘

* DB 是外部依赖但未接口化——这是务实的 trade-off，详见 9.5 节。
```

所有外部依赖调用都有副作用，但并非所有有副作用的代码都需要接口化。写日志也是副作用，但通常不值得为它定义接口。

### 9.4 本工程的验证

回看本工程的外部依赖，每一个都跨越了 I/O 边界：

| 外部依赖 | I/O 边界 | 是否接口化 | 方式 |
|---|---|---|---|
| CPI API | HTTP REST | 是 | `IntegrationService` (7 方法) |
| TMS API | HTTP REST | 是 | `TransportService` (6 方法) |
| Email / JIRA | SMTP / HTTP | 是 | `Notifier` (3 方法) |
| PostgreSQL | TCP/SQL | **否** | 直接持有 `*gorm.DB` |

前三者通过接口隔离，DB 直接持有具体类型——这不是疏忽，而是务实的 trade-off。

而不在接口中的方法（如 Handler 层的 passthrough 调用）同样跨越 I/O 边界，但它们不需要接口化——因为 Handler 层的设计目标不是可单元测试的业务逻辑，而是薄薄的 HTTP 胶水层。

**结论**：接口化 = I/O 边界隔离 + 消费方需要可测试性 + 抽象成本合理。三个条件需综合权衡。

### 9.5 务实的例外：为什么 DB 没有被接口化

`Service` struct 直接持有 `*gorm.DB`，而 TMS、CPI、Notifier 全部通过接口注入。这确实打破了"外部依赖应接口化"的一致性。但这是 Go 社区广泛接受的 trade-off，原因有三：

#### 原因一：GORM 的 API 表面太大

`*gorm.DB` 不是一个简单的 CRUD client。它是 query builder + ORM，调用方式是链式的：

```go
s.DB.Where("delivery_rule_id = ?", ruleID).
    Preload("SourceTenant").
    Preload("IncludedTenants").
    First(&rule)
```

要把这接口化，需要抽象 `Where`、`Preload`、`First`、`Create`、`Save`、`Delete`、`Model`、`Association`、`Transaction` 等几十个方法的组合行为。定义一个 `DatabaseService` 接口要么变成 `gorm.DB` 的完整镜像（违反接口隔离原则），要么只覆盖部分查询模式的 Repository 接口（需要为每个实体写一套方法）。

对比 TMS/CPI：它们的 API 是**离散的、可枚举的方法调用**（`GetNodes`、`DeployArtifact`），天然适合接口抽象。

#### 原因二：GORM 自身提供了测试方案

GORM 支持 SQLite 内存数据库：

```go
db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
db.AutoMigrate(&DeliveryRule{}, &VersionCompareSnapshot{}, ...)
svc := &Service{DB: db}  // 用真实的 gorm.DB，但后端是内存 SQLite
```

这给了接近真实行为的测试，不需要 mock。而 CPI/TMS 没有这种内存替代——它们是远程 HTTP 服务，**必须**通过接口 mock。

#### 原因三：Go 社区的主流实践

| 策略 | 适用场景 | 代价 |
|---|---|---|
| 直接持有 `*gorm.DB` | 中小型项目，GORM 用得深 | 零额外抽象，SQLite 内存测试 |
| Repository 接口 | 大型项目，需要切换存储后端 | 每实体写接口 + 实现 + mock |
| `sqlc` 生成类型安全查询 | 偏好 SQL 优先 | 需要维护 SQL 文件 |

Repository 模式大概长这样：

```go
type DeliveryRuleRepository interface {
    GetByID(ctx context.Context, id uint) (*db.DeliveryRule, error)
    GetWithTenants(ctx context.Context, id uint) (*db.DeliveryRule, error)
    List(ctx context.Context) ([]db.DeliveryRule, error)
}

type VersionCompareRepository interface {
    GetSnapshot(ctx context.Context, ruleID uint) (*db.VersionCompareSnapshot, error)
    UpsertSnapshot(ctx context.Context, snapshot *db.VersionCompareSnapshot) error
    AtomicSetRunning(ctx context.Context, ruleID uint, ...) (bool, error)
}
```

能让 service 层完全不依赖 GORM，测试时用纯内存 map 实现。但代价是**每个实体、每种查询模式都要写接口方法 + 实现**，对本项目规模来说工程量不成比例。

#### 判断标准：何时该考虑 Repository 抽象

如果未来出现以下信号，再引入 Repository 接口：

- 需要切换数据库引擎（PostgreSQL → MySQL / MongoDB）
- Service 层需要纯 mock 级别的 DB 测试（不想依赖 SQLite 行为差异）
- 查询逻辑重复严重，需要集中管理和复用
- 团队规模增大，需要更严格的分层边界

#### 对 9.3 图的修正

这个例外意味着 9.3 的集合关系图需要补充一个维度——**不是所有外部依赖都值得接口化**，决策取决于抽象成本与收益的比较：

| 外部依赖 | API 形状 | 内存替代方案 | 接口化成本 | 决策 |
|---|---|---|---|---|
| CPI API | 离散方法调用 | 无 | 低（7 个方法） | **接口化** |
| TMS API | 离散方法调用 | 无 | 低（6 个方法） | **接口化** |
| Email/JIRA | 离散方法调用 | 无 | 低（3 个方法） | **接口化** |
| PostgreSQL (GORM) | 链式 query builder | SQLite 内存 | 高（几十个组合） | **直接持有** |

### 9.6 一句话总结

> **接口化的核心场景是隔离 I/O 边界（外部系统依赖），使 service 层的业务逻辑可以独立于具体实现进行测试和演化。DB 是一个务实的例外——GORM 的链式 API 使接口抽象成本过高，而 SQLite 内存数据库提供了可接受的替代测试方案。**
