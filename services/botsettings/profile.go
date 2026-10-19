package botsettings

import (
	"fmt"
	"log"
	"time"
)

// Profile push timing: one push 10 s after the last change (so toggling
// three platforms in a row pushes once), retried with a growing wait.
const (
	profileDebounce     = 10 * time.Second
	profileRetryDelay   = 30 * time.Second
	profileMaxAttempts  = 5
	profileFailureAlert = "⚠️ The Telegram profile (command menu, descriptions) was not updated after a settings change: %v. It is pushed again on the next change or restart."
)

// profileSyncer keeps the Telegram profile in step with the settings: after
// a change it re-renders the profile and pushes only the parts that differ
// from what Telegram is known to show.
type profileSyncer struct {
	push      func(known map[string]string) (map[string]string, []string, error)
	now       func() time.Time
	known     map[string]string
	dueAt     time.Time
	attempts  int
	pushedAt  time.Time
	lastErr   error
	lastCalls []string
}

// newProfileSyncer returns a syncer that pushes with push.
func newProfileSyncer(push func(known map[string]string) (map[string]string, []string, error), now func() time.Time) *profileSyncer {
	result := &profileSyncer{push: push, now: now, known: map[string]string{}}

	return result
}

// pushInitial pushes every part, as the bot does at start.
func (p *profileSyncer) pushInitial() error {
	var result error

	if p.push != nil {
		result = p.pushChanges()
	}

	return result
}

// schedule asks for a check once the debounce has passed since this call;
// a later change moves it again.
func (p *profileSyncer) schedule() {
	p.dueAt = p.now().Add(profileDebounce)
	p.attempts = 0
}

// isDue reports a scheduled check whose time has come.
func (p *profileSyncer) isDue() bool {
	now := p.now()
	result := (p.push != nil) && (p.dueAt.IsZero() == false) && (now.Before(p.dueAt) == false)

	return result
}

// syncIfDue runs a due check: it pushes the changed parts, retries a failure
// with a growing wait, and alerts once it gives up. It returns whether it
// ran.
func (p *profileSyncer) syncIfDue(alert func(text string)) bool {
	result := p.isDue()

	if result == true {
		p.dueAt = time.Time{}
		p.attempts++
		err := p.pushChanges()
		p.handleFailure(err, alert)
	}

	return result
}

// pushChanges pushes the parts that differ and records the outcome.
func (p *profileSyncer) pushChanges() error {
	known, called, err := p.push(p.known)
	p.known = known
	p.lastErr = err
	p.lastCalls = called

	if len(called) > 0 {
		p.pushedAt = p.now()
		log.Printf("⚙️ Telegram profile pushed: %v", called)
	}

	return err
}

// handleFailure schedules a retry, or alerts after the last attempt.
func (p *profileSyncer) handleFailure(err error, alert func(text string)) {
	isRetry := (err != nil) && (p.attempts < profileMaxAttempts)
	isGivenUp := (err != nil) && (p.attempts >= profileMaxAttempts)

	if isRetry == true {
		wait := profileRetryDelay * time.Duration(1<<(p.attempts-1))
		p.dueAt = p.now().Add(wait)
		log.Printf("⚠️ Telegram profile push failed (attempt %d of %d), retrying in %s: %v", p.attempts, profileMaxAttempts, wait, err)
	}

	if isGivenUp == true {
		log.Printf("❌ Telegram profile push failed after %d attempts: %v", p.attempts, err)
		alertText := fmt.Sprintf(profileFailureAlert, err)
		alert(alertText)
	}

	if err == nil {
		p.attempts = 0
	}
}

// profileStatus is the published state of the profile push.
type profileStatus struct {
	PushedAt  *time.Time `json:"pushed_at"`
	Methods   []string   `json:"methods"`
	Error     string     `json:"error,omitempty"`
	PendingAt *time.Time `json:"pending_at,omitempty"`
	Attempts  int        `json:"attempts"`
}

// status returns the published state.
func (p *profileSyncer) status() profileStatus {
	result := profileStatus{Methods: p.lastCalls, Attempts: p.attempts}

	if p.pushedAt.IsZero() == false {
		pushedAt := p.pushedAt
		result.PushedAt = &pushedAt
	}

	if p.dueAt.IsZero() == false {
		dueAt := p.dueAt
		result.PendingAt = &dueAt
	}

	if p.lastErr != nil {
		result.Error = p.lastErr.Error()
	}

	return result
}
