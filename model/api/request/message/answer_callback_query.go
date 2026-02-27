package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

const RouteAnswerCallbackQuery = "answerCallbackQuery"

type AnswerCallbackQuery struct {
	Payload         payload.TgPlAnswerCallbackQuery
	ResponsePayload *bool
	Request         request.AuthorizedRequest
}

func NewAnswerCallbackQuery(payload payload.TgPlAnswerCallbackQuery) *AnswerCallbackQuery {
	reqRespInfo := &AnswerCallbackQuery{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteAnswerCallbackQuery,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: new(bool),
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *AnswerCallbackQuery) send() *bool {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
