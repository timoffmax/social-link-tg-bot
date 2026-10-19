package botsettings

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/config/overrides"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// fakeRequest is one request of the fake store; request.IsExpired stands
// for the DB clock having passed its expires_at.
type fakeRequest struct {
	request ChangeRequest
	status  string
	err     string
}

// fakeExpiresAt is the expiry the fake store gives an awaiting request.
var fakeExpiresAt = time.Date(2026, 10, 19, 12, 15, 0, 0, time.UTC)

// fakeStore is an in-memory Store with the same decide-and-record contract
// as pgStore.
type fakeStore struct {
	mu       sync.Mutex
	rows     map[string]json.RawMessage
	requests []*fakeRequest
	statuses map[string]json.RawMessage
	loadErr  error
	nextID   int64
	// decideErr fails DecideApproval without changing anything.
	decideErr error
	// isHanging makes LoadSettings and PublishStatus block until their
	// context is done, like a database that accepts the connection but
	// never answers.
	isHanging bool
}

func newFakeStore() *fakeStore {
	result := &fakeStore{rows: map[string]json.RawMessage{}, statuses: map[string]json.RawMessage{}}

	return result
}

// waitIfHanging blocks until ctx is done while the store hangs.
func (s *fakeStore) waitIfHanging(ctx context.Context) error {
	s.mu.Lock()
	isHanging := s.isHanging
	s.mu.Unlock()
	var err error

	if isHanging == true {
		<-ctx.Done()
		err = ctx.Err()
	}

	return err
}

func (s *fakeStore) LoadSettings(ctx context.Context) ([]StoredValue, error) {
	err := s.waitIfHanging(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []StoredValue{}

	for key, value := range s.rows {
		result = append(result, StoredValue{Key: key, Value: value})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })

	if err == nil {
		err = s.loadErr
	}

	return result, err
}

func (s *fakeStore) ProcessNextRequest(ctx context.Context, decide func(ChangeRequest) Decision) (ChangeRequest, Decision, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var claimed *fakeRequest

	for _, queued := range s.requests {
		if (claimed == nil) && (queued.status == settings.StatusQueued) {
			claimed = queued
		}
	}

	request := ChangeRequest{}
	decision := Decision{}

	if claimed != nil {
		request = claimed.request
		request.StoredCurrent = s.rows[request.Key]
		request.AwaitingCount = s.countStatus(settings.StatusAwaitingApproval)
		decision = decideClaimed(request, decide)
		claimed.status = decision.Status
		claimed.err = decision.Error
	}

	if (claimed != nil) && (decision.IsAwaitingApproval() == true) {
		decision.ExpiresAt = fakeExpiresAt
		claimed.request.IsExpired = false
	}

	if (claimed != nil) && (decision.IsApplied() == true) && (decision.Value == nil) {
		delete(s.rows, request.Key)
	}

	if (claimed != nil) && (decision.IsApplied() == true) && (decision.Value != nil) {
		s.rows[request.Key] = decision.Value
	}

	return request, decision, (claimed != nil), nil
}

func (s *fakeStore) countStatus(status string) int {
	result := 0

	for _, request := range s.requests {
		if request.status == status {
			result++
		}
	}

	return result
}

func (s *fakeStore) find(requestID int64) *fakeRequest {
	var result *fakeRequest

	for _, request := range s.requests {
		if request.request.ID == requestID {
			result = request
		}
	}

	return result
}

func (s *fakeStore) ExpireRequests(ctx context.Context) ([]ChangeRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []ChangeRequest{}

	for _, open := range s.requests {
		isOpen := (open.status == settings.StatusQueued) || (open.status == settings.StatusAwaitingApproval)

		if (isOpen == true) && (open.request.IsExpired == true) {
			open.err = requestApprovalExpiredError

			if open.status == settings.StatusQueued {
				open.err = requestExpiredError
			}

			open.status = settings.StatusExpired
			result = append(result, open.request)
		}
	}

	return result, nil
}

func (s *fakeStore) SetApprovalMessage(ctx context.Context, requestID int64, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.find(requestID)

	if (found != nil) && (found.status == settings.StatusAwaitingApproval) {
		found.request.ApprovalMessageID = messageID
	}

	return nil
}

func (s *fakeStore) RejectAwaiting(ctx context.Context, requestID int64, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.find(requestID)
	result := (found != nil) && (found.status == settings.StatusAwaitingApproval)

	if result == true {
		found.status = settings.StatusRejected
		found.err = reason
	}

	return result, nil
}

// DecideApproval mirrors pgStore: a conditional claim of an awaiting,
// unexpired request, else the request's state (expiring it when due).
func (s *fakeStore) DecideApproval(ctx context.Context, requestID int64, isApprove bool, decide func(ChangeRequest) Decision) (ApprovalOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ApprovalOutcome{}
	found := s.find(requestID)
	isAwaiting := (found != nil) && (found.status == settings.StatusAwaitingApproval) && (s.decideErr == nil)
	isClaimable := (isAwaiting == true) && (found.request.IsExpired == false)

	if (isAwaiting == true) && (isClaimable == false) {
		found.status = settings.StatusExpired
		found.err = requestApprovalExpiredError
	}

	if isClaimable == true {
		result = s.decideFound(found, isApprove, decide)
	}

	if (isClaimable == false) && (found != nil) {
		result = ApprovalOutcome{Request: found.request, Status: found.status, Error: found.err}
	}

	return result, s.decideErr
}

func (s *fakeStore) decideFound(found *fakeRequest, isApprove bool, decide func(ChangeRequest) Decision) ApprovalOutcome {
	request := found.request
	request.StoredCurrent = s.rows[request.Key]
	decision := rejected(requestOwnerRejectedError)

	if isApprove == true {
		decision = decide(request)
	}

	if (decision.IsApplied() == true) && (decision.Value == nil) {
		delete(s.rows, request.Key)
	}

	if (decision.IsApplied() == true) && (decision.Value != nil) {
		s.rows[request.Key] = decision.Value
	}

	found.status = decision.Status
	found.err = decision.Error
	result := ApprovalOutcome{Request: request, Decision: decision, Status: decision.Status, Error: decision.Error}

	return result
}

func (s *fakeStore) PublishStatus(ctx context.Context, component string, data json.RawMessage) error {
	err := s.waitIfHanging(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil {
		s.statuses[component] = data
	}

	return err
}

// queue adds a request: value "" is a reset.
func (s *fakeStore) queue(key string, value string, expected string) *fakeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	request := ChangeRequest{ID: s.nextID, Kind: settings.KindSetting, Key: key, Expected: json.RawMessage(expected), Source: settings.ActorDashboard}

	if value != "" {
		request.Value = json.RawMessage(value)
	}

	result := &fakeRequest{request: request, status: settings.StatusQueued}
	s.requests = append(s.requests, result)

	return result
}

func (s *fakeStore) requestStatus(request *fakeRequest) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return request.status, request.err
}

func (s *fakeStore) expire(request *fakeRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request.request.IsExpired = true
}

func (s *fakeStore) messageID(request *fakeRequest) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return request.request.ApprovalMessageID
}

func (s *fakeStore) setRow(key string, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[key] = json.RawMessage(value)
}

func (s *fakeStore) status(component string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.statuses[component]
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

// fakePusher records profile pushes.
type fakePusher struct {
	mu     sync.Mutex
	pushes int
	failN  int
}

func (p *fakePusher) push(known map[string]string) (map[string]string, []string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushes++
	var err error

	if p.failN > 0 {
		p.failN--
		err = errors.New("Too Many Requests: retry after 5")
	}

	return map[string]string{"setMyDescription": "x"}, []string{"setMyDescription"}, err
}

func (p *fakePusher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.pushes
}

// testHarness is a manager on a fake store, clock and pusher.
type testHarness struct {
	manager *Manager
	store   *fakeStore
	clock   *fakeClock
	pusher  *fakePusher
	alerts  *[]string
	cache   string
}

// newHarness builds a manager; store may be nil (no database).
func newHarness(t *testing.T, store Store) testHarness {
	alerts := []string{}
	var alertsMu sync.Mutex
	clock := &fakeClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	pusher := &fakePusher{}
	cache := t.TempDir() + "/var/settings-cache.json"
	options := Options{
		Store:     store,
		CachePath: cache,
		Notify: func(text string) bool {
			alertsMu.Lock()
			defer alertsMu.Unlock()
			alerts = append(alerts, text)

			return true
		},
		PushProfile: pusher.push,
		Now:         clock.Now,
	}
	previous := overrides.Current()
	t.Cleanup(func() { overrides.Set(previous) })
	fake, _ := store.(*fakeStore)
	result := testHarness{manager: New(options), store: fake, clock: clock, pusher: pusher, alerts: &alerts, cache: cache}

	return result
}

// clearSettingsEnv unsets the .env variable of every managed setting.
func clearSettingsEnv(t *testing.T) {
	for _, setting := range settings.All() {
		t.Setenv(setting.EnvVar, "")
	}
}
