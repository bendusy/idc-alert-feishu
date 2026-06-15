# task-progress 客户端

`scripts/task-progress` 是长任务进度播报客户端。它只调用 adapter 的 `/progress/<group>`，不读取也不保存飞书 webhook/sign。

## 基本命令

```bash
scripts/task-progress send \
  --group idc-infra \
  --summary "📦 sdf1 抽盘 · 剩 2.9TiB · 18.2MiB/s" \
  --detail "ETA: 1d2h"

scripts/task-progress watch --probe /etc/task-progress.d/sdf1.conf
scripts/task-progress register /etc/task-progress.d/sdf1.conf
scripts/task-progress unregister /etc/task-progress.d/sdf1.conf
```

默认 endpoint 是 `http://127.0.0.1:18930`，可加 `--endpoint http://host:port` 覆盖。

`send` 遇到非 2xx 响应会重试，覆盖偶发 `429 rate limited`。默认最多 3 次，环境变量 `TASK_PROGRESS_RETRIES` 可调整。

## 探针文件

探针是 shell 片段，默认放在 `/etc/task-progress.d/<name>.conf`：

```bash
GROUP="idc-infra"
TITLE="sdf1 抽盘"

remaining_bytes() {
  btrfs filesystem show /mnt/bulk | awk '/sdf1/ {print 3135326126080}'
}

is_done() {
  ! btrfs filesystem show /mnt/bulk | grep -q /dev/sdf1
}

is_healthy() {
  pgrep -f "btrfs device delete.*sdf1" >/dev/null
}
```

必填：

- `GROUP`：adapter 配置里的 group 名称。
- `TITLE`：卡片标题。
- `remaining_bytes()`：输出纯字节数。
- `is_done()`：完成时 exit 0。

选填：

- `is_healthy()`：健康时 exit 0，卡片显示 `🟢 healthy`，否则显示 `🔴 unhealthy`。
- `extra_detail()`：输出追加到进度卡详情。
- `completion_detail()`：输出完成卡详情。

`watch` 会维护 `/var/lib/task-progress/<name>.last`，格式为 `剩余字节|时间戳|skip_counter`。

## 自适应发送频率

`register` 安装固定 30 分钟 cron：

```cron
*/30 * * * * root /path/to/task-progress watch --probe /etc/task-progress.d/name.conf ...
```

真正是否发卡由 `watch` 根据 ETA 判断：

- ETA ≤ 2h：每次采样都发，约 30 分钟一次。
- 2h < ETA ≤ 6h：每 2 次采样发 1 次，约 1 小时一次。
- ETA > 6h：每 4 次采样发 1 次，约 2 小时一次。
- 首次无 ETA：立即发一张基线卡。

## 模板

`scripts/probes/` 提供可复制模板：

- `btrfs-device-delete.conf`
- `dd-disk.conf`
- `rsync.conf`
- `dir-grow-du.conf`
- `pbs-backup.conf`
- `immich-import.conf`
- `btrfs-scrub.conf`

复制后只改变量，不改 `task-progress` 框架。新增任务类型时优先新增探针模板。
