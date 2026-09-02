package lexicon

import (
	"sort"
	"strings"
)

// WordTrie indexes multi-word phrases for longest-match lookup over a token
// sequence.
//
// It exists to replace the previous engine's drug matching, which scanned the
// entire drug list on every rx line and compared each entry as a string prefix
// of the joined tokens. With the bundled list of 5,724 entries that is 5,724
// comparisons per prescription; against a real drug vocabulary it would be
// hundreds of thousands. Lookup here costs one map probe per word of the match,
// independent of vocabulary size.
//
// Matching is case-insensitive. Values keep their original casing so a note can
// render the canonical form of a name.
type WordTrie struct {
	children map[string]*WordTrie
	value    string
	terminal bool
	size     int
}

// NewWordTrie returns an empty trie.
func NewWordTrie() *WordTrie {
	return &WordTrie{children: make(map[string]*WordTrie)}
}

// Insert adds a phrase. The phrase is split on whitespace and matched
// case-insensitively; value is what LongestMatch returns.
//
// Inserting the same phrase twice keeps the first value, so a user override
// loaded before the defaults wins without needing a separate merge pass.
func (t *WordTrie) Insert(phrase, value string) {
	words := strings.Fields(strings.ToLower(phrase))
	if len(words) == 0 {
		return
	}

	node := t
	for _, w := range words {
		next, ok := node.children[w]
		if !ok {
			next = NewWordTrie()
			node.children[w] = next
		}
		node = next
	}
	if !node.terminal {
		node.terminal = true
		node.value = value
		t.size++
	}
}

// InsertAll adds every phrase, using the phrase itself as its value.
func (t *WordTrie) InsertAll(phrases []string) {
	for _, p := range phrases {
		t.Insert(p, p)
	}
}

// Len returns the number of distinct phrases in the trie.
func (t *WordTrie) Len() int {
	if t == nil {
		return 0
	}
	return t.size
}

// LongestMatch finds the longest phrase that prefixes words.
//
// It returns the stored value and how many words it consumed. A zero count
// means no match. Matching is greedy over whole words only: "vitamin b12"
// matches the two-word phrase rather than stopping at "vitamin", and a trie
// containing "amoxi" would not match the word "amoxicillin".
func (t *WordTrie) LongestMatch(words []string) (value string, n int) {
	if t == nil {
		return "", 0
	}

	node := t
	for i, w := range words {
		next, ok := node.children[strings.ToLower(w)]
		if !ok {
			break
		}
		node = next
		if node.terminal {
			value, n = node.value, i+1
		}
	}
	return value, n
}

// Contains reports whether the trie holds exactly this phrase.
func (t *WordTrie) Contains(phrase string) bool {
	words := strings.Fields(strings.ToLower(phrase))
	if len(words) == 0 {
		return false
	}
	_, n := t.LongestMatch(words)
	return n == len(words)
}

// PrefixSearch returns up to limit stored phrases beginning with prefix.
//
// Results are sorted so completions never depend on map iteration order. This
// is what lets plugins contribute completions without the previous engine's
// separate hardcoded autocomplete map, which was a third source of clinical
// vocabulary alongside the JSON lexicons and the parser tables.
func (t *WordTrie) PrefixSearch(prefix string, limit int) []string {
	if t == nil || limit <= 0 {
		return nil
	}

	words := strings.Fields(strings.ToLower(prefix))
	if len(words) == 0 {
		return nil
	}

	// Walk the complete words, then treat the final word as a partial prefix.
	node := t
	for _, w := range words[:len(words)-1] {
		next, ok := node.children[w]
		if !ok {
			return nil
		}
		node = next
	}

	last := words[len(words)-1]
	var out []string
	for word, child := range node.children {
		if !strings.HasPrefix(word, last) {
			continue
		}
		child.collect(&out, limit*4)
	}

	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// eachPhrase calls fn for every stored phrase, in sorted order.
//
// Used to mirror the trie into the concept table so a matched drug name
// resolves to a stable identity rather than a bare string.
func (t *WordTrie) eachPhrase(fn func(string)) {
	if t == nil {
		return
	}
	if t.terminal {
		fn(t.value)
	}
	keys := make([]string, 0, len(t.children))
	for k := range t.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.children[k].eachPhrase(fn)
	}
}

// collect appends this node's value and its descendants', stopping at cap.
func (t *WordTrie) collect(out *[]string, cap int) {
	if len(*out) >= cap {
		return
	}
	if t.terminal {
		*out = append(*out, t.value)
	}
	// Sorted traversal keeps the result deterministic regardless of map order.
	keys := make([]string, 0, len(t.children))
	for k := range t.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.children[k].collect(out, cap)
	}
}
