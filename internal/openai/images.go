package openai

import (
	"encoding/base64"
	"fmt"
	"strings"

	"copilot-openai-proxy/internal/copilot"
)

const (
	maxImagesPerRequest = 4
	maxImageBytes       = 10 << 20 // 10 MiB
)

type completionPayload struct {
	Prompt string
	Images []copilot.ImageInput
}

// buildCompletionPayload assembles the text prompt and image bytes from OpenAI
// chat messages. Only data:image/{png,jpeg};base64,... URLs are accepted.
func buildCompletionPayload(req ChatCompletionRequest) (completionPayload, error) {
	var (
		b      strings.Builder
		images []copilot.ImageInput
	)
	for _, msg := range req.Messages {
		text, msgImages, err := extractMessageContent(msg.Content)
		if err != nil {
			return completionPayload{}, err
		}
		if len(images)+len(msgImages) > maxImagesPerRequest {
			return completionPayload{}, fmt.Errorf("at most %d images are supported per request", maxImagesPerRequest)
		}
		images = append(images, msgImages...)

		switch msg.Role {
		case "system":
			b.WriteString("[System] ")
			b.WriteString(text)
			b.WriteString("\n\n")
		case "user":
			b.WriteString(text)
			b.WriteString("\n")
		case "assistant":
			b.WriteString("[Assistant] ")
			b.WriteString(text)
			b.WriteString("\n")
		default:
			b.WriteString(text)
			b.WriteString("\n")
		}
	}
	return completionPayload{Prompt: b.String(), Images: images}, nil
}

func extractMessageContent(content interface{}) (string, []copilot.ImageInput, error) {
	switch v := content.(type) {
	case string:
		return v, nil, nil
	case []interface{}:
		var (
			b      strings.Builder
			images []copilot.ImageInput
		)
		for _, part := range v {
			m, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			switch m["type"] {
			case "text":
				if text, ok := m["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString(text)
				}
			case "image_url":
				image, err := parseImageURLPart(m["image_url"])
				if err != nil {
					return "", nil, err
				}
				images = append(images, image)
			}
		}
		return b.String(), images, nil
	default:
		return fmt.Sprintf("%v", content), nil, nil
	}
}

func parseImageURLPart(raw interface{}) (copilot.ImageInput, error) {
	switch v := raw.(type) {
	case map[string]interface{}:
		url, _ := v["url"].(string)
		return parseDataImageURL(url)
	case string:
		return parseDataImageURL(v)
	default:
		return copilot.ImageInput{}, fmt.Errorf("image_url must be an object with a url field")
	}
}

func parseDataImageURL(url string) (copilot.ImageInput, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return copilot.ImageInput{}, fmt.Errorf("image_url.url is required")
	}
	if !strings.HasPrefix(strings.ToLower(url), "data:") {
		return copilot.ImageInput{}, fmt.Errorf("only data:image URI image_url values are supported; external https URLs are not fetched")
	}

	// data:[<mediatype>][;base64],<data>
	rest := url[len("data:"):]
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return copilot.ImageInput{}, fmt.Errorf("invalid data URI: missing comma")
	}
	meta := rest[:comma]
	payload := rest[comma+1:]
	if payload == "" {
		return copilot.ImageInput{}, fmt.Errorf("invalid data URI: empty image data")
	}

	parts := strings.Split(meta, ";")
	mime := strings.ToLower(strings.TrimSpace(parts[0]))
	if mime == "image/jpg" {
		mime = "image/jpeg"
	}
	if mime != "image/png" && mime != "image/jpeg" {
		return copilot.ImageInput{}, fmt.Errorf("unsupported image mime type %q; only image/png and image/jpeg are supported", mime)
	}

	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	if !isBase64 {
		return copilot.ImageInput{}, fmt.Errorf("only base64 data:image URIs are supported")
	}

	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// Some clients omit padding; try RawStdEncoding.
		data, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return copilot.ImageInput{}, fmt.Errorf("invalid base64 image data: %w", err)
		}
	}
	if len(data) == 0 {
		return copilot.ImageInput{}, fmt.Errorf("decoded image data is empty")
	}
	if len(data) > maxImageBytes {
		return copilot.ImageInput{}, fmt.Errorf("image exceeds maximum size of %d bytes", maxImageBytes)
	}
	return copilot.ImageInput{MIME: mime, Data: data}, nil
}
