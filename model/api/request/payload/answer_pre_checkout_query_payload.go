package payload

/*
Ok has no omitempty: a refusal must send "ok": false.

@link https://core.telegram.org/bots/api#answerprecheckoutquery
*/
type TgPlAnswerPreCheckoutQuery struct {
	PreCheckoutQueryId string `json:"pre_checkout_query_id"`
	Ok                 bool   `json:"ok"`
	ErrorMessage       string `json:"error_message,omitempty"`
}
