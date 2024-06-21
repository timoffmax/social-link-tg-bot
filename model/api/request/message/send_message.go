package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendMessage = "sendMessage"

type SendMessage struct {
	Payload         payload.TgPlSendMessage
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
}

func NewSendMessage(payload payload.TgPlSendMessage) *SendMessage {
	reqRespInfo := &SendMessage{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendMessage,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendMessage) send() *response.TgMessage {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
