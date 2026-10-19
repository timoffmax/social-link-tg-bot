package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// limitsStub records the limits calls of a stubStore and holds what it
// answers.
type limitsStub struct {
	calls     int
	rows      int
	scope     string
	id        int64
	snapshot  LimitsSnapshot
	draft     *SettingRequestDraft
	queueErr  error
	scopeRows []LimitRow
}

func (s *stubStore) Limits(ctx context.Context, rows int) (LimitsSnapshot, error) {
	s.limits.calls++
	s.limits.rows = rows

	return s.limits.snapshot, s.err
}

func (s *stubStore) LimitScope(ctx context.Context, scope string, id int64) (LimitsSnapshot, error) {
	s.limits.calls++
	s.limits.scope = scope
	s.limits.id = id
	snapshot := s.limits.snapshot
	snapshot.Rows = s.limits.scopeRows

	return snapshot, s.err
}

func (s *stubStore) QueueLimitRequest(ctx context.Context, draft SettingRequestDraft) (SettingRequest, error) {
	s.limits.calls++
	s.limits.draft = &draft
	request := SettingRequest{ID: 51, Kind: limitRequestKind, Key: draft.Key, Status: "queued", Value: draft.Value, Expected: draft.Expected}

	return request, s.limits.queueErr
}

func int64Ref(value int64) *int64 {
	return &value
}

func testDefaults() LimitDefaults {
	return LimitDefaults{User: int64Ref(20), Chat: int64Ref(50)}
}

// limitsRegistry is the test registry with the limit rules a bot after
// migration 012 publishes.
func limitsRegistry(t *testing.T) SettingsRegistry {
	t.Helper()
	registry := testRegistry(t)
	registry.SpecialLimit = &SpecialLimitRules{Kind: limitRequestKind, Min: 0, Max: 1000}

	return registry
}

func limitRequest(body string) blogRequest {
	return blogRequest{method: http.MethodPost, target: "/api/limits/requests", body: body}
}

// testLimitsSnapshot is a published state with rules, defaults, a cap, a
// fresh paid and a stale relay component.
func testLimitsSnapshot(heartbeat time.Time) LimitsSnapshot {
	bot := map[string]interface{}{"heartbeat_at": heartbeat.Format(time.RFC3339Nano), "applies_requests": true}
	botJSON, _ := json.Marshal(bot)
	registry := `{"groups": [], "settings": [], "special_limit": {"kind": "special_limit", "min": 0, "max": 1000}}`
	effective := `{"gated.user_monthly_allowance": {"value": 20}, "gated.chat_monthly_cap": {"value": 50}, "gated.global_monthly_cap": {"value": 400}}`
	result := LimitsSnapshot{
		Components: map[string]StatusComponent{
			componentBot:      {Data: botJSON, UpdatedAt: heartbeat},
			componentRegistry: {Data: json.RawMessage(registry)},
			componentSettings: {Data: json.RawMessage(effective)},
			componentPaid:     {Data: json.RawMessage(`{"apify": {"state": "on"}}`), UpdatedAt: testNow.Add(-time.Minute)},
			componentRelay:    {Data: json.RawMessage(`{"configured": true}`), UpdatedAt: testNow.Add(-5 * time.Minute)},
		},
		GlobalUsed: 37,
		Rows: []LimitRow{
			{Scope: "user", ScopeID: 7, Used: 3, Enabled: true},
			{Scope: "chat", ScopeID: -1001, Used: 48, Enabled: true},
			{Scope: "chat", ScopeID: -1002, Used: 10, StoredLimit: int64Ref(10), Enabled: true},
		},
		Total: 5,
		OpenRequests: map[string]SettingRequest{
			"chat:-1001": {ID: 9, Kind: limitRequestKind, Key: "chat:-1001", Status: "queued"},
		},
	}

	return result
}

func TestLimitStateResolvesDefaultCustomUnlimitedOffBlocked(t *testing.T) {
	cases := []struct {
		name      string
		row       LimitRow
		source    string
		effective *int64
		state     string
	}{
		{"default person", LimitRow{Scope: "user", Used: 3, Enabled: true}, limitSourceDefault, int64Ref(20), limitStateOK},
		{"custom group near", LimitRow{Scope: "chat", Used: 8, StoredLimit: int64Ref(10), Enabled: true}, limitSourceCustom, int64Ref(10), limitStateNear},
		{"default group full", LimitRow{Scope: "chat", Used: 50, Enabled: true}, limitSourceDefault, int64Ref(50), limitStateFull},
		{"unlimited", LimitRow{Scope: "user", Used: 300, StoredLimit: int64Ref(-1), Enabled: true}, limitSourceUnlimited, int64Ref(-1), limitStateUnlimited},
		{"group switched off", LimitRow{Scope: "chat", Used: 1, StoredLimit: int64Ref(30), Enabled: false}, limitSourceCustom, int64Ref(30), limitStateOff},
		{"group at zero", LimitRow{Scope: "chat", StoredLimit: int64Ref(0), Enabled: true}, limitSourceCustom, int64Ref(0), limitStateOff},
		{"person blocked", LimitRow{Scope: "user", StoredLimit: int64Ref(0), Enabled: true}, limitSourceCustom, int64Ref(0), limitStateBlocked},
		{"person switched off is still counted", LimitRow{Scope: "user", Used: 1, Enabled: false}, limitSourceDefault, int64Ref(20), limitStateOK},
	}

	for _, testCase := range cases {
		got := limitState(testCase.row, testDefaults())

		if (got.Source != testCase.source) || (got.State != testCase.state) {
			t.Errorf("%s: source = %s, state = %s", testCase.name, got.Source, got.State)
		}

		assertEqual(t, testCase.name, got.EffectiveLimit, testCase.effective)
	}

	unlimited := limitState(LimitRow{Scope: "user", StoredLimit: int64Ref(-1)}, testDefaults())

	if unlimited.Remaining != nil {
		t.Errorf("unlimited remaining = %v", *unlimited.Remaining)
	}
}

func TestLimitStateWithoutPublishedDefaults(t *testing.T) {
	got := limitState(LimitRow{Scope: "chat", Used: 400, Enabled: true}, LimitDefaults{})

	if (got.EffectiveLimit != nil) || (got.Remaining != nil) || (got.State != limitStateOK) || (got.Source != limitSourceDefault) {
		t.Errorf("row = %+v", got)
	}

	custom := limitState(LimitRow{Scope: "chat", Used: 4, StoredLimit: int64Ref(4), Enabled: true}, LimitDefaults{})

	if custom.State != limitStateFull {
		t.Errorf("custom = %+v", custom)
	}
}

func TestRemainingFlooredAtZero(t *testing.T) {
	got := limitState(LimitRow{Scope: "user", Used: 35, StoredLimit: int64Ref(30), Enabled: true}, testDefaults())
	left := limitState(LimitRow{Scope: "user", Used: 5, Enabled: true}, testDefaults())

	assertEqual(t, "over", got.Remaining, int64Ref(0))
	assertEqual(t, "left", left.Remaining, int64Ref(15))
}

func TestSortLimitRowsNearestFirst(t *testing.T) {
	rows := []LimitRow{
		{Scope: "user", ScopeID: 1, Used: 1, Enabled: true},
		{Scope: "chat", ScopeID: -5, Used: 1, StoredLimit: int64Ref(0), Enabled: true},
		{Scope: "user", ScopeID: 2, Used: 900, StoredLimit: int64Ref(-1), Enabled: true},
		{Scope: "user", ScopeID: 3, Used: 17, Enabled: true},
		{Scope: "chat", ScopeID: -6, Used: 60, Enabled: true},
		{Scope: "user", ScopeID: 4, Used: 10, Enabled: true},
		{Scope: "user", ScopeID: 5, Used: 0, StoredLimit: int64Ref(0), Enabled: true},
		{Scope: "chat", ScopeID: -7, Used: 50, Enabled: true},
	}
	resolved := []LimitRow{}

	for _, row := range rows {
		resolved = append(resolved, limitState(row, testDefaults()))
	}

	sortLimitRows(resolved)
	order := []int64{}

	for _, row := range resolved {
		order = append(order, row.ScopeID)
	}

	assertEqual(t, "order", order, []int64{-6, -7, 3, 4, 1, 2, -5, 5})
}

func TestLimitsPageJoinsThePublishedState(t *testing.T) {
	store := &stubStore{}
	store.limits.snapshot = testLimitsSnapshot(testNow.Add(-time.Minute))
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/limits?limit=3")
	page := LimitsPage{}

	if err := json.Unmarshal(recorder.Body.Bytes(), &page); (err != nil) || (recorder.Code != http.StatusOK) {
		t.Fatalf("status = %d, err = %v, body = %s", recorder.Code, err, recorder.Body.String())
	}

	if (store.limits.rows != 3) || (page.Period != "2026-10") || (page.ResetsAt != "2026-11-01T00:00:00Z") || (page.Applying != true) {
		t.Errorf("page = %+v, rows = %d", page, store.limits.rows)
	}

	if (page.Rules == nil) || (page.Rules.Max != 1000) || (*page.Defaults.User != 20) || (*page.Defaults.Chat != 50) {
		t.Errorf("rules = %+v, defaults = %+v", page.Rules, page.Defaults)
	}

	if (page.Global.Used != 37) || (page.Global.Cap == nil) || (*page.Global.Cap != 400) || (page.Global.IsOff == true) {
		t.Errorf("global = %+v", page.Global)
	}

	if (page.Paid == nil) || (page.Paid.IsStale == true) || (string(page.Paid.Data) != `{"apify":{"state":"on"}}`) || (page.Relay == nil) || (page.Relay.IsStale == false) {
		t.Errorf("paid = %+v, relay = %+v", page.Paid, page.Relay)
	}

	if (len(page.Rows) != 3) || (page.Rows[0].ScopeID != -1002) || (page.Rows[1].ScopeID != -1001) || (page.Rows[1].OpenRequest == nil) || (page.Rows[1].OpenRequest.ID != 9) {
		t.Errorf("rows = %+v", page.Rows)
	}

	if (page.Total != 5) || (page.Truncated == false) {
		t.Errorf("total = %d, truncated = %v", page.Total, page.Truncated)
	}
}

func TestLimitsPageBeforeTheBotPublished(t *testing.T) {
	body := decodeBody(t, doRequest(newTestServer(&stubStore{}), http.MethodGet, "/api/limits"))
	rows, isList := body["rows"].([]interface{})
	global, _ := body["global"].(map[string]interface{})

	if (isList == false) || (len(rows) != 0) || (body["rules"] != nil) || (body["paid"] != nil) || (body["relay"] != nil) || (body["applying"] != false) || (global["cap"] != nil) {
		t.Errorf("body = %v", body)
	}
}

func TestLimitsPageGlobalCapOffAndUnlimited(t *testing.T) {
	cases := map[string]bool{"0": true, "-1": false}

	for value, isOff := range cases {
		snapshot := LimitsSnapshot{Components: map[string]StatusComponent{
			componentSettings: {Data: json.RawMessage(`{"gated.global_monthly_cap": {"value": ` + value + `}}`)},
		}}
		page, err := buildLimitsPage(snapshot, testNow)
		isCapShown := (page.Global.Cap != nil)

		if (err != nil) || (page.Global.IsOff != isOff) || (isCapShown != isOff) {
			t.Errorf("%s: global = %+v, err = %v", value, page.Global, err)
		}
	}
}

func TestLimitsPageWithoutRuleKindIsNotSupported(t *testing.T) {
	snapshot := LimitsSnapshot{Components: map[string]StatusComponent{
		componentRegistry: {Data: json.RawMessage(`{"special_limit": {"kind": "other", "min": 0, "max": 5}}`)},
	}}
	page, err := buildLimitsPage(snapshot, testNow)

	if (err != nil) || (page.Rules != nil) {
		t.Errorf("rules = %+v, err = %v", page.Rules, err)
	}
}

func TestLimitRequestQueued202WithKindAndKey(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = limitsRegistry(t)
	recorder := limitRequest(`{"scope": "chat", "id": -1001234567890, "limit": 30, "expected": null}`).do(newTestServer(store))
	body := decodeBody(t, recorder)
	request, _ := body["request"].(map[string]interface{})
	draft := store.limits.draft

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %v", recorder.Code, body)
	}

	if (draft == nil) || (draft.Key != "chat:-1001234567890") || (string(draft.Value) != "30") || (string(draft.Expected) != "null") {
		t.Errorf("draft = %+v", draft)
	}

	if (request["kind"] != "special_limit") || (request["id"] != float64(51)) {
		t.Errorf("request = %v", request)
	}
}

func TestLimitRequestResetSendsSQLNull(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = limitsRegistry(t)
	recorder := limitRequest(`{"scope": "user", "id": 7, "limit": null, "expected": 1500}`).do(newTestServer(store))
	draft := store.limits.draft

	if (recorder.Code != http.StatusAccepted) || (draft == nil) || (draft.Value != nil) || (string(draft.Expected) != "1500") || (draft.Key != "user:7") {
		t.Errorf("status = %d, draft = %+v", recorder.Code, draft)
	}
}

func TestBadLimitRequestsAre400(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		message string
	}{
		{"scope", `{"scope": "global", "id": 7, "limit": 30, "expected": null}`, errLimitScope},
		{"person sign", `{"scope": "user", "id": -7, "limit": 30, "expected": null}`, errLimitID},
		{"group sign", `{"scope": "chat", "id": 7, "limit": 30, "expected": null}`, errLimitID},
		{"above the max", `{"scope": "user", "id": 7, "limit": 1001, "expected": null}`, "from 0 to 1000"},
		{"unlimited", `{"scope": "user", "id": 7, "limit": -1, "expected": null}`, "unlimited only with /gated"},
		{"not whole", `{"scope": "user", "id": 7, "limit": 10.5, "expected": null}`, "from 0 to 1000"},
		{"text", `{"scope": "user", "id": 7, "limit": "10", "expected": null}`, "from 0 to 1000"},
		{"missing limit", `{"scope": "user", "id": 7, "expected": null}`, "limit is required"},
		{"missing expected", `{"scope": "user", "id": 7, "limit": 30}`, "expected is required"},
		{"expected -2", `{"scope": "user", "id": 7, "limit": 30, "expected": -2}`, errLimitExpectedForm},
		{"unknown field", `{"scope": "user", "id": 7, "limit": 30, "expected": null, "enabled": true}`, "unknown field"},
	}

	for _, testCase := range cases {
		store := &stubStore{}
		store.settings.registry = limitsRegistry(t)
		recorder := limitRequest(testCase.body).do(newTestServer(store))
		body := decodeBody(t, recorder)
		message, _ := body["error"].(string)

		if (recorder.Code != http.StatusBadRequest) || (strings.Contains(message, testCase.message) == false) || (store.limits.draft != nil) {
			t.Errorf("%s: status = %d, error = %q", testCase.name, recorder.Code, message)
		}
	}
}

func TestLimitRequestBeforeRulesIs409(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	recorder := limitRequest(`{"scope": "user", "id": 7, "limit": 30, "expected": null}`).do(newTestServer(store))
	body := decodeBody(t, recorder)

	if (recorder.Code != http.StatusConflict) || (body["error"] != ErrLimitsNotSupported.Error()) || (store.limits.draft != nil) {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestLimitRequestOpenIs409(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = limitsRegistry(t)
	store.limits.queueErr = ErrLimitRequestOpen
	recorder := limitRequest(`{"scope": "user", "id": 7, "limit": 30, "expected": null}`).do(newTestServer(store))
	body := decodeBody(t, recorder)

	if (recorder.Code != http.StatusConflict) || (body["error"] != "a change of this limit is already waiting") {
		t.Errorf("status = %d, body = %v", recorder.Code, body)
	}
}

func TestKindCheckViolationIs409(t *testing.T) {
	kindErr := &pq.Error{Code: checkViolation, Constraint: kindCheckConstraint}
	valueErr := &pq.Error{Code: checkViolation, Constraint: "settings_change_requests_special_limit_value_check"}
	openErr := &pq.Error{Code: uniqueViolation, Constraint: openRequestIndex}

	if (limitQueueError(kindErr) != ErrLimitsNotSupported) || (limitQueueError(valueErr) != ErrLimitRejectedByDB) || (limitQueueError(openErr) != ErrLimitRequestOpen) {
		t.Errorf("kind = %v, value = %v, open = %v", limitQueueError(kindErr), limitQueueError(valueErr), limitQueueError(openErr))
	}

	statuses := map[error]int{ErrLimitsNotSupported: http.StatusConflict, ErrLimitRejectedByDB: http.StatusBadRequest}

	for queueErr, status := range statuses {
		store := &stubStore{}
		store.settings.registry = limitsRegistry(t)
		store.limits.queueErr = queueErr
		recorder := limitRequest(`{"scope": "user", "id": 7, "limit": 30, "expected": null}`).do(newTestServer(store))

		if recorder.Code != status {
			t.Errorf("%v: status = %d", queueErr, recorder.Code)
		}
	}
}

func TestLimitScopeValidatesScopeAndId(t *testing.T) {
	cases := map[string]string{
		"/api/limits/scope?scope=team&id=7":                    errLimitScope,
		"/api/limits/scope?scope=user&id=-7":                   errLimitID,
		"/api/limits/scope?scope=chat&id=7":                    errLimitID,
		"/api/limits/scope?scope=user&id=x":                    errLimitID,
		"/api/limits/scope?scope=user&id=99999999999999999999": errLimitID,
		"/api/limits/scope?scope=user":                         errLimitID,
	}

	for target, message := range cases {
		store := &stubStore{}
		recorder := doRequest(newTestServer(store), http.MethodGet, target)
		body := decodeBody(t, recorder)

		if (recorder.Code != http.StatusBadRequest) || (body["error"] != message) || (store.limits.calls != 0) {
			t.Errorf("%s: status = %d, body = %v", target, recorder.Code, body)
		}
	}

	store := &stubStore{}
	store.limits.snapshot = testLimitsSnapshot(testNow)
	store.limits.scopeRows = []LimitRow{{Scope: "chat", ScopeID: -1001, Used: 0, StoredLimit: int64Ref(1500), Enabled: true}}
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/limits/scope?scope=chat&id=-1001")
	answer := LimitScopeResponse{}

	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); (err != nil) || (recorder.Code != http.StatusOK) {
		t.Fatalf("status = %d, err = %v", recorder.Code, err)
	}

	isRow := (answer.Row.ScopeID == -1001) && (answer.Row.State == limitStateOK) && (answer.Row.OpenRequest != nil) && (*answer.Row.StoredLimit == 1500)

	if (store.limits.scope != "chat") || (store.limits.id != -1001) || (isRow == false) || (answer.Rules == nil) || (answer.Applying == false) {
		t.Errorf("answer = %+v", answer)
	}
}

func TestSettingsAuditSpecialLimitsGroupUsesTheKeyPattern(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings/audit?group=special_limits")
	filter := store.settings.filter

	if (recorder.Code != http.StatusOK) || (filter == nil) || (filter.KeyPrefix != `special\_%`) || (filter.Keys != nil) {
		t.Fatalf("status = %d, filter = %+v", recorder.Code, filter)
	}
}

func TestSettingsAuditSpecialLimitsGroupWithARegistryKeyMatchesNothing(t *testing.T) {
	store := &stubStore{}
	store.settings.registry = testRegistry(t)
	recorder := doRequest(newTestServer(store), http.MethodGet, "/api/settings/audit?group=special_limits&key=platform.tiktok.enabled")
	filter := store.settings.filter

	if (recorder.Code != http.StatusOK) || (filter == nil) || (filter.Keys == nil) || (len(filter.Keys) != 0) {
		t.Fatalf("status = %d, filter = %+v", recorder.Code, filter)
	}
}

func TestMoneyHasNoGatedCurrent(t *testing.T) {
	body := decodeBody(t, doRequest(newTestServer(&stubStore{}), http.MethodGet, "/api/money"))

	if _, isPresent := body["gated_current"]; isPresent == true {
		t.Errorf("body = %v", body)
	}
}
