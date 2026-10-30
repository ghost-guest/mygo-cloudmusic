package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// loader fetches a source script from a URL or reads it from a local
// file, persists it under a directory, and reports what its header says.

var headerField = regexp.MustCompile(`@(name|author|version|homepage)\s+([^\r\n*]+)`)

// parseHeader pulls @name/@author/@version from a source's header comment.
func parseHeader(js string) SourceMeta {
	meta := SourceMeta{}
	for _, m := range headerField.FindAllStringSubmatch(js, -1) {
		val := strings.TrimSpace(strings.TrimSuffix(m[2], "*/"))
		switch m[1] {
		case "name":
			meta.Name = val
		case "author":
			meta.Author = val
		case "version":
			meta.Version = val
		}
	}
	if meta.Sources == nil {
		meta.Sources = map[string]PlatformInfo{}
	}
	return meta
}

// loadFromURL fetches the script at url with a timeout.
func loadFromURL(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hostUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// saveScript writes js to dir/<slug>.js, returning the path.
func saveScript(dir, slug string, js []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, slug+".js")
	if err := os.WriteFile(path, js, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// slugOf derives a safe file name from a source URL.
func slugOf(url string) string {
	name := filepath.Base(strings.Split(url, "?")[0])
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if name == "" || name == "." || name == "/" {
		name = "source"
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "source"
	}
	return s
}
