package services

import "fmt"

// textMessageBuilder formats a text message around a prefix of its plain
// body; isCut tells whether the body was cut to fit.
type textMessageBuilder func(bodyPrefix string, isCut bool) string

// fitTextMessage returns build() of the longest prefix of the plain body for
// which the message, with the original link the sender appends, is at most
// Telegram's message limit. The body is cut at a rune boundary before it is
// escaped, so an entity is never split.
func fitTextMessage(body, postURL string, build textMessageBuilder) string {
	linkSuffix := FormatOriginalLink(postURL)
	measure := func(prefix string) int {
		isCut := (len(prefix) < len(body))
		caption := build(prefix, isCut)
		message := fmt.Sprintf("%s%s", caption, linkSuffix)

		return telegramTextLength(message)
	}
	end := fitPrefixEnd(body, telegramMessageLimit, measure)
	fittedEnd := max(end, 0)
	isCut := (fittedEnd < len(body))
	result := build(body[:fittedEnd], isCut)

	return result
}

// fitCaptionMessage returns build() of the longest prefix of the plain body
// for which the caption, with the original link appendOriginalURL adds, fits
// Telegram's caption limit (see captionSize), so no later byte cut touches
// it. The body is cut at a rune boundary before it is escaped, so an entity
// or a tag is never split.
func fitCaptionMessage(body, postURL string, build textMessageBuilder) string {
	measure := func(prefix string) int {
		isCut := (len(prefix) < len(body))
		caption := build(prefix, isCut)

		return captionSize(caption, postURL)
	}
	end := fitPrefixEnd(body, TelegramCaptionLimitBytes, measure)
	fittedEnd := max(end, 0)
	isCut := (fittedEnd < len(body))
	result := build(body[:fittedEnd], isCut)

	return result
}

// captionSize is the size of a caption with the original link appended: the
// larger of its bytes (what the senders' cuts measure) and Telegram's count
// (UTF-16 units of the text without tags, entities decoded), so a caption
// within the limit by it passes both.
func captionSize(caption, postURL string) int {
	linkSuffix := FormatOriginalLink(postURL)
	message := fmt.Sprintf("%s%s", caption, linkSuffix)
	telegramLength := telegramTextLength(message)
	result := max(len(message), telegramLength)

	return result
}
