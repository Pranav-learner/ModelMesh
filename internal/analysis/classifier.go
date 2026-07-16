package analysis

import (
	"fmt"
	"sort"
)

// Classifier assigns a complexity classification to a set of signals. It is an
// interface so the rule-based default can be swapped without touching the engine.
type Classifier interface {
	Classify(s Signals) Classification
}

// ClassifierConfig configures the rule-based classifier: the rule set and the two
// score thresholds that separate Simple / Medium / Complex.
type ClassifierConfig struct {
	RuleSet          RuleSet `json:"-"`
	MediumThreshold  float64 `json:"medium_threshold"`
	ComplexThreshold float64 `json:"complex_threshold"`
}

// DefaultClassifierConfig returns the default rule set and thresholds.
func DefaultClassifierConfig() ClassifierConfig {
	return ClassifierConfig{
		RuleSet:          DefaultRuleSet(),
		MediumThreshold:  DefaultMediumThreshold,
		ComplexThreshold: DefaultComplexThreshold,
	}
}

// withDefaults fills unset fields.
func (c ClassifierConfig) withDefaults() ClassifierConfig {
	if len(c.RuleSet.Rules) == 0 {
		c.RuleSet = DefaultRuleSet()
	}
	if c.MediumThreshold <= 0 {
		c.MediumThreshold = DefaultMediumThreshold
	}
	if c.ComplexThreshold <= c.MediumThreshold {
		c.ComplexThreshold = c.MediumThreshold + DefaultComplexThreshold - DefaultMediumThreshold
	}
	return c
}

// Validate reports whether the configuration is usable.
func (c ClassifierConfig) Validate() error {
	if c.MediumThreshold <= 0 {
		return fmt.Errorf("analysis: medium_threshold must be positive")
	}
	if c.ComplexThreshold <= c.MediumThreshold {
		return fmt.Errorf("analysis: complex_threshold must exceed medium_threshold")
	}
	if len(c.RuleSet.Rules) == 0 {
		return fmt.Errorf("analysis: rule set must not be empty")
	}
	return nil
}

// RuleClassifier is the default, deterministic, explainable Classifier. It runs
// every rule, sums the weights of the matches, and maps the total onto a
// complexity band, recording which rules fired and which features they used.
type RuleClassifier struct {
	cfg ClassifierConfig
}

// Compile-time assertion.
var _ Classifier = (*RuleClassifier)(nil)

// NewRuleClassifier constructs a rule classifier, applying config defaults.
func NewRuleClassifier(cfg ClassifierConfig) *RuleClassifier {
	return &RuleClassifier{cfg: cfg.withDefaults()}
}

// Classify evaluates the rules against the signals and returns the classification.
func (c *RuleClassifier) Classify(s Signals) Classification {
	var score float64
	triggered := make([]TriggeredRule, 0, len(c.cfg.RuleSet.Rules))
	featureSet := map[string]struct{}{}

	for _, r := range c.cfg.RuleSet.Rules {
		if r.Match(s) {
			score += r.Weight
			triggered = append(triggered, TriggeredRule{Name: r.Name, Description: r.Description, Weight: r.Weight})
			for _, f := range r.Features {
				featureSet[f] = struct{}{}
			}
		}
	}

	complexity := c.band(score)
	return Classification{
		Complexity:     complexity,
		Score:          score,
		Confidence:     c.confidence(score, complexity),
		FeaturesUsed:   sortedKeys(featureSet),
		TriggeredRules: triggered,
	}
}

// band maps a score to a complexity label.
func (c *RuleClassifier) band(score float64) Complexity {
	switch {
	case score >= c.cfg.ComplexThreshold:
		return ComplexityComplex
	case score >= c.cfg.MediumThreshold:
		return ComplexityMedium
	default:
		return ComplexitySimple
	}
}

// confidence measures how decisively the score sits within its band, in [0.5, 1].
// A score near a boundary is borderline (→ 0.5); one deep in a band is confident.
func (c *RuleClassifier) confidence(score float64, complexity Complexity) float64 {
	med, cmp := c.cfg.MediumThreshold, c.cfg.ComplexThreshold
	var margin, width float64
	switch complexity {
	case ComplexitySimple:
		margin, width = med-score, med
	case ComplexityComplex:
		margin, width = score-cmp, cmp
	default: // Medium: distance to the nearer boundary, over half the band width.
		margin = minFloat(score-med, cmp-score)
		width = (cmp - med) / 2
	}
	if width <= 0 {
		return 1
	}
	frac := margin / width
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return 0.5 + 0.5*frac
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
