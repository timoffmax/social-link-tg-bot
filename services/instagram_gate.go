package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/timoffmax/social-link-bot/model/settings"
)

// instagramOEmbedEndpoint is Instagram's logged-out oEmbed API. For a post whose
// author limited its audience it answers HTTP 400 with a JSON gating notice
// instead of the embed, which tells "restricted by the author" apart from a
// redirector outage.
const instagramOEmbedEndpoint = "https://i.instagram.com/api/v1/oembed/"

// instagramGateTimeout caps the oEmbed lookup so a slow Instagram answer
// doesn't hold up the failure reply.
const instagramGateTimeout = 5 * time.Second

// instagramGateMaxBody caps how much of the oEmbed answer is read; its html
// repeats the caption, escaped, so a long caption needs well over 64 KB.
const instagramGateMaxBody = 256 * 1024

// instagramAgeGateMarker is the blocks_logging_data value of an account-level
// minimum age limit ("People under 15 can't see this content").
const instagramAgeGateMarker = "MIN_AGE_ACCOUNT"

// instagramGateDefaultTitle replaces an empty gating title in the reply.
const instagramGateDefaultTitle = "its author limited who can see it"

// instagramGateMessageTemplate is the chat reply for a gated post: emoji, then
// Instagram's own gating title.
const instagramGateMessageTemplate = "%s This Instagram post is restricted: %s.\n\nInstagram only shows it to logged-in users who meet the limit, so the bot can't download it."

// instagramGateEmojiAge and instagramGateEmojiOther prefix the reply.
const (
	instagramGateEmojiAge   = "🔞"
	instagramGateEmojiOther = "🔒"
)

// instagramGateNotice is the gating part of an oEmbed error answer.
type instagramGateNotice struct {
	GatingType        string      `json:"gating_type"`
	Title             string      `json:"title"`
	BlocksLoggingData string      `json:"blocks_logging_data"`
	GatingTypeThrift  interface{} `json:"gating_type_thrift"`
}

// instagramAudienceGateThrift is the gating_type_thrift of an account's
// region/audience limit ("It can't be seen by certain audiences"), which
// comes with an empty blocks_logging_data. Only this known non-age gate is
// sent unblurred; any other gate is treated as possibly age-related.
const instagramAudienceGateThrift = 3

// InstagramGate describes a viewer restriction the author put on a post.
type InstagramGate struct {
	Restricted      bool
	AgeLimited      bool
	AudienceLimited bool
	Title           string
}

// NeedsSpoiler reports whether fetched media of the post is sent blurred:
// every gate except the known region/audience limit.
func (g InstagramGate) NeedsSpoiler() bool {
	result := (g.AudienceLimited == false)

	return result
}

// InstagramPost is what the oEmbed API tells about a post: its gate, and for
// an ungated post the author's user name and the caption text (plain text).
type InstagramPost struct {
	Gate       InstagramGate
	AuthorName string
	Caption    string
}

// instagramOEmbedPost is the part of a 200 oEmbed answer the bot reads; the
// title holds the full caption.
type instagramOEmbedPost struct {
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
}

// BlursMedia reports whether media fetched for free is sent blurred: a gated
// post, unless only its region/audience is limited.
func (g InstagramGate) BlursMedia() bool {
	result := (g.Restricted == true) && (g.NeedsSpoiler() == true)

	return result
}

// InstagramGatedError is returned by the Instagram chain for a post its author
// gated, so callers can explain the restriction instead of a generic failure.
type InstagramGatedError struct {
	Gate InstagramGate
}

// Error implements error.
func (e *InstagramGatedError) Error() string {
	result := fmt.Sprintf("instagram post is gated by its author: %s", e.Gate.Title)

	return result
}

// LookupInstagramPost asks Instagram's oEmbed API for a post's gate, author and
// caption; a failed lookup yields a zero InstagramPost.
func LookupInstagramPost(instagramURL string) InstagramPost {
	result := InstagramPost{}
	status := 0
	var body []byte
	postURL, err := canonicalInstagramPostURL(instagramURL)

	if err == nil {
		status, body, err = fetchInstagramOEmbed(postURL)
	}

	if err == nil {
		result, err = parseInstagramOEmbed(status, body)
	}

	if err != nil {
		log.Printf("⚠️ [instagram-oembed] lookup failed for %s: %v", instagramURL, err)
	}

	return result
}

// instagramPostCaption builds the author line and caption, fitted next to the
// original link with room for a later note; "" when the post had neither.
func instagramPostCaption(post InstagramPost, instagramURL string) string {
	username := safeProfileUsername(post.AuthorName)
	caption := authorCaption{
		Platform:  settings.PlatformInstagram,
		Username:  username,
		Text:      post.Caption,
		PostURL:   instagramURL,
		Reserve:   gatedQuotaNoteReserve,
		PlainText: true,
	}
	result := buildAuthorCaption(caption)

	return result
}

// InstagramGateMessage builds the chat reply for a gated post.
func InstagramGateMessage(gate InstagramGate) string {
	title := gate.Title
	emoji := instagramGateEmojiOther

	if title == "" {
		title = instagramGateDefaultTitle
	}

	if gate.AgeLimited == true {
		emoji = instagramGateEmojiAge
	}

	result := fmt.Sprintf(instagramGateMessageTemplate, emoji, title)

	return result
}

// canonicalInstagramPostURL rebuilds the instagram.com post URL the oEmbed API
// expects from any recognized Instagram or proxy URL.
func canonicalInstagramPostURL(instagramURL string) (string, error) {
	result := ""
	var err error
	postType, shortcode := instagramPostPath(instagramURL)

	if shortcode == "" {
		err = fmt.Errorf("cannot extract post ID from URL: %s", instagramURL)
	}

	if shortcode != "" {
		result = fmt.Sprintf("https://www.instagram.com/%s/%s/", postType, shortcode)
	}

	return result, err
}

// fetchInstagramOEmbed returns the oEmbed status code and (capped) body.
func fetchInstagramOEmbed(postURL string) (int, []byte, error) {
	result := 0
	var body []byte
	var resp *http.Response
	query := url.Values{"url": {postURL}}
	requestURL := fmt.Sprintf("%s?%s", instagramOEmbedEndpoint, query.Encode())
	ctx, cancel := context.WithTimeout(context.Background(), instagramGateTimeout)

	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)

	if err == nil {
		resp, err = SharedHTTPClient.Do(req)
	}

	if err == nil {
		defer resp.Body.Close()

		result = resp.StatusCode
		limited := io.LimitReader(resp.Body, instagramGateMaxBody)
		body, err = io.ReadAll(limited)
	}

	return result, body, err
}

// parseInstagramOEmbed reads an oEmbed answer: the gate (see
// parseInstagramGate), plus the author and caption of an ungated post.
func parseInstagramOEmbed(status int, body []byte) (InstagramPost, error) {
	result := InstagramPost{}
	post := instagramOEmbedPost{}
	gate, err := parseInstagramGate(status, body)

	if (err == nil) && (http.StatusOK == status) {
		err = json.Unmarshal(body, &post)
	}

	if err == nil {
		result = InstagramPost{Gate: gate, AuthorName: post.AuthorName, Caption: post.Title}
	}

	return result, err
}

// parseInstagramGate reads an oEmbed answer: 200 is an ungated post, 400 with a
// gating_type is a gated one, anything else (404 for a missing post) is an error.
func parseInstagramGate(status int, body []byte) (InstagramGate, error) {
	result := InstagramGate{}
	notice := instagramGateNotice{}
	var err error
	isGateAnswer := (http.StatusBadRequest == status)
	isUnexpected := ((http.StatusOK != status) && (isGateAnswer == false))

	if isGateAnswer == true {
		err = json.Unmarshal(body, &notice)
	}

	if isUnexpected == true {
		err = fmt.Errorf("unexpected oEmbed status code: %d", status)
	}

	if (err == nil) && (notice.GatingType != "") {
		result = newInstagramGate(notice)
	}

	return result, err
}

// newInstagramGate maps a gating notice to an InstagramGate.
func newInstagramGate(notice instagramGateNotice) InstagramGate {
	thrift := gatingThriftValue(notice.GatingTypeThrift)
	isAudience := (notice.BlocksLoggingData == "") && (thrift == instagramAudienceGateThrift)
	result := InstagramGate{
		Restricted:      true,
		AgeLimited:      (instagramAgeGateMarker == notice.BlocksLoggingData),
		AudienceLimited: isAudience,
		Title:           notice.Title,
	}

	return result
}

// gatingThriftValue reads gating_type_thrift whether it comes as a number or
// a string; anything else is -1 (unknown, so the post stays blurred).
func gatingThriftValue(raw interface{}) int {
	result := -1
	number, isNumber := raw.(float64)
	text, isText := raw.(string)

	if isNumber == true {
		result = int(number)
	}

	if isText == true {
		parsed, err := strconv.ParseFloat(text, 64)

		if err == nil {
			result = int(parsed)
		}
	}

	return result
}
