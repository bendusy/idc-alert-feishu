package feishu

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// maxTokenRefreshRetries 限制初始 token 获取的重试次数，避免飞书长期不可达时无限重试。
const maxTokenRefreshRetries = 5

type Sdk struct {
	appID     string
	appSecret string
	client    http.Client

	mu    sync.RWMutex
	token string
}

func NewSDK(appID string, appSecret string) *Sdk {
	s := &Sdk{
		appID:     appID,
		appSecret: appSecret,
		client:    http.Client{},
	}

	if appID != "" && appSecret != "" {
		s.refreshToken()
	}

	return s
}

func (s *Sdk) getToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

func (s *Sdk) setToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

type batchGetIDResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		EmailUsers map[string][]struct {
			OpenId string `json:"open_id"`
			UserId string `json:"user_id"`
		} `json:"email_users"`
		EmailsNotExist []string `json:"emails_not_exist"`
		MobileUsers    map[string][]struct {
			OpenId string `json:"open_id"`
			UserId string `json:"user_id"`
		} `json:"mobile_users"`
		MobilesNotExist []string `json:"mobiles_not_exist"`
	} `json:"data"`
}

// BatchGetID https://open.feishu.cn/document/ukTMukTMukTM/uUzMyUjL1MjM14SNzITN
func (s *Sdk) BatchGetID(emails []string) (map[string]string, error) {
	if len(emails) == 0 {
		return nil, errors.New("at least 1 email")
	}
	if len(emails) > 50 {
		return nil, errors.New("at most 50 emails")
	}

	params := url.Values{}
	for _, email := range emails {
		params.Add("emails", email)
	}
	api := "https://open.feishu.cn/open-apis/user/v1/batch_get_id?" + params.Encode()
	var response batchGetIDResponse
	err := s.get(api, s.getToken(), &response)
	if err != nil {
		return nil, err
	}

	if response.Code != 0 {
		return nil, fmt.Errorf("code: %d, err: %s", response.Code, response.Msg)
	}

	res := make(map[string]string)
	for k, vv := range response.Data.EmailUsers {
		for _, v := range vv {
			res[k] = v.OpenId
		}
	}
	return res, nil
}

type tokenRequest struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

type tokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

// TenantAccessToken https://open.feishu.cn/document/ukTMukTMukTM/uIjNz4iM2MjLyYzM
func (s *Sdk) TenantAccessToken() (*tokenResponse, error) {
	request := tokenRequest{
		AppID:     s.appID,
		AppSecret: s.appSecret,
	}
	var response tokenResponse
	err := s.post("https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal/", s.getToken(), request, &response)
	if err != nil {
		return nil, err
	}

	if response.Code != 0 {
		return nil, fmt.Errorf("code: %d, err: %s", response.Code, response.Msg)
	}

	return &response, nil
}

// wired response:
// response of success
//
//	{
//	   "Extra": null,
//	   "StatusCode": 0,
//	   "StatusMessage": "success"
//	}
//
// response of failure
//
//	{
//	   "code": 99991300,
//	   "msg": "invalid request body: not json, invalid character '\\n' in string literal"
//	}
type webhookV2Response struct {
	StatusCode    int    `json:"StatusCode"`
	StatusMessage string `json:"StatusMessage"`
	Code          int    `json:"code"`
	Msg           string `json:"msg"`
}

// genSign 飞书自定义机器人签名校验
// 算法见 https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot
// stringToSign = timestamp + "\n" + secret，以其为 HMAC-SHA256 的 key，对空字符串签名
func genSign(secret string, timestamp int64) string {
	stringToSign := strconv.FormatInt(timestamp, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// injectSign 把 timestamp/sign 注入飞书消息 body 顶层
func injectSign(body io.Reader, secret string) (io.Reader, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	timestamp := time.Now().Unix()
	payload["timestamp"] = strconv.FormatInt(timestamp, 10)
	payload["sign"] = genSign(secret, timestamp)

	signed, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(signed), nil
}

func (s *Sdk) WebhookV2(webhook string, body io.Reader, sign string) error {
	if sign != "" {
		signed, err := injectSign(body, sign)
		if err != nil {
			return err
		}
		body = signed
	}
	logrus.Debug(webhook, body)
	req, err := http.NewRequest("POST", webhook, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	do, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer do.Body.Close()

	var resp webhookV2Response
	err = json.NewDecoder(do.Body).Decode(&resp)
	if err != nil {
		return err
	}
	logrus.Debug(resp)

	if resp.Code != 0 {
		return fmt.Errorf("code: %d, err: %s", resp.Code, resp.Msg)
	}

	return nil
}

func (s *Sdk) get(url string, auth string, responseBody interface{}) error {
	return s.call("GET", url, auth, nil, responseBody)
}

func (s *Sdk) post(url string, auth string, requestBody, responseBody interface{}) error {
	return s.call("POST", url, auth, requestBody, responseBody)
}

func (s *Sdk) call(method string, url string, auth string, requestBody, responseBody interface{}) error {
	logrus.Debugf("%s %s with %v", method, url, requestBody)
	var body io.Reader
	if requestBody != nil {
		bs, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(bs)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}

	do, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer do.Body.Close()

	err = json.NewDecoder(do.Body).Decode(&responseBody)
	if err != nil {
		return err
	}

	logrus.Debug(responseBody)

	return nil
}

func (s *Sdk) refreshToken() {
	// 用循环重试代替递归，避免飞书长期不可达时 goroutine 栈无限增长
	var response *tokenResponse
	for attempt := 1; ; attempt++ {
		var err error
		response, err = s.TenantAccessToken()
		if err == nil {
			break
		}
		logrus.Errorf("refresh token failed (attempt %d/%d), %v", attempt, maxTokenRefreshRetries, err)
		if attempt >= maxTokenRefreshRetries {
			logrus.Errorf("refresh token giving up after %d attempts", maxTokenRefreshRetries)
			return
		}
		time.Sleep(time.Second * 1)
	}

	s.setToken(response.TenantAccessToken)

	// https://open.feishu.cn/document/ukTMukTMukTM/uIjNz4iM2MjLyYzM
	// Token 有效期为 2 小时，在此期间调用该接口 token 不会改变。当 token 有效期小于 30 分的时候，再次请求获取 token 的时候，会生成一个新的 token，与此同时老的 token 依然有效。
	// 在过期前 1 分钟刷新
	time.AfterFunc(time.Second*time.Duration(response.Expire-60), func() {
		s.refreshToken()
	})
}
