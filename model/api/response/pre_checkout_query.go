package response

/*
@link https://core.telegram.org/bots/api#precheckoutquery
*/
type TgPreCheckoutQuery struct {
	Id               string `json:"id"`
	From             TgUser `json:"from"`
	Currency         string `json:"currency"`
	TotalAmount      int    `json:"total_amount"`
	InvoicePayload   string `json:"invoice_payload"`
	ShippingOptionId string `json:"shipping_option_id,omitempty"`
}
