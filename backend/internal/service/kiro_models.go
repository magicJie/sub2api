package service

import (
	"sort"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/kiro"
)

// KiroAvailableModels 返回 Kiro 账号在管理后台可选的模型列表：
// 未配置显式映射时返回内置模型，否则按映射键列出（已知模型保留展示名）。
func KiroAvailableModels(account *Account) []kiropkg.Model {
	hasExplicitMapping := false
	if account != nil {
		switch rawMapping := account.Credentials["model_mapping"].(type) {
		case map[string]any:
			hasExplicitMapping = len(rawMapping) > 0
		case map[string]string:
			hasExplicitMapping = len(rawMapping) > 0
		}
	}
	if !hasExplicitMapping {
		return kiropkg.DefaultModels
	}

	defaultsByID := make(map[string]kiropkg.Model, len(kiropkg.DefaultModels))
	for _, model := range kiropkg.DefaultModels {
		defaultsByID[model.ID] = model
	}
	mapping := account.GetModelMapping()
	requested := make([]string, 0, len(mapping))
	for id := range mapping {
		requested = append(requested, id)
	}
	sort.Strings(requested)

	models := make([]kiropkg.Model, 0, len(requested))
	for _, id := range requested {
		if model, ok := defaultsByID[id]; ok {
			models = append(models, model)
			continue
		}
		models = append(models, kiropkg.Model{ID: id, Type: "model", DisplayName: id})
	}
	return models
}

// KiroDefaultModelIDs 返回 Kiro 内置模型 ID 列表（/v1/models 默认值）。
func KiroDefaultModelIDs() []string {
	ids := make([]string, 0, len(kiropkg.DefaultModels))
	for _, model := range kiropkg.DefaultModels {
		ids = append(ids, model.ID)
	}
	return ids
}
