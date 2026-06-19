package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/bendusy/idc-alert-feishu/feishu"
	"github.com/bendusy/idc-alert-feishu/model"
	"github.com/bendusy/idc-alert-feishu/tmpl"
)

type Server struct {
	bots          map[string]feishu.IBot
	splitByStatus bool
	progressLimit *rateLimiter
}

func New(bots map[string]feishu.IBot, splitByStatus bool) *Server {
	s := &Server{
		bots:          bots,
		splitByStatus: splitByStatus,
		// 进度通道限流：每 project ≤1 条/秒、突发 5
		progressLimit: newRateLimiter(1, 5),
	}
	return s
}

func (s Server) hook(w http.ResponseWriter, r *http.Request) {
	// get path param
	vars := mux.Vars(r)
	group := vars["group"]
	bot, ok := s.bots[group]
	if !ok {
		logrus.Errorf("group not found: %s", group)
		http.Error(w, "group not found", http.StatusBadRequest)
		return
	}

	// get body param
	var alerts model.WebhookMessage
	err := json.NewDecoder(r.Body).Decode(&alerts)
	if err != nil {
		logrus.Errorf("cannot parse content, %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		spew.Dump(alerts)
	}

	// get query string
	meta := make(map[string]string)
	for key, values := range r.URL.Query() {
		meta[key] = strings.Join(values, ",")
	}
	// also include path param
	meta["group"] = group

	var alertsGroups []model.WebhookMessage
	if s.splitByStatus {
		alertsGroups = split(alerts)
	} else {
		alertsGroups = []model.WebhookMessage{alerts}
	}

	for _, alerts := range alertsGroups {
		alerts.Meta = meta
		err = bot.Send(&alerts)
		if err != nil {
			logrus.Errorf("cannot send alerts, %s", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	_, _ = fmt.Fprintf(w, "ok")
}

// progressOption / progressQuestion / progressRequest 是进度通道（旁路 Alertmanager）
// 的精简上报格式。字段命名向 CC 官方 channels 契约靠拢，便于未来并入 feishu_hub。
type progressOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type progressQuestion struct {
	Question string           `json:"question"`
	Options  []progressOption `json:"options"`
}

type progressRequest struct {
	// Kind: "" / "progress"（默认，普通进度/响铃）| "question"（CC 选项卡 permission_request）
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
	// kind=question 时使用
	Project   string             `json:"project"`
	Cwd       string             `json:"cwd"`
	RequestID string             `json:"request_id"`
	Questions []progressQuestion `json:"questions"`
}

// progressColor 从 summary 的状态 emoji 前缀推断卡片颜色。
func progressColor(summary string) string {
	switch {
	case strings.HasPrefix(summary, "🚦"):
		return "blue"
	case strings.HasPrefix(summary, "✅"):
		return "green"
	case strings.HasPrefix(summary, "❌"):
		return "red"
	case strings.HasPrefix(summary, "🔔"):
		return "yellow"
	default:
		return "grey"
	}
}

// progress 进度/事件上报端点：旁路 Alertmanager，接精简 payload，
// 用 progress.tmpl 渲染专属出站卡片（不再伪装成 firing alert）。
// 即时逐条发，无 group/dedup/inhibit/resolved。带 per-project 限流。
func (s Server) progress(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	group := vars["group"]
	bot, ok := s.bots[group]
	if !ok {
		logrus.Errorf("progress: group not found: %s", group)
		http.Error(w, "group not found", http.StatusBadRequest)
		return
	}

	if !s.progressLimit.allow(group) {
		logrus.Warnf("progress: rate limited for group %s", group)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}

	var req progressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logrus.Errorf("progress: cannot parse content, %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	msg := &model.ProgressMessage{
		Kind:    req.Kind,
		Group:   group,
		GroupCN: tmpl.ProjectCN(group),
		TimeCN:  time.Now().Format("2006-01-02 15:04:05"),
	}

	if req.Kind == "question" {
		if len(req.Questions) == 0 {
			http.Error(w, "questions required for kind=question", http.StatusBadRequest)
			return
		}
		msg.Color = "orange"
		msg.Project = req.Project
		msg.Cwd = req.Cwd
		msg.RequestID = req.RequestID
		for i, q := range req.Questions {
			mq := model.ProgressQuestion{Index: i + 1, Question: q.Question}
			for _, o := range q.Options {
				mq.Options = append(mq.Options, model.ProgressOption{Label: o.Label, Description: o.Description})
			}
			msg.Questions = append(msg.Questions, mq)
		}
	} else {
		if req.Summary == "" {
			http.Error(w, "summary required", http.StatusBadRequest)
			return
		}
		msg.Summary = req.Summary
		msg.Detail = req.Detail
		msg.Color = progressColor(req.Summary)
		// bell 卡片（summary 带 🔔）走"响铃"标题分支：用 Project 字段非空区分（复用模板分支）
		if strings.HasPrefix(req.Summary, "🔔") {
			msg.Project = group
		}
	}

	if err := bot.SendProgress(msg); err != nil {
		logrus.Errorf("progress: cannot send, %s", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = fmt.Fprintf(w, "ok")
}

func split(alerts model.WebhookMessage) []model.WebhookMessage {
	var groups []model.WebhookMessage
	if len(alerts.Alerts.Firing()) != 0 {
		alertsClone := alerts
		alertsClone.Alerts = alerts.Alerts.Firing()
		groups = append(groups, alertsClone)
	}
	if len(alerts.Alerts.Resolved()) != 0 {
		alertsClone := alerts
		alertsClone.Alerts = alerts.Alerts.Resolved()
		groups = append(groups, alertsClone)
	}
	return groups
}

func (s Server) health(w http.ResponseWriter, r *http.Request) {
	_, _ = fmt.Fprintf(w, "ok")
}

func (s Server) Start(address string) error {
	r := mux.NewRouter()
	r.HandleFunc("/hook/{group}", s.hook).Methods("POST")
	r.HandleFunc("/progress/{group}", s.progress).Methods("POST")

	// management etc...
	sr := r.PathPrefix("/-").Subrouter()
	sr.HandleFunc("/healthz", s.health).Methods("GET")

	// prometheus
	r.Handle("/metrics", promhttp.Handler()).Methods("GET")

	srv := &http.Server{
		Handler:      r,
		Addr:         address,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}

	return srv.ListenAndServe()
}
