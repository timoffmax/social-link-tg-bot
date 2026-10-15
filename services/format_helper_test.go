package services

import (
	"strings"
	"testing"
)

func TestFormatProfileMentionLinksDottedInstagramNames(t *testing.T) {
	mention := FormatProfileMention("instagram", "insight.gastrobar")

	if mention != `<a href="https://www.instagram.com/insight.gastrobar/">@insight.gastrobar</a>` {
		t.Fatalf("unexpected mention: %q", mention)
	}

	if FormatProfileMention("tiktok", "some.user") != `<a href="https://www.tiktok.com/@some.user">@some.user</a>` {
		t.Fatalf("unexpected TikTok mention: %q", FormatProfileMention("tiktok", "some.user"))
	}
}

func TestFormatProfileMentionKeepsTwitterAndUnsafeNamesPlain(t *testing.T) {
	if FormatProfileMention("twitter", "jack") != "@jack" {
		t.Fatalf("Twitter must stay plain: %q", FormatProfileMention("twitter", "jack"))
	}

	if FormatProfileMention("instagram", `bad"name`) != `@bad"name` {
		t.Fatalf("unsafe names must stay plain: %q", FormatProfileMention("instagram", `bad"name`))
	}
}

func TestFormatAuthorCaptionUsesTheProfileLink(t *testing.T) {
	caption := FormatAuthorCaption("instagram", "", "insight.gastrobar", "")

	if (strings.HasPrefix(caption, "📷 <a href=") == false) || (IsProfileMentionLine(caption) == false) {
		t.Fatalf("unexpected caption: %q", caption)
	}
}
