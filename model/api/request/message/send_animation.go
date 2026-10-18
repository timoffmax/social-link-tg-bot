package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
)

// RouteSendAnimation is the Bot API method that sends a GIF or a silent MP4
// as an animation (it plays in a loop, unlike a photo's still frame).
const RouteSendAnimation = "sendAnimation"

// SendAnimation is one sendAnimation call and its answer.
type SendAnimation struct {
	Payload         payload.TgPlSendAnimation
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
	Response        request.ResponseInfo
	Err             error
}

// NewSendAnimation uploads the animation file and sends it.
func NewSendAnimation(animationPayload payload.TgPlSendAnimation) *SendAnimation {
	result := &SendAnimation{
		Payload: animationPayload,
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendAnimation,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeFormData,
			Payload:            animationPayload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	result.Response, result.Err = result.Request.Send(result.ResponsePayload)

	return result
}
