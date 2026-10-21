// Package settings is the registry of the bot settings the owner manages
// from the dashboard: key, type, range, hard ceiling, .env fallback, code
// default, approval rule and the texts the owner sees. It is pure data plus
// stdlib validation: no I/O, no state to change and no imports beyond the
// standard library, so it opens no code path between the bot and the
// dashboard. The bot validates every value with it when it loads or applies
// one, and its copy is the one that counts.
package settings

// ValueType is the JSON shape of a setting's value.
type ValueType string

// Value types.
const (
	TypeBool        ValueType = "bool"
	TypeInt         ValueType = "int"
	TypeDecimal     ValueType = "decimal"
	TypeEnum        ValueType = "enum"
	TypeHost        ValueType = "host"
	TypeOrderedList ValueType = "ordered_list"
)

// Group is the Settings tab section a setting is shown in.
type Group string

// Groups, in the order the Settings tab shows them.
const (
	GroupPlatforms  Group = "platforms"
	GroupServices   Group = "services"
	GroupPaid       Group = "paid"
	GroupRestricted Group = "restricted_posts"
	GroupStars      Group = "stars"
	GroupLimits     Group = "limits"
)

// ApprovalRule says which changes of a setting wait for the owner's Approve
// in Telegram; every other change applies directly.
type ApprovalRule string

// Approval rules.
const (
	ApprovalNever  ApprovalRule = "never"
	ApprovalRaise  ApprovalRule = "raise"
	ApprovalLower  ApprovalRule = "lower"
	ApprovalToTrue ApprovalRule = "to_true"
	ApprovalAlways ApprovalRule = "always"
)

// Sources of an effective value: an owner-set value from the database, the
// same value from the bot's cache file while the database was unavailable,
// the safe value of a money or payment key whose stored value is no longer
// valid, the .env value, or the code default.
const (
	SourceDB      = "db"
	SourceCache   = "cache"
	SourceSafe    = "safe"
	SourceEnv     = "env"
	SourceDefault = "default"
)

// Actors of a bot_settings write (bot_settings.updated_by, settings_audit.actor).
const (
	ActorDashboard = "dashboard"
	ActorTelegram  = "telegram"
	ActorBot       = "bot"
	ActorManual    = "manual"
)

// Kinds of a change request (settings_change_requests.kind).
const KindSetting = "setting"

// Statuses of a change request (settings_change_requests.status). Queued
// waits for the bot; awaiting_approval waits for the owner's Approve in
// Telegram; applied, rejected and expired are final.
const (
	StatusQueued           = "queued"
	StatusAwaitingApproval = "awaiting_approval"
	StatusApplied          = "applied"
	StatusRejected         = "rejected"
	StatusExpired          = "expired"
)

// Platform is one supported platform, in the order the bot lists them.
type Platform struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Platform ids.
const (
	PlatformInstagram = "instagram"
	PlatformTikTok    = "tiktok"
	PlatformTwitter   = "twitter"
	PlatformYouTube   = "youtube"
	PlatformReddit    = "reddit"
	PlatformFacebook  = "facebook"
	Platform9GAG      = "9gag"
	PlatformThreads   = "threads"
)

// Paid Instagram providers (paid.instagram.provider_order).
const (
	ProviderApify = "apify"
	ProviderHiker = "hiker"
)

// Instagram fixed-link services (platform.instagram.fixed_link_service).
const (
	FixedLinkToInstagram = "toinstagram"
	FixedLinkOnebigram   = "onebigram"
)

// Setting keys.
const (
	KeyPlatformInstagramEnabled  = "platform.instagram.enabled"
	KeyPlatformTikTokEnabled     = "platform.tiktok.enabled"
	KeyPlatformTwitterEnabled    = "platform.twitter.enabled"
	KeyPlatformYouTubeEnabled    = "platform.youtube.enabled"
	KeyPlatformRedditEnabled     = "platform.reddit.enabled"
	KeyPlatformFacebookEnabled   = "platform.facebook.enabled"
	KeyPlatform9GAGEnabled       = "platform.9gag.enabled"
	KeyPlatformThreadsEnabled    = "platform.threads.enabled"
	KeyInstagramFixedLinkService = "platform.instagram.fixed_link_service"
	KeyServiceKKInstagramHost    = "service.kkinstagram.host"
	KeyServiceKKTikTokHost       = "service.kktiktok.host"
	KeyServiceVxredditHost       = "service.vxreddit.host"
	KeyServiceFacebedHost        = "service.facebed.host"
	KeyApifyMonthlyBudgetUsd     = "paid.apify.monthly_budget_usd"
	KeyHikerMonthlyBudgetUsd     = "paid.hiker.monthly_budget_usd"
	KeyHikerPricePerRequestUsd   = "paid.hiker.price_per_request_usd"
	KeyInstagramProviderOrder    = "paid.instagram.provider_order"
	KeyHikerLowBalanceRequests   = "alerts.hiker_low_balance_requests"
	KeyGatedGlobalMonthlyCap     = "gated.global_monthly_cap"
	KeyGatedUserMonthlyAllowance = "gated.user_monthly_allowance"
	KeyGatedChatMonthlyCap       = "gated.chat_monthly_cap"
	KeyStarsEnabled              = "stars.enabled"
	KeyDefaultDailyMediaLimit    = "limits.default_daily_media"
)

// Hard ceilings: going above them needs a code change and a deploy, never
// only access to the dashboard. They apply to stored values only, so .env
// values keep working as before.
const (
	CeilingApifyMonthlyBudgetUsd = 20.0
	CeilingHikerMonthlyBudgetUsd = 10.0
	CeilingGatedGlobalMonthlyCap = 5000.0
	CeilingGatedMonthlyQuota     = 1000.0
)

// Setting is one managed setting. Default is the value the bot uses when
// neither the database nor .env has one; it matches the .env getter's own
// default. SafeValue, when set, replaces a stored value that no longer
// validates for a money or payment key, so a narrowed range can never turn
// spending back on through the .env fallback.
type Setting struct {
	Key          string       `json:"key"`
	Group        Group        `json:"group"`
	Type         ValueType    `json:"type"`
	EnvVar       string       `json:"env_var,omitempty"`
	Default      any          `json:"default"`
	Min          *float64     `json:"min,omitempty"`
	MinExclusive bool         `json:"min_exclusive,omitempty"`
	Max          *float64     `json:"max,omitempty"`
	Ceiling      *float64     `json:"ceiling,omitempty"`
	Scale        int          `json:"scale,omitempty"`
	Enum         []string     `json:"enum,omitempty"`
	Approval     ApprovalRule `json:"approval"`
	SafeValue    any          `json:"safe_value,omitempty"`
	Label        string       `json:"label"`
	Impact       string       `json:"impact"`
}

// bound returns a pointer to value, for the optional range fields.
func bound(value float64) *float64 {
	result := &value

	return result
}

// platforms lists the supported platforms in the order the bot lists them.
var platforms = []Platform{
	{ID: PlatformInstagram, Label: "Instagram"},
	{ID: PlatformTikTok, Label: "TikTok"},
	{ID: PlatformTwitter, Label: "Twitter/X"},
	{ID: PlatformYouTube, Label: "YouTube"},
	{ID: PlatformReddit, Label: "Reddit"},
	{ID: PlatformFacebook, Label: "Facebook"},
	{ID: Platform9GAG, Label: "9GAG"},
	{ID: PlatformThreads, Label: "Threads"},
}

// platformEnabledKeys maps a platform id to its on/off key.
var platformEnabledKeys = map[string]string{
	PlatformInstagram: KeyPlatformInstagramEnabled,
	PlatformTikTok:    KeyPlatformTikTokEnabled,
	PlatformTwitter:   KeyPlatformTwitterEnabled,
	PlatformYouTube:   KeyPlatformYouTubeEnabled,
	PlatformReddit:    KeyPlatformRedditEnabled,
	PlatformFacebook:  KeyPlatformFacebookEnabled,
	Platform9GAG:      KeyPlatform9GAGEnabled,
	PlatformThreads:   KeyPlatformThreadsEnabled,
}

// Platforms returns the supported platforms in the order the bot lists them.
func Platforms() []Platform {
	result := make([]Platform, len(platforms))
	copy(result, platforms)

	return result
}

// PlatformIDs returns the ids of Platforms, in the same order.
func PlatformIDs() []string {
	result := make([]string, 0, len(platforms))

	for _, platform := range platforms {
		result = append(result, platform.ID)
	}

	return result
}

// PlatformEnabledKey returns the on/off key of a platform.
func PlatformEnabledKey(platformID string) (string, bool) {
	result, isKnown := platformEnabledKeys[platformID]

	return result, isKnown
}

// All returns every managed setting in the order the Settings tab shows them.
func All() []Setting {
	result := make([]Setting, 0, len(registry))

	for _, setting := range registry {
		copied := setting.clone()
		result = append(result, copied)
	}

	return result
}

// Lookup returns the setting of a key.
func Lookup(key string) (Setting, bool) {
	result := Setting{}
	isKnown := false

	for _, setting := range registry {
		if setting.Key == key {
			result = setting.clone()
			isKnown = true
		}
	}

	return result, isKnown
}

// clone copies a setting, so callers can not change the registry.
func (s Setting) clone() Setting {
	result := s
	result.Enum = append([]string(nil), s.Enum...)
	result.Default = cloneValue(s.Default)
	result.SafeValue = cloneValue(s.SafeValue)

	return result
}

// cloneValue copies the list values; the others are immutable.
func cloneValue(value any) any {
	result := value
	list, isList := value.([]string)

	if isList == true {
		result = append([]string(nil), list...)
	}

	return result
}
