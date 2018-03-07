// Generic data manipulation utilities.

package main

// Convert a list of IDs into ranges
func delrangeDeserialize(in []types.Range) []MsgDelRange {
	if len(in) == 0 {
		return nil
	}

	var out []MsgDelRange
	for _, r := range in {
		out = append(out, MsgDelRange{LowId: r.Low, HiId: r.Hi})
	}

	return out
}

func delrangeSerialize(in []MsgDelRange) []types.Range {
	if len(in) == 0 {
		return nil
	}

	var out []types.Range
	for _, r := range in {
		out = append(out, types.Range{Low: r.LowId, Hi: r.HiId})
	}

	return out
}

// Convert a slice of strings into a slice of tags.
func toTagSlice(tags []string, uniqueTags map[string]bool) TagSlice {
	var out TagSlice
	if len(uniqueTags) > 0 && len(tags) > 0 {
		for _, s := range tags {
			parts := strings.SplitN(s, ":", 2)
			out = append(out, t.Tag{Val: s, Unique: en(parts) == 2 && uniqueTags[parts[0]]})
		}
	}
	return out
}

// Trim whitespace, remove empty tags and duplicates, ensure proper format of prefixes.
func normalizeTags(dst []string, src []string) []string {
	if len(src) == 0 {
		return dst
	}

	// Make sure the number of tags does not exceed the maximum.
	// Technically it may result in fewer tags than the maximum due to empty tags and
	// duplicates, but that's user's fault.
	if len(src) > globals.maxTagCount {
		src = src[:globals.maxTagCount]
	}

	// Trim whitespace and force to lowercase.
	for i := 0; i < len(src); i++ {
		src[i] = strings.ToLower(strings.TrimSpace(src[i]))
	}

	// Sort tags
	sort.Strings(src)

	// Remove short tags and de-dupe keeping the order. It may result in fewer tags than could have
	// been if length were enforced later, but that's client's fault.
	var prev string
	for i := 0; i < len(src); {
		curr := src[i]
		if len(curr) < minTagLength || curr == prev || isNullValue(curr) {
			// Re-slicing is not efficient but the slice is short so we don't care.
			src = append(src[:i], src[i+1:]...)
			continue
		}
		prev = curr
		i++
	}

	// If prefixes are used, check their format.
	// Copy to destination slice.
	for i := 0; i < len(src); i++ {
		parts := strings.SplitN(src[i], ":", 2)
		if len(parts) < 2 {
			// Not in "tag:value" format
			dst = append(dst, src[i])
			continue
		}

		// Skip invalid strings of the form "tag:" or ":value" or ":"
		if parts[0], parts[1] = strings.TrimSpace(parts[0]),
			strings.TrimSpace(parts[1]); parts[0] == "" || parts[1] == "" {
			continue
		}

		// Add tag to output.
		dst = append(dst, parts[0]+":"+parts[1])
	}

	// Because prefixes were forced to lowercase, the tags may be unsorted now.
	return dst
}

func filterUniqueTags(unique, tags []string) []string {
	var out []string
	if len(unique) > 0 && len(tags) > 0 {
		for _, s := range tags {
			parts := strings.SplitN(s, ":", 2)

			if len(parts) < 2 {
				continue
			}

			for _, u := range unique {
				if parts[0] == u {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// Takes get.data or get.del parameters, returns database query parameters
func msgOpts2storeOpts(req *MsgBrowseOpts) *types.BrowseOpt {
	var opts *types.BrowseOpt
	if req != nil {
		opts = &types.BrowseOpt{
			Limit:  req.Limit,
			Since:  req.SinceId,
			Before: req.BeforeId,
		}
	}
	return opts
}

func isNullValue(i interface{}) bool {
	// Unicode Del control character
	const clearValue = "\u2421"
	if str, ok := i.(string); ok {
		return str == clearValue
	}
	return false
}

func decodeAuthError(code int, id string, timestamp time.Time) *ServerComMessage {
	var errmsg *ServerComMessage
	switch code {
	case auth.NoErr:
		errmsg = NoErr(id, "", timestamp)
	case auth.InfoNotModified:
		errmsg = InfoNotModified(id, "", timestamp)
	case auth.ErrInternal:
		errmsg = ErrUnknown(id, "", timestamp)
	case auth.ErrMalformed:
		errmsg = ErrMalformed(id, "", timestamp)
	case auth.ErrFailed:
		errmsg = ErrAuthFailed(id, "", timestamp)
	case auth.ErrDuplicate:
		errmsg = ErrDuplicateCredential(id, "", timestamp)
	case auth.ErrUnsupported:
		errmsg = ErrNotImplemented(id, "", timestamp)
	case auth.ErrExpired:
		errmsg = ErrAuthFailed(id, "", timestamp)
	case auth.ErrPolicy:
		errmsg = ErrPolicy(id, "", timestamp)
	default:
		errmsg = ErrUnknown(id, "", timestamp)
	}
	return errmsg
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
