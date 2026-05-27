package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Notifier sends notifications via an Apprise REST API instance.
// See: https://github.com/caronc/apprise-api
type Notifier struct {
	apiURL     string
	httpClient *http.Client
}

// Notification is the payload to send.
type Notification struct {
	Title string
	Body  string
	URLs  []string // Apprise notification URLs, e.g. "discord://webhook..."
}

// New creates a Notifier pointing at the given Apprise API base URL.
func New(apiURL string) *Notifier {
	return &Notifier{
		apiURL:     strings.TrimRight(apiURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Send delivers the notification to all provided URLs via Apprise API.
func (n *Notifier) Send(notif Notification) error {
	if len(notif.URLs) == 0 {
		return fmt.Errorf("no notification URLs provided")
	}

	payload := map[string]interface{}{
		"urls":  strings.Join(notif.URLs, "\n"),
		"title": notif.Title,
		"body":  notif.Body,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := n.apiURL + "/notify/"
	resp, err := n.httpClient.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("post to apprise-api at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("apprise-api returned HTTP %d", resp.StatusCode)
	}

	return nil
}
