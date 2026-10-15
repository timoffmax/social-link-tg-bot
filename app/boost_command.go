package app

import (
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// boostCommand raises a group's limit of restricted posts with Stars.
const boostCommand = "/boost"

// boostStartPrefix starts the /start payload "boost_<chat_id>" that opens a
// group's boost screen in the private chat.
const boostStartPrefix = "boost_"

// boostCallbackPrefix starts a boost tier button "/boost_<stars>_<chat_id>".
const boostCallbackPrefix = "/boost_"

// boostCallbackSeparator splits the stars and the chat id of a tier button.
const boostCallbackSeparator = "_"

// boostButtonText opens the private chat on the group's boost screen.
const boostButtonText = "🚀 Boost this group"

// boostTierButtonTemplate is a boost tier: Stars, then the group's credits.
const boostTierButtonTemplate = "⭐ %d Stars: +%d for the group"

// boostInvoiceTitle is the invoice title (Telegram allows 1-32 characters).
const boostInvoiceTitle = "Boost a group"

// boostTitleLimit bounds the group title in texts and the invoice, in runes.
const boostTitleLimit = 60

// Boost texts.
const (
	boostGroupText            = "🚀 <b>Boost this group</b>\n\nThis group gets a monthly number of restricted posts for free. Anyone can raise it with Telegram Stars: the group's bonus downloads are shared by everyone here and used once the free monthly ones run out. They don't expire.\n\nThe payment happens in a private chat with me."
	boostPrivateText          = "🚀 To boost a group, send /boost in that group and tap the button."
	boostUnavailableText      = "🚀 Boosting groups with Telegram Stars isn't available right now."
	boostClosedText           = "🚀 This group can't be boosted: restricted posts are turned off there, or it has no monthly limit."
	boostUnknownGroupText     = "🚀 I couldn't find that group, or you aren't in it. Send /boost in the group you want to boost."
	boostScreenTemplate       = "🚀 <b>Boost %s</b>\n\nBonus downloads for this group are shared by all its members and used once its free monthly restricted posts run out. They don't expire, and a post I can't fetch doesn't use one.\n\n🎟️ Group bonus downloads now: %d\n\nPick an amount. By paying you agree to the /terms. Help with payments: /paysupport."
	boostInvoiceTemplate      = "Bonus downloads of restricted Instagram posts for %s: +%d, shared by all members and used after the group's free monthly ones. They don't expire. By paying you agree to /terms."
	boostThanksTemplate       = "🚀 Thank you! %s got %d bonus downloads of restricted posts. Group bonus downloads now: %d."
	boostGroupNoticeTemplate  = "🚀 %s boosted this group: +%d restricted-post downloads, shared by everyone here. Thank you! Group bonus downloads: %d."
	boostRepeatTemplate       = "🚀 Thank you! This payment was already counted. Group bonus downloads: %d."
	boostGatedHintTemplate    = "Raise this group's limit with Telegram Stars: %s"
	boostDefaultGroupNameText = "the group"
)

// handleBoostCommand answers /boost: in a group a button to the private boost
// screen, in a private chat how to boost.
func handleBoostCommand(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser) {
	isEnabled := config.IsStarsSupportEnabled()
	isGroup := (origChat.Type != chatTypePrivate)
	text := boostPrivateText
	var keyboard *api.TgInlineKeyboardMarkup

	if isGroup == true {
		text, keyboard = groupBoostOffer(origChat.Id)
	}

	if isEnabled == false {
		text = boostUnavailableText
		keyboard = nil
	}

	sendBoostMessage(origChat.Id, origMessage.MessageId, text, keyboard)
	log.Printf("🚀 Sent /boost to user %d in chat %d", origUser.Id, origChat.Id)
}

// groupBoostOffer returns the group's boost text and link button, or why it
// can't be boosted.
func groupBoostOffer(chatID int64) (string, *api.TgInlineKeyboardMarkup) {
	var result *api.TgInlineKeyboardMarkup
	text := boostClosedText
	isOpen := canBoostGroup(chatID)
	button, hasButton := groupBoostButton(chatID)

	if (isOpen == true) && (hasButton == true) {
		text = boostGroupText
		result = &api.TgInlineKeyboardMarkup{InlineKeyboard: [][]api.TgInlineKeyboardButton{{button}}}
	}

	return text, result
}

// groupBoostButton links to the private chat on the group's boost screen;
// false while the bot's username is unknown.
func groupBoostButton(chatID int64) (api.TgInlineKeyboardButton, bool) {
	startPayload := fmt.Sprintf("%s%d", boostStartPrefix, chatID)
	url := fmt.Sprintf(donateStartURLTemplate, botUsername, startPayload)
	result := api.TgInlineKeyboardButton{Text: boostButtonText, URL: url}
	isKnown := (botUsername != "")

	return result, isKnown
}

// Group boost checks; variables so tests run without a database or Telegram.
var (
	canBoostGroup      = services.CanUseGroupFetchCredits
	isGroupReachable   = services.IsGroupReachable
	getGroupTitle      = services.GetGroupTitle
	getGroupBoostBonus = services.GetGroupFetchCredits
	isGroupMember      = services.IsChatMember
)

// boostableGroupTitle returns the group's title when the bot is in the group
// and the user is a member: only members see a group's boost screen or get
// its invoice, so a guessed id reveals nothing and no payment lands on a
// chat the bot can't serve.
func boostableGroupTitle(chatID int64, userID int64) (string, error) {
	title, err := getGroupTitle(chatID)
	isMember := false

	if err == nil {
		isMember, err = isGroupMember(chatID, userID)
	}

	if (err == nil) && (isMember == false) {
		err = errBoostNotMember
	}

	return title, err
}

// errBoostNotMember refuses a boost by someone who isn't in the group.
var errBoostNotMember = errors.New("not a member of the group")

// boostStartChatID returns the group id of a "/start boost_<chat_id>" link.
func boostStartChatID(startPayload string) (int64, bool) {
	raw := strings.TrimPrefix(startPayload, boostStartPrefix)
	chatID, err := strconv.ParseInt(raw, 10, 64)
	hasPrefix := strings.HasPrefix(startPayload, boostStartPrefix)
	result := (hasPrefix == true) && (err == nil) && (chatID < 0)

	return chatID, result
}

// handleBoostStart shows a group's boost screen in the private chat: the
// group's name, its bonus downloads and the Stars tiers.
func handleBoostStart(origMessage *response.TgMessage, origChat *response.TgChat, origUser *response.TgUser, chatID int64) {
	isEnabled := config.IsStarsSupportEnabled()
	userID := int64(origUser.Id)
	title, titleErr := boostableGroupTitle(chatID, userID)
	isOpen := (titleErr == nil) && (canBoostGroup(chatID) == true)
	text := boostUnknownGroupText
	var keyboard *api.TgInlineKeyboardMarkup

	if (titleErr == nil) && (isOpen == false) {
		text = boostClosedText
	}

	if isOpen == true {
		text = boostScreenText(chatID, title)
		keyboard = boostTierKeyboard(chatID)
	}

	if isEnabled == false {
		text = boostUnavailableText
		keyboard = nil
	}

	if titleErr != nil {
		log.Printf("🚀 Boost screen for chat %d: %v", chatID, titleErr)
	}

	sendBoostMessage(origChat.Id, origMessage.MessageId, text, keyboard)
}

// boostScreenText renders the boost screen with the group's bonus balance.
func boostScreenText(chatID int64, title string) string {
	name := boostGroupName(title)
	balance, err := getGroupBoostBonus(chatID)

	if err != nil {
		log.Printf("⚠️ Group %d bonus balance unavailable: %v", chatID, err)
	}

	result := fmt.Sprintf(boostScreenTemplate, name, balance)

	return result
}

// boostGroupName returns the HTML-escaped, shortened group title.
func boostGroupName(title string) string {
	shortTitle := shortenRunes(title, boostTitleLimit)
	result := html.EscapeString(shortTitle)

	if result == "" {
		result = boostDefaultGroupNameText
	}

	return result
}

// shortenRunes shortens text to limit runes, with an ellipsis.
func shortenRunes(text string, limit int) string {
	runes := []rune(text)
	result := text

	if len(runes) > limit {
		result = fmt.Sprintf("%s…", string(runes[:limit]))
	}

	return result
}

// boostTierKeyboard has one button per tier for the group.
func boostTierKeyboard(chatID int64) *api.TgInlineKeyboardMarkup {
	result := &api.TgInlineKeyboardMarkup{}
	tiers := services.SupportTiers()

	for _, tier := range tiers {
		label := fmt.Sprintf(boostTierButtonTemplate, tier.Stars, tier.Credits)
		callbackData := fmt.Sprintf("%s%d%s%d", boostCallbackPrefix, tier.Stars, boostCallbackSeparator, chatID)
		button := api.TgInlineKeyboardButton{Text: label, CallbackData: callbackData}
		result.InlineKeyboard = append(result.InlineKeyboard, []api.TgInlineKeyboardButton{button})
	}

	return result
}

// parseBoostTierCallback returns the tier and group of a boost tier button.
func parseBoostTierCallback(data string) (services.SupportTier, int64, bool) {
	tier := services.SupportTier{}
	chatID := int64(0)
	isFound := false
	raw := strings.TrimPrefix(data, boostCallbackPrefix)
	parts := strings.SplitN(raw, boostCallbackSeparator, 2)
	isShaped := (strings.HasPrefix(data, boostCallbackPrefix) == true) && (len(parts) == 2)

	if isShaped == true {
		tier, chatID, isFound = parseBoostTierParts(parts[0], parts[1])
	}

	return tier, chatID, isFound
}

// parseBoostTierParts parses the stars and chat id of a boost tier button.
func parseBoostTierParts(rawStars string, rawChatID string) (services.SupportTier, int64, bool) {
	stars, starsErr := strconv.Atoi(rawStars)
	chatID, chatErr := strconv.ParseInt(rawChatID, 10, 64)
	tier, isKnown := services.FindSupportTier(stars)
	result := (starsErr == nil) && (chatErr == nil) && (chatID < 0) && (isKnown == true)

	return tier, chatID, result
}

// isBoostTierCallback reports a boost tier button.
func isBoostTierCallback(data string) bool {
	result := strings.HasPrefix(data, boostCallbackPrefix)

	return result
}

// handleBoostTierCallback sends the invoice of the pressed boost tier.
func handleBoostTierCallback(callbackQuery *response.TgCallbackQuery) {
	tier, chatID, isFound := parseBoostTierCallback(callbackQuery.Data)
	isEnabled := config.IsStarsSupportEnabled()
	privateChatID := callbackQuery.Message.Chat.Id
	userID := int64(callbackQuery.From.Id)

	if isFound == false {
		log.Printf("🤷 Unknown boost tier: %s", callbackQuery.Data)
	}

	if (isFound == true) && (isEnabled == false) {
		sendSupportReply(privateChatID, callbackQuery.Message.MessageId, boostUnavailableText)
	}

	title := ""
	var groupErr error
	isBoostable := false

	if (isFound == true) && (isEnabled == true) {
		title, groupErr = boostableGroupTitle(chatID, userID)
		isBoostable = (groupErr == nil) && (canBoostGroup(chatID) == true)
	}

	if (isFound == true) && (isEnabled == true) && (isBoostable == false) {
		log.Printf("🚀 Boost invoice for chat %d refused for user %d: %v", chatID, userID, groupErr)
		sendSupportReply(privateChatID, callbackQuery.Message.MessageId, boostUnknownGroupText)
	}

	if (isFound == true) && (isEnabled == true) && (isBoostable == true) {
		invoice := buildBoostInvoice(privateChatID, userID, chatID, title, tier)
		sendSupportInvoice(invoice)
	}
}

// buildBoostInvoice returns a Stars invoice boosting the group: currency XTR,
// no provider token, exactly one price and no reply markup.
func buildBoostInvoice(privateChatID int64, userID int64, chatID int64, title string, tier services.SupportTier) payload.TgPlSendInvoice {
	shortTitle := shortenRunes(title, boostTitleLimit)
	groupName := boostDefaultGroupNameText

	if shortTitle != "" {
		groupName = shortTitle
	}

	result := payload.TgPlSendInvoice{
		ChatId:              privateChatID,
		Title:               boostInvoiceTitle,
		Description:         fmt.Sprintf(boostInvoiceTemplate, groupName, tier.Credits),
		Payload:             services.BuildBoostPayload(tier, userID, chatID),
		Currency:            payload.CurrencyStars,
		Prices:              []payload.TgLabeledPrice{{Label: supportInvoicePriceLabel, Amount: tier.Stars}},
		DisableNotification: true,
	}

	return result
}

// sendBoostMessage replies with a boost text and optional buttons.
func sendBoostMessage(chatID int64, replyToMessageID int, text string, keyboard *api.TgInlineKeyboardMarkup) {
	reply := payload.TgPlSendMessage{
		ChatId:              chatID,
		ParseMode:           payload.ParseModeHTML,
		Text:                text,
		ReplyToMessageId:    replyToMessageID,
		DisableNotification: true,
	}

	if keyboard != nil {
		reply.ReplyMarkup = keyboard
	}

	message.NewSendMessage(reply)
}

// sendBoostPaymentReplies thanks the payer and tells the group, in the
// background: the group's title comes from Telegram, which may be slow.
func sendBoostPaymentReplies(record services.SupportPaymentRecord, credits int, chatID int64, payerName string, privateChatID int64, replyToMessageID int) {
	thanks, notice := boostPaymentTexts(record, credits, chatID, payerName)
	sendSupportReply(privateChatID, replyToMessageID, thanks)
	sendBoostGroupNotice(chatID, notice)
}

// boostPaymentTexts returns the payer's thank-you and the group notice of a
// recorded boost; the notice is empty for a repeated delivery.
func boostPaymentTexts(record services.SupportPaymentRecord, credits int, chatID int64, payerName string) (string, string) {
	title, _ := getGroupTitle(chatID)
	groupName := boostGroupName(title)
	name := html.EscapeString(payerName)
	thanks := fmt.Sprintf(boostThanksTemplate, groupName, credits, record.Balance)
	notice := fmt.Sprintf(boostGroupNoticeTemplate, name, credits, record.Balance)

	if record.IsNew == false {
		thanks = fmt.Sprintf(boostRepeatTemplate, record.Balance)
		notice = ""
	}

	// A payment that matched no tier credits nothing.
	if (record.IsNew == true) && (credits == 0) {
		thanks = supportNoTierText
		notice = ""
	}

	return thanks, notice
}

// sendBoostGroupNotice tells the group it was boosted.
func sendBoostGroupNotice(chatID int64, notice string) {
	if notice != "" {
		sendBoostMessage(chatID, 0, notice, nil)
	}
}

// moveGroupCredits moves a group's paid rows; a variable for tests.
var moveGroupCredits = services.MoveGroupCredits

// moveUpgradedGroup moves a group's bonus downloads, its restricted-post
// policy and its boosts to the supergroup's new id, logging the outcome.
func moveUpgradedGroup(oldChatID int64, newChatID int64) {
	err := moveGroupCredits(oldChatID, newChatID)

	if err != nil {
		log.Printf("❌ Moving group %d to supergroup %d failed: %v", oldChatID, newChatID, err)
	}

	if err == nil {
		log.Printf("🚀 Moved group %d bonus downloads and policy to supergroup %d", oldChatID, newChatID)
	}
}
