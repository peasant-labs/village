package handler

import (
	"fmt"
	"strings"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
)

func titleContext(req schema.PublishRequest) redact.TitleContext {
	return redact.TitleContext{Harness: req.Model.Harness, ProjectPath: req.Project.FilePath}
}

func harnessSessionTitle(h schema.Harness) string {
	return schema.HarnessDisplayName(h) + " session"
}

func (h *Handler) sanitizeGeneratedTitle(req *schema.PublishRequest) {
	fallback := harnessSessionTitle(req.Model.Harness)
	candidate := ""
	if req.Quality != nil && req.Quality.TitleGenerated != nil {
		candidate = *req.Quality.TitleGenerated
	}
	safe := fallback
	if candidate != "" && h.titles != nil {
		if result, err := h.titles.Sanitize(candidate, titleContext(*req)); err == nil && result.Text != "" {
			safe = result.Text
		}
	}
	if req.Quality == nil {
		req.Quality = &schema.QualityMetrics{}
	}
	req.Quality.TitleGenerated = &safe
}

func titleValidationMessage(categories []redact.CategoryString) string {
	labels := make([]string, len(categories))
	for i, category := range categories {
		labels[i] = string(category)
	}
	return fmt.Sprintf("title update rejected during PATCH title validation because sensitive categories were detected (%s); no transcript row changed; remove or replace the sensitive content and retry", strings.Join(labels, ", "))
}
