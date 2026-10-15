package payload

/*
@link https://core.telegram.org/bots/api#refundstarpayment
*/
type TgPlRefundStarPayment struct {
	UserId                  int64  `json:"user_id"`
	TelegramPaymentChargeId string `json:"telegram_payment_charge_id"`
}
