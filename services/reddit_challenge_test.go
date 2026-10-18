package services

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The fixture is Reddit's challenge page of 2026-10: the token field is
// jsc_token, with an empty jsc_orig_r next to it.
const (
	redditChallengeTestToken = "2824be10929bdc604753c70a67a1c331426b896abbbaf4739e1ee08def410def"
	redditChallengeTestSeed  = "5e3d8bfd92312e18"
	redditChallengeTestPath  = "/r/explainlikeimfive/comments/1x1cmbg/eli5_how_is_it_that_charcoal_which_is_wood_thats/"
)

func TestSolveRedditChallengeSubmitsTheJscTokenForm(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "reddit_challenge.html"))

	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var mu sync.Mutex
	var submitted []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		isSolution := (query.Get("js_challenge") == "1")

		if isSolution == true {
			mu.Lock()
			submitted = append(submitted, query)
			mu.Unlock()
		}

		if (isSolution == false) && (r.URL.Path == redditChallengeTestPath) {
			_, _ = w.Write(page)
		}
	}))
	t.Cleanup(server.Close)

	err = solveRedditChallenge(server.Client(), server.URL+redditChallengeTestPath)

	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	if len(submitted) != 1 {
		t.Fatalf("want 1 submitted form, got %d", len(submitted))
	}

	form := submitted[0]
	_, hasOrigR := form["jsc_orig_r"]

	if (form.Get("solution") != redditChallengeTestSeed+redditChallengeTestSeed) || (form.Get("jsc_token") != redditChallengeTestToken) || (hasOrigR == false) || (form.Get("token") != "") {
		t.Errorf("submitted %v", form)
	}
}

func TestBuildChallengeSolveURLKeepsTheOldTokenField(t *testing.T) {
	form := redditChallengeForm{Action: "/r/x/comments/1/", Seed: "ab", TokenField: "token", Token: "cd"}

	solveURL, err := buildChallengeSolveURL("https://www.reddit.com/r/x/comments/1/y/", form)

	if (err != nil) || (solveURL != "https://www.reddit.com/r/x/comments/1/?js_challenge=1&solution=abab&token=cd") {
		t.Errorf("got %q %v", solveURL, err)
	}
}
