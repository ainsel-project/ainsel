package pluginsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MaxSkillBodyBytes caps how much of a plugin's skill document the hub will
// store. The platform's own skill validation caps bodies at 100 KB; the
// plugin-served cap is tighter because the fetch happens on a background loop
// against a pod nobody paid for a large response to, and a skill document over
// 64 KiB is not a document an agent should be carrying per task anyway.
const MaxSkillBodyBytes = 64 * 1024

// errSkillBodyTooLarge is the error an over-cap response is answered with. It
// is a fixed string so a syncer log line names the rule that was hit.
var errSkillBodyTooLarge = errors.New("skill body exceeds 64 KiB")

// NewHTTPSkillFetcher builds the SkillFetcher that reads a skill document off
// a plugin pod: GET "http://<endpoint><path>", the body as text. The caller
// supplies the client (and with it the timeout) so the hub can share its
// timeout policy rather than the fetcher inventing one.
func NewHTTPSkillFetcher(httpClient *http.Client) SkillFetcher {
	return func(ctx context.Context, endpoint, path string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+path, nil)
		if err != nil {
			return "", fmt.Errorf("pluginsync: skill request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("pluginsync: skill fetch: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("pluginsync: skill fetch: unexpected status %d", resp.StatusCode)
		}
		// Read one byte past the cap: "exactly at the cap" must not error, and
		// a full read-then-check is the only way to tell the two apart.
		body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSkillBodyBytes+1))
		if err != nil {
			return "", fmt.Errorf("pluginsync: skill fetch: read body: %w", err)
		}
		if len(body) > MaxSkillBodyBytes {
			return "", errSkillBodyTooLarge
		}
		return string(body), nil
	}
}
