# 节点上游同步 prompt

把 `<TAG>` 换为用户明确指定的官方版本。此模板用于将 `official-upstream` 的节点增量移植到本 fork，**不是本轮文档整理要执行的命令**，也不授权发布、强制重置、改写历史或操作生产面板。

## 先读与固定基线

- [当前开发约束](./current-customizations.md)、[当前台账](../sync-diffs/v1.2.0-local-2026-11-20.md)、[逐文件索引](../sync-diffs/v1.2.0-local-2026-11-20.files.md)、[Panel ↔ node 笔记](../panel-sync-notes.md)。
- 上次确认官方 v1.2.0：`8bd89c328519eacada5c4bfae60725b671a5df36`。
- 本地同步落点：`fb3f1d671dcd77aa08ed6e161bfacae55586fa28`；台账 HEAD：`55e8776e4d0c49c2829c5a321144bc933648f1d4`，接手时重新检查。
- 此仓库存在原始/改写后的两套历史。原 v1.1.5 是 `6c3ec13`，本地同名 tag `43e2250` 仅 tree 相同，不是 v1.2.0 的祖先。历史官方增量是 `6c3ec13..8bd89c3` 的 **15 个非 merge 提交**；不能重用错误的 50 个提交统计。
- remote 拼写是 **`official-upstream`**，不同于兄弟 frontend/backend 的 `offical-upstream`。台账观察到的 remote/master 较旧，不代表远端当前最新版本。

## 1. 检查工作树与目标

在 node-backend 仓库内执行；根工作区不是仓库。先检查 tracked/untracked，不清理、stash、reset 或覆盖他人工作。

```powershell
git --no-pager status --short --untracked-files=all
git --no-pager log -5 --oneline
$LastOfficial = '8bd89c328519eacada5c4bfae60725b671a5df36'
$TargetTag = '<TAG>'
$TargetRef = "refs/upstream-tags/official-upstream/$TargetTag"
# 仅在确已获准同步/获取该官方版本时执行。
git fetch official-upstream "refs/tags/${TargetTag}:${TargetRef}"
if ($LASTEXITCODE -ne 0) { throw 'fetch failed' }
$TargetCommit = git rev-parse "$TargetRef^{commit}"
if ($LASTEXITCODE -ne 0) { throw 'target ref is not a commit' }
git merge-base --is-ancestor $LastOfficial $TargetCommit
if ($LASTEXITCODE -ne 0) { throw 'Inspect upstream ancestry before choosing a range' }
git --no-pager log --reverse --format=fuller "$LastOfficial..$TargetCommit"
git --no-pager diff --name-status $LastOfficial $TargetCommit
```

记录解析出的 SHA，不覆盖本地 `tia-*`/旧 tag，不用最高 tag 自动选版本。官方增量、落点后本地增量、官方目标与 fork 的存量差异分别记录，不能把移植落点称作已 merge 官方历史。

## 2. 建立移植决策表

每项写明「移植 / 适配 / 保留 fork / 不适用 / 待实现」，绑定真实源码和测试；不因提交名相似直接视为已完成。

| 范围 | 必须核对 |
| --- | --- |
| Panel API | v2 配置 JSON fallback、Protobuf 字段号/serializer/decoder、ETag/304 与空用户、`protocol_id`、body code 和 HTTP 状态 |
| 多实例 | controller tag、证书旧路径、流量目标、online/status query，不按 type 去重 |
| Mieru | 独立 controller、UUID/TCP/UDP/multiplex、会话计数、空用户恢复、Stop/Prepare、C01 心跳 pending/序列 ACK；它不自动获得 Xray limiter |
| 全局设备 | alive 404/405 准备阶段回退、ClaimDevice 超时/退避/有界等待、撤销 fence、online 保活与批量上限 |
| TLS / NW3 | 文件/ACME/self 证书、self pin、TLS 1.3、NW3/version/ALPN/共享密钥、模块许可证与外部 interop 边界 |
| Xray / QUIC | fork replacement、MPTCP 默认、SS2022 密钥、SS none/plain、REALITY 版本、TUIC 首请求并发、出站和 UDP 行为 |
| 流量 / 重载 | 准备后切换、失败回滚、用户撤销前取数、跨 core 重载和移除实例保留 pending；不是磁盘持久化/exactly-once |
| CI / 部署 | fork 安装来源、发布 tag 重建、Android 链接参数、Linux 与嵌套模块测试、现有容器配置，不照抄兄弟 backend 的部署方案 |

## 3. 按实际 panel 合约适配

同时读取[backend 当前约束](../../../backend/.ai/prompts/current-customizations.md)和相关实际代码。backend 已有设备 API 和 NW3 用户转换；旧笔记的“等待 panel 升级”不能再当当前阻塞。反过来，面板保存了 SSR/socks/http/naive 也不代表 node 支持，不能只开放 accept-list。

修改 Protobuf 必须连同 schema、生成 Go、类型转换、身份字段和测试核对；不手改生成文件去伪造字段。若只有一侧支持实例 ID，继续旧 JSON fallback。保留 panel 的 Shadowsocks legacy obfs 与 Mieru transport/multiplex 投影。

不要为了同步节点升级 backend 的 ORM/数据库迁移；那是另一仓库的显式需求。不要修改全局 Go module cache 或运行真实 `config-dev.yml`。

## 4. 选择最小验证并保留证据

先执行直接覆盖改动的已有测试；失败时先定位，依赖缺失或变更 manifest 才按需恢复依赖。完整同步再执行：

```powershell
go test .\... -count=1 -timeout=8m
if ($LASTEXITCODE -ne 0) { throw 'root tests failed' }
go -C .\pkg\nowhere test .\... -count=1 -timeout=8m
if ($LASTEXITCODE -ne 0) { throw 'nested tests failed' }
go build .\...
if ($LASTEXITCODE -ne 0) { throw 'build failed' }
```

根据平台和实际范围补 Linux `-race`、Unix 私钥权限、静态构建、协议客户端测试；外部 Rust 测试仅在明确提供 `NOWHERE_RUST_VECTOR_BINARY` 时运行。不要将 skip、缺环境、单元测试、mock panel 和真实面板联调统称“端到端通过”。运行构建不得覆盖用户已有产物；隔离测试使用回环/临时端口，完成后清理自己创建的临时资源。

## 5. 更新文档后交付

- 历史同步：`.ai/sync-diffs/<last-tag>-to-<TAG>.md`，必要时配套 `.files.md`；固定官方 SHA、本地父/落点、处理决策和全部改动路径。
- 当前定制：更新日期化 local 台账和索引，区分 fork 存量与落点后增量；把未提交工作单列，保留采样时间和历史证据。
- 更新 [台账入口](../sync-diffs/README.md)、[prompts 入口](./README.md)、[当前约束](./current-customizations.md)及受影响的契约说明；只保存 Markdown，不创建额外结构化清单。
- 文档写真实行为、回归入口和未验证项，不把业务代码提交视为上线，也不把历史“正在跑”当作当前仍有任务运行。
- README 不恢复成任务台账；交付摘要列出完成项、兼容保留项、实测/跳过/未实现项，不擅自提交或部署。
