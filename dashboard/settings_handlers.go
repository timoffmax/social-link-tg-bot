package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Settings routes: GET /api/settings and /api/settings/audit are plain read
// routes; /api/settings/requests (POST) and /api/settings/requests/{id}
// (GET) are matched as write routes.
const (
	settingsPath         = "/api/settings"
	settingsAuditPath    = "/api/settings/audit"
	settingRequestsPath  = "/api/settings/requests"
	settingRequestPrefix = "/api/settings/requests/"
)

// Allow headers of the settings request routes.
const (
	allowSettingRequests = "POST"
	allowSettingRequest  = "GET, HEAD"
)

// Query parameters of the audit log.
const (
	paramKey    = "key"
	paramGroup  = "group"
	paramBefore = "before"
)

// Audit paging.
const (
	defaultAuditLimit = 50
	maxAuditLimit     = 100
	beforeIDBase      = 10
	beforeIDBits      = 63
)

// auditLimitParam is the audit page size.
var auditLimitParam = intParam{name: paramLimit, fallback: defaultAuditLimit, min: minLimit, max: maxAuditLimit}

// maxSettingValueBytes is the largest value or expected the request table
// accepts (its pg_column_size checks).
const maxSettingValueBytes = 2048

// heartbeatStaleAfter marks the bot as not running: it publishes its
// heartbeat every minute.
const heartbeatStaleAfter = 3 * time.Minute

// Fields of the published 'bot' component.
const (
	botFieldHeartbeatAt     = "heartbeat_at"
	botFieldAppliesRequests = "applies_requests"
	botFieldIsStale         = "is_stale"
)

// jsonNull is the JSON null literal (a reset).
const jsonNull = "null"

// Validation messages of the settings requests.
const (
	errSettingKeyRequired      = "key is required"
	errSettingUnknownKey       = "unknown setting %q"
	errSettingValueRequired    = "value is required (null resets the setting to .env / the default)"
	errSettingExpectedRequired = "expected is required: send the value the page showed"
	errSettingValueTooLarge    = "%s is too large: at most %d bytes"
	errSettingUnknownGroup     = "unknown group %q"
	errSettingBeforeInvalid    = "invalid before: must be an audit entry id"
	fieldValue                 = "value"
	fieldExpected              = "expected"
)

// settingRequestBody is the body of POST /api/settings/requests; value and
// expected stay raw, so a missing value is told from null.
type settingRequestBody struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Expected json.RawMessage `json:"expected"`
}

// newSettingsRoutes binds the settings handlers of the server.
func (s *Server) newSettingsRoutes() {
	s.routes[settingsPath] = s.handleSettings
	s.routes[settingsAuditPath] = s.handleSettingsAudit
	s.settingRequests = writeRoute{
		handlers: map[string]routeHandler{http.MethodPost: s.handleQueueSettingRequest},
		allow:    allowSettingRequests,
	}
	s.settingRequest = writeRoute{
		handlers: map[string]routeHandler{
			http.MethodGet:  s.handleGetSettingRequest,
			http.MethodHead: s.handleGetSettingRequest,
		},
		allow: allowSettingRequest,
	}
}

// matchSettingsRoute matches "/api/settings/requests" and
// "/api/settings/requests/{id}"; false for any other path.
func (s *Server) matchSettingsRoute(urlPath string) (writeRoute, bool) {
	result := s.settingRequests
	isMatch := (urlPath == settingRequestsPath)
	idText, isItem := strings.CutPrefix(urlPath, settingRequestPrefix)

	if isItem == true {
		result = s.settingRequest
		result.id, isMatch = parseRowID(idText)
	}

	return result, isMatch
}

// handleSettings serves GET /api/settings.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	load := func(ctx context.Context) (payload, error) {
		snapshot, err := s.store.Settings(ctx)
		data := &SettingsPage{}

		if err == nil {
			data, err = buildSettingsPage(snapshot, now)
		}

		return data, err
	}

	s.respond(w, r, nil, load)
}

// handleQueueSettingRequest serves POST /api/settings/requests: checks the
// request with the published registry and queues it for the bot (202).
func (s *Server) handleQueueSettingRequest(w http.ResponseWriter, r *http.Request, id int64) {
	body := settingRequestBody{}
	err := decodeJSONBody(w, r, &body)
	draft := SettingRequestDraft{}

	if err == nil {
		draft, err = settingRequestDraft(body)
	}

	load := func(ctx context.Context) (payload, error) {
		registry, loadErr := s.store.SettingsRegistry(ctx)
		data := &SettingRequestAnswer{}

		if loadErr == nil {
			loadErr = checkSettingRequest(registry, draft)
		}

		if loadErr == nil {
			data.Request, loadErr = s.store.QueueSettingRequest(ctx, draft)
		}

		return data, loadErr
	}

	s.respondWithStatus(w, r, http.StatusAccepted, err, load)
}

// handleGetSettingRequest serves GET /api/settings/requests/{id}, polled by
// the page until the bot decided.
func (s *Server) handleGetSettingRequest(w http.ResponseWriter, r *http.Request, id int64) {
	load := func(ctx context.Context) (payload, error) {
		request, err := s.store.SettingRequest(ctx, id)
		data := &SettingRequestAnswer{Request: request}

		return data, err
	}

	s.respondLoaded(w, r, http.StatusOK, load)
}

// handleSettingsAudit serves GET /api/settings/audit.
func (s *Server) handleSettingsAudit(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, limitErr := auditLimitParam.parse(query)
	beforeText := query.Get(paramBefore)
	keyText := query.Get(paramKey)
	groupText := query.Get(paramGroup)
	before, beforeErr := parseBeforeID(beforeText)
	key := strings.TrimSpace(keyText)
	group := strings.TrimSpace(groupText)
	err := firstError(limitErr, beforeErr)
	load := func(ctx context.Context) (payload, error) {
		registry, loadErr := s.store.SettingsRegistry(ctx)
		data := &SettingsAuditPage{Entries: []SettingsAuditEntry{}}
		filter := SettingsAuditFilter{Before: before, Limit: limit + 1}

		if loadErr == nil {
			filter.Keys, loadErr = auditKeys(registry, key, group)
		}

		if loadErr == nil {
			data.Entries, loadErr = s.store.SettingsAudit(ctx, filter)
		}

		if loadErr == nil {
			data.Entries, data.NextBefore = auditPage(data.Entries, limit)
		}

		return data, loadErr
	}

	s.respond(w, r, err, load)
}

// settingRequestDraft checks the body's shape and compacts its values.
func settingRequestDraft(body settingRequestBody) (SettingRequestDraft, error) {
	var err error
	key := strings.TrimSpace(body.Key)
	value, valueErr := compactSettingJSON(fieldValue, body.Value)
	expected, expectedErr := compactSettingJSON(fieldExpected, body.Expected)
	isReset := (string(value) == jsonNull)

	switch {
	case key == "":
		err = badRequest(errSettingKeyRequired)
	case len(body.Value) == 0:
		err = badRequest(errSettingValueRequired)
	case len(body.Expected) == 0:
		err = badRequest(errSettingExpectedRequired)
	default:
		err = firstError(valueErr, expectedErr)
	}

	if isReset == true {
		value = nil
	}

	result := SettingRequestDraft{Key: key, Value: value, Expected: expected}

	return result, err
}

// compactSettingJSON compacts a raw value and refuses one over the table's
// size limit.
func compactSettingJSON(field string, raw json.RawMessage) (json.RawMessage, error) {
	var result json.RawMessage
	var err error
	buffer := bytes.Buffer{}

	if len(raw) > 0 {
		err = json.Compact(&buffer, raw)
		result = buffer.Bytes()
	}

	if (err == nil) && (len(result) > maxSettingValueBytes) {
		err = badRequest(errSettingValueTooLarge, field, maxSettingValueBytes)
	}

	return result, err
}

// checkSettingRequest refuses a key the published registry does not list
// and a value that fails its checks; a reset needs no value check.
func checkSettingRequest(registry SettingsRegistry, draft SettingRequestDraft) error {
	var err error
	setting, isKnown := findRegistrySetting(registry, draft.Key)

	if isKnown == false {
		err = badRequest(errSettingUnknownKey, draft.Key)
	}

	if (isKnown == true) && (draft.Value != nil) {
		err = validateSettingValue(setting, draft.Value)
	}

	return err
}

// findRegistrySetting looks a key up in the published registry.
func findRegistrySetting(registry SettingsRegistry, key string) (RegistrySetting, bool) {
	result := RegistrySetting{}
	isKnown := false

	for _, setting := range registry.Settings {
		if setting.Key == key {
			result = setting
			isKnown = true
		}
	}

	return result, isKnown
}

// parseBeforeID reads the audit cursor: 0 when missing.
func parseBeforeID(text string) (int64, error) {
	var result int64
	var err error
	raw := strings.TrimSpace(text)

	if raw != "" {
		result, err = strconv.ParseInt(raw, beforeIDBase, beforeIDBits)
	}

	if (err != nil) || (result < 0) {
		result = 0
		err = badRequest(errSettingBeforeInvalid)
	}

	return result, err
}

// auditKeys turns the key and group filters into the keys to show: nil for
// every key; an unknown key or group is a 400.
func auditKeys(registry SettingsRegistry, key string, group string) ([]string, error) {
	var result []string
	var err error
	_, isKnownKey := findRegistrySetting(registry, key)
	isKnownGroup := slices.Contains(registry.Groups, group)

	switch {
	case (key != "") && (isKnownKey == false):
		err = badRequest(errSettingUnknownKey, key)
	case (group != "") && (isKnownGroup == false):
		err = badRequest(errSettingUnknownGroup, group)
	case (key != "") || (group != ""):
		result = filterRegistryKeys(registry, key, group)
	}

	return result, err
}

// filterRegistryKeys lists the registry keys matching key and group (an
// empty filter matches all).
func filterRegistryKeys(registry SettingsRegistry, key string, group string) []string {
	result := []string{}

	for _, setting := range registry.Settings {
		isKeyMatch := (key == "") || (setting.Key == key)
		isGroupMatch := (group == "") || (setting.Group == group)

		if (isKeyMatch == true) && (isGroupMatch == true) {
			result = append(result, setting.Key)
		}
	}

	return result
}

// auditPage cuts the one extra row read past limit and turns it into the
// next page's cursor.
func auditPage(entries []SettingsAuditEntry, limit int) ([]SettingsAuditEntry, *int64) {
	result := entries
	var next *int64

	if len(entries) > limit {
		result = entries[:limit]
		lastID := result[limit-1].ID
		next = &lastID
	}

	return result, next
}

// buildSettingsPage joins the published registry with the effective values,
// the stored values, the open requests and the newest audit rows.
func buildSettingsPage(snapshot SettingsSnapshot, now time.Time) (*SettingsPage, error) {
	registry, err := decodeRegistry(snapshot.Components)
	effective := map[string]json.RawMessage{}
	settingsComponent, isPublished := snapshot.Components[componentSettings]

	if (err == nil) && (isPublished == true) {
		err = json.Unmarshal(settingsComponent.Data, &effective)
	}

	bot, botErr := botState(snapshot.Components, now)
	result := &SettingsPage{
		Bot:       bot,
		Profile:   componentData(snapshot.Components, componentProfile),
		Secrets:   componentData(snapshot.Components, componentSecrets),
		Groups:    nonNilStrings(registry.Groups),
		Platforms: nonNilPlatforms(registry.Platforms),
		Applying:  (bot[botFieldAppliesRequests] == true),
		Settings:  []SettingRow{},
	}

	for _, setting := range registry.Settings {
		row := settingRow(setting, snapshot, effective)
		result.Settings = append(result.Settings, row)
	}

	err = firstError(err, botErr)

	return result, err
}

// settingRow builds one setting of the page.
func settingRow(setting RegistrySetting, snapshot SettingsSnapshot, effective map[string]json.RawMessage) SettingRow {
	result := SettingRow{RegistrySetting: setting, Effective: effective[setting.Key]}
	stored, isStored := snapshot.Stored[setting.Key]
	request, isOpen := snapshot.OpenRequests[setting.Key]
	change, isChanged := snapshot.LastChanges[setting.Key]

	if isStored == true {
		result.Stored = &stored
	}

	if isOpen == true {
		result.OpenRequest = &request
	}

	if isChanged == true {
		result.LastChange = &change
	}

	return result
}

// botState is the published 'bot' component with is_stale added: true when
// the heartbeat (else the row's time) is older than heartbeatStaleAfter.
// Nil when the bot never published one.
func botState(components map[string]StatusComponent, now time.Time) (map[string]interface{}, error) {
	var result map[string]interface{}
	var err error
	component, isPublished := components[componentBot]

	if isPublished == true {
		err = json.Unmarshal(component.Data, &result)
	}

	if (err == nil) && (result != nil) {
		heartbeat := heartbeatTime(result, component.UpdatedAt)
		age := now.Sub(heartbeat)
		result[botFieldIsStale] = (age > heartbeatStaleAfter)
	}

	return result, err
}

// heartbeatTime reads heartbeat_at, else the row's update time.
func heartbeatTime(bot map[string]interface{}, updatedAt time.Time) time.Time {
	result := updatedAt
	text, isText := bot[botFieldHeartbeatAt].(string)
	parsed, err := time.Parse(time.RFC3339Nano, text)

	if (isText == true) && (err == nil) {
		result = parsed
	}

	return result
}

// componentData is a component's JSON, or null when it is not published.
func componentData(components map[string]StatusComponent, name string) json.RawMessage {
	var result json.RawMessage
	component, isPublished := components[name]

	if isPublished == true {
		result = component.Data
	}

	return result
}

// nonNilStrings makes a missing list an empty JSON array.
func nonNilStrings(values []string) []string {
	result := values

	if result == nil {
		result = []string{}
	}

	return result
}

// nonNilPlatforms makes a missing list an empty JSON array.
func nonNilPlatforms(values []RegistryPlatform) []RegistryPlatform {
	result := values

	if result == nil {
		result = []RegistryPlatform{}
	}

	return result
}
