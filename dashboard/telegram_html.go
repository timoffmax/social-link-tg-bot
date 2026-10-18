package dashboard

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TelegramMessageLimit is the most characters a Telegram message may have
// after its formatting is parsed, counted in UTF-16 code units.
const TelegramMessageLimit = 4096

// Telegram HTML tags and the attributes they accept.
const (
	tagAnchor           = "a"
	tagSpan             = "span"
	tagCode             = "code"
	tagBlockquote       = "blockquote"
	tagEmoji            = "tg-emoji"
	attrHref            = "href"
	attrClass           = "class"
	attrExpandable      = "expandable"
	attrEmojiID         = "emoji-id"
	spoilerClass        = "tg-spoiler"
	languageClassPrefix = "language-"
)

// Lengths of "<" and "</" before a tag name.
const (
	openingTagStartLen = len("<")
	closingTagStartLen = len("</")
)

// supportedTags are the tags Telegram's HTML parse mode understands.
var supportedTags = map[string]bool{
	"b":           true,
	"strong":      true,
	"i":           true,
	"em":          true,
	"u":           true,
	"ins":         true,
	"s":           true,
	"strike":      true,
	"del":         true,
	tagSpan:       true,
	"tg-spoiler":  true,
	tagAnchor:     true,
	tagCode:       true,
	"pre":         true,
	tagBlockquote: true,
	tagEmoji:      true,
}

// namedEntities are the only named entities Telegram decodes.
var namedEntities = map[string]rune{
	"lt":   '<',
	"gt":   '>',
	"amp":  '&',
	"quot": '"',
}

// Character reference syntax and bounds.
const (
	entityStart       = '&'
	entityEnd         = ';'
	entityNumberMark  = '#'
	entityDelimiters  = len("&;")
	hexPrefixLower    = "x"
	hexPrefixUpper    = "X"
	decimalBaseNumber = 10
	hexBase           = 16
	runeBits          = 32
	maxRune           = 0x10FFFF
	surrogateMin      = 0xD800
	surrogateMax      = 0xDFFF
	firstSupplemental = 0x10000
	supplementalUnits = 2
	basicUnits        = 1
)

// HTMLError is the first problem found in a Telegram HTML text, with its
// 1-based position (column in characters).
type HTMLError struct {
	Line    int
	Column  int
	Message string
}

// Error renders the position and the problem.
func (e *HTMLError) Error() string {
	result := fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Message)

	return result
}

// openTag is a tag waiting for its closing tag.
type openTag struct {
	name   string
	offset int
}

// htmlParser walks a text the way Telegram's HTML parse mode does.
type htmlParser struct {
	input  string
	pos    int
	length int
	open   []openTag
	err    *HTMLError
}

// CheckTelegramHTML checks text against Telegram's HTML parse mode and
// returns the length of the resulting message (tags removed, entities
// decoded) in UTF-16 code units, as Telegram counts it. On the first problem
// it returns 0 and an *HTMLError with the problem's position.
func CheckTelegramHTML(input string) (int, error) {
	parser := &htmlParser{input: input}
	parser.run()
	result := parser.length
	var err error

	if parser.err != nil {
		result = 0
		err = parser.err
	}

	return result, err
}

// telegramLength is the message length shown for stored content: the parsed
// length, or the raw UTF-16 length for content Telegram would reject.
func telegramLength(content string) int {
	result, err := CheckTelegramHTML(content)

	if err != nil {
		result = utf16Length(content)
	}

	return result
}

// utf16Length counts the UTF-16 code units of text.
func utf16Length(text string) int {
	result := 0

	for _, char := range text {
		result += utf16Units(char)
	}

	return result
}

// utf16Units is the number of UTF-16 code units of one character.
func utf16Units(char rune) int {
	result := basicUnits

	if char >= firstSupplemental {
		result = supplementalUnits
	}

	return result
}

// run parses the whole input, then checks that every tag was closed.
func (p *htmlParser) run() {
	for (p.err == nil) && (p.pos < len(p.input)) {
		p.step()
	}

	hasUnclosed := (p.err == nil) && (len(p.open) > 0)

	if hasUnclosed == true {
		last := p.open[len(p.open)-1]
		message := fmt.Sprintf("<%s> is never closed", last.name)
		p.fail(last.offset, message)
	}
}

// step consumes one tag, entity or character.
func (p *htmlParser) step() {
	switch p.input[p.pos] {
	case '<':
		p.parseTag()
	case entityStart:
		p.parseEntity()
	case '>':
		p.fail(p.pos, `unescaped ">": write &gt;`)
	default:
		p.parseChar()
	}
}

// parseChar counts one plain character.
func (p *htmlParser) parseChar() {
	char, size := utf8.DecodeRuneInString(p.input[p.pos:])
	isInvalid := (char == utf8.RuneError) && (size == 1)

	if isInvalid == true {
		p.fail(p.pos, "invalid UTF-8")
	}

	if isInvalid == false {
		p.length += utf16Units(char)
		p.pos += size
	}
}

// fail records the first problem at a byte offset.
func (p *htmlParser) fail(offset int, message string) {
	if p.err == nil {
		line, column := textPosition(p.input, offset)
		p.err = &HTMLError{Line: line, Column: column, Message: message}
	}
}

// textPosition converts a byte offset into a 1-based line and column.
func textPosition(input string, offset int) (int, int) {
	before := input[:offset]
	line := strings.Count(before, "\n") + 1
	lineStart := strings.LastIndex(before, "\n") + 1
	column := utf8.RuneCountInString(before[lineStart:]) + 1

	return line, column
}

// parseEntity decodes a character reference at an "&".
func (p *htmlParser) parseEntity() {
	start := p.pos
	name, isEntity := p.entityName()

	if isEntity == false {
		p.fail(start, `unescaped "&": write &amp;`)
	}

	if isEntity == true {
		p.decodeEntity(start, name)
	}
}

// entityName reads the name between "&" and ";"; false when there is no
// well-formed reference.
func (p *htmlParser) entityName() (string, bool) {
	end := p.pos + 1

	for (end < len(p.input)) && (isEntityNameByte(p.input[end]) == true) {
		end++
	}

	isEntity := (end < len(p.input)) && (p.input[end] == entityEnd) && (end > p.pos+1)
	result := ""

	if isEntity == true {
		result = p.input[p.pos+1 : end]
	}

	return result, isEntity
}

// isEntityNameByte reports a byte allowed inside a reference name.
func isEntityNameByte(value byte) bool {
	isLetter := ((value >= 'a') && (value <= 'z')) || ((value >= 'A') && (value <= 'Z'))
	isDigit := (value >= '0') && (value <= '9')
	result := (isLetter == true) || (isDigit == true) || (value == entityNumberMark)

	return result
}

// decodeEntity counts a named or numeric reference, or fails on an
// unsupported one.
func (p *htmlParser) decodeEntity(start int, name string) {
	char, isNamed := namedEntities[name]
	isNumeric := strings.HasPrefix(name, string(entityNumberMark))
	isValid := isNamed

	if isNumeric == true {
		char, isValid = numericEntity(name)
	}

	if (isValid == false) && (isNumeric == true) {
		message := fmt.Sprintf("invalid character reference &%s;", name)
		p.fail(start, message)
	}

	if (isValid == false) && (isNumeric == false) {
		message := fmt.Sprintf("unsupported entity &%s;: Telegram knows only &lt; &gt; &amp; &quot; and numeric references", name)
		p.fail(start, message)
	}

	if isValid == true {
		p.length += utf16Units(char)
		p.pos = start + len(name) + entityDelimiters
	}
}

// numericEntity decodes "#123" or "#x1F600" into a valid character.
func numericEntity(name string) (rune, bool) {
	digits := strings.TrimPrefix(name, string(entityNumberMark))
	base := decimalBaseNumber
	isHex := (strings.HasPrefix(digits, hexPrefixLower) == true) || (strings.HasPrefix(digits, hexPrefixUpper) == true)

	if isHex == true {
		digits = digits[1:]
		base = hexBase
	}

	value, err := strconv.ParseUint(digits, base, runeBits)
	isSurrogate := (value >= surrogateMin) && (value <= surrogateMax)
	isValid := (err == nil) && (value > 0) && (value <= maxRune) && (isSurrogate == false)
	result := rune(value)

	return result, isValid
}

// parseTag handles an opening or closing tag at a "<".
func (p *htmlParser) parseTag() {
	start := p.pos
	isClosing := (start+1 < len(p.input)) && (p.input[start+1] == '/')
	isOpening := (start+1 < len(p.input)) && (isTagNameStart(p.input[start+1]) == true)

	switch {
	case isClosing:
		p.parseClosingTag(start)
	case isOpening:
		p.parseOpeningTag(start)
	default:
		p.fail(start, `unescaped "<": write &lt;`)
	}
}

// isTagNameStart reports an ASCII letter.
func isTagNameStart(value byte) bool {
	result := ((value >= 'a') && (value <= 'z')) || ((value >= 'A') && (value <= 'Z'))

	return result
}

// isNameByte reports a byte allowed in a tag or attribute name.
func isNameByte(value byte) bool {
	isDigit := (value >= '0') && (value <= '9')
	result := (isTagNameStart(value) == true) || (isDigit == true) || (value == '-') || (value == '_')

	return result
}

// isSpaceByte reports HTML whitespace.
func isSpaceByte(value byte) bool {
	result := (value == ' ') || (value == '\t') || (value == '\n') || (value == '\r') || (value == '\f')

	return result
}

// readName reads a tag or attribute name from offset, lower-cased because
// Telegram matches names case-insensitively.
func (p *htmlParser) readName(offset int) (string, int) {
	end := offset

	for (end < len(p.input)) && (isNameByte(p.input[end]) == true) {
		end++
	}

	result := strings.ToLower(p.input[offset:end])

	return result, end
}

// skipSpaces returns the offset of the next non-space byte.
func (p *htmlParser) skipSpaces(offset int) int {
	result := offset

	for (result < len(p.input)) && (isSpaceByte(p.input[result]) == true) {
		result++
	}

	return result
}

// parseClosingTag pops the matching open tag.
func (p *htmlParser) parseClosingTag(start int) {
	name, end := p.readName(start + closingTagStartLen)
	end = p.skipSpaces(end)
	isWellFormed := (name != "") && (end < len(p.input)) && (p.input[end] == '>')

	switch {
	case isWellFormed == false:
		p.fail(start, `malformed closing tag: write </name> or escape "<" as &lt;`)
	case supportedTags[name] == false:
		message := fmt.Sprintf("unsupported tag </%s>", name)
		p.fail(start, message)
	case len(p.open) == 0:
		message := fmt.Sprintf("</%s> closes no open tag", name)
		p.fail(start, message)
	default:
		p.closeTag(start, name, end+1)
	}
}

// closeTag checks the nesting and moves past the closing tag.
func (p *htmlParser) closeTag(start int, name string, next int) {
	last := p.open[len(p.open)-1]

	if last.name != name {
		line, column := textPosition(p.input, last.offset)
		message := fmt.Sprintf("</%s> closes <%s> opened at line %d, column %d", name, last.name, line, column)
		p.fail(start, message)
	}

	if last.name == name {
		p.open = p.open[:len(p.open)-1]
		p.pos = next
	}
}

// parseOpeningTag checks the tag and its attributes and pushes it.
func (p *htmlParser) parseOpeningTag(start int) {
	name, end := p.readName(start + openingTagStartLen)
	isSupported := supportedTags[name]

	if isSupported == false {
		message := fmt.Sprintf("unsupported tag <%s>", name)
		p.fail(start, message)
	}

	if isSupported == true {
		p.parseAttributes(start, name, end)
	}
}

// parseAttributes reads the attributes up to ">" and pushes the tag.
func (p *htmlParser) parseAttributes(start int, name string, offset int) {
	seen := map[string]bool{}
	cursor := p.skipSpaces(offset)
	isDone := false

	for (p.err == nil) && (isDone == false) {
		isDone = (cursor < len(p.input)) && (p.input[cursor] == '>')

		if isDone == false {
			cursor = p.parseAttribute(start, name, cursor, seen)
			cursor = p.skipSpaces(cursor)
		}
	}

	if p.err == nil {
		p.checkRequiredAttributes(start, name, seen)
	}

	if p.err == nil {
		p.open = append(p.open, openTag{name: name, offset: start})
		p.pos = cursor + 1
	}
}

// parseAttribute reads one name[=value] and returns the offset after it.
func (p *htmlParser) parseAttribute(start int, tag string, offset int, seen map[string]bool) int {
	result := offset
	isEnd := (offset >= len(p.input))

	switch {
	case isEnd:
		message := fmt.Sprintf(`<%s> is not closed with ">"`, tag)
		p.fail(start, message)
	case p.input[offset] == '/':
		p.fail(offset, "self-closing tags are not supported")
	case isNameByte(p.input[offset]) == false:
		message := fmt.Sprintf("invalid attribute in <%s>", tag)
		p.fail(offset, message)
	default:
		result = p.parseNamedAttribute(tag, offset, seen)
	}

	return result
}

// parseNamedAttribute reads an attribute that starts with a name.
func (p *htmlParser) parseNamedAttribute(tag string, offset int, seen map[string]bool) int {
	name, end := p.readName(offset)
	afterName := p.skipSpaces(end)
	value := ""
	hasValue := (afterName < len(p.input)) && (p.input[afterName] == '=')
	result := end

	if hasValue == true {
		valueStart := p.skipSpaces(afterName + 1)
		value, result = p.readAttributeValue(name, valueStart)
	}

	if p.err == nil {
		p.checkAttribute(offset, tag, name, value, seen[name])
		seen[name] = true
	}

	return result
}

// readAttributeValue reads a quoted or unquoted value.
func (p *htmlParser) readAttributeValue(name string, offset int) (string, int) {
	result := ""
	next := offset
	isEnd := (offset >= len(p.input))
	isQuoted := (isEnd == false) && ((p.input[offset] == '"') || (p.input[offset] == '\''))

	switch {
	case isEnd:
		message := fmt.Sprintf("attribute %s has no value", name)
		p.fail(offset, message)
	case isQuoted:
		result, next = p.readQuotedValue(name, offset)
	default:
		result, next = p.readUnquotedValue(name, offset)
	}

	return result, next
}

// readQuotedValue reads a value up to its closing quote.
func (p *htmlParser) readQuotedValue(name string, offset int) (string, int) {
	quote := p.input[offset]
	length := strings.IndexByte(p.input[offset+1:], quote)
	result := ""
	next := len(p.input)

	if length < 0 {
		message := fmt.Sprintf("the value of attribute %s is not closed", name)
		p.fail(offset, message)
	}

	if length >= 0 {
		result = p.input[offset+1 : offset+1+length]
		next = offset + length + 2
	}

	return result, next
}

// readUnquotedValue reads a value up to whitespace or ">".
func (p *htmlParser) readUnquotedValue(name string, offset int) (string, int) {
	end := offset

	for (end < len(p.input)) && (isSpaceByte(p.input[end]) == false) && (p.input[end] != '>') {
		end++
	}

	result := p.input[offset:end]

	if result == "" {
		message := fmt.Sprintf("attribute %s has no value", name)
		p.fail(offset, message)
	}

	return result, end
}

// checkAttribute allows only the attributes Telegram reads for the tag.
func (p *htmlParser) checkAttribute(offset int, tag string, name string, value string, isRepeated bool) {
	problem := attributeProblem(tag, name, value)

	if isRepeated == true {
		problem = fmt.Sprintf("attribute %s is repeated in <%s>", name, tag)
	}

	if problem != "" {
		p.fail(offset, problem)
	}
}

// attributeProblem describes why an attribute is not allowed, or "". The
// value is checked with its entities decoded, as Telegram reads it.
func attributeProblem(tag string, name string, rawValue string) string {
	value := decodeAttributeEntities(rawValue)
	result := fmt.Sprintf("attribute %s is not allowed in <%s>", name, tag)
	isLanguage := strings.HasPrefix(value, languageClassPrefix)

	switch {
	case (tag == tagAnchor) && (name == attrHref):
		result = ""
	case (tag == tagSpan) && (name == attrClass) && (value == spoilerClass):
		result = ""
	case (tag == tagSpan) && (name == attrClass):
		result = `<span> accepts only class="tg-spoiler"`
	case (tag == tagCode) && (name == attrClass) && (isLanguage == true):
		result = ""
	case (tag == tagCode) && (name == attrClass):
		result = `<code> accepts only class="language-..."`
	case (tag == tagBlockquote) && (name == attrExpandable):
		result = ""
	case (tag == tagEmoji) && (name == attrEmojiID) && (isDigits(value) == true):
		result = ""
	case (tag == tagEmoji) && (name == attrEmojiID):
		result = "emoji-id must be a number"
	}

	return result
}

// decodeAttributeEntities decodes the references Telegram supports in an
// attribute value; any other "&" stays as it is.
func decodeAttributeEntities(value string) string {
	var result strings.Builder
	rest := value

	for rest != "" {
		rest = decodeLeadingEntity(&result, rest)
	}

	return result.String()
}

// decodeLeadingEntity writes the decoded reference at the start of rest, or
// its first character, and returns what is left.
func decodeLeadingEntity(out *strings.Builder, rest string) string {
	char, size := leadingEntity(rest)

	if size == 0 {
		char, size = utf8.DecodeRuneInString(rest)
	}

	out.WriteRune(char)
	result := rest[size:]

	return result
}

// leadingEntity decodes a supported reference at the start of text; size 0
// when there is none.
func leadingEntity(text string) (rune, int) {
	var result rune
	size := 0

	if text[0] == entityStart {
		result, size = referenceAfterAmpersand(text)
	}

	return result, size
}

// referenceAfterAmpersand reads "&name;" at the start of text and decodes
// it; size 0 when it is not a supported reference.
func referenceAfterAmpersand(text string) (rune, int) {
	var result rune
	size := 0
	end := 1

	for (end < len(text)) && (isEntityNameByte(text[end]) == true) {
		end++
	}

	hasName := (end > 1) && (end < len(text)) && (text[end] == entityEnd)

	if hasName == true {
		result, size = decodeEntityName(text[1:end])
	}

	return result, size
}

// decodeEntityName decodes a named or numeric reference name; size is the
// length of the whole reference, 0 when it is not supported.
func decodeEntityName(name string) (rune, int) {
	char, isValid := namedEntities[name]
	isNumeric := strings.HasPrefix(name, string(entityNumberMark))
	size := 0

	if isNumeric == true {
		char, isValid = numericEntity(name)
	}

	if isValid == true {
		size = len(name) + entityDelimiters
	}

	return char, size
}

// isDigits reports a non-empty string of ASCII digits.
func isDigits(value string) bool {
	result := (value != "")

	for index := 0; (result == true) && (index < len(value)); index++ {
		result = (value[index] >= '0') && (value[index] <= '9')
	}

	return result
}

// checkRequiredAttributes fails for a span without the spoiler class or a
// custom emoji without its id.
func (p *htmlParser) checkRequiredAttributes(start int, tag string, seen map[string]bool) {
	isSpanMissing := (tag == tagSpan) && (seen[attrClass] == false)
	isEmojiMissing := (tag == tagEmoji) && (seen[attrEmojiID] == false)

	if isSpanMissing == true {
		p.fail(start, `<span> needs class="tg-spoiler"`)
	}

	if isEmojiMissing == true {
		p.fail(start, "<tg-emoji> needs emoji-id")
	}
}
