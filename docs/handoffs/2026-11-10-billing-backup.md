# Billing 二开暂停备份与恢复交接（2026-11-10）

## 用途与本次边界

用户计划暂时让 `main` 回到上游正式发布版本，要求先梳理交接、建立备份分支，保留将来继续二开的可能。本次只做本地保存和交接：不切换或重置 `main`，不推送、不发布、不部署，不继续下载依赖或运行产品测试，不恢复旧 provider 会话。用户已选择不新建 Trellis 任务。

主备份分支是 `backup/billing-before-upstream-2026-11-10`。它从最后已合入的 fork 基线 F 创建，只增加本交接文档；它的父提交就是 F，可以找回二开完成到 PR #35 时的源码。另有两个明确标为 WIP 的本地备份分支保存未提交源码。WIP 提交只是恢复用快照，不代表功能完成、测试通过或允许合并/发布。

本交接是 2026-11-10 的恢复入口。旧文档、日志和 task.json 作为历史证据原样保留，不能把其中早期的 planning/待合并、UNRUN、旧许可或下一步指令直接当成今天的状态。历史产品实现暂停；以后继续时应从本交接及实际仓库核验开始。

## 已核验的基线

| 名称 | 提交 / 版本 | 含义 |
| --- | --- | --- |
| 当前本地 `main` | `1d0ae7b3868300567fc3887219c132ea64969afc` | PR #25 合入处；已跟踪文件无改动，有未跟踪任务/日志文档 |
| 本地缓存 `origin/main` | `274b61f15` | 缓存停在 PR #28，本地 main 相比它落后 8 个提交；不能用它代表当前远端 |
| F：远端 fork `main` / 同步前二开基线 | `27403ff4aec375fe5c0ab6a0a73501041ce57ad4` | 2026-11-10 通过 GitHub commits/main API 只读确认，包含 PR #34、#35；对象已在本地 |
| 旧同步候选的 U | `9e6687e3a5d1594c961e9c355a7dce64247b0872`，`v0.1.3217` | 原同步工作区的 MERGE_HEAD；尚未形成最终合并提交 |
| F/U 共同祖先 B | `8ac1c7a563e2b2754054346e24ba22848539f98c`，`v0.1.2519` | 旧同步设计所固定的共同基线 |
| 历史 fork 发布 | `38d82052d995e7dc6174d357f108f7fbe84ff44d`，`v0.1.2310-rc.0` | 403 研究引用过此发布源码，不等于当前开发基线或已重新核查的部署版本 |
| 核查时上游最新正式发布 | `v0.1.3504` | GitHub releases/latest 返回，发布时间 `2026-11-10T03:01:30Z`；不是旧候选的 v0.1.3217 |

最新发布来源：<https://github.com/yetone/magpie/releases/tag/v0.1.3504>。该接口给出 `target_commitish=main`，这不是不可变的 tag 提交 SHA。随后查询 tag ref 的请求遇到本机 TLS 证书验证失败，未关闭校验或修改网络配置；本次未取得该 tag 的精确提交，也没有 fetch/reset。未来真正切换 main 时，需重新确认当时的最新正式发布、取得并验证 tag→commit，不能拿移动中的 upstream/main 或上述日期快照代替。

## 备份结构

| 分支 | 内容 | 适用场景 |
| --- | --- | --- |
| `backup/billing-before-upstream-2026-11-10` | F 加本交接文档 | 回看已合入二开功能、从可靠历史基线重新规划 |
| `backup/billing-sync-wip-2026-11-10` | F 为唯一父提交，保存原同步工作区全部 2323 个变更/新增/删除产品路径 | 继续 v0.1.3217 同步研究或提取补丁；不能直接当成可发布的上游合并 |
| `backup/billing-gui-wip-2026-11-10` | `397c0b0f759c77472e8f405489b0aaa71398df4b` 为父，保存 GUI 工作区 5 个未提交路径 | 找回遗留 GUI/文档改动，先比对后续已合入版本，避免重复移植 |

同步 WIP 使用单父快照，故意没有将 U 记作已验收的 merge parent。原工作区的真实 index、MERGE_HEAD、合并消息和 staged/unstaged 差异另外保存。Git 快照保存实际文件字节；原始归档也保留原始换行。原有工作区和分支均不被切换、提交或清理。

本机外置备份根：`E:/97-SoftCache/_backup/magpie/2026-11-10-pre-upstream/`。这是长期开发数据，不能按临时缓存清理。主要文件：

- `HANDOFF.md`：本交接原文；主备份分支内位置为 `docs/handoffs/2026-11-10-billing-backup.md`。
- `branch-map.json`：三条新分支的完整提交与树 SHA。
- `repository.bundle`：全部 Git refs 的离线对象包，包含三条新备份分支与既有历史。
- `worktree-state.zip`：17 个工作区的 Git 元数据、staged/unstaged binary patch，以及有改动工作区的原始文件、未跟踪源码、任务文档和日志证据。
- `inventory.json`：各工作区原路径、HEAD/branch、原 status/index 哈希、逐文件 SHA256、删除项和明确排除项。
- `refs-before.txt`：备份前全部引用，保留本地 main 与陈旧 remote-tracking refs 的事实。
- `verification.json`、`SHA256SUMS.txt`：最终恢复校验结果与外置文件校验值。
- `capture.py`、`snapshot_refs.py`、`verify.py`：本次保存/检查过程，供审计；不是以后要直接重复运行的产品脚本。

归档排除已识别的构建缓存、合成测试 HOME/临时目录和生成的可执行文件；原文件仍留在原 E 盘工作区，逐项排除见 inventory。任务/日志不进入 Git 提交。没有读取或复制真实认证目录、线上账号、AstrLink 数据目录或旧会话内部状态。原始 Orca handoff 文本只读，路径及 SHA256 记在 inventory，未修改或删除。

## 当前工作区与材料入口

| 归档 workspace id | 原位置 | 角色 |
| --- | --- | --- |
| `main` | `D:/workspace/PersonalProjects/magpie` | 本地主工作区；10 月任务历史、当前 403 协调记录和开发日志 |
| `wt-billing1` | `E:/97-SoftCache/tmp/wt-billing1` | 历史协调区，父任务及 Billing 1–5 归档；包含 fetch1 的最终批准原件和 provider 阻塞记录 |
| `billing-sync-preflight-1107` | `E:/97-SoftCache/tmp/orca-magpie-1107/magpie/billing-sync-preflight-1107` | 当前最完整的 R5 产品候选、同步方案、runner、测试日志与冻结清单 |
| `wt-billing5-gui` | `E:/97-SoftCache/tmp/wt-billing5-gui` | 4 个已跟踪改动和 1 个未跟踪 GUI 测试，独立于同步候选 |
| `zzzcoding-client-diagnosis-1107` | `E:/97-SoftCache/tmp/orca-magpie-1107/magpie/zzzcoding-client-diagnosis-1107` | 已暂停的 403 静态研究，28 个未跟踪材料，产品源码无改动 |

其他工作区及完整 HEAD 以 `inventory.json` 为准。包括 billing-fix、billing-fix-usage、billing-4、billing-5、billing-6、detached wt-billing6b，以及 Codex TMP、git-head、hub-roots、decode-share、unrouted-trace 等修复工作区；核查时其源码均干净。无需重新开工这些已合入修复。

ZIP 内结构为 `workspaces/<id>/files/<原相对路径>`。下面相对路径均从该 workspace 的根开始：

| 阅读顺序 | workspace id / 文件 | 用途 |
| --- | --- | --- |
| 1 | `wt-billing1` / `.trellis/tasks/11-03-provider-billing/prd.md` | 用户选择、B1–B8 总目标、后续审计纠正；须读到末尾 |
| 2 | `billing-sync-preflight-1107` / `.trellis/tasks/11-03-billing-integration/research/orca-sync-decisions.md` | 三项兼容策略、F/U/B、同步先于 D4 的顺序 |
| 3 | 同上 research / `sync-plan.md`、`orca-sync-preflight.md` | 冲突清单、发送边界、兼容和审查要求 |
| 4 | 同上 research / `sync-implementation-r4.md`、`sync-adapters-r5-checkpoint.md` | 已做的预算重放与音频适配、仍缺的路由覆盖 |
| 5 | 同上 research / `sync-working-tree-manifest-r5.json` | 当前全部 2323 个产品变更/新增/删除路径的冻结 SHA256 |
| 6 | 同上 research / `sync-budget-slot1-result.md`、`sync-replay-slot2-result.md`、`sync-audio-slot3-result.md` | 三次真实执行的范围、结果和局限 |
| 7 | `wt-billing1` / integration/research/`orca-sync-dependency-fetch1-approved.md`、`orca-sync-provider-block-1107.md` | 最后许可原件及会话错误；不能把批准当成已下载 |
| 8 | `billing-sync-preflight-1107` / integration/research/`sync-dependency-fetch1-plan.md`、两个 fetch1/slot4 runner | 已审查下载方案和禁用的后续测试草案 |
| 9 | 同 workspace / integration 的 `prd.md`、`design.md`、`implement.md`，research/`activation-obligations.md`、`d4-write-visibility.md` | 待实现的 D4 提示方案和剩余运行时集成 |
| 10 | `wt-billing1` / `.trellis/tasks/archive/2026-11/11-03-billing-*` | 已交付组件的历史 PR、验证、局限 |

表内 `integration` 缩写指 `.trellis/tasks/11-03-billing-integration`；`research` 均在该任务内。

## 已合入的二开进度

以下合入事实已与本地 F 的提交图核对；CI 数字和人工验收结论来自保留的历史材料，本次没有重跑或重新查询所有 Actions。

| 阶段 | 已完成内容 | 合入/证据与限制 |
| --- | --- | --- |
| Billing 1 | 保存 inactive billing policy、纯决策与省略字段兼容 | PR #23；不能等同于运行时激活 |
| Billing 2 | inactive quota 状态、证据与私有存储 | PR #24；周期性模型探测明确推迟，不能自动付费续用 |
| Billing 3 | dormant 调用时计费 envelope/统计基础及 race CI | PR #25；完整 spend/value/savings 运行链仍未整体激活 |
| 并发修复 | unrouted trace 等待、会话解码 unique handles 保活 | PR #26/#27 及修正 #29 等；历史缺陷不应重复实现 |
| Billing 5 | 路由组内成员顺序打破策略剩余平局 | PR #28；不把付费模型改为免费；有 Until used up、By weight、laneMate 等例外 |
| Billing 4 | 两类 provider editor 的 List price / Free / Free quota 主控件 | PR #30，仍写 legacy countFree/wildcard；quota 耗尽策略和状态显示待集成。包含 holdModal 修复 |
| Billing 6 / D4 读失败 | usage 无法读取时 limited key 返回 503 `gateway_key_usage_unread`，不向上游发送，UI/CLI 不显示假零 | PR #31；历史三 OS Test 通过，GUI WebKit 分片曾挂起/取消，不能称全 GUI 绿 |
| Billing 6 / D4 写入与预留 | `usage-holds.jsonl` 持久 open/done/rec，截断请求按 reservation 计，未写入记录保留；预留日志不可写返回 503 `gateway_key_usage_unrecorded` | PR #34，`edc05f255`；历史三 OS Test attempt 1 通过。按用户决定没有 fsync，不承诺断电安全 |
| Windows 修复 | Codex helper TMP/TEMP、gitHead 路径分隔、hub roots 测试路径 | PR #32/#33/#35；F 为 `27403ff4a` |

主任务 task.json 还写着 `planning`、旧 implement checklist 未打勾；这属于历史记录未同步，不表示 Billing 1–5 未合入。另一方面，部分子任务 `completed` 只表示 inactive/dormant 组件范围完成，不能推导出整个 Billing B1–B8 完成。Billing 6 与父任务均不得按整体完成归档。

## 未完成的同步候选与技术决定

原工作区分支 `lauchiwa/billing-sync-preflight-1107` 保持 HEAD=F、MERGE_HEAD=U；无 unmerged index entries，但仍有 staged 和 unstaged 实现，未形成最终 merge commit。2026-11-10 重新核验全部 2323 个产品路径与 R5 冻结清单匹配。

用户已接受三项兼容策略：

1. 保留上游按模型限定的额度和组内 fallback，同时把二开持久预留精确落实到实际模型、caller/issuer、scope/window 和每次真实发送。不确定旧 hold 不能当零，也不能套用到无关 scope。
2. 上游零价展示与二开 quota/free 分类分开；保持显式付费覆盖与旧 CountFree 优先级。reference/value 按实际 served model；不借同步偷偷激活 dormant Billing，不回填历史。
3. 保留上游 opt-in sign-in sync，不能自动开启；新增 restore/setter/read 路径必须遵守身份生命周期和 stale-write 安全，quota/journal/gap/生命周期私有状态仍留本地。仅合成数据验证。

R4 已加固按 attempt 的 durable admission/settlement/replay、usage accounting time、packed Hold、共享日志版本失效和真实分进程重放用例。R5 增加 shared retrieval 的六个 speech/transcription/translation audio aliases，要求完整 input/output 证据；显式零可结算，缺失/截断/失败不能推导为零，fallback 每次发送独立预留。

这仍是中间实现。chat/Responses 原生及翻译链、native/image/video、Codex HTTP/socket、decision/search 等子调用和 plugin 内部发送的 scoped parity 未全部交付；缺口处 fail-closed 不能被包装成“上游功能完整可用”。journal 持续增长与冷读反复 parse/replay 成本尚未解决，不能删除不确定证据来换性能。增量读取/索引与有界合成成本验证待做。

凭据兼容“R3”仍需较窄的 no-clobber/ownership 方案；先前提出的全局锁、generation sidecar、批量 ActivationPending 未获准，不得借旧批准落地。这里的“凭据 R3”是评审事项编号，与源码迭代 R3/R4/R5 不是同一概念。

四项 inherited staged whitespace 仍在：`internal/appdir/userdirs_test.go`、两份 codex prompt、`internal/gateway/testdata/grok_reasoning_then_call.sse`。旧报告的 staged diff-check exit 2 没有被“测试通过”覆盖。

## 实际验证与最后阻塞

| 轮次 | 已发生 | 不能据此声称 |
| --- | --- | --- |
| slot1 | R3 选择 `^TestAttempt` 的 budget 测试一次 exit 0，源码选择 9 顶层；非 verbose 日志仅 package OK；含编译约 163.719 秒 | 无逐项事件、无真实第二进程恢复、无全套通过 |
| slot2 | R4 budget/usage：29 顶层 PASS、1 helper 预期 SKIP、36 子测试 PASS；四组独立 writer/reader；含编译 46.296 秒 | 不证明断电、并发网关、timeout 清理或跨平台；采样没有抓到全部短命 child PID |
| slot3 | R5 的 23 个 gateway 测试在依赖解析失败，Go/runner exit 1，4.797 秒；没有任何 Test 事件 | 不是断言失败，不是编译通过，更不是 23 项跳过或通过 |
| dependency-fetch1 | 用户已授权两模块限定获取，协调者已批准固定 runner 一次；本次复查三个输出目录均不存在 | 没有执行/下载成功证据，消息被接受不等于命令执行 |
| slot4 | runner 是禁用草案，`DEPENDENCY_RESULT_SHA = None` | 未批准、未运行；旧 slot3 不可重跑当成 slot4 |

slot3 缺少 `github.com/gen2brain/h265@v0.2.4` 与 `github.com/santhosh-tekuri/jsonschema/v6@v6.0.3`。fetch1 只允许六个固定 `proxy.golang.org` URL、go.sum h1 校验、不跟随重定向、失败即停、仅写 E 盘任务目录，不运行 Go。原批准 SHA256：`5a7b69c4e002e67d9dc6f1fd7dd87577b25fb61794395aa7a85edfdcc7de7b3d`；runner：`def8b353156e7449989355086f63ec29e0825a515079cb3762b255bb97630dd3`。此次备份不消耗该许可；未来需先验证执行记录、哈希、工作区和任务是否仍适用。

旧 worker 因会话绑定 magpie、当前 provider 不符而无法继续；用户切回后又出现不同 OpenAI resource 的历史请求错误。本次仅以文件和 Git 为准，没有恢复或修改旧 provider 会话。Orca 只读状态显示同步工作区 inactive、无终端、原 worker state=done；这不是产品任务完成证明。

历史协调标识：run `run_64e42504a7e4`；task `task_6f165a9f5414`；dispatch `ctx_951dc9c92c6a`；worker session `2660b631-9222-4c3e-a65d-4e6cffb4c9d3`。这些仅用于定位历史，不是续发指令。

## D4 与其他剩余工作

D4 write-failure visibility 已在 2026-11-07 获准设计/实现，但约定先完成同步。目标是 best-effort 持久历史缺口提示：统一记录 usage 写失败及恢复、历史 gap 不因之后成功而消失；API/GUI/CLI 可见、空页面仍显示、CSV 警告走 stderr，unlimited key 不能仅因可选日志失败被阻断。当前没有把这项工作做成可验收产品；详见 integration 设计。

其余门槛包括：每条发送路径的真实计费归属、scope/caller/issuer 组合、quota 真实恢复证据、call-time envelope 及预算/历史所有消费者一致性、同步/旧 peer 兼容、两浏览器五语言 GUI、native OS/race/全套 CI、red-on-broken、真实独立 Claude 验收。旧 CI 绿不能覆盖 R5 未提交候选；未完成或取消的 GUI shard 不能记为通过。

`wt-billing5-gui` 保存 `docs/fork-releases.md`、`internal/gui/assets/freeprice.js`、`internal/gui/assets/plugins/routing/client.js`、`internal/gui/core_budget_fork_test.go` 和未跟踪 `internal/gui/tests/group-order-says.test.cjs`。它基于较早提交；部分语义可能已在后续 PR 实现，故只存档，不自动合入 F/R5。

zzzcoding 403 调查按用户要求暂停，专用 worker 已移除。报告未找到可确认导致原两次 403 的本地缺陷，没有修复；身份档案不是原样转发证明，原失败 Messages→Responses 与 Codex 原生路径有区别。磁盘 route history 存在但当时未读取对应线上历史，不能说已查尽。仅未来用户重新要求时再决定范围，不恢复指纹枚举、供应商探测或线上修改。

## 以后如何恢复

1. 先读本交接、`branch-map.json` 和 `verification.json`，按 `SHA256SUMS.txt` 校验 bundle/ZIP/inventory。保持原 E 盘目录和工作区；不要在尚未恢复完整前清理它们。
2. 在当前仓库可用时，`git show backup/billing-before-upstream-2026-11-10:docs/handoffs/2026-11-10-billing-backup.md` 读取文档。以主备份分支或其父 F 建立新的工作分支；Orca 管理的工作区通过 Orca 创建，位置仍优先 E 盘。保留备份引用不移动。
3. 若原仓库丢失，在 E 盘新目录初始化/克隆，先 `git bundle verify <bundle>`。可从 `repository.bundle` 克隆指定主备份分支，并用显式 refspec 获取两个 WIP 分支；bundle 不依赖原 `.git` 才能恢复历史。原工作区的绝对路径不是恢复前提。
4. 需要候选源码时，从 `backup/billing-sync-wip-2026-11-10` 开新工作分支。它是完整文件树，恢复源码不要求续接旧 provider 会话或原合并状态。若未来改为同步 v0.1.3504 或更晚版本，应重新做差异/语义兼容评估，再选取 R5 补丁；不要把旧候选冒充最新上游。
5. 解压 `worktree-state.zip` 到独立恢复目录，按 workspace id 取相应 `.trellis/tasks` 和研究证据；不要覆盖已有较新的任务文件。校验 inventory 逐文件哈希。需要重建 staged/unstaged 分界时，以对应原 HEAD 为起点，审查并应用 `staged.patch` 到 index，再应用 `unstaged.patch` 到工作树，补充原未跟踪文件。原 index 只作取证，不直接覆盖另一个 Git 版本/仓库的 index。
6. 重新核验 route parity、journal 成本、凭据 R3 和 D4 顺序，决定继续旧目标还是重新同步。旧 slot1/2/3 均已消耗；依赖未准备成功，slot4 没有获准。历史批准不代表测试已跑，也不应跳过冻结核验。
7. 后续若恢复产品开发，遵守用户此前指定的真实 Orca Codex 实现、Claude 独立验收及 CI 优先流程。先读当前版本的技能和仓库规则；不得复活已要求只读的旧 provider 会话。不要自动发送消息或重新派工。

真正切换 `main` 前，应完成最新 release tag 的精确解析、确认三条本地备份和离线包可恢复、处理主工作区仍有的个人任务文件，并明确只改本地还是也改远端。`reset --hard`、远端历史改写、推送和部署不是本次备份动作的一部分。此前每次 push 需新许可的约定继续有效；本地备份分支不表示已经有异机副本。

## 本次验证性质

只验证备份完整性：R5 冻结集合、归档逐文件 SHA256、ZIP 完整性、Git 快照树、bundle 恢复、旧工作区 HEAD/index/status 保持。没有跑新的 Go、browser、race、模型或供应商调用；没有把历史测试结论扩展到未验收源码。最终具体数量、SHA 和校验结果由同目录 `verification.json`、`branch-map.json` 提供。
