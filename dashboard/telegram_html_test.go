package dashboard

import (
	"strings"
	"testing"
)

func TestCheckTelegramHTMLAcceptsTheSupportedSubset(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		length int
	}{
		{"plain text", "Hello", 5},
		{"empty", "", 0},
		{"newlines count", "a\nb", 3},
		{"bold and strong", "<b>a</b><strong>b</strong>", 2},
		{"italic and em", "<i>a</i><em>b</em>", 2},
		{"underline and ins", "<u>a</u><ins>b</ins>", 2},
		{"strike variants", "<s>a</s><strike>b</strike><del>c</del>", 3},
		{"spoiler tag", "<tg-spoiler>secret</tg-spoiler>", 6},
		{"spoiler span", `<span class="tg-spoiler">x</span>`, 1},
		{"spoiler span single quotes", "<span class='tg-spoiler'>x</span>", 1},
		{"spoiler span unquoted", "<span class=tg-spoiler>x</span>", 1},
		{"link", `<a href="https://example.com/?a=1&amp;b=2">site</a>`, 4},
		{"link with empty href", `<a href="">site</a>`, 4},
		{"link with gt in href", `<a href="https://e.com/>">x</a>`, 1},
		{"code", "<code>x := 1</code>", 6},
		{"code with language", `<pre><code class="language-go">x</code></pre>`, 1},
		{"code with empty language", `<code class="language-">x</code>`, 1},
		{"upper-case tag", "<B>x</B>", 1},
		{"upper-case link and attribute", `<A HREF="https://example.com">x</a>`, 1},
		{"pre", "<pre>a\nb</pre>", 3},
		{"blockquote", "<blockquote>q</blockquote>", 1},
		{"expandable blockquote", "<blockquote expandable>q</blockquote>", 1},
		{"custom emoji", `<tg-emoji emoji-id="5368324170671202286">👍</tg-emoji>`, 2},
		{"nested", "<b>bold <i>both <u>all</u></i></b>", 13},
		{"same tag nested", "<b><b>x</b></b>", 1},
		{"spaces in tags", "<b >x</b >", 1},
		{"named entities", "&lt;&gt;&amp;&quot;", 4},
		{"decimal reference", "&#65;", 1},
		{"hex reference", "&#x41;&#X42;", 2},
		{"emoji reference is two units", "&#x1F600;", 2},
		{"emoji is two units", "😀", 2},
		{"flag is four units", "🇺🇦", 4},
		{"cyrillic is one unit each", "Привіт", 6},
		{"family emoji with joiners", "👨‍👩‍👧", 8},
		{"zero-padded reference", "&#0000000000000065;", 1},
		{"entity in spoiler class", `<span class="tg&#45;spoiler">s</span>`, 1},
		{"entity in emoji id", `<tg-emoji emoji-id="&#49;2">👍</tg-emoji>`, 2},
		{"tags removed from length", `<b><a href="https://example.com/very/long">x</a></b>`, 1},
	}

	for _, tc := range cases {
		length, err := CheckTelegramHTML(tc.input)

		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}

		if length != tc.length {
			t.Errorf("%s: length = %d, want %d", tc.name, length, tc.length)
		}
	}
}

func TestCheckTelegramHTMLReportsTheFirstProblem(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		line   int
		column int
		text   string
	}{
		// Nesting.
		{"crossed tags", "<b><i>x</b></i>", 1, 8, "</b> closes <i> opened at line 1, column 4"},
		{"unclosed tag", "<b>x", 1, 1, "<b> is never closed"},
		{"innermost unclosed tag", "<b><i>x</i><u>y", 1, 12, "<u> is never closed"},
		{"closing without opening", "x</b>", 1, 2, "</b> closes no open tag"},
		{"closing after closed", "<b>x</b></b>", 1, 9, "</b> closes no open tag"},
		{"position on a later line", "line one\n<b>two\n</i>", 3, 1, "</i> closes <b> opened at line 2, column 1"},
		{"column counts characters", "Привіт <b", 1, 8, `<b> is not closed with ">"`},
		// Unknown tags.
		{"unknown tag", "<div>x</div>", 1, 1, "unsupported tag <div>"},
		{"upper-case unknown tag", "<DIV>x</DIV>", 1, 1, "unsupported tag <div>"},
		{"line break tag", "a<br>b", 1, 2, "unsupported tag <br>"},
		{"unknown closing tag", "</div>", 1, 1, "unsupported tag </div>"},
		{"self-closing", "<b/>", 1, 3, "self-closing tags are not supported"},
		{"comment", "<!-- x -->", 1, 1, `unescaped "<": write &lt;`},
		// Attributes.
		{"attribute on bold", `<b class="x">y</b>`, 1, 4, "attribute class is not allowed in <b>"},
		{"link title", `<a href="u" title="t">x</a>`, 1, 13, "attribute title is not allowed in <a>"},
		{"span other class", `<span class="red">x</span>`, 1, 7, `<span> accepts only class="tg-spoiler"`},
		{"span without class", "<span>x</span>", 1, 1, `<span> needs class="tg-spoiler"`},
		{"code other class", `<code class="go">x</code>`, 1, 7, `<code> accepts only class="language-..."`},
		{"pre attribute", `<pre lang="go">x</pre>`, 1, 6, "attribute lang is not allowed in <pre>"},
		{"emoji without id", "<tg-emoji>x</tg-emoji>", 1, 1, "<tg-emoji> needs emoji-id"},
		{"emoji non-numeric id", `<tg-emoji emoji-id="abc">x</tg-emoji>`, 1, 11, "emoji-id must be a number"},
		{"repeated attribute", `<a href="x" href="y">z</a>`, 1, 13, "attribute href is repeated in <a>"},
		{"unclosed attribute value", `<a href="x>y</a>`, 1, 9, "the value of attribute href is not closed"},
		{"attribute without value", `<a href=>x</a>`, 1, 9, "attribute href has no value"},
		{"invalid attribute", `<a "x">y</a>`, 1, 4, "invalid attribute in <a>"},
		{"no-break space before attribute", "<a\u00a0href=\"x\">y</a>", 1, 3, "invalid attribute in <a>"},
		{"entity decoded to another class", `<span class="&#116;g">x</span>`, 1, 7, `<span> accepts only class="tg-spoiler"`},
		{"tag cut off", `<a href="x"`, 1, 1, `<a> is not closed with ">"`},
		{"malformed closing tag", "<b>x</b", 1, 5, `malformed closing tag: write </name> or escape "<" as &lt;`},
		// Unescaped characters.
		{"unescaped less-than", "1 < 2", 1, 3, `unescaped "<": write &lt;`},
		{"less-than at the end", "x<", 1, 2, `unescaped "<": write &lt;`},
		{"unescaped greater-than", "2 > 1", 1, 3, `unescaped ">": write &gt;`},
		{"unescaped ampersand", "Tom & Jerry", 1, 5, `unescaped "&": write &amp;`},
		{"ampersand without semicolon", "&amp", 1, 1, `unescaped "&": write &amp;`},
		{"empty entity", "&;", 1, 1, `unescaped "&": write &amp;`},
		// Entities.
		{"unsupported named entity", "a&nbsp;b", 1, 2, "unsupported entity &nbsp;: Telegram knows only &lt; &gt; &amp; &quot; and numeric references"},
		{"apostrophe entity", "&apos;", 1, 1, "unsupported entity &apos;: Telegram knows only &lt; &gt; &amp; &quot; and numeric references"},
		{"zero reference", "&#0;", 1, 1, "invalid character reference &#0;"},
		{"surrogate reference", "&#xD800;", 1, 1, "invalid character reference &#xD800;"},
		{"reference out of range", "&#x110000;", 1, 1, "invalid character reference &#x110000;"},
		{"hex without digits", "&#x;", 1, 1, "invalid character reference &#x;"},
		{"bad decimal", "&#12a;", 1, 1, "invalid character reference &#12a;"},
		// Encoding.
		{"invalid utf-8", "ok\xff", 1, 3, "invalid UTF-8"},
	}

	for _, tc := range cases {
		length, err := CheckTelegramHTML(tc.input)
		htmlErr, isHTMLErr := err.(*HTMLError)

		if isHTMLErr == false {
			t.Errorf("%s: err = %v, want an *HTMLError", tc.name, err)
		}

		want := HTMLError{Line: tc.line, Column: tc.column, Message: tc.text}

		if (isHTMLErr == true) && (*htmlErr != want) {
			t.Errorf("%s:\n got  %+v\n want %+v", tc.name, *htmlErr, want)
		}

		if length != 0 {
			t.Errorf("%s: length = %d on error", tc.name, length)
		}
	}
}

func TestHTMLErrorMessageHasThePosition(t *testing.T) {
	_, err := CheckTelegramHTML("ab\n<x>")

	if (err == nil) || (err.Error() != "line 2, column 1: unsupported tag <x>") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckTelegramHTMLLengthAtTheLimit(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		length int
	}{
		{"ascii at the limit", strings.Repeat("a", TelegramMessageLimit), 4096},
		{"emoji at the limit", strings.Repeat("😀", TelegramMessageLimit/2), 4096},
		{"emoji over the limit", strings.Repeat("😀", TelegramMessageLimit/2) + "a", 4097},
		{"tags do not count", "<b>" + strings.Repeat("a", TelegramMessageLimit) + "</b>", 4096},
		{"entities count once", strings.Repeat("&amp;", TelegramMessageLimit), 4096},
	}

	for _, tc := range cases {
		length, err := CheckTelegramHTML(tc.input)

		if (err != nil) || (length != tc.length) {
			t.Errorf("%s: length = %d, err = %v, want %d", tc.name, length, err, tc.length)
		}
	}
}

func TestTelegramLengthFallsBackToTheRawLength(t *testing.T) {
	if telegramLength("<b>ok</b>") != 2 {
		t.Errorf("valid content: %d", telegramLength("<b>ok</b>"))
	}

	if telegramLength("1 < 2 😀") != 8 {
		t.Errorf("invalid content: %d", telegramLength("1 < 2 😀"))
	}
}
