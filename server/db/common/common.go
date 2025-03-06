// Package common contains utility methods used by all adapters.
package common

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	t "github.com/tinode/chat/server/store/types"
)

// SelectEarliestUpdatedSubs selects no more than the given number of subscriptions from the
// given slice satisfying the query. When the number of subscriptions is greater than the limit,
// the subscriptions with the earliest timestamp are selected.
func SelectEarliestUpdatedSubs(subs []t.Subscription, opts *t.QueryOpt, maxResults int) []t.Subscription {
	limit := maxResults
	ims := time.Time{}
	if opts != nil {
		if opts.Limit > 0 && opts.Limit < limit {
			limit = opts.Limit
		}
		if opts.IfModifiedSince != nil {
			ims = *opts.IfModifiedSince
		}
	}

	// No cache management and the number of results is below the limit: return all.
	if ims.IsZero() && len(subs) <= limit {
		return subs
	}

	// Now that we fetched potentially more subscriptions than needed, we got to take those with the oldest modifications.
	// Sorting in ascending order by modification time.
	sort.Slice(subs, func(i, j int) bool {
		return subs[i].LastModified().Before(subs[j].LastModified())
	})

	if !ims.IsZero() {
		// Keep only those subscriptions which are newer than ims.
		at := sort.Search(len(subs), func(i int) bool {
			return subs[i].LastModified().After(ims)
		})
		subs = subs[at:]
	}
	// Trim slice at the limit.
	if len(subs) > limit {
		subs = subs[:limit]
	}

	return subs
}

// SelectLatestTime picks the latest update timestamp out of the two.
func SelectLatestTime(t1, t2 time.Time) time.Time {
	if t1.Before(t2) {
		// Subscription has not changed recently, use user's update timestamp.
		return t2
	}

	return t1
}

// RangesToSql converts a slice of ranges to SQL BETWEEN or IN() constraint and arguments.
func RangesToSql(in []t.Range) (string, []any) {
	if len(in) > 1 || in[0].Hi == 0 {
		var args []any
		for _, r := range in {
			if r.Hi == 0 {
				args = append(args, r.Low)
			} else {
				for i := r.Low; i < r.Hi; i++ {
					args = append(args, i)
				}
			}
		}

		return "IN (?" + strings.Repeat(",?", len(args)-1) + ")", args
	}

	// Optimizing for a special case of single range low..hi.
	// MySQL's BETWEEN is inclusive-inclusive thus decrement Hi by 1.
	return "BETWEEN ? AND ?", []any{in[0].Low, in[0].Hi - 1}
}

// Convert to JSON before storing to JSON field.
func ToJSON(src any) []byte {
	if src == nil {
		return nil
	}

	jval, _ := json.Marshal(src)
	return jval
}

// Deserialize JSON data from DB.
func FromJSON(src any) any {
	if src == nil {
		return nil
	}
	if bb, ok := src.([]byte); ok {
		var out any
		json.Unmarshal(bb, &out)
		return out
	}
	return nil
}

// Convert update to a list of columns and arguments.
func UpdateByMap(update map[string]any) (cols []string, args []any) {
	for col, arg := range update {
		col = strings.ToLower(col)
		if col == "public" || col == "trusted" || col == "private" || col == "aux" {
			arg = ToJSON(arg)
		}
		cols = append(cols, col+"=?")
		args = append(args, arg)
	}
	return
}

// If Tags field is updated, get the tags so tags table cab be updated too.
func ExtractTags(update map[string]any) []string {
	var tags []string

	if val := update["Tags"]; val != nil {
		tags, _ = val.(t.StringSlice)
	}

	return []string(tags)
}
