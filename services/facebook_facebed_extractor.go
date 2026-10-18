package services

// FacebookFacebedExtractor reads public Facebook photo and text-only posts
// through facebed (an embed service like vxthreads). Facebook serves its
// login wall to the bot's datacenter IP; facebed answers with og tags whose
// signed fbcdn photo URLs download directly, and a text-only post is the same
// page without og:image. Every facebed failure (private, deleted,
// unsupported or blocked post) is the same 200 "Log in" card, so success is
// told by facebed's own site name, not by the status code.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/timoffmax/social-link-bot/config"
)

const (
	// FacebookPlatform is the platform key of Facebook (captions, settings,
	// per-chat toggles, stats).
	FacebookPlatform = "facebook"

	// facebedServiceName is the outage-tracker key for facebed.
	facebedServiceName = "facebed"

	// facebedSiteNamePrefix starts og:site_name on a real post page; the
	// login card has no site name.
	facebedSiteNamePrefix = "facebed by pi.kt"

	// facebedFetchTimeout covers facebed scraping Facebook live (1-9 s).
	facebedFetchTimeout = 25 * time.Second
	facebedMaxPage      = 1024 * 1024

	facebookOriginDomain = "facebook.com"
	facebookMediaDomain  = "fbcdn.net"
	facebookMediaScheme  = "https"

	// facebookOpaqueIDPrefix starts the encoded post ids (pfbid...) that
	// facebed answers with the numeric id, so they can't be matched.
	facebookOpaqueIDPrefix = "pfbid"

	// facebedMaxPhotoRedirects caps the CDN redirects of one photo download.
	facebedMaxPhotoRedirects = 10

	// facebedMaxPhotoSize is Telegram's sendPhoto limit.
	facebedMaxPhotoSize = int64(10 * 1024 * 1024)

	// facebedPhotoConcurrency is how many photos download at once (facebed
	// lists up to 4).
	facebedPhotoConcurrency = 4

	facebedFilePrefix = "facebook_facebed"

	// facebedTextLimit is where facebed cuts a post's text without a marker:
	// post.text[:1024] in facebed.py, a Python slice counting code points
	// (runes here). Text of that length is taken as cut.
	facebedTextLimit = 1024

	// telegramMessageLimit is Telegram's sendMessage limit, counted in UTF-16
	// code units of the text left after entity parsing.
	telegramMessageLimit = 4096

	// facebedTruncationMarker ends a text that facebed or the message limit
	// cut.
	facebedTruncationMarker = "…"

	// facebedTextPostReason explains a text post kept as a last resort.
	facebedTextPostReason = "facebed text-only post (sent if no method finds media)"
)

var (
	// errFacebedUnavailable is a post facebed can't show: usually private or
	// deleted, so it is not an outage (the health probe catches a broken
	// facebed).
	errFacebedUnavailable = errors.New("facebed can't show this post")

	// errFacebedLoginCard is facebed's answer for a private, removed or
	// unsupported post.
	errFacebedLoginCard = fmt.Errorf("%w: login card (private, removed or unsupported post)", errFacebedUnavailable)

	// errFacebedWrongPost marks an answer about another post than the one
	// asked for (og:url without the requested id).
	errFacebedWrongPost = fmt.Errorf("%w: it answered with another post", errFacebedUnavailable)

	// errFacebedNoPhoto marks a post facebed reads but whose photo it can't
	// give: a video post, or a photo or video link answered without media.
	// Left to the other methods.
	errFacebedNoPhoto = errors.New("no photo on the facebed page")

	// errFacebedEmptyPost marks a post facebed reads with neither media nor
	// text, so there is nothing to send.
	errFacebedEmptyPost = errors.New("facebed post has no photo and no text")

	// errFacebookUnsupportedPath marks link shapes facebed can't read or reads
	// wrongly (a group landing returns an unrelated recent post).
	errFacebookUnsupportedPath = errors.New("facebook link shape not supported by facebed")

	// errFacebedPhotoOffCDN marks a photo URL or redirect off the media CDN.
	errFacebedPhotoOffCDN = errors.New("photo is not served from the media CDN")

	// errFacebedBroken marks the failures that feed the outage tracker:
	// facebed's own transport errors, non-200 answers and unparsable pages,
	// and photo downloads the CDN fails at the transport level or answers
	// with a non-2xx status. A photo that fails validation is a per-post
	// failure.
	errFacebedBroken = errors.New("facebed failure")
)

// telegramTagPattern matches an HTML tag of a message, which Telegram turns
// into an entity and leaves out of the length.
var telegramTagPattern = regexp.MustCompile(`<[^>]*>`)

// facebookLinkKind is the shape of a Facebook link.
type facebookLinkKind int

const (
	facebookLinkOther facebookLinkKind = iota
	facebookLinkPhoto
	facebookLinkVideo
	facebookLinkPost
)

var (
	// facebookPhotoPathPattern matches single-photo links: photo/?fbid=,
	// photo.php?fbid= and /<page>/photos/....
	facebookPhotoPathPattern = regexp.MustCompile(`^/(?:photo(?:\.php)?/?|[^/]+/photos/.+)$`)

	// facebookVideoPathPattern matches video links: reels, watch, videos and
	// the reel and video share links.
	facebookVideoPathPattern = regexp.MustCompile(`^/(?:reel/.*|watch/?|videos/.*|[^/]+/videos/.*|share/[rv]/.*)$`)

	// facebookPostPathPattern matches post links: page and group posts,
	// permalink.php, story.php and the other share links (share/r/ and
	// share/v/ are matched as videos first).
	facebookPostPathPattern = regexp.MustCompile(`^/(?:share/.+|[^/]+/posts/.+|permalink\.php/?|story\.php/?|groups/[^/]+/posts/.+)$`)

	// facebookPhotosPathPattern captures the set (any shape: a.<id>,
	// pb.<id>.-<n>, ...) and the photo id of /<page>/photos/[<set>/]<id>/,
	// which facebed shows without the image or as an unrelated post.
	facebookPhotosPathPattern = regexp.MustCompile(`^/[^/]+/photos/(?:([^/]+)/)?([0-9]+)/?$`)

	// facebookPhotosPrefixPattern matches every /<page>/photos path; those
	// without a photo id (photo tabs, sets) are refused.
	facebookPhotosPrefixPattern = regexp.MustCompile(`^/[^/]+/photos(?:/.*)?$`)

	// facebookSingleSegmentPattern captures one-segment paths (mostly profiles).
	facebookSingleSegmentPattern = regexp.MustCompile(`^/([^/]+)/?$`)

	// facebookRefusedPathPattern matches group landings, albums, events,
	// marketplace, stories, hashtags and people profiles.
	facebookRefusedPathPattern = regexp.MustCompile(`^/(?:groups/[^/]+/?|media/.*|events/.*|marketplace/.*|stories/.*|hashtag/.*|people/.*|(?:[^/]+/)?photos/albums(?:/.*)?)$`)

	// facebookPostIDPatterns capture the post id of the path shapes that carry
	// one (the photo set is skipped, a post slug too).
	facebookPostIDPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^/[^/]+/photos/(?:[^/]+/)?([0-9]+)/?$`),
		regexp.MustCompile(`^/groups/[^/]+/posts/([^/]+)/?$`),
		regexp.MustCompile(`^/[^/]+/posts/(?:[^/]+/)?([^/]+)/?$`),
		regexp.MustCompile(`^/reel/([0-9]+)/?$`),
		regexp.MustCompile(`^/(?:[^/]+/)?videos/(?:[^/]+/)?([0-9]+)/?$`),
	}
)

// facebedSingleSegmentRoutes are the one-segment paths that are posts.
var facebedSingleSegmentRoutes = map[string]bool{
	"photo":         true,
	"photo.php":     true,
	"permalink.php": true,
	"story.php":     true,
	"watch":         true,
}

// facebookPostIDParams name the query parameter holding the post id of the
// one-segment routes that carry it.
var facebookPostIDParams = map[string]string{
	"photo":         "fbid",
	"photo.php":     "fbid",
	"permalink.php": "story_fbid",
	"story.php":     "story_fbid",
}

// facebedKeptQueryParams are the query parameters facebed routes on; the rest
// (fbclid, mibextid, rdid, __cft__, ...) is tracking.
var facebedKeptQueryParams = map[string]bool{
	"fbid":       true,
	"set":        true,
	"story_fbid": true,
	"id":         true,
	"v":          true,
}

// FacebookFacebedExtractor downloads Facebook photo and text-only posts via
// facebed.
type FacebookFacebedExtractor struct {
	tempDir     string
	base        string
	mediaDomain string
	mediaScheme string
	// photoClient supplies the transport and timeout of photo downloads; nil
	// means SharedDownloadClient.
	photoClient *http.Client
}

// facebedPost is a parsed facebed page.
type facebedPost struct {
	Author       string
	Text         string
	SiteName     string
	CanonicalURL string
	Photos       []string
	HasVideo     bool
}

// facebedPhoto is what one photo download returned.
type facebedPhoto struct {
	FinalURL    string
	ContentType string
	Size        int64
}

// NewFacebookFacebedExtractor creates a facebed-backed Facebook extractor.
func NewFacebookFacebedExtractor(tempDir string) *FacebookFacebedExtractor {
	host := config.GetFacebedHost()
	base := fmt.Sprintf("https://%s", host)

	return &FacebookFacebedExtractor{tempDir: tempDir, base: base, mediaDomain: facebookMediaDomain, mediaScheme: facebookMediaScheme}
}

// facebookLinkMethods orders the Facebook methods by link shape: photos
// through facebed only (yt-dlp can't fetch Facebook photos), posts through
// facebed then yt-dlp (a video post, or the video of a post facebed shows as
// text only), videos and unknown shapes through yt-dlp then facebed.
func facebookLinkMethods(rawURL string) []ExtractionMethod {
	result := []ExtractionMethod{MethodYtDlp, MethodFacebookFacebed}
	kind := facebookLinkKindOf(rawURL)

	if kind == facebookLinkPost {
		result = []ExtractionMethod{MethodFacebookFacebed, MethodYtDlp}
	}

	if kind == facebookLinkPhoto {
		result = []ExtractionMethod{MethodFacebookFacebed}
	}

	return result
}

// facebookLinkKindOf classifies a link by its path; photo and video shapes
// win over the post shapes they overlap (share/r/, share/v/).
func facebookLinkKindOf(rawURL string) facebookLinkKind {
	result := facebookLinkOther
	path := ""
	parsed, err := url.Parse(rawURL)

	if err == nil {
		path = parsed.Path
	}

	isPhoto := facebookPhotoPathPattern.MatchString(path)
	isVideo := facebookVideoPathPattern.MatchString(path)
	isPost := facebookPostPathPattern.MatchString(path)

	if isPost == true {
		result = facebookLinkPost
	}

	if isVideo == true {
		result = facebookLinkVideo
	}

	if isPhoto == true {
		result = facebookLinkPhoto
	}

	return result
}

// ExtractMedia reads the post's photos from facebed. A text-only post fails
// with a lastResortError carrying its text item, sent only if no later method
// finds the post's media (facebed doesn't see embedded or shared videos).
// Video posts fail (yt-dlp handles video), so do empty posts, the login
// card, another post than the requested one and the refused shapes.
func (e *FacebookFacebedExtractor) ExtractMedia(ctx context.Context, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	post, err := e.readPost(ctx, postURL)

	if err == nil {
		err = checkFacebedPost(post, postURL)
	}

	if err == nil {
		result, err = e.buildMedia(ctx, post, postURL)
	}

	reportFacebedOutcome(err)

	return result, err
}

// checkHealth reads a known public photo post and downloads its first photo
// without keeping it, the way a user's link is handled.
func (e *FacebookFacebedExtractor) checkHealth(ctx context.Context, postURL string) error {
	var photo facebedPhoto
	post, err := e.readPost(ctx, postURL)

	if (err == nil) && (len(post.Photos) == 0) {
		err = fmt.Errorf("%w: no og:image on the page", errFacebedNoPhoto)
	}

	if err == nil {
		photo, err = e.fetchPhoto(ctx, post.Photos[0], io.Discard)
	}

	if err == nil {
		err = e.validatePhoto(photo)
	}

	return err
}

// readPost asks facebed for the post and checks that the answer is about it.
func (e *FacebookFacebedExtractor) readPost(ctx context.Context, postURL string) (facebedPost, error) {
	var result facebedPost
	var page string
	facebedPath, err := buildFacebedPath(postURL)

	if err == nil {
		pageURL := fmt.Sprintf("%s%s", e.base, facebedPath)
		page, err = e.fetch(ctx, pageURL)
	}

	if err == nil {
		result, err = parseFacebedPage(page)
	}

	if err == nil {
		postID := requestedFacebookPostID(postURL)
		err = checkFacebedPostMatches(result, postID)
	}

	return result, err
}

// buildFacebedPath returns the path and query facebed is asked for: the host
// is dropped (www., m., web. and mbasic. alike), tracking parameters too, and
// the /<page>/photos/ form is rewritten to photo/?fbid=.
func buildFacebedPath(rawURL string) (string, error) {
	result := ""
	parsed, err := url.Parse(rawURL)

	if err == nil {
		err = checkFacebookPath(parsed)
	}

	if err == nil {
		result = facebedPathAndQuery(parsed)
	}

	return result, err
}

// checkFacebookPath refuses non-Facebook hosts and the shapes facebed can't
// read, before any request is made.
func checkFacebookPath(parsed *url.URL) error {
	var result error
	host := strings.ToLower(parsed.Hostname())
	isFacebook := hostMatchesDomain(host, facebookOriginDomain)
	isRefused := isRefusedFacebookPath(parsed.Path)

	if isFacebook == false {
		result = fmt.Errorf("not a Facebook URL: %s", parsed.Redacted())
	}

	if (isFacebook == true) && (isRefused == true) {
		result = fmt.Errorf("%w: %s", errFacebookUnsupportedPath, parsed.Path)
	}

	return result
}

// isRefusedFacebookPath reports the site root, profiles, /<page>/photos
// paths without a photo id and the landings matched by
// facebookRefusedPathPattern.
func isRefusedFacebookPath(path string) bool {
	segmentMatch := facebookSingleSegmentPattern.FindStringSubmatch(path)
	isProfile := false

	if len(segmentMatch) == 2 {
		segment := strings.ToLower(segmentMatch[1])
		isProfile = (facebedSingleSegmentRoutes[segment] == false)
	}

	isRoot := (strings.Trim(path, "/") == "")
	isLanding := facebookRefusedPathPattern.MatchString(path)
	isPhotosPath := facebookPhotosPrefixPattern.MatchString(path)
	isPhoto := facebookPhotosPathPattern.MatchString(path)
	isPhotosWithoutID := (isPhotosPath == true) && (isPhoto == false)
	result := (isRoot == true) || (isProfile == true) || (isLanding == true) || (isPhotosWithoutID == true)

	return result
}

// requestedFacebookPostID returns the post id the link names (fbid,
// story_fbid, the photo, post, reel or video id of the path), or "" when it
// names none or only an opaque pfbid id.
func requestedFacebookPostID(rawURL string) string {
	result := ""
	parsed, err := url.Parse(rawURL)

	if err == nil {
		result = facebookPostIDOf(parsed)
	}

	isOpaque := strings.HasPrefix(result, facebookOpaqueIDPrefix)

	if isOpaque == true {
		result = ""
	}

	return result
}

// facebookPostIDOf reads the id from the query of a one-segment route or
// from the path.
func facebookPostIDOf(parsed *url.URL) string {
	result := ""
	trimmedPath := strings.Trim(parsed.Path, "/")
	segment := strings.ToLower(trimmedPath)
	param, hasParam := facebookPostIDParams[segment]

	if hasParam == true {
		values := parsed.Query()
		result = values.Get(param)
	}

	for _, pattern := range facebookPostIDPatterns {
		match := pattern.FindStringSubmatch(parsed.Path)

		if (result == "") && (len(match) == 2) {
			result = match[1]
		}
	}

	return result
}

// checkFacebedPostMatches requires facebed's og:url to name the requested
// post id in its own id position (fbid, story_fbid or the path id), not just
// anywhere: the photo set or the owner id must not pass for the post (facebed
// answers a link it misreads with another post).
func checkFacebedPostMatches(post facebedPost, postID string) error {
	var result error
	answeredID := ""
	parsed, err := url.Parse(post.CanonicalURL)

	if err == nil {
		answeredID = facebookPostIDOf(parsed)
	}

	isOtherPost := (postID != "") && (answeredID != postID)

	if isOtherPost == true {
		result = fmt.Errorf("%w: asked for %s, got %s", errFacebedWrongPost, postID, post.CanonicalURL)
	}

	return result
}

// facebedPathAndQuery builds "<path>?<kept query>".
func facebedPathAndQuery(parsed *url.URL) string {
	values := parsed.Query()
	query := keptFacebookQuery(values)
	photoMatch := facebookPhotosPathPattern.FindStringSubmatch(parsed.Path)
	result := parsed.Path

	if len(photoMatch) == 3 {
		result = "/photo/"
		query = facebookPhotoQuery(photoMatch[2], photoMatch[1])
	}

	if query != "" {
		result = fmt.Sprintf("%s?%s", result, query)
	}

	return result
}

// keptFacebookQuery keeps only the parameters facebed routes on.
func keptFacebookQuery(values url.Values) string {
	kept := url.Values{}

	for name, list := range values {
		if facebedKeptQueryParams[name] == true {
			kept[name] = list
		}
	}

	result := kept.Encode()

	return result
}

// facebookPhotoQuery builds "fbid=<id>[&set=<set>]".
func facebookPhotoQuery(photoID, set string) string {
	values := url.Values{"fbid": []string{photoID}}

	if set != "" {
		values.Set("set", set)
	}

	result := values.Encode()

	return result
}

// fetch downloads a facebed page with the bot UA (a browser UA is redirected
// to facebook.com). Failures other than the chain's deadline are outages.
func (e *FacebookFacebedExtractor) fetch(ctx context.Context, pageURL string) (string, error) {
	headers := map[string]string{"User-Agent": redirectorBotUserAgent}
	result, err := fetchCappedPage(ctx, pageURL, headers, facebedMaxPage, facebedFetchTimeout)
	isCancelled := (ctx.Err() != nil)

	if (err != nil) && (isCancelled == false) {
		err = wrapFacebedBroken(err)
	}

	return result, err
}

// parseFacebedPage reads the og/twitter tags. A page with a title but no
// facebed site name is the login (or "Banned") card; a page with neither
// means facebed is broken or changed its format.
func parseFacebedPage(page string) (facebedPost, error) {
	result := facebedPost{}
	var err error
	matches := threadsMetaPattern.FindAllStringSubmatch(page, -1)

	for _, match := range matches {
		value := html.UnescapeString(match[2])
		result = applyFacebedTag(result, match[1], value)
	}

	hasSiteName := strings.HasPrefix(result.SiteName, facebedSiteNamePrefix)
	isPost := (hasSiteName == true) && (result.CanonicalURL != "")
	isCard := (isPost == false) && (result.Author != "")

	if isCard == true {
		err = errFacebedLoginCard
	}

	if (isPost == false) && (isCard == false) {
		err = fmt.Errorf("%w: no facebed post tags on the page", errFacebedBroken)
	}

	return result, err
}

// applyFacebedTag adds one meta tag to the post.
func applyFacebedTag(post facebedPost, name, value string) facebedPost {
	result := post
	isVideoTag := (name == "og:video") || (name == "og:video:secure_url") || (name == "twitter:player:stream")
	isNewPhoto := (name == "og:image") && (value != "") && (slices.Contains(result.Photos, value) == false)

	switch name {
	case "og:title":
		result.Author = value
	case "og:description":
		result.Text = value
	case "og:site_name":
		result.SiteName = value
	case "og:url":
		result.CanonicalURL = value
	}

	if (isVideoTag == true) && (value != "") {
		result.HasVideo = true
	}

	if isNewPhoto == true {
		result.Photos = append(result.Photos, value)
	}

	return result
}

// checkFacebedPost fails what can't be sent: a video post (facebed drops
// its images), a photo or video link answered without media (its media is
// lost, the text alone would pass for the post), a text-only answer whose
// og:url names no single post (a link without a comparable id answered with
// a page, profile or group landing) and a post with neither media nor text.
func checkFacebedPost(post facebedPost, postURL string) error {
	var result error
	hasPhotos := (len(post.Photos) > 0)
	isMediaLink := isFacebookMediaLink(postURL)
	isPostAnswer := isFacebookPostURL(post.CanonicalURL)
	trimmedText := strings.TrimSpace(post.Text)
	isTextPost := (hasPhotos == false) && (post.HasVideo == false)

	if (isTextPost == true) && (trimmedText == "") {
		result = errFacebedEmptyPost
	}

	if (isTextPost == true) && (isPostAnswer == false) {
		result = fmt.Errorf("%w: a text-only answer that is not a post (%s)", errFacebedWrongPost, post.CanonicalURL)
	}

	if (isTextPost == true) && (isMediaLink == true) {
		result = fmt.Errorf("%w (photo or video link answered without media)", errFacebedNoPhoto)
	}

	if post.HasVideo == true {
		result = fmt.Errorf("%w (video post, left to yt-dlp)", errFacebedNoPhoto)
	}

	return result
}

// isFacebookMediaLink reports a photo or video link.
func isFacebookMediaLink(postURL string) bool {
	kind := facebookLinkKindOf(postURL)
	result := (kind == facebookLinkPhoto) || (kind == facebookLinkVideo)

	return result
}

// isFacebookPostURL reports a URL naming one post by its id: a page or group
// post, permalink.php, story.php or a photo (not a page, profile or group
// landing, not a reel or video).
func isFacebookPostURL(rawURL string) bool {
	result := false
	kind := facebookLinkKindOf(rawURL)
	isPostShape := (kind == facebookLinkPost) || (kind == facebookLinkPhoto)
	parsed, err := url.Parse(rawURL)

	if (err == nil) && (isPostShape == true) {
		postID := facebookPostIDOf(parsed)
		result = (postID != "")
	}

	return result
}

// buildMedia downloads the post's photos; a text-only post fails with its
// text item kept as a last resort.
func (e *FacebookFacebedExtractor) buildMedia(ctx context.Context, post facebedPost, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error

	if len(post.Photos) == 0 {
		textPost := facebedTextPost(post, postURL)
		err = &lastResortError{Media: textPost, Reason: facebedTextPostReason}
	}

	if len(post.Photos) > 0 {
		result, err = e.downloadPhotos(ctx, post, postURL)
	}

	return result, err
}

// facebedTextPost returns a text-only post as one text item (sent as a
// message with the original link appended). A link-share post comes without
// its link preview (facebed doesn't expose it); the original link leads to
// it.
func facebedTextPost(post facebedPost, postURL string) []*MediaInfo {
	caption := fitFacebedTextMessage(post, postURL, telegramMessageLimit)
	result := []*MediaInfo{{Type: MediaTypeText, URL: postURL, Caption: caption}}

	return result
}

// fitFacebedTextMessage formats the author line and the text, fitted in one
// pass so the message with the original link the sender appends is at most
// limit Telegram units long. The text ends with the truncation marker only
// when facebed or this fit cut it.
func fitFacebedTextMessage(post facebedPost, postURL string, limit int) string {
	isFacebedCut := isCutByFacebed(post.Text)
	text := strings.ReplaceAll(post.Text, "\r\n", "\n")
	displayName := html.EscapeString(post.Author)
	linkSuffix := FormatOriginalLink(postURL)
	measure := func(prefix string) int {
		isCut := (isFacebedCut == true) || (len(prefix) < len(text))
		caption := facebedTextCaption(displayName, prefix, isCut)
		message := fmt.Sprintf("%s%s", caption, linkSuffix)

		return telegramTextLength(message)
	}
	end := fitPrefixEnd(text, limit, measure)
	fittedEnd := max(end, 0)
	isCut := (isFacebedCut == true) || (fittedEnd < len(text))
	result := facebedTextCaption(displayName, text[:fittedEnd], isCut)

	return result
}

// isCutByFacebed reports a text facebed may have cut: raw is og:description
// as facebed sent it (entities decoded, not trimmed or normalised), counted
// in code points like facebed's slice.
func isCutByFacebed(raw string) bool {
	codePoints := utf8.RuneCountInString(raw)
	result := (codePoints >= facebedTextLimit)

	return result
}

// facebedTextCaption formats the escaped author line and the plain text
// (escaped as it is), ended with the truncation marker when isCut.
func facebedTextCaption(displayName, text string, isCut bool) string {
	escaped := escapeCaptionLines(text, false, html.EscapeString)

	if isCut == true {
		escaped = fmt.Sprintf("%s%s", escaped, facebedTruncationMarker)
	}

	result := FormatAuthorCaption(FacebookPlatform, displayName, "", escaped)

	return result
}

// telegramTextLength is how Telegram counts an HTML message: tags become
// entities and leave the text, entities are decoded, the rest is counted
// in UTF-16 code units.
func telegramTextLength(htmlText string) int {
	withoutTags := telegramTagPattern.ReplaceAllString(htmlText, "")
	text := html.UnescapeString(withoutTags)
	runes := []rune(text)
	units := utf16.Encode(runes)
	result := len(units)

	return result
}

// downloadPhotos downloads the photos (facebed lists up to 4) in parallel
// with ctx, keeping their order; the first one carries the caption. Photos
// that fail are logged and left out; it fails only when none succeeds.
func (e *FacebookFacebedExtractor) downloadPhotos(ctx context.Context, post facebedPost, postURL string) ([]*MediaInfo, error) {
	var wg sync.WaitGroup
	timestamp := time.Now().UnixNano()
	items := make([]*MediaInfo, len(post.Photos))
	itemErrs := make([]error, len(post.Photos))
	slots := make(chan struct{}, facebedPhotoConcurrency)

	for i, photoURL := range post.Photos {
		wg.Add(1)

		go func(index int, photoURL string) {
			defer wg.Done()

			slots <- struct{}{}
			defer func() { <-slots }()

			basePath := e.photoBasePath(timestamp, index)
			items[index], itemErrs[index] = e.downloadPhoto(ctx, photoURL, basePath)
		}(i, photoURL)
	}

	wg.Wait()

	result, err := collectFacebedPhotos(items, itemErrs, postURL)

	if err == nil {
		result[0].Caption = facebedCaption(post, postURL)
		appendOriginalURL(result, postURL)
	}

	return result, err
}

// collectFacebedPhotos keeps the downloaded photos in order and logs the
// failed ones; with none downloaded it returns every failure joined.
func collectFacebedPhotos(items []*MediaInfo, itemErrs []error, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	var failures []error

	for i, item := range items {
		if itemErrs[i] == nil {
			item.URL = postURL
			result = append(result, item)
		}

		if itemErrs[i] != nil {
			log.Printf("❌ [facebed] photo %d failed: %v", (i + 1), itemErrs[i])
			failures = append(failures, itemErrs[i])
		}
	}

	if len(result) == 0 {
		joined := errors.Join(failures...)
		err = fmt.Errorf("no Facebook photo could be downloaded for %s: %w", postURL, joined)
	}

	return result, err
}

// photoBasePath is the temp path of one photo before it gets its extension.
func (e *FacebookFacebedExtractor) photoBasePath(timestamp int64, index int) string {
	filename := fmt.Sprintf("%s_%d_%d", facebedFilePrefix, timestamp, (index + 1))
	result := filepath.Join(e.tempDir, filename)

	return result
}

// downloadPhoto downloads one photo to basePath and validates it before it
// is sent; a rejected photo is removed.
func (e *FacebookFacebedExtractor) downloadPhoto(ctx context.Context, photoURL, basePath string) (*MediaInfo, error) {
	var result *MediaInfo
	var photo facebedPhoto
	var out *os.File
	err := ctx.Err()

	if err == nil {
		out, err = os.Create(basePath)
	}

	if err == nil {
		photo, err = e.fetchPhoto(ctx, photoURL, out)
		closeErr := out.Close()

		if err == nil {
			err = closeErr
		}
	}

	if err == nil {
		err = e.validatePhoto(photo)
	}

	if err != nil {
		os.Remove(basePath)
	}

	if err == nil {
		filePath := renameWithMediaExt(basePath, photo.ContentType, photo.FinalURL)
		result = &MediaInfo{Type: MediaTypePhoto, Filename: filePath}
	}

	return result, err
}

// fetchPhoto copies one photo into out: only from the media CDN (checked
// before the request and on every redirect), cancelled with ctx and cut one
// byte past the photo limit, so validatePhoto rejects it.
func (e *FacebookFacebedExtractor) fetchPhoto(ctx context.Context, photoURL string, out io.Writer) (facebedPhoto, error) {
	result := facebedPhoto{}
	var resp *http.Response
	err := e.checkPhotoURL(photoURL)

	if err == nil {
		resp, err = e.requestPhoto(ctx, photoURL)
	}

	if err == nil {
		defer resp.Body.Close()

		limited := io.LimitReader(resp.Body, (facebedMaxPhotoSize + 1))
		result.FinalURL = resp.Request.URL.String()
		result.ContentType = resp.Header.Get("Content-Type")
		result.Size, err = io.Copy(out, limited)
	}

	return result, err
}

// checkPhotoURL accepts only a URL on the media CDN over the media scheme,
// so a changed facebed page can't point the bot at another host.
func (e *FacebookFacebedExtractor) checkPhotoURL(photoURL string) error {
	var result error
	parsed, err := url.Parse(photoURL)
	isMediaURL := (err == nil) && (parsed.Scheme == e.mediaScheme) && (e.isMediaHost(photoURL) == true)

	if isMediaURL == false {
		result = fmt.Errorf("%w (%s): %s", errFacebedPhotoOffCDN, e.mediaDomain, photoURL)
	}

	return result
}

// checkPhotoRedirect keeps every redirect of a photo download on the CDN.
func (e *FacebookFacebedExtractor) checkPhotoRedirect(req *http.Request, via []*http.Request) error {
	redirectURL := req.URL.String()
	result := e.checkPhotoURL(redirectURL)

	if (result == nil) && (len(via) >= facebedMaxPhotoRedirects) {
		result = fmt.Errorf("photo stopped after %d redirects", facebedMaxPhotoRedirects)
	}

	return result
}

// requestPhoto GETs a photo with the bot UA. A transport failure (not the
// chain's deadline, not an off-CDN redirect) and a non-2xx answer are
// marked errFacebedBroken.
func (e *FacebookFacebedExtractor) requestPhoto(ctx context.Context, photoURL string) (*http.Response, error) {
	var result *http.Response
	baseClient := e.basePhotoClient()
	client := &http.Client{Transport: baseClient.Transport, Timeout: baseClient.Timeout, CheckRedirect: e.checkPhotoRedirect}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, photoURL, nil)

	if err == nil {
		req.Header.Set("User-Agent", redirectorBotUserAgent)
		result, err = client.Do(req)
		isCancelled := (ctx.Err() != nil)
		isOffCDN := errors.Is(err, errFacebedPhotoOffCDN)
		isTransportFailure := (err != nil) && (isCancelled == false) && (isOffCDN == false)

		if isTransportFailure == true {
			err = wrapFacebedBroken(err)
		}
	}

	isSuccess := (err == nil) && (result.StatusCode >= http.StatusOK) && (result.StatusCode < http.StatusMultipleChoices)

	if (err == nil) && (isSuccess == false) {
		result.Body.Close()
		err = fmt.Errorf("%w: photo status %d from %s", errFacebedBroken, result.StatusCode, result.Request.URL.Host)
		result = nil
	}

	return result, err
}

// basePhotoClient returns the client whose transport and timeout photo
// downloads use.
func (e *FacebookFacebedExtractor) basePhotoClient() *http.Client {
	result := SharedDownloadClient

	if e.photoClient != nil {
		result = e.photoClient
	}

	return result
}

// validatePhoto applies the kk redirector checks (off facebed's and
// Facebook's hosts, media content type, plausible size), and also requires
// an image served from Facebook's CDN within Telegram's photo limit.
func (e *FacebookFacebedExtractor) validatePhoto(photo facebedPhoto) error {
	pageHost := e.pageHost()
	err := validateRedirectorMedia(pageHost, facebookOriginDomain, photo.FinalURL, photo.ContentType, photo.Size)
	isMediaHost := e.isMediaHost(photo.FinalURL)
	mimeType := extractMimeType(photo.ContentType)
	isImage := strings.HasPrefix(mimeType, "image/")

	if (err == nil) && (isMediaHost == false) {
		err = fmt.Errorf("%w (%s): %s", errFacebedPhotoOffCDN, e.mediaDomain, photo.FinalURL)
	}

	if (err == nil) && (isImage == false) {
		err = fmt.Errorf("non-image content-type %q", photo.ContentType)
	}

	if (err == nil) && (photo.Size > facebedMaxPhotoSize) {
		err = fmt.Errorf("photo is above the %d-byte photo limit", facebedMaxPhotoSize)
	}

	return err
}

// pageHost returns facebed's host name.
func (e *FacebookFacebedExtractor) pageHost() string {
	result := ""
	parsed, err := url.Parse(e.base)

	if err == nil {
		result = parsed.Hostname()
	}

	return result
}

// isMediaHost reports a URL on the media CDN domain.
func (e *FacebookFacebedExtractor) isMediaHost(mediaURL string) bool {
	result := false
	parsed, err := url.Parse(mediaURL)

	if err == nil {
		host := strings.ToLower(parsed.Hostname())
		result = hostMatchesDomain(host, e.mediaDomain)
	}

	return result
}

// renameWithMediaExt gives a downloaded file its extension; the file keeps
// its name when the rename fails.
func renameWithMediaExt(basePath, contentType, finalURL string) string {
	result := basePath
	_, ext := classifyRedirectorMedia(contentType, finalURL)
	filePath := fmt.Sprintf("%s%s", basePath, ext)
	err := os.Rename(basePath, filePath)

	if err == nil {
		result = filePath
	}

	return result
}

// wrapFacebedBroken marks an error as a facebed outage signal.
func wrapFacebedBroken(err error) error {
	result := err

	if err != nil {
		result = fmt.Errorf("%w: %v", errFacebedBroken, err)
	}

	return result
}

// reportFacebedOutcome feeds the outage tracker: a success (a text post
// kept as a last resort too), or a failure marked errFacebedBroken (not the
// login card, another post, refused shapes, video or empty posts).
func reportFacebedOutcome(err error) {
	isBroken := errors.Is(err, errFacebedBroken)
	keptMedia := lastResortMediaOf(nil, err)
	isSuccess := (err == nil) || (keptMedia != nil)

	if isSuccess == true {
		ReportServiceSuccess(facebedServiceName)
	}

	if isBroken == true {
		message := err.Error()
		ReportServiceFailure(facebedServiceName, message)
	}
}

// facebedCaption formats author and text (plain text, escaped for HTML),
// the text fitted into what the caption limit leaves next to the author line
// and the original link.
func facebedCaption(post facebedPost, postURL string) string {
	caption := authorCaption{Platform: FacebookPlatform, DisplayName: post.Author, Text: post.Text, PostURL: postURL, PlainText: true}
	result := buildAuthorCaption(caption)

	return result
}
