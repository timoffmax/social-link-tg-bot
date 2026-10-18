package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendDocument = "sendDocument"

// SendDocument is one sendDocument call and its answer; Err is set when the
// request failed or Telegram refused it.
type SendDocument struct {
	Payload         payload.TgPlSendDocument
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
	Response        request.ResponseInfo
	Err             error
}

func NewSendDocument(payload payload.TgPlSendDocument) *SendDocument {
	reqRespInfo := &SendDocument{
		Payload: payload,
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
	service.Response, service.Err = service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
