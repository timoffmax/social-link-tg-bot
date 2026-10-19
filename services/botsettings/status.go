package botsettings

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"time"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// bot_runtime_status components.
const (
	ComponentBot      = "bot"
	ComponentSecrets  = "secrets"
	ComponentSettings = "settings"
	ComponentRegistry = "registry"
	ComponentProfile  = "profile"
	ComponentPaid     = "paid"
	ComponentRelay    = "relay"
)

// Build settings go build stamps into the binary (debug.ReadBuildInfo).
const (
	buildSettingRevision = "vcs.revision"
	buildSettingModified = "vcs.modified"
	buildSettingTrue     = "true"
)

// botStatus is the 'bot' component: the heartbeat.
type botStatus struct {
	StartedAt        time.Time `json:"started_at"`
	HeartbeatAt      time.Time `json:"heartbeat_at"`
	Commit           string    `json:"commit,omitempty"`
	IsCommitModified bool      `json:"commit_modified,omitempty"`
	SettingsLoadedAt time.Time `json:"settings_loaded_at"`
	SettingsSource   string    `json:"settings_source"`
	SettingsCount    int       `json:"settings_count"`
	AppliesRequests  bool      `json:"applies_requests"`
}

// secretsStatus is the 'secrets' component: which secrets are set, never
// their values.
type secretsStatus struct {
	HasBotToken      bool `json:"tg_bot_token"`
	HasAdminUserID   bool `json:"admin_user_id"`
	HasApifyToken    bool `json:"apify_token"`
	HasHikerToken    bool `json:"hiker_token"`
	ProxyCount       int  `json:"proxies"`
	HasRelayURL      bool `json:"relay_url"`
	HasRelayTokenRef bool `json:"relay_token_file"`
}

// registryStatus is the 'registry' component: the settings metadata, so the
// dashboard shows labels, ranges and approval rules without a copy of its
// own.
type registryStatus struct {
	Groups       []settings.Group           `json:"groups"`
	Platforms    []settings.Platform        `json:"platforms"`
	Settings     []settings.Setting         `json:"settings"`
	SpecialLimit settings.SpecialLimitRules `json:"special_limit"`
}

// registryGroups is the order of the Settings tab's sections.
var registryGroups = []settings.Group{
	settings.GroupPlatforms,
	settings.GroupProxies,
	settings.GroupPaid,
	settings.GroupRestricted,
	settings.GroupStars,
	settings.GroupLimits,
}

// publishAll publishes every component (at start).
func (m *Manager) publishAll(ctx context.Context) {
	registry := registryStatus{
		Groups:       registryGroups,
		Platforms:    settings.Platforms(),
		Settings:     settings.All(),
		SpecialLimit: settings.SpecialLimits(),
	}
	m.publish(ctx, ComponentRegistry, registry)
	m.publishHeartbeat(ctx)
	m.publishProfile(ctx)
}

// publishHeartbeat publishes the heartbeat, the secrets, the settings and
// the last computed paid-services and relay states (every 60 s).
func (m *Manager) publishHeartbeat(ctx context.Context) {
	bot := m.botStatus()
	m.publish(ctx, ComponentBot, bot)
	secrets := currentSecrets()
	m.publish(ctx, ComponentSecrets, secrets)
	m.publishSettings(ctx)
	m.publishComputed(ctx, ComponentPaid, m.paidStatus)
	m.publishComputed(ctx, ComponentRelay, m.relayStatus)
}

// publishComputed publishes a state another goroutine computed; a nil
// reader or a nil state publishes nothing.
func (m *Manager) publishComputed(ctx context.Context, component string, read func() any) {
	var state any

	if read != nil {
		state = read()
	}

	if state != nil {
		m.publish(ctx, component, state)
	}
}

// publishSettings publishes the effective value and source of every key.
func (m *Manager) publishSettings(ctx context.Context) {
	effective := effectiveSettings()
	m.publish(ctx, ComponentSettings, effective)
}

// publishProfile publishes the last profile push.
func (m *Manager) publishProfile(ctx context.Context) {
	profile := m.profile.status()
	m.publish(ctx, ComponentProfile, profile)
}

// publish upserts one component; without a database it does nothing. A
// failure is logged with the outage, not every time.
func (m *Manager) publish(ctx context.Context, component string, data any) {
	encoded, err := json.Marshal(data)

	if err != nil {
		log.Printf("⚠️ Could not encode the %s status: %v", component, err)
	}

	if (err == nil) && (m.store != nil) {
		publishCtx, cancel := context.WithTimeout(ctx, statusPublishWait)
		err = m.store.PublishStatus(publishCtx, component, encoded)
		cancel()
		m.recordDBResult(err, "publish the bot status")
	}
}

// botStatus builds the heartbeat.
func (m *Manager) botStatus() botStatus {
	commit, isModified := buildCommit()
	snapshotCount := len(m.stored)
	result := botStatus{
		StartedAt:        m.startedAt,
		HeartbeatAt:      m.now(),
		Commit:           commit,
		IsCommitModified: isModified,
		SettingsLoadedAt: m.loadedAt,
		SettingsSource:   m.origin,
		SettingsCount:    snapshotCount,
		AppliesRequests:  m.applyRequests,
	}

	return result
}

// buildCommit reads the git commit go build stamped into the binary.
func buildCommit() (string, bool) {
	commit := ""
	isModified := false
	info, isRead := debug.ReadBuildInfo()

	if isRead == true {
		commit, isModified = commitFromBuildSettings(info.Settings)
	}

	return commit, isModified
}

// commitFromBuildSettings picks vcs.revision and vcs.modified.
func commitFromBuildSettings(buildSettings []debug.BuildSetting) (string, bool) {
	commit := ""
	isModified := false

	for _, setting := range buildSettings {
		if setting.Key == buildSettingRevision {
			commit = setting.Value
		}

		if setting.Key == buildSettingModified {
			isModified = (setting.Value == buildSettingTrue)
		}
	}

	return commit, isModified
}

// currentSecrets reports which secrets are set.
func currentSecrets() secretsStatus {
	relayURL, _ := config.GetRelayURL()
	proxies := config.GetYtDlpProxies()
	result := secretsStatus{
		HasBotToken:      (config.GetTgBotToken() != ""),
		HasAdminUserID:   (config.GetAdminUserID() != 0),
		HasApifyToken:    (config.GetApifyToken() != ""),
		HasHikerToken:    (config.GetHikerAPIToken() != ""),
		ProxyCount:       len(proxies),
		HasRelayURL:      (relayURL != ""),
		HasRelayTokenRef: (config.GetRelayTokenFile() != ""),
	}

	return result
}

// effectiveSettings returns what the bot uses for every key. A value JSON
// can not hold (an .env budget of "Inf") is published as text.
func effectiveSettings() map[string]config.EffectiveSetting {
	result := map[string]config.EffectiveSetting{}

	for _, setting := range settings.All() {
		effective, _ := config.GetEffectiveSetting(setting.Key)
		_, err := json.Marshal(effective.Value)

		if err != nil {
			effective.Value = fmt.Sprintf("%v", effective.Value)
		}

		result[setting.Key] = effective
	}

	return result
}
