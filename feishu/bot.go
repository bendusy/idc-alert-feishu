package feishu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"text/template"
	"time"

	"github.com/icza/gox/stringsx"
	amtpl "github.com/prometheus/alertmanager/template"
	"github.com/sirupsen/logrus"

	"github.com/bendusy/idc-alert-feishu/config"
	"github.com/bendusy/idc-alert-feishu/feishu/rotate"
	"github.com/bendusy/idc-alert-feishu/model"
	"github.com/bendusy/idc-alert-feishu/tmpl"
)

type Bot struct {
	webhook     string
	sign        string
	openIDs     []string
	rotator     *rotate.MentionRotator
	sdk         *Sdk
	tpl         *template.Template
	alertTpl    *template.Template
	titlePrefix string
	metadata    map[string]string
}

func New(bot *config.Bot, helper *EmailHelper) (*Bot, error) {
	// @xxx
	openIDs, err := getOpenIDs(bot.Mention, helper)
	if err != nil {
		return nil, err
	}

	var rotator *rotate.MentionRotator
	if bot.Mention != nil && bot.Mention.Rotation != "" && len(openIDs) > 1 {
		rotator, err = rotate.New(bot.Mention.Rotation, openIDs)
		if err != nil {
			return nil, err
		}
	}

	// template
	tpl, alertTpl, err := getTemplates(bot.Template)
	if err != nil {
		return nil, err
	}

	return &Bot{
		webhook:     bot.Webhook,
		sign:        bot.Sign,
		rotator:     rotator,
		openIDs:     openIDs,
		sdk:         NewSDK("", ""),
		tpl:         tpl,
		alertTpl:    alertTpl,
		titlePrefix: bot.TitlePrefix,
		metadata:    bot.MetaData,
	}, nil
}

func getOpenIDs(mention *config.Mention, helper *EmailHelper) ([]string, error) {
	if mention == nil {
		return nil, nil
	}
	if mention.All {
		return []string{"all"}, nil
	}

	openIDs := mention.OpenIDs
	emails := mention.Emails
	if len(emails) != 0 && helper == nil {
		return nil, errors.New("@somebody by email need email flag enabled")
	}
	if len(emails) != 0 {
		remaining, err := helper.Lookup(emails)
		if err != nil {
			return nil, err
		}
		openIDs = append(openIDs, remaining...)
	}
	return openIDs, nil
}

func getTemplates(tmplConf *config.Template) (*template.Template, *template.Template, error) {
	if tmplConf != nil && tmplConf.CustomPath != "" {
		t, err := tmpl.GetCustomTemplate(tmplConf.CustomPath)
		if err != nil {
			return nil, nil, err
		}
		return t, nil, nil
	}

	// IDC 专用 fork：默认用 idc 模板（含 maxSeverityColor / mentionIf / 中文项目名）。
	// 注：上游用 query string ?tmpl= 选模板的设计从未在 Send 链路生效（getTemplates
	// 只读 config.custom_path），故这里把默认 embed 模板直接定为 idc.tmpl。
	dt, err := tmpl.GetEmbedTemplate("idc.tmpl")
	if err != nil {
		return nil, nil, err
	}

	dat, err := tmpl.GetEmbedTemplate("idc_alert.tmpl")
	if err != nil {
		return nil, nil, err
	}

	return dt, dat, nil
}

func (b Bot) Send(alerts *model.WebhookMessage) error {
	// attach @xxx
	if b.rotator != nil {
		alerts.OpenIDs = b.rotator.Rotate(time.Now())
	} else {
		alerts.OpenIDs = b.openIDs
	}
	// title prefix
	alerts.TitlePrefix = b.titlePrefix

	// merge metadata
	alerts.Meta = mergeMap(alerts.Meta, b.metadata)

	// prepare data
	err := b.preprocessAlerts(alerts)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	err = b.tpl.Execute(&buf, alerts)
	if err != nil {
		return err
	}
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		if d, err := beautifyJSON(buf.String()); err != nil {
			logrus.Error(err)
			fmt.Println(buf.String())
		} else {
			fmt.Println(d)
		}
	}

	return b.sdk.WebhookV2(b.webhook, &buf, b.sign)
}

// right is immutable
func mergeMap(left, right map[string]string) map[string]string {
	if len(right) == 0 {
		return left
	}
	if left == nil {
		left = make(map[string]string)
	}
	for k, v := range right {
		if _, ok := left[k]; !ok {
			left[k] = v
		}
	}
	return left
}

// renderAlert 用单条告警模板渲染一条 alert，渲染前清洗不可打印字符，
// 渲染后整段做 JSON 字符串转义，返回可嵌入外层 JSON 模板 "content": "{{.}}" 的内容。
// alert 字段来自 Alertmanager（外部可控），不转义会因 " \ 换行等字符破坏 JSON 结构
// （飞书返回 99991300）。
func (b Bot) renderAlert(alert amtpl.Alert) (string, error) {
	// feishu fix: 清除不可打印字符（如 ESC 控制符），避免泄漏给 lark_md 渲染器
	for k, v := range alert.Annotations {
		alert.Annotations[k] = stringsx.Clean(v)
	}
	var buf bytes.Buffer
	if err := b.alertTpl.Execute(&buf, alert); err != nil {
		return "", err
	}
	return tmpl.JSONString(buf.String()), nil
}

func (b Bot) preprocessAlerts(alerts *model.WebhookMessage) error {
	if b.alertTpl == nil {
		return nil
	}

	for _, alert := range alerts.Alerts.Firing() {
		res, err := b.renderAlert(alert)
		if err != nil {
			return err
		}
		alerts.FiringAlerts = append(alerts.FiringAlerts, res)
	}
	for _, alert := range alerts.Alerts.Resolved() {
		res, err := b.renderAlert(alert)
		if err != nil {
			return err
		}
		alerts.ResolvedAlerts = append(alerts.ResolvedAlerts, res)
	}

	return nil
}

func beautifyJSON(raw string) (string, error) {
	data := make(map[string]interface{})
	err := json.Unmarshal([]byte(raw), &data)
	if err != nil {
		return "", err
	}
	d, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return "", err
	}
	return string(d), nil
}
