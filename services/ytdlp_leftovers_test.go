package services

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestRemoveLeftoverFilesKeepsOnlyTheSentMedia(t *testing.T) {
	tempDir := t.TempDir()
	downloader := &YtDlpDownloader{TempDir: tempDir}
	outputTemplate := filepath.Join(tempDir, "1792000000000000000-7_ABCDEF12_%(title).100B_%(id)s.%(ext)s")
	names := []string{
		"1792000000000000000-7_ABCDEF12_Balkan friendship be like_axyp6p1.mp4",
		"1792000000000000000-7_ABCDEF12_Balkan friendship be like_axyp6p1.info.json",
		"1792000000000000000-7_ABCDEF12_Balkan friendship be like_axyp6p1.description",
		"1792000000000000000-7_ABCDEF12_Balkan friendship be like_axyp6p1.orig.jpg",
		"1792000000000000000-8_ABCDEF12_another download_x.info.json",
		"1792000000000000000-8_ABCDEF12_another download_x.mp4",
	}

	for _, name := range names {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("x"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	kept := []*MediaInfo{{Type: MediaTypeVideo, Filename: filepath.Join(tempDir, names[0])}}

	downloader.removeLeftoverFiles(outputTemplate, kept)

	entries, _ := os.ReadDir(tempDir)
	left := []string{}

	for _, entry := range entries {
		left = append(left, entry.Name())
	}

	want := []string{names[4], names[5], names[0]}
	slices.Sort(want)

	if slices.Equal(left, want) == false {
		t.Errorf("left %v, want %v", left, want)
	}
}

func TestRemoveLeftoverFilesClearsAFailedAttempt(t *testing.T) {
	tempDir := t.TempDir()
	downloader := &YtDlpDownloader{TempDir: tempDir}
	outputTemplate := filepath.Join(tempDir, "1792000000000000000-7_ABCDEF12_%(title).100B_%(id)s.%(ext)s")
	name := "1792000000000000000-7_ABCDEF12_I am the pillow now_aryPZ7d.info.json"

	if err := os.WriteFile(filepath.Join(tempDir, name), []byte("{}"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	downloader.removeLeftoverFiles(outputTemplate, nil)

	if entries, _ := os.ReadDir(tempDir); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestYtdlpDownloadPrefixNeedsTheTemplateShape(t *testing.T) {
	if got := ytdlpDownloadPrefix("/tmp/x/1792000000000000000-7_ABCDEF12_%(title).100B_%(id)s.%(ext)s"); got != "1792000000000000000-7_ABCDEF12_" {
		t.Errorf("got %q", got)
	}

	if got := ytdlpDownloadPrefix("/tmp/x/plainname"); got != "" {
		t.Errorf("got %q for a name without the prefix", got)
	}
}

func TestNewDownloadPrefixIsUniquePerCall(t *testing.T) {
	url := "https://9gag.com/gag/axyp6p1"
	first := newDownloadPrefix(url)
	second := newDownloadPrefix(url)
	downloader := &YtDlpDownloader{TempDir: "/tmp/x"}
	template := downloader.downloadOutputTemplate(first)

	if first == second {
		t.Errorf("two calls share the prefix %q", first)
	}

	if ytdlpDownloadPrefix(template) != first {
		t.Errorf("template %q doesn't give back the prefix %q", template, first)
	}

	if (strings.HasSuffix(first, "_") == false) || (strings.Count(first, "_") != 2) {
		t.Errorf("prefix %q has another shape", first)
	}
}

// fakeYtDlpScript writes the --output template's video and info.json after a
// pause, so two downloads overlap.
const fakeYtDlpScript = `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
	if [ "$1" = "--output" ]; then
		out="$2"
		shift
	fi
	shift
done
base=$(printf '%s' "$out" | sed -e 's/%(title)\.100B/clip/' -e 's/%(id)s/x1/')
sleep 0.3
printf 'video' > "$(printf '%s' "$base" | sed 's/%(ext)s/mp4/')"
printf '{"title":"clip"}' > "$(printf '%s' "$base" | sed 's/%(ext)s/info.json/')"
`

func TestConcurrentDownloadsOfTheSameURLKeepTheirOwnFiles(t *testing.T) {
	tempDir := t.TempDir()
	binaryPath := filepath.Join(t.TempDir(), "yt-dlp")

	if err := os.WriteFile(binaryPath, []byte(fakeYtDlpScript), 0700); err != nil {
		t.Fatalf("write fake yt-dlp: %v", err)
	}

	downloader := &YtDlpDownloader{TempDir: tempDir, BinaryPath: binaryPath}
	url := "https://example.com/video/1"
	results := make([][]*MediaInfo, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup

	for i := range results {
		wg.Add(1)

		go func(index int) {
			defer wg.Done()

			results[index], errs[index] = downloader.DownloadMedia(url)
		}(i)
	}

	wg.Wait()

	for i := range results {
		if (errs[i] != nil) || (len(results[i]) != 1) {
			t.Fatalf("download %d: %d items, %v", i, len(results[i]), errs[i])
		}

		if _, err := os.Stat(results[i][0].Filename); err != nil {
			t.Errorf("download %d lost its file: %v", i, err)
		}
	}

	if results[0][0].Filename == results[1][0].Filename {
		t.Errorf("both downloads got %s", results[0][0].Filename)
	}

	entries, _ := os.ReadDir(tempDir)

	if len(entries) != 2 {
		t.Errorf("want only the 2 videos left, got %v", entries)
	}
}
