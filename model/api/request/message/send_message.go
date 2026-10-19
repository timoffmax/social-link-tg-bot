package message

import (
	"time"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendMessage = "sendMessage"

type SendMessage struct {
	Payload         payload.TgPlSendMessage
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
	Response        request.ResponseInfo
	Err             error
}

func NewSendMessage(payload payload.TgPlSendMessage) *SendMessage {
	result := NewSendMessageWithin(payload, 0)

	return result
}

// NewSendMessageWithin sends a message, giving up after timeout (0: the
// client's ClientTimeout).
func NewSendMessageWithin(payload payload.TgPlSendMessage, timeout time.Duration) *SendMessage {
	reqRespInfo := &SendMessage{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendMessage,
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

func (service *SendMessage) send() *response.TgMessage {
	service.Response, service.Err = service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
