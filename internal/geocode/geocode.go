package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"businessplan/usbvault/internal/db"
)

type Location struct {
	Provider     string `json:"provider"`
	Country      string `json:"country"`
	State        string `json:"state"`
	County       string `json:"county"`
	City         string `json:"city"`
	Road         string `json:"road"`
	HouseNumber  string `json:"house_number"`
	Postcode     string `json:"postcode"`
	DisplayName  string `json:"display_name"`
	RawJSON      string `json:"raw_json"`
	GeocodeKey   string `json:"geocode_key"`
	GeocodeLat   float64
	GeocodeLon   float64
	RequestedLat float64
	RequestedLon float64
}

type ReverseGeocoder struct {
	store  *db.Store
	client *http.Client

	rateMu sync.Mutex
	nextAt time.Time

	inflightMu sync.Mutex
	inflight   map[string]*inflightCall
}

type inflightCall struct {
	done chan struct{}
	loc  *Location
	err  error
}

func New(store *db.Store) *ReverseGeocoder {
	return &ReverseGeocoder{
		store: store,
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
		nextAt:   time.Now(),
		inflight: map[string]*inflightCall{},
	}
}

func Enabled() bool {
	v := strings.TrimSpace(os.Getenv("USBVAULT_REVERSE_GEOCODE"))
	if v == "" {
		return true
	}
	v = strings.ToLower(v)
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func userAgent() string {
	if v := strings.TrimSpace(os.Getenv("USBVAULT_GEOCODE_UA")); v != "" {
		return v
	}
	// Nominatim requires a User-Agent. Use something stable by default.
	return "USBVault/0.2 (local reverse geocoder)"
}

func (g *ReverseGeocoder) Reverse(ctx context.Context, lat, lon float64) (*Location, error) {
	if g == nil {
		return nil, nil
	}
	if !Enabled() {
		return nil, nil
	}

	keyLat := round(lat, 3)
	keyLon := round(lon, 3)
	geoKey := fmt.Sprintf("%.3f,%.3f", keyLat, keyLon)
	provider := "nominatim"

	if cached, ok, err := g.store.GetGeocodeCache(ctx, provider, geoKey); err == nil && ok && cached != nil {
		loc := &Location{
			Provider:     cached.Provider,
			Country:      cached.Country,
			State:        cached.State,
			County:       cached.County,
			City:         cached.City,
			Road:         cached.Road,
			HouseNumber:  cached.HouseNumber,
			Postcode:     cached.Postcode,
			DisplayName:  cached.DisplayName,
			RawJSON:      cached.RawJSON,
			GeocodeKey:   cached.GeocodeKey,
			GeocodeLat:   keyLat,
			GeocodeLon:   keyLon,
			RequestedLat: lat,
			RequestedLon: lon,
		}
		return loc, nil
	}

	// Coalesce concurrent requests per key.
	g.inflightMu.Lock()
	if call, exists := g.inflight[geoKey]; exists {
		g.inflightMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return call.loc, call.err
		}
	}
	call := &inflightCall{done: make(chan struct{})}
	g.inflight[geoKey] = call
	g.inflightMu.Unlock()

	loc, err := g.reverseNominatim(ctx, lat, lon, keyLat, keyLon, geoKey)
	call.loc = loc
	call.err = err
	close(call.done)

	g.inflightMu.Lock()
	delete(g.inflight, geoKey)
	g.inflightMu.Unlock()

	return loc, err
}

func (g *ReverseGeocoder) reverseNominatim(ctx context.Context, lat, lon, keyLat, keyLon float64, geoKey string) (*Location, error) {
	// Respect Nominatim usage policy (keep it slow, cached).
	g.rateMu.Lock()
	minInterval := 1100 * time.Millisecond
	if wait := time.Until(g.nextAt); wait > 0 {
		timer := time.NewTimer(wait)
		g.rateMu.Unlock()
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		g.rateMu.Lock()
	}
	g.nextAt = time.Now().Add(minInterval)
	g.rateMu.Unlock()

	url := fmt.Sprintf("https://nominatim.openstreetmap.org/reverse?format=jsonv2&lat=%.8f&lon=%.8f&zoom=18&addressdetails=1", lat, lon)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("geocoder status: %s", resp.Status)
	}

	var parsed struct {
		DisplayName string                 `json:"display_name"`
		Address     map[string]any         `json:"address"`
		Extra       map[string]interface{} `json:"-"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&parsed); err != nil {
		return nil, err
	}

	addr := parsed.Address
	get := func(key string) string {
		v, ok := addr[key]
		if !ok || v == nil {
			return ""
		}
		s, _ := v.(string)
		return strings.TrimSpace(s)
	}

	city := firstNonEmpty(get("city"), get("town"), get("village"), get("hamlet"), get("municipality"))
	loc := &Location{
		Provider:     "nominatim",
		Country:      get("country"),
		State:        get("state"),
		County:       get("county"),
		City:         city,
		Road:         get("road"),
		HouseNumber:  get("house_number"),
		Postcode:     get("postcode"),
		DisplayName:  strings.TrimSpace(parsed.DisplayName),
		GeocodeKey:   geoKey,
		GeocodeLat:   keyLat,
		GeocodeLon:   keyLon,
		RequestedLat: lat,
		RequestedLon: lon,
	}

	// Store raw JSON for debugging (bounded size).
	rawObj := map[string]any{
		"display_name": parsed.DisplayName,
		"address":      parsed.Address,
	}
	if b, err := json.Marshal(rawObj); err == nil {
		if len(b) > 64*1024 {
			loc.RawJSON = string(b[:64*1024])
		} else {
			loc.RawJSON = string(b)
		}
	}

	if err := g.store.UpsertGeocodeCache(ctx, &db.GeocodeCacheEntry{
		Provider:    loc.Provider,
		GeocodeKey:  loc.GeocodeKey,
		Country:     loc.Country,
		State:       loc.State,
		County:      loc.County,
		City:        loc.City,
		Road:        loc.Road,
		HouseNumber: loc.HouseNumber,
		Postcode:    loc.Postcode,
		DisplayName: loc.DisplayName,
		RawJSON:     loc.RawJSON,
	}); err != nil {
		// Non-fatal.
		return loc, nil
	}
	return loc, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func round(v float64, decimals int) float64 {
	if decimals < 0 {
		return v
	}
	p := math.Pow10(decimals)
	if p == 0 {
		return v
	}
	return math.Round(v*p) / p
}

var ErrUnavailable = errors.New("reverse geocoder unavailable")
