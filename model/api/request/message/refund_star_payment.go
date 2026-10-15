package message

import (
	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
)

const RouteRefundStarPayment = "refundStarPayment"

type RefundStarPayment struct {
	Payload         payload.TgPlRefundStarPayment
	ResponsePayload *bool
	Request         request.AuthorizedRequest
	Response        request.ResponseInfo
	Err             error
}

func NewRefundStarPayment(payload payload.TgPlRefundStarPayment) *RefundStarPayment {
	reqRespInfo := &RefundStarPayment{
		Request: request.AuthorizedRequest{
			ApiMethod:          RouteRefundStarPayment,
			RequestMethod:      request.RequestMethodPOST,
			RequestContentType: request.ContentTypeJson,
			Payload:            payload,
		},
		ResponsePayload: new(bool),
	}

	reqRespInfo.send()

	return reqRespInfo
}

func (service *RefundStarPayment) send() *bool {
	service.Response, service.Err = service.Request.Send(service.ResponsePayload)

	return service.ResponsePayload
}
