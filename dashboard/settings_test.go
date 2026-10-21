package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// settingsStub records the settings calls of a stubStore and holds what it
// answers.
type settingsStub struct {
	calls      int
	snapshot   SettingsSnapshot
	registry   SettingsRegistry
	draft      *SettingRequestDraft
	queueErr   error
	requestID  int64
	requestErr error
	filter     *SettingsAuditFilter
	audit      []SettingsAuditEntry
}

func (s *stubStore) Settings(ctx context.Context) (SettingsSnapshot, error) {
	s.settings.calls++

	return s.settings.snapshot, s.err
}

func (s *stubStore) SettingsRegistry(ctx context.Context) (SettingsRegistry, error) {
	s.settings.calls++

	return s.settings.registry, s.err
}

func (s *stubStore) QueueSettingRequest(ctx context.Context, draft SettingRequestDraft) (SettingRequest, error) {
	s.settings.calls++
	s.settings.draft = &draft
	request := SettingRequest{ID: 42, Key: draft.Key, Status: "queued", Value: draft.Value, Expected: draft.Expected}

	return request, s.settings.queueErr
}

func (s *stubStore) SettingRequest(ctx context.Context, id int64) (SettingRequest, error) {
	s.settings.calls++
	s.settings.requestID = id

	return SettingRequest{ID: id, Key: "platform.tiktok.enabled", Status: "applied"}, s.settings.requestErr
}

func (s *stubStore) SettingsAudit(ctx context.Context, filter SettingsAuditFilter) ([]SettingsAuditEntry, error) {
	s.settings.calls++
	s.settings.filter = &filter
	result := s.settings.audit

	if len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, s.err
}

// testRegistryJSON is a registry as the bot publishes it.
const testRegistryJSON = `{
"groups": ["platforms", "paid", "services"],
"platforms": [{"id": "tiktok", "label": "TikTok"}],
"settings": [
 {"key": "platform.tiktok.enabled", "group": "platforms", "type": "bool", "env_var": "ENABLE_TIKTOK", "default": true, "approval": "never", "label": "TikTok", "impact": "Off: ignored."},
 {"key": "paid.apify.monthly_budget_usd", "group": "paid", "type": "decimal", "default": 4, "min": 0, "ceiling": 20, "scale": 2, "approval": "raise", "safe_value": 0, "label": "Apify", "impact": "Cap."},
 {"key": "paid.instagram.provider_order", "group": "paid", "type": "ordered_list", "default": ["apify", "hiker"], "enum": ["apify", "hiker"], "approval": "never", "label": "Order", "impact": "Order."},
 {"key": "service.facebed.host", "group": "services", "type": "host", "default": "www.facebed.com", "approval": "always", "label": "facebed", "impact": "Host."},
 {"key": "gated.global_monthly_cap", "group": "restricted_posts", "type": "int", "default": -1, "min": -1, "ceiling": 5000, "approval": "never", "label": "Cap", "impact": "Cap."},
 {"key": "platform.instagram.fixed_link_service", "group": "platforms", "type": "enum", "default": "toinstagram", "enum": ["toinstagram", "onebigram"], "approval": "never", "label": "Fixed", "impact": "Domain."},
 {"key": "paid.hiker.price_per_request_usd", "group": "paid", "type": "decimal", "default": 0.001, "min": 0, "min_exclusive": true, "max": 1, "scale": 6, "approval": "lower", "label": "Price", "impact": "Price."}
]}`

// testRegistry decodes testRegistryJSON.
func testRegistry(t *testing.T) SettingsRegistry {
	t.Helper()
	registry := SettingsRegistry{}

	if err := json.Unmarshal([]byte(testRegistryJSON), &registry); err != nil {
		t.Fatalf("registry: %v", err)
	}

	return registry
}

func testRegistrySetting(t *testing.T, key string) RegistrySetting {
	t.Helper()
	setting, isKnown := findRegistrySetting(testRegistry(t), key)

	if isKnown == false {
		t.Fatalf("no %s in the test registry", key)
	}

	return setting
}

// testSnapshot is a published state with one stored value, one open request
// and one audit row.
func testSnapshot(heartbeat time.Time, appliesRequests bool) SettingsSnapshot {
	bot := map[string]interface{}{"heartbeat_at": heartbeat.Format(time.RFC3339Nano), "settings_source": "db", "applies_requests": appliesRequests}
	botJSON, _ := json.Marshal(bot)
	updatedAt := "2026-10-15T11:00:00Z"
	result := SettingsSnapshot{
		Components: map[string]StatusComponent{
			componentBot:      {Data: botJSON, UpdatedAt: heartbeat},
			componentRegistry: {Data: json.RawMessage(testRegistryJSON)},
			componentSettings: {Data: json.RawMessage(`{"platform.tiktok.enabled": {"value": false, "source": "db", "env_set": true}}`)},
			componentSecrets:  {Data: json.RawMessage(`{"apify_token": true, "hiker_token": false}`)},
		},
		Stored: map[string]StoredSetting{
			"platform.tiktok.enabled": {Value: json.RawMessage(`false`), UpdatedAt: &updatedAt, UpdatedBy: "dashboard"},
		},
		OpenRequests: map[string]SettingRequest{
			"paid.instagram.provider_order": {ID: 7, Key: "paid.instagram.provider_order", Status: "queued", Value: json.RawMessage(`["hiker","apify"]`)},
		},
		LastChanges: map[string]SettingsAuditEntry{
			"platform.tiktok.enabled": {ID: 3, Key: "platform.tiktok.enabled", OldValue: nil, NewValue: json.RawMessage(`false`), Actor: "dashboard"},
		},
	}

	return result
}

func settingsRequest(body string) blogRequest {
	return blogRequest{method: http.MethodPost, target: "/api/settings/requests", body: body}
}

func TestSettingsPageJoinsTheRegistryWithThePublishedState(t *testing.T) {
	store := &stubStore{}
	store.settings.snapshot = testSnapshot(testNow.Add(-time.Minute), true)
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings")
	page := SettingsPage{}

	if err := json.Unmarshal(recorder.Body.Bytes(), &page); (err != nil) || (recorder.Code != http.StatusOK) {
		t.Fatalf("status = %d, err = %v, body = %s", recorder.Code, err, recorder.Body.String())
	}

	if (len(page.Settings) != 7) || (page.Settings[0].Key != "platform.tiktok.enabled") || (page.Settings[1].Key != "paid.apify.monthly_budget_usd") {
		t.Fatalf("settings = %+v", page.Settings)
	}

	tiktok := page.Settings[0]

	if (string(tiktok.Effective) != `{"value":false,"source":"db","env_set":true}`) || (tiktok.Stored == nil) || (tiktok.Stored.UpdatedBy != "dashboard") || (tiktok.LastChange == nil) || (tiktok.OpenRequest != nil) {
		t.Errorf("tiktok = %+v", tiktok)
	}

	order := page.Settings[2]

	if (order.OpenRequest == nil) || (order.OpenRequest.ID != 7) || (string(order.Effective) != "null") || (order.Stored != nil) {
		t.Errorf("order = %+v", order)
	}

	if (page.Applying != true) || (page.Bot["is_stale"] != false) || (page.Bot["settings_source"] != "db") || (len(page.Groups) != 3) || (page.Platforms[0].ID != "tiktok") {
		t.Errorf("page = %+v", page)
	}

	if (string(page.Secrets) != `{"apify_token":true,"hiker_token":false}`) || (string(page.Profile) != "null") || (page.GeneratedAt == "") {
		t.Errorf("secrets = %s, profile = %s", page.Secrets, page.Profile)
	}
}

func TestSettingsPageKeepsTheRegistryFieldsForThePage(t *testing.T) {
	store := &stubStore{}
	store.settings.snapshot = testSnapshot(testNow, false)
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings")
	body := decodeBody(t, recorder)
	settings, _ := body["settings"].([]interface{})
	budget, _ := settings[1].(map[string]interface{})

	if (budget["ceiling"] != float64(20)) || (budget["scale"] != float64(2)) || (budget["approval"] != "raise") || (budget["safe_value"] != float64(0)) || (budget["default"] != float64(4)) {
		t.Errorf("budget = %v", budget)
	}

	if (budget["effective"] != nil) || (budget["stored"] != nil) || (budget["open_request"] != nil) || (budget["last_change"] != nil) {
		t.Errorf("budget = %v", budget)
	}

	if _, isPresent := budget["effective"]; isPresent == false {
		t.Errorf("effective must be present as null: %v", budget)
	}
}

func TestSettingsPageMarksAnOldHeartbeatStale(t *testing.T) {
	store := &stubStore{}
	store.settings.snapshot = testSnapshot(testNow.Add(-4*time.Minute), false)
	body := decodeBody(t, doRequest(newTestServer(store), http.MethodGet, "/api/settings"))
	bot, _ := body["bot"].(map[string]interface{})

	if (bot["is_stale"] != true) || (body["applying"] != false) {
		t.Errorf("bot = %v, applying = %v", bot, body["applying"])
	}
}

func TestSettingsPageFallsBackToTheRowTimeWithoutAHeartbeat(t *testing.T) {
	store := &stubStore{}
	store.settings.snapshot = SettingsSnapshot{Components: map[string]StatusComponent{componentBot: {Data: json.RawMessage(`{}`), UpdatedAt: testNow.Add(-10 * time.Minute)}}}
	body := decodeBody(t, doRequest(newTestServer(store), http.MethodGet, "/api/settings"))
	bot, _ := body["bot"].(map[string]interface{})

	if bot["is_stale"] != true {
		t.Errorf("bot = %v", bot)
	}
}

func TestSettingsPageBeforeTheBotPublishedIsEmpty(t *testing.T) {
	recorder := doRequest(newTestServer(&stubStore{}), http.MethodGet, "/api/settings")
	body := decodeBody(t, recorder)
	settings, isList := body["settings"].([]interface{})
	groups, isGroups := body["groups"].([]interface{})

	if (recorder.Code != http.StatusOK) || (isList == false) || (len(settings) != 0) || (isGroups == false) || (len(groups) != 0) {
		t.Fatalf("status = %d, body = %v", recorder.Code, body)
	}

	if (body["bot"] != nil) || (body["profile"] != nil) || (body["secrets"] != nil) || (body["applying"] != false) {
		t.Errorf("body = %v", body)
	}
}

func TestSettingsPageWithABrokenRegistryIs500(t *testing.T) {
	store := &stubStore{}
	store.settings.snapshot = SettingsSnapshot{Components: map[string]StatusComponent{componentRegistry: {Data: json.RawMessage(`[]`)}}}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", recorder.Code)
	}
}

func TestSettingRequestIsQueuedCompactAnd202(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	recorder := settingsRequest(`{"key": " platform.tiktok.enabled ", "value": false, "expected": true}`).do(newTestServer(store))
	body := decodeBody(t, recorder)
	request, _ := body["request"].(map[string]interface{})

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %v", recorder.Code, body)
	}

	draft := store.settings.draft

	if (draft == nil) || (draft.Key != "platform.tiktok.enabled") || (string(draft.Value) != "false") || (string(draft.Expected) != "true") {
		t.Errorf("draft = %+v", draft)
	}

	if (request["id"] != float64(42)) || (request["status"] != "queued") || (body["generated_at"] == nil) {
		t.Errorf("body = %v", body)
	}
}

func TestSettingRequestNullValueIsAReset(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	recorder := settingsRequest(`{"key": "paid.apify.monthly_budget_usd", "value": null, "expected": 4.8}`).do(newTestServer(store))

	if (recorder.Code != http.StatusAccepted) || (store.settings.draft == nil) || (store.settings.draft.Value != nil) || (string(store.settings.draft.Expected) != "4.8") {
		t.Errorf("status = %d, draft = %+v", recorder.Code, store.settings.draft)
	}
}

func TestSettingRequestListValueIsCompacted(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	recorder := settingsRequest(`{"key": "paid.instagram.provider_order", "value": [ "hiker", "apify" ], "expected": ["apify","hiker"]}`).do(newTestServer(store))

	if (recorder.Code != http.StatusAccepted) || (string(store.settings.draft.Value) != `["hiker","apify"]`) {
		t.Errorf("status = %d, draft = %+v", recorder.Code, store.settings.draft)
	}
}

func TestBadSettingRequestsAre400WithoutQueuing(t *testing.T) {
	large := strings.Repeat("a", maxSettingValueBytes)
	cases := []struct {
		name    string
		body    string
		message string
	}{
		{"no key", `{"value": true, "expected": true}`, "key is required"},
		{"no value", `{"key": "platform.tiktok.enabled", "expected": true}`, "value is required"},
		{"no expected", `{"key": "platform.tiktok.enabled", "value": true}`, "expected is required"},
		{"unknown field", `{"key": "platform.tiktok.enabled", "value": true, "expected": true, "confirm": true}`, "unknown field"},
		{"unknown key", `{"key": "secrets.token", "value": "x", "expected": "y"}`, `unknown setting "secrets.token"`},
		{"wrong type", `{"key": "platform.tiktok.enabled", "value": "off", "expected": true}`, "platform.tiktok.enabled must be true or false"},
		{"over the ceiling", `{"key": "paid.apify.monthly_budget_usd", "value": 25, "expected": 4}`, "hard ceiling"},
		{"too many decimals", `{"key": "paid.apify.monthly_budget_usd", "value": 1.005, "expected": 4}`, "at most 2 decimals"},
		{"bad host", `{"key": "service.facebed.host", "value": "https://evil.example", "expected": "www.facebed.com"}`, "hostname only"},
		{"value too large", `{"key": "service.facebed.host", "value": "` + large + `", "expected": "x"}`, "value is too large"},
		{"expected too large", `{"key": "service.facebed.host", "value": "a.b", "expected": "` + large + `"}`, "expected is too large"},
	}

	for _, testCase := range cases {
		store := &stubStore{}
		store.settings.registry = testRegistry(t)
		recorder := settingsRequest(testCase.body).do(newTestServer(store))
		body := decodeBody(t, recorder)
		message, _ := body["error"].(string)

		if (recorder.Code != http.StatusBadRequest) || (strings.Contains(message, testCase.message) == false) {
			t.Errorf("%s: status = %d, error = %q", testCase.name, recorder.Code, message)
		}

		if store.settings.draft != nil {
			t.Errorf("%s: queued %+v", testCase.name, store.settings.draft)
		}
	}
}

func TestSettingRequestWhileOneIsOpenIs409(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	store.settings.queueErr = ErrSettingRequestOpen
	recorder := settingsRequest(`{"key": "platform.tiktok.enabled", "value": false, "expected": true}`).do(newTestServer(store))
	body := decodeBody(t, recorder)

	if (recorder.Code != http.StatusConflict) || (body["error"] != "a change of this setting is already waiting") {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestSettingRequestTooLargeToStoreIs400(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	store.settings.queueErr = ErrSettingValueTooLarge
	recorder := settingsRequest(`{"key": "platform.tiktok.enabled", "value": false, "expected": true}`).do(newTestServer(store))
	body := decodeBody(t, recorder)

	if (recorder.Code != http.StatusBadRequest) || (body["error"] != ErrSettingValueTooLarge.Error()) {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestSettingRequestNeedsTheWriteGuard(t *testing.T) {
	empty := ""
	other := "http://evil.example"
	text := "text/plain"
	cases := map[string]blogRequest{
		"no marker":    {marker: &empty},
		"other origin": {origin: &other},
		"not JSON":     {contentType: &text},
	}

	for name, request := range cases {
		store := &stubStore{}
		store.settings.registry = testRegistry(t)
		request.method = http.MethodPost
		request.target = "/api/settings/requests"
		request.body = `{"key": "platform.tiktok.enabled", "value": false, "expected": true}`
		recorder := request.do(newTestServer(store))

		if (recorder.Code != http.StatusForbidden) || (store.settings.calls != 0) {
			t.Errorf("%s: status = %d, calls = %d", name, recorder.Code, store.settings.calls)
		}
	}
}

func TestSettingsRoutesRefuseOtherMethods(t *testing.T) {
	cases := []struct {
		method string
		target string
		allow  string
	}{
		{http.MethodGet, "/api/settings/requests", "POST"},
		{http.MethodPut, "/api/settings/requests", "POST"},
		{http.MethodPost, "/api/settings/requests/5", "GET, HEAD"},
		{http.MethodDelete, "/api/settings/requests/5", "GET, HEAD"},
		{http.MethodPost, "/api/settings", "GET, HEAD"},
		{http.MethodPost, "/api/settings/audit", "GET, HEAD"},
	}

	for _, testCase := range cases {
		store := &stubStore{}
		recorder := blogRequest{method: testCase.method, target: testCase.target, body: `{}`}.do(newTestServer(store))

		if (recorder.Code != http.StatusMethodNotAllowed) || (recorder.Header().Get("Allow") != testCase.allow) || (store.settings.calls != 0) {
			t.Errorf("%s %s: status = %d, Allow = %q", testCase.method, testCase.target, recorder.Code, recorder.Header().Get("Allow"))
		}
	}
}

func TestSettingRequestIsReadByID(t *testing.T) {
	store := &stubStore{}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings/requests/9")
	body := decodeBody(t, recorder)
	request, _ := body["request"].(map[string]interface{})

	if (recorder.Code != http.StatusOK) || (store.settings.requestID != 9) || (request["status"] != "applied") || (recorder.Header().Get("Cache-Control") != "no-store") {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestMissingSettingRequestIs404(t *testing.T) {
	store := &stubStore{}
	store.settings.requestErr = ErrSettingRequestNotFound
	cases := map[string]int{"/api/settings/requests/9": http.StatusNotFound, "/api/settings/requests/x": http.StatusNotFound, "/api/settings/requests/09": http.StatusNotFound}

	for target, status := range cases {
		recorder := doRequest(newTestServer(store), http.MethodGet, target)

		if recorder.Code != status {
			t.Errorf("%s: status = %d", target, recorder.Code)
		}
	}
}

func TestSettingsAuditPagesNewestFirst(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	store.settings.audit = []SettingsAuditEntry{{ID: 9}, {ID: 8}, {ID: 7}}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings/audit?limit=2&before=10")
	page := SettingsAuditPage{}

	if err := json.Unmarshal(recorder.Body.Bytes(), &page); (err != nil) || (recorder.Code != http.StatusOK) {
		t.Fatalf("status = %d, err = %v", recorder.Code, err)
	}

	filter := store.settings.filter

	if (filter == nil) || (filter.Limit != 3) || (filter.Before != 10) || (filter.Keys != nil) {
		t.Errorf("filter = %+v", filter)
	}

	if (len(page.Entries) != 2) || (page.NextBefore == nil) || (*page.NextBefore != 8) {
		t.Errorf("page = %+v", page)
	}
}

func TestSettingsAuditLastPageHasNoCursor(t *testing.T) {
	store := &stubStore{}
	store.settings.audit = []SettingsAuditEntry{{ID: 2}}
	body := decodeBody(t, doRequest(newTestServer(store), http.MethodGet, "/api/settings/audit"))
	entries, _ := body["entries"].([]interface{})

	if (len(entries) != 1) || (body["next_before"] != nil) || (store.settings.filter.Limit != defaultAuditLimit+1) {
		t.Errorf("body = %v, filter = %+v", body, store.settings.filter)
	}
}

func TestSettingsAuditFiltersByKeyAndGroup(t *testing.T) {
	cases := map[string][]string{
		"/api/settings/audit?key=platform.tiktok.enabled":            {"platform.tiktok.enabled"},
		"/api/settings/audit?group=paid":                             {"paid.apify.monthly_budget_usd", "paid.instagram.provider_order", "paid.hiker.price_per_request_usd"},
		"/api/settings/audit?group=paid&key=platform.tiktok.enabled": {},
	}

	for target, keys := range cases {
		store := &stubStore{}
		store.settings.registry = testRegistry(t)
		recorder := doRequest(newTestServer(store), http.MethodGet, target)
		filter := store.settings.filter

		if (recorder.Code != http.StatusOK) || (filter == nil) || (strings.Join(filter.Keys, ",") != strings.Join(keys, ",")) || (filter.Keys == nil) {
			t.Errorf("%s: status = %d, filter = %+v", target, recorder.Code, filter)
		}
	}
}

func TestBadSettingsAuditParamsAre400(t *testing.T) {
	cases := map[string]string{
		"/api/settings/audit?key=nope":    `unknown setting "nope"`,
		"/api/settings/audit?group=nope":  `unknown group "nope"`,
		"/api/settings/audit?before=x":    "invalid before",
		"/api/settings/audit?before=-1":   "invalid before",
		"/api/settings/audit?limit=lots":  "invalid limit",
		"/api/settings/audit?before=1e10": "invalid before",
	}

	for target, message := range cases {
		store := &stubStore{}
		store.settings.registry = testRegistry(t)
		recorder := doRequest(newTestServer(store), http.MethodGet, target)
		body := decodeBody(t, recorder)
		text, _ := body["error"].(string)

		if (recorder.Code != http.StatusBadRequest) || (strings.Contains(text, message) == false) || (store.settings.filter != nil) {
			t.Errorf("%s: status = %d, error = %q", target, recorder.Code, text)
		}
	}
}

func TestSettingValueValidationMirrorsTheBot(t *testing.T) {
	cases := []struct {
		key     string
		value   string
		isValid bool
	}{
		{"platform.tiktok.enabled", `true`, true},
		{"platform.tiktok.enabled", `1`, false},
		{"platform.tiktok.enabled", `true true`, false},
		{"gated.global_monthly_cap", `-1`, true},
		{"gated.global_monthly_cap", `-2`, false},
		{"gated.global_monthly_cap", `5000`, true},
		{"gated.global_monthly_cap", `5001`, false},
		{"gated.global_monthly_cap", `1.5`, false},
		{"gated.global_monthly_cap", `"5"`, false},
		{"paid.apify.monthly_budget_usd", `0`, true},
		{"paid.apify.monthly_budget_usd", `19.99`, true},
		{"paid.apify.monthly_budget_usd", `-0.01`, false},
		{"paid.hiker.price_per_request_usd", `0`, false},
		{"paid.hiker.price_per_request_usd", `0.000001`, true},
		{"paid.hiker.price_per_request_usd", `0.0000001`, false},
		{"paid.hiker.price_per_request_usd", `1.5`, false},
		{"platform.instagram.fixed_link_service", `"onebigram"`, true},
		{"platform.instagram.fixed_link_service", `"other"`, false},
		{"paid.instagram.provider_order", `["hiker"]`, true},
		{"paid.instagram.provider_order", `[]`, false},
		{"paid.instagram.provider_order", `["apify","apify"]`, false},
		{"paid.instagram.provider_order", `["apify","other"]`, false},
		{"paid.instagram.provider_order", `"apify"`, false},
		{"service.facebed.host", `"www.facebed.com"`, true},
		{"service.facebed.host", `"WWW.Facebed.com"`, true},
		{"service.facebed.host", `"localhost"`, false},
		{"service.facebed.host", `"10.0.0.1"`, false},
		{"service.facebed.host", `"facebed.com:8080"`, false},
		{"service.facebed.host", `"-bad.com"`, false},
		{"service.facebed.host", `""`, false},
	}

	for _, testCase := range cases {
		setting := testRegistrySetting(t, testCase.key)
		err := validateSettingValue(setting, json.RawMessage(testCase.value))
		isValid := (err == nil)

		if isValid != testCase.isValid {
			t.Errorf("%s = %s: err = %v", testCase.key, testCase.value, err)
		}
	}
}

func TestUnknownSettingTypeIsRefused(t *testing.T) {
	err := validateSettingValue(RegistrySetting{Key: "x.y", Type: "color"}, json.RawMessage(`"red"`))

	if (err == nil) || (strings.Contains(err.Error(), "type") == false) {
		t.Errorf("err = %v", err)
	}
}
