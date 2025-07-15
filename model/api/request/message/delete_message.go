package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

const RouteDeleteMessage = "deleteMessage"

type DeleteMessage struct {
	Payload         payload.TgPlDeleteMessage
	ResponsePayload *bool
	Request         request.AuthorizedRequest
}

func NewDeleteMessage(payload payload.TgPlDeleteMessage) *DeleteMessage {
	reqRespInfo := &DeleteMessage{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteDeleteMessage,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: new(bool),
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *DeleteMessage) send() *bool {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}