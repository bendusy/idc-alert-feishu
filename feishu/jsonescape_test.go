package feishu

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/prometheus/alertmanager/template"
	"github.com/stretchr/testify/require"

	"github.com/bendusy/idc-alert-feishu/model"
)

// newBot 用内置模板构造一个 Bot，不依赖 config.yml。
func newBot(t *testing.T) *Bot {
	tpl, alertTpl, err := getTemplates(nil)
	require.NoError(t, err)
	return &Bot{tpl: tpl, alertTpl: alertTpl}
}

// TestPreprocessAlerts_ProducesValidJSON 锁死 BUG2：
// alert 字段（body）与 GroupLabels（header title）含双引号等危险字符时，
// 渲染出的整条卡片必须仍是合法 JSON。
func TestPreprocessAlerts_ProducesValidJSON(t *testing.T) {
	b := newBot(t)

	msg := &model.WebhookMessage{
		Data: template.Data{
			Alerts: template.Alerts{
				template.Alert{
					Status: "firing",
					Annotations: map[string]string{
						// 含双引号 + 控制字符，正是会破坏 JSON 的输入
						"summary":     `disk "sda" full` + "\x1b",
						"description": "line1\nline2 with \" quote",
						"runbook_url": "https://rb.example.com?a=1&b=2",
					},
					Labels: map[string]string{"l": `v"x`},
				},
			},
			// header title 注入面：alertname 含双引号
			GroupLabels: map[string]string{"alertname": `name"injected`},
		},
		// note 区注入面：meta value 含双引号
		Meta: map[string]string{"link": `v"x`},
	}

	require.NoError(t, b.preprocessAlerts(msg))

	var buf bytes.Buffer
	require.NoError(t, b.tpl.Execute(&buf, msg))

	// 核心断言：整条输出必须能被 JSON 解析（注入字段没有破坏结构）
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"rendered card must be valid JSON, got: %s", buf.String())

	// 控制字符应被清除
	require.NotContains(t, buf.String(), "\x1b")
}
