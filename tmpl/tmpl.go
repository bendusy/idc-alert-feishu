package tmpl

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	amtmpl "github.com/prometheus/alertmanager/template"
	"github.com/sirupsen/logrus"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"
)

// JSONString 把任意字符串转义成可安全嵌入 JSON 字符串值位置的内容
// （不含首尾引号）。用于防止 alert 字段里的 " \ 换行等字符破坏发往飞书的 JSON。
// 关闭 HTML 转义，避免 URL 里的 & < > 被转成 \u00xx。
func JSONString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return s
	}
	b := buf.Bytes()
	b = b[:len(b)-1] // 去掉 Encode 追加的末尾 '\n'
	return string(b[1 : len(b)-1])
}

//go:embed templates/*
var fs embed.FS
var embedTemplates map[string]*template.Template
var customTemplates map[string]*template.Template
var funcMap template.FuncMap

// severityRank 数值化 severity 便于比较 max。未知 severity 视为最低。
func severityRank(sev string) int {
	switch strings.ToLower(sev) {
	case "critical":
		return 4
	case "error":
		return 3
	case "warn", "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

// projectCN project label → 中文显示名，用于卡片标题直观展示。
// 未知 project 原样返回（含空串时返回空，模板侧自行处理）。
func projectCN(project string) string {
	switch project {
	case "idc-infra":
		return "基础设施"
	case "memory-flow":
		return "记忆库"
	case "banwen-flow":
		return "办文系统"
	case "arcflow":
		return "ArcFlow"
	default:
		return project
	}
}

// firstSummary 取一组 firing alert 的首条 summary（中文摘要），用于标题展示。
func firstSummary(alerts []amtmpl.Alert) string {
	for _, a := range alerts {
		if a.Status == "resolved" {
			continue
		}
		if s := a.Annotations["summary"]; s != "" {
			return s
		}
	}
	return ""
}

// severityToColor severity → 飞书卡片 header 颜色
func severityToColor(sev string) string {
	switch strings.ToLower(sev) {
	case "critical":
		return "red"
	case "error":
		return "orange"
	case "warn", "warning":
		return "yellow"
	case "info":
		return "grey"
	default:
		return "blue"
	}
}

// maxFiringSeverity 取一组 alert 里 firing 的最高 severity。
// 注意：调用方应传入已 Firing() 过滤的列表；这里再按 status 兜底过滤一次，
// 防止调用方误传整组（含 resolved）导致 resolved 的高 severity 被算进来。
func maxFiringSeverity(alerts []amtmpl.Alert) string {
	best, bestRank := "", -1
	for _, a := range alerts {
		if a.Status == "resolved" {
			continue
		}
		sev := a.Labels["severity"]
		if r := severityRank(sev); r > bestRank {
			best, bestRank = sev, r
		}
	}
	return best
}

func init() {
	// func
	funcMap = template.FuncMap{
		"date": func(dt time.Time, zone string) string {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				logrus.Error(err)
				return err.Error()
			}
			dt = dt.In(loc)
			return dt.Format("2006-01-02 15:04:05")
		},
		"isNonZeroDate": func(dt time.Time) bool {
			return !(dt == time.Time{})
		},
		"in": func(m map[string]string, key string) bool {
			_, ok := m[key]
			return ok
		},
		"toUpper": strings.ToUpper,
		"toLink": func(s string) string {
			return fmt.Sprintf("[%s](%s)", s, s)
		},
		"displayKV": func(k, v string) string {
			// 转义后再拼接，避免 meta value（来自 query string，外部可控）里的
			// " \ 等字符破坏外层 JSON 结构
			k = JSONString(k)
			v = JSONString(v)
			_, err := url.ParseRequestURI(v)
			if err != nil {
				return fmt.Sprintf("%s:%s", k, v)
			}
			return fmt.Sprintf("[%s](%s)", k, v)
		},
		"contains": strings.Contains,
		// json: 把外部字段转义后安全嵌入 JSON 字符串值位置（防注入/破坏结构）
		"json": JSONString,
		// IDC: severity → 飞书卡片 header 颜色（单值版，保留兼容现有模板）
		"severityColor": severityToColor,
		// IDC: project → 中文显示名（卡片标题直观展示）
		"projectCN": projectCN,
		// IDC: 取首条 firing alert 的中文 summary
		"firstSummary": firstSummary,
		// IDC: 取一组 firing alert 的 max severity 对应颜色（跨 severity group 下 CommonLabels 丢 severity）
		// 只算 firing，resolved 不参与，避免已恢复的高 severity 影响卡片颜色
		"maxSeverityColor": func(alerts []amtmpl.Alert) string {
			return severityToColor(maxFiringSeverity(alerts))
		},
		// IDC: 按一组 firing alert 的 max severity 条件渲染飞书 @ 标签
		// max severity ≥ error → 渲染 <at>；否则空串。只算 firing（复审 #5：防 resolved critical 误 @）
		"mentionIf": func(alerts []amtmpl.Alert, openIDs []string) string {
			if severityRank(maxFiringSeverity(alerts)) < severityRank("error") {
				return ""
			}
			var b strings.Builder
			for _, id := range openIDs {
				if id == "" {
					continue
				}
				if id == "all" {
					b.WriteString(`<at id=all></at> `)
					continue
				}
				fmt.Fprintf(&b, `<at id=%s></at> `, id)
			}
			return b.String()
		},
		// IDC: 把 asset_id 渲染成飞书 markdown 链接，跳回 IDC handbook
		// 未配置 HANDBOOK_BASE_URL 时退化成 `asset_id` 纯文本
		"assetLink": func(assetID string) string {
			if assetID == "" {
				return ""
			}
			base := strings.TrimRight(os.Getenv("HANDBOOK_BASE_URL"), "/")
			if base == "" {
				return fmt.Sprintf("`%s`", assetID)
			}
			return fmt.Sprintf("[%s](%s/%s)", assetID, base, assetID)
		},
	}

	// embed
	dir, err := fs.ReadDir("templates")
	if err != nil {
		panic(err)
	}

	embedTemplates = make(map[string]*template.Template)
	for _, entry := range dir {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if !strings.HasSuffix(filename, ".tmpl") {
			continue
		}

		t, err := template.New(filename).Funcs(funcMap).ParseFS(fs, "templates/"+filename)
		if err != nil {
			panic(err)
		}

		embedTemplates[t.Name()] = t
	}

	// custom
	customTemplates = make(map[string]*template.Template)
}

func GetEmbedTemplate(filename string) (*template.Template, error) {
	if t, ok := embedTemplates[filename]; ok {
		return t, nil
	}

	return nil, errors.New("template not found")
}

func GetCustomTemplate(filename string) (*template.Template, error) {
	if t, ok := customTemplates[filename]; ok {
		return t, nil
	}

	t, err := template.New(filename).Funcs(funcMap).ParseFiles(filename)
	if err != nil {
		return nil, err
	}
	customTemplates[filename] = t

	return t, nil
}
