package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendInvoice = "sendInvoice"

type SendInvoice struct {
	Payload         payload.TgPlSendInvoice
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
	Response        request.ResponseInfo
	Err             error
}

func NewSendInvoice(payload payload.TgPlSendInvoice) *SendInvoice {
	reqRespInfo := &SendInvoice{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendInvoice,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendInvoice) send() *response.TgMessage {
	service.Response, service.Err = service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
