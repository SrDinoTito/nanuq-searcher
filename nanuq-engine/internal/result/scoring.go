package result

import "sort"

// This file ports the SearXNG relevance scoring and result ordering
// (DSG-010, REQ-010, DECISION-011, CON-005: observable behavior is ported
// as-is). Reference implementation: searx/results.py.
//
// Scoring model (searx/results.py L17-38, calculate_score):
//
//	weight = product(engine.weight for engine in result.engines) * len(positions)
//	score  = sum(weight / position for position in positions)
//
// The TASK-004 signature folds the per-engine weight product and the
// len(positions) factor into a single `weight` argument computed by the
// container (TASK-006); the priority branches ("low" skips the result,
// "high" adds the full weight) are likewise applied by the container, which
// owns the engine registry.

// CalculateScore computes the relevance score of a result with the given
// base weight: score = Σ(weight / position) over positions
// (searx/results.py L24-33, calculate_score inner loop, default priority
// branch: score += weight / position).
//
// positions are the 1-based container positions where the result appeared
// (e.g. [1, 3]); each position contributes weight/position, so a result that
// surfaced multiple times scores higher. Positions are always ≥ 1 by
// construction; a non-positive position is skipped defensively (SearXNG
// would raise ZeroDivisionError — unreachable there, guarded here to avoid
// silently poisoning the score with +Inf).
func CalculateScore(weight float64, positions []int) float64 {
	var score float64
	for _, position := range positions {
		if position <= 0 {
			continue
		}
		score += weight / float64(position)
	}
	return score
}

// grpInfo tracks one category/template/img_src cluster while ordering
// results (searx/results.py L191-247, get_ordered_results).
type grpInfo struct {
	// index is the 0-based position where the next result of this cluster is
	// inserted (Python's "index" is the 1-based position of the cluster's
	// last item; inserting at 0-based index == Python's 1-based index keeps
	// the cluster consecutive).
	index int
	// count is the remaining capacity of this cluster (starts at maxCount,
	// decremented per insert; a fresh cluster starts when it reaches 0).
	count int
}

const (
	// maxCount caps how many results of one group may sit in a single
	// cluster (searx/results.py L199: max_count = 8).
	maxCount = 8
	// maxDistance is how far a cluster may drift from the current tail
	// before a new cluster starts (searx/results.py L200: max_distance = 20).
	maxDistance = 20
)

// GetOrderedResults orders a (deduplicated) result set exactly like SearXNG
// get_ordered_results (searx/results.py L191-247):
//
//  1. stable sort by score, descending — equal scores keep their input
//     (extend) order, like Python's stable sorted(..., reverse=True).
//  2. results are grouped by the key category:template:img_src, where the
//     img_src flag is set when the result carries a thumbnail or img_src.
//     Each group starts a cluster (maxCount=8 results); while a cluster has
//     capacity and has not drifted more than maxDistance=20 positions from
//     the current tail, the next same-group result is inserted right at the
//     cluster position (insert+shift), keeping the group consecutive.
//  3. when a cluster is full (count == 0) or too far behind (len - index
//     >= 20), the result is appended at the end and a new cluster for the
//     group starts there with fresh capacity.
//
// The engine→category lookup of the Python original (searx/results.py
// L204-208) is omitted: it needs the engine registry, which the container
// owns (TASK-006); MainResult.Category is assumed to be set already.
// The input slice is not mutated; a new ordered slice is returned.
func GetOrderedResults(results []*MainResult) []*MainResult {
	if len(results) == 0 {
		return results
	}

	// pass 1: sort by score descending, stable (Python sorted(..., key=score,
	// reverse=True) is stable, so equal scores keep extend order).
	ordered := make([]*MainResult, len(results))
	copy(ordered, results)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Score > ordered[j].Score
	})

	// pass 2: cluster results by category:template:img_src with insert+shift.
	gresults := make([]*MainResult, 0, len(ordered))
	categoryPositions := make(map[string]*grpInfo)

	for _, res := range ordered {
		// template always has its effective value (SearXNG MainResult
		// defaults to "default.html"; see as_dict.go).
		template := res.Template
		if template == "" {
			template = "default.html"
		}
		// img_src flag: set when the result carries a thumbnail or img_src
		// (searx/results.py L210).
		imgFlag := ""
		if res.Thumbnail != "" || res.ImgSrc != "" {
			imgFlag = "img_src"
		}
		category := res.Category + ":" + template + ":" + imgFlag

		grp := categoryPositions[category]
		// group with previous same-category results if the cluster can still
		// accept results and is not too far from the current tail
		// (searx/results.py L213-216).
		if grp != nil && grp.count > 0 && len(gresults)-grp.index < maxDistance {
			// insert at the cluster position (Python list.insert(index, res)).
			index := grp.index
			gresults = append(gresults, nil)
			copy(gresults[index+1:], gresults[index:])
			gresults[index] = res
			// shift every cluster index >= index up by one — including this
			// cluster's own, which grows by one slot
			// (searx/results.py L220-224).
			for _, g := range categoryPositions {
				if g.index >= index {
					g.index++
				}
			}
			// consume one slot of this cluster (searx/results.py L226).
			grp.count--
		} else {
			// append at the end and start/refresh the cluster
			// (searx/results.py L228-232).
			gresults = append(gresults, res)
			categoryPositions[category] = &grpInfo{
				index: len(gresults), // == Python's 1-based len(gresults)
				count: maxCount,
			}
		}
	}

	return gresults
}
