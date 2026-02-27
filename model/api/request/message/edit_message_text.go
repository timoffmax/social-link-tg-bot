package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteEditMessageText = "editMessageText"

type EditMessageText struct {
	Payload         payload.TgPlEditMessageText
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
}

func NewEditMessageText(payload payload.TgPlEditMessageText) *EditMessageText {
	reqRespInfo := &EditMessageText{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteEditMessageText,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *EditMessageText) send() *response.TgMessage {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
