package tgsignal

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/runenames"
)

// Callers decorate follow-ups with emojis. Most are decoration (🔥 on
// price posts), but an emoji-only reply can carry meaning ("✅" after a
// target, "❌" on a waiting call). Code never acts on an emoji alone: the
// same "✅" means different things after "Active" and after a target.
// Every emoji is turned into hint words for the LLM, which reads them with
// the call's trade context: this group's meaning from the glossary, else
// the emoji's Unicode name ("🍀 = four leaf clover").

// emojiMeaning is one glossary row. decor: the emoji only decorates (a
// price running), so an emoji-only reply of these is not worth a read.
type emojiMeaning struct {
	emojis  string
	meaning string
	decor   bool
}

var emojiGlossary = []emojiMeaning{
	{"✅✔☑💯", "done/hit", false},
	{"🎯", "target", false},
	{"🛑❌🚫⛔", "stop/cancel", false},
	{"😵☠💀😢😭", "loss/stop hit", false},
	{"🔔🚨⚠", "alert", false},
	{"💰💵🤑", "profit/money", false},
	{"⏳⌛", "wait", false},
	{"🔥🚀📈🔺🔼⬆", "price rising", true},
	{"📉🔻🔽⬇", "price falling", true},
	{"➡➜", "now at", true},
}

// glossaryOf maps each glossary emoji to its row.
var glossaryOf = func() map[rune]emojiMeaning {
	m := make(map[rune]emojiMeaning)
	for _, g := range emojiGlossary {
		for _, r := range g.emojis {
			m[r] = g
		}
	}
	return m
}()

// isEmoji reports whether r is an emoji or pictograph. Joiners, variation
// selectors and skin-tone modifiers are parts of one, not emojis.
func isEmoji(r rune) bool {
	switch {
	case r == 0x200D, r == 0xFE0F, r == 0xFE0E, r >= 0x1F3FB && r <= 0x1F3FF:
		return false
	case r >= 0x1F000 && r <= 0x1FAFF, // pictographs, emoticons, flags, symbols
		r >= 0x2600 && r <= 0x27BF, // misc symbols, dingbats
		r >= 0x2B00 && r <= 0x2BFF, // arrows, stars
		r >= 0x2190 && r <= 0x21FF, // arrows
		r >= 0x2300 && r <= 0x23FF: // ⌛ ⏳
		return true
	}
	return r > 0x7F && unicode.Is(unicode.So, r)
}

// emojis is each distinct emoji in text, in order.
func emojis(text string) []rune {
	var out []rune
	seen := map[rune]bool{}
	for _, r := range text {
		if isEmoji(r) && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// emojiMeaningOf is r's meaning: the glossary's, else its Unicode name.
func emojiMeaningOf(r rune) string {
	if g, ok := glossaryOf[r]; ok {
		return g.meaning
	}
	if n := runenames.Name(r); n != "" {
		return strings.ToLower(n)
	}
	return "emoji"
}

// emojiHint is the meaning line for every emoji in text, "" if none:
// "(emojis: ✅ = done/hit, 🍀 = four leaf clover)".
func emojiHint(text string) string {
	es := emojis(text)
	if len(es) == 0 {
		return ""
	}
	parts := make([]string, len(es))
	for i, r := range es {
		parts[i] = string(r) + " = " + emojiMeaningOf(r)
	}
	return "(emojis: " + strings.Join(parts, ", ") + ")"
}

// hasActionEmoji reports whether text has an emoji that may be a whole
// instruction: any emoji that is not price decoration (🔥, 📈, ➡), the
// unknown ones included.
func hasActionEmoji(text string) bool {
	for _, r := range emojis(text) {
		if g, ok := glossaryOf[r]; !ok || !g.decor {
			return true
		}
	}
	return false
}
