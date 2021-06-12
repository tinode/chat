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

	if !ims.IsZero() || len(subs) > limit {
		// Now that we fetched potentially more subscriptions than needed, we got to take those with the oldest modifications.
		// Sorting in ascending order by modification time.
		sort.Slice(subs, func(i, j int) bool {
			return subs[i].UpdatedAt.Before(subs[j].UpdatedAt)
		})
		if !ims.IsZero() {
			// Keep only those subscriptions which are newer than ims.
			at := sort.Search(len(subs), func(i int) bool { return subs[i].UpdatedAt.After(ims) })
			subs = subs[at:]
		}
		// Trim slice at the limit.
		if len(subs) > limit {
			subs = subs[:limit]
		}
	}

	return subs
}

// SelectEarliestUpdatedAt picks the oldest timestamp which is still newer than the threshold ims.
// The 'old' can be less than 'ims', the 'curr' must be greater than 'ims'.
func SelectEarliestUpdatedAt(old, curr, ims time.Time) time.Time {
	if old.Before(ims) {
		// Subscription has not changed recently, use user's update timestamp.
		return curr
	} else if old.After(curr) {
		if !ims.IsZero() {
			// Subscription changed after the user and cache management: using earlier timestamp.
			return curr
		}
	} else if ims.IsZero() {
		// Subscription changed before the user and NO cache management: using later timestamp.
		return curr
	}

	return old
}
