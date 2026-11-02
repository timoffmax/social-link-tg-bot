package services

import (
	"net/http"
	"strings"
	"testing"
)

// gateAgeLimitedBody is the oEmbed answer Instagram gave for an author-gated post.
const gateAgeLimitedBody = `{"message":"geoblock_required","gating_type":"unappealable","title":"People under 15 can't see this content","description":"This account has set limits on who can see their profile and content.","buttons":[],"show_notice":false,"session_id":"","blocks_logging_data":"MIN_AGE_ACCOUNT","geo_block_rule_type":null,"gating_type_thrift":12,"media_igid":4006146074141897089,"status":"fail"}`

func TestParseInstagramGateDetectsAgeLimit(t *testing.T) {
	gate, err := parseInstagramGate(http.StatusBadRequest, []byte(gateAgeLimitedBody))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (gate.Restricted == false) || (gate.AgeLimited == false) {
		t.Fatalf("expected an age-limited gate, got %+v", gate)
	}

	if gate.Title != "People under 15 can't see this content" {
		t.Fatalf("unexpected title: %q", gate.Title)
	}
}

func TestParseInstagramGateDetectsOtherGate(t *testing.T) {
	body := `{"gating_type":"sensitive","title":"Sensitive content","blocks_logging_data":"OTHER"}`
	gate, err := parseInstagramGate(http.StatusBadRequest, []byte(body))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (gate.Restricted == false) || (gate.AgeLimited == true) {
		t.Fatalf("expected a non-age gate, got %+v", gate)
	}
}

func TestParseInstagramGateTreatsPublicPostAsUngated(t *testing.T) {
	gate, err := parseInstagramGate(http.StatusOK, []byte(`{"version":"1.0","author_name":"someone"}`))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gate.Restricted == true {
		t.Fatalf("expected no gate, got %+v", gate)
	}
}

func TestParseInstagramGateTreatsErrorWithoutGatingTypeAsUngated(t *testing.T) {
	gate, err := parseInstagramGate(http.StatusBadRequest, []byte(`{"message":"bad request","status":"fail"}`))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gate.Restricted == true {
		t.Fatalf("expected no gate, got %+v", gate)
	}
}

func TestParseInstagramGateRejectsMissingPost(t *testing.T) {
	gate, err := parseInstagramGate(http.StatusNotFound, []byte("No Media Match"))

	if err == nil {
		t.Fatalf("expected an error for a 404 answer")
	}

	if gate.Restricted == true {
		t.Fatalf("expected no gate, got %+v", gate)
	}
}

func TestParseInstagramGateRejectsNonJSONGateAnswer(t *testing.T) {
	_, err := parseInstagramGate(http.StatusBadRequest, []byte("<html>"))

	if err == nil {
		t.Fatalf("expected a decode error")
	}
}

func TestCanonicalInstagramPostURLNormalizesProxyAndReelsURLs(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/p/DeYsKEDjbGB/?obrf=NTFzM2t5M2VsN2E1": "https://www.instagram.com/p/DeYsKEDjbGB/",
		"https://toinstagram.com/reels/DecGn1EIg9y/":                     "https://www.instagram.com/reel/DecGn1EIg9y/",
		"https://www.instagram.com/someuser/p/DecCSqHtNNk/":              "https://www.instagram.com/p/DecCSqHtNNk/",
	}

	for input, expected := range cases {
		actual, err := canonicalInstagramPostURL(input)

		if (err != nil) || (actual != expected) {
			t.Fatalf("%s: expected %s, got %s (err %v)", input, expected, actual, err)
		}
	}
}

func TestCanonicalInstagramPostURLRejectsStoryURL(t *testing.T) {
	_, err := canonicalInstagramPostURL("https://www.instagram.com/stories/someuser/123/")

	if err == nil {
		t.Fatalf("expected an error for a story URL")
	}
}

func TestInstagramGateMessageUsesAgeEmojiAndTitle(t *testing.T) {
	gate := InstagramGate{Restricted: true, AgeLimited: true, Title: "People under 15 can't see this content"}
	text := InstagramGateMessage(gate)

	if (strings.HasPrefix(text, "🔞 ") == false) || (strings.Contains(text, gate.Title) == false) {
		t.Fatalf("unexpected message: %q", text)
	}
}

func TestInstagramGateMessageFallsBackToDefaultTitle(t *testing.T) {
	text := InstagramGateMessage(InstagramGate{Restricted: true})

	if (strings.HasPrefix(text, "🔒 ") == false) || (strings.Contains(text, instagramGateDefaultTitle) == false) {
		t.Fatalf("unexpected message: %q", text)
	}
}

func TestParseInstagramGateDetectsAudienceLimit(t *testing.T) {
	body := `{"message":"geoblock_required","gating_type":"unappealable","title":"This content isn't available to everyone","blocks_logging_data":"","gating_type_thrift":3}`
	gate, err := parseInstagramGate(http.StatusBadRequest, []byte(body))

	if (err != nil) || (gate.AudienceLimited == false) || (gate.AgeLimited == true) || (gate.NeedsSpoiler() == true) {
		t.Fatalf("expected an unblurred audience gate, got %+v %v", gate, err)
	}
}

func TestUnknownGatesAreBlurredByDefault(t *testing.T) {
	body := `{"gating_type":"sensitive","title":"Sensitive content","blocks_logging_data":"OTHER","gating_type_thrift":7}`
	gate, _ := parseInstagramGate(http.StatusBadRequest, []byte(body))
	ageGate, _ := parseInstagramGate(http.StatusBadRequest, []byte(gateAgeLimitedBody))

	if (gate.NeedsSpoiler() == false) || (ageGate.NeedsSpoiler() == false) {
		t.Fatalf("unknown and age gates must be blurred: %+v %+v", gate, ageGate)
	}
}

func TestInstagramMediaCDNCheck(t *testing.T) {
	media := "https://instagram.fsjc1-4.fna.fbcdn.net/v/t51.82787-15/x.jpg"
	cdn := "https://scontent-waw2-1.cdninstagram.com/v/t51/x.jpg"
	logo := "https://static.cdninstagram.com/rsrc.php/v4/yG/r/logo.png"

	if (isInstagramMediaCDNURL(media) == false) || (isInstagramMediaCDNURL(cdn) == false) {
		t.Fatalf("CDN media must pass")
	}

	lookalike := isInstagramMediaCDNURL("https://evilfbcdn.net/v/x.jpg") || isInstagramMediaCDNURL("https://notcdninstagram.com/o1/x")

	if (isInstagramMediaCDNURL("https://www.instagram.com/static/images/ico.png") == true) || (isInstagramMediaCDNURL(logo) == true) || (lookalike == true) {
		t.Fatalf("non-media images must fail")
	}
}

func TestAudienceGateAcceptsThriftAsString(t *testing.T) {
	body := `{"gating_type":"unappealable","title":"x","blocks_logging_data":"","gating_type_thrift":"3"}`
	gate, err := parseInstagramGate(http.StatusBadRequest, []byte(body))

	if (err != nil) || (gate.AudienceLimited == false) {
		t.Fatalf("expected an audience gate from a string thrift, got %+v %v", gate, err)
	}
}

// oembedPostBody is a trimmed oEmbed answer for an ungated photo post.
const oembedPostBody = `{"version":"1.0","title":"Line one & <two>\n\nSecond paragraph","author_name":"some.author_1","author_url":"https://www.instagram.com/some.author_1","provider_name":"Instagram","type":"rich"}`

func TestParseInstagramOEmbedReadsAuthorAndCaption(t *testing.T) {
	post, err := parseInstagramOEmbed(http.StatusOK, []byte(oembedPostBody))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (post.Gate.Restricted == true) || (post.AuthorName != "some.author_1") {
		t.Fatalf("unexpected post: %+v", post)
	}

	if post.Caption != "Line one & <two>\n\nSecond paragraph" {
		t.Fatalf("unexpected caption: %q", post.Caption)
	}
}

func TestParseInstagramOEmbedKeepsGateWithoutCaption(t *testing.T) {
	post, err := parseInstagramOEmbed(http.StatusBadRequest, []byte(gateAgeLimitedBody))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if (post.Gate.AgeLimited == false) || (post.Caption != "") || (post.AuthorName != "") {
		t.Fatalf("expected an age gate without caption, got %+v", post)
	}
}

func TestParseInstagramOEmbedRejectsBrokenAnswer(t *testing.T) {
	_, err := parseInstagramOEmbed(http.StatusOK, []byte("<html>"))

	if err == nil {
		t.Fatal("expected an error for a non-JSON answer")
	}
}

func TestInstagramPostCaptionHasAuthorAndEscapedText(t *testing.T) {
	post := InstagramPost{AuthorName: "some.author_1", Caption: "Line one & <two>\n\nSecond paragraph"}
	caption := instagramPostCaption(post, "https://www.instagram.com/p/ABC123/")

	if strings.Contains(caption, `instagram.com/some.author_1`) == false {
		t.Fatalf("expected the author's profile link, got %q", caption)
	}

	if strings.Contains(caption, "Line one &amp; &lt;two&gt;\n\nSecond paragraph") == false {
		t.Fatalf("expected the escaped caption text, got %q", caption)
	}
}

func TestInstagramPostCaptionDropsUnsafeAuthorName(t *testing.T) {
	post := InstagramPost{AuthorName: `bad"name`, Caption: "Text"}
	caption := instagramPostCaption(post, "https://www.instagram.com/p/ABC123/")

	if caption != "Text" {
		t.Fatalf("expected only the text, got %q", caption)
	}
}

func TestInstagramPostCaptionIsEmptyWithoutAuthorAndText(t *testing.T) {
	caption := instagramPostCaption(InstagramPost{}, "https://www.instagram.com/p/ABC123/")

	if caption != "" {
		t.Fatalf("expected no caption, got %q", caption)
	}
}

func TestInstagramPostCaptionFitsTheCaptionLimit(t *testing.T) {
	postURL := "https://www.instagram.com/p/ABC123/"
	post := InstagramPost{AuthorName: "some.author_1", Caption: strings.Repeat("Дівчина & газ ", 300)}
	caption := instagramPostCaption(post, postURL)
	size := captionSize(caption, postURL)

	if size > TelegramCaptionLimitBytes {
		t.Fatalf("caption with link is %d bytes, over %d", size, TelegramCaptionLimitBytes)
	}

	if strings.HasPrefix(caption, "📷") == false {
		t.Fatalf("expected the author line to stay, got %q", caption)
	}
}

func TestOGPreviewCaptionPrefersOEmbedCaption(t *testing.T) {
	htmlBody := `<meta property="og:title" content="OG title" />`
	post := InstagramPost{AuthorName: "some.author_1", Caption: "oEmbed text"}
	caption := ogPreviewCaption(post, "https://www.instagram.com/p/ABC123/", htmlBody)

	if (strings.Contains(caption, "oEmbed text") == false) || (strings.Contains(caption, "OG title") == true) {
		t.Fatalf("expected the oEmbed caption, got %q", caption)
	}
}

func TestOGPreviewCaptionFallsBackToOGTitle(t *testing.T) {
	htmlBody := `<meta property="og:title" content="Tom &amp; Jerry" />`
	caption := ogPreviewCaption(InstagramPost{}, "https://www.instagram.com/p/ABC123/", htmlBody)

	if caption != "Tom &amp; Jerry" {
		t.Fatalf("expected the escaped og:title, got %q", caption)
	}
}

func TestInstagramPostCaptionKeepsMentionsFromLinkingToTelegram(t *testing.T) {
	post := InstagramPost{Caption: "@chef at @insight.gastrobar (@x)"}
	caption := instagramPostCaption(post, "https://www.instagram.com/p/ABC123/")

	if caption != "@\u2060chef at @\u2060insight.gastrobar (@\u2060x)" {
		t.Fatalf("expected word joiners after each mention @, got %q", caption)
	}
}

func TestInstagramPostCaptionLeavesEmailsAndURLsAlone(t *testing.T) {
	post := InstagramPost{Caption: "mail info@shop.com, josé@shop.com or https://site.com/@user"}
	caption := instagramPostCaption(post, "https://www.instagram.com/p/ABC123/")

	if caption != "mail info@shop.com, josé@shop.com or https://site.com/@user" {
		t.Fatalf("expected the text unchanged, got %q", caption)
	}
}

func TestInstagramPostCaptionLinksNoMentionAfterAFullStop(t *testing.T) {
	caption := instagramPostCaption(InstagramPost{Caption: "Thanks.@chef"}, "https://www.instagram.com/p/ABC123/")

	if caption != "Thanks.@\u2060chef" {
		t.Fatalf("expected a word joiner after the @, got %q", caption)
	}
}

func TestBlursMediaOnlyForAGateOtherThanTheAudienceLimit(t *testing.T) {
	cases := map[string]struct {
		gate InstagramGate
		want bool
	}{
		"ungated":        {gate: InstagramGate{}, want: false},
		"age limit":      {gate: InstagramGate{Restricted: true, AgeLimited: true}, want: true},
		"other gate":     {gate: InstagramGate{Restricted: true}, want: true},
		"audience limit": {gate: InstagramGate{Restricted: true, AudienceLimited: true}, want: false},
	}

	for name, testCase := range cases {
		if got := testCase.gate.BlursMedia(); got != testCase.want {
			t.Errorf("%s: got %t, want %t", name, got, testCase.want)
		}
	}
}
