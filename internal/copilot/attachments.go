package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ImageInput is a pending image to upload before a WebSocket send.
// Anonymous Copilot accepts PNG/JPEG via POST /c/api/attachments and returns
// a relative url that must be placed in the send content array as type "image".
type ImageInput struct {
	MIME string
	Data []byte
}

type attachmentUploadResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// UploadImage posts raw image bytes to Copilot's attachments endpoint using
// the session cookies from an anonymous start. The returned URL is typically
// a relative path such as /attachments/<id>.png and is used as-is in WS send.
func (c *Client) UploadImage(ctx context.Context, cookies []*http.Cookie, mime string, data []byte) (string, error) {
	if c == nil {
		return "", fmt.Errorf("copilot client is nil")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("copilot attachment upload: empty image data")
	}
	mime = normalizeImageMIME(mime)
	if mime == "" {
		return "", fmt.Errorf("copilot attachment upload: unsupported image mime type")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.attachmentsURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create copilot attachment request: %w", err)
	}
	setCommonHeaders(req.Header)
	req.Header.Set("Content-Type", mime)
	req.Header.Set("Content-Length", strconv.Itoa(len(data)))
	req.Header.Set("Origin", copilotOrigin)
	if cookieHeader := collectCookies(cookies); cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("copilot attachment upload: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read copilot attachment response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", upstreamErrorf("copilot attachment upload returned %d: %s", resp.StatusCode, truncateForError(string(body)))
	}

	var uploaded attachmentUploadResponse
	if err := json.Unmarshal(body, &uploaded); err != nil {
		return "", fmt.Errorf("parse copilot attachment response: %w", err)
	}
	if uploaded.URL == "" {
		return "", upstreamErrorf("copilot attachment upload missing url")
	}
	return uploaded.URL, nil
}

func (c *Client) uploadImages(ctx context.Context, cookies []*http.Cookie, images []ImageInput) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	urls := make([]string, 0, len(images))
	for i, image := range images {
		url, err := c.UploadImage(ctx, cookies, image.MIME, image.Data)
		if err != nil {
			return nil, fmt.Errorf("upload image %d: %w", i, err)
		}
		urls = append(urls, url)
	}
	return urls, nil
}

func normalizeImageMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	default:
		return ""
	}
}

func truncateForError(s string) string {
	const max = 256
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
