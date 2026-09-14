# RegTool 改造路线图

> 目标：把 RegTool 改造成一个能拿去找 2026 年 full-time SDE 岗位的 Go 项目。
> 题目不变（一键切换包管理器镜像源），重点补工程质量、测试、并发、发布流程。
> 本文档是工作清单，每完成一步就勾掉对应的框。

最后更新：2026-09-14

---

## 1. 现状

RegTool（原名 RegistryHub）是一个用 bubbletea 写的终端 TUI，按地区（us / cn / eu / jp）切换包管理器的镜像源。

| 项目 | 现状 |
| --- | --- |
| 代码量 | 约 2200 行 Go |
| 测试 | 0 个 `_test.go` |
| Go 版本 | go.mod 写 1.19，CI 用 1.20，本机 1.25 |
| CI | 只 build，不 test、不 lint、不 release |
| 支持平台 | 仅 macOS / Linux |
| 已支持的工具 | npm、yarn、pip、gem、homebrew |

### 目录结构

- `cmd/`：每个 TUI 页面一个 bubbletea model，通过 `init()` 注册到全局 map，主菜单按名字切换页面。
- `source/`：核心抽象 `AppManager` 接口（取当前源、设源、是否安装）。每个包管理器一个实现，全部靠 `exec.Command` 调外部命令。
- `source/source.go`：shell out 到 `curl` 从 gitee 拉 `sources.json`，作为远端源列表。
- `source/localdata/`：把各工具当前的源写到 `~/.config/regtool/backup.json`，作为改之前的快照。
- `shell/`：bash / zsh 的 rc 文件读写 env，目前基本没被用到。
- `common/alias/`：包管理器别名映射。
- `tools/gen_initall.go`：代码生成器，扫 `source/app/*` 生成 blank import。生成文件被 gitignore，所以 clone 后必须先 `make generate` 才能 build。
- `tools/gen-proj-banner/`：React 项目，只用来生成 README 横幅，和主体无关。

### 已知问题

- [x] `pip.go` 和 `yarn.go` 的 `SetRegistry` 取的是 `regionSources["npm"]`，pip 会被设成 npm 的镜像地址。真 bug。
- [x] `env.Init()` 找不到 `.env` 直接 panic，二进制发给别人跑不起来。
- [x] 用 `curl` 而不是 `net/http`，没有 context，没有超时。
- [x] 大量错误被 `_` 吞掉，`UpdateRegistry`、`ChangeAllRegistry` 失败时静默。
- [x] `cmd/run.go` 的 `regions` 里有 "jp"，但 `structs.StringToRegion` 不认，返回空字符串。
- [ ] 全局可变状态（`SOURCES`、`registryManagers`、`commandRegistry`）加 `init()` 注册，无法单测。
- [ ] 只支持 macOS / Linux：依赖 `$SHELL`、`.bashrc`、Makefile 里 `sudo install`。
- [x] go.mod 所有依赖都标 `// indirect`，还 require 了不该出现在运行时依赖里的 `cobra-cli`。
- [x] clone 后直接 `go build` 失败，必须先跑代码生成器。
- [x] 源列表硬编码 gitee 地址，没有本地缓存和离线回退。

---

## 2. 改造原则

- 题目保持不变，不硬套微服务、k8s、消息队列。
- 面试官看 Go 项目在意的是：地道的 Go、测试、并发、接口设计、CLI 体验、发布流程。每一步都对准其中至少一条。
- 每一步做完都要有可验证的产出（能跑的命令、绿的 CI、能装的二进制）。

---

## 3. 分步计划

### Step 1：打地基

目标：clone 下来就能 build，能测，能 lint，bug 修掉。

- [x] go.mod 升到 go 1.25，重新整理直接依赖和间接依赖，去掉 `cobra-cli`。
- [x] 删掉 `tools/gen_initall.go` 和 `source/initall`，改成显式注册（一个 `registry.go` 列出所有后端）。
- [x] 删掉 `env/` 和 `.env` 依赖，调试开关改用 flag 或环境变量，缺失时不 panic。
- [x] `curl` 改成 `net/http`，带 `context.Context` 和超时。
- [x] 错误全部用 `fmt.Errorf("...: %w", err)` 包装并向上返回，不再 `_` 吞掉。
- [x] 日志改用 `log/slog`。
- [x] 修 pip / yarn 取错 key 的 bug。
- [x] 修 "jp" 地区不被识别的问题（要么加进 `Region`，要么从菜单里删掉）。
- [x] 加 `golangci-lint` 配置。
- [x] CI：`go vet`、`golangci-lint`、`go test -race -cover`，linux / macos / windows 三平台矩阵。
- [x] 补第一批单元测试：alias、localdata、sources 解析、`StringToRegion`。

验收：`git clone` 后 `go build ./...` 和 `go test ./...` 直接通过，CI 全绿。

### Step 2：CLI + TUI 双模式

目标：脚本可用，能写端到端测试。

- [ ] 引入 cobra 子命令：
  - `regtool use <region> [app...]`
  - `regtool status`
  - `regtool list [app]`
  - `regtool undo`
  - 无参数进 TUI。
- [ ] TUI 的 model 不再直接调 `source` 包，而是调同一套 service 层，CLI 和 TUI 共用。
- [ ] 用 `testscript` 写 CLI 端到端测试。
- [ ] `--json` 输出，方便脚本消费。

验收：`regtool use cn npm --dry-run` 能打印将要做的改动；testscript 覆盖主要子命令。

### Step 3：核心设计升级（最值得讲的部分）

目标：不再 shell out，直接读写配置文件，做成事务式变更。

- [ ] 定义 `Backend` 接口：`Name()`、`Detect()`、`Current()`、`Plan(target)`、`Apply(plan)`。
- [ ] 每个后端直接读写自己的配置文件，不调外部命令：
  - npm：`~/.npmrc`
  - yarn：`~/.yarnrc.yml`（yarn berry）和 `~/.yarnrc`（yarn 1）
  - pip：`pip.conf` / `pip.ini`
  - gem：`~/.gemrc`
  - go：`GOPROXY`（`go env -w` 或 `~/.config/go/env`）
  - cargo：`~/.cargo/config.toml`
  - homebrew：保留调命令，因为它本身就是 git remote 操作
- [ ] 变更前快照到 `~/.config/regtool/history/<timestamp>/`，可用 `regtool undo` 回滚到任意版本。
- [ ] 写文件走临时文件 + rename 的原子写。
- [ ] `--dry-run` 输出 unified diff。
- [ ] 所有路径用 `os.UserConfigDir` 和 `filepath`，Windows 可用。
- [ ] 每个后端用 fake 文件系统做表驱动测试。

验收：在一台没有 npm 的机器上也能对 `.npmrc` 做 plan / apply / undo，并且测试不依赖任何外部命令。

### Step 4：并发亮点

目标：一个能在面试里讲清楚的 Go 并发案例。

- [ ] `regtool doctor`：并发探测所有镜像的延迟和可达性。
  - `errgroup` 限制并发数。
  - `context` 控制整体超时和单次超时。
  - 结果按后端分组，表格输出。
- [ ] `regtool use --fastest`：对每个后端探测所有地区的镜像，自动选最快的。
- [ ] 探测逻辑注入 `http.Client`，测试用 `httptest.Server` 模拟慢源和挂掉的源。
- [ ] `-race` 下测试通过。

验收：`regtool doctor` 在 3 秒内返回十几个镜像的结果；测试能覆盖超时、部分失败、全部失败。

### Step 5：发布

目标：别人能装能用。

- [ ] goreleaser：linux / macOS / windows，amd64 + arm64。
- [ ] Homebrew tap 和 Scoop bucket。
- [ ] 生成 SBOM，二进制用 `-ldflags` 注入版本号，`regtool version` 可查。
- [ ] README 重写：一句话介绍、安装方式、asciinema 演示、支持矩阵。
- [ ] 删掉 `tools/gen-proj-banner`，横幅图直接放 `assets/`。
- [ ] 发 v1.0.0。

验收：`brew install <tap>/regtool` 或 `scoop install regtool` 能装，`regtool version` 正确。

### Step 6（可选）：后端服务

目标：补一段服务端经验，但控制规模。

- [ ] 一个小 Go HTTP 服务托管 `sources.json`，带 ETag 和缓存头。
- [ ] 定时对所有镜像做健康检查，结果落 SQLite。
- [ ] `/v1/sources`、`/v1/health`、`/metrics`（Prometheus）。
- [ ] Docker 镜像和 docker-compose。
- [ ] CLI 支持配置源服务地址，并在拉不到时回退到本地缓存。

验收：`docker compose up` 后 CLI 能从本地服务拉源列表；Grafana 能看到健康检查指标。

---

## 4. 简历产出

做完 Step 1 到 4 后，可以写出这几条：

- 设计接口驱动的多后端配置管理，支持 npm / yarn / pip / gem / go / cargo 等包管理器，跨 Linux / macOS / Windows。
- 实现事务式配置变更：变更前快照、原子写入、dry-run diff、任意版本回滚。
- 用 errgroup + context 实现并发镜像探测与自动选源，单次探测十余个镜像在秒级完成，测试覆盖超时和部分失败场景。

Step 5 保证项目能被安装和使用，star 和 issue 才会来。Step 6 是加分项，有余力再做。

---

## 5. 进度记录

| 日期 | 完成内容 |
| --- | --- |
| 2026-09-13 | 完成现状分析，写下本路线图 |
| 2026-09-14 | Step 1 完成（PR #2 #3 #4 #5）：可直接 build，net/http + 内嵌默认源，错误上抛，lint + 三平台 CI，模块路径改为 github.com/ZHallen122/RegTool。全局可变状态和 Windows 支持留到 Step 2 / 3 处理 |
