// Package suggest finds the closest name for did-you-mean hints.
package suggest

// Nearest returns the candidate closest to token by Levenshtein distance and
// whether a close-enough match (distance <= 2) exists. Ties resolve to the
// first candidate seen at the minimum distance.
func Nearest(token string, candidates []string) (string, bool) {
	best, bestDist := "", -1
	for _, c := range candidates {
		d := levenshtein(token, c)
		if bestDist == -1 || d < bestDist {
			best, bestDist = c, d
		}
	}
	if bestDist >= 0 && bestDist <= 2 {
		return best, true
	}
	return "", false
}

// levenshtein computes the edit distance between a and b using a two-row DP.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}
