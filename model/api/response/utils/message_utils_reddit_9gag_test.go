package utils

import (
	"slices"
	"testing"

	"github.com/timoffmax/social-link-bot/model/api/response"
)

func TestGetOriginalRedditLinksTurnsShortLinksIntoPostLinks(t *testing.T) {
	text := "a https://redd.it/1wzcjw8 b https://www.reddit.com/r/aww/comments/1wpz4e4/x/ c https://i.redd.it/7nk8fbfkjwth1.jpeg d https://v.redd.it/axrwmetm5quh1 e https://m.reddit.com/r/pics/comments/1wmc84j/y/ f https://www.reddit.com/comments/1x2t6a6 g https://www.reddit.com/user/someone"
	want := []string{
		"https://www.reddit.com/comments/1wzcjw8",
		"https://www.reddit.com/r/aww/comments/1wpz4e4/x/",
		"https://m.reddit.com/r/pics/comments/1wmc84j/y/",
		"https://www.reddit.com/comments/1x2t6a6",
	}

	links := GetOriginalRedditLinks(&response.TgMessage{Text: text})

	if slices.Equal(links, want) == false {
		t.Fatalf("got %v, want %v", links, want)
	}

	if slices.Equal(GetRedditLinks(&response.TgMessage{Text: text}), want) == false {
		t.Fatalf("GetRedditLinks differs from GetOriginalRedditLinks")
	}
}

func TestGetOriginal9GAGLinksAcceptsTheMobileHost(t *testing.T) {
	text := "a https://m.9gag.com/gag/aryPZ7d b http://www.9gag.com/gag/axyp6p1 c https://9gag.com/gag/a7ojx5L?utm_source=copy_link d https://9gag.com/u/someone"
	want := []string{
		"https://9gag.com/gag/aryPZ7d",
		"http://www.9gag.com/gag/axyp6p1",
		"https://9gag.com/gag/a7ojx5L?utm_source=copy_link",
	}

	links := GetOriginal9GAGLinks(&response.TgMessage{Text: text})

	if slices.Equal(links, want) == false {
		t.Fatalf("got %v, want %v", links, want)
	}
}
