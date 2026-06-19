package model

import "github.com/prometheus/alertmanager/template"

type WebhookMessage struct {
	// reference: https://prometheus.io/docs/alerting/latest/notifications/
	template.Data
	// @某人
	OpenIDs []string
	// 用于存储 AlertManager webhook 请求带来的数据，比如 query string
	Meta template.KV
	// 仅内置模板中使用，自定义模板中访问是空数组
	// 目前没有发现在 {{template defined_name .}} 后对其结果进行进一步处理的方式
	// 首先，通过模板，将每个 Alert 转为字符串，大段文本都在 content 字段，需要注意转义。
	FiringAlerts   []string
	ResolvedAlerts []string
	TitlePrefix    string
}

type Alert template.Alert

// ProgressOption 是 CC AskUserQuestion 选项卡里的单个选项。
type ProgressOption struct {
	Label       string
	Description string
}

// ProgressQuestion 是一个待选问题（含若干选项）。
type ProgressQuestion struct {
	// 1-indexed 展示编号
	Index    int
	Question string
	Options  []ProgressOption
	// OptionsMarkdown 由 Bot.SendProgress 预处理：把 Options 拼成编号 markdown
	// 并整段 JSONString 转义后填充，模板直接输出（避免在模板里渲染外部内容绕过转义）。
	OptionsMarkdown string
}

// ProgressMessage 是出站推送（进度/响铃/选项卡）的精简消息，
// 不复用告警的 WebhookMessage，避免被 severity/firing/resolved 等告警语义污染。
// 字段命名向 CC 官方 channels 契约靠拢（kind=channel/permission_request），
// 便于未来 iaf 并入 feishu_hub 升级成 channel 时不返工。
type ProgressMessage struct {
	// Kind: "progress"（普通进度/响铃）| "question"（CC permission_request/选项卡）
	Kind string
	// Color: 飞书卡片 header 颜色（blue/grey/green/red/yellow/orange...）
	Color string
	// GroupCN: 业务线中文名（卡片标题用）
	GroupCN string
	// Group: 业务线标识（如 idc-infra）
	Group string
	// TimeCN: 已格式化的中文时间
	TimeCN string

	// --- kind=progress ---
	Summary string
	Detail  string

	// --- kind=question ---
	// Project: cwd 项目名
	Project string
	Cwd     string
	// RequestID: CC 生成的 permission request_id（5 字母 a-z 去 l），
	// 本轮仅展示，为未来回控（card action 回 permission）预留。
	RequestID string
	Questions []ProgressQuestion
}
