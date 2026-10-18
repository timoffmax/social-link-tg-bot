package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timoffmax/social-link-bot/model/api/request"
	"github.com/timoffmax/social-link-bot/model/api/request/message"
	"github.com/timoffmax/social-link-bot/model/api/response"
	"github.com/timoffmax/social-link-bot/services"
)

// outboxBlockedUserID is a user who blocked the bot in the fake Bot API.
const outboxBlockedUserID = 666

// outboxBlockedError is the fake Bot API's answer for outboxBlockedUserID.
const outboxBlockedError = "Forbidden: bot was blocked by the user"

// outboxRateLimitedUserID gets 429 Too Many Requests with outboxRetryAfter.
const outboxRateLimitedUserID = 429

// outboxRetryAfter is the retry_after of the fake Bot API's 429, in seconds.
const outboxRetryAfter = 7

// outboxServerErrorUserID gets a 502 without a JSON body.
const outboxServerErrorUserID = 502

// outboxMessageID is the message id the fake Bot API gives sent messages.
const outboxMessageID = 4242

// outboxOwnerID is the owner's chat in these tests.
const outboxOwnerID = 1

// fakeReplyStore keeps report replies in memory in place of the database.
type fakeReplyStore struct {
	mu          sync.Mutex
	queued      []services.ClaimedReportReply
	interrupted []services.ReportReply
	created     []services.ReportReply
	sent        map[int]int64
	failed      map[int]string
	unknown     map[int]string
	requeued    map[int]requeuedReply
	resolved    []int
	alerts      []string
	reports     map[int]*services.Report
	nextID      int
}

func (s *fakeReplyStore) claim() (services.ClaimedReportReply, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result services.ClaimedReportReply
	isClaimed := (len(s.queued) > 0)

	if isClaimed == true {
		result = s.queued[0]
		s.queued = s.queued[1:]
	}

	return result, isClaimed, nil
}

func (s *fakeReplyStore) markSent(id int, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[id] = messageID

	return nil
}

func (s *fakeReplyStore) markFailed(id int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[id] = reason

	return nil
}

func (s *fakeReplyStore) markUnknown(id int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unknown[id] = reason

	return nil
}

// requeuedReply is a reply put back in the queue.
type requeuedReply struct {
	reason    string
	notBefore time.Time
}

func (s *fakeReplyStore) requeue(id int, reason string, notBefore time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requeued[id] = requeuedReply{reason: reason, notBefore: notBefore}

	return nil
}

func (s *fakeReplyStore) markInterrupted() ([]services.ReportReply, error) {
	return s.interrupted, nil
}

func (s *fakeReplyStore) create(reportID int, text, source, status string) (services.ReportReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	reply := services.ReportReply{ID: s.nextID, ReportID: reportID, Text: text, Source: source, Status: status}
	s.created = append(s.created, reply)

	return reply, nil
}

func (s *fakeReplyStore) get(id int) (*services.Report, error) {
	var err error
	report, isFound := s.reports[id]

	if isFound == false {
		err = fmt.Errorf("report %d not found", id)
	}

	return report, err
}

func (s *fakeReplyStore) resolve(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolved = append(s.resolved, id)

	return nil
}

func (s *fakeReplyStore) notify(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = append(s.alerts, text)

	return true
}

// startOutboxTest swaps the store and owner alerts for a fake and the Bot API
// for a test server that refuses messages to outboxBlockedUserID.
func startOutboxTest(t *testing.T) (*fakeReplyStore, *botAPIRecorder) {
	store := &fakeReplyStore{
		sent:     map[int]int64{},
		failed:   map[int]string{},
		unknown:  map[int]string{},
		requeued: map[int]requeuedReply{},
		reports:  map[int]*services.Report{},
		nextID:   100,
	}
	recorder := &botAPIRecorder{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := ioutil.ReadAll(r.Body)
		body := map[string]interface{}{}
		json.Unmarshal(raw, &body)
		parts := strings.Split(r.URL.Path, "/")
		recorder.mu.Lock()
		recorder.calls = append(recorder.calls, botAPICall{method: parts[len(parts)-1], body: body})
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		answer := fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":1}}`, outboxMessageID)

		switch body["chat_id"] {
		case float64(outboxBlockedUserID):
			w.WriteHeader(http.StatusForbidden)
			answer = fmt.Sprintf(`{"ok":false,"error_code":403,"description":%q}`, outboxBlockedError)
		case float64(outboxRateLimitedUserID):
			w.WriteHeader(http.StatusTooManyRequests)
			answer = fmt.Sprintf(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after %d","parameters":{"retry_after":%d}}`, outboxRetryAfter, outboxRetryAfter)
		case float64(outboxServerErrorUserID):
			w.WriteHeader(http.StatusBadGateway)
			answer = "<html>Bad Gateway</html>"
		}

		w.Write([]byte(answer))
	}))

	t.Setenv("TG_BOT_TOKEN", "test-token")
	t.Setenv("ADMIN_USER_ID", fmt.Sprint(outboxOwnerID))
	previousTemplate := request.SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")
	previousClaim := claimNextReportReply
	previousSent := markReportReplySent
	previousFailed := markReportReplyFailed
	previousUnknown := markReportReplyUnknown
	previousRequeue := requeueReportReply
	previousInterrupted := markInterruptedReportReplies
	previousCreate := createReportReply
	previousGet := getReport
	previousResolve := resolveReport
	previousNotify := notifyOwner
	claimNextReportReply = store.claim
	markReportReplySent = store.markSent
	markReportReplyFailed = store.markFailed
	markReportReplyUnknown = store.markUnknown
	requeueReportReply = store.requeue
	markInterruptedReportReplies = store.markInterrupted
	createReportReply = store.create
	getReport = store.get
	resolveReport = store.resolve
	notifyOwner = store.notify

	t.Cleanup(func() {
		request.SetAPIURLTemplate(previousTemplate)
		server.Close()
		claimNextReportReply = previousClaim
		markReportReplySent = previousSent
		markReportReplyFailed = previousFailed
		markReportReplyUnknown = previousUnknown
		requeueReportReply = previousRequeue
		markInterruptedReportReplies = previousInterrupted
		createReportReply = previousCreate
		getReport = previousGet
		resolveReport = previousResolve
		notifyOwner = previousNotify
	})

	return store, recorder
}

// queuedReply is a claimed dashboard reply to userID.
func queuedReply(id, reportID int, userID int64, text string) services.ClaimedReportReply {
	reply := services.ReportReply{ID: id, ReportID: reportID, Text: text, Source: services.ReportReplySourceDashboard, Status: services.ReportReplyStatusSending}
	result := services.ClaimedReportReply{Reply: reply, UserID: userID}

	return result
}

func TestOutboxSendsQueuedRepliesInTheReplyFormatAndMarksThemSent(t *testing.T) {
	store, recorder := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(1, 10, 42, "Fixed <b>now</b>"), queuedReply(2, 11, 43, "Thanks")}

	count := sendQueuedReportReplies(context.Background())
	calls := recorder.all()

	if (count != 2) || (len(calls) != 2) || (store.sent[1] != outboxMessageID) || (store.sent[2] != outboxMessageID) || (len(store.failed) != 0) || (len(store.alerts) != 0) {
		t.Fatalf("expected two sent replies, got count=%d calls=%+v store=%+v", count, calls, store)
	}

	first := calls[0].body

	if (first["chat_id"] != float64(42)) || (first["parse_mode"] != "HTML") || (first["text"] != "💬 <b>Reply from the developer:</b>\n\nFixed <b>now</b>") {
		t.Fatalf("unexpected reply message: %+v", first)
	}
}

func TestOutboxMarksATelegramRefusalFailedAndTellsTheOwner(t *testing.T) {
	store, _ := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(3, 12, outboxBlockedUserID, "hello")}

	count := sendQueuedReportReplies(context.Background())

	if (count != 1) || (store.failed[3] != outboxBlockedError) || (len(store.sent) != 0) || (len(store.alerts) != 1) {
		t.Fatalf("expected one failed reply and one alert, got count=%d store=%+v", count, store)
	}

	alert := store.alerts[0]
	isComplete := strings.Contains(alert, "report #12") && strings.Contains(alert, fmt.Sprint(outboxBlockedUserID)) && strings.Contains(alert, outboxBlockedError)

	if isComplete == false {
		t.Fatalf("expected the report, user and error in the alert, got %q", alert)
	}
}

func TestOutboxWithNothingQueuedSendsNothing(t *testing.T) {
	store, recorder := startOutboxTest(t)

	count := sendQueuedReportReplies(context.Background())

	if (count != 0) || (len(recorder.all()) != 0) || (len(store.alerts) != 0) {
		t.Fatalf("expected nothing sent, got %d %+v", count, recorder.all())
	}
}

func TestInterruptedRepliesAreNotResentAndOnlyDashboardOnesAlertTheOwner(t *testing.T) {
	store, recorder := startOutboxTest(t)
	store.reports[20] = &services.Report{ID: 20, UserID: 77}
	interruptedError := sql.NullString{String: services.ReportReplyInterruptedError, Valid: true}
	store.interrupted = []services.ReportReply{
		{ID: 5, ReportID: 20, Source: services.ReportReplySourceDashboard, Status: services.ReportReplyStatusUnknown, Error: interruptedError},
		{ID: 6, ReportID: 20, Source: services.ReportReplySourceTelegram, Status: services.ReportReplyStatusUnknown, Error: interruptedError},
	}

	markInterruptedReplies()

	if (len(recorder.all()) != 0) || (len(store.alerts) != 1) || (len(store.sent) != 0) {
		t.Fatalf("expected one alert and nothing sent, got calls=%+v store=%+v", recorder.all(), store)
	}

	alert := store.alerts[0]
	isComplete := strings.Contains(alert, "#5") && strings.Contains(alert, "user 77") && strings.Contains(alert, "may have been delivered") && strings.Contains(alert, services.ReportReplyInterruptedError)

	if isComplete == false {
		t.Fatalf("unexpected alert %q", alert)
	}
}

// ownerReply runs /reply as the owner and returns the owner's answer.
func ownerReply(t *testing.T, recorder *botAPIRecorder, text string) string {
	handleReplyCommand(&response.TgMessage{MessageId: 9, Text: text}, &response.TgChat{Id: outboxOwnerID}, &response.TgUser{Id: outboxOwnerID})

	calls := recorder.all()
	ownerCalls := callsTo(calls, float64(outboxOwnerID))

	if len(ownerCalls) != 1 {
		t.Fatalf("expected one answer to the owner, got %+v", calls)
	}

	result, _ := ownerCalls[0].body["text"].(string)

	return result
}

func TestReplyCommandRecordsTheReplySendsItAndResolvesTheReport(t *testing.T) {
	store, recorder := startOutboxTest(t)
	store.reports[30] = &services.Report{ID: 30, UserID: 42}

	answer := ownerReply(t, recorder, "/reply 30 Fixed,\nthanks")
	userCalls := callsTo(recorder.all(), 42)
	isRecorded := (len(store.created) == 1) && (store.created[0].Source == services.ReportReplySourceTelegram) && (store.created[0].Status == services.ReportReplyStatusSending) && (store.created[0].Text == "Fixed,\nthanks")

	if (isRecorded == false) || (store.sent[101] != outboxMessageID) || (len(store.resolved) != 1) || (len(userCalls) != 1) {
		t.Fatalf("expected a recorded, sent and resolved reply, got %+v", store)
	}

	if (userCalls[0].body["text"] != "💬 <b>Reply from the developer:</b>\n\nFixed,\nthanks") || (strings.Contains(answer, "report #30 resolved") == false) {
		t.Fatalf("unexpected messages: %+v %q", userCalls[0].body, answer)
	}
}

func TestReplyCommandReportsTelegramsReasonAndKeepsTheReportOpen(t *testing.T) {
	store, recorder := startOutboxTest(t)
	store.reports[31] = &services.Report{ID: 31, UserID: outboxBlockedUserID}

	answer := ownerReply(t, recorder, "/reply 31 hello")

	if (store.failed[101] != outboxBlockedError) || (len(store.sent) != 0) || (len(store.resolved) != 0) || (strings.Contains(answer, outboxBlockedError) == false) {
		t.Fatalf("expected a failed reply reported to the owner, got %+v %q", store, answer)
	}
}

func TestReplyCommandAnswersAnUnknownReport(t *testing.T) {
	store, recorder := startOutboxTest(t)

	answer := ownerReply(t, recorder, "/reply 99 hello")

	if (answer != "Report #99 not found.") || (len(store.created) != 0) {
		t.Fatalf("expected not found, got %q %+v", answer, store)
	}
}

func TestReplyCommandWithoutTextShowsTheUsage(t *testing.T) {
	_, recorder := startOutboxTest(t)

	answer := ownerReply(t, recorder, "/reply 5")

	if answer != replyUsageText {
		t.Fatalf("expected the usage, got %q", answer)
	}
}

func TestTelegramSendErrorKeepsTheTransportErrorWithoutADescription(t *testing.T) {
	transportErr := errors.New("sendMessage: connection refused")
	sent := &message.SendMessage{Err: transportErr, ResponsePayload: &response.TgMessage{}}

	err := telegramSendError(sent)

	if (errors.Is(err, transportErr) == false) || (isReplyDeliveryUnknown(err) == false) {
		t.Fatalf("expected the transport error as delivery unknown, got %v", err)
	}
}

func TestTelegramSendErrorKeepsAnAnsweredErrorAsNotDelivered(t *testing.T) {
	apiErr := errors.New("API error: 400 - ")
	sent := &message.SendMessage{Err: apiErr, Response: request.ResponseInfo{StatusCode: http.StatusBadRequest, HTTPStatus: http.StatusBadRequest}, ResponsePayload: &response.TgMessage{}}

	err := telegramSendError(sent)
	var retryErr replyRetryableError

	if (errors.Is(err, apiErr) == false) || (isReplyDeliveryUnknown(err) == true) || (errors.As(err, &retryErr) == true) {
		t.Fatalf("expected the answered error, got %v", err)
	}
}

func TestTelegramSendErrorOfARequestThatNeverLeftIsNotSent(t *testing.T) {
	buildErr := fmt.Errorf("%w: sendMessage: invalid URL", request.ErrNotSent)
	sent := &message.SendMessage{Err: buildErr, ResponsePayload: &response.TgMessage{}}

	err := telegramSendError(sent)
	var retryErr replyRetryableError

	if (errors.Is(err, request.ErrNotSent) == false) || (isReplyDeliveryUnknown(err) == true) || (errors.As(err, &retryErr) == true) {
		t.Fatalf("expected a definite not sent, got %v", err)
	}
}

func TestTelegramSendErrorOfASuccessWithoutAMessageIDIsDeliveryUnknown(t *testing.T) {
	sent := &message.SendMessage{Response: request.ResponseInfo{StatusCode: http.StatusOK, HTTPStatus: http.StatusOK}, ResponsePayload: &response.TgMessage{}}

	err := telegramSendError(sent)

	if isReplyDeliveryUnknown(err) == false {
		t.Fatalf("expected delivery unknown, got %v", err)
	}
}

func TestOutboxMarksARequestThatCanNotBeBuiltFailedNotUnknown(t *testing.T) {
	store, _ := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(9, 19, 42, "hello")}
	previousTemplate := request.SetAPIURLTemplate("http://bad host/bot{{token}}/{{method_name}}")
	t.Cleanup(func() { request.SetAPIURLTemplate(previousTemplate) })

	count := sendQueuedReportReplies(context.Background())

	if (count != 1) || (strings.Contains(store.failed[9], request.ErrNotSent.Error()) == false) || (len(store.unknown) != 0) || (len(store.requeued) != 0) || (len(store.alerts) != 1) {
		t.Fatalf("expected a failed reply with the real reason, got count=%d store=%+v", count, store)
	}

	if strings.Contains(store.alerts[0], "was not sent") == false {
		t.Fatalf("expected a not sent alert, got %q", store.alerts[0])
	}
}

func TestOutboxRequeuesARateLimitedReplyHonouringRetryAfterAndStopsTheBatch(t *testing.T) {
	store, recorder := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(1, 10, outboxRateLimitedUserID, "hello"), queuedReply(2, 11, 42, "next")}
	before := time.Now()

	count := sendQueuedReportReplies(context.Background())
	requeued, isRequeued := store.requeued[1]
	wait := requeued.notBefore.Sub(before)

	if (count != 1) || (len(recorder.all()) != 1) || (isRequeued == false) || (len(store.failed) != 0) || (len(store.alerts) != 0) || (len(store.queued) != 1) {
		t.Fatalf("expected one requeued reply and the batch stopped, got count=%d store=%+v", count, store)
	}

	if (wait < outboxRetryAfter*time.Second) || (wait > (outboxRetryAfter+5)*time.Second) || (strings.Contains(requeued.reason, "Too Many Requests") == false) {
		t.Fatalf("expected a retry after %ds, got %s %q", outboxRetryAfter, wait, requeued.reason)
	}
}

func TestOutboxRequeuesAServerErrorWithBackoffAndGoesOn(t *testing.T) {
	store, _ := startOutboxTest(t)
	failing := queuedReply(1, 10, outboxServerErrorUserID, "hello")
	failing.Reply.Attempts = 1
	store.queued = []services.ClaimedReportReply{failing, queuedReply(2, 11, 42, "next")}
	before := time.Now()

	count := sendQueuedReportReplies(context.Background())
	requeued, isRequeued := store.requeued[1]
	wait := requeued.notBefore.Sub(before)

	if (count != 2) || (isRequeued == false) || (store.sent[2] != outboxMessageID) || (len(store.alerts) != 0) || (len(store.failed) != 0) {
		t.Fatalf("expected the 502 requeued and the next reply sent, got count=%d store=%+v", count, store)
	}

	if (wait < 2*reportReplyRetryDelay) || (wait > 2*reportReplyRetryDelay+5*time.Second) {
		t.Fatalf("expected the second attempt's backoff, got %s", wait)
	}
}

func TestOutboxFailsARateLimitedReplyAfterTheLastAttemptAndTellsTheOwner(t *testing.T) {
	store, _ := startOutboxTest(t)
	last := queuedReply(1, 10, outboxRateLimitedUserID, "hello")
	last.Reply.Attempts = reportReplyMaxAttempts - 1
	store.queued = []services.ClaimedReportReply{last}

	count := sendQueuedReportReplies(context.Background())
	isGivenUp := strings.Contains(store.failed[1], "Too Many Requests") && strings.Contains(store.failed[1], fmt.Sprintf("gave up after %d attempts", reportReplyMaxAttempts))

	if (count != 1) || (isGivenUp == false) || (len(store.requeued) != 0) || (len(store.alerts) != 1) {
		t.Fatalf("expected a failed reply after the last attempt, got count=%d store=%+v", count, store)
	}
}

func TestReportReplyStuckWindowIsWellAboveTheTelegramTimeout(t *testing.T) {
	if services.ReportReplyStuckAfter < 2*request.ClientTimeout {
		t.Fatalf("stuck window %s must be well above the client timeout %s", services.ReportReplyStuckAfter, request.ClientTimeout)
	}
}

// dropBotAPIConnection makes the Bot API read each request and drop the
// connection without an answer.
func dropBotAPIConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ioutil.ReadAll(r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()

		if err == nil {
			conn.Close()
		}
	}))
	previousTemplate := request.SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")

	t.Cleanup(func() {
		request.SetAPIURLTemplate(previousTemplate)
		server.Close()
	})
}

// cutOffBotAPIAnswer makes the Bot API answer 200 and drop the connection
// before the end of the body.
func cutOffBotAPIAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ioutil.ReadAll(r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()

		if err == nil {
			conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 300\r\n\r\n{\"ok\":true,\"res"))
			conn.Close()
		}
	}))
	previousTemplate := request.SetAPIURLTemplate(server.URL + "/bot{{token}}/{{method_name}}")

	t.Cleanup(func() {
		request.SetAPIURLTemplate(previousTemplate)
		server.Close()
	})
}

func TestOutboxMarksAnAnswerCutOffBeforeItsEndDeliveryUnknown(t *testing.T) {
	store, _ := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(5, 14, 42, "hello")}
	cutOffBotAPIAnswer(t)

	count := sendQueuedReportReplies(context.Background())

	if (count != 1) || (len(store.failed) != 0) || (len(store.sent) != 0) || (strings.Contains(store.unknown[5], reportReplyDeliveryUnknown) == false) {
		t.Fatalf("expected delivery unknown, got count=%d store=%+v", count, store)
	}
}

func TestReplyCommandWithoutAnAnswerSaysDeliveryIsUnknown(t *testing.T) {
	store, _ := startOutboxTest(t)
	store.reports[32] = &services.Report{ID: 32, UserID: 42}
	dropBotAPIConnection(t)

	answer := replyToReport(32, "hi")
	isUnknown := strings.HasPrefix(answer, "⚠️ Reply to user 42 may have been delivered, report #32 stays open.") && strings.Contains(answer, reportReplyDeliveryUnknown)

	if (isUnknown == false) || (len(store.resolved) != 0) || (len(store.failed) != 0) || (strings.Contains(store.unknown[101], reportReplyDeliveryUnknown) == false) {
		t.Fatalf("expected delivery unknown, got %q %+v", answer, store)
	}
}

func TestOutboxWithoutAnAnswerTellsTheOwnerTheReplyMayHaveBeenDelivered(t *testing.T) {
	store, _ := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(4, 13, 42, "hello")}
	dropBotAPIConnection(t)

	count := sendQueuedReportReplies(context.Background())

	if (count != 1) || (len(store.alerts) != 1) || (strings.Contains(store.alerts[0], "may have been delivered, not resent") == false) || (len(store.failed) != 0) || (strings.Contains(store.unknown[4], reportReplyDeliveryUnknown) == false) {
		t.Fatalf("expected a delivery-unknown alert, got count=%d store=%+v", count, store)
	}
}

func TestOutboxSendsNothingOnceStopped(t *testing.T) {
	store, recorder := startOutboxTest(t)
	store.queued = []services.ClaimedReportReply{queuedReply(1, 10, 42, "hello")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	count := sendQueuedReportReplies(ctx)

	if (count != 0) || (len(recorder.all()) != 0) || (len(store.queued) != 1) {
		t.Fatalf("expected nothing sent after the stop, got count=%d calls=%+v", count, recorder.all())
	}
}

func TestOutboxLoopEndsWhenStopped(t *testing.T) {
	startOutboxTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	reportReplyOutboxWg.Add(1)
	go runReportReplyOutbox(ctx)
	cancel()
	done := make(chan struct{})

	go func() {
		waitForReportReplyOutbox()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expected the outbox loop to end after the stop")
	}
}

func TestRecordReplySentRetriesAFailedWrite(t *testing.T) {
	store, _ := startOutboxTest(t)
	attempts := 0
	markReportReplySent = func(id int, messageID int64) error {
		var err error
		attempts++

		if attempts == 1 {
			err = sql.ErrConnDone
		}

		if attempts > 1 {
			err = store.markSent(id, messageID)
		}

		return err
	}

	recordReplySent(8, outboxMessageID)

	if (attempts != 2) || (store.sent[8] != outboxMessageID) {
		t.Fatalf("expected the sent write retried once, got attempts=%d sent=%+v", attempts, store.sent)
	}
}
