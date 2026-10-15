package utils

import (
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

func TestGetThreadsLinksFindsPostLinks(t *testing.T) {
	text := "a https://www.threads.com/@zuck/post/Ddt7cL5EfUG?xmt=1 b https://threads.net/@a.b/post/XYZ123 c https://www.threads.com/t/DAELv-yOsQs d https://www.threads.com/@zuck e https://www.threads.com/share/BAV_IiebyI/"
	links := GetThreadsLinks(&response.TgMessage{Text: text})
	expected := []string{"https://www.threads.com/@zuck/post/Ddt7cL5EfUG?xmt=1", "https://threads.net/@a.b/post/XYZ123", "https://www.threads.com/t/DAELv-yOsQs", "https://www.threads.com/share/BAV_IiebyI/"}

	if len(links) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, links)
	}

	for i, link := range expected {
		if links[i] != link {
			t.Fatalf("expected %v, got %v", expected, links)
		}
	}
}
