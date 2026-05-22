package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxImageEditBytes = 4 * 1024 * 1024
const maxImageReferenceBytes = 8 * 1024 * 1024
const maxImageResponseBytes = 64 * 1024 * 1024

const imageToolHTTPTimeout = 300 * time.Second

var (
	dataImagePattern     = regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=_-]+`)
	markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	looseImageURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
)

type responseToolContext struct {
	Context        context.Context
	ConversationID int64
	UserID         int64
	PublicBaseURL  string
}

type imageToolResultImage struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
}

type imageToolResult struct {
	OK      bool                   `json:"ok"`
	Tool    string                 `json:"tool"`
	Created int64                  `json:"created,omitempty"`
	Images  []imageToolResultImage `json:"images,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

func imageToolDefinitions() []map[string]interface{} {
	imageModelEnum := []string{"gpt-image-2", "gpt-image-1.5", "gpt-image-1", "gpt-4o-image-vip", "gpt-4o-image"}
	return []map[string]interface{}{
		{
			"type":        "function",
			"name":        "image_generate",
			"description": "Create a new image through the general image generation endpoint. Use for text-to-image, image-to-image/reference generation, and any image change when the user has not provided/requested a mask. Choose this when an uploaded or previously generated image URL is a reference, inspiration, base, style, pose, subject, or composition source, including 图生图, 以这张图为参考生成, 以这张图为基础生成, 参考图重新画, 换风格生成, style transfer, or use this image as reference. Pass workspace paths or generated image URLs in the image argument. If there is no mask, use this tool instead of image_edit.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "Required image description. Maximum 5000 characters.",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Optional Tuzi image generation model. Defaults to the admin setting.",
						"enum":        imageModelEnum,
					},
					"image": map[string]interface{}{
						"description": "Reference image(s) for image-to-image generation. Use this for uploaded images that should guide the new image. Accepts URL/base64/workspace path string or array. Workspace paths such as users/1/uploads/... are resolved to temporary external URLs by the backend.",
						"anyOf": []map[string]interface{}{
							{"type": "string"},
							{"type": "array", "items": map[string]interface{}{"type": "string"}},
						},
					},
					"size": map[string]interface{}{
						"type":        "string",
						"description": "Image size. Supports documented ratios plus pixel sizes that satisfy Tuzi gpt-image-2 constraints.",
					},
					"response_format": map[string]interface{}{
						"type":        "string",
						"description": "Return format.",
						"enum":        []string{"url", "b64_json"},
					},
					"quality": map[string]interface{}{
						"type":        "string",
						"description": "Image quality.",
						"enum":        []string{"auto", "low", "medium", "high"},
					},
					"style": map[string]interface{}{
						"type":        "string",
						"description": "Optional style string supported by compatible providers. Ignored for gpt-image-2 because Tuzi's upstream currently fails when style is sent to that model.",
					},
					"n": map[string]interface{}{
						"type":        "integer",
						"description": "Number of images to generate, 1 to 10.",
					},
					"user": map[string]interface{}{
						"type":        "string",
						"description": "Optional end-user identifier.",
					},
				},
				"required":             []string{"prompt"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        "image_edit",
			"description": "Mask-based image editing endpoint. Use this only when the user explicitly provides or asks to use a mask/mask image/mask_index/mask_path for a local edit, inpainting/outpainting, or masked region change. If no mask is provided or requested, do not call image_edit; call image_generate instead, even if the user says edit/change/modify. Do not use for 图生图, reference generation, style transfer, or generating a new image inspired by an upload.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "Required edit description. Maximum 1000 characters.",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Optional Tuzi image edit model. Defaults to the admin setting.",
						"enum":        imageModelEnum,
					},
					"image_index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the uploaded image attachment to edit. Defaults to 0.",
					},
					"image_path": map[string]interface{}{
						"type":        "string",
						"description": "Workspace file path of the image to edit, such as users/1/uploads/20260521/example.png. Prefer this when the user names a workspace file.",
					},
					"workspace_path": map[string]interface{}{
						"type":        "string",
						"description": "Alias for image_path.",
					},
					"mask_index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the uploaded mask image. Only use image_edit when a mask index/path is explicitly provided or requested.",
					},
					"mask_path": map[string]interface{}{
						"type":        "string",
						"description": "Workspace file path of a PNG mask image. Only use image_edit when a mask path/index is explicitly provided or requested.",
					},
					"size": map[string]interface{}{
						"type":        "string",
						"description": "Edit output size.",
						"enum":        []string{"1:1", "2:3", "3:2"},
					},
					"response_format": map[string]interface{}{
						"type":        "string",
						"description": "Return format.",
						"enum":        []string{"url", "b64_json"},
					},
					"n": map[string]interface{}{
						"type":        "integer",
						"description": "Number of images to generate, 1 to 10.",
					},
					"user": map[string]interface{}{
						"type":        "string",
						"description": "Optional end-user identifier.",
					},
				},
				"required":             []string{"prompt"},
				"additionalProperties": false,
			},
		},
	}
}

func (s *Server) executeImageGenerateTool(arguments string) string {
	return s.executeImageGenerateToolWithContext(arguments, responseToolContext{})
}

func (s *Server) executeImageGenerateToolWithContext(arguments string, toolCtx responseToolContext) string {
	var args struct {
		Model          string      `json:"model"`
		Prompt         string      `json:"prompt"`
		Image          interface{} `json:"image"`
		Size           string      `json:"size"`
		ResponseFormat string      `json:"response_format"`
		Quality        string      `json:"quality"`
		Style          string      `json:"style"`
		N              int         `json:"n"`
		User           string      `json:"user"`
	}
	if strings.TrimSpace(arguments) != "" {
		_ = json.Unmarshal([]byte(arguments), &args)
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return imageToolErrorJSON("image_generate", "prompt is required")
	}
	if len([]rune(prompt)) > 5000 {
		return imageToolErrorJSON("image_generate", "prompt must be at most 5000 characters")
	}
	apiKey := strings.TrimSpace(s.settingValue("image_tool_api_key"))
	if apiKey == "" {
		return imageToolErrorJSON("image_generate", "image_tool_api_key must be configured")
	}
	mode := s.imageToolMode()
	model := normalizeImageModel(args.Model, s.imageGenerateModel())
	if mode == imageToolModeResponses {
		model = s.imageResponsesModel()
	} else if mode == imageToolModeChatCompletions {
		model = s.imageChatModel()
	}
	size := normalizeImageGenerateSize(args.Size, s.imageGenerateSize())
	responseFormat := normalizeImageResponseFormat(args.ResponseFormat, s.imageResponseFormat())
	quality := normalizeImageQuality(args.Quality, s.imageQuality())
	n := clampImageCount(args.N)
	references, err := s.normalizeImageGenerateReferences(args.Image, toolCtx.UserID, toolCtx.PublicBaseURL)
	if err != nil {
		return imageToolErrorJSON("image_generate", err.Error())
	}
	request := imageGenerationRequest{
		Tool:           "image_generate",
		Mode:           mode,
		Model:          model,
		Prompt:         prompt,
		References:     references,
		Size:           size,
		ResponseFormat: responseFormat,
		Quality:        quality,
		Style:          args.Style,
		N:              n,
		User:           args.User,
	}
	switch mode {
	case imageToolModeResponses:
		body, err := s.runResponsesImageGeneration(apiKey, request)
		if err != nil {
			return imageToolErrorJSON("image_generate", err.Error())
		}
		return s.normalizeGeneratedImageResponseWithContext("image_generate", body, toolCtx)
	case imageToolModeChatCompletions:
		body, err := s.runChatCompletionsImageGeneration(apiKey, request)
		if err != nil {
			return imageToolErrorJSON("image_generate", err.Error())
		}
		return s.normalizeGeneratedImageResponseWithContext("image_generate", body, toolCtx)
	default:
		body, err := s.runImageAPIImageGeneration(apiKey, request)
		if err != nil {
			return imageToolErrorJSON("image_generate", err.Error())
		}
		return s.normalizeGeneratedImageResponseWithContext("image_generate", body, toolCtx)
	}
}

func (s *Server) executeImageEditTool(call responseToolCall, toolCtx responseToolContext) string {
	var args struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		ImageIndex     int    `json:"image_index"`
		ImagePath      string `json:"image_path"`
		WorkspacePath  string `json:"workspace_path"`
		MaskIndex      *int   `json:"mask_index"`
		MaskPath       string `json:"mask_path"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
		N              int    `json:"n"`
		User           string `json:"user"`
	}
	if strings.TrimSpace(call.Arguments) != "" {
		_ = json.Unmarshal([]byte(call.Arguments), &args)
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return imageToolErrorJSON("image_edit", "prompt is required")
	}
	if len([]rune(prompt)) > 1000 {
		return imageToolErrorJSON("image_edit", "prompt must be at most 1000 characters")
	}
	apiKey := strings.TrimSpace(s.settingValue("image_tool_api_key"))
	if apiKey == "" {
		return imageToolErrorJSON("image_edit", "image_tool_api_key must be configured")
	}
	mode := s.imageToolMode()
	imageAttachment, attachments, err := s.resolveImageEditAttachment(toolCtx.ConversationID, toolCtx.UserID, strings.TrimSpace(firstNonEmpty(args.ImagePath, args.WorkspacePath)), args.ImageIndex)
	if err != nil {
		return imageToolErrorJSON("image_edit", err.Error())
	}
	if mode != imageToolModeImageAPI {
		if strings.TrimSpace(args.MaskPath) != "" || args.MaskIndex != nil {
			return imageToolErrorJSON("image_edit", "mask-based image_edit is only available in Image API mode")
		}
		reference, err := s.normalizeImageEditReference(imageAttachment, toolCtx.UserID, toolCtx.PublicBaseURL)
		if err != nil {
			return imageToolErrorJSON("image_edit", err.Error())
		}
		model := s.imageResponsesModel()
		if mode == imageToolModeChatCompletions {
			model = s.imageChatModel()
		}
		request := imageGenerationRequest{
			Tool:           "image_edit",
			Mode:           mode,
			Model:          model,
			Prompt:         prompt,
			References:     []string{reference},
			Size:           normalizeImageGenerateSize(args.Size, s.imageGenerateSize()),
			ResponseFormat: normalizeImageResponseFormat(args.ResponseFormat, s.imageResponseFormat()),
			Quality:        s.imageQuality(),
			N:              clampImageCount(args.N),
			User:           args.User,
			Action:         "edit",
		}
		if mode == imageToolModeResponses {
			body, err := s.runResponsesImageGeneration(apiKey, request)
			if err != nil {
				return imageToolErrorJSON("image_edit", err.Error())
			}
			return s.normalizeGeneratedImageResponseWithContext("image_edit", body, toolCtx)
		}
		body, err := s.runChatCompletionsImageGeneration(apiKey, request)
		if err != nil {
			return imageToolErrorJSON("image_edit", err.Error())
		}
		return s.normalizeGeneratedImageResponseWithContext("image_edit", body, toolCtx)
	}
	imageFile, err := decodeAttachmentPNG(imageAttachment, "image", s.workspaceRoot())
	if err != nil {
		return imageToolErrorJSON("image_edit", err.Error())
	}
	var maskFile *imageEditFile
	if strings.TrimSpace(args.MaskPath) != "" {
		maskAttachment := attachment{Name: "mask.png", Type: "image/png", WorkspacePath: strings.TrimSpace(args.MaskPath)}
		item, err := decodeAttachmentPNG(maskAttachment, "mask", s.workspaceRoot())
		if err != nil {
			return imageToolErrorJSON("image_edit", err.Error())
		}
		if item.Width != imageFile.Width || item.Height != imageFile.Height {
			return imageToolErrorJSON("image_edit", "mask dimensions must match image dimensions")
		}
		maskFile = &item
	} else if args.MaskIndex != nil {
		if *args.MaskIndex < 0 || *args.MaskIndex >= len(attachments) {
			return imageToolErrorJSON("image_edit", "mask_index is out of range")
		}
		item, err := decodeAttachmentPNG(attachments[*args.MaskIndex], "mask", s.workspaceRoot())
		if err != nil {
			return imageToolErrorJSON("image_edit", err.Error())
		}
		if item.Width != imageFile.Width || item.Height != imageFile.Height {
			return imageToolErrorJSON("image_edit", "mask dimensions must match image dimensions")
		}
		maskFile = &item
	}
	if imageFile.Width != imageFile.Height {
		return imageToolErrorJSON("image_edit", "image must be square")
	}
	model := normalizeImageModel(args.Model, s.imageEditModel())
	size := normalizeImageEditSize(args.Size, s.imageEditSize())
	responseFormat := normalizeImageResponseFormat(args.ResponseFormat, s.imageResponseFormat())
	n := clampImageCount(args.N)
	fields := map[string]string{
		"model":           model,
		"prompt":          prompt,
		"n":               strconv.Itoa(n),
		"size":            size,
		"response_format": responseFormat,
	}
	if strings.TrimSpace(args.User) != "" {
		fields["user"] = strings.TrimSpace(args.User)
	}
	body, err := s.runImageMultipart("/v1/images/edits", apiKey, fields, imageFile, maskFile)
	if err != nil {
		return imageToolErrorJSON("image_edit", err.Error())
	}
	return s.normalizeGeneratedImageResponseWithContext("image_edit", body, toolCtx)
}

type imageGenerationRequest struct {
	Tool           string
	Mode           string
	Model          string
	Prompt         string
	References     []string
	Size           string
	ResponseFormat string
	Quality        string
	Style          string
	N              int
	User           string
	Action         string
}

func (s *Server) runImageAPIImageGeneration(apiKey string, request imageGenerationRequest) ([]byte, error) {
	requestBody := map[string]interface{}{
		"model":           request.Model,
		"prompt":          request.Prompt,
		"n":               request.N,
		"size":            request.Size,
		"response_format": request.ResponseFormat,
	}
	if request.Quality != "" {
		requestBody["quality"] = request.Quality
	}
	if shouldSendImageGenerateStyle(request.Model, request.Style) {
		requestBody["style"] = strings.TrimSpace(request.Style)
	}
	if strings.TrimSpace(request.User) != "" {
		requestBody["user"] = strings.TrimSpace(request.User)
	}
	if len(request.References) == 1 {
		requestBody["image"] = request.References[0]
	} else if len(request.References) > 1 {
		requestBody["image"] = request.References
	}
	return s.runImageJSON("/v1/images/generations", apiKey, requestBody)
}

func (s *Server) runResponsesImageGeneration(apiKey string, request imageGenerationRequest) ([]byte, error) {
	content := []map[string]interface{}{
		{"type": "input_text", "text": request.Prompt},
	}
	for _, reference := range request.References {
		content = append(content, map[string]interface{}{
			"type":      "input_image",
			"image_url": reference,
		})
	}
	tool := map[string]interface{}{
		"type":           "image_generation",
		"partial_images": 1,
	}
	if size := normalizeResponsesImageSize(request.Size); size != "" {
		tool["size"] = size
	}
	if request.Quality != "" {
		tool["quality"] = request.Quality
	}
	if outputFormat := normalizeResponsesImageOutputFormat(request.ResponseFormat); outputFormat != "" {
		tool["output_format"] = outputFormat
	}
	if action := normalizeResponsesImageAction(request.Action); action != "" {
		tool["action"] = action
	}
	payload := map[string]interface{}{
		"model": request.Model,
		"input": []map[string]interface{}{
			{
				"role":    "user",
				"content": content,
			},
		},
		"tools": []map[string]interface{}{tool},
	}
	payload["stream"] = true
	return s.runImageStreamingJSON("/responses", apiKey, payload, imageToolModeResponses)
}

func (s *Server) runChatCompletionsImageGeneration(apiKey string, request imageGenerationRequest) ([]byte, error) {
	text := request.Prompt
	if request.Tool == "image_edit" || request.Action == "edit" {
		text = "Edit or regenerate the provided image according to this instruction. Return the generated image as a URL or base64 image only if your API supports image output.\n\n" + request.Prompt
	} else {
		text = "Generate an image from this instruction. Return the generated image as a URL or base64 image only if your API supports image output.\n\n" + request.Prompt
	}
	content := []map[string]interface{}{
		{"type": "text", "text": text},
	}
	for _, reference := range request.References {
		content = append(content, map[string]interface{}{
			"type": "image_url",
			"image_url": map[string]interface{}{
				"url": reference,
			},
		})
	}
	payload := map[string]interface{}{
		"model": request.Model,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": content,
			},
		},
	}
	if request.N > 1 {
		payload["n"] = request.N
	}
	if strings.TrimSpace(request.User) != "" {
		payload["user"] = strings.TrimSpace(request.User)
	}
	payload["stream"] = true
	return s.runImageStreamingJSON("/chat/completions", apiKey, payload, imageToolModeChatCompletions)
}

func (s *Server) runImageJSON(path, apiKey string, payload map[string]interface{}) ([]byte, error) {
	raw, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(s.imageToolBaseURL(), "/") + path
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: imageToolHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (s *Server) runImageStreamingJSON(path, apiKey string, payload map[string]interface{}, mode string) ([]byte, error) {
	raw, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(s.imageToolBaseURL(), "/") + path
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: imageToolHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxImageResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(limited)
		return nil, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return readImageStreamingJSON(mode, limited)
	}
	body, _ := io.ReadAll(limited)
	if looksLikeImageEventStream(body) {
		return readImageStreamingJSON(mode, bytes.NewReader(body))
	}
	return body, nil
}

func readImageStreamingJSON(mode string, body io.Reader) ([]byte, error) {
	reader := bufio.NewReader(body)
	eventName := ""
	var payload strings.Builder
	aggregate := imageStreamAggregate{
		Mode:         mode,
		StreamEvents: make([]map[string]interface{}, 0),
		Output:       make([]interface{}, 0),
	}
	flush := func() bool {
		name := strings.TrimSpace(eventName)
		data := strings.TrimSpace(payload.String())
		eventName = ""
		payload.Reset()
		if data == "" {
			return false
		}
		if data == "[DONE]" {
			return true
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(data), &decoded); err != nil {
			aggregate.StreamEvents = append(aggregate.StreamEvents, map[string]interface{}{
				"event": name,
				"raw":   data,
			})
			return false
		}
		event := map[string]interface{}{"data": decoded}
		if name != "" {
			event["event"] = name
		}
		aggregate.StreamEvents = append(aggregate.StreamEvents, event)
		appendImageStreamOutputItems(decoded, &aggregate.Output)
		appendImageStreamChatContent(decoded, &aggregate.Content)
		return false
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(trimmed) == "" {
			if flush() {
				break
			}
		} else if strings.HasPrefix(trimmed, "event:") {
			if payload.Len() > 0 && flush() {
				break
			}
			eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
		} else if strings.HasPrefix(trimmed, "data:") {
			if payload.Len() > 0 {
				payload.WriteByte('\n')
			}
			payload.WriteString(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		}
		if err == io.EOF {
			break
		}
	}
	flush()
	return aggregate.marshal()
}

type imageStreamAggregate struct {
	Mode         string
	StreamEvents []map[string]interface{}
	Output       []interface{}
	Content      strings.Builder
}

func (a *imageStreamAggregate) marshal() ([]byte, error) {
	payload := map[string]interface{}{
		"stream_events": a.StreamEvents,
	}
	if len(a.Output) > 0 {
		payload["output"] = a.Output
	}
	if text := a.Content.String(); text != "" {
		payload["choices"] = []map[string]interface{}{
			{
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": text,
				},
			},
		}
	}
	return json.Marshal(payload)
}

func looksLikeImageEventStream(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\nevent:")) || bytes.Contains(trimmed, []byte("\ndata:"))
}

func appendImageStreamOutputItems(value interface{}, output *[]interface{}) {
	obj, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	if response, ok := obj["response"].(map[string]interface{}); ok {
		appendImageStreamOutputItems(response, output)
	}
	if items, ok := obj["output"].([]interface{}); ok {
		*output = append(*output, items...)
	}
	if item, ok := obj["item"].(map[string]interface{}); ok {
		*output = append(*output, item)
	}
	if obj["type"] == "image_generation_call" {
		*output = append(*output, obj)
	}
}

func appendImageStreamChatContent(value interface{}, full *strings.Builder) {
	obj, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	choices, ok := obj["choices"].([]interface{})
	if !ok {
		return
	}
	for _, choiceValue := range choices {
		choice, ok := choiceValue.(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"delta", "message"} {
			part, ok := choice[key].(map[string]interface{})
			if !ok {
				continue
			}
			appendImageStreamContentValue(part["content"], full)
		}
	}
}

func appendImageStreamContentValue(value interface{}, full *strings.Builder) {
	switch typed := value.(type) {
	case string:
		full.WriteString(typed)
	case []interface{}:
		for _, item := range typed {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := obj["text"].(string); ok {
				full.WriteString(text)
			}
		}
	}
}

func (s *Server) runImageMultipart(path, apiKey string, fields map[string]string, imageFile imageEditFile, maskFile *imageEditFile) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	if err := writeMultipartPNG(writer, "image", imageFile); err != nil {
		return nil, err
	}
	if maskFile != nil {
		if err := writeMultipartPNG(writer, "mask", *maskFile); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(s.imageToolBaseURL(), "/") + path
	req, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: imageToolHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func writeMultipartPNG(writer *multipart.Writer, field string, file imageEditFile) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, escapeQuotes(file.Name)))
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(file.Data)
	return err
}

type imageEditFile struct {
	Name   string
	Data   []byte
	Width  int
	Height int
}

func decodeAttachmentPNG(item attachment, label, workspaceRoot string) (imageEditFile, error) {
	var data []byte
	var err error
	if strings.TrimSpace(item.WorkspacePath) != "" {
		data, err = readWorkspaceFileBytes(workspaceRoot, item.WorkspacePath)
		if err != nil {
			return imageEditFile{}, fmt.Errorf("%s workspace file cannot be read: %w", label, err)
		}
	} else {
		if strings.TrimSpace(item.Content) == "" {
			return imageEditFile{}, fmt.Errorf("%s attachment has no image data", label)
		}
		data, err = decodeDataURLOrBase64(item.Content)
		if err != nil {
			return imageEditFile{}, fmt.Errorf("%s attachment is not valid base64 image data", label)
		}
	}
	if len(data) > maxImageEditBytes {
		return imageEditFile{}, fmt.Errorf("%s image must be smaller than 4MB", label)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return imageEditFile{}, fmt.Errorf("%s image must be a valid PNG", label)
	}
	if format != "png" {
		return imageEditFile{}, fmt.Errorf("%s image must be PNG", label)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return imageEditFile{}, fmt.Errorf("%s image dimensions are invalid", label)
	}
	if cfg.Width != cfg.Height {
		return imageEditFile{}, fmt.Errorf("%s image must be square", label)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return imageEditFile{}, fmt.Errorf("%s image must be a valid PNG", label)
	}
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = label + ".png"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".png") {
		name += ".png"
	}
	return imageEditFile{Name: name, Data: data, Width: cfg.Width, Height: cfg.Height}, nil
}

func readWorkspaceFileBytes(workspaceRoot, relPath string) ([]byte, error) {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if relPath == "." || relPath == "" || strings.HasPrefix(relPath, "../") || strings.HasPrefix(relPath, "/") {
		return nil, fmt.Errorf("invalid workspace path")
	}
	fullPath := filepath.Join(workspaceRoot, filepath.FromSlash(relPath))
	root := filepath.Clean(workspaceRoot)
	cleanFull := filepath.Clean(fullPath)
	if cleanFull != root && !strings.HasPrefix(cleanFull, root+string(os.PathSeparator)) {
		return nil, fmt.Errorf("invalid workspace path")
	}
	return os.ReadFile(cleanFull)
}

func (s *Server) resolveImageEditAttachment(conversationID, userID int64, workspacePath string, imageIndex int) (attachment, []attachment, error) {
	if workspacePath != "" {
		if !isWorkspacePathForUser(workspacePath, userID) {
			return attachment{}, nil, fmt.Errorf("workspace path does not belong to current user")
		}
		return attachment{Name: filepath.Base(workspacePath), Type: "image/png", WorkspacePath: workspacePath}, nil, nil
	}
	attachments, err := s.latestImageAttachments(conversationID, userID)
	if err != nil {
		return attachment{}, nil, err
	}
	if len(attachments) == 0 {
		return attachment{}, attachments, fmt.Errorf("no uploaded image attachment is available")
	}
	if imageIndex < 0 || imageIndex >= len(attachments) {
		return attachment{}, attachments, fmt.Errorf("image_index is out of range")
	}
	return attachments[imageIndex], attachments, nil
}

func (s *Server) latestImageAttachments(conversationID, userID int64) ([]attachment, error) {
	rows, err := s.store.DB.Query(`
		SELECT attachments
		FROM messages
		WHERE conversation_id=? AND user_id=? AND role='user' AND deleted_at IS NULL
		ORDER BY sort_order DESC, id DESC
		LIMIT 12
	`, conversationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var images []attachment
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var items []attachment
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			continue
		}
		for _, item := range items {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Type)), "image/") && (strings.TrimSpace(item.Content) != "" || strings.TrimSpace(item.WorkspacePath) != "") {
				images = append(images, item)
			}
		}
		if len(images) > 0 {
			return images, nil
		}
	}
	return images, nil
}

func isWorkspacePathForUser(path string, userID int64) bool {
	if userID <= 0 {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(filepath.Clean(strings.TrimSpace(path))), fmt.Sprintf("users/%d/", userID))
}

func (s *Server) normalizeGeneratedImageResponseWithContext(tool string, body []byte, toolCtx responseToolContext) string {
	return normalizeGeneratedImageResponsePostprocess(tool, body, func(images []imageToolResultImage) []imageToolResultImage {
		return s.persistInlineImageResults(tool, images, toolCtx)
	})
}

func normalizeGeneratedImageResponse(tool string, body []byte) string {
	return normalizeGeneratedImageResponsePostprocess(tool, body, nil)
}

func normalizeGeneratedImageResponsePostprocess(tool string, body []byte, postprocess func([]imageToolResultImage) []imageToolResultImage) string {
	var raw struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return imageToolErrorJSON(tool, "invalid image response: "+err.Error())
	}
	images := make([]imageToolResultImage, 0, len(raw.Data))
	for _, item := range raw.Data {
		urlValue := strings.TrimSpace(item.URL)
		b64Value := strings.TrimSpace(item.B64JSON)
		if urlValue == "" && b64Value == "" {
			continue
		}
		images = append(images, imageToolResultImage{URL: urlValue, B64JSON: b64Value})
	}
	if len(images) == 0 {
		var payload interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			return imageToolErrorJSON(tool, "invalid image response: "+err.Error())
		}
		images = collectImageResults(payload)
	}
	if len(images) == 0 {
		if looksLikeChatCompletionResponse(body) {
			return imageToolErrorJSON(tool, "chat_completions image mode did not return images; official Chat Completions has no native image-generation output format")
		}
		return imageToolErrorJSON(tool, "image response did not contain images")
	}
	if postprocess != nil {
		images = postprocess(images)
	}
	return mustJSONString(imageToolResult{OK: true, Tool: tool, Created: raw.Created, Images: images})
}

func collectImageResults(value interface{}) []imageToolResultImage {
	images := make([]imageToolResultImage, 0)
	seen := map[string]bool{}
	var visit func(interface{}, string)
	visit = func(current interface{}, key string) {
		switch typed := current.(type) {
		case map[string]interface{}:
			for childKey, childValue := range typed {
				visit(childValue, strings.ToLower(childKey))
			}
		case []interface{}:
			for _, item := range typed {
				visit(item, key)
			}
		case string:
			for _, image := range imageResultsFromString(typed, key) {
				value := image.URL
				if value == "" {
					value = image.B64JSON
				}
				if value == "" || seen[value] {
					continue
				}
				seen[value] = true
				images = append(images, image)
			}
		}
	}
	visit(value, "")
	return images
}

func imageResultsFromString(value, key string) []imageToolResultImage {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	images := make([]imageToolResultImage, 0)
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(strings.ToLower(trimmed), "data:image/") {
		images = append(images, imageToolResultImage{B64JSON: trimmed})
	}
	for _, match := range dataImagePattern.FindAllString(trimmed, -1) {
		images = append(images, imageToolResultImage{B64JSON: match})
	}
	if isImageBase64Field(lowerKey, trimmed) {
		images = append(images, imageToolResultImage{B64JSON: trimmed})
	}
	if isImageURLField(lowerKey, trimmed) {
		images = append(images, imageToolResultImage{URL: strings.TrimRight(trimmed, ".,);]\"'")})
	}
	for _, match := range markdownImagePattern.FindAllStringSubmatch(trimmed, -1) {
		if len(match) > 1 {
			urlValue := strings.TrimRight(strings.TrimSpace(match[1]), ".,);]\"'")
			if urlValue != "" {
				images = append(images, imageToolResultImage{URL: urlValue})
			}
		}
	}
	for _, match := range looseImageURLPattern.FindAllString(trimmed, -1) {
		urlValue := strings.TrimRight(match, ".,);]\"'")
		if isLikelyImageURL(urlValue) {
			images = append(images, imageToolResultImage{URL: urlValue})
		}
	}
	return images
}

func isImageBase64Field(key, value string) bool {
	if key != "b64_json" && key != "b64" && key != "base64" && key != "image_base64" && key != "result" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(value), "data:image/") {
		return true
	}
	if len(value) < 80 {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' || r == '-' || r == '_' || r == '\n' || r == '\r' {
			continue
		}
		return false
	}
	return true
}

func isImageURLField(key, value string) bool {
	switch key {
	case "url", "image_url", "image", "uri":
		return strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://")
	default:
		return false
	}
}

func isLikelyImageURL(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "format=image") || strings.Contains(lower, "response-content-type=image") {
		return true
	}
	for _, suffix := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".avif"} {
		if strings.Contains(lower, suffix) {
			return true
		}
	}
	return false
}

func looksLikeChatCompletionResponse(body []byte) bool {
	var raw struct {
		Choices []interface{} `json:"choices"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	return len(raw.Choices) > 0
}

func (s *Server) persistInlineImageResults(tool string, images []imageToolResultImage, toolCtx responseToolContext) []imageToolResultImage {
	if toolCtx.UserID <= 0 || s == nil {
		return images
	}
	next := make([]imageToolResultImage, 0, len(images))
	for index, image := range images {
		item := image
		if strings.TrimSpace(item.WorkspacePath) == "" {
			data, ext, err := imageResultBytes(item)
			if err == nil && len(data) > 0 {
				if relPath, err := s.saveGeneratedImageToWorkspace(toolCtx.UserID, tool, index, data, ext); err == nil {
					item.WorkspacePath = relPath
					if strings.TrimSpace(item.URL) == "" {
						item.URL = "/api/workspace/files/" + relPath
					}
				}
			}
		}
		next = append(next, item)
	}
	return next
}

func imageResultBytes(image imageToolResultImage) ([]byte, string, error) {
	if data, mimeType, ok, err := decodeImageDataURL(strings.TrimSpace(image.URL)); ok || err != nil {
		if err != nil {
			return nil, "", err
		}
		return data, imageExtensionForMIME(mimeType), nil
	}
	if data, mimeType, ok, err := decodeImageDataURL(strings.TrimSpace(image.B64JSON)); ok || err != nil {
		if err != nil {
			return nil, "", err
		}
		return data, imageExtensionForMIME(mimeType), nil
	}
	b64 := strings.TrimSpace(image.B64JSON)
	if b64 == "" {
		return nil, "", fmt.Errorf("no inline image data")
	}
	data, err := decodeDataURLOrBase64(b64)
	if err != nil {
		return nil, "", err
	}
	return data, ".png", nil
}

func decodeImageDataURL(value string) ([]byte, string, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "data:image/") {
		return nil, "", false, nil
	}
	comma := strings.Index(value, ",")
	if comma < 0 {
		return nil, "", true, fmt.Errorf("invalid image data URL")
	}
	header := strings.ToLower(strings.TrimSpace(value[:comma]))
	mimeType := strings.TrimPrefix(strings.Split(strings.TrimPrefix(header, "data:"), ";")[0], " ")
	data, err := decodeDataURLOrBase64(value)
	if err != nil {
		return nil, "", true, err
	}
	return data, mimeType, true, nil
}

func imageExtensionForMIME(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	default:
		return ".png"
	}
}

func (s *Server) saveGeneratedImageToWorkspace(userID int64, tool string, index int, data []byte, ext string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty image data")
	}
	if ext = strings.TrimSpace(ext); ext == "" || strings.ContainsAny(ext, `/\`) {
		ext = ".png"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	now := time.Now().UTC()
	name := fmt.Sprintf("%d-%s-%d%s", now.UnixNano(), safeWorkspaceFileName(tool, index), index+1, ext)
	relPath := filepath.ToSlash(filepath.Join("users", fmt.Sprintf("%d", userID), "generated", now.Format("20060102"), name))
	fullPath := filepath.Join(s.workspaceRoot(), filepath.FromSlash(relPath))
	root := filepath.Clean(s.workspaceRoot())
	cleanFull := filepath.Clean(fullPath)
	if cleanFull != root && !strings.HasPrefix(cleanFull, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid workspace path")
	}
	if err := os.MkdirAll(filepath.Dir(cleanFull), 0750); err != nil {
		return "", err
	}
	if err := os.WriteFile(cleanFull, data, 0640); err != nil {
		return "", err
	}
	return relPath, nil
}

func imageToolErrorJSON(tool, message string) string {
	return mustJSONString(imageToolResult{OK: false, Tool: tool, Error: strings.TrimSpace(message)})
}

func (s *Server) normalizeImageGenerateInput(value interface{}, userID int64, publicBaseURL string) (interface{}, bool, error) {
	switch typed := value.(type) {
	case nil:
		return nil, false, nil
	case string:
		item, err := s.normalizeImageGenerateReference(typed, userID, publicBaseURL)
		if err != nil {
			return nil, false, err
		}
		if item == "" {
			return nil, false, nil
		}
		return item, true, nil
	case []interface{}:
		items := make([]string, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(string)
			if !ok {
				return nil, false, fmt.Errorf("image array must contain only strings")
			}
			var err error
			item, err = s.normalizeImageGenerateReference(item, userID, publicBaseURL)
			if err != nil {
				return nil, false, err
			}
			if item != "" {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			return nil, false, nil
		}
		return items, true, nil
	case []string:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			var err error
			item, err = s.normalizeImageGenerateReference(item, userID, publicBaseURL)
			if err != nil {
				return nil, false, err
			}
			if item != "" {
				items = append(items, item)
			}
		}
		if len(items) == 0 {
			return nil, false, nil
		}
		return items, true, nil
	default:
		return nil, false, fmt.Errorf("image must be a string or an array of strings")
	}
}

func (s *Server) normalizeImageGenerateReferences(value interface{}, userID int64, publicBaseURL string) ([]string, error) {
	imageValue, ok, err := s.normalizeImageGenerateInput(value, userID, publicBaseURL)
	if err != nil || !ok {
		return nil, err
	}
	switch typed := imageValue.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		return []string{typed}, nil
	case []string:
		return typed, nil
	case []interface{}:
		items := make([]string, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("image array must contain only strings")
			}
			if strings.TrimSpace(item) != "" {
				items = append(items, item)
			}
		}
		return items, nil
	default:
		return nil, fmt.Errorf("image must be a string or an array of strings")
	}
}

func (s *Server) normalizeImageGenerateReference(value string, userID int64, publicBaseURL string) (string, error) {
	item := strings.TrimSpace(value)
	if item == "" {
		return "", nil
	}
	if isWorkspacePathForUser(item, userID) {
		fullPath := filepath.Join(s.workspaceRoot(), filepath.FromSlash(filepath.ToSlash(filepath.Clean(item))))
		if _, err := os.Stat(fullPath); err != nil {
			return "", fmt.Errorf("workspace image cannot be read: %w", err)
		}
		signedURL, err := s.publicWorkspaceFileURL(item, userID, publicBaseURL, 30*time.Minute)
		if err != nil {
			return "", err
		}
		return signedURL, nil
	}
	return item, nil
}

func (s *Server) normalizeImageEditReference(item attachment, userID int64, publicBaseURL string) (string, error) {
	if strings.TrimSpace(item.WorkspacePath) != "" {
		return s.normalizeImageGenerateReference(item.WorkspacePath, userID, publicBaseURL)
	}
	if strings.TrimSpace(item.URL) != "" {
		return strings.TrimSpace(item.URL), nil
	}
	if strings.TrimSpace(item.Content) == "" {
		return "", fmt.Errorf("image attachment has no image data")
	}
	content := strings.TrimSpace(item.Content)
	if strings.HasPrefix(strings.ToLower(content), "data:") {
		return content, nil
	}
	data, err := decodeDataURLOrBase64(content)
	if err != nil {
		return "", fmt.Errorf("image attachment is not valid base64 image data")
	}
	if len(data) > maxImageReferenceBytes {
		return "", fmt.Errorf("inline image reference must be smaller than 8MB")
	}
	mimeType := strings.TrimSpace(item.Type)
	if mimeType == "" {
		mimeType = "image/png"
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		mimeType = "image/png"
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)), nil
}

func shouldSendImageGenerateStyle(model, style string) bool {
	if strings.TrimSpace(style) == "" {
		return false
	}
	return strings.TrimSpace(model) != "gpt-image-2"
}

func decodeDataURLOrBase64(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if comma := strings.Index(trimmed, ","); strings.HasPrefix(strings.ToLower(trimmed), "data:") && comma >= 0 {
		trimmed = trimmed[comma+1:]
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return nil, fmt.Errorf("empty base64")
	}
	if data, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(trimmed)
}

func (s *Server) imageToolBaseURL() string {
	return normalizeImageToolBaseURL(s.settingValue("image_tool_base_url"))
}

func (s *Server) imageToolMode() string {
	return normalizeImageToolMode(s.settingValue("image_tool_mode"))
}

func (s *Server) imageGenerateModel() string {
	return normalizeImageModel(s.settingValue("image_generate_model"), defaultImageGenerateModel)
}

func (s *Server) imageEditModel() string {
	return normalizeImageModel(s.settingValue("image_edit_model"), defaultImageEditModel)
}

func (s *Server) imageResponsesModel() string {
	return normalizeImageMainlineModel(s.settingValue("image_responses_model"), defaultImageResponsesModel)
}

func (s *Server) imageChatModel() string {
	return normalizeImageMainlineModel(s.settingValue("image_chat_model"), defaultImageChatModel)
}

func (s *Server) imageGenerateSize() string {
	return normalizeImageGenerateSize(s.settingValue("image_default_size"), defaultImageGenerateSize)
}

func (s *Server) imageEditSize() string {
	return normalizeImageEditSize(s.settingValue("image_edit_size"), defaultImageEditSize)
}

func (s *Server) imageQuality() string {
	return normalizeImageQuality(s.settingValue("image_default_quality"), defaultImageToolQuality)
}

func (s *Server) imageResponseFormat() string {
	return normalizeImageResponseFormat(s.settingValue("image_response_format"), defaultImageToolResponseFormat)
}

func normalizeImageToolBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultImageToolBaseURL
	}
	if _, err := url.ParseRequestURI(value); err != nil {
		return defaultImageToolBaseURL
	}
	return strings.TrimRight(value, "/")
}

func normalizeImageToolMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case imageToolModeResponses:
		return imageToolModeResponses
	case imageToolModeChatCompletions:
		return imageToolModeChatCompletions
	default:
		return imageToolModeImageAPI
	}
}

func normalizeImageModel(value, fallback string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "gpt-image-2", "gpt-image-1.5", "gpt-image-1", "gpt-4o-image-vip", "gpt-4o-image":
		return value
	default:
		if fallback == "" || fallback == value {
			return defaultImageGenerateModel
		}
		return normalizeImageModel(fallback, defaultImageGenerateModel)
	}
}

func normalizeImageMainlineModel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return defaultImageResponsesModel
}

func normalizeResponsesImageSize(value string) string {
	switch strings.TrimSpace(value) {
	case "1024x1024", "1024x1536", "1536x1024", "auto":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeResponsesImageOutputFormat(value string) string {
	switch strings.TrimSpace(value) {
	case "png", "jpeg", "webp":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeResponsesImageAction(value string) string {
	switch strings.TrimSpace(value) {
	case "edit", "generate":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizeImageGenerateSize(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if isAllowedImageRatio(value) || isValidPixelImageSize(value) || value == "auto" {
		return value
	}
	return defaultImageGenerateSize
}

func normalizeImageEditSize(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	switch value {
	case "1:1", "2:3", "3:2":
		return value
	default:
		return defaultImageEditSize
	}
}

func normalizeImageQuality(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	switch value {
	case "auto", "low", "medium", "high":
		return value
	default:
		return defaultImageToolQuality
	}
}

func normalizeImageResponseFormat(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	switch value {
	case "url", "b64_json":
		return value
	default:
		return defaultImageToolResponseFormat
	}
}

func clampImageCount(value int) int {
	if value <= 0 {
		return 1
	}
	if value > 10 {
		return 10
	}
	return value
}

func isAllowedImageRatio(value string) bool {
	switch value {
	case "1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9":
		return true
	default:
		return false
	}
}

func isValidPixelImageSize(value string) bool {
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return false
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return false
	}
	if width%16 != 0 || height%16 != 0 {
		return false
	}
	if width > 3840 || height > 3840 {
		return false
	}
	longSide, shortSide := width, height
	if shortSide > longSide {
		longSide, shortSide = shortSide, longSide
	}
	if shortSide == 0 || float64(longSide)/float64(shortSide) > 3 {
		return false
	}
	pixels := width * height
	return pixels >= 655360 && pixels <= 8294400
}

func escapeQuotes(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}
