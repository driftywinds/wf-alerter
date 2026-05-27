package warframe

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// deApiURL is the official Digital Extremes worldstate endpoint.
const deApiURL = "https://api.warframe.com/cdn/worldState.php"

// cacheKeyCombined is a sentinel key used to cache the combined worldstate response.
const cacheKeyCombined = "_combined"

// Client fetches and caches data from the official DE worldstate endpoint.
type Client struct {
	platform   string
	httpClient *http.Client
	cacheTTL   time.Duration

	mu    sync.RWMutex
	cache map[string]*cacheEntry
}

type cacheEntry struct {
	data      json.RawMessage
	fetchedAt time.Time
}

// NewClient creates a new API client for the given platform (pc, ps4, xb1, swi).
func NewClient(platform string, cacheTTL time.Duration) *Client {
	return &Client{
		platform:   platform,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		cacheTTL:   cacheTTL,
		cache:      make(map[string]*cacheEntry),
	}
}

// WorldstateEndpoints lists all the fields we extract from the combined worldstate response.
var WorldstateEndpoints = []string{
	"alerts",
	"arbitration",
	"archonHunt",
	"cambionCycle",
	"cetusCycle",
	"conclaveChallenges",
	"constructionProgress",
	"dailyDeals",
	"deepArchimedea",
	"earthCycle",
	"events",
	"fissures",
	"globalUpgrades",
	"invasions",
	"nightwave",
	"news",
	"sentientOutposts",
	"simaris",
	"sortie",
	"steelPath",
	"syndicateMissions",
	"vallisCycle",
	"vaultTrader",
	"voidTrader",
	"voidTraders",
}

// GetWorldstate fetches the worldstate from the official DE endpoint and transforms
// it into a map keyed by endpoint name.
//
// The WFCD warframestat.us API no longer returns data for individual endpoints,
// so we fetch directly from the game's official worldstate endpoint.
func (c *Client) GetWorldstate() (map[string]json.RawMessage, error) {
	// Check cache first
	c.mu.RLock()
	entry, ok := c.cache[cacheKeyCombined]
	c.mu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < c.cacheTTL {
		return c.extractFields(entry.data)
	}

	// Fetch from the official DE worldstate endpoint
	// The WFCD warframestat.us API no longer returns data for individual endpoints.
	resp, err := c.httpClient.Get(deApiURL)
	if err != nil {
		// Return stale cache if available
		if ok {
			log.Printf("warframe: using stale cache for worldstate (%v)", err)
			return c.extractFields(entry.data)
		}
		return nil, fmt.Errorf("fetch worldstate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if ok {
			log.Printf("warframe: using stale cache for worldstate (HTTP %d)", resp.StatusCode)
			return c.extractFields(entry.data)
		}
		return nil, fmt.Errorf("fetch worldstate: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		// Return stale cache if available
		if ok {
			log.Printf("warframe: using stale cache for worldstate (read error: %v)", err)
			return c.extractFields(entry.data)
		}
		return nil, fmt.Errorf("read worldstate: %w", err)
	}

	// Transform the raw DE data into the WFCD-style format the app expects
	transformed := TransformWorldstate(data, c.platform)
	if transformed == nil {
		if ok {
			log.Printf("warframe: using stale cache for worldstate (transform failed)")
			return c.extractFields(entry.data)
		}
		return nil, fmt.Errorf("transform worldstate: nil result")
	}

	// Cache the raw DE data for stale fallback
	c.mu.Lock()
	c.cache[cacheKeyCombined] = &cacheEntry{data: data, fetchedAt: time.Now()}
	c.mu.Unlock()

	return transformed, nil
}

// extractFields returns the cached/transformed result. This is used for stale cache fallback.
func (c *Client) extractFields(data json.RawMessage) (map[string]json.RawMessage, error) {
	transformed := TransformWorldstate(data, c.platform)
	if transformed == nil {
		return nil, fmt.Errorf("transform worldstate: nil result")
	}
	return transformed, nil
}

// InvalidateCache clears the entire cache.
func (c *Client) InvalidateCache() {
	c.mu.Lock()
	c.cache = make(map[string]*cacheEntry)
	c.mu.Unlock()
}

// Platform returns the current platform string.
func (c *Client) Platform() string { return c.platform }
