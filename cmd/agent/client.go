package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Client is a thin, read-only HTTP client for the Krane API. It sends
// exactly the caller's bearer token on every request -- it never mints,
// caches, or elevates credentials, so the agent can only ever see what the
// token's own user can see.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: http.DefaultClient}
}

// APIError wraps a non-2xx response using the API's own error envelope
// (CLAUDE.md's error-handling section) so callers see exactly what the API
// said, never a guessed or generalized message.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s (http %d): %s", e.Code, e.Status, e.Message)
}

type Event struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Timezone    string  `json:"timezone"`
	StartsAt    string  `json:"starts_at"`
	EndsAt      string  `json:"ends_at"`
	Version     int     `json:"version"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type EventList struct {
	Data       []Event `json:"data"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

type Room struct {
	ID        string `json:"id"`
	EventID   string `json:"event_id"`
	Name      string `json:"name"`
	Capacity  *int   `json:"capacity"`
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type RoomList struct {
	Data       []Room  `json:"data"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

type Session struct {
	ID              string  `json:"id"`
	EventID         string  `json:"event_id"`
	RoomID          string  `json:"room_id"`
	SpeakerID       string  `json:"speaker_id"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	StartsAt        string  `json:"starts_at"`
	EndsAt          string  `json:"ends_at"`
	DurationMinutes int     `json:"duration_minutes"`
	Version         int     `json:"version"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type SessionList struct {
	Data       []Session `json:"data"`
	NextCursor *string   `json:"next_cursor,omitempty"`
}

func (c *Client) ListEvents(ctx context.Context, cursor string) (EventList, error) {
	var out EventList
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	err := c.get(ctx, "/v1/events?"+q.Encode(), &out)
	return out, err
}

func (c *Client) GetEvent(ctx context.Context, eventID string) (Event, error) {
	var out Event
	err := c.get(ctx, "/v1/events/"+url.PathEscape(eventID), &out)
	return out, err
}

func (c *Client) ListSessions(ctx context.Context, eventID, cursor string) (SessionList, error) {
	var out SessionList
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	err := c.get(ctx, "/v1/events/"+url.PathEscape(eventID)+"/sessions?"+q.Encode(), &out)
	return out, err
}

func (c *Client) ListRooms(ctx context.Context, eventID, cursor string) (RoomList, error) {
	var out RoomList
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	err := c.get(ctx, "/v1/events/"+url.PathEscape(eventID)+"/rooms?"+q.Encode(), &out)
	return out, err
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response for %s: %w", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return &APIError{Status: resp.StatusCode, Code: "unknown", Message: string(body)}
		}
		return &APIError{Status: resp.StatusCode, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding response for %s: %w", path, err)
	}
	return nil
}
