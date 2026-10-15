package utils

import (
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

func TestGetYouTubeLinksKeepsOnlyShortsAndPosts(t *testing.T) {
	text := "a https://www.youtube.com/shorts/aqz-KE-bpKQ b https://youtu.be/jNQXAC9IVRw c https://www.youtube.com/watch?v=jNQXAC9IVRw d https://www.youtube.com/post/UgkxABC e https://youtube.com/channel/UCx/community f https://m.youtube.com/shorts/xyz123"
	links := GetYouTubeLinks(&response.TgMessage{Text: text})
	expected := []string{"https://www.youtube.com/shorts/aqz-KE-bpKQ", "https://www.youtube.com/post/UgkxABC", "https://m.youtube.com/shorts/xyz123"}

	if len(links) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, links)
	}

	for i, link := range expected {
		if links[i] != link {
			t.Fatalf("expected %v, got %v", expected, links)
		}
	}
}
