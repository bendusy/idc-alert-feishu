# 开发需求：长任务进度播报客户端工具链（task-progress reporter）

> 状态：草拟 / 待评审
> 提出：2026-06-15
> 关联提交：`56f97f5`（服务端 `/progress/<group>` 端点已实现）、`0f2170e`（体检修复）
> 关联 LTO run：`.lto/20260613-145130-idc-alert-feishu-d0405d7f`

## 0. 背景与现状（已核实，勿重做）

服务端 `/progress/<group>` 端点**已开发完成并线上可用**，不在本需求范围内：

- 入口：`POST /progress/{group}`，body `{"summary": "...", "detail": "..."}`（精简格式，旁路 Alertmanager）
- 行为：包装成 1 条 `severity=info` 的 firing alert → 复用 `idc.tmpl` 渲染成**灰色卡片、不 @ 人** → 即时逐条发，无 group/dedup/inhibit/resolved
- 校验：未知 group → 400；空 summary → 400；超限 → 429
- 限流：per-project `newRateLimiter(1, 5)`（每 group ≤1 条/秒、突发 5）
- 群路由：group ∈ `{default, idc-infra, memory-flow, banwen-flow, arcflow}`，url+sign 由 adapter 的 `config.yml` 统一持有
- 实现位置：`server/server.go:90-149`，测试 `server/progress_test.go`（5 case 全绿）
- 线上验证：`curl -XPOST http://10.11.12.13:18930/progress/idc-infra -d '{"summary":"x"}'` → 200

**真正缺的、本需求要解决的**：调用方（长任务脚本）目前得自己拼 JSON + POST，没有便捷客户端；且存在反面教材——`axis:/usr/local/sbin/sdf1-report.sh` 自己拼飞书 webhook + **硬编码 sign 密钥**（技术债：密钥应只在 adapter 一处管理）。

## 1. 目标

为"超长 / 耗时任务"（btrfs device delete、dd 写盘、rsync 大迁移、PBS 备份、immich 导入、btrfs scrub…）提供一套**标准化进度播报客户端**，让任何长任务**一行命令 / 一个探针文件**即可定期把进度卡片推到飞书，且：

1. 不再硬编码任何飞书 sign 密钥——全部经 adapter `/progress` 端点（密钥单点管理）
2. 自动算**迁移速率 + ETA**
3. **自适应频率**：ETA<2h → 30min/次；ETA>6h → 2h/次；其间线性
4. 任务**完成自动发完成卡 + 自注销 cron**
5. 加新任务类型 = 丢一个探针文件，不改框架

## 2. 交付物

### 2.1 客户端脚本 `scripts/task-progress`（POSIX sh / bash）

一个零依赖（仅 `curl`+`python3`/`awk`）的客户端，封装对 `/progress/<group>` 的调用：

```
task-progress send   --group idc-infra --summary "..." --detail "..."
task-progress watch  --probe /etc/task-progress.d/sdf1.conf   # 单次：读探针→算速率→send
task-progress register <probe>     # 装 cron（按 ETA 自适应频率）
task-progress unregister <probe>   # 抽完自动调用
```

- adapter 地址默认 `http://127.0.0.1:18930`（axis 本机），可 `--endpoint` 覆盖
- `send` 失败（非 200）要有重试（应对偶发 429：退避后重发）

### 2.2 探针规范 `/etc/task-progress.d/<name>.conf`

每个长任务一个小文件，定义如何取进度（shell 变量 + 函数）：

```sh
GROUP="idc-infra"
TITLE="sdf1 抽盘"
# 取「剩余量」字节（必填）：输出纯字节数到 stdout
remaining_bytes() { sudo btrfs filesystem show /mnt/bulk | awk '/sdf1/ {...}'; }
# 判完成（必填）：完成 exit 0
is_done() { ! sudo btrfs filesystem show /mnt/bulk | grep -q /dev/sdf1; }
# 进程健康（选填）：用于卡片显示 🟢/🔴
is_healthy() { pgrep -f "btrfs device delete.*sdf1" >/dev/null; }
```

框架负责：读 `remaining_bytes` → 对比上次（state `/var/lib/task-progress/<name>.last` 存 `字节|时间戳`）→ 算速率/ETA → 组装 summary+detail → 调 `task-progress send` → `is_done` 真则发完成卡 + `unregister`。

### 2.3 探针模板库 `scripts/probes/`（大而全，覆盖常见长任务）

预置可直接套用的探针：`btrfs-device-delete.conf`、`dd-disk.conf`、`rsync.conf`、`dir-grow-du.conf`、`pbs-backup.conf`、`immich-import.conf`、`btrfs-scrub.conf`。每个含取进度的标准写法 + 坑注释。

### 2.4 文档

- `docs/progress-endpoint.md`：服务端 `/progress` 端点契约（补 README 缺失）
- `docs/task-progress-client.md`：客户端用法 + 探针编写指南 + 自适应频率说明

## 3. 卡片内容约定（summary/detail 怎么填）

由于服务端把 summary 当 alertname、detail 当 description，客户端组装时：

- `summary`：`📦 <TITLE> · 剩 <human> · <rate>` —— 一眼看清，进群通知预览友好
- `detail`：多行，含 `当前剩余 / 上次剩余 / 迁移速率 / ETA / 进程 🟢|🔴 / 池数据状态`
- 完成时 `summary`：`✅ <TITLE> 完成`，detail 给收尾提示（如"可安全拔盘"）

> 注：进度卡是灰色 info 卡（端点写死 severity=info），不变色不 @ 人——符合"进度通知不该像告警那样刷屏打扰"。若未来要完成卡变绿，需服务端支持 severity 参数（**单独评估，不在本需求**）。

## 4. 自适应频率实现

`register` 时不写死 cron 周期，而是装一个**每 30min 跑一次的 cron**，框架内部按 ETA 决定**本次是否真的发**：

- ETA ≤ 2h：每次都发（≈30min）
- 2h < ETA ≤ 6h：每 2 次发 1 次（≈1h）
- ETA > 6h：每 4 次发 1 次（≈2h）
- 首次无 ETA（无上次采样）：发，并标注"基线建立中"

state 里额外记 `skip_counter`。好处：cron 本身固定简单，频率逻辑在脚本里、随 ETA 动态变。

## 5. 迁移现存 sdf1 脚本（验收用例 + 消除技术债）

把 `axis:/usr/local/sbin/sdf1-report.sh` 重写为 `/etc/task-progress.d/sdf1.conf` 探针 + 走 `task-progress` 框架：

- 删除硬编码 `WEBHOOK` / `SIGN_SECRET`（改走 adapter `/progress/idc-infra`）
- 保留已验证的速率/ETA/完成自注销逻辑
- 切换前后各发一张卡对比，确认样式一致或更好

这是本需求的**首个落地验收**：sdf1 抽盘当前在跑（剩 ~2.9TiB），是天然的真实测试场。

## 6. 验收标准

- [ ] `task-progress send` 对 4 个 group 均能 200 推卡，sign 零硬编码
- [ ] 429 退避重试生效（连发 6 次不丢最后一条）
- [ ] sdf1 探针接入，飞书每自适应周期收到进度卡（含速率/ETA）
- [ ] 抽盘完成 → 自动完成卡 + cron 自注销（`/etc/cron.d/` 无残留）
- [ ] cron 裸环境 `env -i PATH=/usr/bin:/bin` 实测 exit=0
- [ ] 至少 2 个探针模板（btrfs-device-delete + 另 1 个）经真实或仿真验证
- [ ] README 增加 progress 端点 + 客户端章节
- [ ] 单测覆盖客户端的速率/ETA/自适应频率纯函数部分

## 7. 非目标（明确不做）

- 不改服务端 `/progress` 端点（已完成）
- 不做完成卡变色 / severity 参数化（单独评估）
- 不做 Web UI / 任务编排 / 多机聚合（YAGNI）
- 不接入 Prometheus（进度是事件流，不是时序指标）

## 8. 风险

- adapter 限流 1/s burst 5：长任务低频上报远低于此，无忧；但若多任务同 group 并发上报可能撞限流 → 客户端退避兜底
- `/var/lib/task-progress/` 需预创建且 root 可写（cron 以 root 跑）
- 探针里 `sudo` 在 cron root 上下文免密——依赖 axis 现有 NOPASSWD（已具备）
