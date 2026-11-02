package logrules

import (
	"regexp/syntax"
	"strings"
)

// prefilter is a cheap test that must pass before a rule's regexp runs: the
// line has to contain at least one of a few literal strings the pattern
// cannot match without. Most log lines contain none of them, so the regexp
// rarely runs. A nil prefilter means "always run the regexp".
type prefilter struct {
	anyOf []string
	fold  bool // compare against the lower-cased line
}

const minLiteral = 4 // shorter literals filter too little to be worth it

func newPrefilter(pattern string) *prefilter {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	lits, fold, ok := required(re.Simplify())
	if !ok || len(lits) == 0 {
		return nil
	}
	for _, l := range lits {
		if len(l) < minLiteral {
			return nil
		}
	}
	if fold {
		for i := range lits {
			lits[i] = strings.ToLower(lits[i])
		}
	}
	return &prefilter{anyOf: lits, fold: fold}
}

// pass reports whether the line might match. lower is the lower-cased line,
// computed by the caller once per line.
func (p *prefilter) pass(line, lower string) bool {
	if p == nil {
		return true
	}
	s := line
	if p.fold {
		s = lower
	}
	for _, l := range p.anyOf {
		if strings.Contains(s, l) {
			return true
		}
	}
	return false
}

// required returns literal strings at least one of which appears in every
// match of re. ok is false when no such set can be derived.
func required(re *syntax.Regexp) (lits []string, fold bool, ok bool) {
	fold = re.Flags&syntax.FoldCase != 0
	switch re.Op {
	case syntax.OpLiteral:
		return []string{string(re.Rune)}, fold, true
	case syntax.OpCapture:
		return required(re.Sub[0])
	case syntax.OpPlus:
		return required(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return required(re.Sub[0])
		}
		return nil, false, false
	case syntax.OpConcat:
		// Join runs of adjacent literals, then keep the best requirement among
		// the parts: the set whose shortest literal is longest.
		var best []string
		bestFold, bestScore := false, -1
		consider := func(l []string, f bool) {
			if s := minLen(l); s > bestScore {
				best, bestFold, bestScore = l, f, s
			}
		}
		var run strings.Builder
		runFold := false
		flush := func() {
			if run.Len() > 0 {
				consider([]string{run.String()}, runFold)
				run.Reset()
				runFold = false
			}
		}
		for _, sub := range re.Sub {
			if sub.Op == syntax.OpLiteral {
				run.WriteString(string(sub.Rune))
				runFold = runFold || sub.Flags&syntax.FoldCase != 0
				continue
			}
			flush()
			if l, f, ok := required(sub); ok {
				consider(l, f)
			}
		}
		flush()
		return best, bestFold, bestScore >= 0
	case syntax.OpAlternate:
		var all []string
		anyFold := false
		for _, sub := range re.Sub {
			l, f, ok := required(sub)
			if !ok {
				return nil, false, false // one branch needs nothing, so the whole needs nothing
			}
			all = append(all, l...)
			anyFold = anyFold || f
		}
		return all, anyFold, true
	}
	return nil, false, false
}

func minLen(l []string) int {
	if len(l) == 0 {
		return -1
	}
	m := len(l[0])
	for _, s := range l[1:] {
		m = min(m, len(s))
	}
	return m
}
