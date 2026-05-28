package tmpl

import (
	"embed"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/*
var fs embed.FS
var embedTemplates map[string]*template.Template
var customTemplates map[string]*template.Template
var funcMap template.FuncMap

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
			_, err := url.ParseRequestURI(v)
			if err != nil {
				return fmt.Sprintf("%s:%s", k, v)
			}
			return fmt.Sprintf("[%s](%s)", k, v)
		},
		"contains": strings.Contains,
		// IDC: severity → 飞书卡片 header 颜色
		"severityColor": func(sev string) string {
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
