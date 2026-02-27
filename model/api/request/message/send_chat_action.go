package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

const RouteSendChatAction = "sendChatAction"

type SendChatAction struct {
	Payload         payload.TgPlSendChatAction
	ResponsePayload *bool
	Request         request.AuthorizedRequest
}

func NewSendChatAction(pl payload.TgPlSendChatAction) *SendChatAction {
	reqRespInfo := &SendChatAction{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendChatAction,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            pl,
		},
		ResponsePayload: new(bool),
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendChatAction) send() {
	service.Request.Send(service.ResponsePayload)
}
