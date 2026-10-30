package service

import (
	"yingce/backend/internal/app"
	"yingce/backend/internal/model"
)

type UserAPIKey = model.UserAPIKey
type CreateUserAPIKeyRequest = app.CreateUserAPIKeyRequest
type CreatedUserAPIKey = app.CreatedUserAPIKey

type PlatformModel = app.PlatformModel
type PlatformModelTarget = app.PlatformModelTarget
type PlatformTaskRequest = app.PlatformTaskRequest
type PlatformTask = app.PlatformTask
type PlatformTaskOutput = app.PlatformTaskOutput
type PlatformBalance = app.PlatformBalance

const (
	UserAPIKeyPrefix                = app.UserAPIKeyPrefix
	MaxUserAPIKeys                  = app.MaxUserAPIKeys
	PlatformEndpointMessages        = app.PlatformEndpointMessages
	PlatformEndpointChatCompletions = app.PlatformEndpointChatCompletions
	PlatformEndpointTasks           = app.PlatformEndpointTasks
)

func EstimatePlatformInputTokens(body []byte) int64 {
	return app.EstimatePlatformInputTokens(body)
}
