package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	"github.com/chenyme/grok2api/backend/internal/pkg/imagecontext"
)

const (
	imageEditOptionParseTimeout   = 45 * time.Second
	imageEditOptionAttemptTimeout = 15 * time.Second
)

type ImageEditOptionParseInput struct {
	RequestID             string
	ClientKey             clientkey.Key
	Instruction           string
	Candidates            []imagecontext.Candidate
	NeedPrompt            bool
	NeedImageSelection    bool
	NeedAspectRatio       bool
	NeedResolution        bool
	NeedSize              bool
	PreferredPublicModels []string
}

type ImageEditOptionHints struct {
	Prompt       *string `json:"prompt"`
	ImageIndexes []int   `json:"image_indexes"`
	MultiImage   bool    `json:"multi_image"`
	AspectRatio  *string `json:"aspect_ratio"`
	Resolution   *string `json:"resolution"`
	Size         *string `json:"size"`
}

func (s *Service) InferImageEditOptions(ctx context.Context, input ImageEditOptionParseInput) (ImageEditOptionHints, error) {
	if strings.TrimSpace(input.Instruction) == "" || !imageEditOptionsNeeded(input) {
		return ImageEditOptionHints{}, nil
	}
	candidates, err := s.videoOptionModelCandidates(ctx, input.PreferredPublicModels)
	if err != nil {
		return ImageEditOptionHints{}, err
	}
	if len(candidates) == 0 {
		return ImageEditOptionHints{}, errors.New("没有可用文本模型用于解析图片编辑请求")
	}
	parseCtx, cancel := context.WithTimeout(ctx, imageEditOptionParseTimeout)
	defer cancel()
	internalKey := input.ClientKey
	internalKey.AllowedModels = nil
	internalKey.ProviderScope = clientkey.ProviderScopeAll
	internalKey.TierScope = clientkey.TierScopeAll
	internalKey.AllowModelAliases = false
	internalKey.BillingLimitUSDTicks = 0

	var lastErr error
	for index, candidate := range candidates {
		attemptCtx, attemptCancel := context.WithTimeout(parseCtx, imageEditOptionAttemptTimeout)
		body, buildErr := buildImageEditOptionRequest(input, candidate)
		if buildErr != nil {
			attemptCancel()
			return ImageEditOptionHints{}, buildErr
		}
		result, requestErr := s.CreateChatCompletion(attemptCtx, Input{
			RequestID: fmt.Sprintf("%s_image_edit_options_%d", input.RequestID, index+1),
			ClientKey: internalKey, PublicModel: candidate, Body: body,
		})
		if requestErr != nil {
			attemptCancel()
			lastErr = requestErr
			if parseCtx.Err() != nil {
				break
			}
			continue
		}
		hints, readErr := readImageEditOptionResult(result, len(input.Candidates))
		attemptCancel()
		if readErr == nil {
			return hints, nil
		}
		lastErr = readErr
		if parseCtx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("图片编辑请求解析模型没有返回有效结果")
	}
	if s.logger != nil {
		s.logger.Warn("image_edit_option_parser_failed", "request_id", input.RequestID, "candidate_count", len(candidates), "error", lastErr)
	}
	return ImageEditOptionHints{}, lastErr
}

func imageEditOptionsNeeded(input ImageEditOptionParseInput) bool {
	return input.NeedPrompt || input.NeedImageSelection || input.NeedAspectRatio || input.NeedResolution || input.NeedSize
}

func buildImageEditOptionRequest(input ImageEditOptionParseInput, publicModel string) ([]byte, error) {
	wanted := make([]string, 0, 5)
	if input.NeedPrompt {
		wanted = append(wanted, "prompt")
	}
	if input.NeedImageSelection {
		wanted = append(wanted, "image_indexes")
		wanted = append(wanted, "multi_image")
	}
	if input.NeedAspectRatio {
		wanted = append(wanted, "aspect_ratio")
	}
	if input.NeedResolution {
		wanted = append(wanted, "resolution")
	}
	if input.NeedSize {
		wanted = append(wanted, "size")
	}
	descriptions := make([]string, 0, len(input.Candidates))
	for index, candidate := range input.Candidates {
		description := strings.TrimSpace(candidate.Description)
		if description == "" {
			description = "request image"
		}
		descriptions = append(descriptions, fmt.Sprintf("%d: %s", index, description))
	}
	system := `Extract missing image-edit request fields from the user's instruction. Return exactly one JSON object. Keys: prompt (the user's complete edit instruction verbatim in the original language, or null), image_indexes (zero-based indexes from the supplied candidate list, or []), multi_image (boolean), aspect_ratio (one of "auto","1:1","16:9","9:16","4:3","3:4","3:2","2:3","2:1","1:2","19.5:9","9:19.5","20:9","9:20" or null), resolution ("1k","2k" or null), size ("auto","1024x1024","1024x1536","1536x1024" or null). Never invent an image or URL. Prefer current-user images when the instruction says "this/current", "这张", or similar. Prefer the newest historical candidate for "previous/latest/last", "上一张", "刚才", or similar. Prefer the oldest historical candidate for "original/first/earliest", "最初", "第一张", or similar. Default to exactly one image index. Set multi_image=true and select multiple indexes only when the instruction explicitly asks to combine, merge, compare, or otherwise use multiple distinct images. Do not infer unspecified output settings.`
	return json.Marshal(map[string]any{
		"model": publicModel,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": fmt.Sprintf("Fields to extract: %s\nImage candidates:\n%s\nUser instruction:\n%s", strings.Join(wanted, ", "), strings.Join(descriptions, "\n"), input.Instruction)},
		},
		"stream": false, "temperature": 0, "max_completion_tokens": 256,
		"response_format": map[string]string{"type": "json_object"},
	})
}

func readImageEditOptionResult(result *Result, candidateCount int) (hints ImageEditOptionHints, err error) {
	if result == nil || result.Body == nil {
		return ImageEditOptionHints{}, errors.New("图片编辑请求解析模型返回空响应")
	}
	usage := Usage{}
	responseID := ""
	errorCode := "image_edit_option_parser_invalid_response"
	defer result.Body.Close()
	if result.Finalize != nil {
		defer func() { result.Finalize(usage, responseID, errorCode) }()
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		errorCode = "image_edit_option_parser_upstream_error"
		_, _ = io.Copy(io.Discard, io.LimitReader(result.Body, videoOptionMaxResponseBytes))
		return ImageEditOptionHints{}, fmt.Errorf("图片编辑请求解析模型返回 HTTP %d", result.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(result.Body, videoOptionMaxResponseBytes+1))
	if readErr != nil || len(body) > videoOptionMaxResponseBytes {
		return ImageEditOptionHints{}, errors.New("读取图片编辑请求解析响应失败")
	}
	var response videoOptionChatResponse
	if json.Unmarshal(body, &response) != nil || len(response.Choices) == 0 {
		return ImageEditOptionHints{}, errors.New("图片编辑请求解析响应格式无效")
	}
	responseID = response.ID
	usage = Usage{
		InputTokens: response.Usage.PromptTokens, CachedInputTokens: response.Usage.PromptTokensDetails.CachedTokens,
		OutputTokens: response.Usage.CompletionTokens, ReasoningTokens: response.Usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens: response.Usage.TotalTokens, CostInUSDTicks: response.Usage.CostInUSDTicks,
		NumSourcesUsed: response.Usage.NumSourcesUsed, NumServerSideToolsUsed: response.Usage.NumServerSideToolsUsed,
		ContextInputTokens: response.Usage.ContextDetails.InputTokens, ContextOutputTokens: response.Usage.ContextDetails.OutputTokens,
		ResponseModel: response.Model,
	}
	hints, err = decodeImageEditOptionHints(response.Choices[0].Message.Content, candidateCount)
	if err != nil {
		return ImageEditOptionHints{}, err
	}
	errorCode = ""
	return hints, nil
}

func decodeImageEditOptionHints(value string, candidateCount int) (ImageEditOptionHints, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") && strings.HasSuffix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) >= 3 {
			value = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	var hints ImageEditOptionHints
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&hints); err != nil {
		return ImageEditOptionHints{}, fmt.Errorf("解析图片编辑请求 JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ImageEditOptionHints{}, errors.New("图片编辑请求 JSON 包含额外内容")
	}
	seen := make(map[int]struct{}, len(hints.ImageIndexes))
	indexes := hints.ImageIndexes[:0]
	for _, index := range hints.ImageIndexes {
		if index < 0 || index >= candidateCount {
			return ImageEditOptionHints{}, errors.New("图片编辑请求 image_indexes 超出候选范围")
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		indexes = append(indexes, index)
	}
	hints.ImageIndexes = indexes
	if !hints.MultiImage && len(hints.ImageIndexes) > 1 {
		// The model's first index is its semantic choice. Preserve it so
		// phrases such as "the original/first image" can intentionally target
		// an older candidate instead of being forced to the newest one.
		hints.ImageIndexes = hints.ImageIndexes[:1]
	}
	if hints.Prompt != nil {
		trimmed := strings.TrimSpace(*hints.Prompt)
		hints.Prompt = &trimmed
	}
	if hints.AspectRatio != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*hints.AspectRatio))
		if !validImageEditAspectRatioHint(trimmed) {
			return ImageEditOptionHints{}, errors.New("图片编辑请求 aspect_ratio 无效")
		}
		*hints.AspectRatio = trimmed
	}
	if hints.Resolution != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*hints.Resolution))
		if trimmed != "1k" && trimmed != "2k" {
			return ImageEditOptionHints{}, errors.New("图片编辑请求 resolution 无效")
		}
		*hints.Resolution = trimmed
	}
	if hints.Size != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*hints.Size))
		if !validImageEditSizeHint(trimmed) {
			return ImageEditOptionHints{}, errors.New("图片编辑请求 size 无效")
		}
		*hints.Size = trimmed
	}
	return hints, nil
}

func validImageEditAspectRatioHint(value string) bool {
	switch value {
	case "auto", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2", "19.5:9", "9:19.5", "20:9", "9:20":
		return true
	default:
		return false
	}
}

func validImageEditSizeHint(value string) bool {
	switch value {
	case "auto", "1024x1024", "1024x1536", "1536x1024":
		return true
	default:
		return false
	}
}
