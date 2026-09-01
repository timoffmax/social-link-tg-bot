package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"log"
)

const RouteSendPhoto = "sendPhoto"

type SendPhoto struct {
	Payload         payload.TgPlSendPhoto
	ResponsePayload *response.TgMessage
	Request         request.AuthorizedRequest
}

func NewSendPhoto(payload payload.TgPlSendPhoto) *SendPhoto {
	reqRespInfo := &SendPhoto{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteSendPhoto,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeFormData,
			Payload:            payload,
		},
		ResponsePayload: &response.TgMessage{},
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *SendPhoto) send() *response.TgMessage {
	respInfo, err := service.Request.Send(service.ResponsePayload)

	if err != nil {
		log.Printf("❌ SendPhoto API error: %v", err)
		log.Printf("❌ Response info: %+v", respInfo)
	} else if respInfo.StatusCode != 200 {
		log.Printf("❌ SendPhoto HTTP error: %d - %s", respInfo.StatusCode, respInfo.Message)
	} else {
		log.Printf("✅ SendPhoto API success: %d", respInfo.StatusCode)
	}

	return service.ResponsePayload
}
