package analysis

import (
	"regexp"
	"strings"
)

var (
	// jsonKeyRe matches a JSON/object key-value pair.
	jsonKeyRe = regexp.MustCompile(`"[\w \-]+"\s*:`)
	// xmlTagRe matches an XML/HTML tag.
	xmlTagRe = regexp.MustCompile(`</?[a-zA-Z][\w-]*(\s[^>]*)?/?>`)
	// yamlKeyRe matches a YAML-style "key: value" line.
	yamlKeyRe = regexp.MustCompile(`(?m)^[ \t]*[\w-]+:[ \t]+\S`)
	// tableSepRe matches a markdown table separator row.
	tableSepRe = regexp.MustCompile(`\|?\s*:?-{3,}:?\s*\|`)
)

// StructuredDataExtractor detects embedded structured data: JSON, XML/HTML, YAML,
// CSV, or a markdown table. Any one match sets the flag.
type StructuredDataExtractor struct{}

// Name returns the extractor identifier.
func (StructuredDataExtractor) Name() string { return "structured_data" }

// Extract sets HasStructuredData.
func (StructuredDataExtractor) Extract(p Preprocessed, f *PromptFeatures) {
	text := p.Text
	f.HasStructuredData = looksLikeJSON(text) ||
		xmlTagRe.MatchString(text) ||
		looksLikeYAML(text) ||
		looksLikeCSV(text) ||
		tableSepRe.MatchString(text)
}

// looksLikeJSON requires both an object/array delimiter and a key-value pair, so
// prose containing a stray brace does not match.
func looksLikeJSON(s string) bool {
	return (strings.Contains(s, "{") || strings.Contains(s, "[")) && jsonKeyRe.MatchString(s)
}

// looksLikeYAML requires at least two "key: value" lines.
func looksLikeYAML(s string) bool {
	return len(yamlKeyRe.FindAllString(s, 3)) >= 2
}

// looksLikeCSV requires at least two lines that share the same (non-zero) comma
// count — a consistent columnar shape rather than incidental commas.
func looksLikeCSV(s string) bool {
	lines := strings.Split(s, "\n")
	commas, rows := -1, 0
	for _, line := range lines {
		c := strings.Count(line, ",")
		if c == 0 {
			continue
		}
		if commas == -1 {
			commas = c
		}
		if c == commas {
			rows++
		}
	}
	return commas >= 1 && rows >= 2
}
