package services

// The pre-send step every platform's files go through (after
// PrepareAnimations): photos are kept within sendPhoto's limits, and a file
// is named by what it holds, not by the name an extractor gave it.

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// contentSniffLength is how many bytes http.DetectContentType reads.
	contentSniffLength = 512

	// mimeTypeJPEG, mimeTypePNG, mimeTypeWebP, mimeTypeMP4 and mimeTypeWebM
	// are the mime types a file is named by.
	mimeTypeJPEG = "image/jpeg"
	mimeTypePNG  = "image/png"
	mimeTypeWebP = "image/webp"
	mimeTypeMP4  = "video/mp4"
	mimeTypeWebM = "video/webm"
)

// MediaContent is what a file holds: its media type (photo, animation,
// video or document) and the extension it is named with.
type MediaContent struct {
	Type      string
	Extension string
}

// errPhotoStillTooBig marks a shrunk photo still above sendPhoto's limit.
var errPhotoStillTooBig = errors.New("photo is still above sendPhoto's limit after shrinking")

// mediaContentByMime names the media files the bot sends by their content.
var mediaContentByMime = map[string]MediaContent{
	mimeTypeJPEG: {Type: MediaTypePhoto, Extension: ".jpg"},
	mimeTypePNG:  {Type: MediaTypePhoto, Extension: ".png"},
	mimeTypeWebP: {Type: MediaTypePhoto, Extension: ".webp"},
	mimeTypeGIF:  {Type: MediaTypeAnimation, Extension: gifExtension},
	mimeTypeMP4:  {Type: MediaTypeVideo, Extension: ".mp4"},
	mimeTypeWebM: {Type: MediaTypeVideo, Extension: ".webm"},
}

// extensionAliases are other spellings of an extension.
var extensionAliases = map[string]string{".jpeg": ".jpg"}

// mediaExtensions are the extensions a renamed file loses; any other
// "extension" may be part of a title (yt-dlp names files after it) and is
// kept.
var mediaExtensions = []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".mp4", ".webm", ".mov", ".mkv", ".image", ".bin"}

// DetectMediaContent reads what a file holds from its first bytes; a file it
// doesn't know (or can't read) is a document keeping its extension.
func DetectMediaContent(path string) MediaContent {
	extension := filepath.Ext(path)
	result := MediaContent{Type: MediaTypeDocument, Extension: strings.ToLower(extension)}
	header := readFileHeader(path)
	contentType := http.DetectContentType(header)
	mimeType := extractMimeType(contentType)
	known, isKnown := mediaContentByMime[mimeType]
	isRead := (len(header) > 0)

	if (isRead == true) && (isKnown == true) {
		result = known
	}

	return result
}

// readFileHeader returns the first bytes of a file (nil when it can't be
// read).
func readFileHeader(path string) []byte {
	var result []byte
	file, err := os.Open(path)

	if err == nil {
		defer file.Close()

		buffer := make([]byte, contentSniffLength)
		count, readErr := io.ReadFull(file, buffer)
		isRead := (readErr == nil) || (readErr == io.ErrUnexpectedEOF)

		if isRead == true {
			result = buffer[:count]
		}
	}

	return result
}

// NameFileByContent renames the item's file so its extension matches what
// it holds (a GIF saved as .jpg becomes .gif) and returns that content; the
// file keeps its name when it already matches or the rename fails.
func NameFileByContent(item *MediaInfo) MediaContent {
	result := DetectMediaContent(item.Filename)
	namedPath := pathWithExtension(item.Filename, result.Extension)
	isRenamed := (namedPath != item.Filename)

	if isRenamed == true {
		err := os.Rename(item.Filename, namedPath)
		logFileRename(item.Filename, namedPath, err)

		if err == nil {
			item.Filename = namedPath
		}
	}

	return result
}

// pathWithExtension replaces a media extension of path (or appends one)
// with extension; a path that has it already comes back as it is.
func pathWithExtension(path, extension string) string {
	current := filepath.Ext(path)
	lowerCurrent := strings.ToLower(current)
	normalized := normalizeExtension(lowerCurrent)
	isMediaExtension := (slices.Contains(mediaExtensions, lowerCurrent) == true)
	hasExtension := (normalized == extension)
	result := path
	base := path

	if isMediaExtension == true {
		base = strings.TrimSuffix(path, current)
	}

	if hasExtension == false {
		result = fmt.Sprintf("%s%s", base, extension)
	}

	return result
}

// normalizeExtension returns the extension a lowercase one is named as
// (".jpeg" is ".jpg").
func normalizeExtension(extension string) string {
	result := extension
	alias, isAlias := extensionAliases[extension]

	if isAlias == true {
		result = alias
	}

	return result
}

// logFileRename logs one rename by content.
func logFileRename(path, namedPath string, err error) {
	oldName := filepath.Base(path)
	newName := filepath.Base(namedPath)

	if err == nil {
		log.Printf("🏷️ File renamed by its content: %s -> %s", oldName, newName)
	}

	if err != nil {
		log.Printf("⚠️ File %s not renamed to %s: %v", oldName, newName, err)
	}
}

// PreparePhotos keeps every photo within sendPhoto's limits before sending:
// one whose sides' ratio sendPhoto refuses (a tall 9GAG post, a long comic
// strip) goes out as a document at full size, since shrinking it would leave
// an unreadable strip; one above sendPhoto's size or width + height limit is
// shrunk, and sent as a document when that fails.
func PreparePhotos(items []*MediaInfo) {
	for _, item := range items {
		isPhoto := (MediaTypePhoto == item.Type)

		if isPhoto == true {
			fitPhoto(item, TelegramPhotoLimitBytes)
		}
	}
}

// fitPhoto applies sendPhoto's limits to one photo (see PreparePhotos); a
// photo it can't read is left as it is.
func fitPhoto(item *MediaInfo, limit int64) {
	info, err := os.Stat(item.Filename)
	width, height := photoDimensions(item.Filename)
	isRead := (err == nil)
	isTooNarrow := (isRead == true) && (breaksSendPhotoRatio(width, height) == true)
	isOverLimit := (isRead == true) && (info.Size() > limit)
	isSumTooBig := (isRead == true) && (breaksSendPhotoSideSum(width, height) == true)
	isShrunk := (isTooNarrow == false) && ((isOverLimit == true) || (isSumTooBig == true))

	if isTooNarrow == true {
		log.Printf("📄 Photo of %dx%d is too narrow for sendPhoto, sending it as a document", width, height)
		item.Type = MediaTypeDocument
	}

	if isShrunk == true {
		size := info.Size()
		shrinkOrSendAsDocument(item, size, limit)
	}
}

// shrinkOrSendAsDocument replaces the photo with a smaller JPEG within limit
// bytes, or makes it a document when that can't be made.
func shrinkOrSendAsDocument(item *MediaInfo, size, limit int64) {
	shrunkPath, err := shrinkPhoto(item.Filename)

	if err == nil {
		err = checkShrunkPhoto(shrunkPath, limit)
	}

	if err == nil {
		log.Printf("🗜️ Photo of %d bytes shrunk to fit sendPhoto", size)
		os.Remove(item.Filename)
		item.Filename = shrunkPath
	}

	if err != nil {
		log.Printf("📄 Photo of %d bytes not shrunk (%v), sending it as a document", size, err)
		item.Type = MediaTypeDocument
	}
}

// checkShrunkPhoto requires a shrunk photo within limit bytes; a bigger one
// is removed.
func checkShrunkPhoto(path string, limit int64) error {
	var result error
	info, err := os.Stat(path)
	isTooBig := (err == nil) && (info.Size() > limit)

	if err != nil {
		result = err
	}

	if isTooBig == true {
		os.Remove(path)
		result = errPhotoStillTooBig
	}

	return result
}
