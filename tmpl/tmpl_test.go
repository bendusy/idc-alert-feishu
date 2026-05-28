package tmpl

import (
	"github.com/prometheus/alertmanager/template"
	"github.com/stretchr/testify/require"
	"github.com/xujiahua/alertmanager-webhook-feishu/model"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFeishuCard(t *testing.T) {
	alerts := model.WebhookMessage{Data: newAlerts()}
	et := embedTemplates["default.tmpl"]
	err := et.Execute(os.Stdout, alerts)
	require.Nil(t, err)
}

func TestIDCAlertTemplate(t *testing.T) {
	t.Run("with HANDBOOK_BASE_URL", func(t *testing.T) {
		os.Setenv("HANDBOOK_BASE_URL", "https://handbook.example.com/")
		defer os.Unsetenv("HANDBOOK_BASE_URL")

		alert := model.Alert{
			Status: "firing",
			Labels: map[string]string{
				"asset_id": "vm-app-1",
				"severity": "critical",
				"source":   "verify-sh",
			},
			Annotations: map[string]string{
				"summary":     "vm down",
				"description": "probe failed 3 times",
			},
			StartsAt: time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC),
		}
		et := embedTemplates["idc_alert.tmpl"]
		require.NotNil(t, et, "idc_alert.tmpl should be loaded")

		buf := &strings.Builder{}
		err := et.Execute(buf, alert)
		require.Nil(t, err)

		out := buf.String()
		t.Log(out)
		// 验证 wikilink 渲染（trailing slash 应被去除）
		require.Contains(t, out, "[vm-app-1](https://handbook.example.com/vm-app-1)")
		require.Contains(t, out, "**严重度**：`CRITICAL`")
		require.Contains(t, out, "probe failed")
	})

	t.Run("without HANDBOOK_BASE_URL falls back to inline code", func(t *testing.T) {
		os.Unsetenv("HANDBOOK_BASE_URL")
		alert := model.Alert{
			Labels: map[string]string{"asset_id": "host-nas", "severity": "error"},
		}
		buf := &strings.Builder{}
		err := embedTemplates["idc_alert.tmpl"].Execute(buf, alert)
		require.Nil(t, err)
		require.Contains(t, buf.String(), "`host-nas`")
		require.NotContains(t, buf.String(), "http")
	})

	t.Run("severityColor mapping", func(t *testing.T) {
		fn := funcMap["severityColor"].(func(string) string)
		require.Equal(t, "red", fn("critical"))
		require.Equal(t, "red", fn("CRITICAL"))
		require.Equal(t, "orange", fn("error"))
		require.Equal(t, "yellow", fn("warn"))
		require.Equal(t, "yellow", fn("warning"))
		require.Equal(t, "grey", fn("info"))
		require.Equal(t, "blue", fn("unknown"))
	})
}

// copyright: https://github.com/tomtom-international/alertmanager-webhook-logger/blob/master/main_test.go#L132
func newAlerts() template.Data {
	return template.Data{
		Alerts: template.Alerts{
			template.Alert{
				Status:       "firing",
				Annotations:  map[string]string{"a_key": "a_value"},
				Labels:       map[string]string{"l_key": "l_value"},
				StartsAt:     time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				EndsAt:       time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC),
				GeneratorURL: "file://generatorUrl",
			},
			template.Alert{
				Annotations: map[string]string{"a_key_warn": "a_value_warn"},
				Labels:      map[string]string{"l_key_warn": "l_value_warn"},
				Status:      "warning",
			},
		},
		CommonAnnotations: map[string]string{"ca_key": "ca_value"},
		CommonLabels:      map[string]string{"cl_key": "cl_value"},
		GroupLabels:       map[string]string{"gl_key": "gl_value"},
		ExternalURL:       "file://externalUrl",
		Receiver:          "test-receiver",
	}
}
