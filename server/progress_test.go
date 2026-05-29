package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/xujiahua/alertmanager-webhook-feishu/feishu"
	"github.com/xujiahua/alertmanager-webhook-feishu/model"
)

// captureBot 捕获最后一次 Send 的 message，便于断言
type captureBot struct {
	last  *model.WebhookMessage
	count int
}

func (c *captureBot) Send(m *model.WebhookMessage) error {
	c.last = m
	c.count++
	return nil
}

func newProgressServer(bot feishu.IBot) *Server {
	return New(map[string]feishu.IBot{"mf": bot}, false)
}

func postProgress(s *Server, group, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/progress/"+group, strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"group": group})
	w := httptest.NewRecorder()
	s.progress(w, req)
	return w
}

func TestProgress(t *testing.T) {
	t.Run("正常上报 → info firing alert", func(t *testing.T) {
		bot := &captureBot{}
		s := newProgressServer(bot)
		w := postProgress(s, "mf", `{"summary":"reindex 完成","detail":"耗时 3s"}`)

		if w.Code != http.StatusOK {
			t.Fatalf("应 200，得 %d", w.Code)
		}
		if bot.last == nil {
			t.Fatal("应调用 bot.Send")
		}
		a := bot.last.Alerts.Firing()
		if len(a) != 1 || a[0].Labels["severity"] != "info" {
			t.Fatalf("应为 1 条 info firing alert，得 %+v", a)
		}
		if a[0].Annotations["summary"] != "reindex 完成" {
			t.Errorf("summary 丢失")
		}
	})

	t.Run("未知 group → 400", func(t *testing.T) {
		s := newProgressServer(&captureBot{})
		w := postProgress(s, "unknown", `{"summary":"x"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("应 400，得 %d", w.Code)
		}
	})

	t.Run("空 summary → 400", func(t *testing.T) {
		s := newProgressServer(&captureBot{})
		w := postProgress(s, "mf", `{"detail":"无摘要"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("应 400，得 %d", w.Code)
		}
	})

	t.Run("超限 → 429", func(t *testing.T) {
		bot := &captureBot{}
		s := newProgressServer(bot)
		// burst=5，第 6 次应 429
		var last int
		for i := 0; i < 6; i++ {
			last = postProgress(s, "mf", `{"summary":"spam"}`).Code
		}
		if last != http.StatusTooManyRequests {
			t.Fatalf("第 6 次应 429，得 %d", last)
		}
		if bot.count != 5 {
			t.Fatalf("应只发 5 条，得 %d", bot.count)
		}
	})
}
