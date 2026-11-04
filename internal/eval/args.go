package eval

import (
	"sort"
	"strconv"
	"strings"
)

// Args is one task invocation's bound parameters (spec 030 R1).
//
// Values holds every parameter; a variadic's value is its elements joined by
// single spaces, the text every reader saw before spec 030. Lists holds the
// variadic's elements as given, which only quote_each, dependency forwarding,
// and invocation identity read.
//
// Invariants:
//   - For every key `k` in `Lists`, `Values[k] == strings.Join(Lists[k], " ")`,
//     except on the dependency path, where `Values[k]` keeps the written
//     arguments' joined text so an empty forwarded list next to other
//     arguments leaves the extra space it always did.
//   - `Lists` has at most one key.
//   - `Lists[k]` may be empty only for a `*` parameter, or for a `+` parameter
//     that a dependency call forwarded an empty list into.
type Args struct {
	Values map[string]string
	Lists  map[string][]string
}

// Key is a canonical encoding of both maps: equal keys mean the same values
// and the same elements. Every string is strconv-quoted so no value can forge
// a separator.
func (a Args) Key() string {
	var b strings.Builder
	for i, k := range sortedKeys(a.Values) {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(a.Values[k]))
	}
	for _, k := range sortedKeys(a.Lists) {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteString("[]=")
		b.WriteString(ElementsKey(a.Lists[k]))
	}
	return b.String()
}

// ElementsKey encodes a list so that different elements never give the same
// text, which the space-joined value cannot promise. Run-once and the cache
// fingerprint share it so the two identities cannot drift apart.
func ElementsKey(elems []string) string {
	quoted := make([]string, len(elems))
	for i, el := range elems {
		quoted[i] = strconv.Quote(el)
	}
	return strings.Join(quoted, " ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
