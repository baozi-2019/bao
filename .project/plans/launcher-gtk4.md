# bao 启动器实施计划（GTK4 文件/软件搜索 + 计算器）

Albert 风格的桌面启动器：搜索框输入关键字匹配已安装软件与文件，输入数学表达式直接给出计算结果，支持配置搜索排除目录。

## 1. 范围

- 软件搜索：扫描 XDG 应用目录（`.desktop` 文件），按名称模糊匹配，回车启动。
- 文件搜索：从 `$HOME` 遍历文件/目录，模糊匹配，可配置排除目录。
- 计算器：整串输入为合法表达式时，结果置顶，回车复制。
- 排除目录配置：设置对话框增删，持久化到 JSON 配置。
- 暂不实现：全局热键唤起、文件内容持久索引、拼音匹配（代码结构预留扩展点）。

## 2. 关键决策

| 决策点 | 结论 | 理由 |
| --- | --- | --- |
| 语言/绑定 | Go + `github.com/diamondburned/gotk4/pkg`（GTK4，cgo） | Go 标准库足以覆盖其余需求；gotk4 是 GTK4 最成熟的 Go 绑定 |
| 计算器 | 手写递归下降解析器 | 不用 `eval`、不引第三方表达式库，严格模式解析失败即静默 |
| 文件搜索 | `filepath.WalkDir` + goroutine + context 取消 | 简单可靠；不设持久索引，靠防抖、取消、命中上限控制延迟 |
| 匹配排序 | 自实现子序列模糊打分（连续/词首命中加权） | 应用与文件共用一套打分，零额外依赖 |
| 配置 | `~/.config/bao/config.json`（`encoding/json`） | 零额外依赖；内置默认排除与用户配置合并 |

## 3. 环境前置

当前系统（Ubuntu 26.04）缺少以下包，实施前需安装：

```bash
sudo apt install golang-go libgtk-4-dev pkg-config build-essential
```

## 4. 项目结构

```text
bao/
├── go.mod / go.sum
├── main.go                    # gtk Application 入口、组装各模块
├── internal/
│   ├── calc/
│   │   ├── calc.go            # 递归下降表达式解析/求值（严格模式）
│   │   └── calc_test.go       # 单元测试
│   ├── config/
│   │   └── config.go          # 排除目录等配置加载/保存：~/.config/bao/config.json
│   ├── apps/
│   │   └── desktop.go         # 扫描 XDG 应用目录解析 .desktop
│   ├── search/
│   │   └── files.go           # 异步文件遍历：防抖、context 取消、排除目录、结果上限
│   └── ui/
│       ├── window.go          # ApplicationWindow：搜索 Entry + 结果 ListBox + 键盘导航
│       └── settings.go        # 排除目录设置对话框
└── .project/plans/            # 本计划
```

## 5. 核心设计

1. **查询路由**：输入变化（防抖 ~150ms）后，若整串是合法数学表达式（以数字/`(`/`-`/`+` 开头且严格解析成功），结果列表首条显示计算结果（回车复制）；同时并行做应用 + 文件搜索，应用排前、文件排后。
2. **计算器**：递归下降解析，支持 `+ - * / % ^`、括号、一元负号、小数及 `sqrt/abs/sin/cos/tan/ln/log`；解析失败静默，不弹错。
3. **应用搜索**：扫描 `$XDG_DATA_HOME/applications`（默认 `~/.local/share/applications`）与 `$XDG_DATA_DIRS/applications`（默认 `/usr/local/share/applications:/usr/share/applications`），启动时载入内存；忽略 `Hidden=true`，`NoDisplay=true` 仅作兜底；按 `Exec` 启动。
4. **文件搜索**：从 `$HOME` 遍历，跳过隐藏文件/目录与排除目录（前缀匹配），命中上限 100 条提前结束；上一次遍历在新输入到达时通过 context 取消。
5. **配置**：默认排除 `node_modules`、`.git`、`.cache` 等，与用户配置合并展示；设置对话框可增删，改动即写回配置文件。
6. **UI**：GTK4 `ApplicationWindow`（不可缩放、顶部居中），垂直 Box：顶部搜索 `Entry`，下方 `ListBox` 结果行（图标 + 标题 + 副标题）；↑↓ 移动选中、Enter 执行/复制、Esc 清空或退出；搜索结果经 GTK 主循环回灌 UI。

## 6. 实施步骤

1. 安装系统依赖（见第 3 节）。
2. `go mod init bao`；`go get github.com/diamondburned/gotk4/pkg@latest`。
3. 实现 `internal/calc`（解析器 + 测试）→ `go test ./internal/calc`。
4. 实现 `internal/config`（默认值、加载、保存，含目录创建）。
5. 实现 `internal/apps`（.desktop 解析 + 内存列表 + 打分匹配）。
6. 实现 `internal/search`（遍历 + 取消 + 排除过滤 + 打分匹配）。
7. 实现 `internal/ui` 与 `main.go` 组装。
8. 验证（见第 7 节）。

## 7. 验证方式

- `go build ./...`、`go vet ./...`、`go test ./...` 全部通过。
- 若环境可装 `xvfb`，用 `xvfb-run` 做启动冒烟（窗口创建不 panic）；否则在交付说明中写明未做 GUI 冒烟。

## 8. 待确认项与风险

- gotk4 依赖 cgo 与 GTK4 头文件，第 3 节装包是硬依赖。
- 大 home 目录首次遍历有 IO 压力，靠取消 + 上限 + 跳过隐藏目录控制延迟。
- 全局热键唤起需要额外系统级依赖，本计划不包含。
