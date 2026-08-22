package bench

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BurntSushi/toml"
)

// Pack identifies the modpack revision a run measured against.
//
// This exists because PackURL is a channel, not a version: it is fetched live
// at container start, so two sweeps a week apart can measure different content
// and nothing in the output would say so. The mod count was the only proxy, and
// it misses the case that matters most - a mod whose version moved while the
// count stayed the same.
//
// IndexHash is the real fingerprint. packwiz writes it over index.toml, so it
// changes if any file in the pack changes, version bump or not. Two rows with
// the same IndexHash measured the same bytes; two with different ones did not,
// whatever their version strings claim.
type Pack struct {
	Version   string `json:"version,omitempty"`
	IndexHash string `json:"index_hash,omitempty"`
	Minecraft string `json:"minecraft,omitempty"`
	Fabric    string `json:"fabric,omitempty"`
}

// Known reports whether anything was actually recorded. A vanilla-workload run
// loads no pack, and a result from before this existed carries none.
func (p Pack) Known() bool { return p.IndexHash != "" || p.Version != "" }

// Same reports whether two runs measured identical pack content.
func (p Pack) Same(o Pack) bool { return p.IndexHash == o.IndexHash }

// String is what a report prints: the version a human recognises, plus enough
// of the hash to tell two builds of one version apart.
func (p Pack) String() string {
	switch {
	case !p.Known():
		return "unrecorded"
	case p.IndexHash == "":
		return p.Version
	case p.Version == "":
		return p.IndexHash[:12]
	}
	return fmt.Sprintf("%s (%s)", p.Version, p.IndexHash[:12])
}

// packFile is the shape of pack.toml that matters here. packwiz writes more
// than this; the rest is not provenance.
type packFile struct {
	Version string `toml:"version"`
	Index   struct {
		Hash string `toml:"hash"`
	} `toml:"index"`
	Versions struct {
		Fabric    string `toml:"fabric"`
		Minecraft string `toml:"minecraft"`
	} `toml:"versions"`
}

// ParsePack reads a packwiz pack.toml.
func ParsePack(data []byte) (Pack, error) {
	var f packFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return Pack{}, fmt.Errorf("parsing pack.toml: %w", err)
	}
	if f.Index.Hash == "" && f.Version == "" {
		// Something parsed, but it was not a pack. Guessing here would record
		// an empty fingerprint as though it were a real one.
		return Pack{}, fmt.Errorf("pack.toml has neither a version nor an index hash")
	}
	return Pack{
		Version:   f.Version,
		IndexHash: f.Index.Hash,
		Minecraft: f.Versions.Minecraft,
		Fabric:    f.Versions.Fabric,
	}, nil
}

// FetchPack reads the pack.toml a sweep is about to measure against.
//
// Called once per sweep rather than per run: the point is to record which
// revision the sweep saw, and a pack republished mid-sweep is a fault the
// validator catches by comparing rows, not something to paper over by
// re-fetching.
func FetchPack(url string) (Pack, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return Pack{}, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Pack{}, fmt.Errorf("fetching %s: %s", url, resp.Status)
	}
	// A pack.toml is a couple of hundred bytes. Anything much larger is a
	// redirect to something else, and reading it all would be the mistake.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return Pack{}, fmt.Errorf("reading %s: %w", url, err)
	}
	return ParsePack(data)
}
