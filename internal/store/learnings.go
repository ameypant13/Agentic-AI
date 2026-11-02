package store

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Learning statuses. Only approved learnings are searchable: drafts come from
// the Copilot, which reads untrusted logs and pages, so an admin vets each one
// before it can steer a later investigation.
const (
	LearningDraft    = "draft"
	LearningApproved = "approved"
	LearningRetired  = "retired"
)

// Learning is a reusable diagnosis pattern distilled from investigations:
// what it looks like, why it happens, how to prove it and how to fix it.
type Learning struct {
	ID           string `gorm:"primaryKey"`
	Title        string
	Category     string `gorm:"index"`
	Status       string `gorm:"index"`
	Symptoms     string `gorm:"type:text"`
	RootCause    string `gorm:"type:text"`
	HowToConfirm string `gorm:"type:text"`
	Fix          string `gorm:"type:text"`
	// KGNodes names related control-hub knowledge-graph nodes, comma separated.
	KGNodes string
	// Entities and Investigations are JSON string lists: where it was seen.
	Entities       string `gorm:"type:text"`
	Investigations string `gorm:"type:text"`
	TimesSeen      int
	// Confirmed and Contradicted count investigations that tested the learning.
	Confirmed    int
	Contradicted int
	// ReviewNote is set when an investigation contradicted an approved learning.
	ReviewNote string `gorm:"type:text"`
	CreatedBy  string
	ApprovedBy string
	ApprovedAt *time.Time
	LastSeen   time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (l *Learning) EntityList() []string        { return jsonList(l.Entities) }
func (l *Learning) InvestigationList() []string { return jsonList(l.Investigations) }

// AddSighting records one more investigation (and entity) the learning
// applies to.
func (l *Learning) AddSighting(investigationID, entity string, at time.Time) {
	l.Investigations = addToList(l.Investigations, investigationID, 50)
	l.Entities = addToList(l.Entities, entity, 50)
	l.TimesSeen++
	l.LastSeen = at
}

func jsonList(s string) []string {
	var out []string
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func addToList(s, v string, max int) string {
	v = strings.TrimSpace(v)
	list := jsonList(s)
	if v != "" {
		found := false
		for _, x := range list {
			if strings.EqualFold(x, v) {
				found = true
				break
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	if len(list) > max {
		list = list[len(list)-max:]
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func (s *Store) SaveLearning(l *Learning) error { return s.db.Save(l).Error }

func (s *Store) GetLearning(id string) (*Learning, error) {
	var l Learning
	if err := s.db.Where("id = ?", id).First(&l).Error; err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// ListLearnings returns learnings with the given status (all when empty),
// most recently seen first.
func (s *Store) ListLearnings(status string) ([]Learning, error) {
	q := s.db.Order("last_seen DESC").Limit(1000)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []Learning
	return out, q.Find(&out).Error
}

func (s *Store) DeleteLearning(id string) error {
	return s.db.Where("id = ?", id).Delete(&Learning{}).Error
}

// LearningMatch is a search result.
type LearningMatch struct {
	Learning
	Score float64
}

// SearchLearnings ranks learnings with the given statuses against a free-text
// query (symptoms, error text, alert names) with BM25 over their text fields.
// Title and symptoms weigh more than the rest. A category or entity that
// matches adds to the score. The set is small (hundreds), so it is scored in
// memory rather than with an index.
func (s *Store) SearchLearnings(query, category, entity string, statuses []string, limit int) ([]LearningMatch, error) {
	var all []Learning
	if err := s.db.Where("status IN ?", statuses).Find(&all).Error; err != nil {
		return nil, err
	}
	terms := tokenize(query)
	if len(terms) == 0 && category == "" && entity == "" {
		return nil, nil
	}
	docs := make([]map[string]float64, len(all))
	df := map[string]int{}
	var totalLen float64
	for i, l := range all {
		tf := map[string]float64{}
		for _, f := range []struct {
			text   string
			weight float64
		}{{l.Title, 3}, {l.Symptoms, 2}, {l.RootCause, 1}, {l.HowToConfirm, 0.5}, {l.Fix, 0.5}, {l.KGNodes, 0.5}} {
			for _, t := range tokenize(f.text) {
				tf[t] += f.weight
			}
		}
		for t := range tf {
			df[t]++
		}
		var n float64
		for _, v := range tf {
			n += v
		}
		totalLen += n
		docs[i] = tf
	}
	avg := totalLen / math.Max(1, float64(len(all)))
	const k1, b = 1.2, 0.75
	var out []LearningMatch
	for i, l := range all {
		tf := docs[i]
		var dl float64
		for _, v := range tf {
			dl += v
		}
		var score float64
		for _, t := range terms {
			f := tf[t]
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(all))-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += idf * f * (k1 + 1) / (f + k1*(1-b+b*dl/math.Max(avg, 1)))
		}
		if category != "" && strings.EqualFold(l.Category, category) {
			score += 1
		}
		if entity != "" {
			for _, e := range l.EntityList() {
				if strings.EqualFold(e, entity) {
					score += 1
					break
				}
			}
		}
		if score > 0 {
			out = append(out, LearningMatch{Learning: l, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SearchLearningsAny runs SearchLearnings for each query (a question and its
// rewordings) and merges the rankings with reciprocal rank fusion, so a
// learning written in other words than the question still surfaces. The first
// query counts double: it is what was actually asked.
func (s *Store) SearchLearningsAny(queries []string, category, entity string, statuses []string, limit int) ([]LearningMatch, error) {
	const k = 60.0
	fused := map[string]*LearningMatch{}
	for qi, q := range queries {
		if strings.TrimSpace(q) == "" {
			continue
		}
		matches, err := s.SearchLearnings(q, category, entity, statuses, 0)
		if err != nil {
			return nil, err
		}
		weight := 1.0
		if qi == 0 {
			weight = 2
		}
		for rank, m := range matches {
			f := fused[m.ID]
			if f == nil {
				f = &LearningMatch{Learning: m.Learning}
				fused[m.ID] = f
			}
			f.Score += weight / (k + float64(rank+1))
		}
	}
	out := make([]LearningMatch, 0, len(fused))
	for _, m := range fused {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var stopwords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "was": true, "are": true, "not": true,
	"from": true, "this": true, "that": true, "into": true, "when": true, "has": true, "but": true, "its": true, "can": true}

// tokenize lowercases and splits on anything but letters, digits, '_' and '-'
// (so error codes, metric and resource names stay whole), dropping short words.
func tokenize(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
	out := f[:0]
	for _, w := range f {
		w = strings.Trim(w, "-_")
		if len(w) >= 3 && !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}
