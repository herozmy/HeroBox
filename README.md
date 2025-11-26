# HeroBox

HeroBox 是一款面向 mosdns 与多代理(sing-box、mihomo) 场景的可视化管控工具，集中展示节点运行状态、版本信息与日志，帮助运维快速排查故障、切换策略并降低维护成本。项目采用 **Go + Vue** 架构：后端负责 systemctl 服务管理、mosdns 内核更新与日志 API；前端通过图形化界面实时调用这些能力。

## 功能概览

- **统一服务控制**：后端通过 systemctl 控制 mosdns / sing-box / mihomo，并在未检测到核心二进制时返回 `missing` 状态。
- **mosdns 内核管理**：自动从 `yyysuo/mosdns` Releases 检查最新版，根据当前平台下载解压到 `/usr/local/bin/mosdns`，并输出详细日志。
- **配置校验**：`/api/mosdns/config` 检查 `/etc/herobox/mosdns/config.yaml` 是否存在，前端会在缺失时给出提示并禁用启动按钮。
- **运行日志**：所有 mosdns 相关操作写入内存缓冲与终端，可在前端“查看日志”弹窗中滚动查看，支持手动刷新。
- **前端交互**：Mosdns 页面分为运行状态、版本管理、配置管理三张卡片，支持启动/停止（一旦运行即自动切换为“重启”按钮）、一键更新、缺失提醒与日志弹窗；配置管理内新增目录树式“预览”弹窗，可递归浏览 mosdns 配置目录，在线查看/编辑 `.yaml/.yml/.txt` 文件并保存，同时在左侧显示文件夹层级，体验类似文件管理器。
- **配置偏好与下载向导**：自定义 FakeIP / 国内 DNS / Socks5 地址 / Proxy 入站地址后，下载配置会自动替换模板中的占位值，并提供进度提示与向导日志；可在线编辑配置文件且保存过程带有进度反馈。

## 快速开始

```bash
# 1. 构建（每次修改后务必执行）
./scripts/build.sh

# 2. 运行
cd bin
MYBOX_ADDR=:8080 ./mybox
# 或
# HEROBOX_ADDR=:8080 go run ./cmd/herobox
```

打开浏览器访问 `http://localhost:8080`，左侧菜单可切换到 “Mosdns” 页面查看实时状态。

## 前端开发

前端目录已经迁移到 **Vite + Vue 3**，首次开发需要安装依赖：

```bash
cd frontend
npm install
npm run dev   # 启动本地调试，默认监听 5173 端口
```

所有页面与组件都位于 `frontend/src` 下，其中：

| 目录                      | 说明 |
|---------------------------|------|
| `src/views/Dashboard.vue` | 总览页面占位，展示系统状态。 |
| `src/views/Mosdns`        | Mosdns 相关的 Overview、ListManagement 等模块。 |
| `src/components`          | 公共组件（弹窗、进度条、提示条等）。 |
| `src/api.js`              | 与后端交互的 API 封装，列表接口返回纯文本也会被自动解析。 |

前端改动完成后，仍需回到仓库根目录执行 `./scripts/build.sh`，以便生产构建结果复制到 `bin/dist/`，确保与后端一同发布。

## 关键路径

| 类型    | 位置                                  |
|---------|---------------------------------------|
| herobox | `/usr/local/bin/herobox`              |
| mosdns  | `/usr/local/bin/mosdns`               |
| sing-box| `/usr/local/bin/sing-box`             |
| mihomo  | `/usr/local/bin/mihomo`               |
| 配置根  | `/etc/herobox/`                       |
| mosdns 配置 | `/etc/herobox/mosdns/config.yaml` |

## 开发约定

1. **构建约束**：任何代码/前端改动后，必须运行 `./scripts/build.sh`，以保证 `bin/mybox` 与 `bin/dist` 与最新源码一致。
2. **极简界面**：前端保持极简风格，优先确保功能可用性与状态透明。
3. **日志透明**：所有服务控制、版本检测、内核下载过程必须写日志，便于前端与后端排障。

## 配置持久化

- `HEROBOX_CONFIG_FILE`（默认生成在程序运行目录的 `herobox.yaml`）保存所有可变设置，格式采用 YAML，包含：
  - `heroboxPort`: 程序当前监听端口（示例 `":8080"`）。
  - `mosdns.configPath`: 前端配置的 mosdns 配置文件路径。
  - `mosdns.status`: 最近一次查询到的 mosdns 状态（`running`/`stopped`/`missing`）。
  - `uiSettings`: 前端的个性化设置（如 `autoRefreshLogs`）。
- 每次在前端修改 mosdns 配置路径、切换自动刷新日志等设置后，后端都会立即写入该 YAML 文件；重启程序会自动加载这些默认值，并根据最新路径调整 mosdns 启动命令的 `-c/-d` 参数。配置目录预览会自动过滤 dump 缓存文件，仅展示真实配置内容。

## API 摘要

- `GET /api/services`、`GET|POST /api/services/{name}`：查询/控制服务状态（`start|stop|restart`）。
- `GET /api/mosdns/kernel/latest`、`POST /api/mosdns/kernel/update`：检测与更新 mosdns 内核。
- `GET /api/mosdns/config`：配置存在性、修改时间。
- `GET /api/mosdns/logs`：mosdns 运行日志（仅含 `[mosdns]` 条目）。

欢迎根据实际需求扩展更多代理或自定义面板，只需沿用上述 API 设计和构建流程即可。
