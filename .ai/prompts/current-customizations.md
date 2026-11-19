# 当前节点定制保护 prompt

用于日常开发、修复和上游移植。把 `<任务>` 换为当前需求；本文不授权提交、推送、部署、运行安装脚本或操作共享服务。

## 开始前

1. 读[当前定制台账](../sync-diffs/v1.2.0-local-2026-11-20.md)、[完整差异索引](../sync-diffs/v1.2.0-local-2026-11-20.files.md)与[Panel ↔ node 契约笔记](../panel-sync-notes.md)。历史同步见 [v1.1.5 → v1.2.0](../sync-diffs/v1.1.5-to-v1.2.0.md)。
2. 在 node-backend 仓库执行只读 `git status --short --untracked-files=all`、`git log -5 --oneline`，核对实际 HEAD/工作树；工作区根目录不是这个 Git 仓库。固定同步落点是 `fb3f1d6`，本次台账固定 HEAD 为 `55e8776`；不是保证以后仍然不变。
3. 官方 v1.2.0 为 `8bd89c3`。原官方 v1.1.5 保存在 `refs/original/refs/tags/v1.1.5`，为 `6c3ec13`；本地同名 tag 是另一历史的 `43e2250`。二者 tree 相同但 ancestry 不同，不能用错误 tag range 统计官方提交或直接合并不相连的历史。
4. 涉及 panel contract 时读[backend 当前约束](../../../backend/.ai/prompts/current-customizations.md)，再核对实际 handler、DTO、model 与 serializer；frontend 有表单不等于 node 有对应 inbound。
5. 实现 `<任务>` 前确定影响范围：C01/C02（同步后改动）、N01–N04（当前面板契约），或历史同步 S01–S06；选择最小验证，不按旧 pending 说明重新实现已有功能。

## 多协议实例与数据格式

- 面板 `MarshalProtocols` 是实例 ID 的权威；相同 type 可以有多个字符串 ID，保留 `id/name` 和请求 `protocol_id`。controller tag、流量目标、用户请求和 online/status 路径不能退回仅按 type 标识。
- 当前 node config Protobuf 不携带实例 `id/name`。配置协商遇到非空协议数组后清 ETag 并改取 JSON、记住该选择；用户和报告仍能用 Protobuf。只有 panel schema、serializer、node decoder 和回归测试一起提供实例 ID，才能改变 fallback。
- 304/未变化结果与显式空用户数组不同：前者保留用户，后者撤销全部用户。Mieru 重试准备过程保留必要的 `ResetUserETag`。
- `IsSupportedProtocol` 决定可运行的协议；unsupported 配置被跳过，不能为支持新协议只增加字符串。SS `none/plain` 已被固定 Xray 拒绝；SSR/socks/http/naive 不会因 panel 接受而自动成为 node 能力。
- Shadowsocks legacy `obfs/obfs_host/obfs_path` 和 Mieru `transport/multiplex` 仍是活动字段；新增字段时检查 panel 的 `SanitizeProtocolsForNodeDistribution` 是否会清掉它。

## 数据面、生命周期与上报

- Xray controller 与 Mieru controller 是两条数据面。Mieru 保留 UUID 作为用户名/密码、空 UUID 过滤、TCP/UDP、multiplex、会话级计数、空用户恢复和撤销关闭会话。
- **Mieru 在线上报不等于全局设备准入。** 它不经过 Xray dispatcher/限速/设备 limiter；不得把 Xray 的租约、退避与配额保证写成对所有协议成立。
- C01 在线心跳间隔为 1–60 秒；按订阅 ID/IP 去重、IPv4-mapped 地址归一化、每批不超过 10000。失败保留 pending，ACK 只清除同序列记录；上报期间新会话不可被旧 ACK 删除。已登记持续会话可无流量保活，撤销和关闭后不得无限续租。
- 设备接口仅在 alive 请求明确 HTTP 404/405 时于准备阶段选择本地回退。其他失败不放行；不要把启动探测描述成运行中自动发现 panel 升级。需要重新准备 controller 才重新选择能力。
- 设备租约以订阅 ID + 来源 IP 计数，不是物理设备数；NAT 共享 IP 的语义不变。保留申请去重、并发/等待上限、取消与用户代际 fence、Retry-After 和有界退避。
- 新配置先 Prepare，再停止/切换监听；启动失败恢复缓存旧配置与用户，不依赖面板可用性。不要承诺已有连接无中断。
- 流量队列以目标 panel、鉴权、server/type/instance 分开；原子取数后未 ACK 批次保留，跨重载/移除实例继承并在退出前尝试 flush。它只在内存中，且 node 没有稳定的上报幂等 ID；响应丢失仍可能重复计费。backend 的内部 replay marker 不等于 node HTTP 报告 exactly-once。

## TLS、NW3 与内核

- 保留证书路径约定：有实例 ID 时用 `<id><serverId>`，否则用 `<type><serverId>`，不要无迁移方案改名。按当前 helper 核对路径，不能把它写成新的 `type:id` 目录结构。
- self/ACME 使用 ECDSA；file 读取已有证书；TLS 1.3、续期后的 reload、私钥写入和权限检查不倒退。self leaf 指纹上报与订阅客户端 pin 验证是两件事。
- Nowhere 只实现 NW3：`nw3` ALPN、TLS certificate、`mix|tcp|udp` 和 `morph|none`；共享密钥为 panel 转换后的 32 位小写十六进制文本。不要把旧 UUID/now/1 默默当新协议。
- 保留 [Nowhere 独立模块](../../pkg/nowhere/)与 Xray 适配器边界、许可证和来源；不要写全局 module cache。根模块 `go test .\...` 不覆盖独立 module 测试。
- Xray replacement 固定 `wyx2685/xray-core` 的 `e087e74a1bf2`，Mieru v3.39.0；同步依赖时核对 TLS/REALITY/SS2022/TUIC/UDP 行为，不只解决编译错误。默认 MPTCP 禁用不是无法被部署配置覆盖的保证。
- TUIC 本地传输修复、REALITY `minClientVer=0.0.0`、Freedom 的目的地址限制、Nowhere 的 UDP 数据面边界见历史台账及模块说明；不能把内核不支持的出站写成可用中继。

## 文档和最小验证

- README 只保留项目入口。版本化变更、状态和验证放 `.ai/sync-diffs`；可复用规则放 prompts；契约细节保留原笔记入口。只保存 Markdown，不生成独立 JSON/CSV 清单。
- 列清提交、官方/本地比较边界、全部路径、工作树与采样时间。官方导入、长期 fork 差异、同步后增量不能混算；旧测试结果标注来源和日期，不称作本轮执行。
- panel API 优先 `go test .\api\panel -count=1`；Mieru 优先 `go test .\node -run 'Mieru' -count=1`；设备准入使用现有 limiter/node 测试；NW3 变更再跑对应 core 和 `go -C .\pkg\nowhere test .\...`。完整同步才需要主/嵌套模块全量及构建。
- 直接保留 Go 退出码；如 PowerShell 使用输出过滤，应在 native 命令后检查 `$LASTEXITCODE`，不要把管道结束当作测试成功。
- 文档-only 检查链接、Git 统计与 `git diff --check`，不重跑业务构建。Linux race、Unix 权限、静态产物、Rust 客户端和真实 panel/Redis 联调分别记录，缺环境为未验证，不等于不可执行或已通过。
- 不读取真实配置/凭据、不启动共享环境服务、不下载或部署新的客户端来为文档制造验收结果；确需外部联调时先明确隔离环境。

交付说明具体文档或行为变化、保留项、实际验证、未实现/未验证边界；不替用户提交，也不覆盖并行工作。
