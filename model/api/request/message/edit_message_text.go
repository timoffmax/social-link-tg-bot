package message

import (
	"time"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteEditMessageText = "editMessageText"

type EditMessageText struct {
	Payload         payload.TgPlEditMessageText
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
	Err             error
}

func NewEditMessageText(payload payload.TgPlEditMessageText) *EditMessageText {
	result := NewEditMessageTextWithin(payload, 0)

	return result
}

// NewEditMessageTextWithin edits a message, giving up after timeout (0: the
// client's ClientTimeout).
func NewEditMessageTextWithin(payload payload.TgPlEditMessageText, timeout time.Duration) *EditMessageText {
	reqRespInfo := &EditMessageText{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteEditMessageText,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
			Timeout:            timeout,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *EditMessageText) send() *response.TgMessage {
	_, service.Err = service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
