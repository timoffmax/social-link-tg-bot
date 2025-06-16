package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendMediaGroup = "sendMediaGroup"

type SendMediaGroup struct {
	Payload         payload.TgPlSendMediaGroup
	ResponsePayload *[]response.TgMessage
	Request         request.AuthorizedRequest
}

func NewSendMediaGroup(payload payload.TgPlSendMediaGroup) *SendMediaGroup {
	reqRespInfo := &SendMediaGroup{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendMediaGroup,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeFormData,
			Payload:            payload,
		},
		ResponsePayload: &[]response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendMediaGroup) send() *[]response.TgMessage {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}