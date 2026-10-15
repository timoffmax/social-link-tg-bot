package app

import (
	"fmt"
	"log"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// donateCommand is the support command; /support is an alias of it.
const donateCommand = "/donate"

// donateLayout is the /donate reply: the intro, then the support options.
const donateLayout = "💚 <b>Support the bot</b>\n\n%s\n\n%s"

// donateSectionsLayout joins two sections of the support options.
const donateSectionsLayout = "%s\n\n━━━━━━━━━━━━━━━━━━━━\n\n%s"

// donateIntroStarsText opens /donate while Stars support is on.
const donateIntroStarsText = "Support is optional: the bot stays free, with no ads and no forced subscriptions. If it's useful to you, you can support the developer with Telegram Stars, a card or crypto."

// donateIntroCryptoText opens /donate while Stars support is off.
const donateIntroCryptoText = "Support is optional: the bot stays free, with no ads and no forced subscriptions. If it's useful to you, you can support the developer with a card or crypto."

// donateStarsText describes the Stars tiers; only Stars support adds bonus
// downloads, crypto donations unlock nothing.
const donateStarsText = `⭐ <b>Telegram Stars:</b>
Pick an amount with the buttons below. As a thank-you you get bonus downloads of restricted Instagram posts: age- or region-limited posts the bot can only fetch through a paid service.

<b>How bonus downloads work:</b>
• They're yours: they work in any chat, but only for links you send. They aren't shared with the group.
• Free downloads come first. A bonus download is used only once the free monthly limit (yours, the chat's or the bot's) runs out.
• If the bot can't fetch the post, no bonus download is used. They don't expire.
• Card and crypto donations don't add bonus downloads.

Telegram sells Stars in packs; anything left over stays in your Telegram balance.

By paying you agree to the /terms. Help with payments: /paysupport.`

// donateBalanceTemplate adds the user's bonus downloads to the Stars section.
const donateBalanceTemplate = "%s\n\n🎟️ Your bonus downloads: %d"

// donateCardText describes the monobank jar, which takes any card.
const donateCardText = `💳 <b>Donate by card:</b>
Use the button below to donate to the monobank jar with any card, Apple Pay or Google Pay, or send money to the jar's card number:
<code>4874 1000 3508 3701</code>`

// donateCryptoText lists the crypto options.
const donateCryptoText = `🪙 <b>Donate with any crypto:</b>
Use the button below to donate via NOWPayments (USDT, BTC, ETH, and 300+ cryptos)

━━━━━━━━━━━━━━━━━━━━

💰 <b>Or send USDT directly:</b>

<b>ERC-20:</b>
<code>0xb3D102eA95Bf2376619730b8f3149131cc3F951B</code>

<b>TRC-20:</b>
<code>TWRfDPJ1TFUYJj47JcghvTEBnKcZWqgqcz</code>

<i>Tap an address to copy it. Thank you!</i> 🙏`

// Buttons of the /donate reply.
const (
	donateCardButtonText   = "💳 Donate by card (monobank)"
	donateCardURL          = "https://send.monobank.ua/jar/6Cn7KbVDjk"
	donateCryptoButtonText = "🪙 Donate via NOWPayments"
	donateCryptoURL        = "https://nowpayments.io/donation/sociallinkbot"
	donateBackButtonText   = "◀️ Back to Menu"
	donateBackCallback     = "/help"
)

// handleDonateCommand answers /donate, its /support alias and the menu
// button: the Stars tiers first when Stars support is on and the user could
// pay (the pre-checkout refuses payers who can't use the bonus downloads),
// then the card and crypto options.
func handleDonateCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	userID := int64(origUser.Id)
	isStarsEnabled := isStarsOfferedTo(userID)
	text := donateText(isStarsEnabled, userID)
	keyboard := donateKeyboard(isStarsEnabled)

	reply := payload.TgPlSendMessage{
		ChatId:              origChat.Id,
		ParseMode:           payload.ParseModeHTML,
		Text:                text,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
		ReplyMarkup:         keyboard,
	}

	message.NewSendMessage(reply)
	log.Printf("💚 Sent /donate (Stars: %t) to user %d", isStarsEnabled, origUser.Id)
}

// isStarsOfferedTo reports whether /donate shows the Stars tiers to a user.
func isStarsOfferedTo(userID int64) bool {
	result := config.IsStarsSupportEnabled()

	if result == true {
		result = supportCreditsUsable(userID)
	}

	return result
}

// postSupportMarkup returns the button that opens /donate for a photo or
// video that is a whole post (not an album item) in a private chat; groups
// stay clean. Otherwise it is nil, so the message has no buttons.
func postSupportMarkup(chat *response.TgChat, isWholePost bool) api.TgReplyMarkupInterface {
	var result api.TgReplyMarkupInterface
	isShown := (isWholePost == true) && (chat.Type == chatTypePrivate)

	if isShown == true {
		button := api.TgInlineKeyboardButton{Text: supportMenuButtonText, CallbackData: donateCommand}
		result = &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{{button}}}
	}

	return result
}

// donateText returns the /donate reply text.
func donateText(isStarsEnabled bool, userID int64) string {
	moneyOptions := fmt.Sprintf(donateSectionsLayout, donateCardText, donateCryptoText)
	result := fmt.Sprintf(donateLayout, donateIntroCryptoText, moneyOptions)

	if isStarsEnabled == true {
		starsSection := donateStarsSection(userID)
		options := fmt.Sprintf(donateSectionsLayout, starsSection, moneyOptions)
		result = fmt.Sprintf(donateLayout, donateIntroStarsText, options)
	}

	return result
}

// donateStarsSection adds the user's bonus balance when it is known.
func donateStarsSection(userID int64) string {
	result := donateStarsText
	balance, err := services.GetGatedFetchCredits(userID)

	if err == nil {
		result = fmt.Sprintf(donateBalanceTemplate, donateStarsText, balance)
	}

	return result
}

// donateKeyboard has the Stars tiers (when on), then card, crypto and back.
func donateKeyboard(isStarsEnabled bool) *api.TgInlineKeyboardMarkup {
	result := &api.TgInlineKeyboardMarkup{}
	cardRow := []api.TgInlineKeyboardButton{{Text: donateCardButtonText, URL: donateCardURL}}
	cryptoRow := []api.TgInlineKeyboardButton{{Text: donateCryptoButtonText, URL: donateCryptoURL}}
	backRow := []api.TgInlineKeyboardButton{{Text: donateBackButtonText, CallbackData: donateBackCallback}}

	if isStarsEnabled == true {
		result = supportTierKeyboard()
	}

	result.InlineKeyboard = append(result.InlineKeyboard, cardRow, cryptoRow, backRow)

	return result
}
