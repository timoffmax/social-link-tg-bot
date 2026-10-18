package services

// GIFs are sent with sendAnimation: sendPhoto turns them into a still first
// frame (and refuses those over 10 MB). Any extractor may hand over a GIF typed
// as a photo, often under a .jpg name, so the file's own signature decides.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// TelegramUploadLimitBytes is the Bot API's limit for an uploaded file
	// (sendVideo, sendAnimation, sendDocument).
	TelegramUploadLimitBytes = int64(50 * 1024 * 1024)

	// TelegramPhotoLimitBytes is sendPhoto's limit.
	TelegramPhotoLimitBytes = int64(10 * 1024 * 1024)

	// gifDownloadLimitBytes caps a GIF download: a GIF above the upload limit
	// is converted to a much smaller MP4.
	gifDownloadLimitBytes = int64(200 * 1024 * 1024)

	// gifSignatureLength is the length of "GIF87a" / "GIF89a".
	gifSignatureLength = 6

	// gifConversionTimeout bounds one ffmpeg GIF to MP4 conversion.
	gifConversionTimeout = 60 * time.Second

	// gifConvertedSuffix ends the name of a GIF converted to MP4.
	gifConvertedSuffix = "_gif.mp4"

	// gifExtension is the extension a GIF file is given.
	gifExtension = ".gif"
)

// gifSignaturePrefix starts every GIF file ("GIF87a" or "GIF89a").
var gifSignaturePrefix = []byte("GIF8")

// IsGIFFile reports a file that starts with the GIF signature.
func IsGIFFile(path string) bool {
	result := false
	header := make([]byte, gifSignatureLength)
	file, err := os.Open(path)

	if err == nil {
		defer file.Close()

		count, readErr := file.Read(header)
		isComplete := (readErr == nil) && (count == gifSignatureLength)
		result = (isComplete == true) && (bytes.HasPrefix(header, gifSignaturePrefix) == true)
	}

	return result
}

// gifEncoding is one ffmpeg H.264 encoding of a GIF: its video filter and
// quality (CRF, higher is smaller).
type gifEncoding struct {
	Filter string
	CRF    string
}

const (
	// gifFullSizeFilter keeps the GIF's size, rounded to even sides (which
	// yuv420p needs); gifFullSizeCRF is its quality.
	gifFullSizeFilter = "scale=trunc(iw/2)*2:trunc(ih/2)*2"
	gifFullSizeCRF    = "23"

	// gifSmallWidth caps the width of the smaller encoding,
	// gifSmallFilterTemplate scales to it (even sides), gifSmallCRF is its
	// lower quality.
	gifSmallWidth          = 480
	gifSmallFilterTemplate = "scale='trunc(min(iw,%d)/2)*2':-2"
	gifSmallCRF            = "32"
)

// gifSmallFilter is the video filter of the smaller encoding.
var gifSmallFilter = fmt.Sprintf(gifSmallFilterTemplate, gifSmallWidth)

// gifEncodings are tried in order until the MP4 fits the upload limit: the
// GIF's own size, then at most gifSmallWidth px wide at a lower quality.
var gifEncodings = []gifEncoding{
	{Filter: gifFullSizeFilter, CRF: gifFullSizeCRF},
	{Filter: gifSmallFilter, CRF: gifSmallCRF},
}

// animationUploadLimit is the most bytes an animation is sent with; a
// variable so tests can lower it.
var animationUploadLimit = TelegramUploadLimitBytes

// errAnimationTooBig marks a GIF whose MP4 is still above the upload limit.
var errAnimationTooBig = errors.New("animation is above Telegram's upload limit even as MP4")

// PrepareAnimations types every GIF photo as an animation, names each GIF
// animation .gif (an extractor may have saved it as .jpg), converts one
// above Telegram's upload limit to MP4 (an animation too) and returns the
// items to send: an animation that can't be brought within the limit is
// left out and its files removed, since Telegram would refuse it anyway.
func PrepareAnimations(items []*MediaInfo) []*MediaInfo {
	result := make([]*MediaInfo, 0, len(items))

	for _, item := range items {
		err := prepareAnimation(item)

		if err != nil {
			log.Printf("⚠️ Animation %s left out: %v", item.Filename, err)
			os.Remove(item.Filename)
		}

		if err == nil {
			result = append(result, item)
		}
	}

	return result
}

// prepareAnimation readies one item when it is (or is a photo holding) a GIF.
func prepareAnimation(item *MediaInfo) error {
	var result error
	isPhotoGIF := (MediaTypePhoto == item.Type) && (IsGIFFile(item.Filename) == true)

	if isPhotoGIF == true {
		item.Type = MediaTypeAnimation
	}

	if MediaTypeAnimation == item.Type {
		NameFileByContent(item)
		result = fitAnimation(item, animationUploadLimit)
	}

	return result
}

// fitAnimation converts an animation GIF above limit bytes to MP4, encoding
// it smaller when the first MP4 is still above limit; it fails when no MP4
// fits (the GIF stays for the caller to remove).
func fitAnimation(item *MediaInfo, limit int64) error {
	var result error
	info, err := os.Stat(item.Filename)
	isTooBig := (err == nil) && (info.Size() > limit)
	isGIF := (isTooBig == true) && (IsGIFFile(item.Filename) == true)

	if isGIF == true {
		size := info.Size()
		convertedPath, convertErr := convertGIFWithinLimit(item.Filename, limit)
		logGIFConversion(item.Filename, size, convertErr)
		result = convertErr

		if convertErr == nil {
			os.Remove(item.Filename)
			item.Filename = convertedPath
		}
	}

	return result
}

// convertGIFWithinLimit tries the encodings in order and returns the first
// MP4 within limit bytes; the bigger ones are removed.
func convertGIFWithinLimit(gifPath string, limit int64) (string, error) {
	result := ""
	err := errAnimationTooBig

	for _, encoding := range gifEncodings {
		isNeeded := (errors.Is(err, errAnimationTooBig) == true)

		if isNeeded == true {
			result, err = convertGIFToMP4(gifPath, encoding)
		}

		if (isNeeded == true) && (err == nil) {
			err = checkConvertedSize(result, limit)
		}
	}

	if err != nil {
		result = ""
	}

	return result, err
}

// checkConvertedSize requires the MP4 within limit bytes; a bigger one is
// removed.
func checkConvertedSize(mp4Path string, limit int64) error {
	var result error
	info, err := os.Stat(mp4Path)
	isTooBig := (err == nil) && (info.Size() > limit)

	if err != nil {
		result = err
	}

	if isTooBig == true {
		size := info.Size()
		log.Printf("🎞️ MP4 of %d bytes is above the %d-byte limit", size, limit)
		os.Remove(mp4Path)
		result = errAnimationTooBig
	}

	return result
}

// logGIFConversion logs the outcome of one conversion.
func logGIFConversion(path string, size int64, err error) {
	if err == nil {
		log.Printf("🎞️ GIF of %d bytes converted to MP4: %s", size, path)
	}

	if err != nil {
		log.Printf("⚠️ GIF of %d bytes not converted to MP4: %v", size, err)
	}
}

// convertGIFToMP4 writes a silent H.264 MP4 of the GIF next to it with the
// encoding and returns its path.
func convertGIFToMP4(gifPath string, encoding gifEncoding) (string, error) {
	var output []byte
	var err error
	result := ""
	ffmpegPath := findFFmpegPath()

	if ffmpegPath == "" {
		err = fmt.Errorf("ffmpeg not available")
	}

	basePath := strings.TrimSuffix(gifPath, gifExtension)
	mp4Path := fmt.Sprintf("%s%s", basePath, gifConvertedSuffix)

	if err == nil {
		baseCtx := context.Background()
		ctx, cancel := context.WithTimeout(baseCtx, gifConversionTimeout)
		defer cancel()

		args := []string{"-i", gifPath, "-movflags", "+faststart", "-pix_fmt", "yuv420p", "-vf", encoding.Filter, "-c:v", "libx264", "-crf", encoding.CRF, "-an", "-y", mp4Path}
		cmd := exec.CommandContext(ctx, ffmpegPath, args...)
		output, err = cmd.CombinedOutput()
	}

	if err != nil {
		os.Remove(mp4Path)
		trimmed := strings.TrimSpace(string(output))
		err = fmt.Errorf("ffmpeg GIF conversion failed: %v (%s)", err, trimmed)
	}

	if err == nil {
		result = mp4Path
	}

	return result, err
}

const (
	// photoShrinkMaxSide is the longest side of a photo shrunk to fit
	// sendPhoto (Telegram shows photos at up to 2560 px).
	photoShrinkMaxSide = 2560

	// photoShrinkSuffix ends the name of a shrunk photo.
	photoShrinkSuffix = "_small.jpg"

	// photoShrinkQuality is ffmpeg's JPEG quality scale (2 best, 31 worst).
	photoShrinkQuality = "3"

	// telegramPhotoMaxSideSum is sendPhoto's limit for width + height.
	telegramPhotoMaxSideSum = 10000

	// telegramPhotoMaxRatio is sendPhoto's limit for the ratio of the long
	// side to the short one.
	telegramPhotoMaxRatio = 20
)

// photoDimensions returns the width and height of a JPEG, PNG or GIF photo
// (0, 0 for a file it can't read).
func photoDimensions(path string) (int, int) {
	width := 0
	height := 0
	file, err := os.Open(path)

	if err == nil {
		defer file.Close()

		config, _, decodeErr := image.DecodeConfig(file)

		if decodeErr == nil {
			width = config.Width
			height = config.Height
		}
	}

	return width, height
}

// breaksSendPhotoDimensions applies sendPhoto's dimension rules
// (PHOTO_INVALID_DIMENSIONS).
func breaksSendPhotoDimensions(width, height int) bool {
	isTooNarrow := breaksSendPhotoRatio(width, height)
	isSumTooBig := breaksSendPhotoSideSum(width, height)
	result := (isSumTooBig == true) || (isTooNarrow == true)

	return result
}

// breaksSendPhotoRatio reports a long side more than telegramPhotoMaxRatio
// times the short one.
func breaksSendPhotoRatio(width, height int) bool {
	longSide := max(width, height)
	shortSide := max(min(width, height), 1)
	result := (longSide > (shortSide * telegramPhotoMaxRatio))

	return result
}

// breaksSendPhotoSideSum reports width + height above sendPhoto's limit.
func breaksSendPhotoSideSum(width, height int) bool {
	result := ((width + height) > telegramPhotoMaxSideSum)

	return result
}

// shrinkPhoto writes a JPEG of the photo next to it and returns its path: a
// photo narrower than sendPhoto's ratio limit is padded with white to it (in
// RGB, so odd sizes of a 4:2:0 JPEG aren't rounded below the input), then
// scaled to at most photoShrinkMaxSide px on its longest side (so
// width + height fits too). The caller checks its size.
func shrinkPhoto(photoPath string) (string, error) {
	var output []byte
	var err error
	result := ""
	ffmpegPath := findFFmpegPath()
	jpegPath := fmt.Sprintf("%s%s", photoPath, photoShrinkSuffix)
	pad := fmt.Sprintf("format=rgb24,pad=w='max(iw,ceil(ih/%d))':h='max(ih,ceil(iw/%d))':x='(ow-iw)/2':y='(oh-ih)/2':color=white", telegramPhotoMaxRatio, telegramPhotoMaxRatio)
	scale := fmt.Sprintf("%s,scale=w=%d:h=%d:force_original_aspect_ratio=decrease", pad, photoShrinkMaxSide, photoShrinkMaxSide)

	if ffmpegPath == "" {
		err = fmt.Errorf("ffmpeg not available")
	}

	if err == nil {
		baseCtx := context.Background()
		ctx, cancel := context.WithTimeout(baseCtx, gifConversionTimeout)
		defer cancel()

		args := []string{"-i", photoPath, "-vf", scale, "-q:v", photoShrinkQuality, "-frames:v", "1", "-y", jpegPath}
		cmd := exec.CommandContext(ctx, ffmpegPath, args...)
		output, err = cmd.CombinedOutput()
	}

	if err != nil {
		os.Remove(jpegPath)
		trimmed := strings.TrimSpace(string(output))
		err = fmt.Errorf("ffmpeg photo shrink failed: %v (%s)", err, trimmed)
	}

	if err == nil {
		result = jpegPath
	}

	return result, err
}
