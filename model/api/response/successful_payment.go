package response

/*
@link https://core.telegram.org/bots/api#successfulpayment
*/
type TgSuccessfulPayment struct {
	Currency                   string `json:"currency"`
	TotalAmount                int    `json:"total_amount"`
	InvoicePayload             string `json:"invoice_payload"`
	SubscriptionExpirationDate int    `json:"subscription_expiration_date,omitempty"`
	IsRecurring                bool   `json:"is_recurring,omitempty"`
	IsFirstRecurring           bool   `json:"is_first_recurring,omitempty"`
	ShippingOptionId           string `json:"shipping_option_id,omitempty"`
	TelegramPaymentChargeId    string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeId    string `json:"provider_payment_charge_id"`
}
