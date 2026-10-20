package services

// Integration tests of the special-download accounting against PostgreSQL;
// skipped unless GATED_FETCH_TEST_DSN is set (see gated_fetch_store_test.go).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runCountedChain runs the chain of mediaURL for requester against the
// database-backed limit.
func runCountedChain(sd *SmartDownloader, mediaURL string, requester DownloadRequester) ([]*MediaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	return sd.downloadMediaWithContext(ctx, mediaURL, newSpecialDownload(mediaURL, requester, dbSpecialLimit{}))
}

func privateRequester(userID int64) DownloadRequester {
	return DownloadRequester{ChatID: userID, UserID: userID, IsPrivate: true}
}

func TestRelayShortUsesOneUnitAndShowsTheNote(t *testing.T) {
	setupGatedFetchTestDB(t)
	sd, _ := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))

	media, err := runCountedChain(sd, relayTestShortsURL, privateRequester(601))
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 601)

	if (err != nil) || (len(media) != 1) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected the Short for one unit, got %v, user=%d global=%d", err, userUsed, globalUsed)
	}

	if strings.HasSuffix(media[0].Caption, "💎 Special downloads left this month: 2 of 3 (regular links don't count)") == false {
		t.Fatalf("expected the note, got %q", media[0].Caption)
	}
}

func TestRelayShortThatFailsRefundsEveryUnit(t *testing.T) {
	setupGatedFetchTestDB(t)
	sd, fake := newRelayShortsDownloader(t, serveRelayError(http.StatusBadGateway, relayCodeYtDlpFailed))

	_, err := runCountedChain(sd, relayTestShortsURL, privateRequester(602))
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 602)
	var deniedErr *GatedFetchDeniedError

	if (len(fake.recorded()) != 1) || (userUsed != 0) || (globalUsed != 0) {
		t.Fatalf("expected one relay call and every unit refunded, got %d call(s), user=%d global=%d", len(fake.recorded()), userUsed, globalUsed)
	}

	if (err == nil) || (errors.As(err, &deniedErr) == true) {
		t.Fatalf("expected a failure that is no refusal, got %v", err)
	}
}

func TestApifyShortAfterYtDlpFailsKeepsTheGlobalUnitOnFailure(t *testing.T) {
	setupGatedFetchTestDB(t)
	t.Setenv("ENABLE_YOUTUBE", "true")
	t.Setenv("RELAY_PLATFORMS", "")
	apify := newFailingApify(t)
	sd := &SmartDownloader{extractYtDlp: blockedYtDlp, youtubeApify: &YouTubeApifyExtractor{tempDir: t.TempDir(), actor: "a~b", api: fakeApifyClient(apify)}}

	_, err := runCountedChain(sd, relayTestShortsURL, privateRequester(603))
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 603)

	if (err == nil) || (userUsed != 0) || (globalUsed != 1) {
		t.Fatalf("expected the user refunded and the bot-wide unit kept, got %v, user=%d global=%d", err, userUsed, globalUsed)
	}
}

func TestShortOverTheAllowanceUsesABonusCredit(t *testing.T) {
	setupGatedFetchTestDB(t)
	useUpAllowance(t, 604)
	recordTestPayment(t, "charge-short", 604, 25)
	var isFailing atomic.Bool
	sd, _ := newRelayShortsDownloader(t, func(w http.ResponseWriter, r *http.Request) {
		if isFailing.Load() == true {
			serveRelayError(http.StatusBadGateway, relayCodeYtDlpFailed)(w, r)
		}

		if isFailing.Load() == false {
			serveRelayVideo("", relayTestMP4)(w, r)
		}
	})

	media, err := runCountedChain(sd, relayTestShortsURL, privateRequester(604))
	credits, _ := GetGatedFetchCredits(604)

	if (err != nil) || (credits != 9) || (strings.HasSuffix(media[0].Caption, "🎟️ Bonus downloads left: 9") == false) {
		t.Fatalf("expected one credit used and noted, got %v, %d left", err, credits)
	}

	isFailing.Store(true)
	runCountedChain(sd, relayTestShortsURL, privateRequester(604))
	creditsAfterFailure, _ := GetGatedFetchCredits(604)
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 604)

	if (creditsAfterFailure != 9) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected the credit returned and the counters untouched, got %d left, user=%d global=%d", creditsAfterFailure, userUsed, globalUsed)
	}
}

func TestStoryChargesOneUnitAcrossProviders(t *testing.T) {
	setupGatedFetchTestDB(t)
	useHikerTestEnv(t)
	fake := newFakeHiker(t)
	fake.bodies[hikerPathStoryByURL] = hikerStoryVideoBody
	sd := newStoriesTestDownloader(t, newFailingApify(t), fake)

	media, err := runCountedChain(sd, storyTestURL, privateRequester(605))
	userUsed, globalUsed := scopeAndGlobalUsage(t, GatedFetchScopeUser, 605)

	if (err != nil) || (len(media) != 1) || (userUsed != 1) || (globalUsed != 1) {
		t.Fatalf("expected the HikerAPI story for one unit, got %v, user=%d global=%d", err, userUsed, globalUsed)
	}
}

func TestStoryInAChatTurnedOffIsRefused(t *testing.T) {
	setupGatedFetchTestDB(t)
	useHikerTestEnv(t)

	if err := SetGatedFetchEnabled(GatedFetchScopeChat, -606, false); err != nil {
		t.Fatalf("off: %v", err)
	}

	runs := 0
	input := ""
	apify := newFakeApify(t, `[]`, &runs, &input)
	fake := newFakeHiker(t)
	sd := newStoriesTestDownloader(t, apify, fake)

	_, err := runCountedChain(sd, storyTestURL, DownloadRequester{ChatID: -606, UserID: 606, IsPrivate: false})
	var deniedErr *GatedFetchDeniedError

	if (errors.As(err, &deniedErr) == false) || (deniedErr.Denial != GatedFetchDeniedChatOff) {
		t.Fatalf("expected a chat_off refusal, got %v", err)
	}

	if (runs != 0) || (fake.callCount(hikerPathStoryByURL) != 0) {
		t.Fatalf("expected no provider call, got %d Apify run(s), %d HikerAPI call(s)", runs, fake.callCount(hikerPathStoryByURL))
	}
}

func TestConcurrentShortsNeverPassTheAllowance(t *testing.T) {
	setupGatedFetchTestDB(t)
	const downloads = 5
	t.Setenv("GATED_FETCH_USER_MONTHLY_ALLOWANCE", "4")
	sd, _ := newRelayShortsDownloader(t, serveRelayVideo("", relayTestMP4))
	var wg sync.WaitGroup
	var served atomic.Int32

	for i := 0; i < downloads; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			media, err := runCountedChain(sd, relayTestShortsURL, privateRequester(607))
			isServed := (err == nil) && (len(media) == 1)

			if isServed == true {
				served.Add(1)
			}
		}()
	}

	wg.Wait()
	userUsed, _ := scopeAndGlobalUsage(t, GatedFetchScopeUser, 607)

	if (served.Load() != downloads-1) || (userUsed != downloads-1) {
		t.Fatalf("expected exactly %d Shorts, got %d served, %d used", downloads-1, served.Load(), userUsed)
	}
}

func TestCreditsAreSellableWithARelayOnlySetup(t *testing.T) {
	setupGatedFetchTestDB(t)
	useNoPaidTokens(t)
	t.Setenv("ENABLE_INSTAGRAM", "true")
	configureTestRelayEnv(t, "http://127.0.0.1:8791", writeRelayTestToken(t))
	t.Setenv("RELAY_PLATFORMS", relayPlatformInstagram)

	if (CanUseGatedFetchCredits(608) == false) || (CanUseGroupFetchCredits(-608) == false) {
		t.Fatalf("expected credits sellable with the relay alone")
	}

	t.Setenv("GATED_FETCH_GLOBAL_MONTHLY_CAP", "0")

	if (CanUseGatedFetchCredits(608) == true) || (CanUseGroupFetchCredits(-608) == true) {
		t.Fatalf("a bot-wide cap of 0 must stop credit sales")
	}
}
