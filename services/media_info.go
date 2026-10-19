package services

// Media types and the Telegram caption limit shared by extractors and the
// send path.
const (
	MediaTypePhoto            = "photo"
	MediaTypeVideo            = "video"
	MediaTypeText             = "text"
	MediaTypeAnimation        = "animation"
	MediaTypeDocument         = "document"
	TelegramCaptionLimitBytes = 1024
)

type MediaInfo struct {
	Type        string // "photo", "video", "animation", "text", "document"
	URL         string
	Caption     string
	Filename    string
	ContentType string
	Spoiler     bool // send blurred (Telegram has_spoiler), e.g. age-gated posts
	VideoCover  bool // the cover image of a video post whose video the source did not give
}
