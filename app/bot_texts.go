package app

import (
	"fmt"
	"strings"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/services"
)

// startIntroText opens the /start menu.
const startIntroText = `🤖 <b>Fix Link Bot</b>

Send a social media link and I reply with the actual photos and videos, right in Telegram. No link previews, no ads, no forced subscriptions.

`

// startPlatformsTemplate is the platform list of the /start menu; %s is
// the Instagram Stories item, shown while the paid service is on.
const startPlatformsTemplate = `🌐 <b>Supported platforms:</b>
📷 <b>Instagram</b> — Posts, Reels%s
🎵 <b>TikTok</b> — Videos, Photo Slideshows
🐦 <b>Twitter/X</b> — Photos, Videos, GIFs, Tweet Text
📺 <b>YouTube</b> — Shorts, Community Posts
🔴 <b>Reddit</b> — Videos, Images
👥 <b>Facebook</b> — Videos, Reels
😂 <b>9GAG</b> — Videos, Images
🧵 <b>Threads</b> — Text, Photos, Videos, Carousels

`

// startStoriesItem adds Stories to the Instagram platform line.
const startStoriesItem = ", Stories"

// startPlatformsText returns the platform list, with Stories while the paid
// service that fetches them is on.
func startPlatformsText() string {
	storiesItem := ""
	isPaidServiceOn := services.IsPaidServiceConfigured()

	if isPaidServiceOn == true {
		storiesItem = startStoriesItem
	}

	result := fmt.Sprintf(startPlatformsTemplate, storiesItem)

	return result
}

// startPrivateTemplate explains private chats; %s is the restricted-post
// line.
const startPrivateTemplate = `💬 <b>In a private chat</b>
• Send me any supported link, and I reply with the media.
%s• Under posts that arrive as a single message: 💚 Support and 🐛 Report, which tells the developer something went wrong.

`

// startGroupTemplate explains groups: the restricted-post line, then the
// support button rate.
const startGroupTemplate = `👥 <b>In a group</b>
• Add me to the group. I answer every supported link anyone posts, no commands needed.
%s• Under posts that arrive as a single message: 🐛 Report, and 💚 Support under about 1 in %d, which opens a private chat.
• Group admins set daily limits from the menu, and choose platforms, descriptions, timezone and both buttons in Group Settings.

`

// Restricted-post lines of the chat sections; %s is the default monthly
// limit (see monthlyLimitText).
const (
	startPrivateRestrictedTemplate = "• Restricted posts: %s for you by default.\n"
	startGroupRestrictedTemplate   = "• Restricted posts: %s by default, shared by the whole group.\n"
)

// Monthly limit wordings: a count, none, or no limit.
const (
	monthlyLimitTemplate = "%d a month"
	monthlyLimitNoneText = "none"
	monthlyLimitOffText  = "no monthly limit"
)

// monthlyLimitText words a monthly limit: 0 is none, a negative one no limit.
func monthlyLimitText(limit int) string {
	result := fmt.Sprintf(monthlyLimitTemplate, limit)

	if limit == 0 {
		result = monthlyLimitNoneText
	}

	if limit < 0 {
		result = monthlyLimitOffText
	}

	return result
}

// startFreeTitleText opens the section on what comes without restrictions.
const startFreeTitleText = `🔓 <b>Free, no monthly limit</b>
Public posts on every platform above. Instagram carousels come with their first item.
`

// Daily limit lines of the free section; %d is the default daily limit.
const (
	startNoDailyLimitText     = "No daily limit either, unless a group admin sets one.\n"
	startDefaultDailyTemplate = "Daily limit: %d media a day by default; group admins can change it for their group.\n"
)

// startStoriesFreeText notes Stories go through the paid service.
const startStoriesFreeText = "Instagram Stories are free for you too, and so are YouTube Shorts, but Stories (and Shorts the free way can't get) go through the paid service (see below).\n"

// startFreeText returns the section on what comes without restrictions,
// with the default daily limit and Stories as configured.
func startFreeText() string {
	var text strings.Builder
	dailyLimit := config.GetDefaultDailyMediaLimit()
	dailyLine := startNoDailyLimitText
	isPaidServiceOn := services.IsPaidServiceConfigured()

	if dailyLimit > 0 {
		dailyLine = fmt.Sprintf(startDefaultDailyTemplate, dailyLimit)
	}

	text.WriteString(startFreeTitleText)
	text.WriteString(dailyLine)

	if isPaidServiceOn == true {
		text.WriteString(startStoriesFreeText)
	}

	text.WriteString("\n")
	result := text.String()

	return result
}

// startRestrictedTemplate says what restricted content is and how it is
// limited: the default allowance per person, per group, then the bonus line.
const startRestrictedTemplate = `🔞 <b>Restricted posts (monthly limit)</b>
Instagram posts whose author limited them by age or region. I can only fetch them through a paid third-party service, so they have a monthly limit: by default %s per person in a private chat and %s per group. Age-limited posts arrive blurred: tap to view. Each one shows how many are left this month.%s

💸 <b>Why the limits?</b>
The paid service charges for every restricted post, Story and Short it fetches, and the developer pays for it out of pocket. The monthly limits keep that affordable. If the month's budget runs out, these pause until it resets. With more support (/donate), the limits can go up.

`

// startRestrictedOffText replaces the restricted section while restricted
// posts can't be fetched at all.
const startRestrictedOffText = `🔞 <b>Restricted posts</b>
Instagram posts whose author limited them by age or region can't be downloaded right now.

`

// startBonusStarsText adds bonus downloads to the restricted section while
// Stars support is on.
const startBonusStarsText = "\nUsed them all? Supporting the bot with Telegram Stars (/donate) adds bonus downloads for you."

// startGuideText returns the "how it works" part of /start: private chats,
// groups, free and restricted content, with the default limits from the
// config (owners can change them per chat and person).
func startGuideText(isStarsEnabled bool) string {
	userAllowance := config.GetGatedFetchUserMonthlyAllowance()
	chatCap := config.GetGatedFetchChatMonthlyCap()
	isRestrictedOn := services.IsRestrictedPostsOffered()
	privateRestricted := ""
	groupRestricted := ""
	restrictedText := startRestrictedOffText
	bonusLine := ""

	if isStarsEnabled == true {
		bonusLine = startBonusStarsText
	}

	if isRestrictedOn == true {
		userLimit := monthlyLimitText(userAllowance)
		chatLimit := monthlyLimitText(chatCap)
		privateRestricted = fmt.Sprintf(startPrivateRestrictedTemplate, userLimit)
		groupRestricted = fmt.Sprintf(startGroupRestrictedTemplate, chatLimit)
		restrictedText = fmt.Sprintf(startRestrictedTemplate, userLimit, chatLimit, bonusLine)
	}

	privateText := fmt.Sprintf(startPrivateTemplate, privateRestricted)
	groupText := fmt.Sprintf(startGroupTemplate, groupRestricted, groupSupportEvery)
	freeText := startFreeText()
	result := fmt.Sprintf("%s%s%s%s", privateText, groupText, freeText, restrictedText)

	return result
}

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
