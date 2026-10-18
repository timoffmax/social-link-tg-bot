package app

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/timoffmax/social-link-bot/model/api"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/request/payload"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// documentStatsType is how a file sent as a document counts in the media
// stats.
const documentStatsType = "document"

// documentFallbackLabels caption a file sent as a document after its own
// media type failed, by what the file holds (plain text: the post caption
// may be what Telegram refused).
var documentFallbackLabels = map[string]string{
	services.MediaTypePhoto:     "🖼️ Photo (sent as a file)",
	services.MediaTypeVideo:     "📹 Video (sent as a file)",
	services.MediaTypeAnimation: "🎞️ GIF (sent as a file)",
	services.MediaTypeDocument:  "📎 File (sent as a file)",
}

// documentSender sends one document; a variable so tests can replace the
// Telegram call.
var documentSender = message.NewSendDocument

// documentStatsTracker counts a sent document; a variable so tests can
// replace the stats call.
var documentStatsTracker = trackMediaSent

// documentSend is one document to send: the file, what it holds, and its
// caption with the caption's parse mode and the reply markup.
type documentSend struct {
	Item      *services.MediaInfo
	Content   services.MediaContent
	Caption   string
	ParseMode string
	Markup    api.TgReplyMarkupInterface
}

// sendAsDocumentFallback sends a file that failed as its own media type as a
// document, named and captioned by what it holds, and counts it only when
// Telegram took it.
func sendAsDocumentFallback(mediaInfo *services.MediaInfo, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
	content := services.NameFileByContent(mediaInfo)
	label := documentFallbackLabels[content.Type]
	log.Printf("Trying fallback: sending the %s as a document...", content.Type)
	document := documentSend{Item: mediaInfo, Content: content, Caption: label}
	result := sendDocument(document, origChat, origMessage, url)

	return result
}

// sendDocumentFile sends an item typed as a document (e.g. a photo too
// narrow for sendPhoto) with the post caption, named by what it holds.
func sendDocumentFile(mediaInfo *services.MediaInfo, caption string, markup api.TgReplyMarkupInterface, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
	content := services.NameFileByContent(mediaInfo)
	document := documentSend{Item: mediaInfo, Content: content, Caption: caption, ParseMode: payload.ParseModeHTML, Markup: markup}
	result := sendDocument(document, origChat, origMessage, url)

	return result
}

// sendDocument sends one document and logs and counts the outcome.
func sendDocument(document documentSend, origChat *response.TgChat, origMessage *response.TgMessage, url string) error {
	var result error
	filename := filepath.Base(document.Item.Filename)
	documentPayload := payload.TgPlSendDocument{
		ChatId:              origChat.Id,
		Document:            filename,
		Caption:             document.Caption,
		ParseMode:           document.ParseMode,
		FilePath:            document.Item.Filename,
		ReplyToMessageId:    origMessage.MessageId,
		DisableNotification: true,
		ReplyMarkup:         document.Markup,
	}
	sent := documentSender(documentPayload)

	if sent.Err != nil {
		log.Printf("❌ Sending the %s %s as a document failed: %v", document.Content.Type, filename, sent.Err)
		result = fmt.Errorf("failed to send document: %w", sent.Err)
	}

	if sent.Err == nil {
		log.Printf("✅ Sent the %s %s as a document", document.Content.Type, filename)
		userID := int64(origMessage.From.Id)
		documentStatsTracker(userID, origMessage.From.Username, origChat.Id, origChat.Title, url, documentStatsType)
	}

	return result
}
