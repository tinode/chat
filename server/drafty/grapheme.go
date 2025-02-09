package drafty

import (
	"github.com/rivo/uniseg"
)

// graphemes is a container holding lengths of grapheme clusters in a string.
type graphemes struct {
	// The original string.
	original string

	// Sizes of grapheme clusters within the original string.
	sizes []byte
}

// prepareGraphemes returns a parsed grapheme cluster container by splitting the string into grapheme clusters
// and saving their lengths.
func prepareGraphemes(str string) *graphemes {
	// Split the string into grapheme clusters and save the size of each cluster.
	sizes := make([]byte, 0, len(str))
	for state, remaining, cluster := -1, str, ""; len(remaining) > 0; {
		cluster, remaining, _, state = uniseg.StepString(remaining, state)
		sizes = append(sizes, byte(len(cluster)))
	}

	return &graphemes{
		original: str,
		sizes:    sizes,
	}
}

// length returns the number of grapheme clusters in the original string.
func (g *graphemes) length() int {
	if g == nil {
		return 0
	}
	return len(g.sizes)
}

// string returns the original string from which the grapheme cluster container was created.
func (g *graphemes) string() string {
	if g == nil {
		return ""
	}
	return g.original
}

// slice returns a new grapheme cluster container with grapheme clusters from 'start' to 'end'.
func (g *graphemes) slice(start, end int) *graphemes {

	// Convert grapheme offsets to string offsets.
	s := 0
	for i := 0; i < start; i++ {
		s += int(g.sizes[i])
	}
	e := s
	for i := start; i < end; i++ {
		e += int(g.sizes[i])
	}

	return &graphemes{
		original: g.original[s:e],
		sizes:    g.sizes[start:end],
	}
}

// append appends 'other' grapheme cluster container to 'g' container and returns g.
// If g is nil, the 'other' is returned.
func (g *graphemes) append(other *graphemes) *graphemes {
	if g == nil {
		return other
	}

	g.original += other.original
	g.sizes = append(g.sizes, other.sizes...)
	return g
}
