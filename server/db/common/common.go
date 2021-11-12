// Package common contains utility methods used by all adapters.
package common

import (
	"sort"
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
