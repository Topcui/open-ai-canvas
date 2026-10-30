package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func TestPlatformEndpointForModel(t *testing.T) {
	for _, tc := range []struct {
		capability string
		protocol   model.ChannelInterfaceType
		want       string
	}{
		{"text", model.ChannelInterfaceClaudeAPI, PlatformEndpointMessages},
		{"text", model.ChannelInterfaceChatCompletion, PlatformEndpointChatCompletions},
		{"text", model.ChannelInterfaceOpenAIResponse, ""},
		{"image", model.ChannelInterfaceOpenAIImage, PlatformEndpointTasks},
		{"video", model.ChannelInterfaceMiniMaxVideo, PlatformEndpointTasks},
		{"audio", model.ChannelInterfaceOpenAIAudio, ""},
	} {
		if got := platformEndpointForModel(PublicChannelModel{Capability: tc.capability, Protocol: tc.protocol}); got != tc.want {
			t.Fatalf("%s/%s = %q, want %q", tc.capability, tc.protocol, got, tc.want)
		}
	}
}

// addPlatformTestModel 在 creationTestService 的夹具上追加一个已定价的渠道模型。
func addPlatformTestModel(t *testing.T, db *gorm.DB, channelID, modelID, modelKey, capability string, protocol model.ChannelInterfaceType) {
	t.Helper()
	capabilityConfig := mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel(string(protocol), modelKey))
	var channel model.ModelChannel
	if err := db.First(&channel, "id = ?", channelID).Error; err != nil {
		if err := db.Create(&model.ModelChannel{ID: channelID, Scope: model.ChannelScopeSystem, Enabled: true, Name: channelID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []any{
		&model.ChannelModel{ID: modelID, ChannelID: channelID, ModelKey: modelKey, Capability: capability, Protocol: protocol, CapabilityConfigJSON: capabilityConfig, BillingMode: "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: modelID + "-tier", ChannelModelID: modelID, SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 100, PriceConfigured: true, Enabled: true},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolvePlatformModelRequiresUniqueNameAndMatchingEndpoint(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	addPlatformTestModel(t, db, "claude-channel", "claude-model", "claude-test", "text", model.ChannelInterfaceClaudeAPI)
	addPlatformTestModel(t, db, "channel-a", "dup-a", "dup-model", "text", model.ChannelInterfaceChatCompletion)
	addPlatformTestModel(t, db, "channel-b", "dup-b", "dup-model", "text", model.ChannelInterfaceChatCompletion)
	user, err := s.repo.User("user")
	if err != nil {
		t.Fatal(err)
	}

	target, err := s.ResolvePlatformModel(user, "text-test", PlatformEndpointChatCompletions)
	if err != nil || target.Channel.ID != "channel" || target.Model.ModelKey != "text-test" {
		t.Fatalf("chat model = %+v, %v", target, err)
	}
	if target, err := s.ResolvePlatformModel(user, "claude-test", PlatformEndpointMessages); err != nil || target.Channel.ID != "claude-channel" {
		t.Fatalf("claude model = %+v, %v", target, err)
	}
	if _, err := s.ResolvePlatformModel(user, "text-test", PlatformEndpointMessages); appErrorStatus(err) != 400 || !strings.Contains(err.Error(), PlatformEndpointChatCompletions) {
		t.Fatalf("wrong endpoint should point to the right one, got %v", err)
	}
	if _, err := s.ResolvePlatformModel(user, "dup-model", PlatformEndpointChatCompletions); appErrorStatus(err) != 409 {
		t.Fatalf("duplicate model name = %v", err)
	}
	if _, err := s.ResolvePlatformModel(user, "missing-model", PlatformEndpointChatCompletions); appErrorStatus(err) != 404 {
		t.Fatalf("missing model = %v", err)
	}
	if _, err := s.ResolvePlatformModel(user, " ", PlatformEndpointChatCompletions); appErrorStatus(err) != 400 {
		t.Fatalf("empty model = %v", err)
	}
	// 系统渠道转发把请求里的 model 原样发给上游，产品标识与上游模型 ID 不同的文本模型要提前拒绝。
	addPlatformTestModel(t, db, "alias-channel", "alias-model", "alias-text", "text", model.ChannelInterfaceChatCompletion)
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "alias-model").Update("provider_model_key", "upstream-text").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePlatformModel(user, "alias-text", PlatformEndpointChatCompletions); appErrorStatus(err) != 400 || !strings.Contains(err.Error(), "上游模型 ID") {
		t.Fatalf("text model with a different upstream ID = %v", err)
	}

	models, err := s.PlatformModels(user)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]string{}
	for _, item := range models {
		if _, seen := endpoints[item.ID]; seen {
			t.Fatalf("model %s listed twice", item.ID)
		}
		endpoints[item.ID] = item.Endpoint
	}
	if endpoints["text-test"] != PlatformEndpointChatCompletions || endpoints["claude-test"] != PlatformEndpointMessages || endpoints["dup-model"] == "" {
		t.Fatalf("models = %+v", endpoints)
	}
}

func TestResolvePlatformModelHonorsUserGroupChannels(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	addPlatformTestModel(t, db, "claude-channel", "claude-model", "claude-test", "text", model.ChannelInterfaceClaudeAPI)
	for _, item := range []any{
		&model.UserGroup{ID: "group-limited", Code: "limited", Name: "limited", RatioBasisPoints: 10_000, PointsPerUSD: defaultPointsPerUSDSeed, PointsPerCNY: defaultPointsPerCNYSeed},
		&model.UserGroupChannel{GroupID: "group-limited", ChannelID: "channel"},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.User{}).Where("id = ?", "user").Update("user_group", "limited").Error; err != nil {
		t.Fatal(err)
	}
	s.invalidateUserGroups()
	user, err := s.repo.User("user")
	if err != nil {
		t.Fatal(err)
	}
	if target, err := s.ResolvePlatformModel(user, "text-test", PlatformEndpointChatCompletions); err != nil || target.Channel.ID != "channel" {
		t.Fatalf("model in the group's channel = %+v, %v", target, err)
	}
	// 分组没绑定的渠道和不存在的模型给同样的 404，不暴露站上有哪些模型。
	if _, err := s.ResolvePlatformModel(user, "claude-test", PlatformEndpointMessages); appErrorStatus(err) != 404 {
		t.Fatalf("model outside the user's group = %v", err)
	}
	models, err := s.PlatformModels(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range models {
		if item.ID == "claude-test" {
			t.Fatalf("model outside the user's group must not be listed: %+v", models)
		}
	}
}

func TestPlatformTaskCreateRequestBuildsCreatePageInput(t *testing.T) {
	image := &PlatformModelTarget{Channel: &model.ModelChannel{ID: "image-channel"}, Model: PublicChannelModel{ModelKey: "image-model", Capability: "image"}}
	req, err := platformTaskCreateRequest(image, PlatformTaskRequest{Model: "image-model", Prompt: " 一只猫 ", Size: "1:1", Resolution: "2K", Images: []string{"https://example.com/a.png", " "}})
	if err != nil {
		t.Fatal(err)
	}
	config := req.Input["config"].(map[string]any)
	references := req.Input["referenceImages"].([]map[string]any)
	if req.Type != "canvas_image" || req.Operation != "image" || req.Prompt != "一只猫" || config["channelId"] != "image-channel" || config["size"] != "1:1" || config["imageResolution"] != "2K" || config["count"] != "1" {
		t.Fatalf("image request = %+v config=%+v", req, config)
	}
	if len(references) != 1 || references[0]["url"] != "https://example.com/a.png" {
		t.Fatalf("image references = %+v", references)
	}

	video := &PlatformModelTarget{Channel: &model.ModelChannel{ID: "video-channel"}, Model: PublicChannelModel{ModelKey: "video-model", Capability: "video"}}
	audio := true
	for _, tc := range []struct {
		name      string
		request   PlatformTaskRequest
		operation string
	}{
		{"text", PlatformTaskRequest{Prompt: "海浪", Seconds: 5, Resolution: "720p", GenerateAudio: &audio}, "text_to_video"},
		{"frames", PlatformTaskRequest{Prompt: "海浪", FirstFrame: "https://example.com/first.png", LastFrame: "https://example.com/last.png"}, "image_to_video"},
		{"references", PlatformTaskRequest{Prompt: "海浪", Images: []string{"https://example.com/ref.png"}, FirstFrame: "https://example.com/first.png"}, "reference_to_video"},
	} {
		req, err := platformTaskCreateRequest(video, tc.request)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		metadata := req.Input["metadata"].(map[string]any)
		if req.Type != "canvas_video" || req.Operation != tc.operation || metadata["videoEditOperation"] != tc.operation {
			t.Fatalf("%s: operation = %s metadata=%+v", tc.name, req.Operation, metadata)
		}
		if tc.name == "text" {
			config := req.Input["config"].(map[string]any)
			if config["videoSeconds"] != "5" || config["vquality"] != "720p" || config["videoGenerateAudio"] != "true" {
				t.Fatalf("video config = %+v", config)
			}
		}
		if tc.name == "frames" {
			references := req.Input["referenceImages"].([]map[string]any)
			if len(references) != 2 || references[0]["role"] != "first_frame" || references[1]["role"] != "last_frame" || metadata["videoStartFrameNodeId"] != "first_frame" || metadata["videoEndFrameNodeId"] != "last_frame" {
				t.Fatalf("frame references = %+v metadata=%+v", references, metadata)
			}
		}
	}

	for name, tc := range map[string]struct {
		target  *PlatformModelTarget
		request PlatformTaskRequest
	}{
		"missing prompt":     {image, PlatformTaskRequest{}},
		"inline image":       {image, PlatformTaskRequest{Prompt: "x", Images: []string{"data:image/png;base64,AAAA"}}},
		"relative image":     {image, PlatformTaskRequest{Prompt: "x", Images: []string{"/api/resources/1/file"}}},
		"frames on image":    {image, PlatformTaskRequest{Prompt: "x", FirstFrame: "https://example.com/a.png"}},
		"seconds on image":   {image, PlatformTaskRequest{Prompt: "x", Seconds: 5}},
		"quality on video":   {video, PlatformTaskRequest{Prompt: "x", Quality: "high"}},
		"negative seconds":   {video, PlatformTaskRequest{Prompt: "x", Seconds: -1}},
		"too many reference": {image, PlatformTaskRequest{Prompt: "x", Images: strings.Split(strings.TrimSuffix(strings.Repeat("https://example.com/a.png,", maxPlatformTaskReferences+1), ","), ",")}},
	} {
		if _, err := platformTaskCreateRequest(tc.target, tc.request); appErrorStatus(err) != 400 {
			t.Fatalf("%s should be rejected, got %v", name, err)
		}
	}
}

func TestCreatePlatformTaskUsesTaskAdmissionAndBilling(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	// Grok 图片的默认能力不要求显式像素映射，服务端默认值即可通过任务准入。
	addPlatformTestModel(t, db, "image-channel", "image-model", "image-test", "image", model.ChannelInterfaceGrokImage)
	user, err := s.repo.User("user")
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreatePlatformTask(user, PlatformTaskRequest{Model: "image-test", Prompt: "一只橘猫"}, "trace", "request")
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != "image" || created.Model != "image-test" || created.Status != string(model.TaskStatusQueued) || len(created.Outputs) != 0 {
		t.Fatalf("created task = %+v", created)
	}
	task, err := s.repo.Task(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	metadata, _ := input["metadata"].(map[string]any)
	if task.BillingOrderID == "" || metadata["source"] != platformAPIMetadataSource {
		t.Fatalf("task must be billed like a page task: billing=%q metadata=%+v", task.BillingOrderID, metadata)
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user").Error; err != nil || account.ReservedMicrocredits != 100 {
		t.Fatalf("credits not reserved: %+v %v", account, err)
	}

	if _, err := s.CreatePlatformTask(user, PlatformTaskRequest{Model: "text-test", Prompt: "x"}, "", ""); appErrorStatus(err) != 400 {
		t.Fatalf("text model on task endpoint = %v", err)
	}
	if view, err := s.PlatformTask(user, created.ID); err != nil || view.ID != created.ID {
		t.Fatalf("read own task = %+v, %v", view, err)
	}
	if _, err := s.PlatformTask(&model.User{ID: "someone-else"}, created.ID); appErrorStatus(err) != 404 {
		t.Fatalf("other user's task = %v", err)
	}
}

func TestPlatformTaskResultOutputs(t *testing.T) {
	outputs := platformTaskResultOutputs(`{"mode":"image","images":[{"resourceId":"img-1","mimeType":"image/png","width":1024,"height":1024},{"storageKey":"resource:missing-id"}]}`)
	if len(outputs) != 1 || outputs[0].Type != "image" || outputs[0].ResourceID != "img-1" || outputs[0].Width != 1024 {
		t.Fatalf("image outputs = %+v", outputs)
	}
	outputs = platformTaskResultOutputs(`{"mode":"video","video":{"resourceId":"vid-1","mimeType":"video/mp4","durationMs":5000}}`)
	if len(outputs) != 1 || outputs[0].Type != "video" || outputs[0].DurationMs != 5000 {
		t.Fatalf("video outputs = %+v", outputs)
	}
	if outputs := platformTaskResultOutputs("not json"); len(outputs) != 0 {
		t.Fatalf("invalid result = %+v", outputs)
	}
}
