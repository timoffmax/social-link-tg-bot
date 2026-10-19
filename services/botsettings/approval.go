package botsettings

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/model/settings"
)

// Callback data of the approval buttons: a prefix and the request id only.
const (
	ApproveCallbackPrefix = "setappr:"
	RejectCallbackPrefix  = "setrej:"
)

// Approval message texts: rendered by the bot from the registry and the
// validated values only, never from text the dashboard supplied.
const (
	approvalHeading      = "⚙️ The dashboard asks to change a setting:"
	approvalAskFooter    = "Request #%d, expires at %s %s."
	approvalFinalFooter  = "Request #%d"
	approvalApproveLabel = "✅ Approve"
	approvalRejectLabel  = "❌ Reject"
	approvalTimeLayout   = "15:04"
	approvalZoneKyiv     = "Kyiv time"
	approvalZoneUTC      = "UTC"
)

// Final states shown in the approval message once the request is decided.
const (
	approvalStateApplied    = "✅ Applied"
	approvalStateRejected   = "❌ Rejected"
	approvalStateExpired    = "⌛ Expired"
	approvalStateChanged    = "Changed meanwhile, not applied"
	approvalStateNotApplied = "❌ Not applied: %s"
)

// Answers to the owner's tap (the callback toast).
const (
	tapAppliedText     = "✅ Applied."
	tapRejectedText    = "❌ Rejected."
	tapChangedText     = "The setting changed meanwhile: not applied."
	tapNotAppliedText  = "Not applied: %s"
	tapDecidedText     = "Already decided."
	tapExpiredText     = "⌛ This request has expired."
	tapCachedText      = "The bot is on cached settings; try again after it reconnects."
	tapFallbackText    = "The bot is on .env and defaults until it reads the settings database; try again later."
	tapBusyText        = "The bot is busy; try again in a moment."
	tapPendingText     = "Still working on it; the message will show the outcome."
	tapFailedText      = "Could not reach the settings database; try again."
	tapUnavailableText = "Settings are not available: the bot runs without a database."
)

// tapWait bounds how long a tap waits for the settings goroutine.
const tapWait = 10 * time.Second

// digestActorApproved names a change the owner approved in Telegram.
const digestActorApproved = "%s, approved in Telegram"

// ApprovalButton is one inline button of the approval message.
type ApprovalButton struct {
	Text string
	Data string
}

// Approver talks to the owner's private chat with the bot.
type Approver interface {
	// Ask sends the approval message with its buttons and returns its id.
	Ask(text string, buttons []ApprovalButton) (int64, error)
	// Edit replaces the message's text and removes its buttons.
	Edit(messageID int64, text string) error
}

// approvalTap is one owner tap handed to the settings goroutine.
type approvalTap struct {
	requestID int64
	messageID int64
	isApprove bool
	reply     chan string
}

// IsApprovalCallback reports callback data of an approval button.
func IsApprovalCallback(data string) bool {
	result := (strings.HasPrefix(data, ApproveCallbackPrefix) == true) || (strings.HasPrefix(data, RejectCallbackPrefix) == true)

	return result
}

// ParseApprovalCallback returns the request id, whether it is Approve, and
// whether data is a well-formed approval button.
func ParseApprovalCallback(data string) (int64, bool, bool) {
	approveID, isApprove := parseCallbackID(data, ApproveCallbackPrefix)
	rejectID, isReject := parseCallbackID(data, RejectCallbackPrefix)
	result := approveID

	if isReject == true {
		result = rejectID
	}

	isValid := (isApprove == true) || (isReject == true)

	return result, isApprove, isValid
}

// parseCallbackID reads a positive request id after prefix.
func parseCallbackID(data string, prefix string) (int64, bool) {
	result := int64(0)
	hasPrefix := strings.HasPrefix(data, prefix)
	rest := strings.TrimPrefix(data, prefix)
	id, err := strconv.ParseInt(rest, 10, 64)
	isValid := (hasPrefix == true) && (err == nil) && (id > 0)

	if isValid == true {
		result = id
	}

	return result, isValid
}

// approvalButtons are the Approve and Reject buttons of a request.
func approvalButtons(requestID int64) []ApprovalButton {
	approveData := fmt.Sprintf("%s%d", ApproveCallbackPrefix, requestID)
	rejectData := fmt.Sprintf("%s%d", RejectCallbackPrefix, requestID)
	result := []ApprovalButton{
		{Text: approvalApproveLabel, Data: approveData},
		{Text: approvalRejectLabel, Data: rejectData},
	}

	return result
}

// approvalChangeLine renders the change from the registry: the value the
// page showed (checked equal to the current one) and the validated new one.
func approvalChangeLine(request ChangeRequest) string {
	result := ""

	if request.Kind == settings.KindSpecialLimit {
		result = specialLimitApprovalLine(request)
	}

	if request.Kind != settings.KindSpecialLimit {
		result = settingApprovalLine(request)
	}

	return result
}

// settingApprovalLine renders the change of a registry setting.
func settingApprovalLine(request ChangeRequest) string {
	setting, _ := settings.Lookup(request.Key)
	oldValue := decodeValue(request.Expected)
	newValue, err := requestedValue(setting, request)

	if err != nil {
		newValue = decodeValue(request.Value)
	}

	result := describeChange(request.Key, oldValue, newValue, request.IsReset())

	return result
}

// specialLimitApprovalLine renders a limit request (turning unlimited on)
// from its key, expected and value.
func specialLimitApprovalLine(request ChangeRequest) string {
	target, _ := settings.ParseSpecialLimitKey(request.Key)
	oldLimit, _ := settings.ParseStoredSpecialLimit(request.Expected)
	newLimit, _ := settings.ParseStoredSpecialLimit(request.Value)
	change := specialLimitChange{Target: target, Old: oldLimit, New: newLimit}
	result := describeSpecialLimitChange(change)

	return result
}

// decodeValue decodes a JSON value; an undecodable one is shown as is.
func decodeValue(raw json.RawMessage) any {
	var result any
	err := json.Unmarshal(raw, &result)

	if err != nil {
		result = string(raw)
	}

	return result
}

// renderApprovalAsk is the message that asks the owner.
func (m *Manager) renderApprovalAsk(request ChangeRequest, expiresAt time.Time) string {
	line := approvalChangeLine(request)
	location := m.digest.location
	at := expiresAt.In(location)
	atText := at.Format(approvalTimeLayout)
	zone := approvalZone(location)
	footer := fmt.Sprintf(approvalAskFooter, request.ID, atText, zone)
	result := strings.Join([]string{approvalHeading, line, footer}, "\n")

	return result
}

// approvalZone names the zone of the expiry time.
func approvalZone(location *time.Location) string {
	result := approvalZoneKyiv

	if location == time.UTC {
		result = approvalZoneUTC
	}

	return result
}

// renderApprovalFinal is the message once the request is decided: the same
// change line, without the buttons, with the outcome.
func renderApprovalFinal(request ChangeRequest, status string, reason string) string {
	line := approvalChangeLine(request)
	footer := fmt.Sprintf(approvalFinalFooter, request.ID)
	state := approvalState(status, reason)
	result := strings.Join([]string{approvalHeading, line, footer, "", state}, "\n")

	return result
}

// approvalState names a final outcome.
func approvalState(status string, reason string) string {
	result := fmt.Sprintf(approvalStateNotApplied, reason)

	switch {
	case status == settings.StatusApplied:
		result = approvalStateApplied
	case status == settings.StatusExpired:
		result = approvalStateExpired
	case reason == requestOwnerRejectedError:
		result = approvalStateRejected
	case reason == requestChangedError:
		result = approvalStateChanged
	}

	return result
}

// isFinalStatus reports a status that is never revived.
func isFinalStatus(status string) bool {
	result := (status == settings.StatusApplied) || (status == settings.StatusRejected) || (status == settings.StatusExpired)

	return result
}

// decide rules on a queued request; approval needs an owner to ask.
func (m *Manager) decide(request ChangeRequest) Decision {
	canAskApproval := (m.approver != nil)
	result := decideRequest(request, canAskApproval)

	return result
}

// askApproval sends the owner the approval message of a request that now
// awaits approval; if it can not be sent, the request is rejected, so it
// never waits for a message the owner did not get.
func (m *Manager) askApproval(ctx context.Context, request ChangeRequest, decision Decision) {
	text := m.renderApprovalAsk(request, decision.ExpiresAt)
	buttons := approvalButtons(request.ID)
	messageID, err := m.approver.Ask(text, buttons)

	if err == nil {
		storeErr := m.store.SetApprovalMessage(ctx, request.ID, messageID)
		m.recordDBResult(storeErr, "store the approval message")
		log.Printf("⚙️ Settings request #%d (%s) awaits the owner's approval", request.ID, request.Key)
	}

	if err != nil {
		log.Printf("⚠️ Could not ask the owner to approve settings request #%d: %v", request.ID, err)
		_, rejectErr := m.store.RejectAwaiting(ctx, request.ID, requestAskFailedError)
		m.recordDBResult(rejectErr, "reject an unasked settings request")
	}
}

// editApproval shows a final outcome in the approval message (best effort).
func (m *Manager) editApproval(request ChangeRequest, messageID int64, status string, reason string) {
	isEditable := (m.approver != nil) && (messageID != 0) && (isFinalStatus(status) == true)

	if isEditable == true {
		text := renderApprovalFinal(request, status, reason)
		err := m.approver.Edit(messageID, text)
		m.logEditError(request.ID, err)
	}
}

// logEditError logs a failed edit of an approval message.
func (m *Manager) logEditError(requestID int64, err error) {
	if err != nil {
		log.Printf("⚠️ Could not update the approval message of settings request #%d: %v", requestID, err)
	}
}

// DecideApproval hands the owner's tap to the settings goroutine, which
// alone records it and swaps the snapshot, and returns the answer for the
// tap. A tap that could not be handed over within tapWait changed nothing.
func (m *Manager) DecideApproval(requestID int64, messageID int64, isApprove bool) string {
	result := tapUnavailableText

	if m.store != nil {
		tap := approvalTap{requestID: requestID, messageID: messageID, isApprove: isApprove, reply: make(chan string, 1)}
		result = m.handOver(tap)
	}

	return result
}

// handOver sends a tap to the loop and waits for its answer.
func (m *Manager) handOver(tap approvalTap) string {
	result := tapBusyText
	timer := time.NewTimer(m.tapWait)
	defer timer.Stop()
	isHandedOver := false

	select {
	case m.taps <- tap:
		isHandedOver = true
	case <-timer.C:
	}

	if isHandedOver == true {
		result = m.awaitTapReply(tap, timer)
	}

	return result
}

// awaitTapReply waits for the loop's answer to a handed-over tap.
func (m *Manager) awaitTapReply(tap approvalTap, timer *time.Timer) string {
	result := tapPendingText

	select {
	case result = <-tap.reply:
	case <-timer.C:
	}

	return result
}

// handleTap records a tap on the settings goroutine. Until the database
// was read nothing is recorded, so the approval is kept.
func (m *Manager) handleTap(ctx context.Context, tap approvalTap) string {
	result := tapFallbackText

	switch m.origin {
	case originDB:
		result = m.recordTap(ctx, tap)
	case originCache:
		result = tapCachedText
	}

	return result
}

// recordTap runs the conditional transition, applies an approved change to
// the snapshot and shows the outcome in the message.
func (m *Manager) recordTap(ctx context.Context, tap approvalTap) string {
	outcome, err := m.store.DecideApproval(ctx, tap.requestID, tap.isApprove, decideApproved)
	m.recordDBResult(err, "record the owner's approval")
	result := tapFailedText

	if err == nil {
		result = m.finishTap(ctx, tap, outcome)
	}

	return result
}

// finishTap puts an approved change into the snapshot and edits the
// message to the request's final state.
func (m *Manager) finishTap(ctx context.Context, tap approvalTap, outcome ApprovalOutcome) string {
	request := outcome.Request
	isApplied := (outcome.IsDecided() == true) && (outcome.Decision.IsApplied() == true)
	messageID := request.ApprovalMessageID

	isSpecialLimit := (request.Kind == settings.KindSpecialLimit)
	actor := fmt.Sprintf(digestActorApproved, request.Source)

	if (isApplied == true) && (isSpecialLimit == true) {
		m.applySpecialLimitCommitted(request, outcome.Decision, actor)
	}

	if (isApplied == true) && (isSpecialLimit == false) {
		m.applyCommitted(ctx, request, outcome.Decision, actor)
	}

	if messageID == 0 {
		messageID = tap.messageID
	}

	if outcome.IsDecided() == true {
		log.Printf("⚙️ Settings request #%d (%s) %s by the owner in Telegram", request.ID, request.Key, outcome.Status)
	}

	if isEditDue(outcome) == true {
		m.editApproval(request, messageID, outcome.Status, outcome.Error)
	}

	result := tapText(outcome)

	return result
}

// isEditDue reports a message that may still show its buttons: the tap
// decided it, expired it, or its message id was never stored.
func isEditDue(outcome ApprovalOutcome) bool {
	isExpired := (outcome.Status == settings.StatusExpired)
	isUnrecorded := (outcome.Request.ApprovalMessageID == 0)
	result := (outcome.IsDecided() == true) || (isExpired == true) || (isUnrecorded == true)

	return result
}

// tapText is the answer to a tap.
func tapText(outcome ApprovalOutcome) string {
	result := tapDecidedText
	isDecided := outcome.IsDecided()

	switch {
	case (isDecided == true) && (outcome.Status == settings.StatusApplied):
		result = tapAppliedText
	case (isDecided == true) && (outcome.Error == requestOwnerRejectedError):
		result = tapRejectedText
	case (isDecided == true) && (outcome.Error == requestChangedError):
		result = tapChangedText
	case isDecided == true:
		result = fmt.Sprintf(tapNotAppliedText, outcome.Error)
	case outcome.Status == settings.StatusExpired:
		result = tapExpiredText
	}

	return result
}
