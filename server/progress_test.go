package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bendusy/idc-alert-feishu/feishu"
	"github.com/bendusy/idc-alert-feishu/model"
	"github.com/gorilla/mux"
)

// captureBot 捕获最后一次 SendProgress 的 message，便于断言
type captureBot struct {
	last  *model.ProgressMessage
	count int
}

func (c *captureBot) Send(m *model.WebhookMessage) error { return nil }

func (c *captureBot) SendProgress(m *model.ProgressMessage) error {
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
	t.Run("正常上报 → progress 卡片", func(t *testing.T) {
		bot := &captureBot{}
		s := newProgressServer(bot)
		w := postProgress(s, "mf", `{"summary":"reindex 完成","detail":"耗时 3s"}`)

		if w.Code != http.StatusOK {
			t.Fatalf("应 200，得 %d", w.Code)
		}
		if bot.last == nil {
			t.Fatal("应调用 bot.SendProgress")
		}
		if bot.last.Kind == "question" {
			t.Errorf("默认应为 progress kind，得 %q", bot.last.Kind)
		}
		if bot.last.Summary != "reindex 完成" {
			t.Errorf("summary 丢失，得 %q", bot.last.Summary)
		}
		if bot.last.Detail != "耗时 3s" {
			t.Errorf("detail 丢失，得 %q", bot.last.Detail)
		}
		if bot.last.Group != "mf" {
			t.Errorf("group 应为 mf，得 %q", bot.last.Group)
		}
	})

	t.Run("状态 emoji → 颜色映射", func(t *testing.T) {
		cases := map[string]string{
			"🚦 开始": "blue",
			"✅ 完成": "green",
			"❌ 失败": "red",
			"🔔 响铃": "yellow",
			"普通进度":  "grey",
		}
		for summary, want := range cases {
			bot := &captureBot{}
			s := newProgressServer(bot)
			postProgress(s, "mf", `{"summary":"`+summary+`"}`)
			if bot.last == nil || bot.last.Color != want {
				got := ""
				if bot.last != nil {
					got = bot.last.Color
				}
				t.Errorf("summary %q 应映射 %q，得 %q", summary, want, got)
			}
		}
	})

	t.Run("question kind → 选项卡", func(t *testing.T) {
		bot := &captureBot{}
		s := newProgressServer(bot)
		body := `{"kind":"question","project":"idc-alert-feishu","cwd":"/p","request_id":"abcde",
			"questions":[{"question":"部署到哪?","options":[
				{"label":"axis","description":"部署到 axis"},
				{"label":"local","description":"本地跑"}]}]}`
		w := postProgress(s, "mf", body)

		if w.Code != http.StatusOK {
			t.Fatalf("应 200，得 %d", w.Code)
		}
		if bot.last == nil || bot.last.Kind != "question" {
			t.Fatal("应为 question kind")
		}
		if bot.last.Color != "orange" {
			t.Errorf("question 应 orange，得 %q", bot.last.Color)
		}
		if len(bot.last.Questions) != 1 {
			t.Fatalf("应 1 个问题，得 %d", len(bot.last.Questions))
		}
		q := bot.last.Questions[0]
		if q.Index != 1 || q.Question != "部署到哪?" {
			t.Errorf("问题内容/编号错: %+v", q)
		}
		if len(q.Options) != 2 || q.Options[0].Label != "axis" {
			t.Errorf("选项错: %+v", q.Options)
		}
		if bot.last.RequestID != "abcde" {
			t.Errorf("request_id 丢失")
		}
	})

	t.Run("question 无 questions → 400", func(t *testing.T) {
		s := newProgressServer(&captureBot{})
		w := postProgress(s, "mf", `{"kind":"question"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("应 400，得 %d", w.Code)
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
