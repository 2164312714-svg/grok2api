package imagecontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)`)

// Candidate is a real image reference supplied by the client. Description is
// safe to send to a text model because it identifies position, not image data.
type Candidate struct {
	URL         string
	Description string
}

type Context struct {
	Prompt            string
	Candidates        []Candidate
	CurrentCandidates []Candidate
}

type requestEnvelope struct {
	Messages json.RawMessage `json:"messages"`
	Input    json.RawMessage `json:"input"`
}

type message struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Parse extracts the latest user instruction and orders real image candidates
// as current-user images, then newest historical assistant/user images.
func Parse(body []byte, operation string) (Context, error) {
	var envelope requestEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Context{}, errors.New("请求 JSON 无效")
	}
	return ParseValues(envelope.Messages, envelope.Input, operation)
}

func ParseValues(messagesRaw, inputRaw json.RawMessage, operation string) (Context, error) {
	var messages []message
	if strings.EqualFold(strings.TrimSpace(operation), "chat") {
		if !hasJSONValue(messagesRaw) {
			return Context{}, errors.New("messages 不能为空")
		}
		if err := json.Unmarshal(messagesRaw, &messages); err != nil {
			return Context{}, errors.New("messages 必须是消息数组")
		}
	} else {
		trimmed := bytes.TrimSpace(inputRaw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return Context{}, errors.New("input 不能为空")
		}
		if trimmed[0] == '"' {
			var prompt string
			if json.Unmarshal(trimmed, &prompt) != nil {
				return Context{}, errors.New("input 格式无效")
			}
			return Context{Prompt: strings.TrimSpace(prompt)}, nil
		}
		if err := json.Unmarshal(trimmed, &messages); err != nil {
			return Context{}, errors.New("input 必须是字符串或消息数组")
		}
	}
	return parseMessages(messages)
}

func parseMessages(messages []message) (Context, error) {
	if len(messages) == 0 {
		return Context{}, errors.New("消息数组不能为空")
	}
	latestUser := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if isMessage(messages[index]) && strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			latestUser = index
			break
		}
	}
	if latestUser < 0 {
		return Context{}, errors.New("消息中缺少用户消息")
	}
	prompt, currentImages := contentValues(messages[latestUser].Content)
	result := Context{Prompt: strings.TrimSpace(prompt), Candidates: make([]Candidate, 0, 4), CurrentCandidates: make([]Candidate, 0, len(currentImages))}
	seen := make(map[string]struct{})
	appendImages := func(images []string, description string) {
		for _, value := range images {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result.Candidates = append(result.Candidates, Candidate{URL: value, Description: description})
		}
	}
	for index, image := range currentImages {
		appendImages([]string{image}, fmt.Sprintf("current user message image %d", index+1))
	}
	result.CurrentCandidates = append(result.CurrentCandidates, result.Candidates...)
	assistantImageNumber := 0
	for index := latestUser - 1; index >= 0; index-- {
		if !isMessage(messages[index]) {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(messages[index].Role))
		if role != "assistant" {
			continue
		}
		_, images := contentValues(messages[index].Content)
		for _, image := range images {
			assistantImageNumber++
			age := "older historical"
			if assistantImageNumber == 1 {
				age = "newest historical"
			}
			appendImages([]string{image}, fmt.Sprintf("previous assistant image %d (%s)", assistantImageNumber, age))
		}
	}
	userImageNumber := 0
	for index := latestUser - 1; index >= 0; index-- {
		if !isMessage(messages[index]) || !strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			continue
		}
		_, images := contentValues(messages[index].Content)
		for _, image := range images {
			userImageNumber++
			age := "older historical"
			if userImageNumber == 1 {
				age = "newest historical"
			}
			appendImages([]string{image}, fmt.Sprintf("previous user image %d (%s)", userImageNumber, age))
		}
	}
	return result, nil
}

func contentValues(raw json.RawMessage) (string, []string) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text), markdownImages(text)
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return "", nil
	}
	texts := make([]string, 0, len(parts))
	images := make([]string, 0, len(parts))
	for _, part := range parts {
		typeName := strings.ToLower(strings.TrimSpace(stringValue(part["type"])))
		if typeName == "text" || typeName == "input_text" || typeName == "output_text" {
			value := strings.TrimSpace(stringValue(part["text"]))
			if value != "" {
				texts = append(texts, value)
				images = append(images, markdownImages(value)...)
			}
		}
		if typeName == "image_url" || typeName == "input_image" || typeName == "image" {
			if value := imageSource(part); value != "" {
				images = append(images, value)
			}
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n")), images
}

func imageSource(part map[string]any) string {
	for _, key := range []string{"image_url", "url"} {
		switch value := part[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		case map[string]any:
			if raw := strings.TrimSpace(stringValue(value["url"])); raw != "" {
				return raw
			}
		}
	}
	return ""
}

func markdownImages(text string) []string {
	matches := markdownImagePattern.FindAllStringSubmatch(text, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			result = append(result, strings.TrimSpace(match[1]))
		}
	}
	return result
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func isMessage(value message) bool {
	typeName := strings.ToLower(strings.TrimSpace(value.Type))
	return typeName == "" || typeName == "message"
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
