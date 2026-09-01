package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

const RouteSendVideo = "sendVideo"

type SendVideo struct {
	Payload         payload.TgPlSendVideo
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
}

func NewSendVideo(payload payload.TgPlSendVideo) *SendVideo {
	reqRespInfo := &SendVideo{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendVideo,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeFormData,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendVideo) send() *response.TgMessage {
	service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
