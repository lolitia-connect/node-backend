# Node-backend 开发 agent prompts

本目录给 node-backend 的日常开发、故障修复和明确的官方同步提供当前上下文。文件只提供工程约束，不授权提交、推送、部署、处理真实凭据或运行生产迁移。

## 使用顺序

1. [current-customizations.md](./current-customizations.md)：先读当前架构、panel 契约、协议实例和验证边界。
2. [当前定制台账](../sync-diffs/v1.2.0-local-2026-11-20.md)及[逐文件索引](../sync-diffs/v1.2.0-local-2026-11-20.files.md)：核对固定 HEAD、同步后本地增量、backend 对账和实际验证。
3. [Panel ↔ node 契约笔记](../panel-sync-notes.md)：涉及协议字段、Nowhere、设备准入、证书指纹或 panel 投影时必读。
4. [sync-upstream.md](./sync-upstream.md)：只有用户明确要求同步官方版本时使用；必须替换明确的目标 tag/SHA，不从浮动 remote 或最高本地 tag 推断。

## 已确认快照

- 官方 node 目标：v1.2.0，`8bd89c328519eacada5c4bfae60725b671a5df36`。
- 本地同步落点：`fb3f1d671dcd77aa08ed6e161bfacae55586fa28`。
- 当前产品 HEAD：`55e8776e4d0c49c2829c5a321144bc933648f1d4`。
- 同步后本地增量：`cd8e1d9` 的 Mieru 在线上报与 `55e8776` 的 README 归档。
- 原官方 v1.1.5：`6c3ec13ec285910db7cd367ed8104ab4157cca9e`；本地同名 tag `43e2250` 属另一历史，详见[同步索引](../sync-diffs/v1.1.5-to-v1.2.0.files.md)。真实官方增量为 15 个提交/157 路径，本地同步落点为 160 路径；当前 fork 存量差异 34 路径，不能混算。
- 当前 backend 源码对账目标：HEAD `57973bf2`，源自 v1.21.7；backend 并行工作树和 node `.ai` 文档工作树不等于生产发布。
- 源码已有设备 API、NW3 用户转换与 self pin 接收；配置 Protobuf 仍缺实例 ID，保留 JSON fallback。Xray 的全局准入不覆盖 Mieru，pin 接收按 type 非逐实例；本地测试与源码对账不等于真实 backend/Redis/客户端联调。

## 文件归属

- 项目 README 只保留介绍、安装和构建入口；不要把任务进度、临时测试输出、同步台账或 agent 记忆写回 README。
- 版本化同步、行为变化、验证证据和未完成项放在 `.ai/sync-diffs`。
- 可复用的架构和安全边界放在 `.ai/prompts`；协议字段级 panel 契约放在 `panel-sync-notes.md`。
- 修改 `.ai` 文档时保留旧历史，不用当前源码结果覆盖过去的验证结论。
