package services

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/config"
	"github.com/timoffmax/social-link-bot/model/settings"
)

// DownloadRequester is who a download is for; the special-download limit is
// counted for them.
type DownloadRequester struct {
	ChatID    int64
	UserID    int64
	IsPrivate bool
}

// costKind says what a costed attempt spends: the owner's home relay or a
// dollar-billing provider.
type costKind int

const (
	costRelay costKind = iota + 1
	costPaid
)

// methodNone marks a chain result that no method produced (concurrent or
// last-resort exit).
const methodNone ExtractionMethod = -1

// costedChainMethods are the chain methods that use the special-download limit.
var costedChainMethods = map[ExtractionMethod]costKind{
	MethodYouTubeRelay: costRelay,
	MethodYouTubeApify: costPaid,
	MethodStoriesApify: costPaid,
	MethodStoriesHiker: costPaid,
}

var (
	// errSpecialDownloadDenied ends a chain the limit refused.
	errSpecialDownloadDenied = errors.New("special download refused by the monthly limit")
	// errSpecialDownloadsUnavailable ends a chain whose limit could not be
	// checked or is off (cap 0, no database, quota check failed).
	errSpecialDownloadsUnavailable = errors.New("special downloads unavailable")
	// errNoCostedSource ends a post-step no source can serve.
	errNoCostedSource = errors.New("no special-download source can run")
	// errCostedNotStarted marks a costed method that refused before any spend.
	errCostedNotStarted = errors.New("costed method not started")
	// errCostedRan marks a chain of paid providers in which one ran, whatever
	// the others' errors say.
	errCostedRan = errors.New("a costed provider ran")
)

// costedRanError keeps a chain's error unchanged and marks it errCostedRan.
type costedRanError struct {
	err error
}

// Error implements error.
func (e *costedRanError) Error() string {
	return e.err.Error()
}

// Unwrap returns the chain's error.
func (e *costedRanError) Unwrap() error {
	return e.err
}

// Is reports errCostedRan.
func (e *costedRanError) Is(target error) bool {
	result := (target == errCostedRan)

	return result
}

// specialReserveSlowLog is the reservation time above which it is logged:
// the reservation eats chain time.
const specialReserveSlowLog = time.Second

// specialDownloadRefusals are the denials the user is told about.
var specialDownloadRefusals = map[GatedFetchDenial]bool{
	GatedFetchDeniedUserCap: true,
	GatedFetchDeniedChatCap: true,
	GatedFetchDeniedGlobal:  true,
	GatedFetchDeniedChatOff: true,
	GatedFetchDeniedBlocked: true,
}

// specialReservation is what one reserve took: counters, usage for the note,
// a bonus credit, or the denial / check error.
type specialReservation struct {
	quotas []GatedFetchQuota
	usage  GatedFetchUsage
	credit gatedCredit
	denial GatedFetchDenial
	err    error
}

// specialLimit reserves and refunds special downloads.
type specialLimit interface {
	reserve(requester DownloadRequester) specialReservation
	refund(reservation specialReservation, requester DownloadRequester, isGlobalKept bool)
}

// dbSpecialLimit is the PostgreSQL-backed limit (gated_fetch_* tables).
type dbSpecialLimit struct{}

// reserve resolves the counters, takes one unit of each, or a bonus credit
// when a cap is used up.
func (dbSpecialLimit) reserve(requester DownloadRequester) specialReservation {
	startTime := time.Now()
	chatID := requester.ChatID
	userID := requester.UserID
	result := specialReservation{}
	result.quotas, result.denial, result.err = ResolveGatedFetchQuotas(chatID, userID, requester.IsPrivate)

	if (result.denial == GatedFetchAllowed) && (result.err == nil) {
		result.denial, result.usage, result.err = ReserveGatedFetch(result.quotas)
	}

	isCreditEligible := (result.err == nil) && (IsCreditDenial(result.denial) == true)

	if isCreditEligible == true {
		result.credit, result.err = takeGatedCredit(chatID, userID, result.denial)
	}

	// A bonus credit replaces the monthly counters: the denied reservation
	// took nothing, so nothing must be refunded from them. Like unlimited
	// scopes, credits skip the shared fetch cap; the dollar budget applies.
	if result.credit.IsUsed == true {
		log.Printf("🎟️ [gated-fetch] chat %d, user %d used a bonus credit (group pool: %t, %d left)", chatID, userID, result.credit.IsGroup, result.credit.Left)
		result.denial = GatedFetchAllowed
		result.quotas = nil
		result.usage = GatedFetchUsage{}
	}

	elapsed := time.Since(startTime)

	if elapsed > specialReserveSlowLog {
		log.Printf("⏱️ [special-download] reserve took %s", elapsed)
	}

	return result
}

// refund gives the reserved units back (the bot-wide one only when no paid
// provider ran) and returns a used credit.
func (dbSpecialLimit) refund(reservation specialReservation, requester DownloadRequester, isGlobalKept bool) {
	if isGlobalKept == true {
		refundScopedGatedFetch(reservation.quotas)
	}

	if isGlobalKept == false {
		refundGatedQuotas(reservation.quotas)
	}

	if reservation.credit.IsUsed == true {
		returnGatedCredit(reservation.credit, requester.ChatID, requester.UserID)
	}
}

// specialDownload is one download's use of the limit: reserved at most once,
// right before the first costed attempt that can run, and settled once.
type specialDownload struct {
	label       string
	requester   DownloadRequester
	limit       specialLimit
	reservation specialReservation
	isAsked     bool
	isAllowed   bool
	hasPaidRun  bool
	budgetErr   error
	isSettled   bool
}

// newSpecialDownload starts one download's use of the limit.
func newSpecialDownload(label string, requester DownloadRequester, limit specialLimit) *specialDownload {
	result := &specialDownload{label: label, requester: requester, limit: limit}

	return result
}

// reserve asks the limit once and reports whether costed attempts may run.
func (d *specialDownload) reserve() bool {
	if d.isAsked == false {
		d.reservation = d.limit.reserve(d.requester)
		d.isAsked = true
		d.isAllowed = (d.reservation.denial == GatedFetchAllowed) && (d.reservation.err == nil)
		d.logReservation()
	}

	return d.isAllowed
}

// logReservation logs the outcome of the reservation.
func (d *specialDownload) logReservation() {
	outcome := "allowed"

	if d.reservation.denial != GatedFetchAllowed {
		outcome = fmt.Sprintf("denied (%s)", d.reservation.denial)
	}

	if d.reservation.err != nil {
		outcome = fmt.Sprintf("check failed: %v", d.reservation.err)
	}

	log.Printf("[special-download] %s: %s", d.label, outcome)
}

// record notes a costed attempt that failed: a budget refusal for the reply,
// and whether a dollar-billing provider ran.
func (d *specialDownload) record(kind costKind, err error) {
	isBudget := isPaidBudgetError(err)
	isPaidRun := (kind == costPaid) && (isCostedSkip(err) == false)

	if isBudget == true {
		d.budgetErr = err
	}

	if isPaidRun == true {
		d.hasPaidRun = true
	}
}

// settle keeps the units of a costed win (adding the note) or refunds them;
// only the first call acts.
func (d *specialDownload) settle(result []*MediaInfo, isCostedWin bool) []*MediaInfo {
	isFirst := (d.isSettled == false)
	isKept := (isFirst == true) && (isCostedWin == true) && (d.isAllowed == true)
	isRefunded := (isFirst == true) && (d.isAsked == true) && (d.isAllowed == true) && (isCostedWin == false)
	d.isSettled = true

	if (isKept == true) && (len(result) > 0) {
		d.appendNotes(result[0])
	}

	if isRefunded == true {
		log.Printf("↩️ [special-download] %s: refunded (bot-wide unit kept: %t)", d.label, d.hasPaidRun)
		d.limit.refund(d.reservation, d.requester, d.hasPaidRun)
	}

	return result
}

// appendNotes ends the first item's caption with the remaining-quota note
// or the bonus-credit note.
func (d *specialDownload) appendNotes(item *MediaInfo) {
	usage := d.reservation.usage
	credit := d.reservation.credit

	if usage.IsLimited == true {
		note := gatedQuotaNote(usage)
		AppendCaptionNote(item, note)
	}

	if credit.IsUsed == true {
		note := gatedCreditNote(credit)
		AppendCaptionNote(item, note)
	}
}

// Denial returns the reason for the reply: the limit's denial (kept when only
// the bonus-credit read failed), else a paid budget used up, else allowed.
func (d *specialDownload) Denial() GatedFetchDenial {
	result := GatedFetchAllowed
	isCheckOK := (d.reservation.err == nil) || (IsCreditDenial(d.reservation.denial) == true)
	isLimitDenial := (d.isAsked == true) && (d.isAllowed == false) && (isCheckOK == true)

	if d.budgetErr != nil {
		result = GatedFetchDeniedBudget
	}

	if isLimitDenial == true {
		result = d.reservation.denial
	}

	return result
}

// stopErr is the error a chain or post-step ends with when the reservation
// was not allowed.
func (d *specialDownload) stopErr() error {
	result := errSpecialDownloadsUnavailable
	denial := d.Denial()
	isRefusal := IsSpecialDownloadRefusal(denial)

	if isRefusal == true {
		result = errSpecialDownloadDenied
	}

	return result
}

// isCostedSkip reports a costed attempt that did not spend anything; a chain
// in which a provider ran is never one.
func isCostedSkip(err error) bool {
	isSkip := isPaidSkip(err)
	isNotStarted := errors.Is(err, errCostedNotStarted)
	hasRun := errors.Is(err, errCostedRan)
	result := ((isSkip == true) || (isNotStarted == true)) && (hasRun == false)

	return result
}

// IsSpecialDownloadRefusal reports a denial the user is told about: their
// allowance, the group's cap, the bot-wide cap, a group turned off, a user blocked.
func IsSpecialDownloadRefusal(denial GatedFetchDenial) bool {
	result := specialDownloadRefusals[denial]

	return result
}

// costedStep is one source of a post-step: what it spends, whether it can run
// and how it fetches. run returns nil media on failure.
type costedStep struct {
	kind   costKind
	canRun bool
	run    func() ([]*MediaInfo, error)
}

// runCostedSteps tries the steps that can run under one reservation and
// settles it once.
func runCostedSteps(download *specialDownload, steps []costedStep) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	isWin := false
	hasRunnable := false

	for _, step := range steps {
		if step.canRun == false {
			continue
		}

		hasRunnable = true

		if download.reserve() == false {
			err = download.stopErr()
			break
		}

		media, stepErr := step.run()
		isWin = (stepErr == nil) && (len(media) > 0)

		if isWin == true {
			result = media
			err = nil
			break
		}

		removeMediaFiles(media)
		download.record(step.kind, stepErr)
		err = stepErr

		if err == nil {
			err = errNoCostedSource
		}
	}

	if hasRunnable == false {
		err = errNoCostedSource
	}

	result = download.settle(result, isWin)

	return result, err
}

// captionNoteSeparator separates a caption from a note appended to it.
const captionNoteSeparator = "\n\n"

// captionCutMark ends a caption body cut to make room for a note.
const captionCutMark = "…"

// AppendCaptionNote appends a note to the caption, cutting the body so the
// whole fits TelegramCaptionLimitBytes, and records the note in Notes.
func AppendCaptionNote(item *MediaInfo, note string) {
	item.Caption = withCaptionNote(item.Caption, note)
	item.Notes = append(item.Notes, note)
}

// withCaptionNote returns caption followed by "\n\n"+note, the caption body
// cut first so the whole stays within TelegramCaptionLimitBytes.
func withCaptionNote(caption, note string) string {
	body := strings.TrimSpace(caption)
	combined := fmt.Sprintf("%s%s%s", body, captionNoteSeparator, note)

	if len(combined) > TelegramCaptionLimitBytes {
		room := TelegramCaptionLimitBytes - len(note) - len(captionNoteSeparator) - len(captionCutMark)
		cutBody := cutCaptionBody(body, room)
		combined = fmt.Sprintf("%s%s%s%s", cutBody, captionCutMark, captionNoteSeparator, note)
	}

	result := strings.TrimSpace(combined)

	return result
}

// CaptionWithoutNotes returns the caption without the notes AppendCaptionNote
// put at its end.
func CaptionWithoutNotes(item *MediaInfo) string {
	result := strings.TrimSpace(item.Caption)

	for index := len(item.Notes) - 1; index >= 0; index-- {
		withoutNote := strings.TrimSuffix(result, item.Notes[index])
		result = strings.TrimSpace(withoutNote)
	}

	return result
}

// CaptionWithNotes returns caption followed by notes, fitted like
// AppendCaptionNote.
func CaptionWithNotes(caption string, notes []string) string {
	result := strings.TrimSpace(caption)

	for _, note := range notes {
		result = withCaptionNote(result, note)
	}

	return result
}

// DropCaptionNote takes note off the caption's end and out of Notes; it
// reports whether the caption ended with it.
func DropCaptionNote(item *MediaInfo, note string) bool {
	caption := strings.TrimSpace(item.Caption)
	body := strings.TrimSuffix(caption, note)
	result := (len(body) < len(caption))

	if result == true {
		item.Caption = strings.TrimSpace(body)
		item.Notes = withoutNote(item.Notes, note)
	}

	return result
}

// withoutNote returns notes without the last occurrence of note.
func withoutNote(notes []string, note string) []string {
	result := notes
	index := -1

	for position, current := range notes {
		if current == note {
			index = position
		}
	}

	if index >= 0 {
		result = slices.Delete(slices.Clone(notes), index, index+1)
	}

	return result
}

// cutCaptionBody cuts an HTML caption body to at most maxBytes at a rune
// boundary, dropping a cut entity or tag and any element left unclosed.
func cutCaptionBody(body string, maxBytes int) string {
	end := max(maxBytes, 0)
	end = min(end, len(body))

	for (end > 0) && (end < len(body)) && (utf8.RuneStart(body[end]) == false) {
		end--
	}

	cut := body[:end]
	cut = dropUnfinishedMarkup(cut, "&", ";")
	cut = dropUnfinishedMarkup(cut, "<", ">")
	cut = dropUnclosedElements(cut)
	result := strings.TrimSpace(cut)

	return result
}

// dropUnfinishedMarkup drops a trailing opener (an entity's "&" or a tag's
// "<") that has no closer after it.
func dropUnfinishedMarkup(text, opener, closer string) string {
	result := text
	openAt := strings.LastIndex(text, opener)
	isUnfinished := (openAt >= 0) && (strings.Contains(text[openAt:], closer) == false)

	if isUnfinished == true {
		result = text[:openAt]
	}

	return result
}

// captionTagPattern matches an HTML tag of a caption and captures its name.
var captionTagPattern = regexp.MustCompile(`</?([a-zA-Z0-9-]+)[^>]*>`)

// dropUnclosedElements cuts the text before the first element whose closing
// tag the cut removed, so Telegram never sees an unclosed tag.
func dropUnclosedElements(text string) string {
	result := text
	matches := captionTagPattern.FindAllStringSubmatchIndex(text, -1)
	openAt := []int{}
	openNames := []string{}

	for _, match := range matches {
		name := strings.ToLower(text[match[2]:match[3]])
		isClosing := strings.HasPrefix(text[match[0]:], "</")
		last := len(openNames) - 1
		isClosingLast := (isClosing == true) && (last >= 0) && (openNames[last] == name)

		if isClosingLast == true {
			openAt = openAt[:last]
			openNames = openNames[:last]
		}

		if isClosing == false {
			openAt = append(openAt, match[0])
			openNames = append(openNames, name)
		}
	}

	if len(openAt) > 0 {
		result = text[:openAt[0]]
	}

	return result
}

// IsSpecialDownloadsOn reports whether costed downloads may run at all: a
// bot-wide cap that isn't 0 and a database to count them in.
func IsSpecialDownloadsOn() bool {
	globalCap := config.GetGatedFetchGlobalMonthlyCap()
	result := (globalCap != 0) && (IsDBInitialized() == true)

	return result
}

// IsSpecialDownloadsConfigured reports a bot-wide cap that isn't 0 and at
// least one costed source.
func IsSpecialDownloadsConfigured() bool {
	globalCap := config.GetGatedFetchGlobalMonthlyCap()
	kinds := CurrentSpecialDownloadKinds()
	result := (globalCap != 0) && (kinds.Any() == true)

	return result
}

// IsSpecialDownloadsOffered reports whether texts should offer special
// downloads: configured, a database, and a default allowance or group cap.
func IsSpecialDownloadsOffered() bool {
	userAllowance := config.GetGatedFetchUserMonthlyAllowance()
	chatCap := config.GetGatedFetchChatMonthlyCap()
	isConfigured := IsSpecialDownloadsConfigured()
	hasDefaultLimit := (userAllowance != 0) || (chatCap != 0)
	result := (isConfigured == true) && (IsDBInitialized() == true) && (hasDefaultLimit == true)

	return result
}

// IsStoriesOffered reports whether Stories can be fetched now.
func IsStoriesOffered() bool {
	kinds := CurrentSpecialDownloadKinds()
	result := (kinds.Stories == true) && (IsSpecialDownloadsOn() == true)

	return result
}

// SpecialDownloadKindSet is what counts as a special download with this config.
type SpecialDownloadKindSet struct {
	RestrictedPosts bool
	ReelVideos      bool
	Stories         bool
	Shorts          bool
	AllShorts       bool
	ViaRelay        bool
	ViaPaid         bool
}

// Any reports at least one kind.
func (k SpecialDownloadKindSet) Any() bool {
	result := (k.RestrictedPosts == true) || (k.ReelVideos == true) || (k.Stories == true) || (k.Shorts == true) || (k.AllShorts == true)

	return result
}

// CurrentSpecialDownloadKinds returns the kinds the current config serves.
func CurrentSpecialDownloadKinds() SpecialDownloadKindSet {
	isInstagramOn := config.IsPlatformEnabled(settings.PlatformInstagram)
	isYouTubeOn := config.IsPlatformEnabled(settings.PlatformYouTube)
	gatedProviders := ConfiguredGatedProviders()
	isInstagramRelay := (isInstagramOn == true) && (isRelayUsableFor(relayPlatformInstagram) == true)
	isApifyShorts := (isYouTubeOn == true) && (isApifyConfigured() == true)
	result := SpecialDownloadKindSet{}
	result.RestrictedPosts = (isInstagramOn == true) && (len(gatedProviders) > 0)
	result.ReelVideos = (result.RestrictedPosts == true) || (isInstagramRelay == true)
	result.Stories = (isInstagramOn == true) && (isStoriesFetchConfigured() == true)
	result.AllShorts = (isYouTubeOn == true) && (isRelayUsableFor(relayPlatformYouTube) == true)
	result.Shorts = (result.AllShorts == true) || (isApifyShorts == true)
	result.ViaRelay = (result.AllShorts == true) || (isInstagramRelay == true)
	result.ViaPaid = (result.RestrictedPosts == true) || (result.Stories == true) || (isApifyShorts == true)

	return result
}

// RelayPlatformsInUse returns the relay platforms that are usable, sorted.
func RelayPlatformsInUse() []string {
	result := []string{}

	for platform := range relaySupportedPlatforms {
		if isRelayUsableFor(platform) == true {
			result = append(result, platform)
		}
	}

	sort.Strings(result)

	return result
}
