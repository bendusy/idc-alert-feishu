package tmpl

import (
	amtmpl "github.com/prometheus/alertmanager/template"
	"github.com/stretchr/testify/require"
	"testing"
)

func fa(status, severity string) amtmpl.Alert {
	return amtmpl.Alert{Status: status, Labels: map[string]string{"severity": severity}}
}

func TestMaxFiringSeverity(t *testing.T) {
	mention := funcMap["mentionIf"].(func([]amtmpl.Alert, []string) string)
	ben := []string{"ou_ben"}

	t.Run("critical firing → @", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "critical")}, ben)
		require.Equal(t, "<at id=ou_ben></at> ", got)
	})

	t.Run("error firing → @", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "error")}, ben)
		require.Equal(t, "<at id=ou_ben></at> ", got)
	})

	t.Run("warn only → 不 @", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "warn")}, ben)
		require.Equal(t, "", got)
	})

	t.Run("info only → 不 @", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "info")}, ben)
		require.Equal(t, "", got)
	})

	t.Run("混合 error+warn → @（取 max=error）", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "warn"), fa("firing", "error")}, ben)
		require.Equal(t, "<at id=ou_ben></at> ", got)
	})

	t.Run("复审#5: resolved critical + firing warn → 不 @", func(t *testing.T) {
		// resolved 的 critical 不应算进 max severity，否则误 @
		got := mention([]amtmpl.Alert{fa("resolved", "critical"), fa("firing", "warn")}, ben)
		require.Equal(t, "", got)
	})

	t.Run("空 openID 跳过", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "critical")}, []string{"", "ou_x"})
		require.Equal(t, "<at id=ou_x></at> ", got)
	})

	t.Run("at all", func(t *testing.T) {
		got := mention([]amtmpl.Alert{fa("firing", "critical")}, []string{"all"})
		require.Equal(t, "<at id=all></at> ", got)
	})
}

func TestMaxSeverityColor(t *testing.T) {
	color := funcMap["maxSeverityColor"].(func([]amtmpl.Alert) string)

	require.Equal(t, "red", color([]amtmpl.Alert{fa("firing", "critical")}))
	require.Equal(t, "orange", color([]amtmpl.Alert{fa("firing", "warn"), fa("firing", "error")}))
	require.Equal(t, "yellow", color([]amtmpl.Alert{fa("firing", "warn")}))
	// resolved critical 不影响颜色
	require.Equal(t, "yellow", color([]amtmpl.Alert{fa("resolved", "critical"), fa("firing", "warn")}))
}
