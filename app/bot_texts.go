package app

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
	"github.com/timoffmax/social-link-bot/services"
)

// startIntroText opens the /start menu.
const startIntroText = `🤖 <b>Fix Link Bot</b>

Send a social media link and I reply with the actual photos and videos, right in Telegram. No link previews, no ads, no forced subscriptions.

`

// startPlatformsTitle opens the platform list of the /start menu.
const startPlatformsTitle = "🌐 <b>Supported platforms:</b>\n"

// startPlatformLines are the platform list's lines, by platform id; the
// Instagram line takes the Stories item, shown while Stories can be fetched.
var startPlatformLines = map[string]string{
	settings.PlatformInstagram: "📷 <b>Instagram</b> — Posts, Reels%s\n",
	settings.PlatformTikTok:    "🎵 <b>TikTok</b> — Videos, Photo Slideshows\n",
	settings.PlatformTwitter:   "🐦 <b>Twitter/X</b> — Photos, Videos, GIFs, Tweet Text\n",
	settings.PlatformYouTube:   "📺 <b>YouTube</b> — Shorts, Community Posts\n",
	settings.PlatformReddit:    "🔴 <b>Reddit</b> — Videos, Images, Galleries, GIFs, Text and Link Posts\n",
	settings.PlatformFacebook:  "👥 <b>Facebook</b> — Videos, Reels, Photos, Text Posts\n",
	settings.Platform9GAG:      "😂 <b>9GAG</b> — Videos, Images, Articles\n",
	settings.PlatformThreads:   "🧵 <b>Threads</b> — Text, Photos, Videos, Carousels\n",
}

// startNoPlatformsText replaces the lines while every platform is off.
const startNoPlatformsText = "⏸ Link downloads are paused right now.\n"

// startStoriesItem adds Stories to the Instagram platform line.
const startStoriesItem = ", Stories"

// startPlatformsText returns the list of the platforms that are on, with
// Stories while they can be fetched.
func startPlatformsText() string {
	var text strings.Builder
	storiesItem := ""
	isStoriesOn := services.IsStoriesOffered()
	enabled := services.EnabledPlatforms()

	if isStoriesOn == true {
		storiesItem = startStoriesItem
	}

	text.WriteString(startPlatformsTitle)

	for _, platformID := range enabled {
		line := startPlatformLine(platformID, storiesItem)
		text.WriteString(line)
	}

	if len(enabled) == 0 {
		text.WriteString(startNoPlatformsText)
	}

	text.WriteString("\n")
	result := text.String()

	return result
}

// startPlatformLine returns one platform's line of the list.
func startPlatformLine(platformID string, storiesItem string) string {
	result := startPlatformLines[platformID]

	if platformID == settings.PlatformInstagram {
		result = fmt.Sprintf(result, storiesItem)
	}

	return result
}

// startPrivateTemplate explains private chats; %s is the special-download
// line.
const startPrivateTemplate = `💬 <b>In a private chat</b>
• Send me any supported link, and I reply with the media.
%s• Under posts that arrive as a single message: 💚 Support and 🐞 Report a bug, which tells the developer something went wrong.

`

// startGroupTemplate explains groups: the special-download line, the support
// button rate, then the admin-rights line (see startGroupAdminRightsText).
const startGroupTemplate = `👥 <b>In a group</b>
• Add me to the group. I answer every supported link anyone posts, no commands needed.
%s• Under posts that arrive as a single message: 🐞 Report a bug, and 💚 Support under about 1 in %d, which opens a private chat.
%s• Group admins set daily limits from the menu, and choose platforms, descriptions, timezone and both buttons in Group Settings.
• To have messages over a daily limit removed, make me an admin with the "Delete messages" right; without it, the limit still works and those messages just stay.

`

// Admin-rights lines of the group section: the claim that no admin rights
// are needed only once getMe confirmed the bot reads every group message
// (Group Privacy off); otherwise a hint that stays true either way.
const (
	startGroupNoAdminRightsText = "• I don't need admin rights to work in a group.\n"
	startGroupAdminHintText     = "• If I don't answer links in your group, make me an admin there.\n"
)

// startGroupAdminRightsText returns the admin-rights line of the group
// section, by the Group Privacy state read at startup.
func startGroupAdminRightsText() string {
	result := startGroupAdminHintText

	if isGroupPrivacyOff() == true {
		result = startGroupNoAdminRightsText
	}

	return result
}

// Special-download lines of the chat sections; %s is the default monthly
// limit (see monthlyLimitText).
const (
	startPrivateSpecialTemplate = "• Special downloads: %s for you by default.\n"
	startGroupSpecialTemplate   = "• Special downloads: %s by default, shared by the whole group.\n"
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

// startSpecialExceptText notes special downloads aren't free.
const startSpecialExceptText = "Except special downloads (see below).\n"

// startFreeText returns the section on what comes without restrictions,
// with the default daily limit and, while any are counted, the special
// downloads excepted.
func startFreeText() string {
	var text strings.Builder
	dailyLimit := config.GetDefaultDailyMediaLimit()
	dailyLine := startNoDailyLimitText
	isSpecialOn := services.IsSpecialDownloadsConfigured()

	if dailyLimit > 0 {
		dailyLine = fmt.Sprintf(startDefaultDailyTemplate, dailyLimit)
	}

	text.WriteString(startFreeTitleText)
	text.WriteString(dailyLine)

	if isSpecialOn == true {
		text.WriteString(startSpecialExceptText)
	}

	text.WriteString("\n")
	result := text.String()

	return result
}

// startSpecialTemplate says what special downloads are and how they are
// limited: the kinds, the channel, the default limits per person and per
// group, the blur and "left" lines, the bonus line, then why.
const startSpecialTemplate = `💎 <b>Special downloads (monthly limit)</b>
%s: I can only get these %s, so they share one monthly limit: by default %s per person in a private chat and %s per group.%s%s%s

💸 <b>Why the limits?</b>
%s

`

// startSpecialOffText replaces the special section while special downloads
// can't be fetched at all.
const startSpecialOffText = `💎 <b>Special downloads</b>
Links that only a paid service or the developer's home connection can fetch can't be downloaded right now.

`

// Kinds of special downloads, in the order the guide lists them.
const (
	startKindRestrictedText = "Instagram posts whose author limited them by age or region"
	startKindReelsText      = "reels whose video Instagram shows only to logged-in users"
	startKindStoriesText    = "Instagram Stories"
	startKindAllShortsText  = "YouTube Shorts"
	startKindSomeShortsText = "YouTube Shorts I can't get for free"
)

// startSpecialBlurText notes age-limited posts arrive blurred.
const startSpecialBlurText = " Age-limited posts arrive blurred: tap to view."

// startSpecialLeftText notes the "left" count; it shows only under a limit.
const startSpecialLeftText = " Each one shows how many are left this month."

// Routes special downloads take: a paid service, the home relay or both.
const (
	specialRoutePaid  = "paid"
	specialRouteRelay = "relay"
	specialRouteBoth  = "both"
)

// startSpecialChannelTexts say how special downloads are fetched, by route.
var startSpecialChannelTexts = map[string]string{
	specialRoutePaid:  "through a paid third-party service",
	specialRouteRelay: "through the developer's own home connection",
	specialRouteBoth:  "through a paid third-party service or the developer's own home connection",
}

// startSpecialWhyTexts say why special downloads are limited, by route.
var startSpecialWhyTexts = map[string]string{
	specialRoutePaid:  "The paid service charges for each one, and the developer pays for it out of pocket. The monthly limits keep that affordable. If the month's budget runs out, these pause until it resets. With more support (/donate), the limits can go up.",
	specialRouteRelay: "Each one goes over the developer's home connection, which sites block if it fetches too much. The monthly limits keep it working for everyone.",
	specialRouteBoth:  "The paid service charges for each one and the developer pays for it out of pocket; the home connection gets blocked by sites if it fetches too much. The monthly limits keep both working. If the month's budget runs out, the paid ones pause until it resets. With more support (/donate), the limits can go up.",
}

// startSpecialRoute returns the route the configured special downloads take.
func startSpecialRoute(kinds services.SpecialDownloadKindSet) string {
	result := specialRoutePaid
	isBoth := (kinds.ViaRelay == true) && (kinds.ViaPaid == true)

	if kinds.ViaRelay == true {
		result = specialRouteRelay
	}

	if isBoth == true {
		result = specialRouteBoth
	}

	return result
}

// startSpecialKindsText lists the configured kinds, capitalised, joined
// "a, b and c".
func startSpecialKindsText(kinds services.SpecialDownloadKindSet) string {
	items := []string{}
	isSomeShorts := (kinds.Shorts == true) && (kinds.AllShorts == false)

	if kinds.RestrictedPosts == true {
		items = append(items, startKindRestrictedText)
	}

	if kinds.ReelVideos == true {
		items = append(items, startKindReelsText)
	}

	if kinds.Stories == true {
		items = append(items, startKindStoriesText)
	}

	if kinds.AllShorts == true {
		items = append(items, startKindAllShortsText)
	}

	if isSomeShorts == true {
		items = append(items, startKindSomeShortsText)
	}

	joined := joinWithAnd(items)
	result := capitaliseFirst(joined)

	return result
}

// Separators of joinWithAnd.
const (
	listSeparator     = ", "
	listLastSeparator = " and "
)

// joinWithAnd joins items as "a, b and c".
func joinWithAnd(items []string) string {
	result := strings.Join(items, listSeparator)
	last := len(items) - 1

	if last > 0 {
		head := strings.Join(items[:last], listSeparator)
		result = fmt.Sprintf("%s%s%s", head, listLastSeparator, items[last])
	}

	return result
}

// capitaliseFirst upper-cases the first letter of a text.
func capitaliseFirst(text string) string {
	result := text

	if text != "" {
		first, size := utf8.DecodeRuneInString(text)
		upper := unicode.ToUpper(first)
		result = fmt.Sprintf("%c%s", upper, text[size:])
	}

	return result
}

// startSpecialText returns the special-download section of the guide for the
// configured kinds and default limits.
func startSpecialText(userAllowance int, chatCap int, bonusLine string) string {
	kinds := services.CurrentSpecialDownloadKinds()
	route := startSpecialRoute(kinds)
	kindsText := startSpecialKindsText(kinds)
	userLimit := monthlyLimitText(userAllowance)
	chatLimit := monthlyLimitText(chatCap)
	hasDefaultLimit := (userAllowance > 0) || (chatCap > 0)
	blurText := ""
	leftText := ""

	if kinds.RestrictedPosts == true {
		blurText = startSpecialBlurText
	}

	if hasDefaultLimit == true {
		leftText = startSpecialLeftText
	}

	channel := startSpecialChannelTexts[route]
	why := startSpecialWhyTexts[route]
	result := fmt.Sprintf(startSpecialTemplate, kindsText, channel, userLimit, chatLimit, blurText, leftText, bonusLine, why)

	return result
}

// startBonusStarsText adds bonus downloads to the special section while
// Stars support is on.
const startBonusStarsText = "\nUsed them all? Supporting the bot with Telegram Stars (/donate) adds bonus downloads for you, and anyone in a group can raise the group's limit for everyone with 🚀 /boost."

// startGuideText returns the "how it works" part of /start: private chats,
// groups, free content and special downloads, with the default limits from
// the config (owners can change them per chat and person).
func startGuideText(isStarsEnabled bool) string {
	userAllowance := config.GetGatedFetchUserMonthlyAllowance()
	chatCap := config.GetGatedFetchChatMonthlyCap()
	isSpecialOn := services.IsSpecialDownloadsOffered()
	privateSpecial := ""
	groupSpecial := ""
	specialText := startSpecialOffText
	bonusLine := ""

	if isStarsEnabled == true {
		bonusLine = startBonusStarsText
	}

	if isSpecialOn == true {
		userLimit := monthlyLimitText(userAllowance)
		chatLimit := monthlyLimitText(chatCap)
		privateSpecial = fmt.Sprintf(startPrivateSpecialTemplate, userLimit)
		groupSpecial = fmt.Sprintf(startGroupSpecialTemplate, chatLimit)
		specialText = startSpecialText(userAllowance, chatCap, bonusLine)
	}

	privateText := fmt.Sprintf(startPrivateTemplate, privateSpecial)
	adminRightsText := startGroupAdminRightsText()
	groupText := fmt.Sprintf(startGroupTemplate, groupSpecial, groupSupportEvery, adminRightsText)
	freeText := startFreeText()
	result := fmt.Sprintf("%s%s%s%s", privateText, groupText, freeText, specialText)

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

💎 <b>Special downloads</b>
Restricted Instagram posts, login-only reels, Stories and Shorts that need a paid service or the developer's home connection now share one monthly limit; each one shows how many are left.

🖼️ <b>One album per post</b>
Posts with both photos and videos now arrive as a single album.

🎬 <b>YouTube Shorts</b>
Shorts download more reliably. Regular YouTube videos are no longer processed.

👥 <b>Facebook and Reddit</b>
Facebook photos (up to 4 per post), text posts and video posts now arrive too. Reddit galleries come in full, GIFs play as GIFs, text posts and links arrive too, and redd.it short links work.

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
