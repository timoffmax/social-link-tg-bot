package services

// The 'services' bot_runtime_status component: external services' health from
// the outage tracker, built from memory only (docs/settings.md).

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// servicesStatusVersion is the version of the component's JSON shape.
const servicesStatusVersion = 1

// serviceFailureAgeOut is how long a traffic failure keeps a row degraded
// or down; an older one is shown as stale.
const serviceFailureAgeOut = 24 * time.Hour

// Service kinds.
const (
	serviceKindFree  = "free"
	serviceKindPaid  = "paid"
	serviceKindRelay = "relay"
	serviceKindProxy = "proxy"
)

// Row and signal states.
const (
	ServiceStateOK       = "ok"
	ServiceStateDegraded = "degraded"
	ServiceStateDown     = "down"
	ServiceStateStale    = "stale"
	ServiceStateIdle     = "idle"
	ServiceStateUnknown  = "unknown"
	ServiceStateOff      = "off"
)

// Why a row is off.
const (
	serviceOffPlatform      = "platform_off"
	serviceOffNotConfigured = "not_configured"
	serviceOffNotListed     = "not_listed"
	serviceOffProxies       = "proxies_off"
	serviceOffSpecial       = "special_off"
	serviceOffRelayDisabled = "relay_disabled"
	serviceOffBudgetPaused  = "budget_paused"
)

// serviceProviderRelay marks a relay row's provider (its not_configured rule).
const serviceProviderRelay = "relay"

// serviceOffReasonOrder is the precedence of the off reasons.
var serviceOffReasonOrder = []string{
	serviceOffPlatform,
	serviceOffNotConfigured,
	serviceOffNotListed,
	serviceOffProxies,
	serviceOffSpecial,
	serviceOffRelayDisabled,
	serviceOffBudgetPaused,
}

// serviceSignalSeverity orders the signal states from mild to severe: the
// worst present one gives the row its state.
var serviceSignalSeverity = []string{ServiceStateStale, ServiceStateOK, ServiceStateDegraded, ServiceStateDown}

// Clocks of the component, variables for tests.
var (
	serviceHealthNow       = time.Now
	serviceHealthStartedAt = time.Now()
)

// servicesRestoredFrom is the computed_at of the row seeded at start.
var servicesRestoredFrom atomic.Pointer[time.Time]

// serviceSpecialDownloadsOn reads whether special downloads are on (a
// variable for tests).
var serviceSpecialDownloadsOn = IsSpecialDownloadsOn

// ServicesStatus is the 'services' component.
type ServicesStatus struct {
	Version      int                `json:"version"`
	StartedAt    time.Time          `json:"started_at"`
	ComputedAt   time.Time          `json:"computed_at"`
	RestoredFrom *time.Time         `json:"restored_from"`
	Services     []ServiceStatusRow `json:"services"`
}

// ServiceStatusRow is one service of the component.
type ServiceStatusRow struct {
	Key        string         `json:"key"`
	Label      string         `json:"label"`
	Kind       string         `json:"kind"`
	Host       *string        `json:"host"`
	Platforms  []string       `json:"platforms"`
	Role       string         `json:"role"`
	IsFallback bool           `json:"fallback"`
	HasTraffic bool           `json:"has_traffic"`
	HasProbe   bool           `json:"has_probe"`
	State      string         `json:"state"`
	OffReason  *string        `json:"off_reason"`
	Traffic    *ServiceSignal `json:"traffic"`
	Probe      *ServiceSignal `json:"probe"`
}

// ServiceSignal is the traffic or the probe of a service.
type ServiceSignal struct {
	State               string     `json:"state"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastFailureAt       *time.Time `json:"last_failure_at"`
	FailingSince        *time.Time `json:"failing_since"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	Threshold           int        `json:"threshold"`
	LastErrorCode       *string    `json:"last_error_code"`
}

// serviceCatalogEntry is one published service; offRules lists the off
// reasons that apply to it, provider and paidFeature parametrise their checks.
type serviceCatalogEntry struct {
	Key         string
	Label       string
	Kind        string
	Role        string
	Platforms   []string
	IsFallback  bool
	TrafficKey  string
	ProbeKey    string
	host        func() string
	offRules    []string
	provider    string
	paidFeature string
}

// Outage-tracker keys of the catalog.
const (
	kkInstagramProbeKey    = kkInstagramServiceName + serviceProbeSuffix
	kkTikTokProbeKey       = kkTikTokServiceName + serviceProbeSuffix
	vxredditProbeKey       = redditVxredditServiceName + serviceProbeSuffix
	facebedProbeKey        = facebedServiceName + serviceProbeSuffix
	relayInstagramKey      = relayPlatformInstagram + relayServiceSuffix
	relayYouTubeKey        = relayPlatformYouTube + relayServiceSuffix
	relayInstagramProbeKey = relayInstagramKey + serviceProbeSuffix
	relayYouTubeProbeKey   = relayYouTubeKey + serviceProbeSuffix
)

// serviceCatalog is every published service, in publish order.
var serviceCatalog = []serviceCatalogEntry{
	{
		Key:        kkInstagramServiceName,
		Label:      "kkinstagram",
		Kind:       serviceKindFree,
		Role:       "Posts and reels (first step)",
		Platforms:  []string{settings.PlatformInstagram},
		TrafficKey: kkInstagramServiceName,
		ProbeKey:   kkInstagramProbeKey,
		host:       config.GetKKInstagramHost,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:         apifyServiceName,
		Label:       "Apify gated posts",
		Kind:        serviceKindPaid,
		Role:        "Author-gated posts and login-only reel videos (special download)",
		Platforms:   []string{settings.PlatformInstagram},
		IsFallback:  true,
		TrafficKey:  apifyServiceName,
		offRules:    []string{serviceOffPlatform, serviceOffNotConfigured, serviceOffNotListed, serviceOffSpecial, serviceOffBudgetPaused},
		provider:    PaidProviderApify,
		paidFeature: PaidFeaturePosts,
	},
	{
		Key:         storiesApifyServiceName,
		Label:       "Apify Stories",
		Kind:        serviceKindPaid,
		Role:        "Stories (special download)",
		Platforms:   []string{settings.PlatformInstagram},
		TrafficKey:  storiesApifyServiceName,
		offRules:    []string{serviceOffPlatform, serviceOffNotConfigured, serviceOffNotListed, serviceOffSpecial, serviceOffBudgetPaused},
		provider:    PaidProviderApify,
		paidFeature: PaidFeatureStories,
	},
	{
		Key:        hikerServiceName,
		Label:      "HikerAPI",
		Kind:       serviceKindPaid,
		Role:       "Backup for gated posts and Stories",
		Platforms:  []string{settings.PlatformInstagram},
		IsFallback: true,
		TrafficKey: hikerServiceName,
		offRules:   []string{serviceOffPlatform, serviceOffNotConfigured, serviceOffNotListed, serviceOffSpecial, serviceOffBudgetPaused},
		provider:   PaidProviderHiker,
	},
	{
		Key:        relayInstagramKey,
		Label:      "Home relay (Instagram)",
		Kind:       serviceKindRelay,
		Role:       "Login-only reel videos from a home IP (details on the Limits tab)",
		Platforms:  []string{settings.PlatformInstagram},
		IsFallback: true,
		TrafficKey: relayInstagramKey,
		ProbeKey:   relayInstagramProbeKey,
		offRules:   []string{serviceOffPlatform, serviceOffNotConfigured, serviceOffSpecial, serviceOffRelayDisabled},
		provider:   serviceProviderRelay,
	},
	{
		Key:        kkTikTokServiceName,
		Label:      "kktiktok",
		Kind:       serviceKindFree,
		Role:       "Videos after tikwm fails",
		Platforms:  []string{settings.PlatformTikTok},
		IsFallback: true,
		TrafficKey: kkTikTokServiceName,
		ProbeKey:   kkTikTokProbeKey,
		host:       config.GetKKTikTokHost,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        tikwmServiceName,
		Label:      "tikwm",
		Kind:       serviceKindFree,
		Role:       "Videos and slideshows after yt-dlp and TikTok's own pages fail",
		Platforms:  []string{settings.PlatformTikTok},
		IsFallback: true,
		TrafficKey: tikwmServiceName,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        fxTwitterServiceName,
		Label:      "FxTwitter",
		Kind:       serviceKindFree,
		Role:       "Tweet text, author and media (first step)",
		Platforms:  []string{settings.PlatformTwitter},
		TrafficKey: fxTwitterServiceName,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        relayYouTubeKey,
		Label:      "Home relay (YouTube)",
		Kind:       serviceKindRelay,
		Role:       "Shorts from a home IP (details on the Limits tab)",
		Platforms:  []string{settings.PlatformYouTube},
		TrafficKey: relayYouTubeKey,
		ProbeKey:   relayYouTubeProbeKey,
		offRules:   []string{serviceOffPlatform, serviceOffNotConfigured, serviceOffSpecial, serviceOffRelayDisabled},
		provider:   serviceProviderRelay,
	},
	{
		Key:         youtubeApifyServiceName,
		Label:       "Apify Shorts",
		Kind:        serviceKindPaid,
		Role:        "Shorts after the home relay or yt-dlp (special download)",
		Platforms:   []string{settings.PlatformYouTube},
		IsFallback:  true,
		TrafficKey:  youtubeApifyServiceName,
		offRules:    []string{serviceOffPlatform, serviceOffNotConfigured, serviceOffSpecial, serviceOffBudgetPaused},
		provider:    PaidProviderApify,
		paidFeature: PaidFeatureShorts,
	},
	{
		Key:        youtubePostServiceName,
		Label:      "Community posts",
		Kind:       serviceKindFree,
		Role:       "YouTube community posts (page data)",
		Platforms:  []string{settings.PlatformYouTube},
		TrafficKey: youtubePostServiceName,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        redditVxredditServiceName,
		Label:      "vxreddit",
		Kind:       serviceKindFree,
		Role:       "Videos, galleries, GIFs, text and link posts (first step)",
		Platforms:  []string{settings.PlatformReddit},
		TrafficKey: redditVxredditServiceName,
		ProbeKey:   vxredditProbeKey,
		host:       config.GetVxredditHost,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        redditProxyServiceName,
		Label:      "Reddit API via proxies",
		Kind:       serviceKindProxy,
		Role:       "Reddit JSON through the proxy pool after vxreddit",
		Platforms:  []string{settings.PlatformReddit},
		IsFallback: true,
		TrafficKey: redditProxyServiceName,
		offRules:   []string{serviceOffPlatform, serviceOffProxies},
	},
	{
		Key:        facebedServiceName,
		Label:      "facebed",
		Kind:       serviceKindFree,
		Role:       "Photos and text posts; video after yt-dlp",
		Platforms:  []string{settings.PlatformFacebook},
		TrafficKey: facebedServiceName,
		ProbeKey:   facebedProbeKey,
		host:       config.GetFacebedHost,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        ninegagAPIServiceName,
		Label:      "9GAG API helper",
		Kind:       serviceKindFree,
		Role:       "Photos and articles after yt-dlp",
		Platforms:  []string{settings.Platform9GAG},
		IsFallback: true,
		TrafficKey: ninegagAPIServiceName,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        threadsEmbedServiceName,
		Label:      "Threads embed",
		Kind:       serviceKindFree,
		Role:       "Posts from Meta's embed page (first step)",
		Platforms:  []string{settings.PlatformThreads},
		TrafficKey: threadsEmbedServiceName,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:        vxthreadsServiceName,
		Label:      "vxthreads",
		Kind:       serviceKindFree,
		Role:       "Posts from the mirror after the embed fails",
		Platforms:  []string{settings.PlatformThreads},
		IsFallback: true,
		TrafficKey: vxthreadsServiceName,
		offRules:   []string{serviceOffPlatform},
	},
	{
		Key:       proxyPoolServiceName,
		Label:     "Proxy pool",
		Kind:      serviceKindProxy,
		Role:      "yt-dlp retries and the Reddit API through YTDLP_PROXIES",
		Platforms: []string{settings.PlatformInstagram, settings.PlatformTikTok, settings.PlatformTwitter, settings.PlatformReddit, settings.PlatformFacebook, settings.Platform9GAG},
		ProbeKey:  proxyPoolServiceName,
		offRules:  []string{serviceOffProxies},
	},
}

// serviceOffInputs holds the values read once per build for the off checks.
type serviceOffInputs struct {
	paidProviderOrder []string
}

// serviceOffChecks tells whether an off reason applies to a catalog entry;
// every check reads memory only.
var serviceOffChecks = map[string]func(entry serviceCatalogEntry, inputs serviceOffInputs) bool{
	serviceOffPlatform:      isServicePlatformOff,
	serviceOffNotConfigured: isServiceNotConfigured,
	serviceOffNotListed:     isServiceNotListed,
	serviceOffProxies:       areServiceProxiesOff,
	serviceOffSpecial:       areServiceSpecialDownloadsOff,
	serviceOffRelayDisabled: isServiceRelayDisabled,
	serviceOffBudgetPaused:  isServiceBudgetPaused,
}

// CurrentServicesStatus builds the component from the tracker and the
// in-memory switches; it never blocks on I/O and is never nil.
func CurrentServicesStatus() any {
	result := buildServicesStatus()

	return result
}

// buildServicesStatus builds the component.
func buildServicesStatus() ServicesStatus {
	snapshot := ServiceHealthSnapshot()
	now := serviceHealthNow()
	rows := make([]ServiceStatusRow, 0, len(serviceCatalog))
	inputs := serviceOffInputs{paidProviderOrder: quietInstagramPaidProviderOrder()}

	for _, entry := range serviceCatalog {
		row := buildServiceRow(entry, inputs, snapshot, now)
		rows = append(rows, row)
	}

	result := ServicesStatus{
		Version:      servicesStatusVersion,
		StartedAt:    serviceHealthStartedAt.UTC(),
		ComputedAt:   now.UTC(),
		RestoredFrom: servicesRestoredFrom.Load(),
		Services:     rows,
	}

	return result
}

// buildServiceRow builds one row: its signals, off reason and state.
func buildServiceRow(entry serviceCatalogEntry, inputs serviceOffInputs, snapshot map[string]ServiceHealth, now time.Time) ServiceStatusRow {
	result := ServiceStatusRow{
		Key:        entry.Key,
		Label:      entry.Label,
		Kind:       entry.Kind,
		Platforms:  entry.Platforms,
		Role:       entry.Role,
		IsFallback: entry.IsFallback,
		HasTraffic: (entry.TrafficKey != ""),
		HasProbe:   (entry.ProbeKey != ""),
	}

	if entry.host != nil {
		host := entry.host()
		result.Host = &host
	}

	offReason := serviceOffReason(entry, inputs)

	if offReason != "" {
		result.OffReason = &offReason
	}

	result.Traffic = buildServiceSignal(snapshot, entry.TrafficKey, true, now)
	result.Probe = buildServiceSignal(snapshot, entry.ProbeKey, false, now)
	result.State = serviceState(result)

	return result
}

// serviceOffReason returns the first off reason of entry that applies, in
// serviceOffReasonOrder, or "".
func serviceOffReason(entry serviceCatalogEntry, inputs serviceOffInputs) string {
	result := ""

	for _, reason := range serviceOffReasonOrder {
		isCandidate := (result == "") && (slices.Contains(entry.offRules, reason) == true)

		if isCandidate == true {
			result = serviceOffReasonIfApplies(entry, inputs, reason)
		}
	}

	return result
}

// serviceOffReasonIfApplies returns reason when its check holds for entry,
// else "".
func serviceOffReasonIfApplies(entry serviceCatalogEntry, inputs serviceOffInputs, reason string) string {
	result := ""
	check := serviceOffChecks[reason]
	isOff := check(entry, inputs)

	if isOff == true {
		result = reason
	}

	return result
}

// buildServiceSignal turns the tracker entry of key into a signal; nil
// without a key or an entry.
func buildServiceSignal(snapshot map[string]ServiceHealth, key string, isTraffic bool, now time.Time) *ServiceSignal {
	var result *ServiceSignal
	health, isFound := snapshot[key]

	if (key != "") && (isFound == true) {
		result = &ServiceSignal{
			State:               signalState(health, isTraffic, now),
			LastSuccessAt:       serviceTime(health.LastSuccessAt),
			LastFailureAt:       serviceTime(health.LastFailureAt),
			FailingSince:        serviceTime(health.FailingSince),
			ConsecutiveFailures: health.ConsecutiveFailures,
			Threshold:           serviceThreshold(health.Threshold),
		}
	}

	if (result != nil) && (health.LastErrorCode != "") {
		code := health.LastErrorCode
		result.LastErrorCode = &code
	}

	return result
}

// signalState is ok without a streak, stale for aged-out traffic, down at the
// alert threshold, else degraded.
func signalState(health ServiceHealth, isTraffic bool, now time.Time) string {
	result := ServiceStateDegraded
	age := now.Sub(health.LastFailureAt)
	isAgedOut := (isTraffic == true) && (age > serviceFailureAgeOut)
	threshold := serviceThreshold(health.Threshold)

	if health.ConsecutiveFailures >= threshold {
		result = ServiceStateDown
	}

	if isAgedOut == true {
		result = ServiceStateStale
	}

	if health.ConsecutiveFailures == 0 {
		result = ServiceStateOK
	}

	return result
}

// serviceState is a row's state: off first, then the worst present signal,
// then stale traffic, then idle (a fallback) or unknown without any signal.
func serviceState(row ServiceStatusRow) string {
	states := []string{}

	if row.Traffic != nil {
		states = append(states, row.Traffic.State)
	}

	if row.Probe != nil {
		states = append(states, row.Probe.State)
	}

	result := ServiceStateUnknown

	if row.IsFallback == true {
		result = ServiceStateIdle
	}

	for _, state := range serviceSignalSeverity {
		if slices.Contains(states, state) == true {
			result = state
		}
	}

	if row.OffReason != nil {
		result = ServiceStateOff
	}

	return result
}

// serviceThreshold returns threshold, or the default before any failure.
func serviceThreshold(threshold int) int {
	result := threshold

	if result < 1 {
		result = outageAlertThreshold
	}

	return result
}

// serviceTime returns t in UTC, nil when zero.
func serviceTime(t time.Time) *time.Time {
	var result *time.Time

	if t.IsZero() == false {
		utc := t.UTC()
		result = &utc
	}

	return result
}

// isServicePlatformOff reports a single-platform row whose platform is off.
func isServicePlatformOff(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	result := false

	if len(entry.Platforms) == 1 {
		isEnabled := config.IsPlatformEnabled(entry.Platforms[0])
		result = (isEnabled == false)
	}

	return result
}

// isServiceNotConfigured reports a provider without its token, or a relay
// that does not serve the row's platform.
func isServiceNotConfigured(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	result := false

	if entry.provider == PaidProviderApify {
		result = (hasApifyToken() == false)
	}

	if entry.provider == PaidProviderHiker {
		result = (hasHikerToken() == false)
	}

	if entry.provider == serviceProviderRelay {
		isUsable := isRelayUsableFor(entry.Platforms[0])
		result = (isUsable == false)
	}

	return result
}

// isServiceNotListed reports a provider missing from INSTAGRAM_PAID_PROVIDERS.
func isServiceNotListed(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	result := (slices.Contains(inputs.paidProviderOrder, entry.provider) == false)

	return result
}

// areServiceProxiesOff reports proxies switched off or none configured.
func areServiceProxiesOff(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	isEnabled := config.IsProxyEnabled()
	proxies := config.GetYtDlpProxies()
	result := (isEnabled == false) || (len(proxies) == 0)

	return result
}

// areServiceSpecialDownloadsOff reports special downloads switched off.
func areServiceSpecialDownloadsOff(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	isOn := serviceSpecialDownloadsOn()
	result := (isOn == false)

	return result
}

// isServiceRelayDisabled reports a platform the relay itself switched off.
func isServiceRelayDisabled(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	result := isRelayPlatformDisabled(entry.Platforms[0])

	return result
}

// isServiceBudgetPaused reports a paid provider its budget paused, from the
// last computed paid state (none computed yet: not paused).
func isServiceBudgetPaused(entry serviceCatalogEntry, inputs serviceOffInputs) bool {
	result := false
	snapshot := paidStatusSnapshot.Load()

	if (snapshot != nil) && (entry.provider == PaidProviderApify) {
		isFeaturePaused := slices.Contains(snapshot.Apify.PausedFeatures, entry.paidFeature)
		result = (snapshot.Apify.State == PaidStatePaused) || (isFeaturePaused == true)
	}

	if (snapshot != nil) && (entry.provider == PaidProviderHiker) {
		result = (snapshot.Hiker.State == PaidStatePaused)
	}

	return result
}

// errServicesStatusVersion rejects a published row of another version.
var errServicesStatusVersion = errors.New("unsupported services status version")

// SeedServicesHealth restores the tracker from the last published 'services'
// row at start, without alerts and without overwriting an existing entry.
func SeedServicesHealth(data json.RawMessage) error {
	published := ServicesStatus{}
	err := json.Unmarshal(data, &published)

	if (err == nil) && (published.Version != servicesStatusVersion) {
		err = fmt.Errorf("%w: %d", errServicesStatusVersion, published.Version)
	}

	if err == nil {
		seedServiceRows(published.Services)
		restoredFrom := published.ComputedAt.UTC()
		servicesRestoredFrom.Store(&restoredFrom)
	}

	return err
}

// seedServiceRows seeds the traffic and probe entries of the catalog rows.
func seedServiceRows(rows []ServiceStatusRow) {
	now := serviceHealthNow()
	catalog := map[string]serviceCatalogEntry{}

	for _, entry := range serviceCatalog {
		catalog[entry.Key] = entry
	}

	serviceHealthMu.Lock()
	defer serviceHealthMu.Unlock()

	for _, row := range rows {
		entry, isKnown := catalog[row.Key]

		if isKnown == true {
			seedServiceEntry(entry.TrafficKey, row.Traffic, now)
			seedServiceEntry(entry.ProbeKey, row.Probe, now)
		}
	}
}

// seedServiceEntry puts one sanitised signal under a key without an entry;
// the caller holds serviceHealthMu.
func seedServiceEntry(key string, signal *ServiceSignal, now time.Time) {
	_, exists := serviceHealthState[key]
	isSeedable := (key != "") && (signal != nil) && (exists == false)
	var entry *serviceHealthEntry

	if isSeedable == true {
		entry = seededServiceEntry(*signal, now)
	}

	if entry != nil {
		serviceHealthState[key] = entry
	}
}

// seededServiceEntry sanitises a published signal into a tracker entry;
// nil when no time is left.
func seededServiceEntry(signal ServiceSignal, now time.Time) *serviceHealthEntry {
	var result *serviceHealthEntry
	failures := max(signal.ConsecutiveFailures, 0)
	threshold := serviceThreshold(signal.Threshold)
	lastSuccessAt := seededServiceTime(signal.LastSuccessAt, now)
	lastFailureAt := seededServiceTime(signal.LastFailureAt, now)
	failingSince := seededServiceTime(signal.FailingSince, now)

	if lastFailureAt.IsZero() == true {
		failures = 0
	}

	if failures == 0 {
		failingSince = time.Time{}
	}

	isEmpty := (lastSuccessAt.IsZero() == true) && (lastFailureAt.IsZero() == true) && (failingSince.IsZero() == true)

	if isEmpty == false {
		result = &serviceHealthEntry{
			consecutiveFailures: failures,
			seededFailures:      failures,
			alerted:             (failures >= threshold),
			lastSuccessAt:       lastSuccessAt,
			lastFailureAt:       lastFailureAt,
			failingSince:        failingSince,
			threshold:           threshold,
		}
	}

	if (result != nil) && (signal.LastErrorCode != nil) {
		result.lastErrorCode = knownServiceErrorCode(*signal.LastErrorCode)
	}

	return result
}

// seededServiceTime returns a published time, zero when it is null or
// after now.
func seededServiceTime(published *time.Time, now time.Time) time.Time {
	result := time.Time{}

	if (published != nil) && (published.After(now) == false) {
		result = *published
	}

	return result
}
