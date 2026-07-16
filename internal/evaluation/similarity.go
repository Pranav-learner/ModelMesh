package evaluation

import (
	"math"
	"strings"
)

// TextSimilarity is a deterministic [0,1] text-similarity function. The default is
// wordCosine; a caller may inject an alternative (e.g. Jaccard) via WithTextSimilarity.
type TextSimilarity func(a, b string) float64

// EmbeddingSimilarity is the optional embedding-similarity abstraction. It returns
// a [0,1] score and whether it could produce one. It is deliberately an interface
// point, not an implementation — ModelMesh ships no embedding model here.
type EmbeddingSimilarity func(a, b string) (float64, bool)

// exactMatch reports whether two responses are identical after trimming.
func exactMatch(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// wordCosine is the default text similarity: cosine similarity over word-frequency
// vectors. It is deterministic, cheap, and order-independent (captures overlap of
// vocabulary weighted by frequency). Two empty strings are perfectly similar; one
// empty and one not are perfectly dissimilar.
func wordCosine(a, b string) float64 {
	fa := wordFreq(a)
	fb := wordFreq(b)
	if len(fa) == 0 && len(fb) == 0 {
		return 1
	}
	if len(fa) == 0 || len(fb) == 0 {
		return 0
	}

	var dot, na, nb float64
	for w, ca := range fa {
		na += float64(ca * ca)
		if cb, ok := fb[w]; ok {
			dot += float64(ca * cb)
		}
	}
	for _, cb := range fb {
		nb += float64(cb * cb)
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// wordFreq tokenizes s into lowercase word counts.
func wordFreq(s string) map[string]int {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	if len(fields) == 0 {
		return nil
	}
	freq := make(map[string]int, len(fields))
	for _, w := range fields {
		freq[w]++
	}
	return freq
}
