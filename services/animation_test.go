package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)

	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

func TestPrepareAnimationsTypesGIFPhotosByTheirContent(t *testing.T) {
	gifNamedJPG := writeTestFile(t, "reddit_gallery_1.jpg", []byte("GIF89a....."))
	jpeg := writeTestFile(t, "photo.jpg", []byte("\xff\xd8\xff\xe0JFIF"))
	gifVideo := writeTestFile(t, "video.mp4", []byte("GIF89a....."))
	items := []*MediaInfo{
		{Type: MediaTypePhoto, Filename: gifNamedJPG},
		{Type: MediaTypePhoto, Filename: jpeg},
		{Type: MediaTypeVideo, Filename: gifVideo},
	}

	prepared := PrepareAnimations(items)

	if (len(prepared) != 3) || (items[0].Type != MediaTypeAnimation) || (items[1].Type != MediaTypePhoto) || (items[2].Type != MediaTypeVideo) {
		t.Errorf("%d items, types %s, %s, %s", len(prepared), items[0].Type, items[1].Type, items[2].Type)
	}

	if (filepath.Base(items[0].Filename) != "reddit_gallery_1.gif") || (IsGIFFile(items[0].Filename) == false) {
		t.Errorf("want the GIF named .gif, got %s", items[0].Filename)
	}

	if _, err := os.Stat(gifNamedJPG); os.IsNotExist(err) == false {
		t.Errorf("the .jpg name must be gone: %v", err)
	}

	if (items[1].Filename != jpeg) || (items[2].Filename != gifVideo) {
		t.Errorf("other files renamed: %s %s", items[1].Filename, items[2].Filename)
	}
}

func TestIsGIFFileReadsTheSignature(t *testing.T) {
	cases := map[string]bool{"GIF87a": true, "GIF89a and more": true, "GIF8": false, "PNG...": false, "": false}

	for content, want := range cases {
		path := writeTestFile(t, "file", []byte(content))

		if got := IsGIFFile(path); got != want {
			t.Errorf("%q: got %t", content, got)
		}
	}

	if IsGIFFile(filepath.Join(t.TempDir(), "missing.gif")) == true {
		t.Error("a missing file is no GIF")
	}
}

func makeTestGIF(t *testing.T, name string) string {
	t.Helper()

	ffmpegPath := findFFmpegPath()

	if ffmpegPath == "" {
		t.Skip("ffmpeg not installed")
	}

	gifPath := filepath.Join(t.TempDir(), name)
	makeGIF := exec.Command(ffmpegPath, "-f", "lavfi", "-i", "testsrc=size=640x360:rate=10:duration=2", "-y", gifPath)

	if output, err := makeGIF.CombinedOutput(); err != nil {
		t.Fatalf("make gif: %v %s", err, output)
	}

	return gifPath
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)

	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	return info.Size()
}

func TestFitAnimationConvertsAGIFAboveTheLimitToMP4(t *testing.T) {
	gifPath := makeTestGIF(t, "anim.gif")
	item := &MediaInfo{Type: MediaTypeAnimation, Filename: gifPath}
	limit := (fileSize(t, gifPath) - 1)

	err := fitAnimation(item, limit)

	_, gifErr := os.Stat(gifPath)

	if (err != nil) || (filepath.Base(item.Filename) != "anim_gif.mp4") || (IsGIFFile(item.Filename) == true) || (fileSize(t, item.Filename) > limit) {
		t.Errorf("converted file %s: %v", item.Filename, err)
	}

	if (item.Type != MediaTypeAnimation) || (os.IsNotExist(gifErr) == false) {
		t.Errorf("want an MP4 animation in place of the GIF, got %s, GIF stat %v", item.Type, gifErr)
	}
}

func TestFitAnimationEncodesSmallerWhenTheFirstMP4IsTooBig(t *testing.T) {
	gifPath := makeTestGIF(t, "anim.gif")
	firstPath, firstErr := convertGIFToMP4(gifPath, gifEncodings[0])
	firstSize := fileSize(t, firstPath)
	smallPath, smallErr := convertGIFToMP4(gifPath, gifEncodings[1])
	smallSize := fileSize(t, smallPath)
	os.Remove(smallPath)

	if (firstErr != nil) || (smallErr != nil) || (smallSize >= firstSize) {
		t.Fatalf("want a smaller second encoding: %d (%v) then %d (%v)", firstSize, firstErr, smallSize, smallErr)
	}

	item := &MediaInfo{Type: MediaTypeAnimation, Filename: gifPath}

	err := fitAnimation(item, smallSize)

	if (err != nil) || (fileSize(t, item.Filename) > smallSize) || (filepath.Base(item.Filename) != "anim_gif.mp4") {
		t.Errorf("want the smaller MP4, got %s %v", item.Filename, err)
	}
}

func TestPrepareAnimationsLeavesOutAGIFNoMP4Fits(t *testing.T) {
	gifPath := makeTestGIF(t, "anim.gif")
	previous := animationUploadLimit
	animationUploadLimit = 1
	t.Cleanup(func() {
		animationUploadLimit = previous
	})
	photo := writeTestFile(t, "photo.jpg", []byte("\xff\xd8\xff\xe0JFIF"))
	items := []*MediaInfo{{Type: MediaTypePhoto, Filename: gifPath}, {Type: MediaTypePhoto, Filename: photo}}

	prepared := PrepareAnimations(items)

	if (len(prepared) != 1) || (prepared[0].Filename != photo) {
		t.Fatalf("want only the photo, got %+v", prepared)
	}

	entries, _ := os.ReadDir(filepath.Dir(gifPath))

	if len(entries) != 0 {
		t.Errorf("want the GIF and its MP4s removed, got %v", entries)
	}
}

func TestFitAnimationKeepsAGIFWithinTheLimit(t *testing.T) {
	gifPath := writeTestFile(t, "small.gif", []byte("GIF89a....."))
	item := &MediaInfo{Type: MediaTypeAnimation, Filename: gifPath}

	if err := fitAnimation(item, TelegramUploadLimitBytes); (err != nil) || (item.Filename != gifPath) {
		t.Errorf("file changed to %s %v", item.Filename, err)
	}
}
