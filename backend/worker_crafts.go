package main

import "encoding/json"

func parseWorkerCrafts(raw any) ([]string, error) {
	var allowed []string
	if raw == nil || json.Unmarshal([]byte(jsonText(raw)), &allowed) != nil || allowed == nil || len(allowed) > len(categories) {
		return nil, fail(400, "施工工艺权限须为勾选列表，未授权请提交空列表")
	}
	seen := map[string]bool{}
	for _, category := range allowed {
		if !contains(categories, category) || seen[category] {
			return nil, fail(400, "施工工艺权限包含无效或重复的大类")
		}
		seen[category] = true
	}
	return allowed, nil
}
