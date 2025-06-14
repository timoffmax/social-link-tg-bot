package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendPhoto = "sendPhoto"

type SendPhoto struct {
	Payload         payload.TgPlSendPhoto
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
}

func NewSendPhoto(payload payload.TgPlSendPhoto) *SendPhoto {
	reqRespInfo := &SendPhoto{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendPhoto,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeFormData,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendPhoto) send() *response.TgMessage {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}