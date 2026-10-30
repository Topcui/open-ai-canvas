// 平台开放接口：站外工具用 API Key 调用站内模型。
// 本文件只做两件事：按模型名找到唯一渠道，以及把生图、生视频请求转成创作页同一种任务输入。
// 文本转发、任务执行、计费结算都复用已有的系统渠道转发与任务链路，不另起一套。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/assets"
	"yingce/backend/internal/model"
)

const (
	PlatformEndpointMessages        = "/v1/messages"
	PlatformEndpointChatCompletions = "/v1/chat/completions"
	PlatformEndpointTasks           = "/v1/tasks"

	platformAPIMetadataSource   = "platform-api"
	maxPlatformTaskReferences   = 16
	maxPlatformReferenceURLSize = 2048
)

// PlatformModel 同时满足 OpenAI 与 Anthropic 模型列表的字段；Endpoint 告诉调用方该走哪个接口。
type PlatformModel struct {
	ID          string                        `json:"id"`
	Object      string                        `json:"object"`
	Type        string                        `json:"type"`
	DisplayName string                        `json:"display_name"`
	OwnedBy     string                        `json:"owned_by"`
	Created     int64                         `json:"created"`
	Capability  string                        `json:"capability"`
	Endpoint    string                        `json:"endpoint"`
	PriceTiers  []PublicChannelModelPriceTier `json:"price_tiers"`
}

// PlatformModelTarget 是模型名解析出的唯一执行渠道。
type PlatformModelTarget struct {
	Channel *model.ModelChannel
	Model   PublicChannelModel
}

// PlatformTaskRequest 是开放接口的生图、生视频请求。参考素材只收公网 http(s) 地址。
type PlatformTaskRequest struct {
	Model         string   `json:"model"`
	Prompt        string   `json:"prompt"`
	Size          string   `json:"size"`
	Resolution    string   `json:"resolution"`
	Quality       string   `json:"quality"`
	Seconds       int      `json:"seconds"`
	GenerateAudio *bool    `json:"generate_audio"`
	Images        []string `json:"images"`
	FirstFrame    string   `json:"first_frame"`
	LastFrame     string   `json:"last_frame"`
}

type PlatformTaskOutput struct {
	Type       string     `json:"type"`
	ResourceID string     `json:"resource_id"`
	URL        string     `json:"url"`
	MimeType   string     `json:"mime_type,omitempty"`
	Width      int        `json:"width,omitempty"`
	Height     int        `json:"height,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type PlatformTask struct {
	ID          string               `json:"id"`
	Object      string               `json:"object"`
	Type        string               `json:"type"`
	Model       string               `json:"model"`
	Status      string               `json:"status"`
	Progress    int                  `json:"progress"`
	Error       string               `json:"error,omitempty"`
	Outputs     []PlatformTaskOutput `json:"outputs"`
	CreatedAt   time.Time            `json:"created_at"`
	CompletedAt *time.Time           `json:"completed_at,omitempty"`
}

type PlatformBalance struct {
	Object                string  `json:"object"`
	AvailableCredits      float64 `json:"available_credits"`
	ReservedCredits       float64 `json:"reserved_credits"`
	AvailableMicrocredits int64   `json:"available_microcredits"`
	ReservedMicrocredits  int64   `json:"reserved_microcredits"`
}

// platformEndpointForModel 决定一个目录模型能走哪个开放接口；返回空表示开放接口暂不支持。
// 文本只开放两种标准协议，两种格式之间不做转换。
func platformEndpointForModel(item PublicChannelModel) string {
	switch normalizeCapability(item.Capability) {
	case "text":
		switch item.Protocol {
		case model.ChannelInterfaceClaudeAPI:
			return PlatformEndpointMessages
		case model.ChannelInterfaceChatCompletion:
			return PlatformEndpointChatCompletions
		}
	case "image", "video":
		return PlatformEndpointTasks
	}
	return ""
}

func (s *Service) platformCatalog(user *model.User) (*ModelCatalogResponse, error) {
	if user == nil {
		return nil, Unauthorized("API Key 无效")
	}
	return s.ModelCatalogForUser(user, nil)
}

// PlatformModels 列出这个用户能通过开放接口调用的模型。同名模型只列一次，调用时再报重名。
func (s *Service) PlatformModels(user *model.User) ([]PlatformModel, error) {
	catalog, err := s.platformCatalog(user)
	if err != nil {
		return nil, err
	}
	models := []PlatformModel{}
	seen := map[string]bool{}
	for _, channel := range catalog.Channels {
		for _, item := range channel.Models {
			endpoint := platformEndpointForModel(item)
			if !item.Available || endpoint == "" || seen[item.ModelKey] {
				continue
			}
			seen[item.ModelKey] = true
			tiers := item.PriceTiers
			if tiers == nil {
				tiers = []PublicChannelModelPriceTier{}
			}
			models = append(models, PlatformModel{
				ID: item.ModelKey, Object: "model", Type: "model", DisplayName: firstNonEmpty(item.DisplayName, item.ModelKey),
				OwnedBy: "system", Capability: normalizeCapability(item.Capability), Endpoint: endpoint, PriceTiers: tiers,
			})
		}
	}
	return models, nil
}

// ResolvePlatformModel 按产品模型标识找到唯一渠道。模型名必须全站唯一：
// 同名出现在多个渠道时明确报错，不按价格或排序替用户选择线路。
func (s *Service) ResolvePlatformModel(user *model.User, modelKey string, endpoint string) (*PlatformModelTarget, error) {
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		return nil, BadAuthRequest("请求缺少 model")
	}
	catalog, err := s.platformCatalog(user)
	if err != nil {
		return nil, err
	}
	var matches []PlatformModelTarget
	otherEndpoint := ""
	for _, channel := range catalog.Channels {
		for _, item := range channel.Models {
			if item.ModelKey != modelKey || !item.Available {
				continue
			}
			itemEndpoint := platformEndpointForModel(item)
			if itemEndpoint != endpoint {
				otherEndpoint = firstNonEmpty(otherEndpoint, itemEndpoint)
				continue
			}
			matches = append(matches, PlatformModelTarget{Channel: &model.ModelChannel{ID: channel.ID}, Model: item})
		}
	}
	switch {
	case len(matches) > 1:
		return nil, NewAppError(409, fmt.Sprintf("模型 %s 对应多个渠道，请联系管理员处理", modelKey))
	case len(matches) == 0 && otherEndpoint != "":
		return nil, BadAuthRequest(fmt.Sprintf("模型 %s 请改用 %s 接口调用", modelKey, otherEndpoint))
	case len(matches) == 0:
		return nil, NotFound(fmt.Sprintf("模型 %s 不存在或当前账号无权使用", modelKey))
	}
	target := matches[0]
	channel, err := s.SystemChannel(target.Channel.ID)
	if err != nil {
		return nil, NotFound(fmt.Sprintf("模型 %s 不存在或当前账号无权使用", modelKey))
	}
	target.Channel = channel
	if endpoint != PlatformEndpointTasks {
		// 文本走系统渠道转发，请求体里的 model 原样发给上游；产品标识与上游模型 ID 不同时上游认不出，
		// 这里提前给出明确错误，而不是把上游的「模型不存在」透传给调用方。
		channelModel, err := s.repo.ChannelModelByKey(channel.ID, modelKey)
		if err != nil {
			return nil, NotFound(fmt.Sprintf("模型 %s 不存在或当前账号无权使用", modelKey))
		}
		if upstream := strings.TrimSpace(channelModel.ProviderModelKey); upstream != "" && upstream != modelKey {
			return nil, BadAuthRequest(fmt.Sprintf("模型 %s 的上游模型 ID 与名称不一致，开放接口暂不支持，请联系管理员", modelKey))
		}
	}
	return &target, nil
}

// EstimatePlatformInputTokens 给 Claude 的 count_tokens 一个本地估算，与预授权冻结积分用同一口径。
func EstimatePlatformInputTokens(body []byte) int64 {
	return estimatedTokens(body)
}

// CreatePlatformTask 把开放接口请求转成创作页发给 CreateTask 的同一种输入，准入、定价、冻结积分和执行全部复用任务链路。
func (s *Service) CreatePlatformTask(user *model.User, req PlatformTaskRequest, traceID, requestID string) (*PlatformTask, error) {
	target, err := s.ResolvePlatformModel(user, req.Model, PlatformEndpointTasks)
	if err != nil {
		return nil, err
	}
	createReq, err := platformTaskCreateRequest(target, req)
	if err != nil {
		return nil, err
	}
	createReq.TraceID, createReq.RequestID = traceID, requestID
	task, err := s.CreateTask(user.ID, createReq)
	if err != nil {
		return nil, err
	}
	return s.platformTaskView(user.ID, task)
}

func platformTaskCreateRequest(target *PlatformModelTarget, req PlatformTaskRequest) (CreateTaskRequest, error) {
	capability := normalizeCapability(target.Model.Capability)
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return CreateTaskRequest{}, BadAuthRequest("请填写 prompt")
	}
	images, err := platformReferenceURLs(req.Images, "images")
	if err != nil {
		return CreateTaskRequest{}, err
	}
	frames := map[string]string{}
	for role, value := range map[string]string{"first_frame": req.FirstFrame, "last_frame": req.LastFrame} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if err := validatePlatformReferenceURL(value, role); err != nil {
			return CreateTaskRequest{}, err
		}
		frames[role] = value
	}
	if len(images)+len(frames) > maxPlatformTaskReferences {
		return CreateTaskRequest{}, BadAuthRequest(fmt.Sprintf("参考素材最多 %d 个", maxPlatformTaskReferences))
	}
	config := map[string]any{"channelId": target.Channel.ID, "model": target.Model.ModelKey}
	setPlatformOption(config, "size", req.Size)
	metadata := map[string]any{"source": platformAPIMetadataSource}
	references := make([]map[string]any, 0, len(images)+len(frames))
	for index, value := range images {
		references = append(references, map[string]any{"id": fmt.Sprintf("reference-%d", index+1), "name": fmt.Sprintf("reference-%d", index+1), "url": value})
	}
	taskType, operation := "", ""
	switch capability {
	case "image":
		if len(frames) > 0 || req.Seconds != 0 || req.GenerateAudio != nil {
			return CreateTaskRequest{}, BadAuthRequest("生图不支持 first_frame、last_frame、seconds、generate_audio")
		}
		taskType, operation = "canvas_image", "image"
		setPlatformOption(config, "imageResolution", req.Resolution)
		setPlatformOption(config, "quality", req.Quality)
		// 按次计费不乘张数，开放接口和创作页一样每个任务只出一张。
		config["count"] = "1"
	case "video":
		if strings.TrimSpace(req.Quality) != "" {
			return CreateTaskRequest{}, BadAuthRequest("生视频不支持 quality，请用 resolution")
		}
		if req.Seconds < 0 {
			return CreateTaskRequest{}, BadAuthRequest("seconds 必须大于 0")
		}
		taskType = "canvas_video"
		if req.Seconds > 0 {
			config["videoSeconds"] = strconv.Itoa(req.Seconds)
		}
		setPlatformOption(config, "vquality", req.Resolution)
		if req.GenerateAudio != nil {
			config["videoGenerateAudio"] = strconv.FormatBool(*req.GenerateAudio)
		}
		for _, role := range []string{"first_frame", "last_frame"} {
			value, ok := frames[role]
			if !ok {
				continue
			}
			references = append(references, map[string]any{"id": role, "name": role, "url": value, "role": role})
			if role == "first_frame" {
				metadata["videoStartFrameNodeId"] = role
			} else {
				metadata["videoEndFrameNodeId"] = role
			}
		}
		// 与创作页的推断一致：有普通参考图走参考生视频，只有首尾帧走图生视频。
		switch {
		case len(images) > 0:
			operation = "reference_to_video"
		case len(frames) > 0:
			operation = "image_to_video"
		default:
			operation = "text_to_video"
		}
		metadata["videoEditOperation"] = operation
	default:
		return CreateTaskRequest{}, BadAuthRequest("该模型不支持任务接口")
	}
	return CreateTaskRequest{
		Type: taskType, Operation: operation, Prompt: prompt, Model: target.Model.ModelKey,
		Input: map[string]any{
			"mode": capability, "prompt": prompt, "config": config,
			"referenceImages": references, "referenceVideos": []any{}, "referenceAudios": []any{}, "metadata": metadata,
		},
	}, nil
}

func setPlatformOption(config map[string]any, key string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		config[key] = value
	}
}

func platformReferenceURLs(values []string, field string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if err := validatePlatformReferenceURL(value, field); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// validatePlatformReferenceURL 只接受公网 http(s) 地址；内嵌 base64 由任务准入统一拒绝，这里提前给出明确提示。
func validatePlatformReferenceURL(value string, field string) error {
	if len(value) > maxPlatformReferenceURLSize {
		return BadAuthRequest(field + " 地址过长")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return BadAuthRequest(field + " 只支持公网 http(s) 图片地址")
	}
	return nil
}

// PlatformTask 只返回当前用户自己的生图、生视频任务。
func (s *Service) PlatformTask(user *model.User, id string) (*PlatformTask, error) {
	if user == nil {
		return nil, Unauthorized("API Key 无效")
	}
	task, err := s.Task(user.ID, strings.TrimSpace(id))
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && task.Type != "canvas_image" && task.Type != "canvas_video") {
		return nil, NotFound("任务不存在")
	}
	if err != nil {
		// 数据库暂时不可用不能说成「任务不存在」，否则轮询方会放弃仍在执行的任务。
		return nil, err
	}
	return s.platformTaskView(user.ID, task)
}

func (s *Service) platformTaskView(userID string, task *model.Task) (*PlatformTask, error) {
	view := &PlatformTask{
		ID: task.ID, Object: "task", Type: strings.TrimPrefix(task.Type, "canvas_"), Model: task.Model, Status: string(task.Status),
		Progress: task.Progress, Error: task.Error, Outputs: []PlatformTaskOutput{}, CreatedAt: task.CreatedAt, CompletedAt: task.CompletedAt,
	}
	if task.Status != model.TaskStatusSucceeded {
		return view, nil
	}
	outputs := platformTaskResultOutputs(task.ResultJSON)
	if len(outputs) == 0 {
		return view, nil
	}
	requests := make([]ResourceAccessRequest, 0, len(outputs))
	for _, output := range outputs {
		requests = append(requests, ResourceAccessRequest{ResourceID: output.ResourceID, AccessOptions: ResourceAccessOptions{Purpose: assets.PurposeDownload}})
	}
	accesses, err := s.ResourceAccessBatch(userID, requests)
	if err != nil {
		return nil, err
	}
	for index := range outputs {
		if index >= len(accesses) || accesses[index].Access == nil {
			// 任务已成功但签不出地址时明确失败，避免调用方拿到空地址以为结果为空。
			return nil, NewAppError(503, "生成结果的下载地址暂时无法签发，请稍后重试")
		}
		outputs[index].URL = s.platformAbsoluteURL(accesses[index].Access.URL)
		outputs[index].ExpiresAt = accesses[index].Access.ExpiresAt
	}
	view.Outputs = outputs
	return view, nil
}

// platformAbsoluteURL 把本地存储签出的站内相对地址补成服务器访问地址；对象存储签出的已是完整地址。
// 未配置服务器访问地址时保留相对地址，由 HTTP 层按请求域名补全。
func (s *Service) platformAbsoluteURL(value string) string {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return value
	}
	base, err := s.publicResourceBaseURL()
	if err != nil {
		return value
	}
	reference, err := url.Parse(value)
	if err != nil {
		return value
	}
	return base.ResolveReference(reference).String()
}

// platformTaskResultOutputs 从任务结果里取出已入库的资源；地址由调用方换成带签名的下载地址。
func platformTaskResultOutputs(raw string) []PlatformTaskOutput {
	type resultMedia struct {
		ResourceID string `json:"resourceId"`
		MimeType   string `json:"mimeType"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		DurationMs int64  `json:"durationMs"`
	}
	var result struct {
		Images []resultMedia `json:"images"`
		Video  *resultMedia  `json:"video"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil
	}
	outputs := []PlatformTaskOutput{}
	add := func(kind string, media resultMedia) {
		if strings.TrimSpace(media.ResourceID) == "" {
			return
		}
		outputs = append(outputs, PlatformTaskOutput{Type: kind, ResourceID: media.ResourceID, MimeType: media.MimeType, Width: media.Width, Height: media.Height, DurationMs: media.DurationMs})
	}
	for _, image := range result.Images {
		add("image", image)
	}
	if result.Video != nil {
		add("video", *result.Video)
	}
	return outputs
}

func (s *Service) PlatformBalance(user *model.User) (*PlatformBalance, error) {
	wallet, err := s.Wallet(user, "", 1, 1)
	if err != nil {
		return nil, err
	}
	return &PlatformBalance{
		Object:                "balance",
		AvailableCredits:      float64(wallet.Account.AvailableMicrocredits) / float64(CreditScale),
		ReservedCredits:       float64(wallet.Account.ReservedMicrocredits) / float64(CreditScale),
		AvailableMicrocredits: wallet.Account.AvailableMicrocredits,
		ReservedMicrocredits:  wallet.Account.ReservedMicrocredits,
	}, nil
}
