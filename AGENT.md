HeroBox 是一款面向 mosdns 与 proxy 场景的可视化管控工具，提供统一的图形化面板集中配置、监控与调试节点状态；通过汇聚分散的 mosdns 规则、代理策略与运行日志，帮助运维快速排查链路问题、动态切换策略，并以最小学习成本完成日常维护。

项目后端使用 Go 编写，项目前端使用 Vue 实现，以兼顾轻量服务性能与灵活的交互体验。

程序用systemctl管理运行控制，mosdns proxy通过mybox去启动关闭重启



前端页面约定：
- 左侧保持固定侧边栏，仅含“仪表盘”“Mosdns”两项菜单，点击后在右侧主视图切换内容；顶部预留面包屑与全局操作位。
- 仪表盘：当前仅渲染基础占位（空状态 + 待添加提示），后续按需补充各类图表或告警组件。
- Mosdns：切分为“运行状态”“mosdns 版本号”两张信息卡，运行状态卡显示启动/停止按钮与最近一次更新时间；版本卡展示当前核心版本、可用更新与一键刷新操作。



构建脚本:
- 执行 `./scripts/build.sh` 可一次性完成 Go 后端编译与 React 前端打包，并将静态资源复制到 `bin/dist` 供运行时读取。

说明:
- mosdns、sing-box、mihomo 的运行/启停均通过 mybox 统一管理；mosdns 内核卡片支持检测最新 GitHub 内核并一键更新，其余代理的控制页面将按上述路径约定扩展。

使用教程:
1. 构建资源: 执行 `./scripts/build.sh`，生成 `bin/mybox` 与同目录下的 `bin/dist` 静态页面。
2. 一键运行: 进入 `bin` 目录执行 `MYBOX_ADDR=:8080 ./mybox`（亦可在源码根目录 `go run .` 并确保存在 `dist` 目录）；浏览器访问 http://localhost:8080 即可看到仪表盘页面。


开发约定:
- 前面页面遵循极简风格，在于功能实现
- 每次修改后必须执行 `./scripts/build.sh`（或等效的 `go build` 与 `npm run build`）确保前后端均能成功编译与打包。移除相关专属平台依赖。保证各平台可以成功编译
- 功能编写及时记录相关更改，方便后续跟进



配置路径:
- herobox: `/etc/herobox`
- mosdns: `/etc/herobox/mosdns/`
- sing-box: `/etc/herobox/sing-box/`
- mihomo: `/etc/herobox/mihomo/`

核心路径:
- herobox: `/usr/local/bin/herobox`
- mosdns: `/usr/local/bin/mosdns`
- sing-box: `/usr/local/bin/sing-box`
- mihomo: `/usr/local/bin/mihomo`


近期实现（2025-11-12）：
- 后端：
  - 新增统一的服务管理器，基于 systemctl 控制 mosdns/sing-box/mihomo，支持缺失核心时返回 `missing` 状态并输出运行日志。
  - 接入 mosdns 内核下载器，默认从 `yyysuo/mosdns` Releases 检测最新版本，仅安装到 `/usr/local/bin/mosdns`，过程包含详细日志。
  - 暴露 REST API：`/api/services/*` 控制服务启停、`/api/mosdns/kernel/*` 检测/更新内核、`/api/mosdns/config` 校验配置文件、`/api/mosdns/logs` 提供 mosdns 运行日志。日志会同时写入终端与内存缓冲，供前端查看。
- 前端（Vue 单页）：
  - 仪表盘保持占位，Mosdns 页面拆分运行状态、版本管理、配置管理三卡片，所有数据实时调用上述 API。
  - 支持 mosdns 核心缺失提示、配置文件缺失提示、版本检测/一键更新、操作状态按钮禁用、更新成功弹窗。
  - 新增“查看日志”按钮，点击弹出模态框展示 mosdns 专属运行日志，可手动刷新。
- 构建脚本：`./scripts/build.sh` 会编译 Go 后端 (`bin/mybox`) 并复制前端静态资源到 `bin/dist`，作为每次修改后的必执行步骤。
