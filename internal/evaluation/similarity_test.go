package evaluation

import (
	"math"
	"testing"
)

func TestExactMatch(t *testing.T) {
	if !exactMatch("hello world", "  hello world  ") {
		t.Errorf("exact match should ignore surrounding whitespace")
	}
	if exactMatch("hello", "hello world") {
		t.Errorf("different text should not match")
	}
}

func TestWordCosine(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"the quick brown fox", "the quick brown fox", 1}, // identical
		{"", "", 1},                               // both empty
		{"hello", "", 0},                          // one empty
		{"apple banana", "cherry date", 0},        // disjoint vocab
		{"the cat sat", "the cat ran", 2.0 / 3.0}, // 2 of 3 words shared, unit vectors
	}
	for _, tc := range cases {
		got := wordCosine(tc.a, tc.b)
		if math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("wordCosine(%q,%q) = %.4f, want %.4f", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestWordCosine_OrderIndependent(t *testing.T) {
	if math.Abs(wordCosine("a b c", "c b a")-1) > 1e-9 {
		t.Errorf("cosine should be order-independent")
	}
}

func TestWordCosine_FrequencyWeighted(t *testing.T) {
	// "spam spam spam eggs" vs "spam eggs" share vocab but differ in frequency.
	s := wordCosine("spam spam spam eggs", "spam eggs")
	if s <= 0 || s >= 1 {
		t.Errorf("frequency-weighted similarity should be strictly between 0 and 1, got %.4f", s)
	}
}
