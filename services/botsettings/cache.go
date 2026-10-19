package botsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultCachePath is the cache file, relative to the bot's working
// directory (git-ignored).
const DefaultCachePath = "var/settings-cache.json"

// Cache file permissions: only the bot's user reads it.
const (
	cacheDirMode  = 0o700
	cacheFileMode = 0o600
)

// cacheFile is the last good set of owner-set values, for a start while the
// database is down: without it a platform the owner switched off would come
// back on.
type cacheFile struct {
	SavedAt time.Time                  `json:"saved_at"`
	Values  map[string]json.RawMessage `json:"values"`
}

// writeCache stores values atomically (a temporary file renamed over the
// old one), so a crash never leaves half a file.
func writeCache(path string, values []StoredValue, savedAt time.Time) error {
	content := cacheFile{SavedAt: savedAt, Values: map[string]json.RawMessage{}}

	for _, value := range values {
		content.Values[value.Key] = value.Value
	}

	encoded, err := json.MarshalIndent(content, "", "  ")
	dir := filepath.Dir(path)

	if err == nil {
		err = os.MkdirAll(dir, cacheDirMode)
	}

	if err == nil {
		err = writeFileAtomically(path, encoded)
	}

	return err
}

// writeFileAtomically writes content to a temporary file next to path and
// renames it over path.
func writeFileAtomically(path string, content []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	temp, err := os.CreateTemp(dir, base+".*.tmp")
	tempPath := ""

	if err == nil {
		tempPath = temp.Name()
		_, err = temp.Write(content)
	}

	if err == nil {
		err = temp.Sync()
	}

	if temp != nil {
		closeErr := temp.Close()
		err = firstError(err, closeErr)
	}

	if err == nil {
		err = os.Chmod(tempPath, cacheFileMode)
	}

	if err == nil {
		err = os.Rename(tempPath, path)
	}

	if (err != nil) && (tempPath != "") {
		_ = os.Remove(tempPath)
	}

	return err
}

// firstError keeps the first of two errors.
func firstError(first error, second error) error {
	result := first

	if result == nil {
		result = second
	}

	return result
}

// readCache loads the cache file.
func readCache(path string) ([]StoredValue, time.Time, error) {
	result := []StoredValue{}
	content := cacheFile{}
	encoded, err := os.ReadFile(path)

	if err == nil {
		err = json.Unmarshal(encoded, &content)
	}

	for key, value := range content.Values {
		result = append(result, StoredValue{Key: key, Value: value})
	}

	return result, content.SavedAt, err
}
