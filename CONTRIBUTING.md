# Contributing to EquilibriaMarket

[English](#english) | [中文](#中文)

---

## English

Thank you for your interest in **EquilibriaMarket**! Whether you're reporting a typo, fixing a bug, or proposing a brand-new governance plugin, you're helping build a healthier multi-agent market economy.

### Code of Conduct

This project follows the [Contributor Covenant](https://www.contributor-covenant.org/) (in spirit — formal adoption pending). Be kind, assume good intent, and prioritize the project over ego.

### How to contribute

1. **Search existing issues** before opening a new one.
2. **Open an issue first** for non-trivial changes so we can discuss the design.
3. **Fork the repository** and create a feature branch:
   ```bash
   git checkout -b feat/your-feature
   ```
4. **Write code + tests** (see "Development Workflow" below).
5. **Run all checks** before pushing:
   ```bash
   go test ./...
   go test -race ./...
   go vet ./...
   gofmt -l .                          # should print nothing
   ```
6. **Commit with Conventional Commits** (see below).
7. **Open a Pull Request** against `main` and fill in the PR template.

### Commit message format (Conventional Commits)

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types**: `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `chore`, `build`, `ci`
**Scope** (optional): `engine`, `governance`, `metrics`, `openrtb`, `prebid`, `admin`, `marketd`, `docs`

**Examples**:

```
feat(governance): add ZK-rollup plugin skeleton
fix(auction): prevent float comparison overflow in SecondPriceAuction
docs(readme): translate "Quick Start" to English
perf(engine): avoid locking critical section in market snapshot
```

### Development workflow

#### Project layout

```
cmd/         # binary entry points (marketd, prebid-mock, admin-console)
pkg/         # library code (importable by external Go programs)
adapters/    # external integrations (Prebid, future: TikTok, Meta)
```

When adding a new package, follow these rules:

- **One concern per package** (e.g., `pkg/governance` for the interface, `pkg/governance/<impl>` for concrete implementations).
- **Public API needs godoc**: every exported symbol must have a comment starting with the symbol name.
- **Errors are values**: use `errors.New` or `fmt.Errorf` with `%w` for wrapping. Define sentinel errors in a `var (...)` block at the top of the file.
- **Concurrency**: shared mutable state MUST be guarded by a mutex (use `sync.RWMutex` for read-heavy). Run tests with `-race` to verify.

#### Test conventions

- Use **table-driven tests** when input space is finite and enumerable.
- Use **sub-tests** (`t.Run(name, func(t *testing.T) {...})`) for grouping.
- Use **`errors.Is`** for error assertions, never string comparison.
- Use **`testify/require`** and **`testify/assert`** for terse assertions (currently allowed as a dev dependency, will be moved to indirect later).
- Coverage target: **> 80%** for `pkg/**`, **> 60%** for `cmd/**`.

Example:

```go
func TestSecondPriceAuction_Table(t *testing.T) {
    tests := []struct {
        name      string
        bids      []types.BidResponse
        wantID    string
        wantPrice float64
        wantErr   bool
    }{
        {"happy path", ..., "bob", 80.01, false},
        {"single bidder", ..., "alice", 0.01, false},
        {"no bids", ..., "", 0, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

#### Adding a new governance plugin

1. Create `pkg/governance/<your-plugin>/`.
2. Implement the `governance.Plugin` interface (6 methods).
3. Register via `init()`:
   ```go
   func init() {
       governance.MustRegister("your-plugin", NewFactory)
   }
   ```
4. Add tests covering happy path, edge cases, and concurrent access.
5. Add a short section to `README.md` (Configuration table) and a `cmd/<demo>/main.go` if appropriate.

#### Adding a new HTTP endpoint

- Follow RESTful conventions (`/v1/resource`, `/v1/resource/{id}`).
- Return `application/json; charset=utf-8` with snake_case keys.
- Use `log/slog` for structured logs.
- Add an entry in the API table of `README.md`.

#### Branching & releases

- `main` is always deployable.
- Feature branches merge via squash.
- Tags follow [Semantic Versioning](https://semver.org/): `vMAJOR.MINOR.PATCH`.
- A release is a Git tag + a GitHub Release with auto-generated notes.

### Reporting security issues

**Please do NOT file public issues for security vulnerabilities.** Email the maintainer (see `CODEOWNERS`) with a description and a reproducer. We aim to acknowledge within 48 hours.

### License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).

---

## 中文

感谢你对 **EquilibriaMarket** 的关注！无论是修正一个 typo、修复一个 Bug，还是新增一个治理插件，都是在帮助构建一个更健康的多智能体市场经济。

### 行为准则

本项目（精神上）遵循 [Contributor Covenant](https://www.contributor-covenant.org/)：友善、善意优先、项目高于自我。

### 如何贡献

1. **先搜索现有 Issue**，避免重复。
2. **非平凡改动请先开 Issue** 讨论设计。
3. **Fork 仓库**并创建特性分支：
   ```bash
   git checkout -b feat/your-feature
   ```
4. **编写代码 + 测试**（见下方"开发流程"）。
5. **推送前跑完所有检查**：
   ```bash
   go test ./...
   go test -race ./...
   go vet ./...
   gofmt -l .                          # 应无输出
   ```
6. **使用 Conventional Commits 规范**提交（见下方）。
7. **创建 PR 到 `main` 分支**，填写 PR 模板。

### Commit 消息格式（Conventional Commits）

```
<type>(<scope>): <subject>

<body>

<footer>
```

**type**：`feat`、`fix`、`perf`、`refactor`、`docs`、`test`、`chore`、`build`、`ci`
**scope**（可选）：`engine`、`governance`、`metrics`、`openrtb`、`prebid`、`admin`、`marketd`、`docs`

**示例**：

```
feat(governance): 增加 ZK-rollup 插件骨架
fix(auction): 修复 SecondPriceAuction 浮点比较溢出
docs(readme): 翻译"快速开始"为英文
perf(engine): 优化 market snapshot 临界区
```

### 开发流程

#### 项目结构

```
cmd/         # 二进制入口（marketd、prebid-mock、admin-console）
pkg/         # 库代码（外部 Go 程序可 import）
adapters/    # 外部集成（Prebid；将来：TikTok、Meta 等）
```

新增包时请遵循：

- **单一职责**：`pkg/governance` 是接口，`pkg/governance/<impl>` 是具体实现。
- **公开 API 必须有 godoc**：每个导出符号需有以符号名开头的注释。
- **错误是值**：用 `errors.New` 或 `fmt.Errorf` + `%w` 包装；sentinel error 放在文件顶部的 `var (...)` 块。
- **并发安全**：共享可变状态必须用 mutex 保护（读多写少用 `sync.RWMutex`）。测试用 `-race` 跑一遍。

#### 测试规范

- **表驱动测试**：输入空间有限时可枚举时优先使用。
- **子测试**：`t.Run(name, func(t *testing.T) {...})` 分组。
- **错误断言**：用 `errors.Is`，禁止字符串比较。
- **覆盖率目标**：`pkg/**` > 80%，`cmd/**` > 60%。

#### 新增治理插件

1. 创建 `pkg/governance/<your-plugin>/`。
2. 实现 `governance.Plugin` 接口（6 个方法）。
3. 在 `init()` 中注册：
   ```go
   func init() {
       governance.MustRegister("your-plugin", NewFactory)
   }
   ```
4. 写测试覆盖正常路径、边界条件、并发访问。
5. 更新 `README.md` 配置表；如有需要，添加 `cmd/<demo>/main.go`。

#### 新增 HTTP 端点

- 遵循 RESTful 规范（`/v1/resource`、`/v1/resource/{id}`）。
- 返回 `application/json; charset=utf-8`，字段用 snake_case。
- 用 `log/slog` 结构化日志。
- 更新 `README.md` API 表。

#### 分支与发布

- `main` 永远可部署。
- 特性分支用 squash merge。
- Tag 遵循 [Semantic Versioning](https://semver.org/)：`vMAJOR.MINOR.PATCH`。
- 一次发布 = 一个 Git tag + 一个 GitHub Release（自动生成 notes）。

### 报告安全问题

**请勿**在公开 Issue 报告安全漏洞。请通过邮件联系维护者（见 `CODEOWNERS`），附上描述与复现步骤。我们承诺 48 小时内回复。

### 许可证

提交即代表你同意你的贡献以 [MIT License](LICENSE) 授权。

---

<p align="center">
  <sub>感谢你的贡献 🙏</sub>
</p>
