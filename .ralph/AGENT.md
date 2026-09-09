# Agent Build Instructions

## Project Setup
```bash
# Backend (Go)
cd backend
go mod download

# Frontend
cd frontend
npm install

# Desktop Shell (Rust / Tauri)
# Rust 工具链已预装（rustup + stable），首次运行生成 Cargo.lock 并验证：
cd src-tauri
cargo check

# 完整桌面构建（需先构建前端 + sidecar 二进制）
cd ..
bash scripts/build-frontend.sh
bash scripts/build-sidecar.sh
cd src-tauri
cargo tauri build
```

## Running Tests
```bash
# Go backend (default tag — memory sandbox)
cd backend
go test ./...

# Go backend (personal tag — SQLite, the shipping config)
go test -tags personal ./...

# Frontend
cd frontend
npm test

# Rust / Tauri shell
cd src-tauri
cargo test          # 运行 Rust 单元测试
cargo check         # 快速编译检查（最快，推荐每轮验证用）
cargo clippy        # 静态检查（可选，代码质量提升用）
```

## Build Commands
```bash
# Go sidecar (all 5 triples, cross-compile)
bash scripts/build-sidecar.sh

# Frontend production build + embed into Go webui
bash scripts/build-frontend.sh

# Rust shell type check (fastest, recommended for per-loop verification)
cd src-tauri
cargo check

# Rust shell release build
cd src-tauri
cargo build --release

# Full Tauri desktop bundle (all platforms via CI)
# 本机只打当前平台（无 cargo tauri 子命令时：npx --yes @tauri-apps/cli@^2 build，
# 从仓库根目录运行即可，CLI 自动发现 src-tauri/tauri.conf.json）：
cd src-tauri
cargo tauri build
# 注意：tauri.conf.json 的 beforeBuildCommand/beforeDevCommand 运行时 cwd 固定为
# frontendDist 的同级目录（<repo>/frontend），与 CLI 调用目录无关——hook 里写
# npm 命令不要带 --prefix ../frontend（实测会解析到仓库外）。裸 `npm run build` 即可。
# AppImage 打包需从 GitHub 下载 AppRun/linuxdeploy/type2-runtime，网络抖动会报
# `io: unexpected end of file` / `Failed to download runtime file`，重试即可。

# Full build pipeline (what CI runs)
bash scripts/build-frontend.sh
bash scripts/build-sidecar.sh
cd src-tauri && cargo tauri build
```

## Development Server
```bash
# Go backend (personal mode, SQLite)
cd backend
go run -tags personal ./cmd/suppliderd --data-dir ./data --addr 127.0.0.1:7612

# Frontend dev server (hot reload, proxies /api to sidecar)
cd frontend
npm run dev

# Tauri dev mode (desktop app with hot-reload frontend)
cd src-tauri
cargo tauri dev
```

## Key Learnings
- Update this section when you learn new build optimizations
- Document any gotchas or special setup requirements
- Keep track of the fastest test/build cycle

### Rust / Tauri 相关要点
- **`cargo check` 是最快的验证方式**：只做类型检查，不生成代码，比 `cargo build` 快 3-5 倍。每轮循环验证 Rust 代码改动优先用它
- **`Cargo.lock` 必须提交**：固定依赖版本，避免上游 crate yanked 或版本漂移导致 CI 构建失败。`cargo check` 或 `cargo build` 后会自动生成/更新
- **`cargo check` 同时需要前端产物和 sidecar 二进制（实测，勿再判断错）**：① `generate_context!` 编译期要嵌入 `frontend/dist`（先 `bash scripts/build-frontend.sh`）；② tauri-build 的 build.rs **无条件**按当前 target triple 拷贝 `bundle.externalBin` 指定的 sidecar，缺失即 `exit(1)("<path> does not exist")`，cargo check 同样触发。本机验证命令前必须先 `bash scripts/build-sidecar.sh`（本机 Linux check 实际只需 `suppliderd-x86_64-unknown-linux-gnu` 那一个，但全量脚本更省心）。2026-09-10 实测：移走 linux sidecar 后 cargo check 立即失败，放回即过
- **sidecar 事件 API 漂移（2026-09-10 首次本地 cargo check 抓到）**：tauri 2.11.5 的 `async_runtime::Receiver` 是 `tokio::sync::mpsc::Receiver`，`rx.blocking_recv()` 返回 `Option<CommandEvent>`（None=发送端全部关闭），旧代码按 crossbeam 时代写法 `while let Ok(Some(event)) = ...` 类型不通过。正解 `while let Some(event) = rx.blocking_recv()`。另：`blocking_recv` 不能在 async 上下文调用，当前在 std thread 里用，正确。这正是无 Cargo.lock + 无本地 Rust 验证时 CI 每次解析到新版本就可能炸的实例——lock 已提交钉住 2.11.5/2.3.6
- **系统依赖**：Linux 上 Tauri 需要 `libwebkit2gtk-4.1-dev` 等 GTK/WebKit 库。这些已在开发机上装好
- **cross-compile Rust 到 Windows/macOS**：在 Linux 上交叉编译 Rust 到 Windows/macOS 目标比较麻烦（需要 MSVC/Apple SDK），所以桌面打包主要靠 GitHub Actions 的各平台 runner 来做。本机只验证 Linux 目标的编译正确性
- **sidecar 二进制由 Go 交叉编译产出**：`scripts/build-sidecar.sh` 产出五平台的 Go 二进制，放到 `src-tauri/binaries/` 目录下，Tauri 打包时会按 `externalBin` 配置取用

### Go 相关要点
- 三档 build tag：`personal` / `small_business` / `enterprise`。业务代码必须三档都能编译通过
- `modernc.org/sqlite` 是纯 Go 实现，无 cgo，可交叉编译。不要引入任何 cgo 依赖
- 测试用 `default` tag（内存存储，快）和 `personal` tag（SQLite，真实配置）各跑一遍

### 前端相关要点
- 前端代码 Web 和 Tauri 共用一套，通过 `resolveBase()` 运行时判断连接地址
- 测试用 vitest（node 环境），只测纯函数逻辑；组件测试按需引入 jsdom

## Feature Development Quality Standards

**CRITICAL**: All new features MUST meet the following mandatory requirements before being considered complete.

### Testing Requirements

- **Minimum Coverage**: 85% code coverage ratio required for all new code
- **Test Pass Rate**: 100% - all tests must pass, no exceptions
- **Test Types Required**:
  - Unit tests for all business logic and services
  - Integration tests for API endpoints or main functionality
  - End-to-end tests for critical user workflows
- **Coverage Validation**: Run coverage reports before marking features complete:
  ```bash
  # Go
  go test -cover ./...
  
  # Frontend
  npm run test:coverage
  
  # Rust
  cargo tarpaulin --out Html   # 可选，需要安装 tarpaulin
  ```
- **Test Quality**: Tests must validate behavior, not just achieve coverage metrics
- **Test Documentation**: Complex test scenarios must include comments explaining the test strategy

### Git Workflow Requirements

Before moving to the next feature, ALL changes must be:

1. **Committed with Clear Messages**:
   ```bash
   git add .
   git commit -m "feat(module): descriptive message following conventional commits"
   ```
   - Use conventional commit format: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, etc.
   - Include scope when applicable: `feat(api):`, `fix(ui):`, `test(auth):`
   - Write descriptive messages that explain WHAT changed and WHY

2. **Pushed to Remote Repository**:
   ```bash
   git push origin <branch-name>
   ```
   - Never leave completed features uncommitted
   - Push regularly to maintain backup and enable collaboration
   - Ensure CI/CD pipelines pass before considering feature complete

3. **Branch Hygiene**:
   - Work on feature branches, never directly on `main`
   - Branch naming convention: `feature/<feature-name>`, `fix/<issue-name>`, `docs/<doc-update>`
   - Create pull requests for all significant changes

4. **Ralph Integration**:
   - Update .ralph/fix_plan.md with new tasks before starting work
   - Mark items complete in .ralph/fix_plan.md upon completion
   - Update .ralph/PROMPT.md if development patterns change
   - Test features work within Ralph's autonomous loop

### Documentation Requirements

**ALL implementation documentation MUST remain synchronized with the codebase**:

1. **Code Documentation**:
   - Language-appropriate documentation (JSDoc, docstrings, etc.)
   - Update inline comments when implementation changes
   - Remove outdated comments immediately

2. **Implementation Documentation**:
   - Update relevant sections in this AGENT.md file
   - Keep build and test commands current
   - Update configuration examples when defaults change
   - Document breaking changes prominently

3. **README Updates**:
   - Keep feature lists current
   - Update setup instructions when dependencies change
   - Maintain accurate command examples
   - Update version compatibility information

4. **AGENT.md Maintenance**:
   - Add new build patterns to relevant sections
   - Update "Key Learnings" with new insights
   - Keep command examples accurate and tested
   - Document new testing patterns or quality gates

### Feature Completion Checklist

Before marking ANY feature as complete, verify:

- [ ] All tests pass with appropriate framework command
- [ ] Code coverage meets 85% minimum threshold
- [ ] Coverage report reviewed for meaningful test quality
- [ ] Code formatted according to project standards (gofmt for Go, prettier for JS/TS, cargo fmt for Rust)
- [ ] Type checking passes (if applicable)
- [ ] **Rust 代码改动：`cargo check` 通过（修改了 src-tauri/ 或 tauri.conf.json 时）**
- [ ] All changes committed with conventional commit messages
- [ ] All commits pushed to remote repository
- [ ] .ralph/fix_plan.md task marked as complete
- [ ] Implementation documentation updated
- [ ] Inline code comments updated or added
- [ ] .ralph/AGENT.md updated (if new patterns introduced)
- [ ] Breaking changes documented
- [ ] Features tested within Ralph loop (if applicable)
- [ ] CI/CD pipeline passes

### Rationale

These standards ensure:
- **Quality**: High test coverage and pass rates prevent regressions
- **Traceability**: Git commits and .ralph/fix_plan.md provide clear history of changes
- **Maintainability**: Current documentation reduces onboarding time and prevents knowledge loss
- **Collaboration**: Pushed changes enable team visibility and code review
- **Reliability**: Consistent quality gates maintain production stability
- **Automation**: Ralph integration ensures continuous development practices

**Enforcement**: AI agents should automatically apply these standards to all feature development tasks without requiring explicit instruction for each task.
