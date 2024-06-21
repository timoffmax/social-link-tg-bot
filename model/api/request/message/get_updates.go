package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteGetUpdates = "getUpdates"

type GetUpdates struct {
	Payload         payload.TgPlGetUpdates
	ResponsePayload *response.TgUpdates
	Request         request.AuthorizedRequest
}

func NewGetUpdates(payload payload.TgPlGetUpdates) *GetUpdates {
	reqRespInfo := &GetUpdates{
		Request: request.AuthorizedRequest{
			ApiMethod:     RouteGetUpdates,
			RequestMethod: request.RequestMethodGET,
			Payload:       payload,
		},
		ResponsePayload: &response.TgUpdates{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *GetUpdates) send() *response.TgUpdates {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
