# HeroBox

HeroBox 是一款面向 mosdns 与多代理(sing-box、mihomo) 场景的可视化管控工具，集中展示节点运行状态、版本信息与日志，帮助运维快速排查故障、切换策略并降低维护成本。项目采用 **Go + Vue** 架构：后端负责 systemctl 服务管理、mosdns 内核更新与日志 API；前端通过图形化界面实时调用这些能力。

## 功能概览

- **统一服务控制**：后端通过 systemctl 控制 mosdns / sing-box / mihomo，并在未检测到核心二进制时返回 `missing` 状态。
- **mosdns 内核管理**：自动从 `yyysuo/mosdns` Releases 检查最新版，根据当前平台下载解压到 `/usr/local/bin/mosdns`，并输出详细日志。
- **配置校验**：`/api/mosdns/config` 检查 `/etc/herobox/mosdns/config.yaml` 是否存在，前端会在缺失时给出提示并禁用启动按钮。
- **运行日志**：所有 mosdns 相关操作写入内存缓冲与终端，可在前端“查看日志”弹窗中滚动查看，支持手动刷新。
- **前端交互**：Mosdns 页面分为运行状态、版本管理、配置管理三张卡片，支持启动/停止、一键更新、缺失提醒与日志弹窗。

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

- `HEROBOX_CONFIG_FILE`（默认 `/etc/herobox/herobox.json`）用于保存前端可调整的设置，目前包含 mosdns 配置路径。
- 当在前端“配置管理”中修改路径时，后端会同步写入该文件；重启程序后依然使用最新路径，同时 mosdns 的启动命令会根据新路径自动调整 `-c` 与 `-d` 参数。

## API 摘要

- `GET /api/services`、`GET|POST /api/services/{name}`：查询/控制服务状态（`start|stop|restart`）。
- `GET /api/mosdns/kernel/latest`、`POST /api/mosdns/kernel/update`：检测与更新 mosdns 内核。
- `GET /api/mosdns/config`：配置存在性、修改时间。
- `GET /api/mosdns/logs`：mosdns 运行日志（仅含 `[mosdns]` 条目）。

欢迎根据实际需求扩展更多代理或自定义面板，只需沿用上述 API 设计和构建流程即可。
