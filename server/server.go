package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gorilla/mux"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/bendusy/idc-alert-feishu/feishu"
	"github.com/bendusy/idc-alert-feishu/model"
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

// progressRequest 进度通道的精简上报格式（旁路 Alertmanager）
type progressRequest struct {
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// progress 进度/事件上报端点：旁路 Alertmanager，接精简 payload，
// 包装成 info severity 的 firing alert 复用 idc.tmpl 渲染（灰卡、不 @）。
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
	if req.Summary == "" {
		http.Error(w, "summary required", http.StatusBadRequest)
		return
	}

	// 包装成 info firing alert，复用告警卡片模板
	msg := model.WebhookMessage{
		Data: template.Data{
			Status: "firing",
			Alerts: template.Alerts{
				{
					Status: "firing",
					Labels: template.KV{"severity": "info", "project": group, "alertname": req.Summary},
					Annotations: template.KV{
						"summary":     req.Summary,
						"description": req.Detail,
					},
				},
			},
			GroupLabels:  template.KV{"alertname": req.Summary, "severity": "info"},
			CommonLabels: template.KV{"severity": "info", "project": group},
		},
	}
	msg.Meta = map[string]string{"group": group}

	if err := bot.Send(&msg); err != nil {
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
