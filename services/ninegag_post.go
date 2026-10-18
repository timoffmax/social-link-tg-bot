package services

// 9GAG posts as the 9GAG API returns them (scripts/9gag_fetch.py prints the
// API's data.post): photo and "Animated" posts carry their files in images,
// "Article" posts (9GAGNews) a list of blocks: RichText (markdown links and
// "> " quotes) and Media, each Media block naming an entry of article.medias
// (a Photo, an Animated MP4, or an embedded YouTube Video).

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

const (
	// NineGagPlatform is the platform key of 9GAG.
	NineGagPlatform = "9gag"

	// ninegagTitlePrefix starts a 9GAG caption.
	ninegagTitlePrefix = "😂"

	// ninegagTypeAnimated, ninegagTypePhoto, ninegagTypeVideo and
	// ninegagTypeArticle are the post and media types of the API.
	ninegagTypeAnimated = "Animated"
	ninegagTypePhoto    = "Photo"
	ninegagTypeVideo    = "Video"
	ninegagTypeArticle  = "Article"

	// ninegagBlockText and ninegagBlockMedia are the article block types.
	ninegagBlockText  = "RichText"
	ninegagBlockMedia = "Media"

	// ninegagImageLarge, ninegagImageSmall and ninegagImageVideo are the
	// image keys holding the photo and the MP4.
	ninegagImageLarge = "image700"
	ninegagImageSmall = "image460"
	ninegagImageVideo = "image460sv"

	// ninegagVideoSourceYouTube marks an embedded YouTube video.
	ninegagVideoSourceYouTube = "YouTube"

	// ninegagYouTubeLinkTemplate links an embedded YouTube video.
	ninegagYouTubeLinkTemplate = "▶️ https://www.youtube.com/watch?v=%s"

	// ninegagArticleMaxMedia caps the files of one article (sent as albums
	// of up to 10).
	ninegagArticleMaxMedia = 20

	// ninegagDownloadConcurrency is how many article files download at once.
	ninegagDownloadConcurrency = 4

	// ninegagQuotePrefix starts a quoted line of a RichText block (escaped).
	ninegagQuotePrefix = "&gt; "

	// ninegagQuoteTemplate renders a quoted line.
	ninegagQuoteTemplate = "<blockquote>%s</blockquote>"

	// ninegagLinkTemplate renders a markdown link.
	ninegagLinkTemplate = `<a href="$2">$1</a>`

	// ninegagTruncationMarker ends an article text cut to the message limit.
	ninegagTruncationMarker = "…"

	// ninegagParagraphSeparator separates the article's paragraphs.
	ninegagParagraphSeparator = "\n\n"

	// ninegagFileTemplate names a post's file "<download prefix>9gag<ext>",
	// ninegagArticleFileTemplate an article's n-th file.
	ninegagFileTemplate        = "%s9gag%s"
	ninegagArticleFileTemplate = "%s9gag_%d%s"

	// ninegagMediaDomain serves 9GAG's photos and MP4s
	// (img-9gag-fun.9cache.com, miscmedia-9gag-fun.9cache.com).
	ninegagMediaDomain = "9cache.com"

	// ninegagMediaScheme is the only scheme 9GAG media is fetched over.
	ninegagMediaScheme = "https"
)

// errNinegagOffCDN marks a 9GAG file URL (or a redirect) off 9GAG's CDN.
var errNinegagOffCDN = errors.New("9GAG media URL is not on 9GAG's CDN")

// ninegagDownloadHeaders are sent with every 9GAG CDN download.
var ninegagDownloadHeaders = map[string]string{
	"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Accept":          "*/*",
	"Accept-Language": "en-US,en;q=0.9",
	"Referer":         "https://9gag.com/",
}

// ninegagMarkdownLinkPattern matches a markdown link of escaped text; the
// URL can't hold a quote (escaped to &#34;), so it stays inside href.
var ninegagMarkdownLinkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)

// ninegagPost is the API's post.
type ninegagPost struct {
	Error   string                  `json:"error"`
	ID      string                  `json:"id"`
	Title   string                  `json:"title"`
	Type    string                  `json:"type"`
	NSFW    int                     `json:"nsfw"`
	Images  map[string]ninegagImage `json:"images"`
	Article *ninegagArticle         `json:"article"`
}

// ninegagImage is one rendition of a post or article media.
type ninegagImage struct {
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	VP9URL   string `json:"vp9Url"`
	H265URL  string `json:"h265Url"`
	Duration int    `json:"duration"`
	HasAudio int    `json:"hasAudio"`
}

// ninegagArticle is the body of an Article post.
type ninegagArticle struct {
	Blocks []ninegagBlock                 `json:"blocks"`
	Medias map[string]ninegagArticleMedia `json:"medias"`
}

// ninegagBlock is one article block.
type ninegagBlock struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	MediaID string `json:"mediaId"`
}

// ninegagArticleMedia is one media of an article.
type ninegagArticleMedia struct {
	ID     string                  `json:"id"`
	Type   string                  `json:"type"`
	Images map[string]ninegagImage `json:"images"`
	Video  *ninegagEmbeddedVideo   `json:"video"`
}

// ninegagEmbeddedVideo is a video an article embeds from another site.
type ninegagEmbeddedVideo struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

// ninegagFile is one file to download: its URL and Telegram media type.
type ninegagFile struct {
	URL       string
	MediaType string
}

// parseNinegagPost decodes the helper's output.
func parseNinegagPost(raw []byte) (ninegagPost, error) {
	result := ninegagPost{}
	err := json.Unmarshal(raw, &result)

	if err != nil {
		err = fmt.Errorf("failed to parse 9gag_fetch.py output: %v", err)
	}

	if (err == nil) && (result.Error != "") {
		err = fmt.Errorf("9GAG API error: %s", result.Error)
	}

	return result, err
}

// NinegagCaption returns "😂 <title>" escaped for Telegram HTML ("" without a
// title); the API and yt-dlp may hand titles with entities.
func NinegagCaption(title string) string {
	result := ""
	trimmed := strings.TrimSpace(title)

	if trimmed != "" {
		escaped := EscapeForTelegramHTML(trimmed)
		result = fmt.Sprintf("%s %s", ninegagTitlePrefix, escaped)
	}

	return result
}

// isNinegagArticle reports an Article post with blocks.
func isNinegagArticle(post ninegagPost) bool {
	hasBlocks := (post.Article != nil) && (len(post.Article.Blocks) > 0)
	result := (ninegagTypeArticle == post.Type) && (hasBlocks == true)

	return result
}

// ninegagMediaFile returns the file of a media: the H.265 MP4 (else the MP4)
// of an Animated one, the large (else small) image of the rest.
func ninegagMediaFile(mediaType string, images map[string]ninegagImage) (ninegagFile, bool) {
	result := ninegagFile{}
	video := images[ninegagImageVideo]
	large := images[ninegagImageLarge]
	small := images[ninegagImageSmall]
	isAnimated := (ninegagTypeAnimated == mediaType) && (video.URL != "")

	if small.URL != "" {
		result = ninegagFile{URL: small.URL, MediaType: MediaTypePhoto}
	}

	if large.URL != "" {
		result = ninegagFile{URL: large.URL, MediaType: MediaTypePhoto}
	}

	if isAnimated == true {
		result = ninegagFile{URL: video.URL, MediaType: MediaTypeVideo}
	}

	if (isAnimated == true) && (video.H265URL != "") {
		result.URL = video.H265URL
	}

	isFound := (result.URL != "")

	return result, isFound
}

// ninegagArticleFiles returns the photos and MP4s of the article's Media
// blocks in order, without duplicates, at most ninegagArticleMaxMedia. An
// embedded YouTube video has no file (the text links it).
func ninegagArticleFiles(article *ninegagArticle) []ninegagFile {
	result := []ninegagFile{}
	seen := map[string]bool{}

	for _, block := range article.Blocks {
		media, isKnown := article.Medias[block.MediaID]
		isMedia := (ninegagBlockMedia == block.Type) && (isKnown == true) && (ninegagTypeVideo != media.Type)
		file, hasFile := ninegagMediaFile(media.Type, media.Images)
		isNew := (isMedia == true) && (hasFile == true) && (seen[file.URL] == false)
		hasRoom := (len(result) < ninegagArticleMaxMedia)

		if (isNew == true) && (hasRoom == true) {
			seen[file.URL] = true
			result = append(result, file)
		}
	}

	return result
}

// ninegagArticleParagraphs returns the article's text in order: each
// RichText block, and a link line for each embedded YouTube video.
func ninegagArticleParagraphs(article *ninegagArticle) []string {
	result := []string{}

	for _, block := range article.Blocks {
		paragraph := ninegagBlockParagraph(article, block)

		if paragraph != "" {
			result = append(result, paragraph)
		}
	}

	return result
}

// ninegagBlockParagraph returns the plain text a block adds ("" for none).
func ninegagBlockParagraph(article *ninegagArticle, block ninegagBlock) string {
	result := ""
	media, isKnown := article.Medias[block.MediaID]
	isText := (ninegagBlockText == block.Type)
	isEmbed := (ninegagBlockMedia == block.Type) && (isKnown == true) && (media.Video != nil)
	isYouTube := (isEmbed == true) && (ninegagVideoSourceYouTube == media.Video.Source) && (media.Video.ID != "")

	if isText == true {
		result = strings.TrimSpace(block.Content)
	}

	if isYouTube == true {
		result = fmt.Sprintf(ninegagYouTubeLinkTemplate, media.Video.ID)
	}

	return result
}

// ninegagArticleText returns the article as one message: "😂 <title>", then
// the paragraphs (escaped, markdown links as links, "> " lines as quotes),
// cut to Telegram's message limit with the original link the sender appends.
func ninegagArticleText(post ninegagPost, postURL string) string {
	paragraphs := ninegagArticleParagraphs(post.Article)
	body := strings.Join(paragraphs, ninegagParagraphSeparator)
	title := NinegagCaption(post.Title)
	build := func(bodyPrefix string, isCut bool) string {
		return ninegagArticleMessage(title, bodyPrefix, isCut)
	}
	result := fitTextMessage(body, postURL, build)

	return result
}

// ninegagArticleMessage formats the title and a prefix of the plain body.
func ninegagArticleMessage(title, body string, isCut bool) string {
	parts := []string{}
	formatted := formatNinegagRichText(body)

	if isCut == true {
		formatted = fmt.Sprintf("%s%s", formatted, ninegagTruncationMarker)
	}

	if title != "" {
		parts = append(parts, title)
	}

	if formatted != "" {
		parts = append(parts, formatted)
	}

	result := strings.Join(parts, ninegagParagraphSeparator)

	return result
}

// formatNinegagRichText escapes plain RichText for Telegram HTML, turns whole
// markdown links into links and "> " lines into quotes (one per line, so a
// cut never leaves a tag open). A link cut in half stays plain text.
func formatNinegagRichText(text string) string {
	escaped := html.EscapeString(text)
	linked := ninegagMarkdownLinkPattern.ReplaceAllString(escaped, ninegagLinkTemplate)
	lines := strings.Split(linked, "\n")

	for i, line := range lines {
		quoted, isQuote := strings.CutPrefix(line, ninegagQuotePrefix)

		if isQuote == true {
			lines[i] = fmt.Sprintf(ninegagQuoteTemplate, quoted)
		}
	}

	result := strings.Join(lines, "\n")

	return result
}

// checkNinegagMediaURL accepts only an https URL on 9GAG's CDN; it runs
// before a 9GAG file download and on every redirect (fromURL is unused).
func checkNinegagMediaURL(mediaURL, fromURL string) error {
	var result error
	host, scheme := urlHostAndScheme(mediaURL)
	isCDNHost := (host != "") && (hostMatchesDomain(host, ninegagMediaDomain) == true)
	isMediaURL := (scheme == ninegagMediaScheme) && (isCDNHost == true)

	if isMediaURL == false {
		result = fmt.Errorf("%w: %s", errNinegagOffCDN, mediaURL)
	}

	return result
}
