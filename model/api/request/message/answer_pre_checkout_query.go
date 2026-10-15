package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

const RouteAnswerPreCheckoutQuery = "answerPreCheckoutQuery"

type AnswerPreCheckoutQuery struct {
	Payload         payload.TgPlAnswerPreCheckoutQuery
	ResponsePayload *bool
	Request         request.AuthorizedRequest
	Response        request.ResponseInfo
	Err             error
}

func NewAnswerPreCheckoutQuery(payload payload.TgPlAnswerPreCheckoutQuery) *AnswerPreCheckoutQuery {
	reqRespInfo := &AnswerPreCheckoutQuery{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteAnswerPreCheckoutQuery,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: new(bool),
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *AnswerPreCheckoutQuery) send() *bool {
	service.Response, service.Err = service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
