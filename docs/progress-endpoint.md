# `/progress/<group>` 进度端点契约

`/progress/<group>` 是给脚本和长任务使用的轻量进度通道，旁路 Alertmanager，但仍复用本服务的飞书机器人配置、签名和卡片模板。

## 请求

```http
POST /progress/idc-infra
Content-Type: application/json

{"summary":"📦 sdf1 抽盘 · 剩 2.9TiB · 18.2MiB/s","detail":"多行详情"}
```

字段：

- `summary`：必填，非空。服务端会放入 `alertname` 和 `annotations.summary`。
- `detail`：可选。服务端会放入 `annotations.description`。
- `group`：路径参数，必须存在于 `config.yml` 的 `bots`。

## 行为

- 成功返回 `200 ok`。
- 未知 group 返回 `400 group not found`。
- JSON 无法解析返回 `400`。
- `summary` 为空返回 `400 summary required`。
- 同一 group 进度上报限流为 `1/s`、突发 `5`，超限返回 `429 rate limited`。

服务端会把请求包装成 1 条 `severity=info` 的 firing alert，复用 `idc.tmpl` 渲染为灰色卡片，不触发 @ 人策略。

## 客户端约束

调用方不要直接拼飞书 webhook，也不要持有飞书 sign 密钥。长任务应通过 `scripts/task-progress send` 或 `watch` 访问本端点，让密钥继续只保留在 adapter 的 `config.yml` 中。
