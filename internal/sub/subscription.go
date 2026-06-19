package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const (
	subscriptionsFileName    = "subscriptions.json"
	subscriptionUserAgent    = "podlaz"
	subscriptionClientHeader = "x-hwid"
)

var subscriptionUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Format identifies a supported or planned subscription source format.
type Format string

const (
	FormatUnknown  Format = "unknown"
	FormatURIList  Format = "uri-list"
	FormatBase64   Format = "base64"
	FormatXrayJSON Format = "xray-json"
	FormatSingBox  Format = "sing-box"
	FormatMihomo   Format = "mihomo"
)

var ErrNotFound = errors.New("subscription not found")
var ErrAlreadyExists = errors.New("subscription already exists")

// Source describes a remote or local subscription source.
type Source struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	Format        Format    `json:"format"`
	ProfileIDs    []string  `json:"profile_ids,omitempty"`
	LastUpdatedAt time.Time `json:"last_updated_at,omitempty"`
}

// Issue describes a non-fatal subscription entry problem.
type Issue struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Parsed contains normalized profiles and non-fatal entry diagnostics.
type Parsed struct {
	Profiles    []profile.Profile
	Unsupported []Issue
	Warnings    []Issue
}

// UpdateResult is the user-visible diff for a subscription update.
type UpdateResult struct {
	Subscription Source
	Imported     int
	Updated      int
	Unchanged    int
	Removed      int
	Unsupported  int
	Warnings     []Issue
	Issues       []Issue
}

// Store persists subscription source metadata under the documented user state location.
type Store struct {
	path string
}

// NewStore returns a subscription store at path. If path is empty, the documented
// XDG user state path is used.
func NewStore(path string) (Store, error) {
	if path == "" {
		defaultPath, err := DefaultStorePath()
		if err != nil {
			return Store{}, err
		}
		path = defaultPath
	}
	return Store{path: path}, nil
}

// DefaultStorePath returns $XDG_STATE_HOME/podlaz/subscriptions.json or the
// documented ~/.local/state/podlaz/subscriptions.json fallback.
func DefaultStorePath() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" || !filepath.IsAbs(stateHome) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve podlaz state directory: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "podlaz", subscriptionsFileName), nil
}

func (s Store) Path() string { return s.path }

// NewSource returns a normalized subscription source with a deterministic local ID.
func NewSource(name, sourceURL string) Source {
	sourceURL = strings.TrimSpace(sourceURL)
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = fallbackSubscriptionDisplayName(sourceURL)
	}
	return Source{
		ID:     profile.NormalizeID(displayName),
		Name:   displayName,
		URL:    sourceURL,
		Format: FormatBase64,
	}
}

func fallbackSubscriptionDisplayName(sourceURL string) string {
	candidate := "subscription"
	if u, err := url.Parse(strings.TrimSpace(sourceURL)); err == nil {
		host := strings.TrimSpace(u.Hostname())
		base := safeHumanPathBase(u.Path)
		switch {
		case host != "" && base != "":
			candidate = host + " " + base
		case host != "":
			candidate = host
		case base != "":
			candidate = base
		}
	}
	if name, ok := profile.SanitizeDisplayName(candidate); ok {
		return name
	}
	return "subscription"
}

func safeHumanPathBase(rawPath string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" || rawPath == "/" {
		return ""
	}
	if suspiciousSubscriptionPathContext(rawPath) {
		return ""
	}
	base, err := url.PathUnescape(pathpkg.Base(rawPath))
	if err != nil {
		return ""
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	if unsafeSubscriptionPathBase(base) {
		return ""
	}
	name, ok := profile.SanitizeDisplayName(base)
	if !ok {
		return ""
	}
	return name
}

func suspiciousSubscriptionPathContext(rawPath string) bool {
	dir := pathpkg.Dir(rawPath)
	if dir == "." || dir == "/" {
		return false
	}
	for _, segment := range strings.Split(dir, "/") {
		segment = normalizeSubscriptionPathContextSegment(segment)
		switch {
		case segment == "sub",
			segment == "subs",
			segment == "subscription",
			segment == "subscriptions",
			segment == "subscribe",
			segment == "invite",
			segment == "link",
			segment == "token",
			segment == "key":
			return true
		case strings.HasPrefix(segment, "sub") && len(segment) >= len("sub")+4:
			return true
		}
	}
	return false
}

func normalizeSubscriptionPathContextSegment(segment string) string {
	segment = strings.ToLower(strings.TrimSpace(segment))
	replacer := strings.NewReplacer(
		"0", "o",
		"1", "i",
		"3", "e",
		"4", "a",
		"5", "s",
		"7", "t",
		"-", "",
		"_", "",
		".", "",
	)
	return replacer.Replace(segment)
}

func unsafeSubscriptionPathBase(base string) bool {
	trimmed := strings.TrimSpace(base)
	lower := strings.ToLower(trimmed)
	return subscriptionUUIDPattern.MatchString(trimmed) ||
		looksSubscriptionSecretLike(lower) ||
		looksSubscriptionPathTokenLike(trimmed) ||
		looksNumericOrHashLike(lower)
}

func looksSubscriptionSecretLike(value string) bool {
	for _, marker := range []string{"token", "password", "passwd", "secret", "private", "authorization", "api_key", "apikey"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func looksSubscriptionPathTokenLike(value string) bool {
	if len(value) < 12 || strings.ContainsAny(value, " -_") {
		return false
	}
	hasLower, hasUpper, hasDigit := false, false, false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			return false
		}
	}
	return hasLower && hasUpper && hasDigit
}

func looksNumericOrHashLike(value string) bool {
	if len(value) >= 6 && allDigits(value) {
		return true
	}
	return len(value) >= 8 && allHex(value)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func allHex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return true
}

func ValidateSource(source Source) error {
	var messages []string
	if strings.TrimSpace(source.ID) == "" {
		messages = append(messages, "id is required")
	}
	if strings.TrimSpace(source.Name) == "" {
		messages = append(messages, "name is required")
	}
	if strings.TrimSpace(source.URL) == "" {
		messages = append(messages, "url is required")
	} else if err := validateSourceURL(source.URL); err != nil {
		messages = append(messages, err.Error())
	}
	switch source.Format {
	case FormatBase64, FormatXrayJSON:
	case "":
		messages = append(messages, "format is required")
	default:
		messages = append(messages, fmt.Sprintf("unsupported subscription format %q", source.Format))
	}
	if len(messages) > 0 {
		return fmt.Errorf("invalid subscription: %s", strings.Join(messages, "; "))
	}
	return nil
}

func validateSourceURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("url is invalid: %w", err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("url must include a scheme")
	}
	switch strings.ToLower(u.Scheme) {
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return fmt.Errorf("file URL host must be empty or localhost")
		}
		if u.Path == "" {
			return fmt.Errorf("file URL path is required")
		}
	case "http", "https":
		if u.Host == "" {
			return fmt.Errorf("url host is required")
		}
	default:
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	return nil
}

func (s Store) Add(source Source) error {
	if err := ValidateSource(source); err != nil {
		return err
	}
	sources, err := s.load()
	if err != nil {
		return err
	}
	for _, existing := range sources {
		if existing.ID == source.ID {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, source.ID)
		}
	}
	sources = append(sources, source)
	sortSources(sources)
	return s.save(sources)
}

func (s Store) List() ([]Source, error) {
	sources, err := s.load()
	if err != nil {
		return nil, err
	}
	sortSources(sources)
	return sources, nil
}

func (s Store) Get(id string) (Source, error) {
	sources, err := s.load()
	if err != nil {
		return Source{}, err
	}
	for _, source := range sources {
		if source.ID == id {
			return source, nil
		}
	}
	return Source{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

func (s Store) Update(source Source) error {
	if err := ValidateSource(source); err != nil {
		return err
	}
	sources, err := s.load()
	if err != nil {
		return err
	}
	updated := false
	for i := range sources {
		if sources[i].ID == source.ID {
			sources[i] = source
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("%w: %s", ErrNotFound, source.ID)
	}
	sortSources(sources)
	return s.save(sources)
}

// FetchSource reads subscription content. file:// URLs are handled locally; http
// and https URLs use a bounded GET and do not start any network processes or
// mutate host networking state.
func FetchSource(ctx context.Context, source Source) ([]byte, error) {
	if err := ValidateSource(source); err != nil {
		return nil, err
	}
	u, err := url.Parse(source.URL)
	if err != nil {
		return nil, fmt.Errorf("parse subscription URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "file":
		path, err := url.PathUnescape(u.Path)
		if err != nil {
			return nil, fmt.Errorf("read subscription %s: file path is not valid percent-encoding", source.ID)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read subscription %s: %w", source.ID, err)
		}
		return data, nil
	case "http", "https":
		requestURL, clientID, err := subscriptionRequestURL(source.URL)
		if err != nil {
			return nil, fmt.Errorf("fetch subscription %s: %w", source.ID, err)
		}
		if clientID == "" {
			clientID, err = LoadOrCreateClientID("")
			if err != nil {
				return nil, fmt.Errorf("fetch subscription %s: prepare subscription client identity: %w", source.ID, err)
			}
		}

		client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: sameOriginRedirectPolicy}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fetchSubscriptionError(source.ID, err, clientID)
		}
		req.Header.Set("User-Agent", subscriptionUserAgent)
		req.Header.Set(subscriptionClientHeader, clientID)
		res, err := client.Do(req)
		if err != nil {
			return nil, fetchSubscriptionError(source.ID, err, clientID)
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch subscription %s: unexpected HTTP status %d", source.ID, res.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024+1))
		if err != nil {
			return nil, fmt.Errorf("read subscription %s response: %w", source.ID, err)
		}
		if len(data) > 4*1024*1024 {
			return nil, fmt.Errorf("read subscription %s response: content exceeds 4 MiB limit", source.ID)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("unsupported subscription URL scheme %q", u.Scheme)
	}
}

func fetchSubscriptionError(sourceID string, err error, clientID string) error {
	if clientID == "" {
		return fmt.Errorf("fetch subscription %s: %w", sourceID, err)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		cause := strings.ReplaceAll(urlErr.Err.Error(), clientID, "REDACTED")
		return fmt.Errorf("fetch subscription %s: %s redacted subscription URL: %s", sourceID, urlErr.Op, cause)
	}
	message := strings.ReplaceAll(err.Error(), clientID, "REDACTED")
	return fmt.Errorf("fetch subscription %s: %s", sourceID, message)
}

func sameOriginRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) > 3 {
		return fmt.Errorf("stopped after 3 redirects")
	}
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1].URL
	if !strings.EqualFold(req.URL.Scheme, previous.Scheme) || !strings.EqualFold(req.URL.Host, previous.Host) {
		return fmt.Errorf("refusing cross-origin subscription redirect from %s://%s to %s://%s", previous.Scheme, previous.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// ParseBase64Subscription decodes a Base64 URI-list subscription and imports
// supported share URI entries into the normalized profile model.
func ParseBase64Subscription(content []byte) (Parsed, error) {
	decoded, err := decodeBase64Flexible(string(content))
	if err != nil {
		return Parsed{}, fmt.Errorf("parse Base64 subscription: %w", err)
	}

	var parsed Parsed
	seenProfiles := map[string]struct{}{}
	lines := strings.Split(strings.ReplaceAll(string(decoded), "\r\n", "\n"), "\n")
	for i, rawLine := range lines {
		lineNo := i + 1
		entry := strings.TrimSpace(rawLine)
		if entry == "" {
			continue
		}
		p, warnings, err := importEntry(entry)
		if err != nil {
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: lineNo, Message: err.Error()})
			continue
		}
		if _, duplicate := seenProfiles[p.ID]; duplicate {
			parsed.Unsupported = append(parsed.Unsupported, Issue{Line: lineNo, Message: fmt.Sprintf("duplicate profile id %q ignored", p.ID)})
			continue
		}
		seenProfiles[p.ID] = struct{}{}
		parsed.Profiles = append(parsed.Profiles, p)
		for _, warning := range warnings {
			parsed.Warnings = append(parsed.Warnings, Issue{Line: lineNo, Message: warning})
		}
	}
	if len(parsed.Profiles) == 0 {
		if len(parsed.Unsupported) > 0 {
			return Parsed{}, fmt.Errorf("subscription contains no supported profiles; first unsupported entry on line %d: %s", parsed.Unsupported[0].Line, parsed.Unsupported[0].Message)
		}
		return Parsed{}, fmt.Errorf("subscription contains no supported profiles")
	}
	profile.DeduplicateDisplayNames(parsed.Profiles)
	sort.SliceStable(parsed.Profiles, func(i, j int) bool { return parsed.Profiles[i].ID < parsed.Profiles[j].ID })
	return parsed, nil
}

func decodeBase64Flexible(raw string) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, raw)
	if compact == "" {
		return nil, fmt.Errorf("content is empty")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func importEntry(entry string) (profile.Profile, []string, error) {
	u, err := url.Parse(entry)
	if err != nil {
		return profile.Profile{}, nil, fmt.Errorf("invalid URI: %w", err)
	}
	if u.Scheme == "" {
		return profile.Profile{}, nil, fmt.Errorf("unsupported URI entry: scheme is required")
	}
	p, warnings, err := profile.ImportShareURI(entry)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	p.Source = profile.SourceSubscription
	if err := profile.Validate(p); err != nil {
		return profile.Profile{}, nil, err
	}
	return p, warnings, nil
}

type storeFile struct {
	SchemaVersion string   `json:"schema_version"`
	Sources       []Source `json:"subscriptions"`
}

func (s Store) load() ([]Source, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read subscription store %s: %w", s.path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var data storeFile
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("read subscription store %s: invalid JSON: %w", s.path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read subscription store %s: invalid JSON: trailing data", s.path)
	}
	if data.SchemaVersion != "v1" {
		return nil, fmt.Errorf("read subscription store %s: unsupported schema_version %q", s.path, data.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(data.Sources))
	for _, source := range data.Sources {
		if err := ValidateSource(source); err != nil {
			return nil, fmt.Errorf("read subscription store %s: stored subscription %q is invalid: %w", s.path, source.ID, err)
		}
		if _, ok := seen[source.ID]; ok {
			return nil, fmt.Errorf("read subscription store %s: duplicate subscription id %q", s.path, source.ID)
		}
		seen[source.ID] = struct{}{}
	}
	return data.Sources, nil
}

func (s Store) save(sources []Source) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create subscription store directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".subscriptions-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary subscription store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary subscription store: %w", err)
	}

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(storeFile{SchemaVersion: "v1", Sources: sources}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary subscription store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary subscription store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary subscription store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace subscription store atomically: %w", err)
	}
	return nil
}

func sortSources(sources []Source) {
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
}
