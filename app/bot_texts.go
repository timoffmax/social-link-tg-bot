package app

import "fmt"

// startPlatformsText is the platform list of the /start menu.
const startPlatformsText = `🌐 <b>Supported Platforms:</b>
📷 <b>Instagram</b> — Posts, Reels, Stories
🎵 <b>TikTok</b> — Videos, Photo Slideshows
🐦 <b>Twitter/X</b> — Photos, Videos, GIFs, Tweet Text
📺 <b>YouTube</b> — Shorts, Community Posts
🔴 <b>Reddit</b> — Videos, Images
👥 <b>Facebook</b> — Videos, Reels
😂 <b>9GAG</b> — Videos, Images
🧵 <b>Threads</b> — Text, Photos, Videos, Carousels

🔞 Restricted (age- or region-limited) Instagram posts work too, within a monthly quota per chat.
🖼️ Photos and videos of one post arrive as a single album.
🚫 No ads, no forced subscriptions.

`

// /start command lines for /donate, by Stars support state.
const (
	startDonateStarsLine  = "• /donate — Support the bot (Telegram Stars, card or crypto)\n"
	startDonateCryptoLine = "• /donate — Support the bot (card or crypto)\n"
)

// supportMenuButtonText is the main-menu button that opens /donate.
const supportMenuButtonText = "💚 Support the bot"

// broadcastAnnouncementTemplate is the predefined "What's New" broadcast;
// %s is the support line.
const broadcastAnnouncementTemplate = `🎉 <b>Fix Link Bot — What's New!</b>

Hey! Here's what's been improved recently:

🧵 <b>Threads support</b>
Send any Threads link — text, photos, videos and carousels arrive right in the chat.

🔞 <b>Restricted Instagram posts</b>
Age- or region-limited posts now download too, within a monthly quota per chat.

🖼️ <b>One album per post</b>
Posts with both photos and videos now arrive as a single album.

🎬 <b>YouTube Shorts</b>
Shorts download more reliably. Regular YouTube videos are no longer processed.

👥 <b>Facebook and Reddit</b>
Facebook videos download more reliably, and Reddit share links work again.

👤 <b>Author links</b>
Instagram and TikTok usernames now link to the author's profile.

━━━━━━━━━━━━━━━━━━━━

💡 <b>Tip:</b> Add me to a group chat — I'll automatically download media from links shared by anyone in the group!

%s`

// Support lines of the announcement, by Stars support state.
const (
	broadcastSupportStarsLine  = "💚 The bot stays free, with no ads. Like it? Support it with /donate — Telegram Stars, card or crypto."
	broadcastSupportCryptoLine = "💚 The bot stays free, with no ads. Like it? Support it with /donate — card or crypto."
)

// buildBroadcastAnnouncement returns the "What's New" text, mentioning
// Telegram Stars only while Stars support is on.
func buildBroadcastAnnouncement(isStarsEnabled bool) string {
	supportLine := broadcastSupportCryptoLine

	if isStarsEnabled == true {
		supportLine = broadcastSupportStarsLine
	}

	result := fmt.Sprintf(broadcastAnnouncementTemplate, supportLine)

	return result
}
