package feishu

import "github.com/bendusy/idc-alert-feishu/model"

type IBot interface {
	Send(*model.WebhookMessage) error
}
