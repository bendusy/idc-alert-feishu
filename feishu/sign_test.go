package feishu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestGenSign(t *testing.T) {
	secret := "wycNWxCrgaVJ7K0hWnaVFg"
	var timestamp int64 = 1700000000

	got := genSign(secret, timestamp)

	// 独立复算飞书算法：key = timestamp+"\n"+secret，对空内容做 HMAC-SHA256 再 base64
	stringToSign := strconv.FormatInt(timestamp, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if got != want {
		t.Fatalf("genSign mismatch: got %q want %q", got, want)
	}
	if got == "" {
		t.Fatal("sign should not be empty")
	}
}

func TestInjectSign(t *testing.T) {
	body := strings.NewReader(`{"msg_type":"text","content":{"text":"hi"}}`)
	out, err := injectSign(body, "somesecret")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(out)

	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("injected body not valid json: %v", err)
	}
	if _, ok := payload["timestamp"]; !ok {
		t.Error("missing timestamp field")
	}
	if _, ok := payload["sign"]; !ok {
		t.Error("missing sign field")
	}
	// 原字段保留
	if payload["msg_type"] != "text" {
		t.Error("original msg_type lost after injecting sign")
	}
}
