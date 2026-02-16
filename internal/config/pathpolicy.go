package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ExcludedMountsSettingKey = "excluded_mounts"

func NormalizeAbsolutePaths(paths []string) []string {
	seen := make(map[string]string, len(paths))
	for _, raw := range paths {
		p := strings.TrimSpace(raw)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		clean := filepath.Clean(p)
		seen[PathKey(clean)] = clean
	}

	out := make([]string, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func ParsePathList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Primary format: JSON array for stable persistence.
	var arr []string
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			return NormalizeAbsolutePaths(arr)
		}
	}

	// Backward/loose fallback: split by newline or comma.
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	return NormalizeAbsolutePaths(parts)
}

func EncodePathList(paths []string) string {
	norm := NormalizeAbsolutePaths(paths)
	b, err := json.Marshal(norm)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func IsPathWithin(path, parent string) bool {
	p := normalizePathForCompare(path)
	par := normalizePathForCompare(parent)
	if p == "" || par == "" {
		return false
	}
	if p == par {
		return true
	}
	sep := string(os.PathSeparator)
	if par == sep {
		return strings.HasPrefix(p, sep)
	}
	return strings.HasPrefix(p, par+sep)
}

func normalizePathForCompare(path string) string {
	key := PathKey(filepath.Clean(path))
	sep := string(os.PathSeparator)
	if key == sep {
		return key
	}
	return strings.TrimSuffix(key, sep)
}
