package settings

// registry lists every managed setting, in the order the Settings tab shows
// them. Defaults match the .env getters' own defaults in config/env.go (a
// test pins it).
var registry = []Setting{
	platformSetting(KeyPlatformInstagramEnabled, "ENABLE_INSTAGRAM", "Instagram", "Off: Instagram links are ignored in every chat."),
	platformSetting(KeyPlatformTikTokEnabled, "ENABLE_TIKTOK", "TikTok", "Off: TikTok links are ignored in every chat."),
	platformSetting(KeyPlatformTwitterEnabled, "ENABLE_TWITTER", "Twitter/X", "Off: Twitter/X links are ignored in every chat."),
	platformSetting(KeyPlatformYouTubeEnabled, "ENABLE_YOUTUBE", "YouTube", "Off: YouTube Shorts and community posts are ignored in every chat."),
	platformSetting(KeyPlatformRedditEnabled, "ENABLE_REDDIT", "Reddit", "Off: Reddit links are ignored in every chat."),
	platformSetting(KeyPlatformFacebookEnabled, "ENABLE_FACEBOOK", "Facebook", "Off: Facebook links are ignored in every chat."),
	platformSetting(KeyPlatform9GAGEnabled, "ENABLE_9GAG", "9GAG", "Off: 9GAG links are ignored in every chat."),
	platformSetting(KeyPlatformThreadsEnabled, "ENABLE_THREADS", "Threads", "Off: Threads links are ignored in every chat."),
	{
		Key:      KeyInstagramFixedLinkService,
		Group:    GroupPlatforms,
		Type:     TypeEnum,
		EnvVar:   "INSTAGRAM_SERVICE",
		Default:  FixedLinkToInstagram,
		Enum:     []string{FixedLinkToInstagram, FixedLinkOnebigram},
		Approval: ApprovalNever,
		Label:    "Instagram fixed-link service",
		Impact:   "The domain of the Instagram link the bot sends when it can not download a post.",
	},
	{
		Key:      KeyProxyEnabled,
		Group:    GroupProxies,
		Type:     TypeBool,
		EnvVar:   "ENABLE_PROXIES",
		Default:  true,
		Approval: ApprovalNever,
		Label:    "Proxies",
		Impact:   "Off: no platform uses the proxy pool (the pool itself stays in .env).",
	},
	proxySetting(KeyProxyYouTubeEnabled, "ENABLE_YOUTUBE_PROXY", false, "YouTube proxy"),
	proxySetting(KeyProxyTwitterEnabled, "ENABLE_TWITTER_PROXY", true, "Twitter/X proxy"),
	proxySetting(KeyProxyTikTokEnabled, "ENABLE_TIKTOK_PROXY", true, "TikTok proxy"),
	proxySetting(KeyProxyFacebookEnabled, "ENABLE_FACEBOOK_PROXY", true, "Facebook proxy"),
	proxySetting(KeyProxyRedditEnabled, "ENABLE_REDDIT_PROXY", true, "Reddit proxy"),
	proxySetting(KeyProxy9GAGEnabled, "ENABLE_9GAG_PROXY", true, "9GAG proxy"),
	hostSetting(KeyServiceKKInstagramHost, "KKINSTAGRAM_HOST", "kkinstagram.com", "kkinstagram host"),
	hostSetting(KeyServiceKKTikTokHost, "KKTIKTOK_HOST", "kktiktok.com", "kktiktok host"),
	hostSetting(KeyServiceVxredditHost, "VXREDDIT_HOST", "vxreddit.com", "vxreddit host"),
	hostSetting(KeyServiceFacebedHost, "FACEBED_HOST", "www.facebed.com", "facebed host"),
	{
		Key:       KeyApifyMonthlyBudgetUsd,
		Group:     GroupPaid,
		Type:      TypeDecimal,
		EnvVar:    "APIFY_MONTHLY_BUDGET_USD",
		Default:   4.0,
		Min:       bound(0),
		Ceiling:   bound(CeilingApifyMonthlyBudgetUsd),
		Scale:     2,
		Approval:  ApprovalRaise,
		SafeValue: 0.0,
		Label:     "Apify monthly budget (USD)",
		Impact:    "Real-dollar cap on Apify spend a month (restricted posts, reel videos, stories, YouTube Shorts); 0 turns Apify off.",
	},
	{
		Key:       KeyHikerMonthlyBudgetUsd,
		Group:     GroupPaid,
		Type:      TypeDecimal,
		EnvVar:    "HIKERAPI_MONTHLY_BUDGET_USD",
		Default:   1.0,
		Min:       bound(0),
		Ceiling:   bound(CeilingHikerMonthlyBudgetUsd),
		Scale:     2,
		Approval:  ApprovalRaise,
		SafeValue: 0.0,
		Label:     "HikerAPI monthly budget (USD)",
		Impact:    "Cap on HikerAPI spend a month, counted by the bot's own ledger; 0 turns HikerAPI off.",
	},
	{
		Key:          KeyHikerPricePerRequestUsd,
		Group:        GroupPaid,
		Type:         TypeDecimal,
		EnvVar:       "HIKERAPI_PRICE_PER_REQUEST_USD",
		Default:      0.001,
		Min:          bound(0),
		MinExclusive: true,
		Max:          bound(1),
		Scale:        6,
		Approval:     ApprovalLower,
		SafeValue:    1.0,
		Label:        "HikerAPI price per request (USD)",
		Impact:       "What the ledger counts for one billed HikerAPI request; a lower price lets more requests fit in the budget.",
	},
	{
		Key:      KeyInstagramProviderOrder,
		Group:    GroupPaid,
		Type:     TypeOrderedList,
		EnvVar:   "INSTAGRAM_PAID_PROVIDERS",
		Default:  []string{ProviderApify, ProviderHiker},
		Enum:     []string{ProviderApify, ProviderHiker},
		Approval: ApprovalNever,
		Label:    "Paid Instagram providers, in order",
		Impact:   "The paid providers tried for restricted posts, reel videos and stories, first to last.",
	},
	{
		Key:      KeyHikerLowBalanceRequests,
		Group:    GroupPaid,
		Type:     TypeInt,
		EnvVar:   "HIKERAPI_LOW_BALANCE_REQUESTS",
		Default:  100,
		Min:      bound(0),
		Max:      bound(100000),
		Approval: ApprovalNever,
		Label:    "HikerAPI low-balance alert (requests)",
		Impact:   "The hourly check alerts you when the HikerAPI account has fewer requests left than this.",
	},
	{
		Key:      KeyGatedGlobalMonthlyCap,
		Group:    GroupRestricted,
		Type:     TypeInt,
		EnvVar:   "GATED_FETCH_GLOBAL_MONTHLY_CAP",
		Default:  -1,
		Min:      bound(-1),
		Ceiling:  bound(CeilingGatedGlobalMonthlyCap),
		Approval: ApprovalNever,
		Label:    "Special downloads a month, bot-wide",
		Impact:   "-1: no cap (only the dollar budgets stop the paid ones); 0 turns special downloads off for everyone, the home relay included (Shorts then use the server's yt-dlp).",
	},
	{
		Key:      KeyGatedUserMonthlyAllowance,
		Group:    GroupRestricted,
		Type:     TypeInt,
		EnvVar:   "GATED_FETCH_USER_MONTHLY_ALLOWANCE",
		Default:  20,
		Min:      bound(0),
		Ceiling:  bound(CeilingGatedMonthlyQuota),
		Approval: ApprovalNever,
		Label:    "Special downloads a month per person (private chats)",
		Impact:   "The default monthly allowance in private chats; shown in the bot's description and /info.",
	},
	{
		Key:      KeyGatedChatMonthlyCap,
		Group:    GroupRestricted,
		Type:     TypeInt,
		EnvVar:   "GATED_FETCH_CHAT_MONTHLY_CAP",
		Default:  20,
		Min:      bound(0),
		Ceiling:  bound(CeilingGatedMonthlyQuota),
		Approval: ApprovalNever,
		Label:    "Special downloads a month per group",
		Impact:   "The default monthly cap of a group; shown in the bot's description and /info.",
	},
	{
		Key:       KeyStarsEnabled,
		Group:     GroupStars,
		Type:      TypeBool,
		EnvVar:    "STARS_SUPPORT_ENABLED",
		Default:   false,
		Approval:  ApprovalNever,
		SafeValue: false,
		Label:     "Telegram Stars support",
		Impact:    "On: /donate sells Stars support tiers and the Stars commands join the menu. Off: no new payments; bonus downloads already paid keep working.",
	},
	{
		Key:      KeyDefaultDailyMediaLimit,
		Group:    GroupLimits,
		Type:     TypeInt,
		EnvVar:   "DEFAULT_DAILY_MEDIA_LIMIT",
		Default:  0,
		Min:      bound(0),
		Max:      bound(1000),
		Approval: ApprovalNever,
		Label:    "Default daily media limit per person",
		Impact:   "Media a person gets a day where no group admin set a limit; 0 means no limit.",
	},
}

// platformSetting is the on/off switch of a platform.
func platformSetting(key string, envVar string, label string, impact string) Setting {
	result := Setting{
		Key:      key,
		Group:    GroupPlatforms,
		Type:     TypeBool,
		EnvVar:   envVar,
		Default:  true,
		Approval: ApprovalNever,
		Label:    label,
		Impact:   impact,
	}

	return result
}

// proxySetting is a platform's use of the proxy pool, effective only while
// proxies are on.
func proxySetting(key string, envVar string, defaultValue bool, label string) Setting {
	result := Setting{
		Key:      key,
		Group:    GroupProxies,
		Type:     TypeBool,
		EnvVar:   envVar,
		Default:  defaultValue,
		Approval: ApprovalNever,
		Label:    label,
		Impact:   "Off: this platform never uses the proxy pool. Has an effect only while Proxies is on.",
	}

	return result
}

// hostSetting is the host of a free extraction service. The bot fetches from
// it and sends what comes back to every chat, so every change waits for the
// owner's approval.
func hostSetting(key string, envVar string, defaultHost string, label string) Setting {
	result := Setting{
		Key:      key,
		Group:    GroupProxies,
		Type:     TypeHost,
		EnvVar:   envVar,
		Default:  defaultHost,
		Approval: ApprovalAlways,
		Label:    label,
		Impact:   "The bot fetches media from this host and sends it to every chat: a hostname only, no scheme, port, path or IP address.",
	}

	return result
}
