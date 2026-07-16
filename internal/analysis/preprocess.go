package analysis

import (
	"strings"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// DefaultMaxConsecutiveBlankLines bounds runs of blank lines after normalization;
// longer runs are collapsed to this many, removing redundant formatting.
const DefaultMaxConsecutiveBlankLines = 1

// Preprocessor cleans and structures a request's prompt before analysis:
// normalizing whitespace, stripping redundant formatting, counting messages,
// extracting system prompts, and measuring conversation length. It performs no
// feature detection — that is the extractors' job.
type Preprocessor struct {
	maxBlankLines int
}

// PreprocessorOption configures a Preprocessor.
type PreprocessorOption func(*Preprocessor)

// WithMaxBlankLines sets how many consecutive blank lines survive normalization.
func WithMaxBlankLines(n int) PreprocessorOption {
	return func(p *Preprocessor) {
		if n >= 0 {
			p.maxBlankLines = n
		}
	}
}

// NewPreprocessor constructs a Preprocessor with defaults applied.
func NewPreprocessor(opts ...PreprocessorOption) *Preprocessor {
	p := &Preprocessor{maxBlankLines: DefaultMaxConsecutiveBlankLines}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Process normalizes the request messages and returns the structured view.
func (p *Preprocessor) Process(req provider.ChatRequest) Preprocessed {
	out := Preprocessed{Messages: make([]provider.ChatMessage, 0, len(req.Messages))}
	var texts []string
	lastUser := ""

	for _, m := range req.Messages {
		norm := p.normalize(m.Content)
		out.Messages = append(out.Messages, provider.ChatMessage{Role: m.Role, Content: norm})
		out.MessageCount++
		if norm != "" {
			texts = append(texts, norm)
		}
		switch m.Role {
		case provider.RoleSystem:
			out.SystemTurns++
			if norm != "" {
				out.SystemPrompts = append(out.SystemPrompts, norm)
			}
		case provider.RoleUser:
			out.UserTurns++
			lastUser = norm
		case provider.RoleAssistant:
			out.AssistantTurns++
		}
	}

	out.Prompt = lastUser
	out.Text = strings.Join(texts, "\n")
	return out
}

// normalize applies whitespace normalization and redundant-formatting removal to
// a single message body:
//   - CRLF/CR line endings become LF
//   - trailing whitespace is trimmed per line
//   - runs of intra-line whitespace collapse to a single space (leading
//     indentation is preserved so code structure survives)
//   - runs of blank lines collapse to at most maxBlankLines
//   - leading/trailing blank lines are trimmed
func (p *Preprocessor) normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	cleaned := make([]string, len(lines))
	for i, line := range lines {
		cleaned[i] = normalizeLine(line)
	}

	// Collapse runs of blank lines.
	var b []string
	blanks := 0
	for _, line := range cleaned {
		if line == "" {
			blanks++
			if blanks > p.maxBlankLines {
				continue
			}
		} else {
			blanks = 0
		}
		b = append(b, line)
	}
	return strings.TrimSpace(strings.Join(b, "\n"))
}

// normalizeLine preserves a line's leading indentation while collapsing redundant
// internal whitespace and trimming the trailing edge.
func normalizeLine(line string) string {
	trimmedLeft := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmedLeft)]
	body := collapseSpaces(strings.TrimRight(trimmedLeft, " \t"))
	if body == "" {
		return ""
	}
	return indent + body
}

// collapseSpaces replaces runs of spaces/tabs within a string with a single space.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
