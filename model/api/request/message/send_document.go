package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendDocument = "sendDocument"

type SendDocument struct {
	Payload         payload.TgPlSendDocument
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
}

func NewSendDocument(payload payload.TgPlSendDocument) *SendDocument {
	reqRespInfo := &SendDocument{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendDocument,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeFormData,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendDocument) send() *response.TgMessage {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
