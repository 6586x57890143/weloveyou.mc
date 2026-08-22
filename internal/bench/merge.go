package bench

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Results are what a shard hands back, and what a merge puts together again.
//
// This is the Go-native shape rather than the presentation JSON that
// RenderJSON produces. Shards write it, the merge step reads several of them,
// and the combined slice then goes through the ordinary Render and RenderJSON
// path. That way the table and the site are produced by exactly one piece of
// code whether the sweep ran on one box or six, and a sharded run cannot
// quietly disagree with an unsharded one.

// MarshalResults serialises one shard's measurements.
func MarshalResults(rs []Result) ([]byte, error) {
	b, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding results: %w", err)
	}
	return append(b, '\n'), nil
}

// MergeResults combines shards into one slice, ordered so the report is stable
// no matter which box finished first or in what order the files were read.
func MergeResults(shards [][]byte) ([]Result, error) {
	var all []Result
	for i, raw := range shards {
		var rs []Result
		if err := json.Unmarshal(raw, &rs); err != nil {
			return nil, fmt.Errorf("decoding shard %d: %w", i+1, err)
		}
		all = append(all, rs...)
	}
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].Workload != all[b].Workload {
			return all[a].Workload < all[b].Workload
		}
		return all[a].Profile < all[b].Profile
	})
	return all, nil
}
