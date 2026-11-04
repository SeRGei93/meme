// Package graph owns the v1 memory-graph link rules: pairing records by shared
// project and keyword overlap, weighting the links and capping their density.
// Persistence and SQL stay in store.
package graph

import (
	"sort"
	"strings"
)

const (
	// ProjectWeight is the contribution of a shared project. It is the weaker
	// of the two v1 signals.
	ProjectWeight = 0.25
	// KeywordWeight scales the Jaccard keyword overlap. Project and keywords
	// together reach the contract's maximum weight of 1.
	KeywordWeight = 0.75

	// MaxEdgesPerNode bounds the graph density so a large project cannot turn
	// into a complete graph that the UI cannot render.
	MaxEdgesPerNode = 20
)

// Link reasons from the v1 contract.
const (
	ReasonProject  = "project"
	ReasonKeywords = "keywords"
)

// Record is the minimum a node needs to participate in linking.
type Record struct {
	ID        int64
	ProjectID int64
	Keywords  []string
}

// Edge links two records. FromID is always smaller than ToID.
type Edge struct {
	FromID int64
	ToID   int64
	Weight float64
	Reason string
}

// Build scores every record pair, drops links below minWeight and keeps the
// strongest links under maxEdgesPerNode per node. The boolean reports that the
// cap removed at least one candidate link.
func Build(records []Record, minWeight float64, maxEdgesPerNode int) ([]Edge, bool) {
	keywords := make([]map[string]struct{}, len(records))
	for i, record := range records {
		keywords[i] = keywordSet(record.Keywords)
	}

	candidates := []Edge{}
	for i := range records {
		for j := i + 1; j < len(records); j++ {
			weight, reason := linkWeight(records[i], records[j], keywords[i], keywords[j])
			if weight == 0 || weight < minWeight {
				continue
			}
			from, to := records[i].ID, records[j].ID
			if from > to {
				from, to = to, from
			}
			candidates = append(candidates, Edge{FromID: from, ToID: to, Weight: weight, Reason: reason})
		}
	}

	if maxEdgesPerNode < 1 {
		return []Edge{}, len(candidates) > 0
	}

	// Strongest first, with a stable tie-break so equal weights still produce a
	// deterministic graph.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		if candidates[i].FromID != candidates[j].FromID {
			return candidates[i].FromID < candidates[j].FromID
		}
		return candidates[i].ToID < candidates[j].ToID
	})

	incident := make(map[int64][]int, len(records)*2)
	for i, edge := range candidates {
		incident[edge.FromID] = append(incident[edge.FromID], i)
		incident[edge.ToID] = append(incident[edge.ToID], i)
	}

	selected := make([]bool, len(candidates))
	degree := make(map[int64]int, len(records))
	for _, list := range incident {
		for _, index := range list[:min(len(list), maxEdgesPerNode)] {
			if selected[index] {
				continue
			}
			selected[index] = true
			degree[candidates[index].FromID]++
			degree[candidates[index].ToID]++
		}
	}

	// A node can exceed the cap when many neighbours nominate it. Shed its
	// weakest kept links, preferring links to other over-cap nodes, so pruning
	// removes redundant edges instead of starving the newest records.
	incidentOrder := make(map[int64][]int, len(incident))
	for nodeID, list := range incident {
		ordered := append([]int(nil), list...)
		sort.SliceStable(ordered, func(i, j int) bool {
			first, second := candidates[ordered[i]], candidates[ordered[j]]
			if first.Weight != second.Weight {
				return first.Weight < second.Weight
			}
			return otherNode(first, nodeID) > otherNode(second, nodeID)
		})
		incidentOrder[nodeID] = ordered
	}

	for {
		var (
			overNode int64
			overDeg  = maxEdgesPerNode
		)
		for nodeID, d := range degree {
			if d > overDeg {
				overNode, overDeg = nodeID, d
			}
		}
		if overNode == 0 {
			break
		}

		pick := -1
		for _, index := range incidentOrder[overNode] {
			if !selected[index] {
				continue
			}
			if degree[otherNode(candidates[index], overNode)] > maxEdgesPerNode {
				pick = index
				break
			}
			if pick == -1 {
				pick = index
			}
		}
		if pick == -1 {
			break
		}
		selected[pick] = false
		degree[candidates[pick].FromID]--
		degree[candidates[pick].ToID]--
	}

	kept := make([]Edge, 0, len(candidates))
	for i, edge := range candidates {
		if selected[i] {
			kept = append(kept, edge)
		}
	}
	// The response is ordered by ids for a stable diff and a predictable UI
	// layout; strength is carried by Weight, not by position.
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].FromID != kept[j].FromID {
			return kept[i].FromID < kept[j].FromID
		}
		return kept[i].ToID < kept[j].ToID
	})

	return kept, len(kept) < len(candidates)
}

// linkWeight combines the two v1 link signals. Reason names the larger
// contributor, so a dominant keyword match is reported as a keyword link even
// when the records also share a project.
func linkWeight(first, second Record, firstKeywords, secondKeywords map[string]struct{}) (float64, string) {
	weight := 0.0
	reason := ""

	if first.ProjectID != 0 && first.ProjectID == second.ProjectID {
		weight = ProjectWeight
		reason = ReasonProject
	}
	if overlap := jaccard(firstKeywords, secondKeywords); overlap > 0 {
		contribution := KeywordWeight * overlap
		weight += contribution
		if contribution >= ProjectWeight || reason == "" {
			reason = ReasonKeywords
		}
	}
	if weight > 1 {
		weight = 1
	}
	return weight, reason
}

// keywordSet normalizes keywords for overlap comparison: trimmed, lower-cased
// and de-duplicated.
func keywordSet(raw []string) map[string]struct{} {
	set := make(map[string]struct{}, len(raw))
	for _, keyword := range raw {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		set[keyword] = struct{}{}
	}
	return set
}

func jaccard(first, second map[string]struct{}) float64 {
	if len(first) == 0 || len(second) == 0 {
		return 0
	}

	shared := 0
	for keyword := range first {
		if _, ok := second[keyword]; ok {
			shared++
		}
	}
	union := len(first) + len(second) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

func otherNode(edge Edge, nodeID int64) int64 {
	if edge.FromID == nodeID {
		return edge.ToID
	}
	return edge.FromID
}
