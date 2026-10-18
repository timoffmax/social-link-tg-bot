package services

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireFFmpeg(t *testing.T) string {
	t.Helper()

	ffmpegPath := findFFmpegPath()

	if ffmpegPath == "" {
		t.Skip("ffmpeg not installed")
	}

	return ffmpegPath
}

func TestPreparePhotosSendsAPhotoTooNarrowForSendPhotoAsADocument(t *testing.T) {
	shapes := [][2]int{{120, 3000}, {301, 9001}, {3000, 120}}

	for _, shape := range shapes {
		path := filepath.Join(t.TempDir(), "strip.png")
		writeTestPNG(t, path, shape[0], shape[1])
		item := &MediaInfo{Type: MediaTypePhoto, Filename: path}

		PreparePhotos([]*MediaInfo{item})

		if (item.Type != MediaTypeDocument) || (item.Filename != path) {
			t.Errorf("%v: want the full-size file as a document, got %s %s", shape, item.Type, item.Filename)
		}
	}
}

func TestPreparePhotosShrinksAPhotoAboveTheSideSum(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "tall.png")
	writeTestPNG(t, path, 1100, 9500)
	item := &MediaInfo{Type: MediaTypePhoto, Filename: path}

	PreparePhotos([]*MediaInfo{item})

	width, height := photoDimensions(item.Filename)

	if (item.Type != MediaTypePhoto) || (item.Filename != path+photoShrinkSuffix) || (breaksSendPhotoDimensions(width, height) == true) {
		t.Errorf("want a shrunk photo, got %s %s %dx%d", item.Type, item.Filename, width, height)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) == false {
		t.Errorf("the original must be removed: %v", err)
	}
}

func TestFitPhotoShrinksAPhotoAboveTheLimit(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)
	pngPath := filepath.Join(t.TempDir(), "reddit_vx_1_1.png")
	makePNG := exec.Command(ffmpegPath, "-f", "lavfi", "-i", "testsrc=size=3000x1500:rate=1:duration=1", "-vf", "noise=alls=60:allf=t", "-frames:v", "1", "-y", pngPath)

	if output, err := makePNG.CombinedOutput(); err != nil {
		t.Fatalf("make png: %v %s", err, output)
	}

	limit := (fileSize(t, pngPath) - 1)
	item := &MediaInfo{Type: MediaTypePhoto, Filename: pngPath}

	fitPhoto(item, limit)

	if (item.Type != MediaTypePhoto) || (item.Filename != pngPath+photoShrinkSuffix) {
		t.Fatalf("want the shrunk JPEG, got %s %s", item.Type, item.Filename)
	}

	file, err := os.Open(item.Filename)

	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	config, _, decodeErr := image.DecodeConfig(file)

	if (decodeErr != nil) || (config.Width != 2560) || (config.Height != 1280) {
		t.Errorf("want 2560x1280, got %dx%d %v", config.Width, config.Height, decodeErr)
	}
}

func TestFitPhotoSendsAPhotoThatCantBeShrunkEnoughAsADocument(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, path, 800, 600)
	item := &MediaInfo{Type: MediaTypePhoto, Filename: path}

	fitPhoto(item, 1)

	if (item.Type != MediaTypeDocument) || (item.Filename != path) {
		t.Errorf("want the original as a document, got %s %s", item.Type, item.Filename)
	}

	entries, _ := os.ReadDir(filepath.Dir(path))

	if len(entries) != 1 {
		t.Errorf("want no shrunk file left, got %v", entries)
	}
}

func TestPreparePhotosKeepsAPhotoWithinTheRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	writeTestPNG(t, path, 800, 600)
	unreadable := writeTestFile(t, "photo.jpg", []byte("jpeg"))
	video := filepath.Join(t.TempDir(), "strip.mp4")
	writeTestPNG(t, video, 120, 3000)
	items := []*MediaInfo{{Type: MediaTypePhoto, Filename: path}, {Type: MediaTypePhoto, Filename: unreadable}, {Type: MediaTypeVideo, Filename: video}}

	PreparePhotos(items)

	for i, want := range []string{MediaTypePhoto, MediaTypePhoto, MediaTypeVideo} {
		if items[i].Type != want {
			t.Errorf("item %d: got %s, want %s", i, items[i].Type, want)
		}
	}

	if (items[0].Filename != path) || (items[1].Filename != unreadable) {
		t.Errorf("files changed: %s %s", items[0].Filename, items[1].Filename)
	}
}

func TestDetectMediaContentReadsTheFile(t *testing.T) {
	cases := map[string]MediaContent{
		"GIF89a.....":                                      {Type: MediaTypeAnimation, Extension: ".gif"},
		"\xff\xd8\xff\xe0\x00\x10JFIF":                     {Type: MediaTypePhoto, Extension: ".jpg"},
		"\x89PNG\r\n\x1a\n....":                            {Type: MediaTypePhoto, Extension: ".png"},
		"\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom": {Type: MediaTypeVideo, Extension: ".mp4"},
		"plain text":                                       {Type: MediaTypeDocument, Extension: ".bin"},
	}

	for content, want := range cases {
		path := writeTestFile(t, "file.BIN", []byte(content))

		if got := DetectMediaContent(path); got != want {
			t.Errorf("%q: got %+v, want %+v", content, got, want)
		}
	}

	missing := filepath.Join(t.TempDir(), "missing.jpg")

	if got := DetectMediaContent(missing); got.Type != MediaTypeDocument {
		t.Errorf("a missing file: got %+v", got)
	}
}

func TestNameFileByContentRenamesOnlyAMisnamedFile(t *testing.T) {
	cases := map[string]string{
		"1_ABC_9gag.jpg": "1_ABC_9gag.gif",
		"reddit_vx_1_1":  "reddit_vx_1_1.gif",
		"title v1.5_id":  "title v1.5_id.gif",
		"already.gif":    "already.gif",
		"UPPER.GIF":      "UPPER.GIF",
	}

	for name, want := range cases {
		path := writeTestFile(t, name, []byte("GIF89a....."))
		item := &MediaInfo{Type: MediaTypeAnimation, Filename: path}

		content := NameFileByContent(item)

		if (filepath.Base(item.Filename) != want) || (content.Type != MediaTypeAnimation) {
			t.Errorf("%s: got %s %+v, want %s", name, item.Filename, content, want)
		}

		if _, err := os.Stat(item.Filename); err != nil {
			t.Errorf("%s: file gone: %v", name, err)
		}
	}

	jpegPath := writeTestFile(t, "photo.jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF"))
	item := &MediaInfo{Type: MediaTypePhoto, Filename: jpegPath}
	NameFileByContent(item)

	if strings.HasSuffix(item.Filename, "photo.jpeg") == false {
		t.Errorf("a .jpeg JPEG must keep its name, got %s", item.Filename)
	}
}
